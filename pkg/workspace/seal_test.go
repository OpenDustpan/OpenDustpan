package workspace_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// sealFixture holds one sealing service over its stores, so tests push
// sealed rows into one workspace and prove what the calls around them do.
type sealFixture struct {
	store      *memStore
	namespaces *namespace.Service
	blobs      blob.Store
	sealed     *workspace.Service
	now        *time.Time
	ws         workspace.Workspace
}

func newSealFixture(t *testing.T) sealFixture {
	t.Helper()

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	acme := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           noOverrides(),
	}

	if err := store.CreateNamespace(t.Context(), acme); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	blobs := openFixtureBlobs(t)

	secrets, err := secret.NewService(secret.Config{
		Keys: newSealedKeys(), Wrapper: newSealedWrapper(t), Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	sealed, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             secrets,
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("sealed service: %v", err)
	}

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := sealed.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return sealFixture{
		store:      store,
		namespaces: namespaces,
		blobs:      blobs,
		sealed:     sealed,
		now:        &now,
		ws:         ws,
	}
}

// pushSealed locks the fixture workspace and uploads content with outputs
// through the sealing service.
func (f sealFixture) pushSealed(t *testing.T, content string, outputs []byte, serial int64) {
	t.Helper()

	ctx := t.Context()

	if _, err := f.sealed.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.sealed.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     sum(content),
		Outputs: outputs,
		Serial:  serial,
		Force:   false,
	}, authority(noOverrides(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	sv, err := f.sealed.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if _, err := f.sealed.Unlock(ctx, f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	if !sv.Sealed {
		t.Fatal("upload through the sealing service reads unsealed, want sealed")
	}
}
