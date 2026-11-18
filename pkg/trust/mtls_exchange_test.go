package trust_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"math/big"
	"net/url"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trust"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

var errResponderDown = errors.New("responder down")

// nsAcme is the namespace of the exchange tests.
const nsAcme = namespace.Name("acme")

func mtlsTestNow() time.Time {
	return time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
}

func makeTestCA(t *testing.T, notBefore, notAfter time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()

	key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if keyErr != nil {
		t.Fatalf("generate CA key: %v", keyErr)
	}

	serial, serialErr := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if serialErr != nil {
		t.Fatalf("serial: %v", serialErr)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = serial
	tmpl.Subject.CommonName = "test CA"
	tmpl.NotBefore = notBefore
	tmpl.NotAfter = notAfter
	tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	tmpl.BasicConstraintsValid = true
	tmpl.IsCA = true

	der, createErr := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if createErr != nil {
		t.Fatalf("create CA: %v", createErr)
	}

	cert, parseErr := x509.ParseCertificate(der)
	if parseErr != nil {
		t.Fatalf("parse CA: %v", parseErr)
	}

	return cert, key, der
}

// leafConfig names the leaf of a verify test.
type leafConfig struct {
	SpiffeIDs []string
	NotBefore time.Time
	NotAfter  time.Time
}

func makeTestLeaf(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cfg leafConfig) []byte {
	t.Helper()

	key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if keyErr != nil {
		t.Fatalf("generate leaf key: %v", keyErr)
	}

	serial, serialErr := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if serialErr != nil {
		t.Fatalf("serial: %v", serialErr)
	}

	uris := make([]*url.URL, 0, len(cfg.SpiffeIDs))

	for _, id := range cfg.SpiffeIDs {
		parsed, parseErr := url.Parse(id)
		if parseErr != nil {
			t.Fatalf("parse SPIFFE ID %q: %v", id, parseErr)
		}

		uris = append(uris, parsed)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = serial
	tmpl.Subject.CommonName = "workload"
	tmpl.NotBefore = cfg.NotBefore
	tmpl.NotAfter = cfg.NotAfter
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	tmpl.BasicConstraintsValid = true
	tmpl.URIs = uris

	der, createErr := x509.CreateCertificate(rand.Reader, &tmpl, caCert, &key.PublicKey, caKey)
	if createErr != nil {
		t.Fatalf("create leaf: %v", createErr)
	}

	return der
}

func rootsOf(t *testing.T, ca *x509.Certificate) *x509.CertPool {
	t.Helper()

	pool := x509.NewCertPool()
	pool.AddCert(ca)

	return pool
}

func goodChecker(_ *x509.Certificate) error {
	return nil
}

func newMtlsRule(pattern, principalID, name string) trustrule.Rule {
	return trustrule.Rule{
		CreatedAt:       mtlsTestNow(),
		ID:              trustrule.NewID(),
		Namespace:       namespace.Name("acme"),
		Name:            name,
		Issuer:          "https://token.actions.example.com",
		Audience:        "https://dustpan.example.com",
		Keys:            trustrule.KeySourceDiscovery,
		StaticKeys:      "",
		PrincipalID:     principalID,
		CreatedBy:       "alice",
		Conditions:      []trustrule.Condition{{Claim: "sub", Values: []string{"repo:acme/infra"}}},
		Revision:        1,
		SpiffeIDPattern: pattern,
		Bundle:          "trb-1",
	}
}

// verifySetup holds the chain inputs of the verify tests.
type verifySetup struct {
	Now      time.Time
	Roots    *x509.CertPool
	LeafDER  []byte
	SPIFFEID string
}

func newVerifySetup(t *testing.T) verifySetup {
	t.Helper()

	now := mtlsTestNow()
	ca, caKey, _ := makeTestCA(t, now.Add(-time.Hour), now.Add(24*time.Hour))
	spiffeID := "spiffe://ci.example.com/ns/acme/sa/deploy"
	leafDER := makeTestLeaf(t, ca, caKey, leafConfig{
		SpiffeIDs: []string{spiffeID}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
	})

	return verifySetup{Now: now, Roots: rootsOf(t, ca), LeafDER: leafDER, SPIFFEID: spiffeID}
}

func checkValidChain(t *testing.T, setup verifySetup) {
	t.Helper()

	chain, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, goodChecker)
	if verifyErr != nil {
		t.Fatalf("VerifyChain = %v, want nil", verifyErr)
	}

	if chain.SPIFFEID() != setup.SPIFFEID || chain.Leaf() == nil || !chain.VerifiedAt().Equal(setup.Now) {
		t.Fatalf("chain = %q, %v, %v; want %q verified at %v", chain.SPIFFEID(), chain.Leaf() != nil,
			chain.VerifiedAt(), setup.SPIFFEID, setup.Now)
	}

	rule := newMtlsRule(setup.SPIFFEID, "prn-worker", "deploy")

	got, matchErr := trust.MatchRule([]trustrule.Rule{rule}, nsAcme, chain.SPIFFEID(), "trb-1")
	if matchErr != nil {
		t.Fatalf("MatchRule = %v, want nil", matchErr)
	}

	if got.SpiffeIDPattern != setup.SPIFFEID {
		t.Fatalf("matched pattern = %q, want %q", got.SpiffeIDPattern, setup.SPIFFEID)
	}
}

func checkWrongCA(t *testing.T, setup verifySetup) {
	t.Helper()

	otherCA, _, _ := makeTestCA(t, setup.Now.Add(-time.Hour), setup.Now.Add(24*time.Hour))

	_, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, rootsOf(t, otherCA), setup.Now, goodChecker)
	if !errors.Is(verifyErr, trust.ErrUnknownIssuer) {
		t.Fatalf("VerifyChain with another CA = %v, want ErrUnknownIssuer", verifyErr)
	}
}

