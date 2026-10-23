package postgres_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// planWorkspaces is the number of live workspaces that the plan test seeds in
// one namespace.
const planWorkspaces = 1500

// workspacePageQuery returns the SQL of ReadWorkspacePage from queries.sql,
// with its named arguments as $1 to $4 in the order that sqlc gives them.
func workspacePageQuery(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatalf("read queries.sql: %v", err)
	}

	_, rest, found := strings.Cut(string(data), "-- name: ReadWorkspacePage :many\n")
	if !found {
		t.Fatal("query ReadWorkspacePage not found")
	}

	body, _, _ := strings.Cut(rest, "\n-- name:")
	body = regexp.MustCompile(`(?m)^--.*$`).ReplaceAllString(body, "")
	body = strings.NewReplacer(
		"sqlc.embed(w)", "w.*",
		"sqlc.arg(namespace)", "$1",
		"sqlc.arg(name_contains)", "$2",
		"sqlc.arg(page_offset)", "$3",
		"sqlc.arg(page_limit)", "$4",
	).Replace(body)

	return strings.TrimSuffix(strings.TrimSpace(body), ";")
}

// seedWorkspaces creates the namespace acme with planWorkspaces live
// workspaces named web-00001 and up. It does not analyze the tables.
func seedWorkspaces(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	statements := []string{
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
			VALUES ('acme', 'eu-west', 1, 0, 'ns-plan')`,
		`INSERT INTO workspaces (id, namespace, name, revision, created_at_ms, updated_at_ms)
			SELECT 'ws-' || lpad(i::text, 5, '0'), 'acme', 'web-' || lpad(i::text, 5, '0'), 1, 0, 0
			FROM generate_series(1, $1::int) AS i`,
	}

	for n, statement := range statements {
		args := []any{}
		if n == 1 {
			args = append(args, planWorkspaces)
		}

		if _, err := conn.Exec(t.Context(), statement, args...); err != nil {
			t.Fatalf("seed workspaces, statement %d: %v", n, err)
		}
	}
}

// genericPlan returns the generic plan of query, the plan that a prepared
// statement keeps for all argument values. The query goes as one simple query,
// so its $n placeholders reach the server without values.
func genericPlan(t *testing.T, conn *pgx.Conn, query string) string {
	t.Helper()

	results, err := conn.PgConn().Exec(t.Context(), "EXPLAIN (GENERIC_PLAN) "+query).ReadAll()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	var plan strings.Builder

	for _, result := range results {
		for _, row := range result.Rows {
			plan.Write(row[0])
			plan.WriteString("\n")
		}
	}

	return plan.String()
}

// pageNames runs query with args and returns the name column of each row.
func pageNames(t *testing.T, conn *pgx.Conn, query string, args ...any) []string {
	t.Helper()

	rows, err := conn.Query(t.Context(), query, args...)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}

	defer rows.Close()

	var names []string

	for rows.Next() {
		values, valuesErr := rows.Values()
		if valuesErr != nil {
			t.Fatalf("read page row: %v", valuesErr)
		}

		name, ok := values[2].(string)
		if !ok {
			t.Fatalf("name column holds %T", values[2])
		}

		names = append(names, name)
	}

	if err = rows.Err(); err != nil {
		t.Fatalf("read page: %v", err)
	}

	return names
}

// pageNameCondition matches an index condition that holds the namespace and the
// name of a page row.
var pageNameCondition = regexp.MustCompile(`Index Cond: \(\(namespace = \$1\) AND \(name = \w+\.name\)\)`)

// TestWorkspacePagePlan seeds one namespace with 1,500 live workspaces and no
// table statistics, as a fresh or restored database has, and checks that the
// generic plan of a page reads the rows of the page by their names. A plan
// that joins each page name to all the workspaces of the namespace grows with
// the size of the namespace. It then reads one page and checks the names.
func TestWorkspacePagePlan(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL plan test", envURL)
	}

	schemaURL := isolatedURL(t, base)
	_ = openURL(t, schemaURL)

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	seedWorkspaces(t, conn)

	query := workspacePageQuery(t)

	plan := genericPlan(t, conn, query)
	if strings.Contains(plan, "Join Filter") || !pageNameCondition.MatchString(plan) {
		t.Fatalf("plan does not read the page rows by name in the index condition:\n%s", plan)
	}

	names := pageNames(t, conn, query, "acme", "web-", 1000, 100)
	if len(names) != 100 || names[0] != "web-01001" || names[99] != "web-01100" {
		t.Fatalf("page = %d names, want web-01001 to web-01100: %v", len(names), names)
	}
}
