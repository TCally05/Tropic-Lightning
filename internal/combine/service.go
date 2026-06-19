package combine

import (
	"context"
	"fmt"
	"strings"

	"github.com/defenseunicorns/keycloak-portal/internal/dataset"
)

// ValidationError is a user-facing input error.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

// DatasetReader reads a dataset's name, columns, and rows. dataset.Service
// satisfies it.
type DatasetReader interface {
	View(ctx context.Context, collection string) (string, []string, []dataset.Row, error)
}

// Service creates combined sources and computes their joined rows on demand.
type Service struct {
	store  Store
	reader DatasetReader
}

// NewService wires the combine service.
func NewService(store Store, reader DatasetReader) *Service {
	return &Service{store: store, reader: reader}
}

// Input is the create form for a combined source.
type Input struct {
	Name        string
	Owner       string
	Left        Member
	Right       Member
	OnlyMatched bool
}

// Result is a computed join: the columns, rows, and match statistics.
type Result struct {
	Name      string
	Columns   []string
	Rows      []dataset.Row
	Matched   int // left rows that found a right match
	Unmatched int // left rows with no match
	Total     int // total left rows considered
}

// Create validates the spec (members exist, keys are real columns) and persists
// it. The result is virtual — rows are computed at view time by Compute.
func (s *Service) Create(ctx context.Context, in Input) (Combined, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Combined{}, ValidationError{"a name is required"}
	}
	if in.Left.Collection == "" || in.Right.Collection == "" {
		return Combined{}, ValidationError{"pick two data sources to combine"}
	}
	if in.Left.Collection == in.Right.Collection {
		return Combined{}, ValidationError{"pick two different data sources"}
	}
	lname, lcols, _, err := s.reader.View(ctx, in.Left.Collection)
	if err != nil {
		return Combined{}, fmt.Errorf("reading first source: %w", err)
	}
	rname, rcols, _, err := s.reader.View(ctx, in.Right.Collection)
	if err != nil {
		return Combined{}, fmt.Errorf("reading second source: %w", err)
	}
	if !contains(lcols, in.Left.Key) {
		return Combined{}, ValidationError{"the first source has no column " + in.Left.Key}
	}
	if !contains(rcols, in.Right.Key) {
		return Combined{}, ValidationError{"the second source has no column " + in.Right.Key}
	}
	if in.Left.Name == "" {
		in.Left.Name = lname
	}
	if in.Right.Name == "" {
		in.Right.Name = rname
	}
	c := Combined{
		Key:         "cmb_" + slug(in.Name),
		Name:        in.Name,
		Owner:       in.Owner,
		Left:        in.Left,
		Right:       in.Right,
		OnlyMatched: in.OnlyMatched,
	}
	if err := s.store.Put(ctx, c); err != nil {
		return Combined{}, err
	}
	return c, nil
}

// Preview computes a join from an unsaved spec (for the builder's live preview).
func (s *Service) Preview(ctx context.Context, in Input) (Result, error) {
	if in.Left.Collection == "" || in.Right.Collection == "" || in.Left.Key == "" || in.Right.Key == "" {
		return Result{}, ValidationError{"pick both sources and a key column in each"}
	}
	return s.computeFor(ctx, Combined{
		Name: firstNonEmpty(in.Name, "Preview"), Left: in.Left, Right: in.Right, OnlyMatched: in.OnlyMatched,
	})
}

// List returns all combined sources.
func (s *Service) List(ctx context.Context) ([]Combined, error) { return s.store.List(ctx) }

// Get returns a combined source by key.
func (s *Service) Get(ctx context.Context, key string) (Combined, bool, error) {
	return s.store.Get(ctx, key)
}

// Delete removes a combined source.
func (s *Service) Delete(ctx context.Context, key string) error { return s.store.Delete(ctx, key) }

// Compute performs the join for a saved combined source.
func (s *Service) Compute(ctx context.Context, key string) (Result, error) {
	c, ok, err := s.store.Get(ctx, key)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, ErrNotFound
	}
	return s.computeFor(ctx, c)
}

// computeFor left-joins Left with Right on their keys (forgiving match). Right is
// reduced to one row per normalized key value (first wins); right columns that
// collide with a left column are prefixed with the right source's name. Reports
// match statistics; drops unmatched rows when OnlyMatched is set.
func (s *Service) computeFor(ctx context.Context, c Combined) (Result, error) {
	_, lcols, lrows, err := s.reader.View(ctx, c.Left.Collection)
	if err != nil {
		return Result{}, fmt.Errorf("reading %q: %w", c.Left.Name, err)
	}
	_, rcols, rrows, err := s.reader.View(ctx, c.Right.Collection)
	if err != nil {
		return Result{}, fmt.Errorf("reading %q: %w", c.Right.Name, err)
	}

	// Right lookup keyed by the normalized join value (first row wins).
	lookup := make(map[string]map[string]string, len(rrows))
	for _, rr := range rrows {
		k := normKey(rr.Fields[c.Right.Key])
		if _, exists := lookup[k]; !exists {
			lookup[k] = rr.Fields
		}
	}

	// Right columns to append (drop the join key; rename on collision).
	leftSet := map[string]bool{}
	for _, col := range lcols {
		leftSet[col] = true
	}
	type rcol struct{ src, disp string }
	var rextra []rcol
	cols := append([]string{}, lcols...)
	for _, col := range rcols {
		if col == c.Right.Key {
			continue
		}
		disp := col
		if leftSet[disp] {
			disp = slug(c.Right.Name) + "_" + col
		}
		rextra = append(rextra, rcol{src: col, disp: disp})
		cols = append(cols, disp)
	}

	res := Result{Name: c.Name, Columns: cols, Total: len(lrows)}
	res.Rows = make([]dataset.Row, 0, len(lrows))
	for _, lr := range lrows {
		match := lookup[normKey(lr.Fields[c.Left.Key])]
		if match != nil {
			res.Matched++
		} else {
			res.Unmatched++
			if c.OnlyMatched {
				continue
			}
		}
		f := make(map[string]string, len(lr.Fields)+len(rextra))
		for k, v := range lr.Fields {
			f[k] = v
		}
		for _, e := range rextra {
			if match != nil {
				f[e.disp] = match[e.src]
			} else {
				f[e.disp] = ""
			}
		}
		res.Rows = append(res.Rows, dataset.Row{ID: lr.ID, Fields: f})
	}
	return res, nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
