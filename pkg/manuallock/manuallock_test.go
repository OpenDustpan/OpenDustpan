package manuallock_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fixedNow is the clock of the tests.
func fixedNow() time.Time {
	return time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC)
}

// fakeNamespaces answers every namespace except "gone".
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == "gone" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: fixedNow(), Name: name, PrimaryInstallation: "r", FailoverVersion: 3,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (fakeNamespaces) Ping(context.Context) error { return nil }

// recordingStore records the calls of the service and answers from its fields.
type recordingStore struct {
	put       []manuallock.Lock
	putEntry  []audit.Entry
	putFence  []namespace.Fence
	removed   []string
	forced    int
	putErr    error
	removeErr error
	held      manuallock.Lock
	coverers  []manuallock.Lock
	getErr    error
}

// newStore returns a store that accepts every write and holds no lock.
func newStore() *recordingStore {
	return &recordingStore{
		put: nil, putEntry: nil, putFence: nil, removed: nil, putErr: nil, removeErr: nil, forced: 0, coverers: nil,
		held: manuallock.Lock{Since: time.Time{}, Target: zeroTarget(), Holder: "", Reason: ""}, getErr: nil,
	}
}

func (s *recordingStore) PutLock(_ context.Context, l manuallock.Lock, f namespace.Fence, e audit.Entry) error {
	if s.putErr != nil {
		return s.putErr
	}

	s.put = append(s.put, l)
	s.putFence = append(s.putFence, f)
	s.putEntry = append(s.putEntry, e)

	return nil
}

func (s *recordingStore) RemoveLock(
	_ context.Context, _ manuallock.Target, holder string, _ namespace.Fence, _ audit.Entry,
) (manuallock.Lock, error) {
	s.removed = append(s.removed, holder)

	return s.held, s.removeErr
}

func (s *recordingStore) ForceRemoveLock(
	_ context.Context, _ manuallock.Target, _ namespace.Fence, _ audit.Entry,
) (manuallock.Lock, error) {
	s.forced++

	return s.held, s.removeErr
}

func (s *recordingStore) GetLock(context.Context, manuallock.Target) (manuallock.Lock, error) {
	return s.held, s.getErr
}

func (s *recordingStore) CoveringLock(context.Context, collection.UnitRef) (manuallock.Lock, error) {
	return s.held, s.getErr
}

func (s *recordingStore) CoveringLocks(context.Context, collection.UnitRef) ([]manuallock.Lock, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}

	if len(s.coverers) > 0 {
		return s.coverers, nil
	}

	return []manuallock.Lock{s.held}, nil
}

// fakeAuthorizer allows the actions in allow on every object and records each
// check. A principal that is not alice, bob or admin has no action.
type fakeAuthorizer struct {
	allow map[identity.Action]bool
	mu    sync.Mutex
	asked []string
}

// allowAll returns an authorizer that allows every action.
func allowAll() *fakeAuthorizer {
	return &fakeAuthorizer{allow: map[identity.Action]bool{
		identity.ActionUnitLock: true, identity.ActionUnitRead: true, identity.ActionCollectionRead: true,
		identity.ActionUnitManage: true,
	}, mu: sync.Mutex{}, asked: nil}
}

// allowOnly returns an authorizer that allows only the actions given.
func allowOnly(actions ...identity.Action) *fakeAuthorizer {
	a := &fakeAuthorizer{allow: map[identity.Action]bool{}, mu: sync.Mutex{}, asked: nil}
	for _, action := range actions {
		a.allow[action] = true
	}

	return a
}

func (a *fakeAuthorizer) Authorize(
	_ context.Context,
	_ identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.asked = append(a.asked, string(action)+" "+obj.String())

	if !a.allow[action] {
		return fmt.Errorf("%w: %s on %s", identity.ErrDenied, action, obj)
	}

	return nil
}

// person returns the principal with the subject sub.
func person(sub string) identity.Principal {
	return identity.Principal{
		Subject: sub, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}
}

func newService(t *testing.T, s manuallock.Store) *manuallock.Service {
	t.Helper()

	return newServiceWith(t, s, allowAll())
}

func newServiceWith(t *testing.T, s manuallock.Store, authz manuallock.Authorizer) *manuallock.Service {
	t.Helper()

	svc, err := manuallock.NewService(manuallock.Config{
		Store: s, Namespaces: fakeNamespaces{}, Authorizer: authz, Now: fixedNow,
		Phases: nil,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

// entry returns the audit entry template of a request in namespace acme.
func entry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "acme", Subject: "alice", IDPSubject: "", TokenID: "", Action: "unit:lock",
		Object: "x", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
		Reason: "",
	}
}

func webUnit() collection.UnitRef {
	return collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}
}

// webLockUnit returns the unit of webUnit with the stable IDs of the tests.
func webLockUnit() manuallock.Unit {
	return manuallock.Unit{Ref: webUnit(), NamespaceID: "ns-1", UnitID: "unt-1"}
}

func acmeTarget() manuallock.Target {
	return manuallock.NamespaceTarget("acme")
}