func checkExpiredLeaf(t *testing.T, setup verifySetup) {
	t.Helper()

	ca, caKey, _ := makeTestCA(t, setup.Now.Add(-time.Hour), setup.Now.Add(24*time.Hour))
	expired := makeTestLeaf(t, ca, caKey, leafConfig{
		SpiffeIDs: []string{setup.SPIFFEID}, NotBefore: setup.Now.Add(-2 * time.Hour),
		NotAfter: setup.Now.Add(-time.Hour),
	})
	roots := rootsOf(t, ca)

	_, verifyErr := trust.VerifyChain([][]byte{expired}, roots, setup.Now, goodChecker)
	if !errors.Is(verifyErr, trust.ErrExpired) {
		t.Fatalf("VerifyChain expired = %v, want ErrExpired", verifyErr)
	}
}

func checkNotYetValidLeaf(t *testing.T, setup verifySetup) {
	t.Helper()

	ca, caKey, _ := makeTestCA(t, setup.Now.Add(-time.Hour), setup.Now.Add(48*time.Hour))
	future := makeTestLeaf(t, ca, caKey, leafConfig{
		SpiffeIDs: []string{setup.SPIFFEID}, NotBefore: setup.Now.Add(time.Hour),
		NotAfter: setup.Now.Add(2 * time.Hour),
	})
	roots := rootsOf(t, ca)

	_, verifyErr := trust.VerifyChain([][]byte{future}, roots, setup.Now, goodChecker)
	if !errors.Is(verifyErr, trust.ErrNotYetValid) {
		t.Fatalf("VerifyChain not yet valid = %v, want ErrNotYetValid", verifyErr)
	}
}

func checkRevokedLeaf(t *testing.T, setup verifySetup) {
	t.Helper()

	revoked := func(_ *x509.Certificate) error { return trust.ErrRevoked }

	_, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, revoked)
	if !errors.Is(verifyErr, trust.ErrRevoked) {
		t.Fatalf("VerifyChain revoked = %v, want ErrRevoked", verifyErr)
	}
}

func checkUnknownRevocation(t *testing.T, setup verifySetup) {
	t.Helper()

	_, nilErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, nil)
	if !errors.Is(nilErr, trust.ErrRevocationUnknown) {
		t.Fatalf("VerifyChain with no checker = %v, want ErrRevocationUnknown", nilErr)
	}

	unknown := func(_ *x509.Certificate) error { return errResponderDown }

	_, unknownErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, unknown)
	if !errors.Is(unknownErr, trust.ErrRevocationUnknown) {
		t.Fatalf("VerifyChain unknown = %v, want ErrRevocationUnknown", unknownErr)
	}
}

