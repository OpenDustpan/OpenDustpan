package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// TestCommittedLockStaysHeld checks that a committed lock has no expiry: a
// second holder's acquire returns ErrLocked at each age of the lock, and only
// the release of the holder or a force-unlock frees it. It also races two lock
// acquires and two state version writes with different fences.
func TestCommittedLockStaysHeld(t *testing.T) {
	t.Parallel()

	base := outageBase(t)

	t.Run("a second holder gets ErrLocked at each age", func(t *testing.T) {
		t.Parallel()

		checkHeldAtEachAge(t, newLockEnv(t, base))
	})

	t.Run("the release of the holder frees the lock", func(t *testing.T) {
		t.Parallel()

		env := newLockEnv(t, base)
		env.lockAsAlice(t)
		env.clock.advanceTo(takeoverStart().Add(time.Hour))

		if _, err := env.svc.UnlockLock(t.Context(), env.ws.ID, "lock-1", "alice"); err != nil {
			t.Fatalf("unlock as alice: %v", err)
		}

		env.lockAsBob(t)
	})

	t.Run("a force-unlock frees the lock", func(t *testing.T) {
		t.Parallel()

		env := newLockEnv(t, base)
		env.lockAsAlice(t)
		env.clock.advanceTo(takeoverStart().Add(time.Hour))

		noOverrides := namespace.Overrides{Forced: false, Unlocked: false}
		_, _, err := env.svc.ForceUnlock(t.Context(), env.ws.ID, "admin", noOverrides, nil, forceUnlockEntry())
		if err != nil {
			t.Fatalf("force-unlock: %v", err)
		}

		env.lockAsBob(t)
	})

	t.Run("two concurrent lock acquires: exactly one wins", func(t *testing.T) {
		t.Parallel()

		raceLockAcquires(t, newLockEnv(t, base))
	})

	t.Run("two concurrent state version writes: the stale fence fails", func(t *testing.T) {
		t.Parallel()

		raceStateWrites(t, openTakeoverStore(t, base))
	})
}

// checkHeldAtEachAge locks the workspace of env as alice, then checks that a
// lock as bob returns ErrLocked at each age of the lock.
func checkHeldAtEachAge(t *testing.T, env lockEnv) {
	t.Helper()

	env.lockAsAlice(t)

	for _, age := range []time.Duration{0, 31 * time.Second, time.Hour, 30 * 24 * time.Hour} {
		env.clock.advanceTo(takeoverStart().Add(age))

		_, err := env.svc.LockWithID(t.Context(), env.ws.ID, "bob", "takeover", "lock-2")
		if !errors.Is(err, workspace.ErrLocked) {
			t.Fatalf("lock as bob after %s = %v, want workspace.ErrLocked", age, err)
		}

		env.wantHolder(t, "alice", "lock-1")
	}
}

// raceLockAcquires races two lock acquires on the free workspace of env:
// exactly one takes the lock.
func raceLockAcquires(t *testing.T, env lockEnv) {
	t.Helper()

	ctx := t.Context()
	holders := []struct{ holder, lockID string }{{"alice", "lock-1"}, {"bob", "lock-2"}}
	errs := make([]error, len(holders))
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i, h := range holders {
		wg.Go(func() {
			<-start
			_, errs[i] = env.svc.LockWithID(ctx, env.ws.ID, h.holder, "apply", h.lockID)
		})
	}

	close(start)
	wg.Wait()

	winners := 0
	winner := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			winner = i
		case errors.Is(err, workspace.ErrLocked), errors.Is(err, workspace.ErrConcurrentModification):
		default:
			t.Fatalf("lock as %s = %v, want nil, workspace.ErrLocked or workspace.ErrConcurrentModification",
				holders[i].holder, err)
		}
	}

	if winners != 1 {
		t.Fatalf("winners = %d (errors %v), want 1", winners, errs)
	}

	env.wantHolder(t, holders[winner].holder, holders[winner].lockID)
}

// raceStateWrites races two state version writes with different fences on one
// workspace of s: the write with the stale fence fails and the other commits.
func raceStateWrites(t *testing.T, s *postgres.Store) {
	t.Helper()

	ctx := t.Context()
	ws, fence := seedStateCommitWorkspace(t, s)

	current := pendingStateVersion(ws)
	stale := pendingStateVersion(ws)
	stale.Content = blob.Key("states/pending-stale")
	stale.Serial = 2

	versions := []workspace.StateVersion{current, stale}
	for i := range versions {
		if err := s.CreateStateVersion(ctx, versions[i], fence, 10); err != nil {
			t.Fatalf("CreateStateVersion %s: %v", versions[i].ID, err)
		}
	}

	staleFence := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion - 1}

	var currentErr, staleErr error
	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		currentErr = s.FinalizeStateVersion(ctx, current, fence)
	})
	wg.Go(func() {
		<-start
		staleErr = s.FinalizeStateVersion(ctx, stale, staleFence)
	})

	close(start)
	wg.Wait()

	if currentErr != nil {
		t.Fatalf("FinalizeStateVersion with the current fence: %v", currentErr)
	}

	if !errors.Is(staleErr, namespace.ErrFenced) {
		t.Fatalf("FinalizeStateVersion with the stale fence = %v, want namespace.ErrFenced", staleErr)
	}

	cur, err := s.CurrentStateVersion(ctx, ws.ID)
	if err != nil {
		t.Fatalf("CurrentStateVersion: %v", err)
	}

	if cur.ID != current.ID {
		t.Fatalf("current state version = %s, want %s", cur.ID, current.ID)
	}
}

