package workspace_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// TestDefaultMaxStateBytes proves the policy default of ADR 45.
func TestDefaultMaxStateBytes(t *testing.T) {
	t.Parallel()

	if workspace.DefaultMaxStateBytes != 32<<20 {
		t.Fatalf("DefaultMaxStateBytes = %d, want %d", workspace.DefaultMaxStateBytes, 32<<20)
	}
}

// limitBody returns a valid-looking JSON document of exactly size bytes,
// padded with trailing whitespace.
func limitBody(size int) string {
	base := `{"version":4,"serial":1,"lineage":"l1"}`
	if len(base) > size {
		panic("limitBody: size smaller than the base document")
	}

	return base + strings.Repeat(" ", size-len(base))
}

// limitFixture returns a fixture whose service caps state at maxState,
// recording deleted blobs in rec.
func limitFixture(t *testing.T, maxState int64) (fixture, *recordingBlobs) {
	t.Helper()

	rec := &recordingBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, deleted: nil}
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

	secrets := newMemSealer(t, namespaces)
	refusals := workspacetest.NewRefusals()
	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              rec,
		Namespaces:         namespaces,
		OutOfScope:         refusals,
		Sealer:             secrets,
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      maxState,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return fixture{
		svc: svc, store: store, namespaces: namespaces, secrets: secrets, blobs: rec, refusals: refusals,
		now: &now, ws: ws,
		sealKEK: nil, sealKeyID: "",
	}, rec
}

// checkLimitError proves err wraps ErrTooLarge and names the flag, the
// limit and the size read.
func checkLimitError(t *testing.T, err error, wants ...string) {
	t.Helper()

	if err == nil {
		t.Fatal("err = nil, want ErrTooLarge")
	}

	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}

	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q lacks %q", err.Error(), want)
		}
	}
}

// TestUploadTooLargeNamesLimitSizeAndFlag proves a state upload over the
// limit names the limit, the size and the flag, and leaves no blob behind.
func TestUploadTooLargeNamesLimitSizeAndFlag(t *testing.T) {
	t.Parallel()

	f, rec := limitFixture(t, 1024)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	body := limitBody(4096)

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, body), authority(forcedOnly(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(body))
	checkLimitError(t, uploadErr, "--max-state-bytes", "1024", "1025", "state")

	if len(rec.deletedKeys()) == 0 {
		t.Fatal("no blob deleted after the oversized upload")
	}

	stored, err := f.store.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	if stored.Content == "" {
		t.Fatal("pending version names no attempt after the oversized upload")
	}

	if r, getErr := f.blobs.Get(ctx, stored.Content); !errors.Is(getErr, blob.ErrNotFound) {
		if getErr == nil {
			_ = r.Close() //nolint:errcheck // Test cleanup after an unexpected blob.
		}

		t.Fatalf("get of refused blob: err = %v, want ErrNotFound", getErr)
	}
}

// TestDiscardJSONRenderingTooLargeNamesLimitSizeAndFlag proves an oversized
// JSON rendering names the rendering, the limit, the size and the flag.
func TestDiscardJSONRenderingTooLargeNamesLimitSizeAndFlag(t *testing.T) {
	t.Parallel()

	f, _ := limitFixture(t, 1024)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	discardErr := f.svc.DiscardJSONRendering(
		ctx,
		pending.Version.ID,
		pending.Upload,
		strings.NewReader(limitBody(4096)),
	)
	checkLimitError(t, discardErr, "--max-state-bytes", "1024", "1025", "JSON rendering")
}

// TestWriteStateTooLargeNamesLimitSizeAndFlag proves a streamed write over
// the limit names the limit, the size and the flag, and leaves no blob
// behind.
func TestWriteStateTooLargeNamesLimitSizeAndFlag(t *testing.T) {
	t.Parallel()

	f, rec := limitFixture(t, 1024)
	ctx := t.Context()

	if _, err := f.svc.LockWithID(ctx, f.ws.ID, "alice", "apply", "lock-1"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	_, writeErr := f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(limitBody(4096)),
	)
	checkLimitError(t, writeErr, "--max-state-bytes", "1024", "1025", "state")

	if len(rec.deletedKeys()) == 0 {
		t.Fatal("no blob deleted after the oversized streamed write")
	}

	assertNoOrphanBlobs(t, f)
}