func checkChainBounds(t *testing.T, setup verifySetup) {
	t.Helper()

	oversize := make([]byte, trust.MaxChainBytes+1)

	_, oversizeErr := trust.VerifyChain([][]byte{oversize}, setup.Roots, setup.Now, goodChecker)
	if !errors.Is(oversizeErr, trust.ErrInvalid) {
		t.Fatalf("VerifyChain oversize = %v, want ErrInvalid", oversizeErr)
	}

	raw := make([][]byte, trust.MaxChainDepth+1)
	for i := range raw {
		raw[i] = setup.LeafDER
	}

	_, deepErr := trust.VerifyChain(raw, setup.Roots, setup.Now, goodChecker)
	if !errors.Is(deepErr, trust.ErrInvalid) {
		t.Fatalf("VerifyChain deep = %v, want ErrInvalid", deepErr)
	}
}

func checkSPIFFEShape(t *testing.T, setup verifySetup) {
	t.Helper()

	ca, caKey, _ := makeTestCA(t, setup.Now.Add(-time.Hour), setup.Now.Add(24*time.Hour))
	roots := rootsOf(t, ca)

	bare := makeTestLeaf(t, ca, caKey, leafConfig{
		SpiffeIDs: nil, NotBefore: setup.Now.Add(-time.Hour), NotAfter: setup.Now.Add(time.Hour),
	})

	_, bareErr := trust.VerifyChain([][]byte{bare}, roots, setup.Now, goodChecker)
	if !errors.Is(bareErr, trust.ErrInvalid) {
		t.Fatalf("VerifyChain with no SPIFFE ID = %v, want ErrInvalid", bareErr)
	}

	two := makeTestLeaf(t, ca, caKey, leafConfig{
		SpiffeIDs: []string{setup.SPIFFEID, "spiffe://ci.example.com/ns/acme/sa/other"},
		NotBefore: setup.Now.Add(-time.Hour), NotAfter: setup.Now.Add(time.Hour),
	})

	_, twoErr := trust.VerifyChain([][]byte{two}, roots, setup.Now, goodChecker)
	if !errors.Is(twoErr, trust.ErrInvalid) {
		t.Fatalf("VerifyChain with two SPIFFE IDs = %v, want ErrInvalid", twoErr)
	}
}

func checkMatchCases(t *testing.T, setup verifySetup) {
	t.Helper()

	rule := newMtlsRule(setup.SPIFFEID, "prn-worker", "deploy")

	_, noMatchErr := trust.MatchRule(
		[]trustrule.Rule{rule}, nsAcme, "spiffe://ci.example.com/ns/acme/sa/other", "trb-1",
	)
	if !errors.Is(noMatchErr, trust.ErrNoMatchingRule) {
		t.Fatalf("MatchRule with no match = %v, want ErrNoMatchingRule", noMatchErr)
	}

	_, bundleErr := trust.MatchRule([]trustrule.Rule{rule}, nsAcme, setup.SPIFFEID, "trb-other")
	if !errors.Is(bundleErr, trust.ErrNoMatchingRule) {
		t.Fatalf("MatchRule with another bundle = %v, want ErrNoMatchingRule", bundleErr)
	}

	second := newMtlsRule(setup.SPIFFEID, "prn-two", "other")
	_, ambiguousErr := trust.MatchRule([]trustrule.Rule{rule, second}, nsAcme, setup.SPIFFEID, "trb-1")
	if !errors.Is(ambiguousErr, trust.ErrAmbiguousRule) {
		t.Fatalf("MatchRule with two matches = %v, want ErrAmbiguousRule", ambiguousErr)
	}

	// The match is exact: a /* pattern is not a wildcard.
	wildcard := newMtlsRule("spiffe://ci.example.com/ns/acme/sa/*", "prn-worker", "wildcard")
	_, wildcardErr := trust.MatchRule([]trustrule.Rule{wildcard}, nsAcme, setup.SPIFFEID, "trb-1")
	if !errors.Is(wildcardErr, trust.ErrNoMatchingRule) {
		t.Fatalf("MatchRule with a /* rule = %v, want ErrNoMatchingRule", wildcardErr)
	}

	_, starErr := trust.MatchRule([]trustrule.Rule{wildcard}, nsAcme, "spiffe://ci.example.com/ns/acme/sa/*", "trb-1")
	if !errors.Is(starErr, trust.ErrInvalid) {
		t.Fatalf("MatchRule of a SPIFFE ID with * = %v, want ErrInvalid", starErr)
	}

	// A rule of another namespace in the list never matches.
	foreign := newMtlsRule(setup.SPIFFEID, "prn-worker", "foreign")
	foreign.Namespace = namespace.Name("other")

	_, foreignErr := trust.MatchRule([]trustrule.Rule{foreign}, nsAcme, setup.SPIFFEID, "trb-1")
	if !errors.Is(foreignErr, trust.ErrNoMatchingRule) {
		t.Fatalf("MatchRule with a rule of another namespace = %v, want ErrNoMatchingRule", foreignErr)
	}

	got, oneErr := trust.MatchRule([]trustrule.Rule{foreign, rule}, nsAcme, setup.SPIFFEID, "trb-1")
	if oneErr != nil || got.ID != rule.ID {
		t.Fatalf("MatchRule with a foreign twin = %v, %v; want the rule of the namespace", got.ID, oneErr)
	}
}