func paymentsRef() collection.Ref {
	return collection.Ref{Namespace: "acme", Name: "payments"}
}

// zeroTarget is a target that names no object.
func zeroTarget() manuallock.Target {
	return manuallock.Target{
		Kind: "", Unit: collection.UnitRef{Namespace: "", Tool: "", Name: ""},
		Collection: collection.Ref{Namespace: "", Name: ""}, Space: "",
	}
}

func TestNewServiceNeedsItsPorts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  manuallock.Config
	}{
		{name: "no store", cfg: manuallock.Config{
			Store: nil, Namespaces: fakeNamespaces{}, Authorizer: allowAll(), Now: nil,
			Phases: nil,
		}},
		{name: "no namespace store", cfg: manuallock.Config{
			Store: newStore(), Namespaces: nil, Authorizer: allowAll(), Now: nil,
			Phases: nil,
		}},
		{name: "no authorizer", cfg: manuallock.Config{
			Store: newStore(), Namespaces: fakeNamespaces{}, Authorizer: nil, Now: nil,
			Phases: nil,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := manuallock.NewService(tc.cfg); !errors.Is(err, manuallock.ErrMissingDependency) {
				t.Errorf("err = %v, want ErrMissingDependency", err)
			}
		})
	}
}

func TestLockChecksTheRequestBeforeTheStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target manuallock.Target
		reason string
		want   error
	}{
		{
			name:   "empty reason",
			target: manuallock.UnitTarget(webUnit()),
			reason: "",
			want:   manuallock.ErrReasonRequired,
		},
		{
			name:   "blank reason",
			target: manuallock.UnitTarget(webUnit()),
			reason: " \t ",
			want:   manuallock.ErrReasonRequired,
		},
		{
			name: "reason of 501 characters", target: manuallock.UnitTarget(webUnit()),
			reason: strings.Repeat("é", 501), want: audit.ErrInvalidReason,
		},
		{
			name: "reason with a line break", target: manuallock.UnitTarget(webUnit()),
			reason: "a\nb", want: audit.ErrInvalidReason,
		},
		{
			name: "reason that is not UTF-8", target: manuallock.UnitTarget(webUnit()),
			reason: "\xff", want: audit.ErrInvalidReason,
		},
		{name: "no object", target: zeroTarget(), reason: "x", want: manuallock.ErrInvalidTarget},
		{
			name:   "unit with no tool",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "", Name: "web"}),
			reason: "x", want: manuallock.ErrInvalidTarget,
		},
		{
			name:   "collection with no name",
			target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: ""}),
			reason: "x", want: manuallock.ErrInvalidTarget,
		},
		{
			name:   "namespace that does not exist",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "gone", Tool: "opentofu", Name: "web"}),
			reason: "x", want: namespace.ErrNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			svc := newService(t, store)

			_, err := svc.Lock(t.Context(), person("alice"), tc.target, tc.reason, entry())
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}

			if len(store.put) != 0 {
				t.Errorf("store writes = %d, want 0", len(store.put))
			}
		})
	}
}

func TestLockWritesTheLockAndTheEntry(t *testing.T) {
	t.Parallel()

	reason := strings.Repeat("é", 500)

	cases := []struct {
		name   string
		target manuallock.Target
	}{
		{name: "unit", target: manuallock.UnitTarget(webUnit())},
		{name: "collection", target: manuallock.CollectionTarget(paymentsRef())},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			svc := newService(t, store)

			got, err := svc.Lock(t.Context(), person("alice"), tc.target, reason, entry())
			if err != nil {
				t.Fatalf("Lock: %v", err)
			}

			want := manuallock.Lock{Since: fixedNow(), Target: tc.target, Holder: "alice", Reason: reason}
			checkWrite(t, store, got, want)
		})
	}
}

// checkWrite checks the lock that Lock returned, the lock that the store got,
// the fence of the write and the audit entry of the write.
func checkWrite(t *testing.T, store *recordingStore, got, want manuallock.Lock) {
	t.Helper()

	if got != want {
		t.Errorf("lock = %+v, want %+v", got, want)
	}

	if len(store.put) != 1 || store.put[0] != want {
		t.Fatalf("stored = %+v, want [%+v]", store.put, want)
	}

	if f := store.putFence[0]; f.Namespace != "acme" || f.FailoverVersion != 3 {
		t.Errorf("fence = %+v, want the fence of the namespace read now", f)
	}

	if e := store.putEntry[0]; !e.At.Equal(fixedNow()) || e.Reason != want.Reason {
		t.Errorf("entry time %v reason %q, want the clock and the reason", e.At, e.Reason)
	}
}

func TestLockOfAnObjectThatHasOneNamesTheHolder(t *testing.T) {
	t.Parallel()

	store := newStore()
	store.putErr = manuallock.ErrAlreadyLocked
	store.held = manuallock.Lock{
		Since: fixedNow(), Target: manuallock.UnitTarget(webUnit()), Holder: "bob", Reason: "change window",
	}
	svc := newService(t, store)

	_, err := svc.Lock(t.Context(), person("alice"), manuallock.UnitTarget(webUnit()), "x", entry())
	if !errors.Is(err, manuallock.ErrAlreadyLocked) {
		t.Fatalf("err = %v, want ErrAlreadyLocked", err)
	}

	for _, want := range []string{"bob", "change window"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to hold %q", err, want)
		}
	}
}

