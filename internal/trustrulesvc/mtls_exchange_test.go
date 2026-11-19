package trustrulesvc_test

import (
	"context"
	"crypto/x509"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// errRevocationSource is a revocation source that cannot answer.
var errRevocationSource = errors.New("revocation source is down")

// TestMtlsExchangeVerify covers the spec scenarios of the mTLS exchange on a
// real store.
func TestMtlsExchangeVerify(t *testing.T) {
	t.Parallel()

	t.Run("a good chain gives one token and one success entry", testGoodChain)
	t.Run("a chain of another CA gets no token and a denial entry", testOtherCA)
	t.Run("a namespace without a bundle refuses", testNoBundle)
	t.Run("after a rotate only the active version is trusted", testRotateActiveOnly)
	t.Run("a server with no revocation source refuses every chain", testNoRevocationSource)
	t.Run("the constructor needs each dependency", testConstructor)
}

func testGoodChain(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)
	raw := [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))}

	chain, err := verify(t.Context(), x, "acme", raw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if chain.SPIFFEID() != spiffeWorker || chain.BundleID() != e.bundleAcme.ID || chain.BundleVersion() < 1 {
		t.Fatalf("chain = %q %q %d", chain.SPIFFEID(), chain.BundleID(), chain.BundleVersion())
	}

	res, err := exchange(t.Context(), x, "acme", chain)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	checkGoodResult(t, e, chain, res)
}

// checkGoodResult checks the token, the rule and the entry of a good exchange.
func checkGoodResult(t *testing.T, e *mtlsEnv, chain trustrulesvc.VerifiedChain, res trustrulesvc.MtlsExchangeResult) {
	t.Helper()

	if res.Secret.Reveal() == "" || res.Rule.ID != e.rule.ID || res.Token.PrincipalID != e.worker {
		t.Fatalf("result = %+v", res)
	}

	if res.Token.ExpiresAt.After(chain.LeafNotAfter()) {
		t.Fatalf("token expires %v, after the leaf %v", res.Token.ExpiresAt, chain.LeafNotAfter())
	}

	if n := e.tokenCount(e.worker); n != 1 {
		t.Fatalf("tokens = %d, want 1", n)
	}

	entries := e.exchangeEntries("acme")
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeSuccess || entries[0].TokenID == "" {
		t.Fatalf("entries = %+v", entries)
	}
}

func testOtherCA(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	_, err := verify(t.Context(), e.exchanger(nil), "acme", [][]byte{e.caOther.leaf(t, goodLeaf(spiffeWorker))})
	e.mustRefuse(err, trustrulesvc.ErrUnknownIssuer, "acme", e.worker)
}

func testNoBundle(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)

	_, err := verify(t.Context(), e.exchanger(nil), "missing", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
	if !isErr(err, trustrulesvc.ErrBundleUnknown) {
		t.Fatalf("error = %v, want ErrBundleUnknown", err)
	}
}

func testRotateActiveOnly(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	next := newTestCA(t, "ca acme 2")
	e.rotate(next)

	x := e.exchanger(nil)

	if _, err := verify(t.Context(), x, "acme", [][]byte{next.leaf(t, goodLeaf(spiffeWorker))}); err != nil {
		t.Fatalf("verify with the new CA: %v", err)
	}

	_, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
	e.mustRefuse(err, trustrulesvc.ErrUnknownIssuer, "acme", e.worker)
}

func testNoRevocationSource(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(func(c *trustrulesvc.MtlsConfig) { c.Revocation = nil })
	_, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
	e.mustRefuse(err, trustrulesvc.ErrRevocationUnknown, "acme", e.worker)
}

