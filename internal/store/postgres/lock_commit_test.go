package postgres_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
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

	commit := postgres.NewStateCommitForTest(s, fence)
	lock := workspace.Lock{
		LockedAt: time.UnixMilli(1_700_000_100_000).UTC(), ID: "lock-1", Holder: "alice", Reason: "reason",
	}
	_, err = commit.AcquireLock(ctx, ws.ID, lock)
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("AcquireLock = %v, want namespace.ErrUnavailable", err)
	}

	got, getErr := s.GetWorkspaceByID(ctx, ws.ID)
	if getErr != nil {
		t.Fatalf("GetWorkspaceByID: %v", getErr)
	}
	if got.Lock.Locked() {
		t.Fatalf("workspace lock = %+v, want unlocked", got.Lock)
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

	commit := postgres.NewStateCommitForTest(s, fence)
	lock := workspace.Lock{LockedAt: at, ID: "lock-1", Holder: "alice", Reason: "reason"}
	got, err := commit.AcquireLock(ctx, ws.ID, lock)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if !got.Lock.Locked() || got.Lock.Holder != "alice" || got.Lock.ID != "lock-1" {
		t.Fatalf("lock = %+v, want alice/lock-1", got.Lock)
	}
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
