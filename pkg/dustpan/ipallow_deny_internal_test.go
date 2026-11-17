package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/setting"
)

// recordingServerAudit keeps the installation audit entries of one test.
type recordingServerAudit struct {
	entries []audit.Entry
}

// AppendServer implements audit.ServerStore.
func (s *recordingServerAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	s.entries = append(s.entries, e)

	return e, nil
}

// TestDenyAnyListDenies is the hostile test of the checker: an address inside
// the namespace list but outside the installation list is refused with the
// installation scope, and the denial entry holds the scope and the address.
// It fails if the checker ignores the installation list.
func TestDenyAnyListDenies(t *testing.T) {
	t.Parallel()

	installation, err := ipallow.ParseList([]string{"198.51.100.0/24"})
	if err != nil {
		t.Fatalf("parse the installation list: %v", err)
	}

	namespace, err := ipallow.ParseList([]string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("parse the namespace list: %v", err)
	}

	store := &recordingServerAudit{entries: nil}
	auditor := allowlistAudit{
		store:   store,
		limiter: nil,
		now:     func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) },
		logger:  slog.New(slog.DiscardHandler),
	}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", http.NoBody,
	)
	record := func(ctx context.Context, d allowDenial) { auditor.record(ctx, req, d) }

	checker := newAllowChecker(true, installation, &namespace, record)
	addr := netip.MustParseAddr("203.0.113.7")

	decision := checker.check(t.Context(), addr)
	if !decision.Denied || decision.Scope != allowScopeInstallation {
		t.Fatalf("decision = %+v, want a denial with scope installation", decision)
	}

	if len(store.entries) != 1 {
		t.Fatalf("denial entries = %d, want 1", len(store.entries))
	}

	entry := store.entries[0]
	if entry.Object != string(allowScopeInstallation) || entry.SourceIP != addr.String() ||
		entry.Outcome != audit.OutcomeDenied || entry.Kind != audit.KindSecurity {
		t.Fatalf("denial entry = %+v, want the installation scope, the address and a security denial", entry)
	}

	if err := entry.Validate(); err != nil {
		t.Fatalf("denial entry is not valid: %v", err)
	}
}

// TestAllowlistRequestSkipsProbes is the hostile test of the request filter:
// a health probe with a credential stays outside the list, so a stopped
// database cannot turn a readiness answer of 503 into a 403. Each other path
// that carries a credential stays inside the list. It fails if the filter
// stops treating the probe paths as exempt, or exempts a path near them.
func TestAllowlistRequestSkipsProbes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		target string
		want   bool
	}{
		{"/health/live", false},
		{"/health/ready", false},
		{"/health/ready/database", false},
		{"/health/ready/", true},
		{"/health/ready/a/b", true},
		{"/health/../api/dustpan/v1/namespaces", true},
		{"/health//ready", true},
		{"/health/ready/../x", true},
		{"/health/readyX", true},
		{"/health/ready/database/", true},
		{"/health/livez", true},
		{"/HEALTH/ready", true},
		{"/Health/Live", true},
		{"/health/READY", true},
		{"/health%2Fready", true},
		{"/health%2flive", true},
		{"/health/ready%2Fdatabase", true},
		{"/health/%72eady", true},
		{"/api/server/metrics", true},
		{"/api/dustpan/v1/namespaces", true},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.target, http.NoBody)
			r.Header.Set("Authorization", "Bearer token")

			if got := allowlistRequest(r); got != tc.want {
				t.Fatalf("allowlistRequest(%q) = %t, want %t", tc.target, got, tc.want)
			}
		})
	}
}

var errStoreDown = errors.New("database unavailable")

// countingAuthn counts the token checks. A check of a real token store
// updates the last-used time of the token, so a call here is that update.
type countingAuthn struct {
	calls atomic.Int32
}

// AuthenticateToken implements identity.Authenticator.
func (a *countingAuthn) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	a.calls.Add(1)

	var principal identity.Principal
	principal.Subject = "alice"

	return principal, nil
}

// failingSettingsStore is a setting.Store whose Load fails from call number
// failFrom on (1 is the first call) and returns values before that.
type failingSettingsStore struct {
	setting.Store

	values   map[string]string
	failFrom int32
	calls    atomic.Int32
}

// Load implements setting.Store.
func (s *failingSettingsStore) Load(context.Context, setting.Scope) (setting.Stored, error) {
	if s.calls.Add(1) >= s.failFrom {
		return setting.Stored{}, errStoreDown
	}

	return setting.Stored{Values: s.values, Revision: 1, Digest: ""}, nil
}

