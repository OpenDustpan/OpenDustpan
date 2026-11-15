package workspace_test

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/blob/blobfault"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/internal/store/storefault"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// crashFixture runs the workspace service over fault wrappers: SQL through
// sql and blobs through blobs.
type crashFixture struct {
	service *workspace.Service
	sql     *storefault.Store
	blobs   *blobfault.Store
	ws      workspace.Workspace
}

// TestCrashBlobWithoutFinalize fails the finalize after the blob write (ADR
// 26 decision 7). The upload reports the fault, the version stays pending
// with its blob stored, no finalized row names a missing blob, and the retry
// makes it current.
func TestCrashBlobWithoutFinalize(t *testing.T) {
	t.Parallel()

	f := newCrashFixture(t,
		[]storefault.Fault{{Method: "FinalizeStateVersion", Call: 1, Kind: storefault.KindFail, Err: nil, Delay: 0}},
		nil,
	)
	ctx := t.Context()
	content := `{"version":4,"serial":1}`

	pending := beginCrash(t, f.service, f.ws.ID, "alice", newState("lineage-1", 1, content))

	if _, err := f.service.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content)); err == nil ||
		!errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("upload with a faulted finalize: err = %v, want ErrUnavailable", err)
	}

	if n := f.sql.Calls("FinalizeStateVersion"); n != 1 {
		t.Fatalf("FinalizeStateVersion calls = %d, want 1", n)
	}

	if _, err := f.service.Current(ctx, f.ws.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Fatalf("current after a faulted finalize: err = %v, want ErrNoState", err)
	}

	assertContentReferenced(t, f.blobs, f.sql, f.ws.ID)

	current, err := f.service.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("retry after a faulted finalize: %v", err)
	}

	if got := readCrashContent(t, f.service, current); got != content {
		t.Fatalf("current content = %q, want %q", got, content)
	}

	assertContentReferenced(t, f.blobs, f.sql, f.ws.ID)

	if _, err := f.service.Upload(ctx, current.ID, pending.Upload, strings.NewReader(content)); err != nil {
		t.Fatalf("retry of a finalized upload: %v", err)
	}
}

// TestCrashCommittedWithoutDelete fails the delete of the superseded attempt
// after its SQL commit (ADR 26 decision 7). The superseded blob stays, the
// next retry still finalizes, no finalized row names a missing blob, and the
// current content reads back.
func TestCrashCommittedWithoutDelete(t *testing.T) {
	t.Parallel()

	f := newCrashFixture(t,
		[]storefault.Fault{{Method: "FinalizeStateVersion", Call: 1, Kind: storefault.KindFail, Err: nil, Delay: 0}},
		// The service tries each blob delete four times with a backoff
		// (blob.DeleteRetried), so the superseded delete fails only when all
		// four attempts fault.
		[]blobfault.Fault{
			{Method: "Delete", Call: 1, Kind: blobfault.KindFail, Err: nil, Delay: 0},
			{Method: "Delete", Call: 2, Kind: blobfault.KindFail, Err: nil, Delay: 0},
			{Method: "Delete", Call: 3, Kind: blobfault.KindFail, Err: nil, Delay: 0},
			{Method: "Delete", Call: 4, Kind: blobfault.KindFail, Err: nil, Delay: 0},
		},
	)
	ctx := t.Context()
	content := `{"version":4,"serial":1}`

	pending := beginCrash(t, f.service, f.ws.ID, "alice", newState("lineage-1", 1, content))

	if _, err := f.service.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content)); err == nil {
		t.Fatal("upload with a faulted finalize = nil, want an error")
	}

	// The retry records a new attempt, which commits and supersedes the
	// first one; the faulted delete of the first attempt fails the upload.
	if _, err := f.service.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content)); err == nil {
		t.Fatal("upload with a faulted superseded delete = nil, want an error")
	}

	if n := f.blobs.Calls("Delete"); n != 4 {
		t.Fatalf("blob Delete calls = %d, want the 4 retried attempts", n)
	}

	current, err := f.service.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("retry after a faulted delete: %v", err)
	}

	if got := readCrashContent(t, f.service, current); got != content {
		t.Fatalf("current content = %q, want %q", got, content)
	}

	assertContentReferenced(t, f.blobs, f.sql, f.ws.ID)

	// The superseded blob of the first attempt stays: one current blob with
	// one orphan.
	if n := len(listWorkspaceBlobs(t, f.blobs, f.ws.ID)); n != 2 {
		t.Fatalf("stored blobs = %d, want the current one with its superseded one", n)
	}
}

