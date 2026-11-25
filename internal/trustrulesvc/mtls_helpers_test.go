package trustrulesvc_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// spiffeWorker is the SPIFFE ID of the workload that the tests exchange for.
const spiffeWorker = "spiffe://example.org/ns/acme/sa/worker"

// testCA is a CA of the tests: ECDSA, valid from 2020 to 2100.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

// commonName returns a subject with a common name only.
func commonName(cn string) pkix.Name {
	var name pkix.Name
	name.CommonName = cn

	return name
}

// serial returns a random serial number.
func serial(t *testing.T) *big.Int {
	t.Helper()

	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}

	return n
}

// newTestCA makes a self-signed CA called cn.
func newTestCA(t *testing.T, cn string) *testCA {
	t.Helper()

	return signCA(t, cn, nil)
}

// signCA makes a CA called cn. A nil parent gives a self-signed CA.
func signCA(t *testing.T, cn string, parent *testCA) *testCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := new(x509.Certificate)
	tmpl.SerialNumber = serial(t)
	tmpl.Subject = commonName(cn)
	tmpl.NotBefore = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.NotAfter = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.KeyUsage = x509.KeyUsageCertSign

	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}

	return &testCA{
		cert: cert,
		key:  key,
		pem:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: der})),
	}
}

// leafSpec describes a leaf certificate.
type leafSpec struct {
	uris      []string
	notBefore time.Time
	notAfter  time.Time
	isCA      bool
	ekus      []x509.ExtKeyUsage
	// padding is the number of DNS names that make the leaf larger.
	padding int
}

// goodLeaf is a leaf for the SPIFFE ID id that is valid at exchangeNow for
// 30 minutes.
func goodLeaf(id string) leafSpec {
	return leafSpec{
		uris: []string{id}, notBefore: exchangeNow().Add(-time.Hour), notAfter: exchangeNow().Add(30 * time.Minute),
		isCA: false, ekus: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, padding: 0,
	}
}

// leaf returns the DER of a leaf certificate that ca signs.
func (ca *testCA) leaf(t *testing.T, s leafSpec) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	uris := make([]*url.URL, 0, len(s.uris))

	for _, raw := range s.uris {
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			t.Fatalf("parse URI %q: %v", raw, parseErr)
		}

		uris = append(uris, u)
	}

	tmpl := new(x509.Certificate)
	tmpl.SerialNumber = serial(t)
	tmpl.Subject = commonName("workload")
	tmpl.NotBefore = s.notBefore
	tmpl.NotAfter = s.notAfter
	tmpl.URIs = uris
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = s.ekus
	tmpl.IsCA = s.isCA
	tmpl.BasicConstraintsValid = s.isCA
	tmpl.DNSNames = paddingNames(s.padding)

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}

	return der
}

// padded returns s with n DNS names that make the leaf larger.
func padded(s leafSpec, n int) leafSpec {
	s.padding = n

	return s
}

// paddingNames returns n DNS names of 60 characters each.
func paddingNames(n int) []string {
	names := make([]string, 0, n)

	for i := range n {
		names = append(names, fmt.Sprintf("%053d.example.com", i))
	}

	return names
}

// noRevocation is a revocation check that finds nothing.
func noRevocation(*x509.Certificate) error { return nil }

// pool returns a pool with the certificates of the CAs.
func pool(cas ...*testCA) *x509.CertPool {
	p := x509.NewCertPool()
	for _, ca := range cas {
		p.AddCert(ca.cert)
	}

	return p
}

// mtlsEnv is the real SQLite store with the namespaces acme and other, a
// service principal in each, a trust bundle in each, and one rule of acme for
// spiffeWorker.
type mtlsEnv struct {
	t      *testing.T
	store  *sqlite.Store
	tokens *accesstoken.Service
	clock  *atomic.Int64
	// caAcme and caOther are the CAs of the bundles.
	caAcme, caOther *testCA
	// bundleAcme is the bundle of acme.
	bundleAcme trustrule.Bundle
	// worker is a service principal of acme, and stranger one of other.
	worker, stranger string
	// rule is the rule of acme for spiffeWorker.
	rule trustrule.Rule
}

// now is the clock of the environment, in Unix milliseconds.
func (e *mtlsEnv) now() time.Time { return time.UnixMilli(e.clock.Load()).UTC() }

// advance moves the clock of the environment.
func (e *mtlsEnv) advance(d time.Duration) { e.clock.Add(d.Milliseconds()) }

