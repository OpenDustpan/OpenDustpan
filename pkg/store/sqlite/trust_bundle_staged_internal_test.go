package sqlite

// Hostile staged rows of the trust bundles and rules: each change makes the
// stored rows fail the rules of a stored row, and the restore check refuses
// them with backup.ErrCorrupt. A staged row that the store wrote passes.

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

// restoreAt is the clock of the restore check: the test CA is valid at it.
func stagedRestoreAt() time.Time {
	return time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
}

// stagedCAPEM returns the PEM text of one CA certificate that is valid from
// 2020 to 2030. The dates are fixed, so the test needs no wall clock.
func stagedCAPEM(t *testing.T) string {
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

// trustEntry returns a valid audit entry for a write in ns. The time is the
// fixed clock of the restore check, so a saved CA is valid at it.
func stagedTrustEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: stagedRestoreAt(), Namespace: ns, Subject: "ana", IDPSubject: "", TokenID: "",
		Action: "trust-rule:create", Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// seedTrustNamespace makes the namespace with a service principal, and
// returns the fence and the principal ID.
func seedTrustNamespace(t *testing.T, s *Store, name namespace.Name) (namespace.Fence, string) {
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

// seedTrustRule stores one valid rule of ns for the principal, with no
// bundle.
func seedTrustRule(t *testing.T, s *Store, fence namespace.Fence, ns namespace.Name, principalID string) {
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
}

// seedTrustBundle saves one CA as the bundle of ns, and returns its ID.
func seedTrustBundle(t *testing.T, s *Store, fence namespace.Fence, ns namespace.Name) string {
	t.Helper()

	b, err := s.TrustRules().SaveBundle(
		t.Context(), ns, trustrule.BundleFields{PEM: stagedCAPEM(t), Overlap: 0, Revision: 0}, fence,
		stagedTrustEntry(ns),
	)
	if err != nil {
		t.Fatalf("save trust bundle: %v", err)
	}

	return string(b.ID)
}

// seedTrustStage makes the namespaces acme and other, a rule of acme with no
// bundle, and a bundle of each namespace. It returns the bundle of other.
func seedTrustStage(t *testing.T, s *Store) string {
	t.Helper()

	fence, worker := seedTrustNamespace(t, s, "acme")
	seedTrustRule(t, s, fence, "acme", worker)
	seedTrustBundle(t, s, fence, "acme")

	otherFence, _ := seedTrustNamespace(t, s, "other")

	return seedTrustBundle(t, s, otherFence, "other")
}

// tamperTrustStage changes the stored rows as stmt says, with the foreign
// keys and the check constraints off: the state of a damaged or hostile
// backup file that holds rows that no constraint allowed.
func tamperTrustStage(t *testing.T, s *Store, stmt string, args ...any) {
	t.Helper()

	for _, pragma := range []string{`PRAGMA foreign_keys = OFF`, `PRAGMA ignore_check_constraints = ON`} {
		if _, err := s.conn.ExecContext(t.Context(), pragma); err != nil {
			t.Fatalf("disable a constraint: %v", err)
		}
	}

	if _, err := s.conn.ExecContext(t.Context(), stmt, args...); err != nil {
		t.Fatalf("change the rows: %v", err)
	}
}

// runTrustTampering tampers the bundle row that stmt names, checks that the
// restore check refuses the staged rows with backup.ErrCorrupt, and uses
// otherBundle as the tamper value when resolve is not nil.
func runTrustTampering(t *testing.T, stmt string, args []any, resolve func(other string) []any) {
	t.Helper()

	s := openInternalTemplated(t)
	other := seedTrustStage(t, s)

	if resolve != nil {
		args = resolve(other)
	}

	tamperTrustStage(t, s, stmt, args...)

	err := checkStagedTrustRules(t.Context(), s.conn, stagedRestoreAt())
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}
}

// withOtherBundle resolves the tamper value to the bundle of the other
// namespace.
func withOtherBundle(other string) []any { return []any{other} }

// TestCheckStagedTrustRulesAndBundles proves the restore check: the rows that
// the store wrote pass, and each hostile staged row is refused with
// backup.ErrCorrupt. Each case fails only through the staged trust check.
func TestCheckStagedTrustRulesAndBundles(t *testing.T) {
	t.Parallel()

	t.Run("rows that the store wrote pass", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedTrustStage(t, s)

		if err := checkStagedTrustRules(t.Context(), s.conn, stagedRestoreAt()); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	tamperings := []struct {
		name    string
		stmt    string
		args    []any
		resolve func(other string) []any
	}{
		{"bad PEM", `UPDATE trust_bundle_versions SET pem = 'not-a-pem'`, nil, nil},
		{
			"oversize version",
			`UPDATE trust_bundle_versions SET pem = ?`,
			[]any{strings.Repeat("a", trustrule.MaxBundleBytes+1)},
			nil,
		},
		{
			"active version with no row",
			`UPDATE trust_bundles SET active_version = 99 WHERE namespace = 'acme'`,
			nil,
			nil,
		},
		{
			"revision 0",
			`UPDATE trust_bundles SET revision = 0 WHERE namespace = 'acme'`,
			nil,
			nil,
		},
		{
			"rule with the bundle of another namespace",
			`UPDATE trust_rules SET bundle_id = ?`,
			nil,
			withOtherBundle,
		},
	}

	for _, tc := range tamperings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runTrustTampering(t, tc.stmt, tc.args, tc.resolve)
		})
	}
}
