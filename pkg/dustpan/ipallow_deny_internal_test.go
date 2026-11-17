package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
		store: store, now: func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) },
		logger: slog.New(slog.DiscardHandler),
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
		audit:    nil, now: nil, logger: slog.New(slog.DiscardHandler),
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
