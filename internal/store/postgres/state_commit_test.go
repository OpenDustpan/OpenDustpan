package postgres_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

func TestStateCommitRollsBackOnOutage(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedStateCommitWorkspace(t, s)

	sv := pendingStateVersion(ws)
	if createErr := s.CreateStateVersion(ctx, sv, fence, 10); createErr != nil {
		t.Fatalf("CreateStateVersion: %v", createErr)
	}

	breakStateCommit(t, s)

	err = s.FinalizeStateVersion(ctx, sv, fence)
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("FinalizeStateVersion = %v, want namespace.ErrUnavailable", err)
	}

	got, getErr := s.GetWorkspaceByID(ctx, ws.ID)
	if getErr != nil {
		t.Fatalf("GetWorkspaceByID: %v", getErr)
	}
	if got.Revision != ws.Revision {
		t.Fatalf("workspace revision = %d, want %d", got.Revision, ws.Revision)
	}
	if _, curErr := s.CurrentStateVersion(ctx, ws.ID); !errors.Is(curErr, workspace.ErrNoState) {
		t.Fatalf("CurrentStateVersion = %v, want ErrNoState", curErr)
	}

	stored, svErr := s.GetStateVersion(ctx, sv.ID)
	if svErr != nil {
		t.Fatalf("GetStateVersion: %v", svErr)
	}
	if stored.Status != workspace.StatusPending {
		t.Fatalf("state version status = %q, want pending", stored.Status)
	}
}

func TestStateCommit(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedStateCommitWorkspace(t, s)

	sv := pendingStateVersion(ws)
	if createErr := s.CreateStateVersion(ctx, sv, fence, 10); createErr != nil {
		t.Fatalf("CreateStateVersion: %v", createErr)
	}

	commit := postgres.NewStateCommitForTest(s, fence)
	if finErr := commit.FinalizeStateVersion(ctx, sv); finErr != nil {
		t.Fatalf("FinalizeStateVersion: %v", finErr)
	}

	cur, err := s.CurrentStateVersion(ctx, ws.ID)
	if err != nil {
		t.Fatalf("CurrentStateVersion: %v", err)
	}
	if cur.ID != sv.ID {
		t.Fatalf("current state version = %s, want %s", cur.ID, sv.ID)
	}
}

func outageBase(t *testing.T) string {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL store tests", envURL)
	}

	return base
}

func seedStateCommitWorkspace(t *testing.T, s *postgres.Store) (workspace.Workspace, namespace.Fence) {
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

	locked := ws
	locked.Lock = workspace.Lock{LockedAt: at, ID: "lock-1", Holder: "alice", Reason: "reason"}
	locked.Revision++
	locked.UpdatedAt = at
	if err := s.SaveWorkspace(t.Context(), locked, ns.Fence()); err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	return locked, ns.Fence()
}

func pendingStateVersion(ws workspace.Workspace) workspace.StateVersion {
	at := time.UnixMilli(1_700_000_100_000).UTC()

	return workspace.StateVersion{
		CreatedAt: at, FinalizedAt: at.Add(time.Minute), UploadExpiresAt: at.Add(time.Hour),
		Upload: token.Digest{}, ID: workspace.StateVersionID(ident.NewID("sv-")), Workspace: ws.ID,
		Holder: "alice", LockID: "lock-1", Lineage: "lineage-1",
		Status: workspace.StatusPending, Content: blob.Key("states/pending"), Outputs: nil,
		MD5: [16]byte{1, 2, 3}, Serial: 1, Size: 1,
		Force: false, UnlockedWrite: false, Sealed: false,
		ClientEncrypted: workspace.ClientEncryptedNo, SensitiveMarks: nil,
		RestoredFrom: "", FenceBy: "",
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

func breakStateCommit(t *testing.T, s *postgres.Store) {
	t.Helper()

	pool := postgres.Pool(s)

	for _, q := range []string{
		`CREATE FUNCTION break_state_commit() RETURNS trigger LANGUAGE plpgsql AS
$$ BEGIN RAISE EXCEPTION 'outage' USING ERRCODE='57P01'; END $$`,
		`CREATE TRIGGER break_state_commit BEFORE UPDATE ON workspaces
FOR EACH ROW EXECUTE FUNCTION break_state_commit()`,
	} {
		if _, err := pool.Exec(t.Context(), q); err != nil {
			t.Fatalf("create outage trigger: %v", err)
		}
	}
}