// checkVerifyGuards sends the inputs that VerifyChain refuses before it
// verifies a signature.
func checkVerifyGuards(t *testing.T, setup verifySetup) {
	t.Helper()

	if _, err := trust.VerifyChain(nil, setup.Roots, setup.Now, goodChecker); !errors.Is(err, trust.ErrNoCertificate) {
		t.Fatalf("VerifyChain with no chain = %v, want ErrNoCertificate", err)
	}

	_, nilRootsErr := trust.VerifyChain([][]byte{setup.LeafDER}, nil, setup.Now, goodChecker)
	if !errors.Is(nilRootsErr, trust.ErrMissingDependency) {
		t.Fatalf("VerifyChain with no roots = %v, want ErrMissingDependency", nilRootsErr)
	}

	_, derErr := trust.VerifyChain([][]byte{[]byte("not DER")}, setup.Roots, setup.Now, goodChecker)
	if !errors.Is(derErr, trust.ErrInvalid) {
		t.Fatalf("VerifyChain with bad DER = %v, want ErrInvalid", derErr)
	}

	ca, caKey, _ := makeTestCA(t, setup.Now.Add(-time.Hour), setup.Now.Add(24*time.Hour))
	star := makeTestLeaf(t, ca, caKey, leafConfig{
		SpiffeIDs: []string{"spiffe://ci.example.com/ns/acme/sa/*"},
		NotBefore: setup.Now.Add(-time.Hour), NotAfter: setup.Now.Add(time.Hour),
	})

	_, starErr := trust.VerifyChain([][]byte{star}, rootsOf(t, ca), setup.Now, goodChecker)
	if !errors.Is(starErr, trust.ErrInvalid) {
		t.Fatalf("VerifyChain with * in the SPIFFE ID = %v, want ErrInvalid", starErr)
	}
}

func checkConstructor(t *testing.T) {
	t.Helper()

	_, cfgErr := trust.NewExchanger(trust.Config{
		Principals: nil, Tokens: nil, Exchanges: nil, Bundles: nil, Audit: nil, Now: nil,
	})
	if !errors.Is(cfgErr, trust.ErrMissingDependency) {
		t.Fatalf("NewExchanger with nil ports = %v, want ErrMissingDependency", cfgErr)
	}
}

func checkStoredIssue(t *testing.T, setup verifySetup) {
	t.Helper()

	chain, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, goodChecker)
	if verifyErr != nil {
		t.Fatalf("VerifyChain = %v", verifyErr)
	}

	rule := newMtlsRule(setup.SPIFFEID, "prn-worker", "deploy")
	fixture := newExchangeFixture()

	res, exchangeErr := fixture.exchanger.Exchange(t.Context(), &trust.ExchangeInput{
		Namespace: namespace.Name("acme"), Chain: chain, Rules: []trustrule.Rule{rule}, Bundle: "trb-1",
		Fence: namespace.Fence{Namespace: namespace.Name("acme"), FailoverVersion: 1}, Entry: testEntry(),
	})
	if exchangeErr != nil {
		t.Fatalf("Exchange = %v", exchangeErr)
	}

	if res.Token.Kind != accesstoken.KindMTLSExchange || res.Token.PrincipalID != "prn-worker" {
		t.Fatalf("token = %+v, want kind mtls-exchange of prn-worker", res.Token)
	}

	if len(fixture.exchanges.exchanges) != 1 || len(fixture.exchanges.entries) != 1 {
		t.Fatalf("stored = %d exchanges %d entries, want one each", len(fixture.exchanges.exchanges),
			len(fixture.exchanges.entries))
	}

	if got := fixture.exchanges.exchanges[0].SPIFFEID; got != setup.SPIFFEID {
		t.Fatalf("stored exchange SPIFFE ID = %q, want %q for the match in the transaction", got, setup.SPIFFEID)
	}

	want := audit.Change{Key: "kind", Old: nil, New: "mtls-exchange", Layer: "", TakesEffect: ""}
	if !slices.ContainsFunc(fixture.exchanges.entries[0].Changes, func(c audit.Change) bool {
		return c.Key == want.Key && c.New == want.New
	}) {
		t.Fatalf("issue entry changes = %+v, want %+v", fixture.exchanges.entries[0].Changes, want)
	}

	if len(fixture.denials.entries) != 0 {
		t.Fatalf("a good exchange wrote %d denial entries", len(fixture.denials.entries))
	}
}

