package runnerapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// hostileToken is the secret of a handoff token that a refused request holds.
// No entry may hold it or its hash.
const hostileToken = runner.HandoffTokenPrefix + "hostile-secret-value"

var errDriver = errors.New("driver text")

// handoffFixture is a handler with an in-memory audit store, a fixed clock and
// a counter of failed appends.
type handoffFixture struct {
	handler  *runnerapi.Handler
	store    *testAudit
	request  *http.Request
	failures *atomic.Int32
	clock    *fakeClock
}

// fakeClock is the injected clock of the tests. It moves only when a test
// advances it.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = c.at.Add(d)
}

func newFakeClock() *fakeClock {
	return &fakeClock{mu: sync.Mutex{}, at: time.Date(2026, time.November, 7, 12, 0, 0, 0, time.UTC)}
}

func newHandoffFixture(t *testing.T) *handoffFixture {
	t.Helper()

	store, failures, clock := newTestAudit(), new(atomic.Int32), newFakeClock()

	return &handoffFixture{
		handler: runnerapi.NewHandoffAuditHandler(
			store, slog.New(slog.DiscardHandler), clock.Now, func() { failures.Add(1) },
		),
		store:    store,
		request:  httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/runner/v1/session", http.NoBody),
		failures: failures,
		clock:    clock,
	}
}

// entries returns all entries of the installation scope, oldest first.
func (f *handoffFixture) entries(t *testing.T) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return listing.Entries
}

// refuseFrom records a refused exchange from a client address.
func (f *handoffFixture) refuseFrom(t *testing.T, addr string, err error) {
	t.Helper()

	r := f.request.Clone(t.Context())
	r.RemoteAddr = addr + ":4000"

	runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), f.handler, r, err)
}

// onlyEntry returns the one entry of the store, and fails when there is not
// exactly one.
func (f *handoffFixture) onlyEntry(t *testing.T) audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(listing.Entries) != 1 {
		t.Fatalf("entries = %d, want exactly 1", len(listing.Entries))
	}

	return listing.Entries[0]
}

func (f *handoffFixture) entryCount(t *testing.T) int {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return len(listing.Entries)
}

func changeOf(e *audit.Entry, key string) string {
	for _, c := range e.Changes {
		if c.Key == key {
			return c.New
		}
	}

	return "<missing>"
}

// holdsToken reports whether any field of e holds the token or its hash.
func holdsToken(e *audit.Entry) bool {
	sum := sha256.Sum256([]byte(hostileToken))
	text := fmt.Sprintf("%+v", *e)

	return strings.Contains(text, hostileToken) || strings.Contains(text, hex.EncodeToString(sum[:]))
}

func runnerOf(id string) *runner.Runner {
	var rn runner.Runner

	rn.ID, rn.Namespace = id, "acme"

	return &rn
}

// TestHandoffRefusalWritesOneEntry sends each refusal branch to the step that
// writes the entry, as if every other layer had failed, and asserts exactly one
// entry with the reason code and the runner ID, and no token.
func TestHandoffRefusalWritesOneEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		verified bool
		reason   runner.HandoffReason
		subject  string
		runnerID string
	}{
		{
			"unknown token", fmt.Errorf("select: %w", runner.ErrNotFound), false, runner.HandoffUnknown,
			"unverified-launcher", "",
		},
		{
			"expired token", fmt.Errorf("check: %w", runner.ErrTokenExpired), false, runner.HandoffExpired,
			"unverified-launcher", "",
		},
		{
			"used token", fmt.Errorf("check: %w", runner.ErrTokenUsed), false, runner.HandoffUsed,
			"unverified-launcher", "",
		},
		{"claim lost on exchange", runner.ErrClaimLost, false, runner.HandoffClaimLost, "unverified-launcher", ""},
		{"wrong runner", runner.ErrWrongRunner, true, runner.HandoffWrongRunner, "rn-abc123", "rn-abc123"},
		{"claim lost on issue", runner.ErrClaimLost, true, runner.HandoffClaimLost, "rn-abc123", "rn-abc123"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newHandoffFixture(t)

			if tc.verified {
				runnerapi.RecordHandoffClaimRefusal(t.Context(), f.handler, f.request, tc.err, runnerOf("rn-abc123"))
			} else {
				runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), f.handler, f.request, tc.err)
			}

			e := f.onlyEntry(t)

			if e.Outcome != audit.OutcomeDenied || e.Reason != string(tc.reason) || e.Subject != tc.subject {
				t.Errorf("entry = %s %q %q, want denied %q %q", e.Outcome, e.Reason, e.Subject, tc.reason, tc.subject)
			}

			checkChanges(t, &e, map[string]string{
				"event": string(runner.EventHandoffRefused), "reason": string(tc.reason), "runner": tc.runnerID,
				"runnerVerified": strconv.FormatBool(tc.verified),
			})

			if holdsToken(&e) || f.failures.Load() != 0 {
				t.Errorf("token in entry %v, append failures %d, want none", holdsToken(&e), f.failures.Load())
			}
		})
	}
}