// recordingServer keeps the entries that the gate appends to the server scope.
type recordingServer struct {
	mu      sync.Mutex
	entries []audit.Entry
}

// AppendServer implements audit.ServerStore.
func (s *recordingServer) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, e)

	return e, nil
}

// newTestGate returns an allowlist request gate over store.
func newTestGate(t *testing.T, store setting.Store) allowlistRequestGate {
	t.Helper()

	registry, err := setting.NewRegistry(ipallow.SettingDeclarations())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	return allowlistRequestGate{
		settings: setting.NewService(store, registry, nil, nil, nil),
		audit:    nil, limiter: nil, now: nil, logger: slog.New(slog.DiscardHandler),
	}
}

// TestAllowlistGateFailsClosed is the hostile test of each fail-closed branch
// of the gate: a failed read of the enabled setting, a failed read of the
// list and a stored list that does not decode each deny an address that the
// stored list would admit. It fails when any one branch answers allow.
func TestAllowlistGateFailsClosed(t *testing.T) {
	t.Parallel()

	inside := netip.MustParseAddr("198.51.100.7")

	tests := []struct {
		name     string
		values   map[string]string
		failFrom int32
	}{
		{"enabled read fails", map[string]string{
			ipallow.SettingEnabled: "true", ipallow.SettingInstallation: "198.51.100.0/24",
		}, 1},
		{"list read fails", map[string]string{
			ipallow.SettingEnabled: "true", ipallow.SettingInstallation: "198.51.100.0/24",
		}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gate := newTestGate(
				t,
				&failingSettingsStore{Store: nil, values: tc.values, failFrom: tc.failFrom, calls: atomic.Int32{}},
			)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces", http.NoBody)
			r.Header.Set("Authorization", "Bearer token")

			checker := gate.checker(t.Context(), r)
			if checker == nil {
				t.Fatal("checker = nil, want a checker that denies")
			}

			if decision := checker.check(t.Context(), inside); !decision.Denied {
				t.Fatalf("decision = %+v, want a denial", decision)
			}
		})
	}

	// The settings layer ignores a stored value that its check refuses, so
	// the decode branch gets its own test on the value.
	for _, value := range []string{"not-a-cidr", "198.51.100.0/24,bad", "10.0.0.0/33"} {
		t.Run("stored list does not decode: "+value, func(t *testing.T) {
			t.Parallel()

			gate := newTestGate(
				t,
				&failingSettingsStore{Store: nil, values: nil, failFrom: 1 << 20, calls: atomic.Int32{}},
			)

			decision := gate.installationChecker(t.Context(), value, nil).check(t.Context(), inside)
			if !decision.Denied {
				t.Fatalf("decision = %+v, want a denial", decision)
			}
		})
	}

	t.Run("control: stored list admits", func(t *testing.T) {
		t.Parallel()

		gate := newTestGate(t, &failingSettingsStore{Store: nil, calls: atomic.Int32{}, values: map[string]string{
			ipallow.SettingEnabled: "true", ipallow.SettingInstallation: "198.51.100.0/24",
		}, failFrom: 1 << 20})
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces", http.NoBody)
		r.Header.Set("Authorization", "Bearer token")

		if decision := gate.checker(t.Context(), r).check(t.Context(), inside); decision.Denied {
			t.Fatalf("decision = %+v, want the stored list to admit", decision)
		}
	})
}