// TestMtlsExchangeVerify checks the verify scenarios: a valid chain verifies
// and matches one rule, and each hostile chain or match is refused with no
// token.
func TestMtlsExchangeVerify(t *testing.T) {
	t.Parallel()

	setup := newVerifySetup(t)
	checkValidChain(t, setup)
	checkWrongCA(t, setup)
	checkExpiredLeaf(t, setup)
	checkNotYetValidLeaf(t, setup)
	checkRevokedLeaf(t, setup)
	checkUnknownRevocation(t, setup)
	checkChainBounds(t, setup)
	checkSPIFFEShape(t, setup)
	checkVerifyGuards(t, setup)
	checkMatchCases(t, setup)
	checkConstructor(t)
	checkStoredIssue(t, setup)
	checkExchangeRefusals(t, setup)
	checkMatchDenialEntry(t, setup)
	checkHandBuiltChain(t, setup)
	checkCrossNamespaceInput(t, setup)
	checkVerifyDenials(t, setup)
	checkAdmitDenials(t, setup)
}

type fakePrincipals struct {
	byID map[string]principal.Principal
}

func (f *fakePrincipals) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	p, ok := f.byID[id]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return p, nil
}

type fakeTokens struct{}

func (fakeTokens) NewMtlsTrustToken(
	_ context.Context, in accesstoken.MtlsInput,
) (accesstoken.Issued, error) {
	tok := accesstoken.Token{
		CreatedAt: in.At, ExpiresAt: in.At.Add(time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
		DisabledAt: time.Time{}, ID: "tok-mtls-1", PrincipalID: in.PrincipalID, Name: in.Name,
		Kind: accesstoken.KindMTLSExchange, ReplacedBy: "", CreatedBy: in.CreatedBy,
	}

	return accesstoken.Issued{Token: tok, Secret: accesstoken.Secret{}, Digest: accesstoken.Digest{}}, nil
}

type fakeExchanges struct {
	exchanges []trustrule.Exchange
	entries   []audit.Entry
	err       error
}

func (f *fakeExchanges) RecordExchange(
	_ context.Context, x trustrule.Exchange, _ namespace.Fence, e audit.Entry,
) error {
	if f.err != nil {
		return f.err
	}

	if validateErr := e.Validate(); validateErr != nil {
		return validateErr
	}

	f.exchanges = append(f.exchanges, x)
	f.entries = append(f.entries, e)

	return nil
}

type fakeBundles struct {
	bundle trustrule.Bundle
	err    error
}

func (f *fakeBundles) GetBundle(_ context.Context, _ namespace.Name) (trustrule.Bundle, error) {
	return f.bundle, f.err
}

type fakeAudit struct {
	entries []audit.Entry
}

func (f *fakeAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if validateErr := e.Validate(); validateErr != nil {
		return audit.Entry{}, validateErr
	}

	f.entries = append(f.entries, e)

	return e, nil
}

type exchangeFixture struct {
	exchanger  *trust.Exchanger
	exchanges  *fakeExchanges
	denials    *fakeAudit
	principals *fakePrincipals
	bundles    *fakeBundles
}

func newExchangeFixture() *exchangeFixture {
	principals := &fakePrincipals{byID: map[string]principal.Principal{
		"prn-worker": {
			CreatedAt: mtlsTestNow(), UpdatedAt: mtlsTestNow(), DisabledAt: time.Time{}, ID: "prn-worker",
			Subject: "ci-deploy", DisplayName: "ci-deploy", Kind: principal.KindService,
			Source: principal.SourceLocal, Namespace: namespace.Name("acme"), GroupCount: 0, BreakGlass: false,
		},
	}}

	exchanges := &fakeExchanges{exchanges: nil, entries: nil, err: nil}
	denials := &fakeAudit{entries: nil}
	bundles := &fakeBundles{
		bundle: trustrule.Bundle{
			ID: "trb-1", Namespace: namespace.Name("acme"), Active: 1,
			Versions: []trustrule.BundleVersion{{
				Number: 1, Certificates: nil, CreatedAt: mtlsTestNow(), OverlapEndsAt: time.Time{},
			}},
			Revision: 1, CreatedAt: mtlsTestNow(),
		},
		err: nil,
	}

	exchanger, exchangerErr := trust.NewExchanger(trust.Config{
		Principals: principals, Tokens: fakeTokens{}, Exchanges: exchanges, Bundles: bundles, Audit: denials,
		Now: mtlsTestNow,
	})
	if exchangerErr != nil {
		panic(exchangerErr)
	}

	return &exchangeFixture{
		exchanger: exchanger, exchanges: exchanges, denials: denials, principals: principals, bundles: bundles,
	}
}

func exchangeInput(chain trust.VerifiedChain, rule trustrule.Rule) *trust.ExchangeInput {
	return &trust.ExchangeInput{
		Namespace: namespace.Name("acme"), Chain: chain, Rules: []trustrule.Rule{rule}, Bundle: "trb-1",
		Fence: namespace.Fence{Namespace: namespace.Name("acme"), FailoverVersion: 1}, Entry: testEntry(),
	}
}

// checkExchangeRefusals checks that each hostile principal or bundle gives
// no token, no stored exchange, and one denial entry.
func checkExchangeRefusals(t *testing.T, setup verifySetup) {
	t.Helper()

	chain, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, goodChecker)
	if verifyErr != nil {
		t.Fatalf("VerifyChain = %v", verifyErr)
	}

	rule := newMtlsRule(setup.SPIFFEID, "prn-worker", "deploy")

	cases := []struct {
		name   string
		mutate func(f *exchangeFixture)
		want   error
	}{
		{"person principal", func(f *exchangeFixture) {
			p := f.principals.byID["prn-worker"]
			p.Kind = principal.KindPerson
			f.principals.byID["prn-worker"] = p
		}, trustrule.ErrPrincipal},
		{"cross-namespace principal", func(f *exchangeFixture) {
			p := f.principals.byID["prn-worker"]
			p.Namespace = namespace.Name("other")
			f.principals.byID["prn-worker"] = p
		}, trustrule.ErrPrincipal},
		{"disabled principal", func(f *exchangeFixture) {
			p := f.principals.byID["prn-worker"]
			p.DisabledAt = mtlsTestNow()
			f.principals.byID["prn-worker"] = p
		}, trustrule.ErrPrincipal},
		{"missing principal", func(f *exchangeFixture) {
			delete(f.principals.byID, "prn-worker")
		}, trustrule.ErrPrincipal},
		{"bundle with no active version", func(f *exchangeFixture) {
			f.bundles.bundle.Active = 9
		}, trust.ErrBundleUnknown},
		{"bundle of another ID", func(f *exchangeFixture) {
			f.bundles.bundle.ID = "trb-other"
		}, trust.ErrBundleUnknown},
		{"namespace with no bundle", func(f *exchangeFixture) {
			f.bundles.err = trustrule.ErrNotFound
		}, trust.ErrBundleUnknown},
	}

	for _, tc := range cases {
		fixture := newExchangeFixture()
		tc.mutate(fixture)

		res, exchangeErr := fixture.exchanger.Exchange(t.Context(), exchangeInput(chain, rule))
		if !errors.Is(exchangeErr, tc.want) {
			t.Errorf("%s: Exchange = %v, want %v", tc.name, exchangeErr, tc.want)
		}

		if res.Token.ID != "" || len(fixture.exchanges.exchanges) != 0 || len(fixture.exchanges.entries) != 0 {
			t.Errorf("%s: the refusal gave a token or a stored exchange", tc.name)
		}

		if len(fixture.denials.entries) != 1 || fixture.denials.entries[0].Outcome != audit.OutcomeDenied ||
			fixture.denials.entries[0].Action != trust.ActionExchange {
			t.Errorf("%s: denial entries = %+v, want one denied exchange entry", tc.name, fixture.denials.entries)
		}
	}
}

