package workspace_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// sealFixture holds one sealing service and one service without a sealer
// over the same stores, so tests write sealed and plain rows into one
// workspace and prove which calls need the sealer.
type sealFixture struct {
	store      *memStore
	namespaces *namespace.Service
	blobs      blob.Store
	plain      *workspace.Service
	sealed     *workspace.Service
	now        *time.Time
	ws         workspace.Workspace
}

func newSealFixture(t *testing.T) sealFixture {
	t.Helper()

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	acme := namespace.Namespace{
		CreatedAt:       now,
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: 1,
		Overrides:       noOverrides(),
	}

	if err := store.CreateNamespace(t.Context(), acme); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	blobs := openFixtureBlobs(t)

	plain, err := workspace.NewService(workspace.Config{
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		Sealer:             nil,
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("plain service: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: newSealedKeys(), Wrapper: newSealedWrapper(t), Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	sealed, err := workspace.NewService(workspace.Config{
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
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

	ws, err := plain.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return sealFixture{
		store:      store,
		namespaces: namespaces,
		blobs:      blobs,
		plain:      plain,
		sealed:     sealed,
		now:        &now,
		ws:         ws,
	}
}

// pushSealed locks the fixture workspace and uploads content with outputs
// through the sealing service.
func (f sealFixture) pushSealed(t *testing.T, content string, outputs []byte, serial int64) {
	t.Helper()

	if sv := f.pushWith(t, f.sealed, content, outputs, serial); !sv.Sealed {
		t.Fatal("upload through the sealing service reads unsealed, want sealed")
	}
}

// pushPlain locks the fixture workspace and uploads content through the
// service without a sealer, as a server before the seal change did.
func (f sealFixture) pushPlain(t *testing.T, content string, serial int64) {
	t.Helper()

	if sv := f.pushWith(t, f.plain, content, nil, serial); sv.Sealed {
		t.Fatal("upload through the plain service reads sealed, want unsealed")
	}
}

// pushWith locks the fixture workspace and uploads content with outputs
// through svc, returning the finalized version.
func (f sealFixture) pushWith(
	t *testing.T, svc *workspace.Service, content string, outputs []byte, serial int64,
) workspace.StateVersion {
	t.Helper()

	ctx := t.Context()

	if _, err := f.plain.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := svc.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     sum(content),
		Outputs: outputs,
		Serial:  serial,
		Force:   false,
	}, authority(noOverrides(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	sv, err := svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if _, err := f.plain.Unlock(ctx, f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	return sv
}