func testConstructor(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)

	for name, change := range map[string]func(*trustrulesvc.MtlsConfig){
		"rules":      func(c *trustrulesvc.MtlsConfig) { c.Rules = nil },
		"exchanges":  func(c *trustrulesvc.MtlsConfig) { c.Exchanges = nil },
		"bundles":    func(c *trustrulesvc.MtlsConfig) { c.Bundles = nil },
		"namespaces": func(c *trustrulesvc.MtlsConfig) { c.Namespaces = nil },
		"principals": func(c *trustrulesvc.MtlsConfig) { c.Principals = nil },
		"tokens":     func(c *trustrulesvc.MtlsConfig) { c.Tokens = nil },
		"audit":      func(c *trustrulesvc.MtlsConfig) { c.Audit = nil },
		"server":     func(c *trustrulesvc.MtlsConfig) { c.ServerAudit = nil },
	} {
		if _, err := trustrulesvc.NewMtlsExchanger(e.config(change)); !isErr(err, trustrule.ErrMissingDependency) {
			t.Errorf("%s: error = %v, want ErrMissingDependency", name, err)
		}
	}
}

// TestMtlsExchangeChainHostile sends hostile chains to VerifyChain, the chain
// layer, as if no other layer existed.
func TestMtlsExchangeChainHostile(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "ca")
	other := newTestCA(t, "other")
	now := exchangeNow()
	good := goodLeaf(spiffeWorker)

	expired := good
	expired.notBefore, expired.notAfter = now.Add(-2*time.Hour), now.Add(-time.Hour)

	future := good
	future.notBefore, future.notAfter = now.Add(time.Hour), now.Add(2*time.Hour)

	// These two fail the time check first: the SPIFFE ID check and the roots
	// check would give another error. They tell the time check of VerifyChain
	// apart from the time check of x509.
	expiredNoURI := expired
	expiredNoURI.uris = nil

	futureNoURI := future
	futureNoURI.uris = nil

	twoURIs := good
	twoURIs.uris = []string{spiffeWorker, "spiffe://example.org/ns/acme/sa/other"}

	noURI := good
	noURI.uris = nil

	wildcard := good
	wildcard.uris = []string{"spiffe://example.org/ns/acme/sa/*"}

	httpURI := good
	httpURI.uris = []string{"https://example.org/ns/acme/sa/worker"}

	isCA := good
	isCA.isCA = true

	big := padded(good, 600)

	serverOnly := good
	serverOnly.ekus = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}

	deep := make([][]byte, 1, 1+trustrulesvc.MaxChainDepth)
	deep[0] = ca.leaf(t, good)
	for range trustrulesvc.MaxChainDepth {
		deep = append(deep, ca.cert.Raw)
	}

	cases := []struct {
		name  string
		raw   [][]byte
		roots *x509.CertPool
		check func(*x509.Certificate) error
		want  error
	}{
		{"no chain", nil, pool(ca), noRevocation, trustrulesvc.ErrNoCertificate},
		{"other CA", [][]byte{other.leaf(t, good)}, pool(ca), noRevocation, trustrulesvc.ErrUnknownIssuer},
		{"expired", [][]byte{ca.leaf(t, expired)}, pool(ca), noRevocation, trustrulesvc.ErrExpired},
		{"not yet valid", [][]byte{ca.leaf(t, future)}, pool(ca), noRevocation, trustrulesvc.ErrNotYetValid},
		{"expired, no URI", [][]byte{ca.leaf(t, expiredNoURI)}, pool(ca), noRevocation, trustrulesvc.ErrExpired},
		{
			"not yet valid, no URI",
			[][]byte{ca.leaf(t, futureNoURI)},
			pool(ca),
			noRevocation,
			trustrulesvc.ErrNotYetValid,
		},
		{"expired, nil roots", [][]byte{ca.leaf(t, expired)}, nil, noRevocation, trustrulesvc.ErrExpired},
		{
			"revoked",
			[][]byte{ca.leaf(t, good)},
			pool(ca),
			func(*x509.Certificate) error { return trustrulesvc.ErrRevoked }, trustrulesvc.ErrRevoked,
		},
		{
			"revocation unknown",
			[][]byte{ca.leaf(t, good)},
			pool(ca),
			func(*x509.Certificate) error { return errRevocationSource }, trustrulesvc.ErrRevocationUnknown,
		},
		{"no revocation check", [][]byte{ca.leaf(t, good)}, pool(ca), nil, trustrulesvc.ErrRevocationUnknown},
		{
			"oversize",
			[][]byte{ca.leaf(t, good), make([]byte, trustrulesvc.MaxChainBytes)},
			pool(ca), noRevocation,
			trustrulesvc.ErrInvalid,
		},
		{"oversize but valid", [][]byte{ca.leaf(t, big)}, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"depth 9", deep, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"garbage", [][]byte{[]byte("not a certificate")}, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"two SPIFFE URIs", [][]byte{ca.leaf(t, twoURIs)}, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"no URI", [][]byte{ca.leaf(t, noURI)}, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"wildcard", [][]byte{ca.leaf(t, wildcard)}, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"not a SPIFFE URI", [][]byte{ca.leaf(t, httpURI)}, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"leaf is a CA", [][]byte{ca.leaf(t, isCA)}, pool(ca), noRevocation, trustrulesvc.ErrInvalid},
		{"server auth only", [][]byte{ca.leaf(t, serverOnly)}, pool(ca), noRevocation, trustrulesvc.ErrUnknownIssuer},
		{"nil roots", [][]byte{ca.leaf(t, good)}, nil, noRevocation, trustrulesvc.ErrBundleUnknown},
		{"empty roots", [][]byte{ca.leaf(t, good)}, x509.NewCertPool(), noRevocation, trustrulesvc.ErrUnknownIssuer},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, err := trustrulesvc.VerifyChain(tc.raw, tc.roots, now, tc.check)
			if !isErr(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}

			if errors.Is(tc.want, trustrulesvc.ErrRevoked) && isErr(err, trustrulesvc.ErrRevocationUnknown) {
				t.Fatalf("a revoked certificate is reported as unknown: %v", err)
			}

			if c.SPIFFEID() != "" {
				t.Fatalf("a refused chain holds the SPIFFE ID %q", c.SPIFFEID())
			}
		})
	}

	t.Run("a good chain", func(t *testing.T) {
		t.Parallel()

		c, err := trustrulesvc.VerifyChain([][]byte{ca.leaf(t, good)}, pool(ca), now, noRevocation)
		if err != nil || c.SPIFFEID() != spiffeWorker {
			t.Fatalf("chain = %q, %v", c.SPIFFEID(), err)
		}
	})
}

