package sqlite

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestListTrustRulesNamingSpiffeIDPlan seeds a namespace with 1,000 rules and
// checks that the plan of the match read of an mTLS exchange searches the
// trust_rules_spiffe index with all three conditions, and neither scans the
// table nor sorts the rows.
func TestListTrustRulesNamingSpiffeIDPlan(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	fix := seedTrustRecheckFixture(t, s)

	if _, err := s.conn.ExecContext(t.Context(), `ANALYZE`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	plan := explainTrustRuleMatch(t, s, fix)

	const want = "SEARCH trust_rules USING INDEX trust_rules_spiffe (namespace=? AND bundle_id=? AND spiffe_id_pattern=?)"
	if !strings.Contains(plan, want) {
		t.Fatalf("plan does not hold %q:\n%s", want, plan)
	}

	for _, bad := range []string{"SCAN", "USE TEMP B-TREE"} {
		if strings.Contains(plan, bad) {
			t.Fatalf("plan holds %q:\n%s", bad, plan)
		}
	}
}

// explainTrustRuleMatch returns the plan of ListTrustRulesNamingSpiffeID.
func explainTrustRuleMatch(t *testing.T, s *Store, fix trustRecheckFixture) string {
	t.Helper()

	data, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatalf("read queries.sql: %v", err)
	}

	_, rest, found := strings.Cut(string(data), "-- name: ListTrustRulesNamingSpiffeID :many\n")
	if !found {
		t.Fatal("query ListTrustRulesNamingSpiffeID not found")
	}

	body, _, _ := strings.Cut(rest, "\n-- name:")
	body = regexp.MustCompile(`(?m)^--.*$`).ReplaceAllString(body, "")
	body = strings.ReplaceAll(body, "sqlc.arg(namespace)", "?1")
	body = strings.ReplaceAll(body, "sqlc.arg(bundle_id)", "?2")
	body = strings.ReplaceAll(body, "sqlc.arg(spiffe_id_pattern)", "?3")
	body = strings.TrimSuffix(strings.TrimSpace(body), ";")

	rows, err := s.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+body,
		fix.fence.Namespace.String(), fix.rule.BundleID, fix.rule.SpiffeIDPattern)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close the plan rows: %v", closeErr)
		}
	}()

	var plan strings.Builder

	for rows.Next() {
		var (
			id, parent, unused int
			detail             string
		)

		if scanErr := rows.Scan(&id, &parent, &unused, &detail); scanErr != nil {
			t.Fatalf("read the plan: %v", scanErr)
		}

		plan.WriteString(detail + "\n")
	}

	if err = rows.Err(); err != nil {
		t.Fatalf("read the plan: %v", err)
	}

	return plan.String()
}