func TestRemoval(t *testing.T) {
	t.Parallel()

	target := manuallock.UnitTarget(webUnit())

	cases := []struct {
		name      string
		call      func(*testing.T, *manuallock.Service) error
		storeErr  error
		wantErr   error
		wantCalls []string
		wantForce int
	}{
		{
			name: "the holder removes",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.Unlock(t.Context(), person("alice"), target, entry())
			},
			storeErr: nil, wantErr: nil, wantCalls: []string{"alice"}, wantForce: 0,
		},
		{
			name: "a removal with no holder is refused before the store",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.Unlock(t.Context(), person(""), target, entry())
			},
			storeErr: nil, wantErr: manuallock.ErrNotHolder, wantCalls: nil, wantForce: 0,
		},
		{
			name: "another principal is refused by the store",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.Unlock(t.Context(), person("bob"), target, entry())
			},
			storeErr:  manuallock.ErrNotHolder,
			wantErr:   manuallock.ErrNotHolder,
			wantCalls: []string{"bob"},
			wantForce: 0,
		},
		{
			name: "a force-unlock uses the forced removal of the store",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.ForceUnlock(t.Context(), person("admin"), target, entry())
			},
			storeErr: nil, wantErr: nil, wantCalls: nil, wantForce: 1,
		},
		{
			name: "a lock that does not exist",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.ForceUnlock(t.Context(), person("admin"), target, entry())
			},
			storeErr: manuallock.ErrNotLocked, wantErr: manuallock.ErrNotLocked, wantCalls: nil, wantForce: 1,
		},
		{
			name: "an invalid object",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.ForceUnlock(t.Context(), person("admin"), zeroTarget(), entry())
			},
			storeErr: nil, wantErr: manuallock.ErrInvalidTarget, wantCalls: nil, wantForce: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			store.removeErr = tc.storeErr

			err := tc.call(t, newService(t, store))
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}

			if strings.Join(store.removed, ",") != strings.Join(tc.wantCalls, ",") {
				t.Errorf("store removals for holders %q, want %q", store.removed, tc.wantCalls)
			}

			if store.forced != tc.wantForce {
				t.Errorf("store forced removals = %d, want %d", store.forced, tc.wantForce)
			}
		})
	}
}

func TestReadsPassThrough(t *testing.T) {
	t.Parallel()

	held := manuallock.Lock{Since: fixedNow(), Target: manuallock.UnitTarget(webUnit()), Holder: "bob", Reason: "r"}
	store := newStore()
	store.held = held
	svc := newService(t, store)

	if got, err := svc.Get(t.Context(), person("alice"), held.Target); err != nil || got != held {
		t.Errorf("Get = %+v, %v; want %+v", got, err, held)
	}

	if got, err := svc.Covering(t.Context(), webUnit()); err != nil || got != held {
		t.Errorf("Covering = %+v, %v; want %+v", got, err, held)
	}

	if _, err := svc.Get(t.Context(), person("alice"), zeroTarget()); !errors.Is(err, manuallock.ErrInvalidTarget) {
		t.Errorf("Get of no object: err = %v, want ErrInvalidTarget", err)
	}
}

func TestLockedErrorNamesTheHolderAndTheReason(t *testing.T) {
	t.Parallel()

	const detail = "by alice since 2026-10-19T10:00:00Z: change window closed"

	cases := []struct {
		name   string
		target manuallock.Target
		noun   string
		object string
		want   string
	}{
		{
			name: "stack", target: manuallock.UnitTarget(webUnit()), noun: "stack", object: "acme/web/prod",
			want: "stack acme/web/prod has a manual lock " + detail,
		},
		{
			name: "workspace", target: manuallock.UnitTarget(webUnit()), noun: "workspace", object: "acme/web",
			want: "workspace acme/web has a manual lock " + detail,
		},
		{
			name: "stack in a locked collection", target: manuallock.CollectionTarget(paymentsRef()),
			noun: "stack", object: "acme/web/prod",
			want: "stack acme/web/prod is in collection acme/payments, which has a manual lock " + detail,
		},
		{
			name: "workspace in a locked collection", target: manuallock.CollectionTarget(paymentsRef()),
			noun: "workspace", object: "acme/web",
			want: "workspace acme/web is in collection acme/payments, which has a manual lock " + detail,
		},
		{
			name: "stack in a locked namespace", target: acmeTarget(), noun: "stack", object: "acme/web/prod",
			want: "stack acme/web/prod is in namespace acme, which has a manual lock " + detail,
		},
		{
			name: "workspace in a locked namespace", target: acmeTarget(), noun: "workspace", object: "acme/web",
			want: "workspace acme/web is in namespace acme, which has a manual lock " + detail,
		},
		{
			name: "a tool that sets no noun", target: manuallock.UnitTarget(webUnit()), noun: "", object: "acme/web",
			want: "stack or workspace acme/web has a manual lock " + detail,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := manuallock.Lock{Since: fixedNow(), Target: tc.target, Holder: "alice", Reason: "change window closed"}
			locked := &manuallock.LockedError{
				Lock: l, Unit: collection.UnitRef{Namespace: "", Tool: "", Name: ""}, Noun: tc.noun, Name: tc.object,
			}
			err := error(locked)

			if locked.Refusal() != tc.want {
				t.Errorf("refusal = %q, want %q", locked.Refusal(), tc.want)
			}

			if strings.Contains(locked.Refusal(), "unit") {
				t.Errorf("refusal = %q: the word unit is not a word of any tool", locked.Refusal())
			}

			if !errors.Is(err, manuallock.ErrLocked) {
				t.Error("the error does not match ErrLocked")
			}

			if errors.Is(err, manuallock.ErrNotLocked) {
				t.Error("the error matches ErrNotLocked")
			}
		})
	}
}

func TestTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		target    manuallock.Target
		wantNS    namespace.Name
		wantValid bool
	}{
		{name: "unit", target: manuallock.UnitTarget(webUnit()), wantNS: "acme", wantValid: true},
		{name: "collection", target: manuallock.CollectionTarget(paymentsRef()), wantNS: "acme", wantValid: true},
		{name: "namespace", target: acmeTarget(), wantNS: "acme", wantValid: true},
		{name: "namespace with no name", target: manuallock.NamespaceTarget(""), wantNS: "", wantValid: false},
		{name: "unknown kind", target: manuallock.Target{
			Kind: "tenant", Unit: webUnit(), Collection: paymentsRef(), Space: "acme",
		}, wantNS: "", wantValid: false},
		{name: "zero", target: zeroTarget(), wantNS: "", wantValid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.target.Namespace(); got != tc.wantNS {
				t.Errorf("namespace = %q, want %q", got, tc.wantNS)
			}

			if valid := tc.target.Valid() == nil; valid != tc.wantValid {
				t.Errorf("valid = %v, want %v", valid, tc.wantValid)
			}
		})
	}
}

// TestServiceChecksAuthorizationAtThePointOfUse calls the service directly, as
// if the route had not checked anything, with a principal that lacks the action.
// The service must refuse before it reads the fence or touches the store.
func TestServiceChecksAuthorizationAtThePointOfUse(t *testing.T) {
	t.Parallel()

	unit := manuallock.UnitTarget(webUnit())
	coll := manuallock.CollectionTarget(paymentsRef())
	space := acmeTarget()

	cases := []struct {
		name   string
		allow  []identity.Action
		call   func(*manuallock.Service) error
		action string
		object string
	}{
		{
			name:  "lock a unit without unit:lock",
			allow: []identity.Action{identity.ActionUnitRead, identity.ActionUnitManage},
			call: func(s *manuallock.Service) error {
				_, err := s.Lock(context.Background(), person("alice"), unit, "r", entry())

				return err
			},
			action: "unit:lock",
			object: unit.Unit.String(),
		},
		{
			name: "lock a collection without unit:lock", allow: []identity.Action{identity.ActionCollectionRead},
			call: func(s *manuallock.Service) error {
				_, err := s.Lock(context.Background(), person("alice"), coll, "r", entry())

				return err
			},
			action: "unit:lock", object: "",
		},
		{
			name:  "unlock without unit:lock",
			allow: []identity.Action{identity.ActionUnitRead, identity.ActionUnitManage},
			call: func(s *manuallock.Service) error {
				return s.Unlock(context.Background(), person("alice"), unit, entry())
			},
			action: "unit:lock",
			object: unit.Unit.String(),
		},
		{
			name:  "force-unlock with unit:lock but without unit:manage",
			allow: []identity.Action{identity.ActionUnitLock, identity.ActionUnitRead},
			call: func(s *manuallock.Service) error {
				return s.ForceUnlock(context.Background(), person("alice"), unit, entry())
			},
			action: "unit:manage", object: unit.Unit.String(),
		},
		{
			name: "force-unlock a collection without unit:manage", allow: []identity.Action{identity.ActionUnitLock},
			call: func(s *manuallock.Service) error {
				return s.ForceUnlock(context.Background(), person("alice"), coll, entry())
			},
			action: "unit:manage", object: "",
		},
		{
			name:  "get a unit lock without unit:read",
			allow: []identity.Action{identity.ActionUnitLock, identity.ActionUnitManage},
			call: func(s *manuallock.Service) error {
				_, err := s.Get(context.Background(), person("alice"), unit)

				return err
			},
			action: "unit:read",
			object: unit.Unit.String(),
		},
		{
			name: "get a collection lock without collection:read", allow: []identity.Action{identity.ActionUnitRead},
			call: func(s *manuallock.Service) error {
				_, err := s.Get(context.Background(), person("alice"), coll)

				return err
			},
			action: "collection:read", object: "",
		},
		{
			// namespace:break-freeze lets a job start through a freeze. It is no
			// action on a manual lock, so it never lets a caller lock a namespace.
			name: "lock a namespace with only namespace:break-freeze", allow: []identity.Action{
				identity.ActionNamespaceBreakFreeze, identity.ActionNamespaceRead, identity.ActionUnitManage,
			},
			call: func(s *manuallock.Service) error {
				_, err := s.Lock(context.Background(), person("alice"), space, "r", entry())

				return err
			},
			action: "unit:lock", object: "acme",
		},
		{
			name: "unlock a namespace without unit:lock", allow: []identity.Action{
				identity.ActionNamespaceBreakFreeze, identity.ActionNamespaceRead, identity.ActionUnitManage,
			},
			call: func(s *manuallock.Service) error {
				return s.Unlock(context.Background(), person("alice"), space, entry())
			},
			action: "unit:lock", object: "acme",
		},
		{
			name:  "force-unlock a namespace with unit:lock but without unit:manage",
			allow: []identity.Action{identity.ActionUnitLock, identity.ActionNamespaceBreakFreeze},
			call: func(s *manuallock.Service) error {
				return s.ForceUnlock(context.Background(), person("alice"), space, entry())
			},
			action: "unit:manage", object: "acme",
		},
		{
			name: "get a namespace lock without namespace:read", allow: []identity.Action{identity.ActionUnitRead},
			call: func(s *manuallock.Service) error {
				_, err := s.Get(context.Background(), person("alice"), space)

				return err
			},
			action: "namespace:read", object: "acme",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			authz := allowOnly(tc.allow...)

			err := tc.call(newServiceWith(t, store, authz))
			if !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("err = %v, want identity.ErrDenied", err)
			}

			if len(authz.asked) != 1 || !strings.HasPrefix(authz.asked[0], tc.action+" ") ||
				!strings.Contains(authz.asked[0], tc.object) {
				t.Errorf("authorization checks = %q, want one check of %s on %q", authz.asked, tc.action, tc.object)
			}

			if len(store.put) != 0 || len(store.removed) != 0 || store.forced != 0 {
				t.Errorf(
					"store was reached: put %d, removed %d, forced %d",
					len(store.put),
					len(store.removed),
					store.forced,
				)
			}
		})
	}
}