// checkMatchDenialEntry checks the denial entry of a SPIFFE ID with no rule.
func checkMatchDenialEntry(t *testing.T, setup verifySetup) {
	t.Helper()

	chain, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, goodChecker)
	if verifyErr != nil {
		t.Fatalf("VerifyChain = %v", verifyErr)
	}

	rule := newMtlsRule("spiffe://ci.example.com/ns/acme/sa/other", "prn-worker", "other")
	fixture := newExchangeFixture()

	_, exchangeErr := fixture.exchanger.Exchange(t.Context(), exchangeInput(chain, rule))
	if !errors.Is(exchangeErr, trust.ErrNoMatchingRule) {
		t.Fatalf("Exchange with no match = %v, want ErrNoMatchingRule", exchangeErr)
	}

	if len(fixture.denials.entries) != 1 || fixture.denials.entries[0].Subject != setup.SPIFFEID {
		t.Fatalf("denial entries = %+v, want one entry for %s", fixture.denials.entries, setup.SPIFFEID)
	}
}

// checkHandBuiltChain sends a VerifiedChain that VerifyChain did not make.
// Its fields are unexported, so the zero value is the only one another
// package can build: Exchange refuses it, stores nothing and writes one
// denial entry with the refused subject.
func checkHandBuiltChain(t *testing.T, setup verifySetup) {
	t.Helper()

	var handBuilt trust.VerifiedChain

	rule := newMtlsRule(setup.SPIFFEID, "prn-worker", "deploy")
	fixture := newExchangeFixture()

	res, err := fixture.exchanger.Exchange(t.Context(), exchangeInput(handBuilt, rule))
	if !errors.Is(err, trust.ErrNoCertificate) {
		t.Fatalf("Exchange with a hand-built chain = %v, want ErrNoCertificate", err)
	}

	if res.Token.ID != "" || len(fixture.exchanges.exchanges) != 0 {
		t.Fatal("a hand-built chain gave a token or a stored exchange")
	}

	if len(fixture.denials.entries) != 1 || fixture.denials.entries[0].Subject != trust.RefusedSubject ||
		fixture.denials.entries[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("denial entries = %+v, want one entry for %s", fixture.denials.entries, trust.RefusedSubject)
	}
}