// TestAdmitProbeUsesNoToken is the hostile test of the admit step: a token
// from an address that the list refuses, sent to a probe path, is never
// checked, so its last-used time does not change. The same token on another
// path from the same address is refused before the token store. It fails if
// admit authenticates on a probe path.
func TestAdmitProbeUsesNoToken(t *testing.T) {
	t.Parallel()

	refused := netip.MustParseAddr("203.0.113.9")
	store := &failingSettingsStore{Store: nil, calls: atomic.Int32{}, values: map[string]string{
		ipallow.SettingEnabled: "true", ipallow.SettingInstallation: "198.51.100.0/24",
	}, failFrom: 1 << 20}

	for _, target := range []string{"/health/ready", "/health/live", "/health/ready/database"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			authn := &countingAuthn{calls: atomic.Int32{}}
			caller := requestCaller{authn: authn, allow: newTestGate(t, store)}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
			r.Header.Set("Authorization", "Bearer token")

			got := caller.admit(httptest.NewRecorder(), r, refused, slog.New(slog.DiscardHandler))
			if got.refused || got.signedIn || authn.calls.Load() != 0 {
				t.Fatalf("admit = %+v with %d token checks, want no refusal, no caller and no token check",
					got, authn.calls.Load())
			}
		})
	}

	t.Run("other path from the refused address", func(t *testing.T) {
		t.Parallel()

		authn := &countingAuthn{calls: atomic.Int32{}}
		caller := requestCaller{authn: authn, allow: newTestGate(t, store)}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces", http.NoBody)
		r.Header.Set("Authorization", "Bearer token")

		rec := httptest.NewRecorder()

		got := caller.admit(rec, r, refused, slog.New(slog.DiscardHandler))
		if !got.refused || rec.Code != http.StatusForbidden || authn.calls.Load() != 0 {
			t.Fatalf("admit = %+v, status %d, %d token checks, want a 403 and no token check",
				got, rec.Code, authn.calls.Load())
		}
	})

	t.Run("control: allowed address on another path", func(t *testing.T) {
		t.Parallel()

		authn := &countingAuthn{calls: atomic.Int32{}}
		caller := requestCaller{authn: authn, allow: newTestGate(t, store)}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces", http.NoBody)
		r.Header.Set("Authorization", "Bearer token")

		got := caller.admit(
			httptest.NewRecorder(),
			r,
			netip.MustParseAddr("198.51.100.5"),
			slog.New(slog.DiscardHandler),
		)
		if got.refused || !got.signedIn || authn.calls.Load() != 1 {
			t.Fatalf("admit = %+v with %d token checks, want a caller and one token check", got, authn.calls.Load())
		}
	})
}

// TestAllowlistRefusalWritesAuditEntry proves that a refusal writes its
// security entry: one refused address, one entry of the action
// installation:deny-ip-allowlist with the outcome denied. It fails when the
// record closure of the gate writes nothing.
func TestAllowlistRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	outside := netip.MustParseAddr("203.0.113.9")
	server := &recordingServer{mu: sync.Mutex{}, entries: nil}
	gate := newTestGate(
		t,
		&failingSettingsStore{Store: nil, calls: atomic.Int32{}, failFrom: 1 << 20, values: map[string]string{
			ipallow.SettingEnabled: "true", ipallow.SettingInstallation: "198.51.100.0/24",
		}},
	)
	gate.audit = server

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces", http.NoBody)
	r.Header.Set("Authorization", "Bearer token")

	checker := gate.checker(t.Context(), r)
	if checker == nil {
		t.Fatal("checker = nil, want a checker")
	}

	if decision := checker.check(t.Context(), outside); !decision.Denied {
		t.Fatalf("decision = %+v, want a denial", decision)
	}

	server.mu.Lock()
	defer server.mu.Unlock()

	if len(server.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(server.entries))
	}

	got := server.entries[0]
	if got.Action != "installation:deny-ip-allowlist" || got.Outcome != audit.OutcomeDenied ||
		got.Kind != audit.KindSecurity || got.SourceIP != outside.String() {
		t.Fatalf("entry = %+v, want installation:deny-ip-allowlist, denied, security, %s", got, outside)
	}
}

