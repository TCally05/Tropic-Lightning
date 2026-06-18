package web

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/defenseunicorns/keycloak-portal/internal/dataset"
)

// parseNum parses a cell as a float (trimmed). ok is false for non-numeric cells.
func parseNum(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// formatNum renders an aggregate: integers without decimals, else 2 places.
func formatNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// aggregate reduces a group of rows to one number. "count" (or no value column)
// counts rows; sum/avg/min/max operate over numeric cells in valueCol.
func aggregate(rows []dataset.Row, valueCol, agg string) float64 {
	if agg == "count" || agg == "" || valueCol == "" {
		return float64(len(rows))
	}
	var sum, min, max float64
	n := 0
	for _, r := range rows {
		v, ok := parseNum(r.Fields[valueCol])
		if !ok {
			continue
		}
		if n == 0 || v < min {
			min = v
		}
		if n == 0 || v > max {
			max = v
		}
		sum += v
		n++
	}
	switch agg {
	case "sum":
		return sum
	case "avg":
		if n == 0 {
			return 0
		}
		return sum / float64(n)
	case "min":
		return min
	case "max":
		return max
	}
	return float64(len(rows))
}

// groupBy buckets rows by a column value (blank -> "(blank)"), returning the
// distinct keys and the bucket map.
func groupByCol(rows []dataset.Row, col string) ([]string, map[string][]dataset.Row) {
	buckets := map[string][]dataset.Row{}
	var keys []string
	for _, r := range rows {
		k := strings.TrimSpace(r.Fields[col])
		if k == "" {
			k = "(blank)"
		}
		if _, ok := buckets[k]; !ok {
			keys = append(keys, k)
		}
		buckets[k] = append(buckets[k], r)
	}
	return keys, buckets
}

// barVM is one bar: a category, its aggregated value, and bar width (% of max).
type barVM struct {
	Label   string
	Display string
	BarPct  float64
	Color   string
}

// computeBars aggregates rows per category, largest first.
func computeBars(rows []dataset.Row, groupBy, valueCol, agg string) []barVM {
	if groupBy == "" {
		return nil
	}
	keys, buckets := groupByCol(rows, groupBy)
	type entry struct {
		label string
		value float64
	}
	entries := make([]entry, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, entry{k, aggregate(buckets[k], valueCol, agg)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].value != entries[j].value {
			return entries[i].value > entries[j].value
		}
		return entries[i].label < entries[j].label
	})
	max := 0.0
	for _, e := range entries {
		if e.value > max {
			max = e.value
		}
	}
	bars := make([]barVM, 0, len(entries))
	for i, e := range entries {
		pct := 0.0
		if max > 0 {
			pct = math.Round(e.value/max*1000) / 10
		}
		bars = append(bars, barVM{Label: e.label, Display: formatNum(e.value), BarPct: pct, Color: wheelPalette[i%len(wheelPalette)]})
	}
	return bars
}

// statsVM is the summary-stats KPI set for a numeric column.
type statsVM struct {
	Count   int // total rows
	Numeric int // rows with a numeric value
	Sum     string
	Avg     string
	Min     string
	Max     string
}

// computeStats summarizes a numeric column across rows.
func computeStats(rows []dataset.Row, valueCol string) statsVM {
	st := statsVM{Count: len(rows)}
	if valueCol == "" {
		return st
	}
	var sum, min, max float64
	for _, r := range rows {
		v, ok := parseNum(r.Fields[valueCol])
		if !ok {
			continue
		}
		if st.Numeric == 0 || v < min {
			min = v
		}
		if st.Numeric == 0 || v > max {
			max = v
		}
		sum += v
		st.Numeric++
	}
	st.Sum = formatNum(sum)
	st.Min = formatNum(min)
	st.Max = formatNum(max)
	if st.Numeric > 0 {
		st.Avg = formatNum(sum / float64(st.Numeric))
	} else {
		st.Sum, st.Min, st.Max, st.Avg = "—", "—", "—", "—"
	}
	return st
}

// linePointVM is a plotted point with its svg coords and labels.
type linePointVM struct {
	CX, CY float64
	X      string
	YDisp  string
}

// lineVM is a line chart laid out in an SVG viewBox.
type lineVM struct {
	W, H     float64
	Polyline string // "x1,y1 x2,y2 ..."
	Points   []linePointVM
	YMin     string
	YMax     string
}

// computeLine aggregates a value per sorted x-category and lays out an SVG line.
func computeLine(rows []dataset.Row, xCol, valueCol, agg string) (lineVM, bool) {
	if xCol == "" {
		return lineVM{}, false
	}
	keys, buckets := groupByCol(rows, xCol)
	if len(keys) == 0 {
		return lineVM{}, false
	}
	// Sort x numerically when every key is a number, else lexically.
	allNum := true
	for _, k := range keys {
		if _, ok := parseNum(k); !ok {
			allNum = false
			break
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if allNum {
			a, _ := parseNum(keys[i])
			b, _ := parseNum(keys[j])
			return a < b
		}
		return keys[i] < keys[j]
	})

	ys := make([]float64, len(keys))
	yMin, yMax := math.Inf(1), math.Inf(-1)
	for i, k := range keys {
		ys[i] = aggregate(buckets[k], valueCol, agg)
		yMin = math.Min(yMin, ys[i])
		yMax = math.Max(yMax, ys[i])
	}
	if yMin == yMax { // flat line: pad so it sits mid-plot
		yMin -= 1
		yMax += 1
	}

	const w, h = 640.0, 220.0
	padL, padR, padT, padB := 8.0, 8.0, 12.0, 18.0
	plotW, plotH := w-padL-padR, h-padT-padB
	vm := lineVM{W: w, H: h, YMin: formatNum(minOf(ys)), YMax: formatNum(maxOf(ys))}
	var b strings.Builder
	for i, k := range keys {
		cx := padL
		if len(keys) > 1 {
			cx = padL + float64(i)*(plotW/float64(len(keys)-1))
		} else {
			cx = padL + plotW/2
		}
		cy := padT + (1-(ys[i]-yMin)/(yMax-yMin))*plotH
		cx = math.Round(cx*10) / 10
		cy = math.Round(cy*10) / 10
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%g,%g", cx, cy)
		vm.Points = append(vm.Points, linePointVM{CX: cx, CY: cy, X: k, YDisp: formatNum(ys[i])})
	}
	vm.Polyline = b.String()
	return vm, true
}

func minOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		m = math.Min(m, x)
	}
	return m
}

func maxOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		m = math.Max(m, x)
	}
	return m
}
