package trust_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trust"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

var errStoreDown = errors.New("the exchange store is down")

func checkNoCertificate(t *testing.T) {
	t.Helper()

	guard, guardErr := trust.NewGuard(trust.GuardConfig{
		MaxBodyBytes: 0, MaxPerWindow: 0, Window: 0, Now: nil,
	})
	if guardErr != nil {
		t.Fatalf("NewGuard = %v", guardErr)
	}

	for i := range 1000 {
		if checkErr := guard.Check(trust.CheckInput{HasCert: false, BodyLen: 10}); !errors.Is(
			checkErr, trust.ErrNoCertificate,
		) {
			t.Fatalf("call %d: Check with no certificate = %v, want ErrNoCertificate", i, checkErr)
		}
	}
}

func checkBoundedRead(t *testing.T) {
	t.Helper()

	guard, guardErr := trust.NewGuard(trust.GuardConfig{
		MaxBodyBytes: 16, MaxPerWindow: 0, Window: 0, Now: nil,
	})
	if guardErr != nil {
		t.Fatalf("NewGuard = %v", guardErr)
	}

	body, readErr := guard.ReadBody(strings.NewReader(strings.Repeat("a", 16)))
	if readErr != nil || len(body) != 16 {
		t.Fatalf("ReadBody at the cap = %d bytes, %v; want 16 bytes", len(body), readErr)
	}

	// The reader holds far more than the cap: the read must stop at cap+1.
	over := &countingReader{left: 1 << 30, read: 0}

	if _, overErr := guard.ReadBody(over); !errors.Is(overErr, trust.ErrInvalid) {
		t.Fatalf("ReadBody over the cap = %v, want ErrInvalid", overErr)
	}

	if over.read > 17 {
		t.Fatalf("ReadBody read %d bytes, want at most 17", over.read)
	}
}

type countingReader struct {
	left int
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.left == 0 {
		return 0, io.EOF
	}

	n := min(len(p), c.left)
	c.left -= n
	c.read += n

	return n, nil
}

func checkOversizeBody(t *testing.T) {
	t.Helper()

	guard, guardErr := trust.NewGuard(trust.GuardConfig{
		MaxBodyBytes: 0, MaxPerWindow: 0, Window: 0, Now: nil,
	})
	if guardErr != nil {
		t.Fatalf("NewGuard = %v", guardErr)
	}

	in := trust.CheckInput{HasCert: true, BodyLen: trust.MaxBodyBytes + 1}

	for i := range 1000 {
		if checkErr := guard.Check(in); !errors.Is(checkErr, trust.ErrInvalid) {
			t.Fatalf("call %d: Check oversize = %v, want ErrInvalid", i, checkErr)
		}
	}
}

func checkFloodRate(t *testing.T) {
	t.Helper()

	now := mtlsTestNow()

	guard, guardErr := trust.NewGuard(trust.GuardConfig{
		MaxBodyBytes: 0, MaxPerWindow: 2, Window: time.Minute,
		Now: func() time.Time { return now },
	})
	if guardErr != nil {
		t.Fatalf("NewGuard = %v", guardErr)
	}

	for range 2 {
		if checkErr := guard.Check(trust.CheckInput{HasCert: true, BodyLen: 10}); checkErr != nil {
			t.Fatalf("Check within rate = %v, want nil", checkErr)
		}
	}

	if checkErr := guard.Check(trust.CheckInput{HasCert: true, BodyLen: 10}); !errors.Is(
		checkErr, trust.ErrRateLimited,
	) {
		t.Fatalf("Check past the rate = %v, want ErrRateLimited", checkErr)
	}
}

func checkStoreFailure(t *testing.T) {
	t.Helper()

	setup := newVerifySetup(t)

	chain, verifyErr := trust.VerifyChain([][]byte{setup.LeafDER}, setup.Roots, setup.Now, goodChecker)
	if verifyErr != nil {
		t.Fatalf("VerifyChain = %v", verifyErr)
	}

	rule := newMtlsRule(setup.SPIFFEID, "prn-worker", "deploy")
	fixture := newExchangeFixture()
	fixture.exchanges.err = errStoreDown

	_, exchangeErr := fixture.exchanger.Exchange(t.Context(), &trust.ExchangeInput{
		Namespace: namespace.Name("acme"), Chain: chain, Rules: []trustrule.Rule{rule}, Bundle: "trb-1",
		Fence: namespace.Fence{Namespace: namespace.Name("acme"), FailoverVersion: 1}, Entry: testEntry(),
	})
	if !errors.Is(exchangeErr, trust.ErrIssueFailed) {
		t.Fatalf("Exchange with a store failure = %v, want ErrIssueFailed", exchangeErr)
	}

	if len(fixture.exchanges.exchanges) != 0 || len(fixture.exchanges.entries) != 0 {
		t.Fatal("the failed exchange stored a token or an entry")
	}
}

func checkBadGuardBounds(t *testing.T) {
	t.Helper()

	_, guardErr := trust.NewGuard(trust.GuardConfig{
		MaxBodyBytes: -1, MaxPerWindow: 1, Window: time.Minute, Now: mtlsTestNow,
	})
	if !errors.Is(guardErr, trust.ErrInvalid) {
		t.Fatalf("NewGuard with a bad body cap = %v, want ErrInvalid", guardErr)
	}
}

// TestMtlsExchangeLimitHostile floods the handler with no-certificate calls
// and oversize bodies, and forces a store error: each call gets no token.
func TestMtlsExchangeLimitHostile(t *testing.T) {
	t.Parallel()

	checkNoCertificate(t)
	checkOversizeBody(t)
	checkBoundedRead(t)
	checkFloodRate(t)
	checkStoreFailure(t)
	checkBadGuardBounds(t)
}
