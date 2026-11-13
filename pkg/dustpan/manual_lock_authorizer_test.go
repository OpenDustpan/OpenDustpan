package dustpan_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// lockTestNow is the clock of the servers in this file. Every manual lock gets
// this time as its start.
func lockTestNow() time.Time {
	return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC)
}

// lockServer returns a server with the namespaces acme and other, and its
// store. extra options come after the full set, so they win.
func lockServer(t *testing.T, extra ...dustpan.Option) (*dustpan.Server, *sqlite.Store) {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn),
		dustpan.WithClock(lockTestNow))

	for _, name := range []string{"acme", "other"} {
		err := st.CreateNamespace(t.Context(), namespace.Namespace{
			ID: namespace.NewID(), CreatedAt: lockTestNow(), Name: namespace.Name(name),
			PrimaryInstallation: installationName, FailoverVersion: 1,
			Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		})
		if err != nil {
			t.Fatalf("create namespace %s: %v", name, err)
		}
	}

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	for _, name := range []namespace.Name{"acme", "other"} {
		makeWeb(t, srv, st, name)
	}

	return srv, st
}

// makeWeb makes the workspace web in ns, so that a manual lock can name it.
func makeWeb(t *testing.T, srv *dustpan.Server, store any, ns namespace.Name) {
	t.Helper()

	if srv.ManualLocks() == nil {
		t.Fatal("the server has no manual lock service")
	}

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ref := workspace.Ref{Namespace: ns, Name: name}

	if _, err := srv.Workspaces().Create(t.Context(), ref, placeWorkspace(t, store, ns)); err != nil {
		t.Fatalf("create workspace in %s: %v", ns, err)
	}
}

// allowlistLockServer returns a server whose namespace acme holds the IP
// allowlist 198.51.100.0/24 and the workspace web. The bootstrap administrator
// is under the list too.
func allowlistLockServer(t *testing.T) *dustpan.Server {
	t.Helper()

	srv, st := newIPAllowlistServerWith(t, true)
	makeWeb(t, srv, st, "acme")

	return srv
}

// lockAdmin is the bootstrap administrator.
func lockAdmin() identity.Principal {
	return identity.Principal{
		Subject: "admin", DisplayName: "admin", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// lockAudit is the audit entry that a lock call needs.
func lockAudit() audit.Entry {
	return audit.Entry{
		At:            lockTestNow(),
		Namespace:     "acme",
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:lock",
		Object:        "opentofu:acme/web",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// lockUnit is the unit that the tests lock.
func lockUnit(ns namespace.Name) collection.UnitRef {
	return collection.UnitRef{Namespace: ns, Tool: "opentofu", Name: "web"}
}

// TestManualLockBlocksAToolLock proves, through the whole server, that a
// manual lock on the namespace refuses the lock request of a tool, and that
// the same request passes when no manual lock exists.
func TestManualLockBlocksAToolLock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		manualLock bool
		want       int
	}{
		{name: "no manual lock", manualLock: false, want: http.StatusOK},
		{name: "manual lock on the namespace", manualLock: true, want: http.StatusLocked},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.createNamespace(t, "acme", installationName)
			seedWebWorkspace(t, f)

			if tc.manualLock {
				lockNamespace(t, f)
			}

			if got, body := tofuDo(t, f, "LOCK", tofuLockBody); got != tc.want {
				t.Fatalf("LOCK = %d, want %d: %s", got, tc.want, body)
			}
		})
	}
}

// TestManualLockPassesOnlyAPhaseThatStartedBeforeIt proves that the manual lock
// service of the server lets a job token pass a lock only when the live phase
// of that job on the unit started before the lock. Each other case keeps the
// lock: a phase that started at or after the lock, a job that the clock does
// not know, and a request with no job.
func TestManualLockPassesOnlyAPhaseThatStartedBeforeIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		phaseStart time.Time
		askedJob   string
		wantPassed bool
	}{
		{
			name:       "phase before the lock",
			phaseStart: lockTestNow().Add(-time.Minute),
			askedJob:   "job-1",
			wantPassed: true,
		},
		{name: "phase at the lock", phaseStart: lockTestNow(), askedJob: "job-1", wantPassed: false},
		{
			name:       "phase after the lock",
			phaseStart: lockTestNow().Add(time.Minute),
			askedJob:   "job-1",
			wantPassed: false,
		},
		{name: "unknown job", phaseStart: lockTestNow().Add(-time.Minute), askedJob: "job-2", wantPassed: false},
		{name: "no job", phaseStart: lockTestNow().Add(-time.Minute), askedJob: "", wantPassed: false},
	}

	ref := lockUnit("acme")
	unit := manuallock.Unit{Ref: ref, NamespaceID: "ns-1", UnitID: "unt-1"}

	object, err := identity.UnitObject(ref.Tool, ref.Namespace, ref.Name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	object = object.WithUnitID(unit.UnitID)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var j job.Job

			j.ID = "job-1"
			j.NamespaceID = unit.NamespaceID
			j.State = job.StateRunning
			j.Spec.Unit = object
			j.Phases = make([]job.Phase, 1)
			j.Phases[0].StartedAt = tc.phaseStart

			srv, _ := lockServer(t, dustpan.WithJobStore(&phaseJobStore{Store: nil, j: j}))
			locks := srv.ManualLocks()

			_, lockErr := locks.Lock(t.Context(), lockAdmin(), manuallock.UnitTarget(ref), "freeze", lockAudit())
			if lockErr != nil {
				t.Fatalf("lock: %v", lockErr)
			}

			_, err := locks.CoveringFor(t.Context(), unit, tc.askedJob)

			if tc.wantPassed {
				if !errors.Is(err, manuallock.ErrNotLocked) {
					t.Fatalf("CoveringFor = %v, want ErrNotLocked", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("CoveringFor = %v, want the lock to hold", err)
			}
		})
	}
}

