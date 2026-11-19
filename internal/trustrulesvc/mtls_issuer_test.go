package trustrulesvc_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
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

	t.Run("a rate limit writes one denial entry", testRefuseRateLimit)
	t.Run("held counts are written with the entry", testRefuseHeld)
	t.Run("a canceled context still writes the entry", testRefuseCanceled)
	t.Run("an unknown namespace writes no entry", testRefuseUnknown)
}

func testRefuseRateLimit(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)

	if _, err := x.RefuseCall(t.Context(), "acme", emptyEntry(), trustrulesvc.ErrRateLimited, nil); err != nil {
		t.Fatalf("RefuseCall: %v, want nil", err)
	}

	entries := e.exchangeEntries("acme")
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied || entries[0].Reason == "" {
		t.Fatalf("entries = %+v, want one denial entry with a reason", entries)
	}
}

func testRefuseHeld(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)
	held := []trustrulesvc.HeldCount{{Namespace: "acme", Count: 7}, {Namespace: "nowhere", Count: 2}}

	unwritten, err := x.RefuseCall(t.Context(), "acme", emptyEntry(), trustrulesvc.ErrRateLimited, held)
	if err != nil || len(unwritten) != 0 {
		t.Fatalf("RefuseCall = %v, %v, want no error and no unwritten count", unwritten, err)
	}

	var found bool

	entries := e.exchangeEntries("acme")
	for i := range entries {
		found = found || entries[i].Reason == "7 refused exchanges wrote no entry of their own"
	}

	if !found {
		t.Fatalf("entries = %+v, want the held count of 7", e.exchangeEntries("acme"))
	}
}

func testRefuseCanceled(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := x.RefuseCall(ctx, "acme", emptyEntry(), trustrulesvc.ErrRateLimited, nil); err != nil {
		t.Fatalf("RefuseCall: %v, want nil", err)
	}

	if n := len(e.exchangeEntries("acme")); n != 1 {
		t.Fatalf("%d entries, want 1", n)
	}
}

func testRefuseUnknown(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)

	_, err := x.RefuseCall(t.Context(), "nowhere", emptyEntry(), trustrulesvc.ErrRateLimited, nil)
	if !isErr(err, trustrulesvc.ErrBundleUnknown) || isErr(err, trustrulesvc.ErrRateLimited) {
		t.Fatalf("error = %v, want ErrBundleUnknown", err)
	}

	if n := len(e.exchangeEntries("acme")) + len(e.exchangeEntries("other")); n != 0 {
		t.Fatalf("%d entries, want none", n)
	}
}
