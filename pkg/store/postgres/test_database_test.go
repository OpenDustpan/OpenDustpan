package postgres_test

import (
	"strconv"
	"testing"
	"time"
)

// TestStaleTestDatabase checks which names the start of a run drops: only
// test databases older than the stale age. Another name, or a name whose
// time does not parse, stays.
func TestStaleTestDatabase(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(-d).Unix(), 10) }

	cases := []struct {
		name string
		db   string
		want bool
	}{
		{name: "older than the stale age", db: testDatabasePrefix + at(3*time.Hour) + "_0011223344556677", want: true},
		{name: "a template older", db: testDatabasePrefix + at(3*time.Hour) + "_00112233_tpl", want: true},
		{name: "a clone older", db: testDatabasePrefix + at(3*time.Hour) + "_00112233_tpl_c0011", want: true},
		{name: "a clone younger", db: testDatabasePrefix + at(time.Hour) + "_00112233_tpl_c0011", want: false},
		{name: "younger than the stale age", db: testDatabasePrefix + at(time.Hour) + "_0011223344556677", want: false},
		{name: "exactly the stale age", db: testDatabasePrefix + at(staleTestDatabaseAge) + "_00", want: false},
		{name: "another database", db: "postgres", want: false},
		{name: "the prefix without a time", db: testDatabasePrefix + "0011223344556677", want: false},
		{name: "a time that does not parse", db: testDatabasePrefix + "yesterday_00", want: false},
		{name: "the prefix in another case", db: "DUSTPAN_TEST_" + at(3*time.Hour) + "_00", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := staleTestDatabase(tc.db, now); got != tc.want {
				t.Fatalf("staleTestDatabase(%q) = %v, want %v", tc.db, got, tc.want)
			}
		})
	}
}