// TestServiceFailsClosedOnAnAuthorizerError sends an error that is not a
// denial. The service must refuse and not touch the store.
func TestServiceFailsClosedOnAnAuthorizerError(t *testing.T) {
	t.Parallel()

	store := newStore()
	svc := newServiceWith(t, store, errAuthorizer{})

	_, err := svc.Lock(t.Context(), person("alice"), manuallock.UnitTarget(webUnit()), "r", entry())
	if err == nil || len(store.put) != 0 {
		t.Fatalf("Lock: err = %v, writes = %d; want an error and no write", err, len(store.put))
	}
}

var errPolicyDown = errors.New("policy store is down")

type errAuthorizer struct{}

func (errAuthorizer) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errPolicyDown
}

// TestLockedTextHasNoTerminalControls puts a hostile reason, holder and name
// straight into a Lock, as if a restored backup had brought them in. No text
// that reaches a terminal may hold a control or format character.
func TestLockedTextHasNoTerminalControls(t *testing.T) {
	t.Parallel()

	hostile := "ok\x1b[2J\x1b]0;owned\x07\r\nforged: line\u202eevil\u2028\u200b\x00\xff"

	cases := []struct {
		name   string
		target manuallock.Target
	}{
		{name: "unit", target: manuallock.UnitTarget(webUnit())},
		{name: "collection", target: manuallock.CollectionTarget(paymentsRef())},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := manuallock.Lock{Since: fixedNow(), Target: tc.target, Holder: hostile, Reason: hostile}
			locked := &manuallock.LockedError{
				Lock: l, Unit: collection.UnitRef{Namespace: "", Tool: "", Name: ""},
				Noun: "stack" + hostile, Name: "acme/web" + hostile,
			}

			checkNoHiddenText(t, "Detail", l.Detail())
			checkNoHiddenText(t, "LockedError", locked.Error())
		})
	}
}

// checkNoHiddenText fails when text holds a hidden character, or when it lost
// the visible text or the written-out escape.
func checkNoHiddenText(t *testing.T, name, text string) {
	t.Helper()

	hidden := func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
	}

	if i := strings.IndexFunc(text, hidden); i >= 0 {
		t.Errorf("%s = %q holds a hidden character at byte %d", name, text, i)
	}

	if !strings.Contains(text, "forged: line") || !strings.Contains(text, "\\u001B") {
		t.Errorf("%s = %q, want the visible text kept and the escape written out", name, text)
	}
}

