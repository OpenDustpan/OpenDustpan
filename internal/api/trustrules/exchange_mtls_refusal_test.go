package trustrules_test

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ghostURL is the exchange route of a namespace that does not exist.
func ghostURL(i int) string {
	return fmt.Sprintf("/api/dustpan/v1/namespaces/ghost%d/trust-rules/exchange", i)
}

// testClock is a clock that the tests move. It starts at the test time.
type testClock struct {
	offset atomic.Int64
}

func (c *testClock) now() time.Time { return testNow().Add(time.Duration(c.offset.Load())) }

func (c *testClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

// openOpts returns the options of a guard with a limit of one call.
func openOpts(clock *testClock, onRefused func(string), failServer *atomic.Bool) mtlsOpts {
	return mtlsOpts{
		revocation: noRevocation, failWrite: false, limit: 1, noMtls: false, canceled: false,
		guardNow: clock.now, onRefused: onRefused, failServer: failServer,
	}
}

// serverReasons returns the reasons of the entries of the installation log.
func (e *env) serverReasons() []string {
	e.t.Helper()

	listing, err := e.store.List(e.t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		e.t.Fatalf("list server audit: %v", err)
	}

	out := make([]string, 0, len(listing.Entries))
	for i := range listing.Entries {
		out = append(out, listing.Entries[i].Reason)
	}

	return out
}

// hasReason reports whether one reason has all the parts.
func hasReason(reasons []string, parts ...string) bool {
	for _, r := range reasons {
		ok := true

		for _, p := range parts {
			if !strings.Contains(r, p) {
				ok = false
			}
		}

		if ok {
			return true
		}
	}

	return false
}

// TestExchangeMtlsRefusedReasons checks the reason that the handler gives to
// the counter of refused calls.
func TestExchangeMtlsRefusedReasons(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)

	var (
		mu      sync.Mutex
		reasons []string
	)

	m := e.newMtlsServer(openOpts(&testClock{offset: atomic.Int64{}}, func(r string) {
		mu.Lock()
		defer mu.Unlock()

		reasons = append(reasons, r)
	}, nil))
	cert := ca.goodLeaf(t)

	for range 3 {
		m.call(t, mtlsExchangeURL, &cert, nil)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(reasons) != 2 || reasons[0] != "rate-limit" || reasons[1] != "rate-limit" {
		t.Fatalf("reasons = %v, want [rate-limit rate-limit]", reasons)
	}
}

// TestExchangeMtlsUnknownNamesFreeSlots checks that refusals to names that do
// not exist do not take the slots of a real namespace: the real namespace
// still gets its denial entry.
func TestExchangeMtlsUnknownNamesFreeSlots(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	m := e.newMtlsServer(openOpts(&testClock{offset: atomic.Int64{}}, nil, nil))
	cert := ca.goodLeaf(t)

	m.call(t, mtlsExchangeURL, &cert, nil)

	for i := 1; i <= 9; i++ {
		m.call(t, ghostURL(i), &cert, nil)
	}

	for range 3 {
		m.call(t, mtlsExchangeURL, &cert, nil)
	}

	if got := len(e.deniedExchangeEntries()); got != 1 {
		t.Fatalf("%d denial entries in acme after 9 unknown names, want 1", got)
	}
}

// TestExchangeMtlsUnknownNamesBound checks that the namespace reads for
// names that do not exist stay bounded in one window.
func TestExchangeMtlsUnknownNamesBound(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	counter := &countingNamespaces{Namespaces: nil, writable: atomic.Int64{}, fail: nil, failing: nil}
	m := e.newMtlsServerWith(
		openOpts(&testClock{offset: atomic.Int64{}}, nil, nil),
		func(n trustrulesvc.Namespaces) trustrulesvc.Namespaces {
			counter.Namespaces = n

			return counter
		},
	)
	cert := ca.goodLeaf(t)

	m.call(t, mtlsExchangeURL, &cert, nil)

	before := counter.writable.Load()

	for i := 1; i <= 20; i++ {
		m.call(t, ghostURL(i), &cert, nil)
	}

	// 8 names are freed, then 8 names and the shared slot stay marked.
	if got := counter.writable.Load() - before; got > 17 {
		t.Fatalf("%d namespace reads for 20 unknown names, want at most 17", got)
	}
}

// TestExchangeMtlsNonHomeNamespace checks that refusals to a namespace that
// another installation owns cost one namespace read in a window, and that a
// store that always fails costs a bounded number of reads.
func TestExchangeMtlsNonHomeNamespace(t *testing.T) {
	t.Parallel()

	t.Run("a namespace that is not home", func(t *testing.T) {
		t.Parallel()
		testRefusalReads(t, namespace.ErrNotHome, 1)
	})
	t.Run("a store that always fails", func(t *testing.T) {
		t.Parallel()
		testRefusalReads(t, errDisk, 3)
	})
}

func testRefusalReads(t *testing.T, failWith error, most int64) {
	t.Helper()

	e, ca, _ := mtlsFixture(t)
	failing := &atomic.Bool{}
	counter := &countingNamespaces{Namespaces: nil, writable: atomic.Int64{}, fail: failWith, failing: failing}
	m := e.newMtlsServerWith(
		openOpts(&testClock{offset: atomic.Int64{}}, nil, nil),
		func(n trustrulesvc.Namespaces) trustrulesvc.Namespaces {
			counter.Namespaces = n

			return counter
		},
	)
	cert := ca.goodLeaf(t)

	m.call(t, mtlsExchangeURL, &cert, nil)
	failing.Store(true)

	before := counter.writable.Load()

	for i := 2; i <= 13; i++ {
		status, _, body := m.call(t, mtlsExchangeURL, &cert, nil)
		if status != http.StatusTooManyRequests {
			t.Fatalf("call %d = %d %s, want 429", i, status, body)
		}
	}

	if got := counter.writable.Load() - before; got > most {
		t.Fatalf("%d namespace reads for 12 refused calls, want at most %d", got, most)
	}

	if got := len(e.deniedExchangeEntries()); got != 0 {
		t.Fatalf("%d denial entries in acme, want 0", got)
	}
}

// exhaust makes the sequence of an attacker in one window: 17 refused calls
// to names that do not exist, and then 3 refused calls to acme.
func exhaust(t *testing.T, m *mtlsServer, cert *tls.Certificate) {
	t.Helper()

	m.call(t, mtlsExchangeURL, cert, nil)

	for i := 1; i <= 17; i++ {
		m.call(t, ghostURL(i), cert, nil)
	}

	for range 3 {
		m.call(t, mtlsExchangeURL, cert, nil)
	}
}

// TestExchangeMtlsNamesBeyondBound checks where the count of the refusals to a
// real namespace goes when 17 other names filled the slots of a window: acme
// gets no entry, and the log of the installation gets the count, with a
// reason that says why.
func TestExchangeMtlsNamesBeyondBound(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	clock := &testClock{offset: atomic.Int64{}}
	m := e.newMtlsServer(openOpts(clock, nil, nil))
	cert := ca.goodLeaf(t)

	exhaust(t, m, &cert)

	if got := len(e.deniedExchangeEntries()); got != 0 {
		t.Fatalf("%d denial entries in acme in the window of the attack, want 0", got)
	}

	clock.advance(time.Hour)

	m.call(t, mtlsExchangeURL, &cert, nil)
	m.call(t, mtlsExchangeURL, &cert, nil)

	if got := len(e.deniedExchangeEntries()); got != 1 {
		t.Fatalf("%d denial entries in acme in the next window, want 1", got)
	}

	reasons := e.serverReasons()
	if !hasReason(reasons, "3 refused mTLS exchanges to namespaces beyond the 8 names the guard tracks") {
		t.Fatalf("server reasons = %q, want the count of 3 with the reason of the bound", reasons)
	}
}

// TestExchangeMtlsHeldCountGivenBack checks that a count that the server log
// could not take returns to the guard, and a later first refusal writes it.
func TestExchangeMtlsHeldCountGivenBack(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	clock := &testClock{offset: atomic.Int64{}}
	failServer := &atomic.Bool{}
	m := e.newMtlsServer(openOpts(clock, nil, failServer))
	cert := ca.goodLeaf(t)

	exhaust(t, m, &cert)

	clock.advance(time.Hour)
	failServer.Store(true)

	m.call(t, mtlsExchangeURL, &cert, nil)
	m.call(t, mtlsExchangeURL, &cert, nil)

	if hasReason(e.serverReasons(), "3 refused mTLS exchanges") {
		t.Fatal("the count reached the server log while its write fails")
	}

	failServer.Store(false)
	clock.advance(time.Hour)

	m.call(t, mtlsExchangeURL, &cert, nil)
	m.call(t, mtlsExchangeURL, &cert, nil)

	if !hasReason(e.serverReasons(), "3 refused mTLS exchanges") {
		t.Fatalf("server reasons = %q, want the count of 3 after the write works", e.serverReasons())
	}
}
