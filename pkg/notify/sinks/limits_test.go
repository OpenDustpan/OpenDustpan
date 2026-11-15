package sinks_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// errAuditAppend stands for a store that cannot append the audit entry.
var errAuditAppend = errors.New("audit append failed")

// clock is a clock that a test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{mu: sync.Mutex{}, now: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// serviceWith returns a fixture whose service has the ports and the clock, over
// the store and the sender of f.
func serviceWith(t *testing.T, f fixture, ports egress.Ports, ns fakeNamespaces, c *clock) fixture {
	t.Helper()

	cfg := config(f.store, ns, f.hook)
	cfg.Ports = ports
	cfg.Now = c.Now
	cfg.Counter = authgatetest.NewMemory(c.Now)

	svc, err := sinks.NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc

	return f
}

func mustPorts(t *testing.T, extra ...int) egress.Ports {
	t.Helper()

	p, err := egress.NewPorts(extra...)
	if err != nil {
		t.Fatalf("NewPorts: %v", err)
	}

	return p
}

// TestPortIsCheckedWhenASinkIsSaved sends hostile ports to Create and Replace.
// A refused port stores nothing.
func TestPortIsCheckedWhenASinkIsSaved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     sinks.New
		ports  egress.Ports
		stored bool
	}{
		{"webhook on 443", hook("https://hooks.example.com/ops"), egress.Ports{}, true},
		{"webhook on explicit 443", hook("https://hooks.example.com:443/ops"), egress.Ports{}, true},
		{"webhook on ssh", hook("https://hooks.example.com:22/ops"), egress.Ports{}, false},
		{"webhook on redis", hook("https://hooks.example.com:6379/ops"), egress.Ports{}, false},
		{"webhook on 8443 by default", hook("https://hooks.example.com:8443/ops"), egress.Ports{}, false},
		{"webhook on 8443 by the operator", hook("https://hooks.example.com:8443/ops"), mustPorts(t, 8443), true},
		{"webhook on ssh when 8443 is allowed", hook("https://hooks.example.com:22/ops"), mustPorts(t, 8443), false},
		{"chat on 443", chat("https://hooks.slack.com/services/T0/B0/x"), egress.Ports{}, true},
		{"chat on 8443 by default", chat("https://chat.example.com:8443/hooks/x"), egress.Ports{}, false},
		{"chat on 25", chat("https://chat.example.com:25/hooks/x"), egress.Ports{}, false},
		{"chat on 8443 by the operator", chat("https://chat.example.com:8443/hooks/x"), mustPorts(t, 8443), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := serviceWith(t, newFixture(t, nil), tt.ports, fakeNamespaces{notHome: false}, newClock())

			_, err := f.svc.Create(t.Context(), f.caller, shop, tt.in)
			if tt.stored {
				if err != nil {
					t.Fatalf("Create = %v, want a sink", err)
				}

				return
			}

			if !errors.Is(err, notify.ErrInvalid) || !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("Create = %v, want notify.ErrInvalid and egress.ErrRefused", err)
			}

			if f.store.puts != 0 {
				t.Fatalf("the store got %d writes, want none", f.store.puts)
			}
		})
	}
}

func hook(target string) sinks.New {
	return sinks.New{Kind: notify.SinkWebhook, Platform: "", Name: "Hook", Target: target, URL: "", AccessToken: ""}
}

func chat(url string) sinks.New {
	return sinks.New{Kind: notify.SinkChat, Platform: "mattermost", Name: "Chat", Target: "", URL: url, AccessToken: ""}
}

func TestPortIsCheckedWhenASinkIsReplaced(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	created := makeHook(t, f)

	for _, target := range []string{"https://hooks.example.com:22/ops", "https://hooks.example.com:8443/ops"} {
		to := target

		_, err := f.svc.Replace(t.Context(), f.caller, shop, created.Sink.ID, sinks.Change{
			Version: created.Sink.Version, Name: "Hook", Target: &to, URL: nil,
			AccessToken: nil,
		})
		if !errors.Is(err, notify.ErrInvalid) || !errors.Is(err, egress.ErrRefused) {
			t.Fatalf("Replace to %s = %v, want notify.ErrInvalid and egress.ErrRefused", target, err)
		}
	}

	if f.store.puts != 1 {
		t.Fatalf("the store got %d writes, want the create only", f.store.puts)
	}

	// A chat sink takes a new URL on a closed port as little as a webhook.
	c := makeChat(t, f)
	url := "https://hooks.slack.com:6379/services/x"

	_, err := f.svc.Replace(t.Context(), f.caller, shop, c.ID, sinks.Change{
		Version: c.Version, Name: "Chat", Target: nil, URL: &url,
		AccessToken: nil,
	})
	if !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Replace chat URL on 6379 = %v, want egress.ErrRefused", err)
	}
}

