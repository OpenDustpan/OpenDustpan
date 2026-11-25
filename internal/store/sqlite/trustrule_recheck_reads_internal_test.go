package sqlite

// Tests of the rows that the unexported recheck of an mTLS exchange reads.

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
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
}

// TestRecheckExchangeReadsOnlyTheMatchingRules proves that the recheck reads
// the narrow match rows and the bundle row, and never the full list of the
// rules of the namespace.
func TestRecheckExchangeReadsOnlyTheMatchingRules(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	fix := seedTrustRecheckFixture(t, s)
	recorder := &trustRecheckRecorder{DBTX: s.conn, calls: nil}
	q := db.New(recorder)
	x := trustRecheckExchange(fix.rule, fix.bundle)

	if err := recheckExchange(t.Context(), q, fix.fence.Namespace, fix.rule, &x); err != nil {
		t.Fatalf("recheckExchange = %v", err)
	}

	if len(recorder.calls) != 2 {
		t.Fatalf("statements = %d, want 2: %+v", len(recorder.calls), recorder.calls)
	}

	checkTrustRecheckMatchCall(t, recorder.calls[0], fix)

	if !strings.Contains(recorder.calls[1].query, "trust_bundles") {
		t.Fatalf("bundle statement = %q", recorder.calls[1].query)
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

	row := s.conn.QueryRowContext(t.Context(), `
SELECT COUNT(*) FROM access_tokens AS t
JOIN principals AS p ON p.id = t.principal_id
WHERE p.namespace = ?`, ns.String())
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
			name: "a twin of another bundle",
			mutate: func(t *testing.T, s *Store, _ trustRecheckFixture) {
				t.Helper()
				ns := namespace.Name("other-bundle-ns")
				otherFence, _ := seedTrustNamespace(t, s, ns)
				otherWorker := seedTrustRecheckWorkerIn(t, s, otherFence, ns)
				otherBundle := trustrule.BundleID(seedTrustBundle(t, s, otherFence, ns))
				seedTrustRecheckRuleRow(t, s, trustRecheckRuleSpec{
					ns: ns, worker: otherWorker, name: "twin-other-bundle",
					pattern: trustRecheckSPIFFEID, bundle: otherBundle,
				})
			},
			wantErr: nil,
		},
		{
			name: "the same bundle ID text and pattern in another namespace",
			mutate: func(t *testing.T, s *Store, fix trustRecheckFixture) {
				t.Helper()
				ns := namespace.Name("cross")
				fence, worker := seedTrustNamespace(t, s, ns)
				_ = seedTrustBundle(t, s, fence, ns)
				// Insert a rule in another namespace with the same bundle ID text
				// and pattern. The SQL write bypasses the cross-namespace bundle
				// foreign key, as a hostile backup row would.
				seedTrustRecheckRuleRowRaw(t, s, trustRecheckRuleSpec{
					ns: ns, worker: worker, name: "deploy",
					pattern: trustRecheckSPIFFEID, bundle: trustrule.BundleID(fix.rule.BundleID),
				})
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			fix := seedTrustRecheckFixture(t, s)
			tc.mutate(t, s, fix)
			rule := reloadTrustRecheckRule(t, s, fix.fence.Namespace, fix.rule.ID)
			x := trustRecheckExchange(rule, fix.bundle)

			err := recheckExchange(t.Context(), db.New(s.conn), fix.fence.Namespace, rule, &x)
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

// tamperTrustRulePattern changes the stored SPIFFE ID pattern with SQL.
func tamperTrustRulePattern(t *testing.T, s *Store, id, pattern string) {
	t.Helper()

	tamperTrustStage(t, s, `UPDATE trust_rules SET spiffe_id_pattern = ? WHERE id = ?`, pattern, id)
}

// tamperTrustRuleNullBundle clears the stored bundle of a rule with SQL.
func tamperTrustRuleNullBundle(t *testing.T, s *Store, id string) {
	t.Helper()

	tamperTrustStage(t, s, `UPDATE trust_rules SET bundle_id = NULL WHERE id = ?`, id)
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
