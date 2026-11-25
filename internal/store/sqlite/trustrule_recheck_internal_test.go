package sqlite

// Tests of the unexported recheck of an mTLS exchange. The file name keeps the
// white-box package under the testpackage exception in .golangci.yml.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/internal/store/trustrulestore"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// trustRecheckTime is the fixed time of the recheck tests.
func trustRecheckTime() time.Time {
	return time.Date(2025, time.June, 1, 12, 0, 0, 0, time.UTC)
}

// trustRecheckSPIFFEID is the SPIFFE ID that the rule of the exchange names.
const trustRecheckSPIFFEID = "spiffe://example.org/ns/acme/sa/deploy"

// trustRecheckRecorder wraps a DBTX and records the text and the arguments of
// each statement that the recheck runs.
type trustRecheckRecorder struct {
	db.DBTX

	calls []trustRecheckCall
}

// trustRecheckCall is one recorded statement.
type trustRecheckCall struct {
	query string
	args  []any
}

// QueryContext records the statement and runs it.
func (r *trustRecheckRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.calls = append(r.calls, trustRecheckCall{query: query, args: append([]any(nil), args...)})

	return r.DBTX.QueryContext(ctx, query, args...)
}

// QueryRowContext records the statement and runs it.
func (r *trustRecheckRecorder) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.calls = append(r.calls, trustRecheckCall{query: query, args: append([]any(nil), args...)})

	return r.DBTX.QueryRowContext(ctx, query, args...)
}

// ExecContext records the statement and runs it.
func (r *trustRecheckRecorder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.calls = append(r.calls, trustRecheckCall{query: query, args: append([]any(nil), args...)})

	return r.DBTX.ExecContext(ctx, query, args...)
}

// trustRecheckFixture is a namespace with one rule of the exchange, many other
// rules and a bundle.
type trustRecheckFixture struct {
	fence  namespace.Fence
	rule   trustrulestore.Row
	bundle trustrulestore.BundleRow
}

// trustRecheckRuleSpec names the parts of one stored rule.
type trustRecheckRuleSpec struct {
	ns      namespace.Name
	worker  string
	name    string
	pattern string
	bundle  trustrule.BundleID
}