// TestMtlsMatchRule sends hostile rule lists to MatchRule.
func TestMtlsMatchRule(t *testing.T) {
	t.Parallel()

	rule := func(ns namespace.Name, bundle, pattern string) trustrule.Rule {
		var r trustrule.Rule

		r.Namespace, r.Bundle, r.SpiffeIDPattern = ns, bundle, pattern

		return r
	}

	cases := []struct {
		name   string
		rules  []trustrule.Rule
		ns     namespace.Name
		id, bn string
		want   error
	}{
		{"one", []trustrule.Rule{rule("acme", "b1", spiffeWorker)}, "acme", spiffeWorker, "b1", nil},
		{"none", nil, "acme", spiffeWorker, "b1", trustrulesvc.ErrNoMatchingRule},
		{
			"other namespace",
			[]trustrule.Rule{rule("other", "b1", spiffeWorker)},
			"acme", spiffeWorker, "b1",
			trustrulesvc.ErrNoMatchingRule,
		},
		{
			"other bundle",
			[]trustrule.Rule{rule("acme", "b2", spiffeWorker)},
			"acme", spiffeWorker, "b1",
			trustrulesvc.ErrNoMatchingRule,
		},
		{
			"pattern is a prefix",
			[]trustrule.Rule{rule("acme", "b1", "spiffe://example.org/ns/acme/sa/*")},
			"acme",
			spiffeWorker, "b1", trustrulesvc.ErrNoMatchingRule,
		},
		{
			"two",
			[]trustrule.Rule{rule("acme", "b1", spiffeWorker), rule("acme", "b1", spiffeWorker)},
			"acme",
			spiffeWorker, "b1", trustrulesvc.ErrAmbiguousRule,
		},
		{"empty ID", []trustrule.Rule{rule("acme", "b1", "")}, "acme", "", "b1", trustrulesvc.ErrNoMatchingRule},
		{
			"empty bundle",
			[]trustrule.Rule{rule("acme", "", spiffeWorker)},
			"acme", spiffeWorker, "",
			trustrulesvc.ErrNoMatchingRule,
		},
		{"wildcard ID", []trustrule.Rule{rule("acme", "b1", "*")}, "acme", "*", "b1", trustrulesvc.ErrNoMatchingRule},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := trustrulesvc.MatchRule(tc.rules, tc.ns, tc.id, tc.bn)
			if tc.want == nil {
				if err != nil || r.SpiffeIDPattern != tc.id {
					t.Fatalf("rule = %+v, %v", r, err)
				}

				return
			}

			if !isErr(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

// failingExchanges wraps the exchange store of the real store.
type failingExchanges struct {
	trustrule.ExchangeStore

	err error
}

func (f failingExchanges) RecordExchange(
	_ context.Context, _ trustrule.Exchange, _ namespace.Fence, _ audit.Entry,
) error {
	return f.err
}

// strangerPrincipals answers a principal of another namespace for every ID.
type strangerPrincipals struct {
	trustrulesvc.Principals

	ns   namespace.Name
	kind principal.Kind
}

func (s strangerPrincipals) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	p, err := s.Principals.GetPrincipal(ctx, id)
	if err != nil {
		return p, err
	}

	p.Namespace, p.Kind = s.ns, s.kind

	return p, nil
}

// doubledRules lists every rule twice.
type doubledRules struct {
	trustrule.Store
}

func (d doubledRules) List(ctx context.Context, ns namespace.Name) ([]trustrule.Rule, error) {
	l, err := d.Store.List(ctx, ns)
	if err != nil {
		return nil, err
	}

	return append(l, l...), nil
}

// TestMtlsExchangeServiceHostile sends hostile values to Exchange, the service
// layer, as if the chain layer had failed.
func TestMtlsExchangeServiceHostile(t *testing.T) {
	t.Parallel()

	good := func(e *mtlsEnv, x *trustrulesvc.MtlsExchanger, spiffe string) trustrulesvc.VerifiedChain {
		t.Helper()

		c, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffe))})
		if err != nil {
			t.Fatalf("verify: %v", err)
		}

		return c
	}

	t.Run("a zero chain", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		_, err := exchange(t.Context(), e.exchanger(nil), "acme", trustrulesvc.VerifiedChain{})
		e.mustRefuse(err, trustrulesvc.ErrNoCertificate, "acme", e.worker)
	})

	t.Run("no rule for the SPIFFE ID", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(nil)
		c := good(e, x, "spiffe://example.org/ns/acme/sa/nobody")
		_, err := exchange(t.Context(), x, "acme", c)
		e.mustRefuse(err, trustrulesvc.ErrNoMatchingRule, "acme", e.worker)
	})

	t.Run("the rule of another SPIFFE ID does not match", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		e.createRule("acme", "other-rule", "spiffe://example.org/ns/acme/sa/batch", e.worker, e.bundleAcme.ID)

		x := e.exchanger(nil)
		c := good(e, x, "spiffe://example.org/ns/acme/sa/web")
		_, err := exchange(t.Context(), x, "acme", c)
		e.mustRefuse(err, trustrulesvc.ErrNoMatchingRule, "acme", e.worker)
	})

	t.Run("two rules for the SPIFFE ID", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(func(c *trustrulesvc.MtlsConfig) { c.Rules = doubledRules{c.Rules} })
		c := good(e, x, spiffeWorker)
		_, err := exchange(t.Context(), x, "acme", c)
		e.mustRefuse(err, trustrulesvc.ErrAmbiguousRule, "acme", e.worker)
	})

	t.Run("the chain of another namespace", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(nil)
		c := good(e, x, spiffeWorker)
		_, err := exchange(t.Context(), x, "other", c)
		e.mustRefuse(err, trustrulesvc.ErrNoMatchingRule, "other", e.stranger)

		if n := e.tokenCount(e.worker); n != 0 {
			t.Fatalf("worker holds %d tokens", n)
		}
	})

	t.Run("a principal of another namespace", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
			c.Principals = strangerPrincipals{Principals: c.Principals, ns: "other", kind: principal.KindService}
		})
		c := good(e, x, spiffeWorker)
		_, err := exchange(t.Context(), x, "acme", c)
		e.mustRefuse(err, trustrule.ErrPrincipal, "acme", e.worker)
	})

	t.Run("a principal that is a person", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
			c.Principals = strangerPrincipals{Principals: c.Principals, ns: "acme", kind: principal.KindPerson}
		})
		c := good(e, x, spiffeWorker)
		_, err := exchange(t.Context(), x, "acme", c)
		e.mustRefuse(err, trustrule.ErrPrincipal, "acme", e.worker)
	})

	t.Run("no bundle row in the namespace of the fence", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
			c.Exchanges = failingExchanges{ExchangeStore: c.Exchanges, err: trustrule.ErrNotFound}
		})
		c := good(e, x, spiffeWorker)
		_, err := exchange(t.Context(), x, "acme", c)
		e.mustRefuse(err, trustrulesvc.ErrNoMatchingRule, "acme", e.worker)
	})

	t.Run("the fence of another namespace", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
			c.Exchanges = failingExchanges{ExchangeStore: c.Exchanges, err: namespace.ErrFenced}
		})
		c := good(e, x, spiffeWorker)
		_, err := exchange(t.Context(), x, "acme", c)

		if !isErr(err, trustrulesvc.ErrIssueFailed) || e.tokenCount(e.worker) != 0 ||
			len(e.exchangeEntries("acme")) != 0 {
			t.Fatalf(
				"error = %v, tokens = %d, entries = %d",
				err,
				e.tokenCount(e.worker),
				len(e.exchangeEntries("acme")),
			)
		}
	})

	t.Run("a store error gives ErrIssueFailed, no token and no entry", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
			c.Exchanges = failingExchanges{ExchangeStore: c.Exchanges, err: errDiskFull}
		})
		c := good(e, x, spiffeWorker)
		res, err := exchange(t.Context(), x, "acme", c)

		if !isErr(err, trustrulesvc.ErrIssueFailed) || res.Secret.Reveal() != "" || e.tokenCount(e.worker) != 0 ||
			len(e.exchangeEntries("acme")) != 0 {
			t.Fatalf("error = %v, secret = %q, tokens = %d", err, "redacted", e.tokenCount(e.worker))
		}
	})

	t.Run("a leaf that expired between verify and exchange", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(nil)
		c := good(e, x, spiffeWorker)
		e.advance(time.Hour)

		_, err := exchange(t.Context(), x, "acme", c)
		e.mustRefuse(err, trustrulesvc.ErrExpired, "acme", e.worker)
	})
}

