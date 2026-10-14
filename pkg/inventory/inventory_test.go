package inventory_test

import (
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
)

// deletedEntries returns n deleted units of acme named unit-00 to unit-nn,
// in the order of the port.
func deletedEntries(n int) []inventory.DeletedEntry {
	at := time.Date(2026, time.October, 14, 0, 0, 0, 0, time.UTC)
	out := make([]inventory.DeletedEntry, 0, n)

	for i := range n {
		out = append(out, inventory.DeletedEntry{
			DeletedAt:  at,
			PurgeAfter: at.Add(time.Duration(i) * time.Hour),
			Namespace:  "acme",
			Tool:       "pulumi",
			UnitID:     fmt.Sprintf("stk-%02d", i),
			Name:       fmt.Sprintf("web/unit-%02d", i),
		})
	}

	return out
}

// TestPageDeletedSizes proves the page size rules: zero means the default,
// a value above the cap means the cap, and a page that the matches fill
// exactly has no next cursor.
func TestPageDeletedSizes(t *testing.T) {
	t.Parallel()

	all := deletedEntries(inventory.MaxDeletedPageSize + 5)

	cases := []struct {
		name     string
		limit    int
		count    int
		wantLen  int
		wantNext bool
	}{
		{name: "default", limit: 0, count: len(all), wantLen: inventory.DefaultDeletedPageSize, wantNext: true},
		{
			name:     "capped",
			limit:    inventory.MaxDeletedPageSize + 100,
			count:    len(all),
			wantLen:  inventory.MaxDeletedPageSize,
			wantNext: true,
		},
		{name: "exact fit", limit: 3, count: 3, wantLen: 3, wantNext: false},
		{name: "empty", limit: 3, count: 0, wantLen: 0, wantNext: false},
	}

	for _, tc := range cases {
		page := inventory.PageDeleted(all[:tc.count], inventory.DeletedQuery{
			PurgeBefore: time.Time{},
			After:       nil,
			Namespace:   "acme",
			Tool:        "",
			NamePrefix:  "",
			UnitIDs:     nil,
			Restricted:  false,
			Limit:       tc.limit,
		})

		if len(page.Entries) != tc.wantLen || (page.Next != nil) != tc.wantNext || page.Total != tc.count {
			t.Errorf("%s: page = %d entries, next %v, total %d; want %d, next %v, total %d",
				tc.name, len(page.Entries), page.Next != nil, page.Total, tc.wantLen, tc.wantNext, tc.count)
		}
	}
}

// TestPageDeletedTrimMatchesPage proves that TrimDeletedPage over a store
// read of size plus one rows gives the page that PageDeleted gives.
func TestPageDeletedTrimMatchesPage(t *testing.T) {
	t.Parallel()

	all := deletedEntries(7)
	q := inventory.DeletedQuery{
		PurgeBefore: time.Time{},
		After:       nil,
		Namespace:   "acme",
		Tool:        "",
		NamePrefix:  "",
		UnitIDs:     nil,
		Restricted:  false,
		Limit:       3,
	}

	want := inventory.PageDeleted(all, q)
	got := inventory.TrimDeletedPage(all[:4], 3, len(all))

	if len(got.Entries) != len(want.Entries) || got.Next == nil || want.Next == nil || *got.Next != *want.Next ||
		got.Total != want.Total {
		t.Fatalf("trimmed page = %+v, want %+v", got, want)
	}
}
