package postgres

// Hostile staged rows of the trust bundles and rules: each change makes the
// stored rows fail the rules of a stored row, and the restore check refuses
// them with backup.ErrCorrupt. A staged row that the store wrote passes.
// The test skips without a PostgreSQL database.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// stagedTrustRestoreAt is the clock of the restore check: the test CA is
// valid at it.
func stagedTrustRestoreAt() time.Time {
	return time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
}

// stagedTrustCAPEM returns the PEM text of one CA certificate that is valid
// from 2020 to 2030. The dates are fixed, so the test needs no wall clock.
func stagedTrustCAPEM(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = big.NewInt(1)
	tmpl.Subject.CommonName = "test ca"
	tmpl.NotBefore = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	tmpl.NotAfter = time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.KeyUsage = x509.KeyUsageCertSign

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	var block pem.Block

	block.Type = "CERTIFICATE"
	block.Bytes = der
	block.Headers = nil

	return string(pem.EncodeToMemory(&block))
}

// stagedTrustEntry returns a valid audit entry for a write in ns. The time
// is the fixed clock of the restore check, so a saved CA is valid at it.
func stagedTrustEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: stagedTrustRestoreAt(), Namespace: ns, Subject: "ana", IDPSubject: "", TokenID: "",
		Action: "trust-rule:create",
		Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
		JobID: "", Reason: "", Origin: nil,
	}
}

// seedStagedTrustNamespace makes the namespace with a service principal, and
// returns the fence and the principal ID.
func seedStagedTrustNamespace(t *testing.T, s *Store, name namespace.Name) (namespace.Fence, string) {
	t.Helper()

	overrides := namespace.Overrides{Forced: false, Unlocked: false}
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: name,
		PrimaryInstallation: "eu-west", FailoverVersion: 1, Overrides: overrides,
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fence := ns.Fence()

	p := principal.Principal{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), UpdatedAt: time.UnixMilli(1_700_000_000_000).UTC(),
		DisabledAt: time.Time{}, ID: "prn-" + name.String(), Subject: "subject-" + name.String(),
		DisplayName: "worker", Kind: principal.KindService, Source: principal.SourceLocal, Namespace: name,
		GroupCount: 0, BreakGlass: false,
	}
	if err := s.CreateNamespacePrincipal(t.Context(), p, fence, stagedTrustEntry(name)); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	return fence, p.ID
}

// seedStagedTrustRule stores one valid rule of ns for the principal, with no
// bundle, and returns its ID.
func seedStagedTrustRule(
	t *testing.T, s *Store, fence namespace.Fence, ns namespace.Name, principalID string,
) string {
	t.Helper()

	r := trustrule.Rule{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), ID: trustrule.NewID(), Namespace: ns, Name: "deploy",
		Issuer: "https://token.actions.example.com", Audience: "https://dustpan.example.com",
		Keys: trustrule.KeySourceDiscovery, StaticKeys: "", PrincipalID: principalID, CreatedBy: "ana",
		Revision: 1, SpiffeIDPattern: "", Bundle: "",
		Conditions: []trustrule.Condition{
			{Claim: "repository", Values: []string{"acme/infra"}},
			{Claim: "sub", Values: []string{"repo:acme/infra"}},
		},
	}

	if err := s.TrustRules().Create(t.Context(), r, fence, stagedTrustEntry(ns)); err != nil {
		t.Fatalf("create trust rule: %v", err)
	}

	return string(r.ID)
}

// seedStagedTrustBundle saves one CA as the bundle of ns, and returns its ID.
func seedStagedTrustBundle(t *testing.T, s *Store, fence namespace.Fence, ns namespace.Name) string {
	t.Helper()

	b, err := s.TrustRules().SaveBundle(
		t.Context(), ns, trustrule.BundleFields{PEM: stagedTrustCAPEM(t), Overlap: 0, Revision: 0}, fence,
		stagedTrustEntry(ns),
	)
	if err != nil {
		t.Fatalf("save trust bundle: %v", err)
	}

	return string(b.ID)
}