// checkCrossNamespaceInput sends a rule of another namespace and a fence of
// another namespace to Exchange, as if the route had read the wrong rows.
func checkCrossNamespaceInput(t *testing.T, setup verifySetup) {
	t.Helper()

	chain, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, goodChecker)
	if verifyErr != nil {
		t.Fatalf("VerifyChain = %v", verifyErr)
	}

	foreign := newMtlsRule(setup.SPIFFEID, "prn-worker", "foreign")
	foreign.Namespace = namespace.Name("other")

	fixture := newExchangeFixture()

	_, ruleErr := fixture.exchanger.Exchange(t.Context(), exchangeInput(chain, foreign))
	if !errors.Is(ruleErr, trust.ErrNoMatchingRule) {
		t.Fatalf("Exchange with a rule of another namespace = %v, want ErrNoMatchingRule", ruleErr)
	}

	rule := newMtlsRule(setup.SPIFFEID, "prn-worker", "deploy")
	in := exchangeInput(chain, rule)
	in.Fence = namespace.Fence{Namespace: namespace.Name("other"), FailoverVersion: 1}

	_, fenceErr := fixture.exchanger.Exchange(t.Context(), in)
	if !errors.Is(fenceErr, trust.ErrInvalid) {
		t.Fatalf("Exchange under the fence of another namespace = %v, want ErrInvalid", fenceErr)
	}

	if len(fixture.exchanges.exchanges) != 0 || len(fixture.denials.entries) != 2 {
		t.Fatalf("stored %d exchanges and %d denial entries, want 0 and 2", len(fixture.exchanges.exchanges),
			len(fixture.denials.entries))
	}
}

