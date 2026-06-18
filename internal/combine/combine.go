// Package combine defines "combined data sources": a virtual dataset produced
// by joining two existing datasets on a shared key column. The combination is
// computed live at view time (never stored), so it stays fresh as its member
// sources change and reuses all the normal dataset machinery (filter, charts,
// saved views).
package combine

import (
	"errors"
	"strings"
)

// ErrNotFound is returned when a combined source is missing.
var ErrNotFound = errors.New("combined source not found")

// Member is one side of a join: a dataset collection and the column to join on.
type Member struct {
	Collection string `json:"collection"`
	Name       string `json:"name"` // display name (used to disambiguate columns)
	Key        string `json:"key"`  // join key column
}

// Combined is a left join: every row of Left, augmented with the matching Right
// row's columns (Right is used as a lookup — one row per key value; unmatched
// left rows keep blank right columns).
type Combined struct {
	Key   string `json:"key"`   // == its virtual collection id ("cmb_<slug>")
	Name  string `json:"name"`  // display name
	Owner string `json:"owner"` // creator (Keycloak username)
	Left  Member `json:"left"`
	Right Member `json:"right"`
}

// slug normalises a name into a safe key suffix.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "combined"
	}
	return out
}
