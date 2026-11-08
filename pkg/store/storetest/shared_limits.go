package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// CountingSender is a notify.Sender for webhook sinks that counts the
// messages it gets and sends nothing.
type CountingSender struct {
	mu sync.Mutex
	n  int
}

// NewCountingSender returns a sender with a count of zero.
func NewCountingSender() *CountingSender { return &CountingSender{mu: sync.Mutex{}, n: 0} }

// Kind implements notify.Sender.
func (*CountingSender) Kind() notify.SinkKind { return notify.SinkWebhook }

// Send implements notify.Sender.
func (c *CountingSender) Send(context.Context, notify.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.n++

	return nil
}

// Sent returns the number of messages that the sender got.
func (c *CountingSender) Sent() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n
}

// ErrCounterBroken is the error of a BreakableCounter after Break.
var ErrCounterBroken = errors.New("the shared count is broken")

// BreakableCounter is an authgate.FailureCounter that passes each call to the
// counter that it wraps, until Break makes each call fail. It stands for a
// handle that has lost its connection to the database only for the count.
type BreakableCounter struct {
	inner  authgate.FailureCounter
	broken atomic.Bool
}

// NewBreakableCounter returns a counter that passes its calls to inner.
func NewBreakableCounter(inner authgate.FailureCounter) *BreakableCounter {
	return &BreakableCounter{inner: inner, broken: atomic.Bool{}}
}

// Break makes each later call fail with ErrCounterBroken.
func (b *BreakableCounter) Break() { b.broken.Store(true) }

// AddFailure implements authgate.FailureCounter.
func (b *BreakableCounter) AddFailure(
	ctx context.Context, source string, length time.Duration,
) (authgate.FailureCount, error) {
	if b.broken.Load() {
		return authgate.FailureCount{At: time.Time{}, End: time.Time{}, Count: 0}, ErrCounterBroken
	}

	count, err := b.inner.AddFailure(ctx, source, length)
	if err != nil {
		return authgate.FailureCount{At: time.Time{}, End: time.Time{}, Count: 0}, fmt.Errorf("add failure: %w", err)
	}

	return count, nil
}

// PruneFailures implements authgate.FailureCounter.
func (b *BreakableCounter) PruneFailures(ctx context.Context, age time.Duration) (int64, error) {
	if b.broken.Load() {
		return 0, ErrCounterBroken
	}

	n, err := b.inner.PruneFailures(ctx, age)
	if err != nil {
		return 0, fmt.Errorf("prune failures: %w", err)
	}

	return n, nil
}

// PruneFailuresAt implements authgate.FailureCounter.
func (b *BreakableCounter) PruneFailuresAt(ctx context.Context, now time.Time, age time.Duration) (int64, error) {
	if b.broken.Load() {
		return 0, ErrCounterBroken
	}

	n, err := b.inner.PruneFailuresAt(ctx, now, age)
	if err != nil {
		return 0, fmt.Errorf("prune failures at: %w", err)
	}

	return n, nil
}

// SinkBackend is a store handle that can serve a sink service.
type SinkBackend interface {
	namespace.Store
	secret.KeyStore
	NotificationSinks() notify.SinkStore
}

// SinkServiceInput describes one sink service of NewSharedSinkService.
type SinkServiceInput struct {
	// Store is the handle that the service uses.
	Store SinkBackend
	// Counter holds the shared count of the limits.
	Counter authgate.FailureCounter
	// Sender sends the test messages.
	Sender notify.Sender
	// Namespace is the namespace of the sinks.
	Namespace namespace.Name
	// MakeNamespace makes Namespace, which must be new in the database. Set
	// it for one service of a database only.
	MakeNamespace bool
}

