package postgres_test

import (
	_ "embed"
	"regexp"
	"testing"
)

//go:embed queries.sql
var unitStateQueries []byte

// unitStateWritePattern matches a statement that writes to the unit_state
// table, in any case and with any spacing.
var unitStateWritePattern = regexp.MustCompile(`(?i)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+unit_state\b`)

// queryNamePattern matches the name line that starts a query of queries.sql.
var queryNamePattern = regexp.MustCompile(`(?m)^-- name: ([A-Za-z0-9_]+)`)

// TestQueriesWriteUnitStateOnlyInPublish proves that PublishUnitState is the
// only query that writes the unit_state table.
func TestQueriesWriteUnitStateOnlyInPublish(t *testing.T) {
	t.Parallel()

	checkQueriesWriteUnitStateOnlyInPublish(t)
}

// checkQueriesWriteUnitStateOnlyInPublish reads the queries of the dialect
// and fails when a query other than PublishUnitState writes unit_state.
func checkQueriesWriteUnitStateOnlyInPublish(t *testing.T) {
	t.Helper()

	names := queryNamePattern.FindAllSubmatchIndex(unitStateQueries, -1)
	if len(names) == 0 {
		t.Fatal("queries.sql holds no query")
	}

	published := false

	for i, loc := range names {
		end := len(unitStateQueries)
		if i+1 < len(names) {
			end = names[i+1][0]
		}

		name := string(unitStateQueries[loc[2]:loc[3]])
		query := unitStateQueries[loc[0]:end]

		if name == "PublishUnitState" {
			published = true

			if !unitStateWritePattern.Match(query) {
				t.Fatal("PublishUnitState does not write unit_state")
			}

			continue
		}

		if unitStateWritePattern.Match(query) {
			t.Fatalf("query %s writes unit_state, want PublishUnitState only", name)
		}
	}

	if !published {
		t.Fatal("queries.sql holds no PublishUnitState query")
	}
}