// TestPortIsCheckedBeforeATestSends makes sinks that an earlier setting
// allowed, and tests them with the default ports: nothing is sent.
func TestPortIsCheckedBeforeATestSends(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	wide := serviceWith(t, f, mustPorts(t, 8443), fakeNamespaces{notHome: false}, newClock())

	made, err := wide.svc.Create(t.Context(), wide.caller, shop, hook("https://hooks.example.com:8443/ops"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	narrow := serviceWith(t, f, egress.Ports{}, fakeNamespaces{notHome: false}, newClock())

	res, err := runTestWith(t.Context(), narrow.svc, narrow.caller, shop, made.Sink.ID)
	if err != nil || res.Delivered || res.Class != notify.ClassRefused || res.Message != sinks.TextRefused {
		t.Fatalf("Test = %+v, %v, want the class refused", res, err)
	}

	if len(f.hook.got) != 0 {
		t.Fatalf("sender got %d messages, want none", len(f.hook.got))
	}

	// The same sink passes where the operator allows the port.
	res, err = runTestWith(t.Context(), wide.svc, wide.caller, shop, made.Sink.ID)
	if err != nil || !res.Delivered {
		t.Fatalf("Test with the port allowed = %+v, %v, want delivered", res, err)
	}
}

// TestChatURLIsCheckedBeforeATestSends gives a chat sink a stored URL that the
// rules refuse while its host equals its target. The host compare alone would
// let it pass.
func TestChatURLIsCheckedBeforeATestSends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		url    string
	}{
		{"http scheme", "hooks.slack.com", "http://hooks.slack.com/services/x"},
		{"private address", "10.0.0.1", "https://10.0.0.1/services/x"},
		{"metadata address", "169.254.169.254", "https://169.254.169.254/latest"},
		{"user information", "hooks.slack.com", fakecred.URL("https", "u", "p", "hooks.slack.com", "/services/x")},
		{"closed port", "hooks.slack.com:22", "https://hooks.slack.com:22/services/x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, chatSender := chatFixture(t)
			c := makeChat(t, f)

			s := f.store.sinks[c.ID]
			s.Target = tt.target
			f.store.sinks[s.ID] = s

			sealed, err := notifytest.Sealer().Seal(t.Context(), s, []notify.SinkSecret{
				{Ref: s.SecretRefs[0], Value: []byte(tt.url)},
			})
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}

			f.store.secrets[s.ID] = sealed

			res, err := runTest(t.Context(), f, shop, c.ID)
			if err != nil || res.Class != notify.ClassRefused {
				t.Fatalf("Test = %+v, %v, want the class refused", res, err)
			}

			if len(chatSender.got) != 0 {
				t.Fatalf("sender got %d messages, want none", len(chatSender.got))
			}
		})
	}
}

// TestTestIsLimitedForEachSink proves that a second test of one sink inside the
// interval sends nothing and writes nothing, and that the wait is in the error.
func TestTestIsLimitedForEachSink(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := serviceWith(t, newFixture(t, nil), egress.Ports{}, fakeNamespaces{notHome: false}, clk)
	made := makeHook(t, f)

	if _, err := runTest(t.Context(), f, shop, made.Sink.ID); err != nil {
		t.Fatalf("first Test: %v", err)
	}

	sent, entries := len(f.hook.got), len(f.store.entries)

	for _, after := range []time.Duration{0, 3 * time.Second, sinks.TestSinkInterval - 3*time.Second - time.Nanosecond} {
		clk.advance(after)

		_, err := runTest(t.Context(), f, shop, made.Sink.ID)

		var limited *sinks.RateLimitedError
		if !errors.Is(err, sinks.ErrRateLimited) || !errors.As(err, &limited) {
			t.Fatalf("Test after %s = %v, want a *sinks.RateLimitedError", after, err)
		}

		if limited.RetryAfter <= 0 || limited.RetryAfter > sinks.TestSinkInterval {
			t.Fatalf("RetryAfter = %s, want 1ns to %s", limited.RetryAfter, sinks.TestSinkInterval)
		}

		if len(f.hook.got) != sent || len(f.store.entries) != entries {
			t.Fatalf("a limited test sent %d messages and wrote %d entries, want none",
				len(f.hook.got)-sent, len(f.store.entries)-entries)
		}
	}

	clk.advance(time.Nanosecond)

	if res, err := runTest(t.Context(), f, shop, made.Sink.ID); err != nil || !res.Delivered {
		t.Fatalf("Test after the interval = %+v, %v, want delivered", res, err)
	}
}