// TestManualLockServiceKeepsTheNamespaceServiceRule proves that the manual
// lock service of the server refuses every action of the namespace service
// principal on a lock, in its own namespace and in another one, also when the
// embedder authorizer allows everything.
func TestManualLockServiceKeepsTheNamespaceServiceRule(t *testing.T) {
	t.Parallel()

	svcPrincipal, err := identity.NamespaceService("acme")
	if err != nil {
		t.Fatalf("namespace service principal: %v", err)
	}

	srv, _ := lockServer(t, dustpan.WithAuthorizer(allowEverything{}))
	locks := srv.ManualLocks()

	for _, ns := range []namespace.Name{"acme", "other"} {
		target := manuallock.UnitTarget(lockUnit(ns))

		calls := []struct {
			name string
			call func() error
		}{
			{"lock", func() error {
				_, e := locks.Lock(t.Context(), svcPrincipal, target, "freeze", lockAudit())

				return e
			}},
			{"unlock", func() error { return locks.Unlock(t.Context(), svcPrincipal, target, lockAudit()) }},
			{"force-unlock", func() error { return locks.ForceUnlock(t.Context(), svcPrincipal, target, lockAudit()) }},
			{"read", func() error {
				_, e := locks.Get(t.Context(), svcPrincipal, target)

				return e
			}},
		}

		for _, tc := range calls {
			t.Run(tc.name+" in "+ns.String(), func(t *testing.T) {
				t.Parallel()

				if got := tc.call(); !errors.Is(got, identity.ErrDenied) {
					t.Fatalf("%s = %v, want ErrDenied", tc.name, got)
				}
			})
		}
	}

	// The refusals leave no lock behind.
	for _, ns := range []namespace.Name{"acme", "other"} {
		_, got := locks.LockOn(t.Context(), manuallock.UnitTarget(lockUnit(ns)))
		if !errors.Is(got, manuallock.ErrNotLocked) {
			t.Errorf("LockOn(%s) = %v, want ErrNotLocked", ns, got)
		}
	}
}

// TestManualLockServiceKeepsTheIPAllowlist proves that the manual lock service
// of the server refuses lock, unlock, force-unlock and read from an address
// outside the list of the namespace, with no route in front, and accepts the
// lock from an address inside the list. The routes refuse the request too.
func TestManualLockServiceKeepsTheIPAllowlist(t *testing.T) {
	t.Parallel()

	outside := netip.MustParseAddr("192.0.2.10")
	inside := netip.MustParseAddr("198.51.100.7")
	target := manuallock.UnitTarget(lockUnit("acme"))

	t.Run("service from outside the list", func(t *testing.T) {
		t.Parallel()

		locks := allowlistLockServer(t).ManualLocks()
		ctx := clientip.WithAddr(t.Context(), outside)

		calls := map[string]func() error{
			"lock": func() error {
				_, e := locks.Lock(ctx, lockAdmin(), target, "freeze", lockAudit())

				return e
			},
			"unlock":       func() error { return locks.Unlock(ctx, lockAdmin(), target, lockAudit()) },
			"force-unlock": func() error { return locks.ForceUnlock(ctx, lockAdmin(), target, lockAudit()) },
			"read": func() error {
				_, e := locks.Get(ctx, lockAdmin(), target)

				return e
			},
		}

		for name, call := range calls {
			got := call()
			if !errors.Is(got, identity.ErrDenied) || !errors.Is(got, ipallow.ErrAddressNotAllowed) {
				t.Errorf("%s from %s = %v, want ErrDenied by the IP allowlist", name, outside, got)
			}
		}

		if _, got := locks.LockOn(t.Context(), target); !errors.Is(got, manuallock.ErrNotLocked) {
			t.Errorf("LockOn after the refusals = %v, want ErrNotLocked", got)
		}
	})

	t.Run("service from inside the list", func(t *testing.T) {
		t.Parallel()

		locks := allowlistLockServer(t).ManualLocks()
		ctx := clientip.WithAddr(t.Context(), inside)

		if _, got := locks.Lock(ctx, lockAdmin(), target, "freeze", lockAudit()); got != nil {
			t.Fatalf("lock from %s = %v, want nil", inside, got)
		}
	})

	t.Run("route from outside the list", func(t *testing.T) {
		t.Parallel()

		srv := allowlistLockServer(t)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/inventory/unit-lock?unit=opentofu:acme/web", strings.NewReader(`{"reason":"freeze"}`))
		req.RemoteAddr = outside.String() + ":1234"
		req.Header.Set("Authorization", "Bearer "+adminToken)

		rec := httptest.NewRecorder()
		startedHandler(t, srv).ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf(
				"PUT unit-lock from %s = %d, want %d: %s",
				outside,
				rec.Code,
				http.StatusForbidden,
				rec.Body.String(),
			)
		}

		if _, got := srv.ManualLocks().LockOn(t.Context(), target); !errors.Is(got, manuallock.ErrNotLocked) {
			t.Errorf("LockOn after the refused route = %v, want ErrNotLocked", got)
		}
	})
}