// NewSharedSinkService returns a sink service as in describes it.
func NewSharedSinkService(t *testing.T, in SinkServiceInput) *sinks.Service {
	t.Helper()

	store := in.Store

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if in.MakeNamespace {
		if _, createErr := namespaces.Create(t.Context(), in.Namespace); createErr != nil {
			t.Fatalf("create namespace: %v", createErr)
		}
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: store, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	svc, err := sinks.NewService(sinks.Config{
		Store: store.NotificationSinks(), Sealer: notify.SinkSealer{Namespaces: secrets, Installation: nil},
		Namespaces: namespaces, Senders: []notify.Sender{in.Sender}, Ports: egress.Ports{}, Counter: in.Counter,
		Now: nil,
	})
	if err != nil {
		t.Fatalf("sink service: %v", err)
	}

	return svc
}

// SharedLimits is what one case of RunSharedLimits runs on: two handles on
// one database, as two server instances have them. An adapter that allows one
// handle for each database (SQLite) gives the same handle twice: the limits
// then hold across two services over one store, which is the most that the
// adapter allows.
type SharedLimits struct {
	// Stores are the two handles, for the limits of the store.
	Stores [2]WorkspaceStore
	// Services are two sink services, one on each handle. Both send with the
	// sender that the factory got, and both find the sinks in Namespace.
	Services [2]*sinks.Service
	// Namespace is a namespace that exists and is writable on this
	// installation, for the sink services.
	Namespace namespace.Name
	// SecondCounter is the counter of the second service. The case of a
	// counter that fails calls its Break.
	SecondCounter *BreakableCounter
}

// SharedLimitsFactory returns a fresh database with two handles for one case.
// Both services send with sender.
type SharedLimitsFactory func(t *testing.T, sender *CountingSender) SharedLimits

// RunSharedLimits executes the cases that show a limit holds across two
// handles on one database: the cap of pending state versions of a workspace,
// and the limits of the test route of a notification destination.
//
// A limit that counts in the memory of the process passes none of the cases
// that send through both handles.
func RunSharedLimits(t *testing.T, newShared SharedLimitsFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, sh SharedLimits, sender *CountingSender)
	}{
		{"pending state versions: declares through both handles never pass the cap", testSharedPendingCap},
		{"test of one destination: the second handle sees the test of the first", testSharedSinkInterval},
		{"tests of one namespace: both handles count to one burst", testSharedNamespaceBurst},
		{"tests fail closed when the second handle cannot count", testSharedFailClosed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sender := NewCountingSender()
			tc.run(t, newShared(t, sender), sender)
		})
	}
}

// testSharedPendingCap declares from both handles at the same time with a low
// cap. The cap counts in the store, so exactly the cap succeeds.
func testSharedPendingCap(t *testing.T, sh SharedLimits, _ *CountingSender) {
	t.Helper()

	first := sh.Stores[0]
	ns := newWorkspaceNamespace(t, first, "acme")
	w := createTestWorkspace(t, first, ns, "network")
	locked := lockTestWorkspace(t, first, ns, w, "alice")

	const declares = 8

	const limit = 3

	versions := make([]workspace.StateVersion, declares)
	for i := range versions {
		versions[i] = newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v1", int64(i+1)))
	}

	errs := make([]error, declares)

	var wg sync.WaitGroup

	for i := range versions {
		wg.Go(func() {
			errs[i] = sh.Stores[i%len(sh.Stores)].CreateStateVersion(t.Context(), versions[i], ns.Fence(), limit)
		})
	}

	wg.Wait()

	succeeded := 0

	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, workspace.ErrTooManyPending):
		default:
			t.Fatalf("declare through one of two handles: err = %v, want nil or ErrTooManyPending", err)
		}
	}

	if succeeded != limit {
		t.Fatalf("successful declares through two handles = %d, want %d: the cap", succeeded, limit)
	}

	assertPendingCount(t, sh.Stores[1], w.ID, limit)
}

// testSharedSinkInterval sends a test through each handle for one
// destination. The second must be refused: one destination takes one test in
// each TestSinkInterval, whatever instance gets the request.
func testSharedSinkInterval(t *testing.T, sh SharedLimits, sender *CountingSender) {
	t.Helper()

	waitForWindow(t, sinks.TestSinkInterval, sinks.TestSinkInterval/2)

	id := sharedSink(t, sh, "Hook")

	if _, err := sh.Services[0].SendTest(t.Context(), sharedCaller(), sh.Namespace, id); err != nil {
		t.Fatalf("test through the first handle: %v", err)
	}

	_, err := sh.Services[1].SendTest(t.Context(), sharedCaller(), sh.Namespace, id)

	var limited *sinks.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("test through the second handle = %v, want *sinks.RateLimitedError", err)
	}

	if limited.RetryAfter <= 0 || limited.RetryAfter > sinks.TestSinkInterval {
		t.Fatalf("RetryAfter = %s, want more than 0 and at most %s", limited.RetryAfter, sinks.TestSinkInterval)
	}

	if got := sender.Sent(); got != 1 {
		t.Fatalf("messages sent = %d, want 1: the refused test sends nothing", got)
	}
}

