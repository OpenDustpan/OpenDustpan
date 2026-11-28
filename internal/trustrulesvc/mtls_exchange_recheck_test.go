package trustrulesvc_test

import (
	"context"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// recordingExchanges records each exchange that the service sends and
// answers err. It never calls the real store, so it stores no token.
type recordingExchanges struct {
	trustrule.ExchangeStore

	err  error
	mu   *sync.Mutex
	seen *[]trustrule.Exchange
}

func (r recordingExchanges) RecordExchange(
	_ context.Context, x trustrule.Exchange, _ namespace.Fence, _ audit.Entry,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	*r.seen = append(*r.seen, x)

	return r.err
}

// TestMtlsExchangeStoreRefusal sends the refusals of the store recheck to the
// service, as if the store had found a change after the read of the rules.
func TestMtlsExchangeStoreRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want error
	}{
		// A rule that another call added after the read makes two rules.
		{"the store finds no single rule", trustrule.ErrNotFound, trustrulesvc.ErrNoMatchingRule},
		{"the store refuses the principal", trustrule.ErrPrincipal, trustrule.ErrPrincipal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newMtlsEnv(t)
			// A rule of another SPIFFE ID in the same bundle, so that the
			// matched rule is one of two.
			e.createRule("acme", "batch", "spiffe://example.org/ns/acme/sa/batch", e.worker, e.bundleAcme.ID)

			var (
				mu   sync.Mutex
				seen []trustrule.Exchange
			)

			x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
				c.Exchanges = recordingExchanges{ExchangeStore: c.Exchanges, err: tc.err, mu: &mu, seen: &seen}
			})

			chain, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
			if err != nil {
				t.Fatalf("verify: %v", err)
			}

			res, err := exchange(t.Context(), x, "acme", chain)
			if res.Secret.Reveal() != "" || res.Token.ID != "" {
				t.Fatal("the exchange returned a token after a store refusal")
			}

			if isErr(err, trustrulesvc.ErrIssueFailed) {
				t.Fatalf("error = %v, want a refusal, not ErrIssueFailed", err)
			}

			// mustRefuse checks the error, no token in the store and exactly
			// one denial entry.
			e.mustRefuse(err, tc.want, "acme", e.worker)

			mu.Lock()
			defer mu.Unlock()

			if len(seen) != 1 {
				t.Fatalf("the store received %d exchanges, want 1", len(seen))
			}

			checkSentExchange(t, e, chain, &seen[0])
		})
	}
}

// checkSentExchange checks that the store received the SPIFFE ID of the
// verified chain and the ID of the matched rule. The service matches a rule
// only when its pattern is the exact SPIFFE ID, so the two texts are equal;
// the test compares with the leaf of the chain and the ID of the rule.
func checkSentExchange(t *testing.T, e *mtlsEnv, chain trustrulesvc.VerifiedChain, x *trustrule.Exchange) {
	t.Helper()

	if chain.SPIFFEID() != spiffeWorker {
		t.Fatalf("chain SPIFFE ID = %q, want %q", chain.SPIFFEID(), spiffeWorker)
	}

	if x.SPIFFEID != chain.SPIFFEID() {
		t.Fatalf("exchange SPIFFE ID = %q, want the SPIFFE ID of the leaf %q", x.SPIFFEID, chain.SPIFFEID())
	}

	if x.Rule != e.rule.ID {
		t.Fatalf("exchange rule = %q, want the matched rule %q", x.Rule, e.rule.ID)
	}

	if x.BundleID != chain.BundleID() || x.BundleVersion != chain.BundleVersion() {
		t.Fatalf("exchange bundle = %q %d, want %q %d", x.BundleID, x.BundleVersion, chain.BundleID(),
			chain.BundleVersion())
	}
}
