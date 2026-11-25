package postgres

// Tests of the rows that the unexported recheck of an mTLS exchange reads.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/internal/store/trustrulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// checkTrustRecheckMatchCall fails the test when the match statement reads
// more than the narrow match rows.
func checkTrustRecheckMatchCall(t *testing.T, match trustRecheckCall, fix trustRecheckFixture) {
	t.Helper()

	if !strings.Contains(match.query, "FROM trust_rules") || !strings.Contains(match.query, "LIMIT 2") {
		t.Fatalf("match statement = %q", match.query)
	}

	if strings.Contains(match.query, "ORDER BY") {
		t.Fatalf("match statement orders the rows: %q", match.query)
	}

	if len(match.args) != 3 || match.args[0] != fix.fence.Namespace.String() ||
		match.args[1] != fix.rule.BundleID || match.args[2] != fix.rule.SpiffeIDPattern {
		t.Fatalf("match args = %#v", match.args)
	}

	if match.rows > 2 {
		t.Fatalf("match rows read = %d, want at most 2", match.rows)
	}
}

// TestRecheckExchangeReadsOnlyTheMatchingRules proves that the recheck reads
// the narrow match rows and the bundle row, and never the full list of the
// rules of the namespace.
func TestRecheckExchangeReadsOnlyTheMatchingRules(t *testing.T) {
	t.Parallel()

	base := testPostgresURL(t)
	s := openKeepLockStore(t, base)
	fix := seedTrustRecheckFixture(t, s)
	seedTrustRecheckOtherBundleTwin(t, s, fix)
	seedTrustRecheckCrossNamespaceRule(t, s, fix)

	recorder := &trustRecheckRecorder{DBTX: s.reads, calls: nil}
	q := db.New(recorder)
	x := trustRecheckExchange(fix.rule, fix.bundle)

	if err := recheckExchange(t.Context(), q, fix.fence.Namespace, fix.rule, &x); err != nil {
		t.Fatalf("recheckExchange = %v", err)
	}

	if len(recorder.calls) != 2 {
		t.Fatalf("statements = %d, want 2: %+v", len(recorder.calls), recorder.calls)
	}

	checkTrustRecheckMatchCall(t, recorder.calls[0], fix)

	bundle := recorder.calls[1]
	if !strings.Contains(bundle.query, "trust_bundles") {
		t.Fatalf("bundle statement = %q", bundle.query)
	}

	if bundle.rows > 1 {
		t.Fatalf("bundle rows read = %d, want at most 1", bundle.rows)
	}

	for _, c := range recorder.calls {
		if strings.Contains(c.query, "trust_rules") && !strings.Contains(c.query, "spiffe_id_pattern =") {
			t.Fatalf("statement reads trust_rules without the SPIFFE ID condition: %q", c.query)
		}
	}
}

// countTrustRecheckTokens returns the number of access tokens of the
// principals of ns.
func countTrustRecheckTokens(t *testing.T, s *Store, ns namespace.Name) int {
	t.Helper()

	var n int

	row := s.pool.QueryRow(t.Context(), `
SELECT COUNT(*) FROM access_tokens AS t
JOIN principals AS p ON p.id = t.principal_id
WHERE p.namespace = $1`, ns.String())
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}

	return n
}