// TestTestIsLimitedForEachNamespace proves that a namespace takes
// TestNamespaceBurst tests in a window, however many sinks it has.
func TestTestIsLimitedForEachNamespace(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := serviceWith(t, newFixture(t, nil), egress.Ports{}, fakeNamespaces{notHome: false}, clk)

	ids := make([]string, 0, sinks.TestNamespaceBurst+1)

	for i := range sinks.TestNamespaceBurst + 1 {
		got, err := f.svc.Create(t.Context(), f.caller, shop, sinks.New{
			Kind: notify.SinkWebhook, Platform: "", Name: fmt.Sprintf("Hook %d", i),
			Target: "https://hooks.example.com/ops", URL: "",
			AccessToken: "",
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}

		ids = append(ids, got.Sink.ID)
	}

	for _, id := range ids[:sinks.TestNamespaceBurst] {
		if res, err := runTest(t.Context(), f, shop, id); err != nil || !res.Delivered {
			t.Fatalf("Test = %+v, %v, want delivered", res, err)
		}

		clk.advance(time.Second)
	}

	sent, entries := len(f.hook.got), len(f.store.entries)

	_, err := runTest(t.Context(), f, shop, ids[sinks.TestNamespaceBurst])

	var limited *sinks.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("Test over the burst = %v, want a *sinks.RateLimitedError", err)
	}

	// The oldest test was 10 seconds ago, so the window frees a slot in 50 s.
	want := sinks.TestNamespaceWindow - time.Duration(sinks.TestNamespaceBurst)*time.Second
	if limited.RetryAfter != want {
		t.Fatalf("RetryAfter = %s, want %s", limited.RetryAfter, want)
	}

	if len(f.hook.got) != sent || len(f.store.entries) != entries {
		t.Fatal("a limited test sent a message or wrote an entry")
	}

	clk.advance(limited.RetryAfter)

	res, err := runTest(t.Context(), f, shop, ids[sinks.TestNamespaceBurst])
	if err != nil || !res.Delivered {
		t.Fatalf("Test after the wait = %+v, %v, want delivered", res, err)
	}
}

// TestRefusedTestDoesNotUseTheLimit proves that a test that fails before the
// send does not take a slot: a fenced namespace must not make the limits
// refuse the first test after the fence moves.
func TestRefusedTestDoesNotUseTheLimit(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newFixture(t, nil)
	made := makeHook(t, f)

	away := serviceWith(t, f, egress.Ports{}, fakeNamespaces{notHome: true}, clk)

	for range 3 {
		_, err := runTestWith(t.Context(), away.svc, away.caller, shop, made.Sink.ID)
		if !errors.Is(err, namespace.ErrNotHome) {
			t.Fatalf("Test = %v, want namespace.ErrNotHome", err)
		}
	}

	home := serviceWith(t, f, egress.Ports{}, fakeNamespaces{notHome: false}, clk)

	if res, err := runTestWith(t.Context(), home.svc, home.caller, shop, made.Sink.ID); err != nil || !res.Delivered {
		t.Fatalf("Test = %+v, %v, want delivered", res, err)
	}
}

// TestNothingIsSentWhenAStepBeforeTheSendFails proves the order of the steps:
// a namespace of another installation, a stale fence and a failed audit append each
// end the test before the sender gets a message.
func TestNothingIsSentWhenAStepBeforeTheSendFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		notHome bool
		testErr error
		want    error
	}{
		{"namespace of another installation", true, nil, namespace.ErrNotHome},
		{"stale fence", false, namespace.ErrFenced, namespace.ErrFenced},
		{"failed audit append", false, errAuditAppend, errAuditAppend},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			made := makeHook(t, f)

			f = serviceWith(t, f, egress.Ports{}, fakeNamespaces{notHome: tt.notHome}, newClock())
			f.store.testErr = tt.testErr
			before := len(f.store.entries)

			res, err := runTest(t.Context(), f, shop, made.Sink.ID)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Test = %+v, %v, want %v", res, err, tt.want)
			}

			if len(f.hook.got) != 0 || len(f.store.entries) != before {
				t.Fatalf("sender got %d messages and %d entries were written, want none", len(f.hook.got),
					len(f.store.entries)-before)
			}
		})
	}
}

