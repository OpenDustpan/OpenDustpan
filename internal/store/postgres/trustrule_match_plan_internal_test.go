package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestListTrustRulesNamingSpiffeIDPlan seeds a namespace with 20,000 rules,
// runs ANALYZE and checks that the plan of the match read of an mTLS exchange
// uses the trust_rules_spiffe index and neither sorts the rows nor scans the
// table.
func TestListTrustRulesNamingSpiffeIDPlan(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	fix := seedTrustRecheckFixture(t, s)

	// The fixture holds 1,001 rules. Add 19,000 more with one statement.
	_, err := s.pool.Exec(t.Context(), `
INSERT INTO trust_rules (
	id, namespace, name, issuer, audience, key_source, static_keys, conditions_json,
	principal_id, created_by, created_at_ms, revision, spiffe_id_pattern, bundle_id
)
SELECT 'plan-' || n, $1, 'plan-' || n, 'https://token.actions.example.com', 'https://dustpan.example.com',
	'discovery', '', '[]', $2, 'ana', 0, 1, 'spiffe://example.org/ns/acme/sa/plan-' || n, $3
FROM generate_series(1, 19000) AS n`,
		fix.fence.Namespace.String(), fix.rule.PrincipalID, fix.rule.BundleID)
	if err != nil {
		t.Fatalf("seed rules: %v", err)
	}

	if _, err = s.pool.Exec(t.Context(), `ANALYZE trust_rules`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	plan := explainTrustRuleMatch(t, s, fix)

	if !strings.Contains(plan, "trust_rules_spiffe") {
		t.Fatalf("plan does not use the index trust_rules_spiffe:\n%s", plan)
	}

	for _, bad := range []string{"Sort", "Seq Scan"} {
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
	body = strings.ReplaceAll(body, "sqlc.arg(namespace)", "$1")
	body = strings.ReplaceAll(body, "sqlc.arg(bundle_id)", "$2")
	body = strings.ReplaceAll(body, "sqlc.arg(spiffe_id_pattern)", "$3")
	body = strings.TrimSuffix(strings.TrimSpace(body), ";")

	rows, err := s.pool.Query(t.Context(), "EXPLAIN "+body,
		fix.fence.Namespace.String(), fix.rule.BundleID, fix.rule.SpiffeIDPattern)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder

	for rows.Next() {
		var line string
		if scanErr := rows.Scan(&line); scanErr != nil {
			t.Fatalf("read the plan: %v", scanErr)
		}

		plan.WriteString(line + "\n")
	}

	if err = rows.Err(); err != nil {
		t.Fatalf("read the plan: %v", err)
	}

	return plan.String()
}