// TestRecheckExchangeHostileRows sends hostile stored rows to the recheck
// through SQL, as if the write guards had failed. The recheck must not count
// a row that does not name the same SPIFFE ID in the same bundle of the same
// namespace, and it must refuse a twin in the same bundle.
func TestRecheckExchangeHostileRows(t *testing.T) {
	t.Parallel()

	type caseSpec struct {
		name    string
		mutate  func(t *testing.T, s *Store, fix trustRecheckFixture)
		wantErr error
	}

	patternCase := func(name, pattern string) caseSpec {
		return caseSpec{
			name: name,
			mutate: func(t *testing.T, s *Store, fix trustRecheckFixture) {
				t.Helper()
				tamperTrustRulePattern(t, s, fix.rule.ID, pattern)
			},
			wantErr: trustrule.ErrNotFound,
		}
	}

	cases := []caseSpec{
		patternCase("a stored pattern with a wildcard", "spiffe://example.org/ns/acme/sa/*"),
		patternCase("a stored pattern with a percent sign", "spiffe://example.org/ns/acme/sa/%"),
		patternCase("a stored pattern with an underscore", "spiffe://example.org/ns/acme/sa/_eploy"),
		patternCase("a pattern that differs by case", "spiffe://example.org/ns/acme/sa/Deploy"),
		patternCase("a pattern that differs by a trailing slash", trustRecheckSPIFFEID+"/"),
		patternCase("a pattern that differs by a trailing space", trustRecheckSPIFFEID+" "),
		{
			name: "a twin of the same bundle",
			mutate: func(t *testing.T, s *Store, fix trustRecheckFixture) {
				t.Helper()
				seedTrustRecheckRuleRow(t, s, trustRecheckRuleSpec{
					ns: namespace.Name("acme"), worker: fix.rule.PrincipalID, name: "twin-same",
					pattern: trustRecheckSPIFFEID, bundle: trustrule.BundleID(fix.rule.BundleID),
				})
			},
			wantErr: trustrule.ErrNotFound,
		},
		{
			name: "a twin of another bundle in the same namespace",
			mutate: func(t *testing.T, s *Store, fix trustRecheckFixture) {
				t.Helper()
				seedTrustRecheckOtherBundleTwin(t, s, fix)
			},
			wantErr: nil,
		},
		{
			name: "a twin with a NULL bundle in the same namespace",
			mutate: func(t *testing.T, s *Store, fix trustRecheckFixture) {
				t.Helper()
				seedTrustRecheckHostileRule(t, s, fix.rule.PrincipalID, "twin-null-bundle", nil)
			},
			wantErr: nil,
		},
		{
			name: "the same bundle ID text and pattern in another namespace",
			mutate: func(t *testing.T, s *Store, fix trustRecheckFixture) {
				t.Helper()
				seedTrustRecheckCrossNamespaceRule(t, s, fix)
			},
			wantErr: nil,
		},
		{
			name: "a rule with a NULL bundle ID and the same pattern",
			mutate: func(t *testing.T, s *Store, fix trustRecheckFixture) {
				t.Helper()
				tamperTrustRuleNullBundle(t, s, fix.rule.ID)
			},
			wantErr: trustrule.ErrNotFound,
		},
	}

	base := testPostgresURL(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openKeepLockStore(t, base)
			fix := seedTrustRecheckFixture(t, s)
			tc.mutate(t, s, fix)
			rule := reloadTrustRecheckRule(t, s, fix.fence.Namespace, fix.rule.ID)
			x := trustRecheckExchange(rule, fix.bundle)

			err := recheckExchange(t.Context(), db.New(s.pool), fix.fence.Namespace, rule, &x)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("recheckExchange = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("recheckExchange = %v, want %v", err, tc.wantErr)
			}

			if n := countTrustRecheckTokens(t, s, fix.fence.Namespace); n != 0 {
				t.Fatalf("tokens after refusal = %d, want 0", n)
			}
		})
	}
}

// TestRecheckExchangeHostileRowsCannotFillTheLimit stores hostile rows of the
// same namespace and SPIFFE ID with another bundle and with a NULL bundle
// before the rule of the exchange, so that they come first in the order of
// each index and of the table. The match query reads at most 2 rows. Only its
// bundle_id condition keeps the hostile rows out of the 2 slots: without it
// the rule of the exchange is refused, and a real twin can hide behind them.
func TestRecheckExchangeHostileRowsCannotFillTheLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		twin    bool
		wantErr error
	}{
		{"the rule of the exchange alone is accepted", false, nil},
		{"a real twin of the same bundle is refused", true, trustrule.ErrNotFound},
	}

	base := testPostgresURL(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openKeepLockStore(t, base)
			fix := seedTrustRecheckLimitFixture(t, s)
			if tc.twin {
				seedTrustRecheckRuleRow(t, s, trustRecheckRuleSpec{
					ns: fix.fence.Namespace, worker: fix.rule.PrincipalID, name: "twin",
					pattern: trustRecheckSPIFFEID, bundle: trustrule.BundleID(fix.rule.BundleID),
				})
			}

			x := trustRecheckExchange(fix.rule, fix.bundle)

			err := recheckExchange(t.Context(), db.New(s.pool), fix.fence.Namespace, fix.rule, &x)
			checkTrustRecheckAnswer(t, s, fix.fence.Namespace, err, tc.wantErr)
		})
	}
}

