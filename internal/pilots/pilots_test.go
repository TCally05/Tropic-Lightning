package pilots

import (
	"context"
	"testing"

	"github.com/defenseunicorns/keycloak-portal/internal/datasource"
)

func TestParseDataset(t *testing.T) {
	ps, err := ParseDataset()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ps) < 100 {
		t.Fatalf("expected many pilots, got %d", len(ps))
	}
	p := ps[0]
	if p.PilotID == "" {
		t.Error("expected a pilot_id")
	}
	if p.Age <= 0 || p.FlightHoursTotal <= 0 {
		t.Errorf("numeric fields not parsed: %+v", p)
	}
	if len(p.PhaLastDate) != 10 { // YYYY-MM-DD, time component stripped
		t.Errorf("pha_last_date not normalized: %q", p.PhaLastDate)
	}
}

func TestImportPopulatesStoreAndCatalog(t *testing.T) {
	store := NewMemoryStore()
	catalog := datasource.NewService(datasource.NewMemoryStore())
	svc := NewService(store, catalog, nil)
	ctx := context.Background()

	n, err := svc.Import(ctx)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n < 100 {
		t.Fatalf("imported %d, want many", n)
	}
	got, _ := store.Count(ctx)
	if got != n {
		t.Errorf("store count = %d, want %d", got, n)
	}

	// A catalog data-source entry was registered, and re-import is idempotent.
	ds, _ := catalog.List(ctx)
	if len(ds) != 1 {
		t.Fatalf("catalog entries = %d, want 1", len(ds))
	}
	if _, err := svc.Import(ctx); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	ds, _ = catalog.List(ctx)
	if len(ds) != 1 {
		t.Errorf("catalog entries after re-import = %d, want 1 (idempotent)", len(ds))
	}
}

func TestSetStatusAndSummary(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, nil)
	ctx := context.Background()
	if _, err := svc.Import(ctx); err != nil {
		t.Fatalf("import: %v", err)
	}

	before, _ := svc.ReadinessSummary(ctx)
	if before.Total == 0 || before.Available+before.Grounded != before.Total {
		t.Fatalf("summary inconsistent: %+v", before)
	}

	// Find an available pilot and ground them.
	all, _ := svc.List(ctx)
	var target string
	for _, p := range all {
		if p.Available() {
			target = p.PilotID
			break
		}
	}
	if target == "" {
		t.Skip("no available pilot to ground")
	}
	if _, err := svc.SetStatus(ctx, target, StatusGrounded, "sick", "s1"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	after, _ := svc.ReadinessSummary(ctx)
	if after.Available != before.Available-1 {
		t.Errorf("available = %d, want %d", after.Available, before.Available-1)
	}
	got, _ := store.Get(ctx, target)
	if got.Available() || got.StatusBy != "s1" || got.StatusNote != "sick" {
		t.Errorf("grounded pilot = %+v", got)
	}

	// Invalid status rejected.
	if _, err := svc.SetStatus(ctx, target, "bogus", "", "s1"); err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestSummaryAvailablePct(t *testing.T) {
	s := Summary{Total: 4, Available: 3, Grounded: 1}
	if s.AvailablePct() != 75 {
		t.Errorf("pct = %d, want 75", s.AvailablePct())
	}
	if (Summary{}).AvailablePct() != 0 {
		t.Error("empty summary pct should be 0")
	}
}

func TestBrowseFiltersAndFacets(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	seed := []Pilot{
		{PilotID: "P1", Base: "Hill AFB", Aircraft: "F-16", Rank: "O-2", MissionStatus: StatusAvailable},
		{PilotID: "P2", Base: "Hill AFB", Aircraft: "C-17", Rank: "O-3", MissionStatus: StatusGrounded},
		{PilotID: "P3", Base: "Nellis AFB", Aircraft: "F-16", Rank: "O-2", MissionStatus: StatusAvailable},
	}
	for _, p := range seed {
		_ = store.Put(ctx, p)
	}
	svc := NewService(store, nil, nil)

	// No filter: all 3, facets list distinct values.
	all, err := svc.Browse(ctx, Filter{})
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if all.GrandTotal != 3 || all.Summary.Total != 3 {
		t.Fatalf("unfiltered = %+v", all.Summary)
	}
	if len(all.Facets.Bases) != 2 || len(all.Facets.Aircraft) != 2 {
		t.Errorf("facets = %+v", all.Facets)
	}

	// Filter by base: only Hill AFB (2), summary reflects subset.
	hill, _ := svc.Browse(ctx, Filter{Base: "Hill AFB"})
	if hill.Summary.Total != 2 || hill.GrandTotal != 3 {
		t.Errorf("hill summary = %+v (grand %d)", hill.Summary, hill.GrandTotal)
	}
	if hill.Summary.Available != 1 || hill.Summary.Grounded != 1 {
		t.Errorf("hill availability = %+v", hill.Summary)
	}

	// Combined filter: Hill AFB + F-16 -> P1 only.
	combo, _ := svc.Browse(ctx, Filter{Base: "Hill AFB", Aircraft: "F-16"})
	if combo.Summary.Total != 1 || combo.Pilots[0].PilotID != "P1" {
		t.Errorf("combo = %+v", combo.Pilots)
	}

	// Free-text search.
	q, _ := svc.Browse(ctx, Filter{Query: "nellis"})
	if q.Summary.Total != 1 || q.Pilots[0].PilotID != "P3" {
		t.Errorf("query = %+v", q.Pilots)
	}
}