// TestAllowlistRefusalAuditIsLimited is the hostile test of the audit bound: a
// client outside the list that sends many requests in one window writes one
// entry, a second address writes its own entry, and the next window carries
// the count of the held back refusals. The 403 decision is the same for each
// request, also when the limiter drops the entry. It fails when the limiter
// is removed from the record path.
func TestAllowlistRefusalAuditIsLimited(t *testing.T) {
	t.Parallel()

	const refusals = 50

	outside := netip.MustParseAddr("203.0.113.9")
	other := netip.MustParseAddr("203.0.113.10")
	server := &recordingServer{mu: sync.Mutex{}, entries: nil}

	var (
		clockMu sync.Mutex
		clock   = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	)

	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()

		return clock
	}

	gate := newTestGate(
		t,
		&failingSettingsStore{Store: nil, calls: atomic.Int32{}, failFrom: 1 << 20, values: map[string]string{
			ipallow.SettingEnabled: "true", ipallow.SettingInstallation: "198.51.100.0/24",
		}},
	)
	gate.audit = server
	gate.now = now
	gate.limiter = denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 100, MaxFlush: 100, MaxKeysPerPrincipal: 10, Now: now,
	})

	refuse := func(address netip.Addr) {
		t.Helper()

		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces", http.NoBody)
		r.Header.Set("Authorization", "Bearer x")

		checker := gate.checker(t.Context(), r)
		if checker == nil {
			t.Fatal("checker = nil, want a checker")
		}

		if decision := checker.check(t.Context(), address); !decision.Denied {
			t.Fatalf("decision for %s = %+v, want a denial", address, decision)
		}
	}

	count := func() int {
		server.mu.Lock()
		defer server.mu.Unlock()

		return len(server.entries)
	}

	for range refusals {
		refuse(outside)
	}

	if got := count(); got != 1 {
		t.Fatalf("entries after %d refusals from one address = %d, want 1", refusals, got)
	}

	refuse(other)

	if got := count(); got != 2 {
		t.Fatalf("entries after a second address = %d, want 2", got)
	}

	clockMu.Lock()
	clock = clock.Add(2 * time.Minute)
	clockMu.Unlock()

	refuse(outside)

	server.mu.Lock()
	defer server.mu.Unlock()

	var carried bool

	for _, entry := range server.entries {
		if entry.SourceIP == outside.String() && strings.Contains(entry.Reason, "49 further refusals") {
			carried = true
		}
	}

	if !carried {
		t.Fatalf("no entry carries the count of 49 held back refusals: %+v", server.entries)
	}
}

var errAppendRefused = errors.New("append refused")

// failFirstServer is an audit.ServerStore that refuses its first append.
type failFirstServer struct {
	inner  recordingServer
	failed bool
}

// AppendServer implements audit.ServerStore.
func (s *failFirstServer) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	s.inner.mu.Lock()

	if !s.failed {
		s.failed = true
		s.inner.mu.Unlock()

		return audit.Entry{}, errAppendRefused
	}

	s.inner.mu.Unlock()

	return s.inner.AppendServer(ctx, e)
}

// limitedGate is a gate with a limiter, a recording audit store and a clock
// that a test moves.
type limitedGate struct {
	gate  allowlistRequestGate
	store *recordingServer
	clock *atomic.Int64
}

// newLimitedGate returns a gate for the installation list 198.51.100.0/24
// with a limiter of maxKeys keys and a window of one minute. store is the
// audit store of the gate; nil means a recording store.
func newLimitedGate(t *testing.T, maxKeys int, store audit.ServerStore) limitedGate {
	t.Helper()

	var clock atomic.Int64

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return start.Add(time.Duration(clock.Load())) }

	recorded := &recordingServer{mu: sync.Mutex{}, entries: nil}
	if store == nil {
		store = recorded
	}

	gate := newTestGate(
		t,
		&failingSettingsStore{Store: nil, calls: atomic.Int32{}, failFrom: 1 << 20, values: map[string]string{
			ipallow.SettingEnabled: "true", ipallow.SettingInstallation: "198.51.100.0/24",
		}},
	)
	gate.audit = store
	gate.now = now
	gate.limiter = denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: maxKeys, MaxFlush: 100, MaxKeysPerPrincipal: 10, Now: now,
	})

	return limitedGate{gate: gate, store: recorded, clock: &clock}
}

// advance moves the clock of the gate.
func (l limitedGate) advance(d time.Duration) { l.clock.Add(int64(d)) }

// refuse sends one request from address and requires the denial.
func (l limitedGate) refuse(t *testing.T, address netip.Addr) {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces", http.NoBody)
	r.Header.Set("Authorization", "Bearer x")

	checker := l.gate.checker(t.Context(), r)
	if checker == nil {
		t.Fatal("checker = nil, want a checker")
	}

	if decision := checker.check(t.Context(), address); !decision.Denied {
		t.Fatalf("decision for %s = %+v, want a denial", address, decision)
	}
}

// entries returns a copy of the recorded entries.
func (l limitedGate) entries() []audit.Entry {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()

	return append([]audit.Entry(nil), l.store.entries...)
}

// hasEntry reports whether an entry has the source address and a reason that
// holds text.
func hasEntry(entries []audit.Entry, address netip.Addr, text string) bool {
	for i := range entries {
		if entries[i].SourceIP == address.String() && strings.Contains(entries[i].Reason, text) {
			return true
		}
	}

	return false
}

