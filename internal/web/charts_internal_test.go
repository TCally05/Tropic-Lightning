package web

import (
	"testing"

	"github.com/defenseunicorns/keycloak-portal/internal/dataset"
)

func rows(fs ...map[string]string) []dataset.Row {
	out := make([]dataset.Row, len(fs))
	for i, f := range fs {
		out[i] = dataset.Row{ID: "r", Fields: f}
	}
	return out
}

func TestAggregate(t *testing.T) {
	rs := rows(
		map[string]string{"hrs": "10"},
		map[string]string{"hrs": "20"},
		map[string]string{"hrs": "x"}, // non-numeric, ignored by sum/avg/min/max
	)
	cases := []struct {
		agg  string
		want float64
	}{
		{"count", 3}, // counts all rows
		{"sum", 30},
		{"avg", 15}, // 30 / 2 numeric
		{"min", 10},
		{"max", 20},
	}
	for _, c := range cases {
		if got := aggregate(rs, "hrs", c.agg); got != c.want {
			t.Errorf("aggregate %s = %v, want %v", c.agg, got, c.want)
		}
	}
	// empty value column falls back to count.
	if got := aggregate(rs, "", "sum"); got != 3 {
		t.Errorf("aggregate sum with no column = %v, want 3 (count)", got)
	}
}

func TestComputeBars(t *testing.T) {
	rs := rows(
		map[string]string{"base": "Hill", "hrs": "100"},
		map[string]string{"base": "Hill", "hrs": "50"},
		map[string]string{"base": "Ramstein", "hrs": "30"},
	)
	// count: Hill=2 (biggest, full bar), Ramstein=1.
	bars := computeBars(rs, "base", "", "count")
	if len(bars) != 2 || bars[0].Label != "Hill" || bars[0].Display != "2" || bars[0].BarPct != 100 {
		t.Fatalf("count bars = %+v", bars)
	}
	if bars[1].Label != "Ramstein" || bars[1].BarPct != 50 {
		t.Errorf("second bar = %+v", bars[1])
	}
	// sum of hrs: Hill=150, Ramstein=30.
	sum := computeBars(rs, "base", "hrs", "sum")
	if sum[0].Display != "150" || sum[1].Display != "30" {
		t.Errorf("sum bars = %+v", sum)
	}
	if computeBars(rs, "", "", "count") != nil {
		t.Error("no group-by should yield no bars")
	}
}

func TestComputeStats(t *testing.T) {
	rs := rows(
		map[string]string{"hrs": "10"},
		map[string]string{"hrs": "30"},
		map[string]string{"hrs": ""},
	)
	st := computeStats(rs, "hrs")
	if st.Count != 3 || st.Numeric != 2 || st.Sum != "40" || st.Avg != "20" || st.Min != "10" || st.Max != "30" {
		t.Errorf("stats = %+v", st)
	}
	// no value column -> only count.
	if st := computeStats(rs, ""); st.Count != 3 || st.Sum != "" {
		t.Errorf("no-col stats = %+v", st)
	}
}

func TestComputeLine(t *testing.T) {
	rs := rows(
		map[string]string{"x": "3", "y": "9"},
		map[string]string{"x": "1", "y": "1"},
		map[string]string{"x": "2", "y": "4"},
	)
	// numeric x is sorted ascending; avg of y per x.
	line, ok := computeLine(rs, "x", "y", "avg")
	if !ok || len(line.Points) != 3 {
		t.Fatalf("line ok=%v points=%d", ok, len(line.Points))
	}
	if line.Points[0].X != "1" || line.Points[2].X != "3" {
		t.Errorf("x not sorted: %v .. %v", line.Points[0].X, line.Points[2].X)
	}
	if line.YMin != "1" || line.YMax != "9" {
		t.Errorf("y range = %s..%s, want 1..9", line.YMin, line.YMax)
	}
	if _, ok := computeLine(rs, "", "y", "avg"); ok {
		t.Error("no x column should yield no line")
	}
}

func TestFormatNum(t *testing.T) {
	for in, want := range map[float64]string{10: "10", 15.5: "15.50", 0: "0"} {
		if got := formatNum(in); got != want {
			t.Errorf("formatNum(%v) = %q, want %q", in, got, want)
		}
	}
}