func newMtlsEnv(t *testing.T) *mtlsEnv {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	clock := &atomic.Int64{}
	clock.Store(exchangeNow().UnixMilli())

	e := new(mtlsEnv)
	e.t, e.store, e.clock = t, s, clock
	e.caAcme, e.caOther = newTestCA(t, "ca acme"), newTestCA(t, "ca other")

	for _, name := range []namespace.Name{"acme", "other"} {
		ns := namespace.Namespace{
			ID: namespace.NewID(), CreatedAt: exchangeNow(), Name: name, PrimaryInstallation: "eu-west",
			FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		}
		if createErr := s.CreateNamespace(t.Context(), ns); createErr != nil {
			t.Fatalf("create namespace: %v", createErr)
		}
	}

	e.worker = e.principal("acme", principal.KindService)
	e.stranger = e.principal("other", principal.KindService)
	e.bundleAcme = e.saveBundle("acme", e.caAcme)
	e.saveBundle("other", e.caOther)
	e.rule = e.createRule("acme", "worker", spiffeWorker, e.worker, e.bundleAcme.ID)

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Makers: nil,
		Reach: access.ReachConfig{
			Grants: s, Roles: s, Callers: s, Principals: s, Collections: nil, Units: s,
		},
		Store: s, Principals: s, Guard: noFreshStepGuard(t), Authorizer: nil, TempAdmins: nil, Logger: nil,
		Now: e.now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	e.tokens = tokens

	return e
}

// mtlsTestEntry returns a valid entry of the action in ns.
func mtlsTestEntry(ns namespace.Name, action, objectID string) audit.Entry {
	e := emptyEntry()
	e.At = exchangeNow()
	e.Namespace = ns
	e.Action = action
	e.Object = ns.String()
	e.ObjectID = objectID
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.SourceIP = "192.0.2.1"

	return e
}

// principal stores a principal of type kind in ns and returns its ID.
func (e *mtlsEnv) principal(ns namespace.Name, kind principal.Kind) string {
	e.t.Helper()

	id := "prn-" + ns.String() + "-" + string(kind)
	p := principal.Principal{
		CreatedAt: exchangeNow(), UpdatedAt: exchangeNow(), DisabledAt: time.Time{}, ID: id, Subject: id,
		DisplayName: id, Kind: kind, Source: principal.SourceLocal, Namespace: ns, GroupCount: 0, BreakGlass: false,
	}

	n, err := e.store.GetNamespace(e.t.Context(), ns)
	if err != nil {
		e.t.Fatalf("read namespace: %v", err)
	}

	entry := mtlsTestEntry(ns, "server:manage-principals", "")
	entry.Object = principal.Ref(id)

	if err := e.store.CreateNamespacePrincipal(e.t.Context(), p, n.Fence(), entry); err != nil {
		e.t.Fatalf("create principal: %v", err)
	}

	return id
}

// saveBundle stores the CA as the first version of the bundle of ns.
func (e *mtlsEnv) saveBundle(ns namespace.Name, ca *testCA) trustrule.Bundle {
	e.t.Helper()

	n, err := e.store.GetNamespace(e.t.Context(), ns)
	if err != nil {
		e.t.Fatalf("read namespace: %v", err)
	}

	b, err := e.store.TrustRules().SaveBundle(e.t.Context(), ns, trustrule.BundleFields{
		PEM: ca.pem, Overlap: 0, Revision: 0,
	}, n.Fence(), mtlsTestEntry(ns, "trust-bundle:save", ""))
	if err != nil {
		e.t.Fatalf("save bundle: %v", err)
	}

	return b
}

// rotate stores the CA as a new active version of the bundle of acme, with an
// overlap of one hour.
func (e *mtlsEnv) rotate(ca *testCA) {
	e.t.Helper()

	n, err := e.store.GetNamespace(e.t.Context(), "acme")
	if err != nil {
		e.t.Fatalf("read namespace: %v", err)
	}

	b, err := e.store.TrustRules().GetBundle(e.t.Context(), "acme")
	if err != nil {
		e.t.Fatalf("get bundle: %v", err)
	}

	_, err = e.store.TrustRules().RotateBundle(e.t.Context(), "acme", trustrule.BundleFields{
		PEM: ca.pem, Overlap: time.Hour, Revision: b.Revision,
	}, n.Fence(), mtlsTestEntry("acme", "trust-bundle:rotate", ""))
	if err != nil {
		e.t.Fatalf("rotate bundle: %v", err)
	}
}