// TestMtlsExchangeBundleRotationAudit is the audit test of the bundle refusal:
// a rotate between verify and record gives a denial entry and no token.
func TestMtlsExchangeBundleRotationAudit(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)

	c, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	next := newTestCA(t, "ca acme 2")
	e.rotate(next)

	res, err := exchange(t.Context(), x, "acme", c)
	if res.Secret.Reveal() != "" {
		t.Fatal("a token was issued after the rotate")
	}

	e.mustRefuse(err, trustrulesvc.ErrNoMatchingRule, "acme", e.worker)

	if entries := e.exchangeEntries("acme"); len(entries) != 1 || entries[0].Reason == "" {
		t.Fatalf("entries = %+v", entries)
	}

	// After a new verify, the exchange gives a token.
	c2, err := verify(t.Context(), x, "acme", [][]byte{next.leaf(t, goodLeaf(spiffeWorker))})
	if err != nil {
		t.Fatalf("verify again: %v", err)
	}

	if _, err := exchange(t.Context(), x, "acme", c2); err != nil {
		t.Fatalf("exchange after a new verify: %v", err)
	}
}

// wrongNamespaces answers a namespace that was not asked for.
type wrongNamespaces struct {
	trustrulesvc.Namespaces

	answer namespace.Name
}

func (w wrongNamespaces) WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, err := w.Namespaces.WritableHere(ctx, name)
	ns.Name = w.answer

	return ns, err
}

