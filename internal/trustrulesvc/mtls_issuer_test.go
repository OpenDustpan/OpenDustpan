package trustrulesvc_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/accesstoken"
)

// lenientIssuer is a token issuer that does not check the leaf end time: it
// moves LeafNotAfter a day past the issue time.
type lenientIssuer struct{ inner trustrulesvc.MtlsTokenIssuer }

func (l lenientIssuer) NewMtlsTrustToken(ctx context.Context, in accesstoken.MtlsInput) (accesstoken.Issued, error) {
	in.LeafNotAfter = in.At.Add(24 * time.Hour)

	return l.inner.NewMtlsTrustToken(ctx, in)
}

// invalidIssuer is a token issuer that always refuses with ErrInvalid.
type invalidIssuer struct{}

func (invalidIssuer) NewMtlsTrustToken(context.Context, accesstoken.MtlsInput) (accesstoken.Issued, error) {
	return accesstoken.Issued{}, accesstoken.ErrInvalid
}

// TestMtlsExchangeLeafTimeOwnLayer checks the time of the leaf in Exchange
// with an issuer that does not check it, so no later layer refuses the leaf.
func TestMtlsExchangeLeafTimeOwnLayer(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(func(c *trustrulesvc.MtlsConfig) { c.Tokens = lenientIssuer{inner: e.tokens} })

	chain, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	e.advance(31 * time.Minute)

	res, err := exchange(t.Context(), x, "acme", chain)
	if res.Secret.Reveal() != "" {
		t.Fatal("a token was issued for an expired leaf")
	}

	e.mustRefuse(err, trustrulesvc.ErrExpired, "acme", e.worker)
}

// TestMtlsExchangeIssuerInvalid maps the refusal of the token service for a
// leaf that is not valid after the issue time to ErrExpired with a denial
// entry, not to ErrIssueFailed.
func TestMtlsExchangeIssuerInvalid(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(func(c *trustrulesvc.MtlsConfig) { c.Tokens = invalidIssuer{} })

	chain, err := verify(t.Context(), x, "acme", [][]byte{e.caAcme.leaf(t, goodLeaf(spiffeWorker))})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	_, err = exchange(t.Context(), x, "acme", chain)
	e.mustRefuse(err, trustrulesvc.ErrExpired, "acme", e.worker)

	if isErr(err, trustrulesvc.ErrIssueFailed) {
		t.Fatalf("error = %v, must not be ErrIssueFailed", err)
	}
}

// TestMtlsRefuseCall writes the denial entry of a call that the guard
// refused, and writes none for a namespace that does not exist.
func TestMtlsRefuseCall(t *testing.T) {
	t.Parallel()

	t.Run("a rate limit writes one denial entry", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(nil)

		err := x.RefuseCall(t.Context(), "acme", emptyEntry(), trustrulesvc.ErrRateLimited)
		e.mustRefuse(err, trustrulesvc.ErrRateLimited, "acme", e.worker)

		if entries := e.exchangeEntries("acme"); entries[0].Reason == "" {
			t.Fatalf("entry = %+v, want a reason", entries[0])
		}
	})

	t.Run("an unknown namespace writes no entry", func(t *testing.T) {
		t.Parallel()

		e := newMtlsEnv(t)
		x := e.exchanger(nil)

		err := x.RefuseCall(t.Context(), "nowhere", emptyEntry(), trustrulesvc.ErrRateLimited)
		if !isErr(err, trustrulesvc.ErrBundleUnknown) || isErr(err, trustrulesvc.ErrRateLimited) {
			t.Fatalf("error = %v, want ErrBundleUnknown", err)
		}

		if n := len(e.exchangeEntries("acme")) + len(e.exchangeEntries("other")); n != 0 {
			t.Fatalf("%d entries, want none", n)
		}
	})
}
