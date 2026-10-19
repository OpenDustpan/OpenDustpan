package manuallock_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
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
		ID: namespace.NewID(), CreatedAt: fixedNow(), Name: name, HomeRegion: "r", FailoverVersion: 3,
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
	putErr    error
	removeErr error
	held      manuallock.Lock
	getErr    error
}

// newStore returns a store that accepts every write and holds no lock.
func newStore() *recordingStore {
	return &recordingStore{
		put: nil, putEntry: nil, putFence: nil, removed: nil, putErr: nil, removeErr: nil,
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

func (s *recordingStore) GetLock(context.Context, manuallock.Target) (manuallock.Lock, error) {
	return s.held, s.getErr
}

func (s *recordingStore) CoveringLock(context.Context, collection.UnitRef) (manuallock.Lock, error) {
	return s.held, s.getErr
}

func newService(t *testing.T, s manuallock.Store) *manuallock.Service {
	t.Helper()

	svc, err := manuallock.NewService(manuallock.Config{
		Store: s, Namespaces: fakeNamespaces{}, Now: fixedNow,
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

func paymentsRef() collection.Ref {
	return collection.Ref{Namespace: "acme", Name: "payments"}
}

// zeroTarget is a target that names no object.
func zeroTarget() manuallock.Target {
	return manuallock.Target{
		Kind: "", Unit: collection.UnitRef{Namespace: "", Tool: "", Name: ""},
		Collection: collection.Ref{Namespace: "", Name: ""},
	}
}

func TestNewServiceNeedsItsPorts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  manuallock.Config
	}{
		{name: "no store", cfg: manuallock.Config{Store: nil, Namespaces: fakeNamespaces{}, Now: nil}},
		{name: "no namespace store", cfg: manuallock.Config{Store: newStore(), Namespaces: nil, Now: nil}},
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

			_, err := svc.Lock(t.Context(), tc.target, "alice", tc.reason, entry())
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

			got, err := svc.Lock(t.Context(), tc.target, "alice", reason, entry())
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

	_, err := svc.Lock(t.Context(), manuallock.UnitTarget(webUnit()), "alice", "x", entry())
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
	}{
		{
			name: "the holder removes",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.Unlock(t.Context(), target, "alice", entry())
			},
			storeErr: nil, wantErr: nil, wantCalls: []string{"alice"},
		},
		{
			name: "a removal with no holder is refused before the store",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.Unlock(t.Context(), target, "", entry())
			},
			storeErr: nil, wantErr: manuallock.ErrNotHolder, wantCalls: nil,
		},
		{
			name: "another principal is refused by the store",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.Unlock(t.Context(), target, "bob", entry())
			},
			storeErr: manuallock.ErrNotHolder, wantErr: manuallock.ErrNotHolder, wantCalls: []string{"bob"},
		},
		{
			name: "a force-unlock names no holder",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.ForceUnlock(t.Context(), target, entry())
			},
			storeErr: nil, wantErr: nil, wantCalls: []string{""},
		},
		{
			name: "a lock that does not exist",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.ForceUnlock(t.Context(), target, entry())
			},
			storeErr: manuallock.ErrNotLocked, wantErr: manuallock.ErrNotLocked, wantCalls: []string{""},
		},
		{
			name: "an invalid object",
			call: func(t *testing.T, s *manuallock.Service) error {
				t.Helper()

				return s.ForceUnlock(t.Context(), zeroTarget(), entry())
			},
			storeErr: nil, wantErr: manuallock.ErrInvalidTarget, wantCalls: nil,
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
		})
	}
}

func TestReadsPassThrough(t *testing.T) {
	t.Parallel()

	held := manuallock.Lock{Since: fixedNow(), Target: manuallock.UnitTarget(webUnit()), Holder: "bob", Reason: "r"}
	store := newStore()
	store.held = held
	svc := newService(t, store)

	if got, err := svc.Get(t.Context(), held.Target); err != nil || got != held {
		t.Errorf("Get = %+v, %v; want %+v", got, err, held)
	}

	if got, err := svc.Covering(t.Context(), webUnit()); err != nil || got != held {
		t.Errorf("Covering = %+v, %v; want %+v", got, err, held)
	}

	if _, err := svc.Get(t.Context(), zeroTarget()); !errors.Is(err, manuallock.ErrInvalidTarget) {
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
			name: "a tool that sets no noun", target: manuallock.UnitTarget(webUnit()), noun: "", object: "acme/web",
			want: "object acme/web has a manual lock " + detail,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := manuallock.Lock{Since: fixedNow(), Target: tc.target, Holder: "alice", Reason: "change window closed"}
			err := error(&manuallock.LockedError{Lock: l, Noun: tc.noun, Name: tc.object})

			if err.Error() != tc.want {
				t.Errorf("text = %q, want %q", err.Error(), tc.want)
			}

			if strings.Contains(err.Error(), "unit") {
				t.Errorf("text = %q: the word unit is not a word of any tool", err.Error())
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