// createRule stores a rule of ns for the SPIFFE ID spiffeID in the bundle
// bundleID, for the principal principalID.
func (e *mtlsEnv) createRule(
	ns namespace.Name, name, spiffeID, principalID string, bundleID trustrule.BundleID,
) trustrule.Rule {
	e.t.Helper()

	r := trustrule.Rule{
		CreatedAt: exchangeNow(), ID: trustrule.NewID(), Namespace: ns, Name: name,
		Issuer: "https://issuer.example.com", Audience: "https://dustpan.example.com",
		Keys: trustrule.KeySourceDiscovery, StaticKeys: "", PrincipalID: principalID, CreatedBy: "ada",
		Conditions: []trustrule.Condition{{Claim: "sub", Values: []string{"workload"}}}, Revision: 1,
		SpiffeIDPattern: spiffeID, Bundle: string(bundleID),
	}

	n, err := e.store.GetNamespace(e.t.Context(), ns)
	if err != nil {
		e.t.Fatalf("read namespace: %v", err)
	}

	entry := mtlsTestEntry(ns, "trust-rule:create", string(r.ID))
	if err := e.store.TrustRules().Create(e.t.Context(), r, n.Fence(), entry); err != nil {
		e.t.Fatalf("create rule: %v", err)
	}

	return r
}

// config returns the config of an exchanger over the real store. change edits
// it before the exchanger is made.
func (e *mtlsEnv) config(change func(*trustrulesvc.MtlsConfig)) trustrulesvc.MtlsConfig {
	e.t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: e.store, Now: nil, Installation: "eu-west"})
	if err != nil {
		e.t.Fatalf("namespace service: %v", err)
	}

	cfg := trustrulesvc.MtlsConfig{
		Rules: e.store.TrustRules(), Exchanges: e.store.TrustRules(), Bundles: e.store.TrustRules(),
		Namespaces: namespaces, Principals: e.store, Tokens: e.tokens, Audit: e.store, ServerAudit: e.store,
		Refusals: nil, Revocation: noRevocation, Now: e.now, Log: nil,
	}

	if change != nil {
		change(&cfg)
	}

	return cfg
}

// exchanger returns an exchanger over the real store.
func (e *mtlsEnv) exchanger(change func(*trustrulesvc.MtlsConfig)) *trustrulesvc.MtlsExchanger {
	e.t.Helper()

	x, err := trustrulesvc.NewMtlsExchanger(e.config(change))
	if err != nil {
		e.t.Fatalf("exchanger: %v", err)
	}

	return x
}

// verify runs Verify of x for the namespace ns.
func verify(
	ctx context.Context, x *trustrulesvc.MtlsExchanger, ns namespace.Name, raw [][]byte,
) (trustrulesvc.VerifiedChain, error) {
	return x.Verify(ctx, &trustrulesvc.MtlsVerifyInput{Namespace: ns, RawChain: raw, Request: emptyEntry()})
}

// exchange runs Exchange of x for the namespace ns.
func exchange(
	ctx context.Context, x *trustrulesvc.MtlsExchanger, ns namespace.Name, c trustrulesvc.VerifiedChain,
) (trustrulesvc.MtlsExchangeResult, error) {
	return x.Exchange(ctx, &trustrulesvc.MtlsExchangeInput{Namespace: ns, Chain: c, Request: emptyEntry()})
}

// tokenCount returns the number of access tokens of the principal id.
func (e *mtlsEnv) tokenCount(id string) int {
	e.t.Helper()

	list, _, err := e.store.ListTokens(e.t.Context(), accesstoken.Query{
		At: e.now(), AfterCreatedAt: time.Time{}, PrincipalID: id, AfterID: "", Filter: accesstoken.FilterAll,
		Limit: 100, HasAfter: false,
	})
	if err != nil {
		e.t.Fatalf("list tokens: %v", err)
	}

	return len(list)
}

// exchangeEntries returns the entries of the exchange action in ns.
func (e *mtlsEnv) exchangeEntries(ns namespace.Name) []audit.Entry {
	e.t.Helper()

	l, err := e.store.List(e.t.Context(), ns, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		e.t.Fatalf("list audit: %v", err)
	}

	out := make([]audit.Entry, 0, len(l.Entries))

	for i := range l.Entries {
		if l.Entries[i].Action == trustrulesvc.ActionExchange {
			out = append(out, l.Entries[i])
		}
	}

	return out
}

// mustRefuse fails the test unless err wraps want, the principal got no token
// and exactly one denial entry exists in ns with the outcome denied and no
// success entry.
func (e *mtlsEnv) mustRefuse(err, want error, ns namespace.Name, principalID string) {
	e.t.Helper()

	if !isErr(err, want) {
		e.t.Fatalf("error = %v, want %v", err, want)
	}

	if n := e.tokenCount(principalID); n != 0 {
		e.t.Fatalf("principal %s holds %d tokens after a refusal", principalID, n)
	}

	entries := e.exchangeEntries(ns)
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied || entries[0].TokenID != "" {
		e.t.Fatalf("entries = %+v, want one denial entry", entries)
	}
}

// errDiskFull is the store error of the service tests.
var errDiskFull = errors.New("disk full")

// isErr reports whether err wraps want.
func isErr(err, want error) bool { return errors.Is(err, want) }