// seedTrustRecheckFixture makes one namespace, a bundle, 1,000 rules of other
// SPIFFE IDs and the rule of the exchange.
func seedTrustRecheckFixture(t *testing.T, s *Store) trustRecheckFixture {
	t.Helper()

	fence, _ := seedTrustNamespace(t, s, namespace.Name("acme"))
	worker := seedTrustRecheckWorker(t, s, fence)
	seedTrustBundle(t, s, fence, namespace.Name("acme"))

	b, err := s.TrustRules().GetBundle(t.Context(), namespace.Name("acme"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}

	base := trustRecheckRuleSpec{ns: namespace.Name("acme"), worker: worker, name: "", pattern: "", bundle: b.ID}
	seedTrustRecheckManyRules(t, s, base, 1000)

	base.name = "deploy"
	base.pattern = trustRecheckSPIFFEID
	rule := seedTrustRecheckRuleRow(t, s, base)

	return trustRecheckFixture{
		fence: fence,
		rule:  rule,
		bundle: trustrulestore.BundleRow{
			ID: string(b.ID), Namespace: "acme", Active: b.Active,
			Revision: b.Revision, CreatedAtMs: b.CreatedAt.UnixMilli(),
		},
	}
}

// seedTrustRecheckWorker makes a service principal in acme and returns its ID.
func seedTrustRecheckWorker(t *testing.T, s *Store, fence namespace.Fence) string {
	t.Helper()

	return seedTrustRecheckWorkerIn(t, s, fence, namespace.Name("acme"))
}

// seedTrustRecheckWorkerIn makes a service principal in ns and returns its ID.
func seedTrustRecheckWorkerIn(t *testing.T, s *Store, fence namespace.Fence, ns namespace.Name) string {
	t.Helper()

	id := "prn-" + ns.String() + "-worker"
	p := principal.Principal{
		CreatedAt: trustRecheckTime(), UpdatedAt: trustRecheckTime(), DisabledAt: time.Time{}, ID: id,
		Subject: "subject-" + id, DisplayName: "worker", Kind: principal.KindService,
		Source: principal.SourceLocal, Namespace: ns, GroupCount: 0, BreakGlass: false,
	}
	if err := s.CreateNamespacePrincipal(t.Context(), p, fence, stagedTrustEntry(ns)); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	return p.ID
}

// seedTrustRecheckManyRules inserts n mTLS rules with SQL, so the test can
// seed more than MaxPerNamespace rules without the store rule-count check.
func seedTrustRecheckManyRules(t *testing.T, s *Store, base trustRecheckRuleSpec, n int) {
	t.Helper()

	for i := range n {
		spec := base
		spec.name = fmt.Sprintf("other-%04d", i)
		spec.pattern = fmt.Sprintf("spiffe://example.org/ns/acme/sa/other-%04d", i)
		seedTrustRecheckRuleRow(t, s, spec)
	}
}

// seedTrustRecheckRuleRow stores one mTLS rule with SQL and returns its shared
// row. It bypasses the store rule-count check so the fixture can hold more
// than MaxPerNamespace rules.
func seedTrustRecheckRuleRow(t *testing.T, s *Store, spec trustRecheckRuleSpec) trustrulestore.Row {
	t.Helper()

	id := string(trustrule.NewID())
	const conditions = `[{"claim":"repository","values":["acme/infra"]},{"claim":"sub","values":["repo:acme/infra"]}]`
	_, err := s.conn.ExecContext(t.Context(), `
INSERT INTO trust_rules (
	id, namespace, name, issuer, audience, key_source, static_keys, conditions_json,
	principal_id, created_by, created_at_ms, revision, spiffe_id_pattern, bundle_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		id, spec.ns.String(), spec.name,
		"https://token.actions.example.com", "https://dustpan.example.com",
		"discovery", "", conditions, spec.worker, "ana", trustRecheckTime().UnixMilli(),
		spec.pattern, string(spec.bundle),
	)
	if err != nil {
		t.Fatalf("insert rule %s: %v", spec.name, err)
	}

	return trustrulestore.Row{
		ID: id, Namespace: spec.ns.String(), Name: spec.name, Issuer: "https://token.actions.example.com",
		Audience: "https://dustpan.example.com", KeySource: "discovery", StaticKeys: "",
		ConditionsJSON: conditions, PrincipalID: spec.worker, CreatedBy: "ana",
		CreatedAtMs: trustRecheckTime().UnixMilli(), Revision: 1,
		SpiffeIDPattern: spec.pattern, BundleID: string(spec.bundle),
	}
}

// seedTrustRecheckRuleRowRaw stores a rule row with the foreign keys and the
// check constraints off, as a hostile backup row would.
func seedTrustRecheckRuleRowRaw(t *testing.T, s *Store, spec trustRecheckRuleSpec) {
	t.Helper()

	for _, pragma := range []string{`PRAGMA foreign_keys = OFF`, `PRAGMA ignore_check_constraints = ON`} {
		if _, err := s.conn.ExecContext(t.Context(), pragma); err != nil {
			t.Fatalf("disable a constraint: %v", err)
		}
	}

	seedTrustRecheckRuleRow(t, s, spec)

	for _, pragma := range []string{`PRAGMA ignore_check_constraints = OFF`, `PRAGMA foreign_keys = ON`} {
		if _, err := s.conn.ExecContext(t.Context(), pragma); err != nil {
			t.Fatalf("restore a constraint: %v", err)
		}
	}
}

// trustRecheckExchange returns the exchange value that the recheck checks. The
// SPIFFE ID is the verified SPIFFE ID of the exchange, not the pattern stored
// in the rule row, so a hostile-row test can tamper the rule and keep the
// exchange fixed.
func trustRecheckExchange(rule trustrulestore.Row, bundle trustrulestore.BundleRow) trustrule.Exchange {
	var tok accesstoken.Token
	tok.Kind = accesstoken.KindMTLSExchange

	return trustrule.Exchange{
		Token: tok, Digest: accesstoken.Digest{}, Rule: trustrule.ID(rule.ID), SPIFFEID: trustRecheckSPIFFEID,
		BundleID: trustrule.BundleID(bundle.ID), BundleVersion: bundle.Active,
	}
}

// trustRecheckKindExchange returns an exchange of kind with no rule and no
// bundle.
func trustRecheckKindExchange(kind accesstoken.Kind) trustrule.Exchange {
	var tok accesstoken.Token
	tok.Kind = kind

	return trustrule.Exchange{
		Token: tok, Digest: accesstoken.Digest{}, Rule: "", SPIFFEID: "", BundleID: "", BundleVersion: 0,
	}
}

// trustRecheckNoRow returns a rule row with no value.
func trustRecheckNoRow() trustrulestore.Row {
	return trustrulestore.Row{
		ID: "", Namespace: "", Name: "", Issuer: "", Audience: "", KeySource: "", StaticKeys: "",
		ConditionsJSON: "", PrincipalID: "", CreatedBy: "", CreatedAtMs: 0, Revision: 0,
		SpiffeIDPattern: "", BundleID: "",
	}
}

// TestRecheckExchangeRefusesAnUnknownKind sends an exchange of a kind that no
// exchange makes directly to the recheck, as if CheckExchange had failed to
// refuse it. The recheck must fail closed and read no row.
func TestRecheckExchangeRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	kinds := []accesstoken.Kind{accesstoken.KindPersonal, accesstoken.KindService, "other", ""}
	for _, kind := range kinds {
		x := trustRecheckKindExchange(kind)

		err := recheckExchange(t.Context(), new(db.Queries), namespace.Name("acme"), trustRecheckNoRow(), &x)
		if !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("kind %q: error = %v, want ErrInvalid", kind, err)
		}
	}
}

// TestRecheckExchangeNamesTheJWTKind checks that a jwt-exchange passes with no
// bundle check and no row read.
func TestRecheckExchangeNamesTheJWTKind(t *testing.T) {
	t.Parallel()

	x := trustRecheckKindExchange(accesstoken.KindJWTExchange)

	err := recheckExchange(t.Context(), new(db.Queries), namespace.Name("acme"), trustRecheckNoRow(), &x)
	if err != nil {
		t.Errorf("error = %v, want nil", err)
	}
}
