package postgres_test

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

func TestLockAcquireRollsBackOnOutage(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedLockWorkspace(t, s)

	breakWorkspaceWrite(t, s, "break_lock_acquire")

	locked := withTestLock(ws, time.UnixMilli(1_700_000_100_000).UTC())
	err = s.SaveWorkspace(ctx, locked, fence)
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("SaveWorkspace lock = %v, want namespace.ErrUnavailable", err)
	}

	got, getErr := s.GetWorkspaceByID(ctx, ws.ID)
	if getErr != nil {
		t.Fatalf("GetWorkspaceByID: %v", getErr)
	}
	if got.Lock.Locked() {
		t.Fatalf("workspace lock = %+v, want unlocked", got.Lock)
	}

	// The outage ends: the rolled-back acquire left nothing that stops the next one.
	if _, dropErr := postgres.Pool(s).Exec(ctx, `DROP TRIGGER break_lock_acquire ON workspaces`); dropErr != nil {
		t.Fatalf("drop outage trigger: %v", dropErr)
	}

	relocked, err := lockService(t, s).LockWithID(ctx, ws.ID, "alice", "apply", "lock-1")
	if err != nil {
		t.Fatalf("LockWithID after the outage: %v", err)
	}
	if !relocked.Lock.Locked() || relocked.Lock.Holder != "alice" || relocked.Lock.ID != "lock-1" {
		t.Fatalf("lock after the outage = %+v, want alice/lock-1", relocked.Lock)
	}
}

func TestLockAcquire(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedLockWorkspace(t, s)
	at := time.UnixMilli(1_700_000_100_000).UTC()

	if saveErr := s.SaveWorkspace(ctx, withTestLock(ws, at), fence); saveErr != nil {
		t.Fatalf("SaveWorkspace lock: %v", saveErr)
	}

	got, err := s.GetWorkspaceByID(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspaceByID: %v", err)
	}
	if !got.Lock.Locked() || got.Lock.Holder != "alice" || got.Lock.ID != "lock-1" {
		t.Fatalf("lock = %+v, want alice/lock-1", got.Lock)
	}
}

// lockService returns a real workspace service over s, with a fixed clock.
func lockService(t *testing.T, s *postgres.Store) *workspace.Service {
	t.Helper()

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

	return svc
}

// withTestLock returns ws one revision on, locked by alice under lock-1 at at.
func withTestLock(ws workspace.Workspace, at time.Time) workspace.Workspace {
	locked := ws
	locked.Lock = workspace.Lock{LockedAt: at, ID: "lock-1", Holder: "alice", Reason: "reason"}
	locked.Revision++
	locked.UpdatedAt = at

	return locked
}

func seedLockWorkspace(t *testing.T, s *postgres.Store) (workspace.Workspace, namespace.Fence) {
	t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "acme", PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	ws := workspace.Workspace{
		CreatedAt: at, UpdatedAt: at, ID: workspace.ID(ident.NewID("ws-")),
		NamespaceID: ns.ID,
		Ref:         workspace.Ref{Namespace: ns.Name, Name: workspace.Name("web")},
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(), Revision: 1,
		Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
		InCollection: false,
		Origin:       identity.StateOrigin{Job: "", Commit: ""},
	}
	if err := s.CreateWorkspace(t.Context(), ws, ns.Fence()); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	return ws, ns.Fence()
}

func breakWorkspaceWrite(t *testing.T, s *postgres.Store, name string) {
	t.Helper()

	pool := postgres.Pool(s)

	for _, q := range []string{
		`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS
$$ BEGIN RAISE EXCEPTION 'outage' USING ERRCODE='57P01'; END $$`,
		`CREATE TRIGGER ` + name + ` BEFORE UPDATE ON workspaces
FOR EACH ROW EXECUTE FUNCTION ` + name + `()`,
	} {
		if _, err := pool.Exec(t.Context(), q); err != nil {
			t.Fatalf("create outage trigger: %v", err)
		}
	}
}