func TestSafeText(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, want string }{
		{name: "plain", in: "freeze for audit", want: "freeze for audit"},
		{name: "unicode letters", in: "gel \u00e9t\u00e9 \u65e5\u672c", want: "gel \u00e9t\u00e9 \u65e5\u672c"},
		{name: "escape", in: "a\x1bb", want: "a\\u001Bb"},
		{name: "bidi override", in: "a\u202eb", want: "a\\u202Eb"},
		{name: "line separator", in: "a\u2028b", want: "a\\u2028b"},
		{name: "paragraph separator", in: "a\u2029b", want: "a\\u2029b"},
		{name: "next line control", in: "a\u0085b", want: "a\\u0085b"},
		{name: "bad bytes", in: "a\xffb", want: "a\ufffdb"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := manuallock.SafeText(tc.in); got != tc.want {
				t.Errorf("SafeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// fakePhases answers the start of the phase of one job.
type fakePhases struct {
	jobs map[string]time.Time
	err  error
}

func (f fakePhases) PhaseStartedAt(_ context.Context, jobID string, _ manuallock.Unit) (time.Time, error) {
	if f.err != nil {
		return time.Time{}, f.err
	}

	started, ok := f.jobs[jobID]
	if !ok {
		return time.Time{}, manuallock.ErrNoPhase
	}

	return started, nil
}

var errClockDown = errors.New("job store is down")

// TestCoveringForLetsAnEarlierPhasePass proves a lock request that carries the
// job token of a phase that started before the manual lock passes, and that
// every other request meets the lock: another job, a phase that started at or
// after the lock, a clock that fails, a clock of zero and a service with no clock.
func TestCoveringForLetsAnEarlierPhasePass(t *testing.T) {
	t.Parallel()

	since := fixedNow()
	held := manuallock.Lock{Since: since, Target: acmeTarget(), Holder: "alice", Reason: "freeze"}

	cases := []struct {
		name       string
		phases     manuallock.PhaseClock
		jobID      string
		wantLocked bool
	}{
		{
			name: "phase started before the lock", jobID: "job-1", wantLocked: false,
			phases: fakePhases{jobs: map[string]time.Time{"job-1": since.Add(-time.Minute)}, err: nil},
		},
		{
			name: "phase started at the time of the lock", jobID: "job-1", wantLocked: true,
			phases: fakePhases{jobs: map[string]time.Time{"job-1": since}, err: nil},
		},
		{
			name: "phase started after the lock", jobID: "job-1", wantLocked: true,
			phases: fakePhases{jobs: map[string]time.Time{"job-1": since.Add(time.Second)}, err: nil},
		},
		{
			name: "job with no live phase on the unit", jobID: "job-2", wantLocked: true,
			phases: fakePhases{jobs: map[string]time.Time{"job-1": since.Add(-time.Minute)}, err: nil},
		},
		{name: "no job token", jobID: "", wantLocked: true, phases: fakePhases{
			jobs: map[string]time.Time{"": since.Add(-time.Minute)}, err: nil,
		}},
		{
			name: "clock fails", jobID: "job-1", wantLocked: true,
			phases: fakePhases{jobs: nil, err: errClockDown},
		},
		{
			name: "clock answers zero", jobID: "job-1", wantLocked: true,
			phases: fakePhases{jobs: map[string]time.Time{"job-1": {}}, err: nil},
		},
		{name: "no clock", jobID: "job-1", wantLocked: true, phases: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			store.held = held

			svc, err := manuallock.NewService(manuallock.Config{
				Store: store, Namespaces: fakeNamespaces{}, Authorizer: allowAll(), Phases: tc.phases, Now: fixedNow,
			})
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}

			got, err := svc.CoveringFor(t.Context(), webLockUnit(), tc.jobID)
			if tc.wantLocked {
				if err != nil || got != held {
					t.Fatalf("CoveringFor = %+v, %v; want the lock", got, err)
				}

				return
			}

			if !errors.Is(err, manuallock.ErrNotLocked) {
				t.Fatalf("CoveringFor = %+v, %v; want ErrNotLocked", got, err)
			}
		})
	}
}

// TestCoveringForNeedsEveryLockToBeNewer proves a lock request passes only when
// its phase started before every manual lock that covers the unit: an older
// lock on a wider object keeps the refusal that a newer lock on the unit alone
// would lift, so adding a lock never lifts a refusal.
func TestCoveringForNeedsEveryLockToBeNewer(t *testing.T) {
	t.Parallel()

	phase := fixedNow()
	unitLock := manuallock.Lock{
		Since: phase.Add(time.Minute), Target: manuallock.UnitTarget(webUnit()), Holder: "alice", Reason: "unit",
	}
	spaceLock := manuallock.Lock{
		Since: phase.Add(-time.Minute), Target: acmeTarget(), Holder: "bob", Reason: "namespace",
	}

	var noLock manuallock.Lock

	cases := []struct {
		name    string
		locks   []manuallock.Lock
		want    manuallock.Lock
		wantErr error
	}{
		{
			name: "one newer lock", locks: []manuallock.Lock{unitLock},
			want: noLock, wantErr: manuallock.ErrNotLocked,
		},
		{
			name: "newer unit lock, older namespace lock", locks: []manuallock.Lock{unitLock, spaceLock},
			want: spaceLock, wantErr: nil,
		},
		{name: "older namespace lock alone", locks: []manuallock.Lock{spaceLock}, want: spaceLock, wantErr: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			store.coverers = tc.locks

			svc, err := manuallock.NewService(manuallock.Config{
				Store: store, Namespaces: fakeNamespaces{}, Authorizer: allowAll(), Now: fixedNow,
				Phases: fakePhases{jobs: map[string]time.Time{"job-1": phase}, err: nil},
			})
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}

			got, err := svc.CoveringFor(t.Context(), webLockUnit(), "job-1")
			if !errors.Is(err, tc.wantErr) || got != tc.want {
				t.Fatalf("CoveringFor = %+v, %v; want %+v, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

// TestCoveringForKeepsTheStoreAnswer proves an unlocked unit and a failing
// store give the same answer with and without a job token.
func TestCoveringForKeepsTheStoreAnswer(t *testing.T) {
	t.Parallel()

	for _, storeErr := range []error{manuallock.ErrNotLocked, errClockDown} {
		store := newStore()
		store.getErr = storeErr

		svc, err := manuallock.NewService(manuallock.Config{
			Store: store, Namespaces: fakeNamespaces{}, Authorizer: allowAll(), Now: fixedNow,
			Phases: fakePhases{jobs: map[string]time.Time{"job-1": fixedNow().Add(-time.Hour)}, err: nil},
		})
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}

		if _, err := svc.CoveringFor(t.Context(), webLockUnit(), "job-1"); !errors.Is(err, storeErr) {
			t.Errorf("CoveringFor err = %v, want %v", err, storeErr)
		}
	}
}

// idPhases answers the start of the phase of one unit by its stable IDs, as
// the phase clock of the job store does. The reference is not compared, so a
// rename of the unit changes no answer. A unit with an empty unit ID is a unit
// that does not exist yet, and the fake matches it by the namespace ID, the
// tool and the name.
type idPhases struct {
	namespaceID namespace.ID
	unitID      string
	tool        string
	name        string
	start       time.Time
}

func (f idPhases) PhaseStartedAt(_ context.Context, _ string, unit manuallock.Unit) (time.Time, error) {
	if unit.NamespaceID == "" || unit.NamespaceID != f.namespaceID {
		return time.Time{}, manuallock.ErrNoPhase
	}

	if unit.UnitID != "" {
		if unit.UnitID != f.unitID {
			return time.Time{}, manuallock.ErrNoPhase
		}

		return f.start, nil
	}

	if unit.Ref.Tool != f.tool || unit.Ref.Name != f.name {
		return time.Time{}, manuallock.ErrNoPhase
	}

	return f.start, nil
}

// TestCoveringForUsesTheUnitIDs proves that the phase clock receives the stable
// namespace ID and unit ID of the request, so a phase passes the lock after a
// rename of its unit. A request with an empty ID fails closed: it meets the
// lock.
func TestCoveringForUsesTheUnitIDs(t *testing.T) {
	t.Parallel()

	since := fixedNow()
	held := manuallock.Lock{Since: since, Target: acmeTarget(), Holder: "alice", Reason: "freeze"}
	good := webLockUnit()
	renamed := manuallock.Unit{
		Ref:         collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web-2"},
		NamespaceID: good.NamespaceID,
		UnitID:      good.UnitID,
	}

	cases := []struct {
		name       string
		unit       manuallock.Unit
		wantLocked bool
	}{
		{"the unit IDs", good, false},
		{"a renamed unit with the same IDs", renamed, false},
		{"no namespace ID", manuallock.Unit{Ref: good.Ref, NamespaceID: "", UnitID: good.UnitID}, true},
		{"no unit ID", manuallock.Unit{Ref: good.Ref, NamespaceID: good.NamespaceID, UnitID: ""}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			store.held = held

			svc, err := manuallock.NewService(manuallock.Config{
				Store: store, Namespaces: fakeNamespaces{}, Authorizer: allowAll(), Now: fixedNow,
				Phases: idPhases{
					namespaceID: good.NamespaceID, unitID: good.UnitID,
					tool: good.Ref.Tool, name: good.Ref.Name, start: since.Add(-time.Minute),
				},
			})
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}

			got, err := svc.CoveringFor(t.Context(), tc.unit, "job-1")
			if tc.wantLocked {
				if err != nil || got != held {
					t.Fatalf("CoveringFor = %+v, %v; want the lock", got, err)
				}

				return
			}

			if !errors.Is(err, manuallock.ErrNotLocked) {
				t.Fatalf("CoveringFor = %+v, %v; want ErrNotLocked", got, err)
			}
		})
	}
}

// TestNamespaceLockForANewUnitLetsAnEarlierPhasePass proves the rule of
// NamespaceLockFor for a unit that does not exist yet. The unit has no unit ID,
// so the phase clock matches the job by the namespace ID, the tool and the
// name. A phase that started before the namespace lock passes it. A unit with
// no namespace ID matches no job, so it keeps the lock.
func TestNamespaceLockForANewUnitLetsAnEarlierPhasePass(t *testing.T) {
	t.Parallel()

	since := fixedNow()
	held := manuallock.Lock{Since: since, Target: acmeTarget(), Holder: "alice", Reason: "freeze"}
	good := webLockUnit()
	newUnit := manuallock.Unit{Ref: good.Ref, NamespaceID: good.NamespaceID, UnitID: ""}

	cases := []struct {
		name       string
		unit       manuallock.Unit
		wantLocked bool
	}{
		{"new unit with an earlier phase", newUnit, false},
		{"new unit with no namespace ID", manuallock.Unit{Ref: good.Ref, NamespaceID: "", UnitID: ""}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			store.held = held

			svc, err := manuallock.NewService(manuallock.Config{
				Store: store, Namespaces: fakeNamespaces{}, Authorizer: allowAll(), Now: fixedNow,
				Phases: idPhases{
					namespaceID: good.NamespaceID, unitID: good.UnitID,
					tool: good.Ref.Tool, name: good.Ref.Name, start: since.Add(-time.Minute),
				},
			})
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}

			got, err := svc.NamespaceLockFor(t.Context(), tc.unit, "job-1")
			if tc.wantLocked {
				wantNamespaceLockHeld(t, got, err, held)

				return
			}

			wantNamespaceLockPassed(t, got, err)
		})
	}
}

// TestNamespaceLockForLetsAnEarlierPhasePass proves the rule of CoveringFor for
// a unit that does not exist yet: only the lock of the namespace covers it, and
// a job whose phase started before that lock passes it. Every other request
// meets the lock: a phase at or after the lock, another job, no job, a clock
// that fails, a clock of zero and a service with no clock. PhaseStart gives the
// same start, or zero where the request meets the lock.
func TestNamespaceLockForLetsAnEarlierPhasePass(t *testing.T) {
	t.Parallel()

	since := fixedNow()
	held := manuallock.Lock{Since: since, Target: acmeTarget(), Holder: "alice", Reason: "freeze"}
	before := since.Add(-time.Minute)

	cases := []struct {
		name       string
		phases     manuallock.PhaseClock
		jobID      string
		wantLocked bool
		wantStart  time.Time
	}{
		{
			"phase started before the lock",
			fakePhases{jobs: map[string]time.Time{"job-1": before}, err: nil},
			"job-1", false, before,
		},
		{
			"phase started at the time of the lock",
			fakePhases{jobs: map[string]time.Time{"job-1": since}, err: nil},
			"job-1", true, since,
		},
		{
			"phase started after the lock",
			fakePhases{jobs: map[string]time.Time{"job-1": since.Add(time.Second)}, err: nil},
			"job-1", true, since.Add(time.Second),
		},
		{
			"job with no live phase on the unit",
			fakePhases{jobs: map[string]time.Time{"job-1": before}, err: nil},
			"job-2", true,
			time.Time{},
		},
		{"no job token", fakePhases{jobs: map[string]time.Time{"": before}, err: nil}, "", true, time.Time{}},
		{"clock fails", fakePhases{jobs: nil, err: errClockDown}, "job-1", true, time.Time{}},
		{
			"clock answers zero",
			fakePhases{jobs: map[string]time.Time{"job-1": {}}, err: nil},
			"job-1",
			true,
			time.Time{},
		},
		{"no clock", nil, "job-1", true, time.Time{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newStore()
			store.held = held

			svc, err := manuallock.NewService(manuallock.Config{
				Store: store, Namespaces: fakeNamespaces{}, Authorizer: allowAll(), Phases: tc.phases, Now: fixedNow,
			})
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}

			if start := svc.PhaseStart(t.Context(), webLockUnit(), tc.jobID); !start.Equal(tc.wantStart) {
				t.Errorf("PhaseStart = %v, want %v", start, tc.wantStart)
			}

			got, err := svc.NamespaceLockFor(t.Context(), webLockUnit(), tc.jobID)
			if tc.wantLocked {
				wantNamespaceLockHeld(t, got, err, held)

				return
			}

			wantNamespaceLockPassed(t, got, err)
		})
	}
}