// TestTestWritesTheStartBeforeTheSend proves that the entry that says the test
// started exists when the sender runs, and that the result follows it.
func TestTestWritesTheStartBeforeTheSend(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	made := makeHook(t, f)
	seen := &entryCounter{store: f.store, during: 0}

	cfg := config(f.store, fakeNamespaces{notHome: false}, seen)
	svc, err := sinks.NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err = runTestWith(t.Context(), svc, f.caller, shop, made.Sink.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}

	if seen.during != 1 {
		t.Fatalf("entries when the sender ran = %d, want the one entry of the start", seen.during)
	}

	last := f.store.entries[len(f.store.entries)-2:]
	if last[0].Reason != "started" || last[1].Reason != "class: delivered" ||
		last[0].Action != "sink:test" || last[1].Action != "sink:test" {
		t.Fatalf("entries = %q and %q, want the start and then the result", last[0].Reason, last[1].Reason)
	}

	for _, e := range last {
		if strings.Contains(e.Reason, "http") || e.Validate() != nil {
			t.Fatalf("entry %+v holds an address or is not valid", e)
		}
	}
}

// entryCounter is a sender that counts the test entries of the store when it
// runs.
type entryCounter struct {
	store  *fakeStore
	during int
}

func (*entryCounter) Kind() notify.SinkKind { return notify.SinkWebhook }

func (e *entryCounter) Send(_ context.Context, _ notify.Message) error {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()

	for i := range e.store.entries {
		if e.store.entries[i].Action == "sink:test" {
			e.during++
		}
	}

	return nil
}