// TestAllowlistLimiterIgnoresForeignCounts is the hostile test of the label of
// a flush entry: the table holds the count of another adapter's key, for
// example the stack:delete denials of a principal. A refusal from an address
// that sweeps the table must not write that count as an allowlist entry. It
// fails when the flush filter is removed.
func TestAllowlistLimiterIgnoresForeignCounts(t *testing.T) {
	t.Parallel()

	g := newLimitedGate(t, 100, nil)
	foreign := denylimit.Key{Principal: "alice", Action: "stack:delete", Kind: "stack", Scope: "team"}

	g.gate.limiter.Allow(foreign)
	g.gate.limiter.Allow(foreign)
	g.advance(2 * time.Minute)

	g.refuse(t, netip.MustParseAddr("203.0.113.9"))

	entries := g.entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d (%+v), want 1: the count of the foreign key must not be written", len(entries), entries)
	}
}

// TestAllowlistFlushWritesHeldBackCounts is the test of the flush loop of a
// refusal: the count of an address whose window ended goes out with the next
// refusal of another address. It fails when the loop is removed.
func TestAllowlistFlushWritesHeldBackCounts(t *testing.T) {
	t.Parallel()

	g := newLimitedGate(t, 100, nil)
	first, second := netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("203.0.113.10")

	for range 3 {
		g.refuse(t, first)
	}

	g.advance(2 * time.Minute)
	g.refuse(t, second)

	if !hasEntry(g.entries(), first, "2 further refusals") {
		t.Fatalf("no flush entry for %s with the count 2: %+v", first, g.entries())
	}
}

// TestAllowlistFailedAppendKeepsTheCount is the test of the release of a
// failed append: the first entry cannot be stored, so the next refusal in the
// same window writes an entry that carries the lost refusal. It fails when
// the release is removed.
func TestAllowlistFailedAppendKeepsTheCount(t *testing.T) {
	t.Parallel()

	failing := &failFirstServer{inner: recordingServer{mu: sync.Mutex{}, entries: nil}, failed: false}
	g := newLimitedGate(t, 100, failing)
	address := netip.MustParseAddr("203.0.113.9")

	g.refuse(t, address)
	g.refuse(t, address)

	failing.inner.mu.Lock()
	defer failing.inner.mu.Unlock()

	if len(failing.inner.entries) != 1 || !strings.Contains(failing.inner.entries[0].Reason, "1 further refusals") {
		t.Fatalf("entries = %+v, want one entry that carries the count 1", failing.inner.entries)
	}
}

// TestAllowlistOverflowEntry is the test of the overflow label: a limiter
// with room for one key gives the second address the aggregate entry, which
// names untracked client addresses. It fails when the overflow case is
// removed.
func TestAllowlistOverflowEntry(t *testing.T) {
	t.Parallel()

	g := newLimitedGate(t, 1, nil)

	g.refuse(t, netip.MustParseAddr("203.0.113.9"))
	g.refuse(t, netip.MustParseAddr("203.0.113.10"))

	var found bool

	for _, entry := range g.entries() {
		if strings.Contains(entry.Object, "untracked client addresses") {
			found = true
		}
	}

	if !found {
		t.Fatalf("no overflow entry: %+v", g.entries())
	}
}

// TestAllowlistShutdownFlush is the test of the drain at shutdown: the count
// that the limiter holds is written once. It fails when the drain is removed.
func TestAllowlistShutdownFlush(t *testing.T) {
	t.Parallel()

	g := newLimitedGate(t, 100, nil)
	address := netip.MustParseAddr("203.0.113.9")

	for range 3 {
		g.refuse(t, address)
	}

	allowlistAudit{
		store: g.gate.audit, limiter: g.gate.limiter, now: g.gate.now, logger: g.gate.logger,
	}.flush(t.Context())

	if !hasEntry(g.entries(), address, "2 further refusals") {
		t.Fatalf("no shutdown entry with the count 2: %+v", g.entries())
	}
}

// TestAllowlistLimiterIsNotShared fails when the gate shares the limiter of
// the other adapters: many client addresses would then fill its table before
// sign-in and push the denials of signed-in principals into the overflow count.
func TestAllowlistLimiterIsNotShared(t *testing.T) {
	t.Parallel()

	cfg := defaultSettings()
	if cfg.allowlistLimiter == nil || cfg.allowlistLimiter == cfg.denialLimiter {
		t.Fatal("the allowlist limiter is nil or the shared denial limiter")
	}
}