// checkChanges fails for each change of e that differs from want.
func checkChanges(t *testing.T, e *audit.Entry, want map[string]string) {
	t.Helper()

	for key, value := range want {
		if got := changeOf(e, key); got != value {
			t.Errorf("change %s = %q, want %q", key, got, value)
		}
	}
}

// TestHandoffRefusalIgnoresOtherErrors proves that an error that is not a
// refusal of a token or a claim writes no entry, for either kind of caller.
func TestHandoffRefusalIgnoresOtherErrors(t *testing.T) {
	t.Parallel()

	for _, err := range []error{namespace.ErrUnavailable, runner.ErrBadProof, runner.ErrInvalid, errDriver, nil} {
		f := newHandoffFixture(t)

		runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), f.handler, f.request, err)
		runnerapi.RecordHandoffClaimRefusal(t.Context(), f.handler, f.request, err, runnerOf("rn-abc123"))

		if n := f.entryCount(t); n != 0 {
			t.Errorf("%v: entries = %d, want 0", err, n)
		}
	}
}

// TestHandoffRefusalHostileRunnerID sends a refusal with a runner ID that is a
// token, a control sequence or too long, as if every layer before the adapter
// had failed, and even with Verified set. The entry must not echo the ID.
func TestHandoffRefusalHostileRunnerID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		id       string
		verified bool
		want     string
	}{
		{"token as runner ID", hostileToken, true, ""},
		{"token as runner ID, unverified", hostileToken, false, ""},
		{"token behind the prefix", "rn-" + hostileToken + "\n", true, ""},
		{"line break in the ID", "rn-abc\r\nforged: yes", true, ""},
		{"too long", "rn-" + strings.Repeat("a", 200), true, ""},
		{"valid ID", "rn-abc123", true, "rn-abc123"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newHandoffFixture(t)
			ref := runner.HandoffRefusal{Reason: runner.HandoffUsed, Runner: tc.id, Verified: tc.verified}

			runnerapi.RecordHandoffRefusal(t.Context(), f.handler, f.request, &ref)

			e := f.onlyEntry(t)
			if got := changeOf(&e, "runner"); got != tc.want {
				t.Errorf("runner = %q, want %q", got, tc.want)
			}

			wantSubject := "unverified-launcher"
			if tc.want != "" {
				wantSubject = tc.want
			}

			if e.Subject != wantSubject || holdsToken(&e) || strings.ContainsAny(e.Subject, "\r\n") {
				t.Errorf("subject = %q, token in entry %v, want %q and no token",
					e.Subject, holdsToken(&e), wantSubject)
			}
		})
	}
}

// TestHandoffRefusalFloodIsBounded sends many forged refusals from one client.
// The refusal count stays whole, the entries stay bounded by the limiter, and
// the counter rises for each dropped entry.
func TestHandoffRefusalFloodIsBounded(t *testing.T) {
	t.Parallel()

	const flood = 200

	f := newHandoffFixture(t)
	dropped := new(atomic.Int32)

	small := denylimit.New(
		denylimit.Config{Window: 0, MaxKeys: 8, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: f.clock.Now},
	)

	runnerapi.LimitHandoffRefusals(f.handler, small, func() { dropped.Add(1) })

	for range flood {
		runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), f.handler, f.request, runner.ErrNotFound)
	}

	if got := f.entryCount(t); got != 1 {
		t.Errorf("entries after %d identical refusals = %d, want 1", flood, got)
	}

	if got := int(dropped.Load()); got != flood-1 {
		t.Errorf("dropped = %d, want %d", got, flood-1)
	}

	// Many client addresses stay inside the key bound: more entries than
	// MaxKeys never appear, and the rest count as dropped.
	for i := range flood {
		r := f.request.Clone(t.Context())
		r.RemoteAddr = fmt.Sprintf("10.1.%d.%d:4000", i/250, i%250+1)

		runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), f.handler, r, runner.ErrTokenUsed)
	}

	if got := f.entryCount(t); got > 1+8+1 {
		t.Errorf("entries after a flood from many addresses = %d, want at most %d", got, 1+8+1)
	}
}

// TestHandoffRefusalFailedAppendKeepsTheWrite proves that an append failure
// gives the write back, so the next refusal still writes an entry.
func TestHandoffRefusalFailedAppendKeepsTheWrite(t *testing.T) {
	t.Parallel()

	failing, clock := new(failingAudit), newFakeClock()
	h := runnerapi.NewHandoffAuditHandler(
		failing, slog.New(slog.DiscardHandler), clock.Now, func() {},
	)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/runner/v1/session", http.NoBody)
	dropped := new(atomic.Int32)

	defaults := denylimit.New(denylimit.Config{
		Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: clock.Now,
	})

	runnerapi.LimitHandoffRefusals(h, defaults, func() { dropped.Add(1) })

	runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), h, r, runner.ErrTokenUsed)
	runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), h, r, runner.ErrTokenUsed)

	if dropped.Load() != 0 {
		t.Errorf("dropped = %d after two failed appends, want 0: a failed write hid the next one", dropped.Load())
	}
}

