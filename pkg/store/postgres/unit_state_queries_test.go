package postgres_test

import (
	_ "embed"
	"regexp"
	"testing"
)

//go:embed queries.sql
var unitStateQueries string

// unitStateWritePattern matches a statement that writes to the unit_state
// table, in any case and with any spacing.
var unitStateWritePattern = regexp.MustCompile(`(?i)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+unit_state\b`)

// queryNamePattern matches the name line that starts a query of queries.sql.
var queryNamePattern = regexp.MustCompile(`(?m)^-- name: ([A-Za-z0-9_]+)`)

// TestQueriesWriteUnitStateOnlyInStateWriters proves that the state writers
// are the only queries that write the unit_state table.
func TestQueriesWriteUnitStateOnlyInStateWriters(t *testing.T) {
	t.Parallel()

	checkQueriesWriteUnitStateOnlyInStateWriters(t)
}

// checkQueriesWriteUnitStateOnlyInStateWriters reads the queries of the
// dialect and fails when a query other than a state writer writes unit_state.
func checkQueriesWriteUnitStateOnlyInStateWriters(t *testing.T) {
	t.Helper()

	names := queryNamePattern.FindAllStringSubmatchIndex(unitStateQueries, -1)
	if len(names) == 0 {
		t.Fatal("queries.sql holds no query")
	}

	writers := map[string]bool{
		"FinalizeReleasedUnitState": true,
		"FinalizeUnitState":         true,
		"PublishUnitState":          true,
	}
	wrote := make(map[string]bool, len(writers))

	for i, loc := range names {
		end := len(unitStateQueries)
		if i+1 < len(names) {
			end = names[i+1][0]
		}

		name := unitStateQueries[loc[2]:loc[3]]
		query := unitStateQueries[loc[0]:end]

		if writers[name] {
			wrote[name] = true

			if !unitStateWritePattern.MatchString(query) {
				t.Fatalf("%s does not write unit_state", name)
			}

			continue
		}

		if unitStateWritePattern.MatchString(query) {
			t.Fatalf("query %s writes unit_state, want one of the state writers", name)
		}
	}

	for name := range writers {
		if !wrote[name] {
			t.Fatalf("queries.sql holds no %s query", name)
		}
	}
}
