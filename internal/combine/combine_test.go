package combine

import (
	"context"
	"testing"

	"github.com/defenseunicorns/keycloak-portal/internal/dataset"
)

type fakeReader struct {
	cols map[string][]string
	rows map[string][]dataset.Row
}

func (f fakeReader) View(_ context.Context, c string) (string, []string, []dataset.Row, error) {
	return c, f.cols[c], f.rows[c], nil
}

func row(fields map[string]string) dataset.Row { return dataset.Row{ID: "r", Fields: fields} }

func newSvc() (*Service, context.Context) {
	r := fakeReader{
		cols: map[string][]string{
			"pilots":  {"id", "base"},
			"weather": {"base", "temp"},
		},
		rows: map[string][]dataset.Row{
			"pilots": {
				row(map[string]string{"id": "1", "base": "Hill"}),
				row(map[string]string{"id": "2", "base": "Hill"}),
				row(map[string]string{"id": "3", "base": "Edwards"}), // no weather match
			},
			"weather": {
				row(map[string]string{"base": "Hill", "temp": "31"}),
				row(map[string]string{"base": "Ramstein", "temp": "19"}),
			},
		},
	}
	return NewService(NewMemoryStore(), r), context.Background()
}

func TestJoinCompute(t *testing.T) {
	svc, ctx := newSvc()
	c, err := svc.Create(ctx, Input{
		Name:  "Pilots+Weather",
		Left:  Member{Collection: "pilots", Key: "base"},
		Right: Member{Collection: "weather", Key: "base"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Key != "cmb_pilotsweather" {
		t.Errorf("key = %q", c.Key)
	}

	name, cols, rows, err := svc.Compute(ctx, c.Key)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if name != "Pilots+Weather" {
		t.Errorf("name = %q", name)
	}
	// Columns: left (id, base) + right extra (temp); right key 'base' dropped.
	if len(cols) != 3 || cols[0] != "id" || cols[1] != "base" || cols[2] != "temp" {
		t.Fatalf("cols = %v", cols)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d (left-join keeps all left rows)", len(rows))
	}
	// Matched rows get the base's temp; unmatched left row gets blank.
	if rows[0].Fields["temp"] != "31" || rows[1].Fields["temp"] != "31" {
		t.Errorf("Hill rows temp = %q,%q want 31", rows[0].Fields["temp"], rows[1].Fields["temp"])
	}
	if rows[2].Fields["base"] != "Edwards" || rows[2].Fields["temp"] != "" {
		t.Errorf("unmatched row = %+v, want blank temp", rows[2].Fields)
	}
}

func TestJoinColumnCollisionRenamed(t *testing.T) {
	r := fakeReader{
		cols: map[string][]string{
			"a": {"base", "status"},
			"b": {"base", "status"}, // 'status' collides with left
		},
		rows: map[string][]dataset.Row{
			"a": {row(map[string]string{"base": "Hill", "status": "ready"})},
			"b": {row(map[string]string{"base": "Hill", "status": "stormy"})},
		},
	}
	svc := NewService(NewMemoryStore(), r)
	ctx := context.Background()
	c, _ := svc.Create(ctx, Input{Name: "AB", Left: Member{Collection: "a", Key: "base"}, Right: Member{Collection: "b", Name: "Wx", Key: "base"}})
	_, cols, rows, err := svc.Compute(ctx, c.Key)
	if err != nil {
		t.Fatal(err)
	}
	// left status stays; right status renamed to wx_status.
	if !containsCol(cols, "status") || !containsCol(cols, "wx_status") {
		t.Fatalf("cols = %v, want status + wx_status", cols)
	}
	if rows[0].Fields["status"] != "ready" || rows[0].Fields["wx_status"] != "stormy" {
		t.Errorf("row = %+v", rows[0].Fields)
	}
}

func TestCreateValidation(t *testing.T) {
	svc, ctx := newSvc()
	if _, err := svc.Create(ctx, Input{Name: "", Left: Member{Collection: "pilots", Key: "base"}, Right: Member{Collection: "weather", Key: "base"}}); err == nil {
		t.Error("expected error for empty name")
	}
	if _, err := svc.Create(ctx, Input{Name: "x", Left: Member{Collection: "pilots", Key: "base"}, Right: Member{Collection: "pilots", Key: "base"}}); err == nil {
		t.Error("expected error for same source twice")
	}
	if _, err := svc.Create(ctx, Input{Name: "x", Left: Member{Collection: "pilots", Key: "nope"}, Right: Member{Collection: "weather", Key: "base"}}); err == nil {
		t.Error("expected error for missing left key column")
	}
}

func containsCol(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