// failingAudit refuses every append and puts a token in the error text.
type failingAudit struct{ testAudit }

var errStoreDown = errors.New("store down: " + hostileToken)

func (*failingAudit) AppendServer(context.Context, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errStoreDown
}

// TestHandoffRefusalAppendFailureFailsClosed proves that a failed append is
// counted once and does not panic, and that the log does not hold the text of
// the store error, which could hold a token.
func TestHandoffRefusalAppendFailureFailsClosed(t *testing.T) {
	t.Parallel()

	var logged strings.Builder

	failures := new(atomic.Int32)
	h := runnerapi.NewHandoffAuditHandler(
		new(failingAudit), slog.New(slog.NewTextHandler(&logged, nil)), func() time.Time { return time.Time{} },
		func() { failures.Add(1) },
	)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/runner/v1/session", http.NoBody)

	runnerapi.RecordUnverifiedHandoffRefusal(t.Context(), h, r, runner.ErrTokenUsed)

	if failures.Load() != 1 {
		t.Errorf("append failures = %d, want 1", failures.Load())
	}

	if strings.Contains(logged.String(), hostileToken) {
		t.Error("the log holds the text of the store error")
	}
}

// TestHandoffRefusalNextWindowCarriesSuppressedCount proves that the first
// refusal of the next window writes an entry with the count of the refusals
// that the earlier window held back.
func TestHandoffRefusalNextWindowCarriesSuppressedCount(t *testing.T) {
	t.Parallel()

	f := newHandoffFixture(t)

	for range 3 {
		f.refuseFrom(t, "10.0.0.1", runner.ErrTokenUsed)
	}

	if got := f.entryCount(t); got != 1 {
		t.Fatalf("entries in the first window = %d, want 1", got)
	}

	f.clock.Advance(denylimit.DefaultWindow + time.Second)
	f.refuseFrom(t, "10.0.0.1", runner.ErrTokenUsed)

	entries := f.entries(t)
	if len(entries) != 2 {
		t.Fatalf("entries after the next window = %d, want 2", len(entries))
	}

	if got := changeOf(&entries[1], "suppressed"); got != "2" {
		t.Errorf("suppressed = %q, want 2", got)
	}
}

// TestHandoffRefusalFlushesCountOfQuietKey proves that the count of a key that
// stays quiet goes out as an entry of its own, when a refusal of another key
// finds the window ended. A forged exchange from another address must not erase
// the count.
func TestHandoffRefusalFlushesCountOfQuietKey(t *testing.T) {
	t.Parallel()

	f := newHandoffFixture(t)

	for range 4 {
		f.refuseFrom(t, "10.0.0.1", runner.ErrTokenUsed)
	}

	f.clock.Advance(denylimit.DefaultWindow + time.Second)
	f.refuseFrom(t, "10.0.0.2", runner.ErrTokenUsed)

	entries := f.entries(t)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3: first entry, entry of the other key, flush entry", len(entries))
	}

	flushed := 0

	for i := range entries {
		if strings.HasPrefix(entries[i].Reason, "3 further refusals") && entries[i].Subject == "10.0.0.1" {
			flushed++
		}
	}

	if flushed != 1 {
		t.Errorf("flush entries for the quiet key = %d, want 1", flushed)
	}
}

// TestHandoffRefusalOverflowWritesAggregateEntry proves that a full key table
// does not silence the refusals: the aggregate entry is written with its count,
// and the count of the next window goes out too.
func TestHandoffRefusalOverflowWritesAggregateEntry(t *testing.T) {
	t.Parallel()

	f := newHandoffFixture(t)
	dropped := new(atomic.Int32)

	full := denylimit.New(denylimit.Config{
		Window: 0, MaxKeys: 2, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: f.clock.Now,
	})

	runnerapi.LimitHandoffRefusals(f.handler, full, func() { dropped.Add(1) })

	for _, addr := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		f.refuseFrom(t, addr, runner.ErrTokenUsed)
	}

	if got := f.entryCount(t); got != 3 {
		t.Fatalf("entries = %d, want 3: two keys and the aggregate", got)
	}

	if dropped.Load() != 1 {
		t.Errorf("dropped = %d, want 1", dropped.Load())
	}

	f.clock.Advance(denylimit.DefaultWindow + time.Second)
	f.refuseFrom(t, "10.0.0.5", runner.ErrTokenUsed)

	aggregates := 0

	for _, e := range f.entries(t) {
		if e.Subject == "denial-limiter" {
			aggregates++

			if strings.Contains(e.Reason, "1 refusals") && f.failures.Load() == 0 {
				aggregates += 10
			}
		}
	}

	if aggregates != 12 {
		t.Errorf("aggregate check = %d, want 12: two aggregate entries, one with the count 1", aggregates)
	}
}