// TestRefusedAddressCountsAgainstTheLimits proves that the limits come before
// the opening of the secrets and the check of the address: a test of a sink
// whose address the rules refuse takes a slot, and a limited test opens no
// secret and writes no entry, so a caller cannot write audit entries without
// bound.
func TestRefusedAddressCountsAgainstTheLimits(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	wide := serviceWith(t, f, mustPorts(t, 8443), fakeNamespaces{notHome: false}, newClock())

	made, err := wide.svc.Create(t.Context(), wide.caller, shop, hook("https://hooks.example.com:8443/ops"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk := newClock()
	narrow := serviceWith(t, f, egress.Ports{}, fakeNamespaces{notHome: false}, clk)

	res, err := runTestWith(t.Context(), narrow.svc, narrow.caller, shop, made.Sink.ID)
	if err != nil || res.Class != notify.ClassRefused {
		t.Fatalf("first Test = %+v, %v, want the class refused", res, err)
	}

	reads, entries := f.store.secretReads, len(f.store.entries)

	_, err = runTestWith(t.Context(), narrow.svc, narrow.caller, shop, made.Sink.ID)

	if !errors.Is(err, sinks.ErrRateLimited) {
		t.Fatalf("second Test of a refused address = %v, want sinks.ErrRateLimited", err)
	}

	if f.store.secretReads != reads || len(f.store.entries) != entries {
		t.Fatalf("a limited test read %d secrets and wrote %d entries, want none",
			f.store.secretReads-reads, len(f.store.entries)-entries)
	}
}

// windowEnd returns the end of the window of length that holds now, on the
// clock of authgatetest.Memory.
func windowEnd(now time.Time, length time.Duration) time.Time {
	at, ms := now.UnixMilli(), length.Milliseconds()

	return time.UnixMilli((at/ms + 1) * ms)
}

// TestTakeRefusesHostileIdentifiers sends hostile names and sink IDs to the
// limiter. Each case must fail before a counter call, so the counts of the
// valid keys stay the same.
func TestTakeRefusesHostileIdentifiers(t *testing.T) {
	t.Parallel()

	const (
		validNS = namespace.Name("shop")
		validID = "snk-aaaaaaaaaaaaaaaaaaaa"
	)

	tests := []struct {
		name string
		ns   namespace.Name
		id   string
	}{
		{"empty namespace", "", validID},
		{"empty sink ID", validNS, ""},
		{"empty namespace and sink ID", "", ""},
		{"slash in namespace", "a/b", validID},
		{"slash in sink ID", validNS, "snk-a/b"},
		{"pipe in namespace", "a|b", validID},
		{"pipe in sink ID", validNS, "snk-a|b"},
		{"counter key in namespace", "sinktest|namespace|x", validID},
		{"counter key in sink ID", validNS, "sinktest|namespace|x"},
		{"parent in namespace", "..", validID},
		{"parent in sink ID", validNS, "snk-.."},
		{"path up in sink ID", validNS, "snk-a/../b"},
		{"carriage return in namespace", "a\rb", validID},
		{"carriage return in sink ID", validNS, "snk-a\rb"},
		{"line feed in namespace", "a\nb", validID},
		{"line feed in sink ID", validNS, "snk-a\nb"},
		{
			"long namespace", namespace.Name(strings.Repeat("a", namespace.MaxNameLength+1)),
			validID,
		},
		{"long sink ID", validNS, notify.SinkIDPrefix + strings.Repeat("a", 100)},
		{"no sink prefix", validNS, "b"},
		{"uppercase sink ID", validNS, "snk-AAAAAAAAAAAAAAAAAAAA"},
		{"non-ASCII in namespace", "shöp", validID},
		{"non-ASCII in sink ID", validNS, "snk-äaaa"},
		{"NUL in namespace", "a\x00b", validID},
		{"NUL in sink ID", validNS, "snk-a\x00b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkHostileTake(t, validNS, validID, tt.ns, tt.id)
		})
	}
}

// checkHostileTake proves that take refuses the hostile namespace and sink ID
// before a counter call, and that the counts of the valid keys stay the same.
func checkHostileTake(
	t *testing.T, validNS namespace.Name, validID string, hostileNS namespace.Name, hostileID string,
) {
	t.Helper()

	clk := newClock()
	counter := authgatetest.NewMemory(clk.Now)
	now := clk.Now()

	if _, err := sinks.TakeForTest(t.Context(), counter, validNS, validID, now); err != nil {
		t.Fatalf("TakeForTest of a valid sink = %v, want nil", err)
	}

	sinkKey := sinks.SinkKeyForTest(validNS, validID)
	namespaceKey := sinks.NamespaceKeyForTest(validNS)
	sinkEnd := windowEnd(now, sinks.TestSinkInterval)
	namespaceEnd := windowEnd(now, sinks.TestNamespaceWindow)
	adds := counter.Adds()
	sinkCount := counter.Count(sinkKey, sinkEnd)
	namespaceCount := counter.Count(namespaceKey, namespaceEnd)

	if sinkCount != 1 || namespaceCount != 1 {
		t.Fatalf("counts of the valid keys = %d and %d, want 1 and 1", sinkCount, namespaceCount)
	}

	_, err := sinks.TakeForTest(t.Context(), counter, hostileNS, hostileID, now)
	if !errors.Is(err, notify.ErrInvalid) || errors.Is(err, sinks.ErrLimitUnavailable) {
		t.Fatalf(
			"TakeForTest(%q, %q) = %v, want notify.ErrInvalid and not ErrLimitUnavailable",
			hostileNS,
			hostileID,
			err,
		)
	}

	if got := counter.Adds(); got != adds {
		t.Fatalf("the refused take wrote %d counts, want none", got-adds)
	}

	if got := counter.Count(sinkKey, sinkEnd); got != sinkCount {
		t.Fatalf("the count of the sink key changed from %d to %d", sinkCount, got)
	}

	if got := counter.Count(namespaceKey, namespaceEnd); got != namespaceCount {
		t.Fatalf("the count of the namespace key changed from %d to %d", namespaceCount, got)
	}
}

// TestHostileIdentifiersCannotShareACounterKey proves that the pair that
// would make one counter key, namespace "a" with sink ID "b/c" and namespace
// "a/b" with sink ID "c", is refused before it writes: the limits never count
// the two pairs as one.
func TestHostileIdentifiersCannotShareACounterKey(t *testing.T) {
	t.Parallel()

	clk := newClock()
	counter := authgatetest.NewMemory(clk.Now)
	now := clk.Now()

	first := sinks.SinkKeyForTest("a", "b/c")
	second := sinks.SinkKeyForTest("a/b", "c")
	if first != second {
		t.Fatalf("the counter keys %q and %q differ, so the case does not collide", first, second)
	}

	pairs := []struct {
		ns namespace.Name
		id string
	}{{"a", "b/c"}, {"a/b", "c"}}

	for _, pair := range pairs {
		_, err := sinks.TakeForTest(t.Context(), counter, pair.ns, pair.id, now)
		if !errors.Is(err, notify.ErrInvalid) || errors.Is(err, sinks.ErrLimitUnavailable) {
			t.Fatalf(
				"TakeForTest(%q, %q) = %v, want notify.ErrInvalid and not ErrLimitUnavailable",
				pair.ns,
				pair.id,
				err,
			)
		}
	}

	if counter.Adds() != 0 || counter.Windows() != 0 {
		t.Fatalf("the refused takes wrote %d counts in %d windows, want none", counter.Adds(), counter.Windows())
	}
}