// listSpy counts the calls of List.
type listSpy struct {
	trustrule.Store

	lists *atomic.Int64
}

func (l listSpy) List(ctx context.Context, ns namespace.Name) ([]trustrule.Rule, error) {
	l.lists.Add(1)

	return l.Store.List(ctx, ns)
}

// TestMtlsExchangeWrongNamespaceAnswer sends an answer of the namespace
// service for another namespace to Exchange.
func TestMtlsExchangeWrongNamespaceAnswer(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)

	chain, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	lists := new(atomic.Int64)
	bad := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
		c.Namespaces = wrongNamespaces{Namespaces: c.Namespaces, answer: "other"}
		c.Rules = listSpy{Store: c.Rules, lists: lists}
	})

	if _, err := exchange(t.Context(), bad, "acme", chain); !isErr(err, trustrulesvc.ErrIssueFailed) ||
		e.tokenCount(e.worker) != 0 || len(e.exchangeEntries("acme")) != 0 || lists.Load() != 0 {
		t.Fatalf("error = %v, tokens = %d, rule reads = %d", err, e.tokenCount(e.worker), lists.Load())
	}
}

// editedPrincipals changes the principal that the real store answers.
type editedPrincipals struct {
	trustrulesvc.Principals

	edit func(*principal.Principal)
	err  error
}