// checkVerifyDenials checks that each refusal of Exchanger.Verify writes one
// denial entry with the reason of the refusal.
func checkVerifyDenials(t *testing.T, setup verifySetup) {
	t.Helper()

	otherCA, _, _ := makeTestCA(t, setup.Now.Add(-time.Hour), setup.Now.Add(24*time.Hour))
	revoked := func(_ *x509.Certificate) error { return trust.ErrRevoked }

	cases := []struct {
		name  string
		raw   [][]byte
		roots *x509.CertPool
		check func(*x509.Certificate) error
		want  error
	}{
		{"no certificate", nil, setup.Roots, goodChecker, trust.ErrNoCertificate},
		{"another CA", [][]byte{setup.LeafDER}, rootsOf(t, otherCA), goodChecker, trust.ErrUnknownIssuer},
		{"revoked", [][]byte{setup.LeafDER}, setup.Roots, revoked, trust.ErrRevoked},
		{"unknown revocation", [][]byte{setup.LeafDER}, setup.Roots, nil, trust.ErrRevocationUnknown},
		{"oversize", [][]byte{make([]byte, trust.MaxChainBytes+1)}, setup.Roots, goodChecker, trust.ErrInvalid},
	}

	for _, tc := range cases {
		fixture := newExchangeFixture()

		chain, err := fixture.exchanger.Verify(t.Context(), &trust.VerifyInput{
			Call: testCall(), RawChain: tc.raw, Roots: tc.roots, Check: tc.check,
		})
		if !errors.Is(err, tc.want) || chain.Leaf() != nil {
			t.Errorf("%s: Verify = %v, leaf %v; want %v and no leaf", tc.name, err, chain.Leaf() != nil, tc.want)
		}

		mustDenyOnce(t, tc.name, fixture.denials.entries, tc.want)
	}

	expired := newExchangeFixture()
	late := setup.Now.Add(2 * time.Hour)
	lateExchanger, lateErr := trust.NewExchanger(trust.Config{
		Principals: expired.principals, Tokens: fakeTokens{}, Exchanges: expired.exchanges,
		Bundles: expired.bundles, Audit: expired.denials, Now: func() time.Time { return late },
	})
	if lateErr != nil {
		t.Fatalf("NewExchanger = %v", lateErr)
	}

	_, expiredErr := lateExchanger.Verify(t.Context(), &trust.VerifyInput{
		Call: testCall(), RawChain: [][]byte{setup.LeafDER}, Roots: setup.Roots, Check: goodChecker,
	})
	if !errors.Is(expiredErr, trust.ErrExpired) {
		t.Errorf("Verify of an expired leaf = %v, want ErrExpired", expiredErr)
	}

	mustDenyOnce(t, "expired", expired.denials.entries, trust.ErrExpired)
}

// checkAdmitDenials checks that each refusal of the limit layer through
// Exchanger.Admit writes one denial entry.
func checkAdmitDenials(t *testing.T, setup verifySetup) {
	t.Helper()

	now := setup.Now

	cases := []struct {
		name string
		in   trust.CheckInput
		max  int
		want error
	}{
		{"no certificate", trust.CheckInput{HasCert: false, BodyLen: 10}, 5, trust.ErrNoCertificate},
		{"oversize body", trust.CheckInput{HasCert: true, BodyLen: trust.MaxBodyBytes + 1}, 5, trust.ErrInvalid},
		{"rate", trust.CheckInput{HasCert: true, BodyLen: 10}, 1, trust.ErrRateLimited},
	}

	for _, tc := range cases {
		guard, guardErr := trust.NewGuard(trust.GuardConfig{
			MaxBodyBytes: 0, MaxPerWindow: tc.max, Window: time.Minute, Now: func() time.Time { return now },
		})
		if guardErr != nil {
			t.Fatalf("NewGuard = %v", guardErr)
		}

		if tc.max == 1 {
			// Fill the window, so the next call is past the rate.
			if fillErr := guard.Check(trust.CheckInput{HasCert: true, BodyLen: 10}); fillErr != nil {
				t.Fatalf("Check within the rate = %v, want nil", fillErr)
			}
		}

		fixture := newExchangeFixture()
		call := testCall()

		if err := fixture.exchanger.Admit(t.Context(), guard, &call, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: Admit = %v, want %v", tc.name, err, tc.want)
		}

		mustDenyOnce(t, tc.name, fixture.denials.entries, tc.want)
	}
}

// mustDenyOnce fails the test unless entries is one denied exchange entry
// with the refused subject and the reason of want.
func mustDenyOnce(t *testing.T, name string, entries []audit.Entry, want error) {
	t.Helper()

	if len(entries) != 1 {
		t.Errorf("%s: %d denial entries, want 1", name, len(entries))

		return
	}

	e := entries[0]
	if e.Outcome != audit.OutcomeDenied || e.Action != trust.ActionExchange || e.Subject != trust.RefusedSubject {
		t.Errorf("%s: denial entry = %+v, want a denied exchange of %s", name, e, trust.RefusedSubject)
	}

	if !slices.ContainsFunc(e.Changes, func(c audit.Change) bool {
		return c.Key == "reason" && c.New == want.Error()
	}) {
		t.Errorf("%s: denial changes = %+v, want the reason %q", name, e.Changes, want.Error())
	}
}

func testCall() trust.Call {
	return trust.Call{
		Namespace: nsAcme, Fence: namespace.Fence{Namespace: nsAcme, FailoverVersion: 1}, Entry: testEntry(),
	}
}

func testEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "",
		Action: "", Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
		Origin: nil,
	}
}