// seedStagedTrust makes the namespaces acme and other, a rule of acme with no
// bundle, and a bundle of each namespace. It returns the bundle of other.
func seedStagedTrust(t *testing.T, s *Store) string {
	t.Helper()

	fence, worker := seedStagedTrustNamespace(t, s, "acme")
	seedStagedTrustRule(t, s, fence, "acme", worker)
	seedStagedTrustBundle(t, s, fence, "acme")

	otherFence, _ := seedStagedTrustNamespace(t, s, "other")

	return seedStagedTrustBundle(t, s, otherFence, "other")
}

// tamperStagedTrust runs the statements that change the stored rows as a
// damaged or hostile backup file would hold them.
func tamperStagedTrust(t *testing.T, s *Store, stmts []string, args ...any) {
	t.Helper()

	for _, stmt := range stmts {
		// Only a statement with a placeholder takes the arguments.
		stmtArgs := args
		if !strings.Contains(stmt, "$1") {
			stmtArgs = nil
		}

		if _, err := s.pool.Exec(t.Context(), stmt, stmtArgs...); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// TestCheckStagedTrustRulesAndBundles proves the restore check: the rows that
// the store wrote pass, and each hostile staged row is refused with
// backup.ErrCorrupt. Each case fails only through the staged trust check.
func TestCheckStagedTrustRulesAndBundles(t *testing.T) {
	t.Parallel()

	t.Run("rows that the store wrote pass", func(t *testing.T) {
		t.Parallel()

		s := openForSavedViews(t)
		seedStagedTrust(t, s)

		if err := checkStagedTrustRules(t.Context(), s.pool, currentSchema(t, s), stagedTrustRestoreAt()); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	tamperings := []struct {
		name    string
		stmts   []string
		args    []any
		resolve func(other string) []any
	}{
		{"bad PEM", []string{`UPDATE trust_bundle_versions SET pem = 'not-a-pem'`}, nil, nil},
		{
			"oversize version",
			[]string{`UPDATE trust_bundle_versions SET pem = $1`},
			[]any{strings.Repeat("a", trustrule.MaxBundleBytes+1)},
			nil,
		},
		{
			"active version with no row",
			[]string{`UPDATE trust_bundles SET active_version = 99 WHERE namespace = 'acme'`},
			nil,
			nil,
		},
		{
			"revision 0",
			[]string{
				`ALTER TABLE trust_bundles DROP CONSTRAINT trust_bundles_revision_check`,
				`UPDATE trust_bundles SET revision = 0 WHERE namespace = 'acme'`,
			},
			nil,
			nil,
		},
		{
			"rule with the bundle of another namespace",
			[]string{
				`ALTER TABLE trust_rules DROP CONSTRAINT trust_rules_namespace_bundle_id_fkey`,
				`UPDATE trust_rules SET bundle_id = $1`,
			},
			nil,
			withStagedOtherBundle,
		},
	}

	for _, tc := range tamperings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runStagedTrustTampering(t, tc.stmts, tc.args, tc.resolve)
		})
	}
}

// runStagedTrustTampering tampers the staged rows with stmts, checks that the
// restore check refuses them with backup.ErrCorrupt, and uses resolve as the
// tamper value when it is not nil.
func runStagedTrustTampering(t *testing.T, stmts []string, args []any, resolve func(other string) []any) {
	t.Helper()

	s := openForSavedViews(t)
	other := seedStagedTrust(t, s)

	if resolve != nil {
		args = resolve(other)
	}

	tamperStagedTrust(t, s, stmts, args...)

	err := checkStagedTrustRules(t.Context(), s.pool, currentSchema(t, s), stagedTrustRestoreAt())
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}
}

// withStagedOtherBundle resolves the tamper value to the bundle of the other
// namespace.
func withStagedOtherBundle(other string) []any { return []any{other} }
