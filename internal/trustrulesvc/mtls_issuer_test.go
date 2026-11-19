package trustrulesvc_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	t.Run("a count for the other names goes to the log of the installation", testRefuseSharedCount)
	t.Run("a namespace that is not home gives its own error", testRefuseNotHome)
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

// notHomeNamespaces is a namespace reader for which the namespace abroad
// belongs to another installation.
type notHomeNamespaces struct {
	trustrulesvc.Namespaces

	abroad namespace.Name
}

func (n notHomeNamespaces) WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == n.abroad {
		return namespace.Namespace{}, namespace.ErrNotHome
	}

	return n.Namespaces.WritableHere(ctx, name)
}

// notHomeAudit is an audit store whose append to the namespace abroad fails
// with namespace.ErrNotHome.
type notHomeAudit struct {
	audit.Store

	abroad namespace.Name
}

func (n notHomeAudit) Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if e.Namespace == n.abroad {
		return audit.Entry{}, namespace.ErrNotHome
	}

	return n.Store.Append(ctx, fence, e)
}

func testRefuseSharedCount(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(nil)
	held := []trustrulesvc.HeldCount{{Namespace: "", Count: 5}}

	request := emptyEntry()
	request.SourceIP, request.UserAgent = "192.0.2.9", "attacker"

	unwritten, err := x.RefuseCall(t.Context(), "acme", request, trustrulesvc.ErrRateLimited, held)
	if err != nil || len(unwritten) != 0 {
		t.Fatalf("RefuseCall = %v, %v, want no error and no unwritten count", unwritten, err)
	}

	l, err := e.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list server audit: %v", err)
	}

	want := "5 refused mTLS exchanges to namespaces beyond the 8 names the guard tracks in a window wrote no entry of their own"

	var found bool

	for i := range l.Entries {
		if l.Entries[i].Reason != want {
			continue
		}

		found = true

		if l.Entries[i].SourceIP != "" || l.Entries[i].UserAgent != "" {
			t.Fatalf("entry has the address %q and the agent %q of the current caller, want none",
				l.Entries[i].SourceIP, l.Entries[i].UserAgent)
		}
	}

	if !found {
		t.Fatalf("server entries = %+v, want one with the reason %q", l.Entries, want)
	}
}

func testRefuseNotHome(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	x := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
		c.Namespaces = notHomeNamespaces{Namespaces: c.Namespaces, abroad: "other"}
	})

	held := []trustrulesvc.HeldCount{{Namespace: "acme", Count: 3}}

	unwritten, err := x.RefuseCall(t.Context(), "other", emptyEntry(), trustrulesvc.ErrRateLimited, held)
	if !isErr(err, trustrulesvc.ErrNamespaceNotHome) || isErr(err, trustrulesvc.ErrIssueFailed) {
		t.Fatalf("error = %v, want ErrNamespaceNotHome and not ErrIssueFailed", err)
	}

	if len(unwritten) != 1 || unwritten[0].Count != 3 {
		t.Fatalf("unwritten = %v, want the held count back: nothing was written", unwritten)
	}

	// A held count for a namespace that is not home is dropped: no entry can
	// be written here, so a give-back would repeat for ever.
	y := e.exchanger(func(c *trustrulesvc.MtlsConfig) {
		c.Audit = notHomeAudit{Store: c.Audit, abroad: "other"}
	})
	dropped := []trustrulesvc.HeldCount{{Namespace: "other", Count: 4}}

	unwritten, err = y.RefuseCall(t.Context(), "acme", emptyEntry(), trustrulesvc.ErrRateLimited, dropped)
	if err != nil || len(unwritten) != 0 {
		t.Fatalf("RefuseCall = %v, %v, want no error and the count dropped", unwritten, err)
	}
}