// newCrashFixture wires the workspace service over fault wrappers and leaves
// one workspace locked by alice. sqlFaults and blobFaults fire in the
// wrappers; nil fires nothing.
func newCrashFixture(
	t *testing.T, sqlFaults []storefault.Fault, blobFaults []blobfault.Fault,
) crashFixture {
	t.Helper()

	ctx := t.Context()
	wrappedSQL, innerSQL := wrapCrashSQL(t, sqlFaults)
	wrappedBlobs := wrapCrashBlobs(t, blobFaults)
	namespaces := crashNamespaces(t, wrappedSQL)

	service, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         wrappedSQL,
		States:             wrappedSQL,
		Blobs:              wrappedBlobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             newMemSealer(t, namespaces),
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	ns, err := namespaces.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("namespace of the workspace: %v", err)
	}

	// The fault wrapper does not serve the placed-store port, so a second
	// service over the store under the wrapper makes the workspace in a
	// collection.
	placer, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         innerSQL,
		States:             innerSQL,
		Blobs:              wrappedBlobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             newMemSealer(t, namespaces),
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("placing service: %v", err)
	}

	ws, err := placer.Create(
		ctx, workspace.Ref{Namespace: "acme", Name: name}, placementtest.Workspace(t, innerSQL, ns.Fence()),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	_, err = service.Lock(ctx, ws.ID, "alice", "crash test")
	if err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	return crashFixture{service: service, sql: wrappedSQL, blobs: wrappedBlobs, ws: ws}
}

// wrapCrashSQL opens a fresh SQLite store and wraps it with faults. Cleanup
// closes the wrapper, which closes the store. It also returns the store under
// the wrapper: the wrapper does not serve the placed-store port, so a test
// that makes a workspace in a collection makes it through that store.
func wrapCrashSQL(t *testing.T, faults []storefault.Fault) (*storefault.Store, *sqlite.Store) {
	t.Helper()

	inner, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	wrapped, err := storefault.New(inner, faults...)
	if err != nil {
		t.Fatalf("wrap store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := wrapped.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return wrapped, inner
}

// wrapCrashBlobs opens a filesystem blob store and wraps it with faults.
// Cleanup closes the raw store.
func wrapCrashBlobs(t *testing.T, faults []blobfault.Fault) *blobfault.Store {
	t.Helper()

	raw, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	wrapped, err := blobfault.New(raw, faults...)
	if err != nil {
		t.Fatalf("wrap blob store: %v", err)
	}

	return wrapped
}

// crashNamespaces returns a namespace service holding "acme".
func crashNamespaces(t *testing.T, store *storefault.Store) *namespace.Service {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	_, createErr := namespaces.Create(t.Context(), "acme")
	if createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	return namespaces
}

// beginCrash declares a pending state version for holder.
func beginCrash(
	t *testing.T, service *workspace.Service, id workspace.ID, holder string, in workspace.NewState,
) workspace.PendingState {
	t.Helper()

	pending, err := service.BeginState(t.Context(), id, holder, in, authority(noOverrides(), nil))
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	return pending
}

// readCrashContent returns the stored content of a finalized version.
func readCrashContent(t *testing.T, service *workspace.Service, sv workspace.StateVersion) string {
	t.Helper()

	rc, err := service.Open(t.Context(), sv)
	if err != nil {
		t.Fatalf("open state %s: %v", sv.ID, err)
	}

	data, err := io.ReadAll(rc)
	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close state %s: %v", sv.ID, closeErr)
	}

	if err != nil {
		t.Fatalf("read state %s: %v", sv.ID, err)
	}

	return string(data)
}

// listWorkspaceBlobs returns the stored blobs under the workspace prefix.
func listWorkspaceBlobs(t *testing.T, blobs *blobfault.Store, id workspace.ID) []blob.Object {
	t.Helper()

	return listStateBlobs(t, blobs, id)
}

// assertContentReferenced checks the crash invariant: every blob key SQL
// names opens in the blob store, so no row names a missing blob.
func assertContentReferenced(t *testing.T, blobs *blobfault.Store, s *storefault.Store, id workspace.ID) {
	t.Helper()

	keys, err := s.ReferencedContentKeys(t.Context(), id)
	if err != nil {
		t.Fatalf("referenced keys of %s: %v", id, err)
	}

	for _, key := range keys {
		rc, err := blobs.Get(t.Context(), key)
		if err != nil {
			t.Fatalf("open referenced blob of %s: %v", id, err)
		}

		if err := rc.Close(); err != nil {
			t.Fatalf("close referenced blob of %s: %v", id, err)
		}
	}
}