func (s editedPrincipals) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	if s.err != nil {
		return principal.Principal{}, s.err
	}

	p, err := s.Principals.GetPrincipal(ctx, id)
	if err != nil {
		return p, err
	}

	s.edit(&p)

	return p, nil
}

// TestMtlsExchangeForgedChain sends chain values that only a bug could make
// to Exchange, as if VerifyChain and Verify had failed.
func TestMtlsExchangeForgedChain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		spiffe  string
		bundle  func(e *mtlsEnv) trustrule.BundleID
		version int64
		want    error
	}{
		{"no bundle", spiffeWorker, func(*mtlsEnv) trustrule.BundleID { return "" }, 1, trustrulesvc.ErrBundleUnknown},
		{
			"no bundle version", spiffeWorker, func(e *mtlsEnv) trustrule.BundleID { return e.bundleAcme.ID }, 0,
			trustrulesvc.ErrBundleUnknown,
		},
		{"wildcard SPIFFE ID", "spiffe://example.org/ns/acme/sa/*", func(e *mtlsEnv) trustrule.BundleID {
			return e.bundleAcme.ID
		}, 1, trustrulesvc.ErrInvalid},
		{
			"empty SPIFFE ID", "", func(e *mtlsEnv) trustrule.BundleID { return e.bundleAcme.ID }, 1,
			trustrulesvc.ErrNoCertificate,
		},
		{"a bundle of another namespace", spiffeWorker, func(e *mtlsEnv) trustrule.BundleID {
			b, err := e.store.TrustRules().GetBundle(e.t.Context(), "other")
			if err != nil {
				e.t.Fatalf("get bundle: %v", err)
			}

			return b.ID
		}, 1, trustrulesvc.ErrNoMatchingRule},
		{"a bundle version that never existed", spiffeWorker, func(e *mtlsEnv) trustrule.BundleID {
			return e.bundleAcme.ID
		}, 99, trustrulesvc.ErrNoMatchingRule},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newMtlsEnv(t)
			leaf, err := x509.ParseCertificate(e.caAcme.leaf(t, goodLeaf(spiffeWorker)))
			if err != nil {
				t.Fatalf("parse leaf: %v", err)
			}

			c := trustrulesvc.ForgeChain(leaf, tc.spiffe, tc.bundle(e), tc.version)
			_, err = exchange(t.Context(), e.exchanger(nil), "acme", c)
			e.mustRefuse(err, tc.want, "acme", e.worker)
		})
	}
}

// TestMtlsExchangePrincipalHostile sends principals that fail the check to
// Exchange.
func TestMtlsExchangePrincipalHostile(t *testing.T) {
	t.Parallel()

	cases := map[string]editedPrincipals{
		"disabled": {Principals: nil, edit: func(p *principal.Principal) { p.DisabledAt = exchangeNow() }, err: nil},
		"missing":  {Principals: nil, edit: nil, err: principal.ErrNotFound},
	}

	for name, fake := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newMtlsEnv(t)
			x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
				fake.Principals = c.Principals
				c.Principals = fake
			})

			chain, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
			if err != nil {
				t.Fatalf("verify: %v", err)
			}

			_, err = exchange(t.Context(), x, "acme", chain)
			e.mustRefuse(err, trustrule.ErrPrincipal, "acme", e.worker)
		})
	}
}