// takeoverStart returns the time of the clock of the lock environment at its
// start.
func takeoverStart() time.Time {
	return time.UnixMilli(1_700_000_100_000).UTC()
}

func openTakeoverStore(t *testing.T, base string) *postgres.Store {
	t.Helper()

	s, err := postgres.Open(t.Context(), pgtest.MigratedURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// lockEnv is a real workspace service over its own PostgreSQL store, with one
// free workspace and a clock that the test moves. The lock tests and the
// retry test of an upload use it.
type lockEnv struct {
	s     *postgres.Store
	svc   *workspace.Service
	clock *takeoverClock
	ws    workspace.Workspace
}

func newLockEnv(t *testing.T, base string) lockEnv {
	t.Helper()

	s := openTakeoverStore(t, base)
	ws, _ := seedLockWorkspace(t, s)
	clock := &takeoverClock{mu: sync.Mutex{}, now: takeoverStart()}

	namespaces, err := namespace.NewService(namespace.Config{Store: s, Now: clock.Now, Installation: "eu-west"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard: blobguardtest.None{}, ManualLocks: nil, Workspaces: s, States: s, Blobs: blobs,
		Namespaces: namespaces, OutOfScope: workspacetest.NewRefusals(), Sealer: plainSealer{},
		Logger: slog.New(slog.DiscardHandler), Now: clock.Now, MaxStateBytes: 0, MaxPendingVersions: 0,
		Retention:    0,
		OnDeleteWait: nil,
	})
	if err != nil {
		t.Fatalf("workspace service: %v", err)
	}

	return lockEnv{s: s, svc: svc, clock: clock, ws: ws}
}

// lockAsAlice takes the lock of the workspace of e as alice under lock-1.
func (e lockEnv) lockAsAlice(t *testing.T) {
	t.Helper()

	if _, err := e.svc.LockWithID(t.Context(), e.ws.ID, "alice", "apply", "lock-1"); err != nil {
		t.Fatalf("lock as alice: %v", err)
	}
}

// lockAsBob takes the free lock of the workspace of e as bob under lock-2, and
// checks the stored holder.
func (e lockEnv) lockAsBob(t *testing.T) {
	t.Helper()

	if _, err := e.svc.LockWithID(t.Context(), e.ws.ID, "bob", "apply", "lock-2"); err != nil {
		t.Fatalf("lock as bob: %v", err)
	}

	e.wantHolder(t, "bob", "lock-2")
}

// wantHolder fails the test when the stored lock is not lockID of holder.
func (e lockEnv) wantHolder(t *testing.T, holder, lockID string) {
	t.Helper()

	got, err := e.s.GetWorkspaceByID(t.Context(), e.ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspaceByID: %v", err)
	}

	if !got.Lock.Locked() || got.Lock.Holder != holder || got.Lock.ID != lockID {
		t.Fatalf("lock = %+v, want %s/%s", got.Lock, holder, lockID)
	}
}

// takeoverClock is a clock that a test moves.
type takeoverClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *takeoverClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *takeoverClock) advanceTo(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = at
}

// plainSealer is a workspace.Sealer that keeps each content as it is. These
// tests check the store, not the encryption of the state.
type plainSealer struct{}

func (plainSealer) SealState(_ context.Context, _ namespace.Name, _, _ string, r io.Reader) (io.Reader, error) {
	return r, nil
}

func (plainSealer) OpenState(
	_ context.Context, _ namespace.Name, _, _ string, r io.Reader,
) (io.ReadCloser, error) {
	return io.NopCloser(r), nil
}

func (plainSealer) SealOutputs(_ context.Context, _ namespace.Name, _, _ string, b []byte) ([]byte, error) {
	return b, nil
}

func (plainSealer) OpenOutputs(_ context.Context, _ namespace.Name, _, _ string, b []byte) ([]byte, error) {
	return b, nil
}

// forceUnlockEntry is the audit entry of the force-unlock of the test.
func forceUnlockEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "tok-1",
		Action: "unit:write-forced", Object: "opentofu:acme/web", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "tofu/1.11.0",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "req-1", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason: "the run crashed", PrincipalKind: "", JobID: "", Origin: nil,
	}
}