// wantNamespaceLockHeld fails the test unless the answer of NamespaceLockFor is
// the held lock.
func wantNamespaceLockHeld(t *testing.T, got manuallock.Lock, err error, held manuallock.Lock) {
	t.Helper()

	if err != nil || got != held {
		t.Fatalf("NamespaceLockFor = %+v, %v; want the lock", got, err)
	}
}

// wantNamespaceLockPassed fails the test unless NamespaceLockFor answered
// manuallock.ErrNotLocked.
func wantNamespaceLockPassed(t *testing.T, got manuallock.Lock, err error) {
	t.Helper()

	if !errors.Is(err, manuallock.ErrNotLocked) {
		t.Fatalf("NamespaceLockFor = %+v, %v; want ErrNotLocked", got, err)
	}
}

// TestNamespaceLockForWithNoLock proves a namespace with no manual lock covers
// no new unit, whatever the job.
func TestNamespaceLockForWithNoLock(t *testing.T) {
	t.Parallel()

	store := newStore()
	store.getErr = manuallock.ErrNotLocked

	svc := newService(t, store)

	if _, err := svc.NamespaceLockFor(t.Context(), webLockUnit(), "job-1"); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Fatalf("NamespaceLockFor with no lock = %v, want ErrNotLocked", err)
	}
}