// testSharedNamespaceBurst sends TestNamespaceBurst tests of different
// destinations, through both handles in turn. The next test through either
// handle must be refused.
func testSharedNamespaceBurst(t *testing.T, sh SharedLimits, sender *CountingSender) {
	t.Helper()

	// The burst needs one window of the namespace for all its tests.
	waitForWindow(t, sinks.TestNamespaceWindow, sinks.TestNamespaceWindow*2/3)

	ids := make([]string, 0, sinks.TestNamespaceBurst+2)
	for i := range sinks.TestNamespaceBurst + 2 {
		ids = append(ids, sharedSink(t, sh, "Hook "+string(rune('a'+i))))
	}

	for i, id := range ids[:sinks.TestNamespaceBurst] {
		if _, err := sh.Services[i%2].SendTest(t.Context(), sharedCaller(), sh.Namespace, id); err != nil {
			t.Fatalf("test %d through handle %d: %v", i, i%2, err)
		}
	}

	for i, id := range ids[sinks.TestNamespaceBurst:] {
		_, err := sh.Services[i%2].SendTest(t.Context(), sharedCaller(), sh.Namespace, id)
		if !errors.Is(err, sinks.ErrRateLimited) {
			t.Fatalf("test over the burst through handle %d = %v, want ErrRateLimited", i%2, err)
		}
	}

	if got := sender.Sent(); got != sinks.TestNamespaceBurst {
		t.Fatalf("messages sent = %d, want %d: the burst", got, sinks.TestNamespaceBurst)
	}
}

// testSharedFailClosed breaks the second handle. A test through it must fail
// with sinks.ErrLimitUnavailable and send nothing, because the limit cannot
// count.
func testSharedFailClosed(t *testing.T, sh SharedLimits, sender *CountingSender) {
	t.Helper()

	id := sharedSink(t, sh, "Hook")

	sh.SecondCounter.Break()

	_, err := sh.Services[1].SendTest(t.Context(), sharedCaller(), sh.Namespace, id)
	if !errors.Is(err, sinks.ErrLimitUnavailable) {
		t.Fatalf("test with a broken counter = %v, want ErrLimitUnavailable", err)
	}

	if got := sender.Sent(); got != 0 {
		t.Fatalf("messages sent = %d, want 0: the limits fail closed", got)
	}
}

// sharedSink makes a webhook destination through the first service and
// returns its ID.
func sharedSink(t *testing.T, sh SharedLimits, name string) string {
	t.Helper()

	made, err := sh.Services[0].Create(t.Context(), sharedCaller(), sh.Namespace, sinks.New{
		Kind: notify.SinkWebhook, Platform: "", Name: name, Target: "https://hooks.example.com/ops", URL: "",
		AccessToken: "",
	})
	if err != nil {
		t.Fatalf("create destination %q: %v", name, err)
	}

	return made.Sink.ID
}

// sharedCaller returns a person who calls the sink use cases.
func sharedCaller() *sinks.Caller {
	return &sinks.Caller{
		Principal: identity.Principal{
			Subject: "alice", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
			Credential: "", JobID: "",
			JobUnit: identity.Object{},
		},
		By: audit.Entry{
			At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
			Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
			Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "req-1",
			ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "",
			JobID: "",
		},
	}
}

// waitForWindow sleeps until the wall clock is in the first maxOffset of a
// window of length, so that the calls that follow fall in one fixed window.
// The databases of the test run on the host of the test, so their clock is
// this clock.
func waitForWindow(t *testing.T, length, maxOffset time.Duration) {
	t.Helper()

	now := time.Now()

	offset := time.Duration(now.UnixNano()) % length
	if offset <= maxOffset {
		return
	}

	select {
	case <-time.After(length - offset):
	case <-t.Context().Done():
		t.Fatalf("wait for the next window: %v", t.Context().Err())
	}
}