// seedTrustRecheckLimitFixture makes acme with its bundle, 2 hostile rules of
// another bundle and 2 hostile rules with a NULL bundle, then the rule of the
// exchange.
func seedTrustRecheckLimitFixture(t *testing.T, s *Store) trustRecheckFixture {
	t.Helper()

	fence, _ := seedStagedTrustNamespace(t, s, namespace.Name("acme"))
	worker := seedTrustRecheckWorker(t, s, fence)
	seedStagedTrustBundle(t, s, fence, namespace.Name("acme"))

	b, err := s.TrustRules().GetBundle(t.Context(), namespace.Name("acme"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}

	for i := range 2 {
		seedTrustRecheckHostileRule(t, s, worker, fmt.Sprintf("a-hostile-%d", i), trustRecheckHostileBundle)
		seedTrustRecheckHostileRule(t, s, worker, fmt.Sprintf("a-hostile-null-%d", i), nil)
	}

	spec := trustRecheckRuleSpec{
		ns: namespace.Name("acme"), worker: worker, name: "deploy", pattern: trustRecheckSPIFFEID, bundle: b.ID,
	}
	rule := seedTrustRecheckRuleRow(t, s, spec)

	return trustRecheckFixture{
		fence: fence,
		rule:  rule,
		bundle: trustrulestore.BundleRow{
			ID: string(b.ID), Namespace: "acme", Active: b.Active, Revision: b.Revision,
			CreatedAtMs: b.CreatedAt.UnixMilli(),
		},
	}
}

// checkTrustRecheckAnswer fails the test when err is not wantErr, or when a
// refused exchange left a token in ns.
func checkTrustRecheckAnswer(t *testing.T, s *Store, ns namespace.Name, err, wantErr error) {
	t.Helper()

	if wantErr == nil {
		if err != nil {
			t.Fatalf("recheckExchange = %v, want nil", err)
		}

		return
	}

	if !errors.Is(err, wantErr) {
		t.Fatalf("recheckExchange = %v, want %v", err, wantErr)
	}

	if n := countTrustRecheckTokens(t, s, ns); n != 0 {
		t.Fatalf("tokens after refusal = %d, want 0", n)
	}
}

// trustRecheckHostileBundle is a bundle ID that no bundle row has. Its text
// sorts before the IDs of the bundles that the store makes.
const trustRecheckHostileBundle = "aaa-hostile-bundle"

// seedTrustRecheckHostileRule stores a rule of acme that names the SPIFFE ID
// of the exchange, with SQL and the bundle foreign key off, as a hostile
// backup row would. A nil bundle stores NULL.
func seedTrustRecheckHostileRule(t *testing.T, s *Store, worker, name string, bundle any) {
	t.Helper()

	statements := []string{
		`ALTER TABLE trust_rules DROP CONSTRAINT IF EXISTS trust_rules_namespace_bundle_id_fkey`,
		`INSERT INTO trust_rules (
	id, namespace, name, issuer, audience, key_source, static_keys, conditions_json,
	principal_id, created_by, created_at_ms, revision, spiffe_id_pattern, bundle_id
) VALUES ($1, 'acme', $2, 'https://token.actions.example.com', 'https://dustpan.example.com',
	'discovery', '', '[]', $3, 'ana', $4, 1, $5, $6)`,
	}

	tamperStagedTrust(t, s, statements,
		string(trustrule.NewID()), name, worker, trustRecheckTime().UnixMilli(), trustRecheckSPIFFEID, bundle,
	)
}

// seedTrustRecheckOtherBundleTwin stores a rule in the namespace of the
// exchange that names its SPIFFE ID in a bundle that is not the bundle of
// the rule of the exchange.
func seedTrustRecheckOtherBundleTwin(t *testing.T, s *Store, fix trustRecheckFixture) {
	t.Helper()

	seedTrustRecheckHostileRule(t, s, fix.rule.PrincipalID, "twin-other-bundle", trustRecheckHostileBundle)
}

// seedTrustRecheckCrossNamespaceRule stores a rule of another namespace with
// the same bundle ID text and pattern as the rule of the exchange.
func seedTrustRecheckCrossNamespaceRule(t *testing.T, s *Store, fix trustRecheckFixture) {
	t.Helper()

	ns := namespace.Name("cross")
	fence, worker := seedStagedTrustNamespace(t, s, ns)
	_ = seedStagedTrustBundle(t, s, fence, ns)
	seedTrustRecheckRuleRowRaw(t, s, trustRecheckRuleSpec{
		ns: ns, worker: worker, name: "deploy",
		pattern: trustRecheckSPIFFEID, bundle: trustrule.BundleID(fix.rule.BundleID),
	})
}

// tamperTrustRulePattern changes the stored SPIFFE ID pattern with SQL.
func tamperTrustRulePattern(t *testing.T, s *Store, id, pattern string) {
	t.Helper()

	tamperStagedTrust(t, s, []string{`UPDATE trust_rules SET spiffe_id_pattern = $1 WHERE id = $2`}, pattern, id)
}

// tamperTrustRuleNullBundle clears the stored bundle of a rule with SQL.
func tamperTrustRuleNullBundle(t *testing.T, s *Store, id string) {
	t.Helper()

	tamperStagedTrust(t, s, []string{`UPDATE trust_rules SET bundle_id = NULL WHERE id = $1`}, id)
}

// reloadTrustRecheckRule reads the rule row after a hostile SQL change.
func reloadTrustRecheckRule(t *testing.T, s *Store, ns namespace.Name, id string) trustrulestore.Row {
	t.Helper()

	row, err := s.queries.GetTrustRule(t.Context(), db.GetTrustRuleParams{Namespace: ns.String(), ID: id})
	if err != nil {
		t.Fatalf("read rule: %v", err)
	}

	return trustRuleRow(row)
}
