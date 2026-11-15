package stack_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/blob/blobfault"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/internal/store/storefault"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// crashFixture runs the update cycle over fault wrappers: SQL through sql and
// blobs through blobs.
type crashFixture struct {
	updates *stack.Updates
	service *stack.Service
	sql     *storefault.Store
	blobs   *blobfault.Store
	ref     stack.Ref
	stackID stack.ID
	id      stack.UpdateID
	tok     token.Token
	nsID    namespace.ID
}

// TestCrashBlobWithoutRow fails the SQL commit after the blob write (ADR 37
// decision 3). The save reports the fault, no row names the orphan blob, and
// the retry adopts the blob and succeeds.
func TestCrashBlobWithoutRow(t *testing.T) {
	t.Parallel()

	f := newCrashFixture(t,
		[]storefault.Fault{{Method: "PutCheckpoint", Call: 1, Kind: storefault.KindFail, Err: nil, Delay: 0}},
		nil,
	)
	ctx := t.Context()
	input := checkpointInput([]byte(`{"resources":[]}`))

	if err := f.updates.SaveCheckpoint(ctx, f.ref, f.id, f.tok, input); !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("save with a faulted commit: err = %v, want ErrUnavailable", err)
	}

	if n := f.sql.Calls("PutCheckpoint"); n != 1 {
		t.Fatalf("PutCheckpoint calls = %d, want 1", n)
	}

	if keys := referencedKeys(t, f.sql, f.ref); len(keys) != 0 {
		t.Fatalf("referenced keys = %v, want none", keys)
	}

	if n := len(listUpdateBlobs(t, f.blobs, f.nsID, f.stackID, f.id)); n != 1 {
		t.Fatalf("stored blobs = %d, want the one orphan", n)
	}

	assertBlobsReferenced(t, f.blobs, f.sql, f.ref)

	if err := f.updates.SaveCheckpoint(ctx, f.ref, f.id, f.tok, input); err != nil {
		t.Fatalf("retry after a faulted commit: %v", err)
	}

	assertBlobsReferenced(t, f.blobs, f.sql, f.ref)

	if got := exportDeployment(t, f.updates, f.ref); !bytes.Equal(got, input.Deployment) {
		t.Fatalf("export = %q, want %q", got, input.Deployment)
	}
}

// TestCrashRowWithoutDelete fails every blob delete after the SQL commit
// (ADR 37 decision 3). Each save still succeeds, the superseded blob stays,
// no row names a missing blob, and the next save succeeds.
func TestCrashRowWithoutDelete(t *testing.T) {
	t.Parallel()

	f := newCrashFixture(t,
		nil,
		[]blobfault.Fault{{Method: "Delete", Call: 0, Kind: blobfault.KindFail, Err: nil, Delay: 0}},
	)
	ctx := t.Context()

	first := checkpointInput([]byte(`{"resources":[]}`))
	second := checkpointInput([]byte(`{"resources":[{"urn":"urn:pulumi:prod::web::x"}]}`))

	if err := f.updates.SaveCheckpoint(ctx, f.ref, f.id, f.tok, first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	if err := f.updates.SaveCheckpoint(ctx, f.ref, f.id, f.tok, second); err != nil {
		t.Fatalf("second save with a faulted delete: %v", err)
	}

	if n := f.blobs.Calls("Delete"); n < 1 {
		t.Fatalf("blob Delete calls = %d, want at least one faulted delete", n)
	}

	if got := exportDeployment(t, f.updates, f.ref); !bytes.Equal(got, second.Deployment) {
		t.Fatalf("export = %q, want the second save %q", got, second.Deployment)
	}

	if n := len(listUpdateBlobs(t, f.blobs, f.nsID, f.stackID, f.id)); n != 2 {
		t.Fatalf("stored blobs = %d, want the current one with its superseded one", n)
	}

	assertBlobsReferenced(t, f.blobs, f.sql, f.ref)

	third := checkpointInput([]byte(`{"resources":[{"urn":"urn:pulumi:prod::web::y"}]}`))

	if err := f.updates.SaveCheckpoint(ctx, f.ref, f.id, f.tok, third); err != nil {
		t.Fatalf("third save with a faulted delete: %v", err)
	}

	assertBlobsReferenced(t, f.blobs, f.sql, f.ref)

	if got := exportDeployment(t, f.updates, f.ref); !bytes.Equal(got, third.Deployment) {
		t.Fatalf("export = %q, want the third save %q", got, third.Deployment)
	}
}

// newCrashFixture wires the update cycle over fault wrappers and leaves one
// started update ready for checkpoint saves. sqlFaults and blobFaults fire in
// the wrappers; nil fires nothing.
func newCrashFixture(
	t *testing.T, sqlFaults []storefault.Fault, blobFaults []blobfault.Fault,
) crashFixture {
	t.Helper()

	ctx := t.Context()
	wrappedSQL, innerSQL := openCrashSQL(t, sqlFaults)
	wrappedBlobs := wrapCrashBlobs(t, blobFaults)
	namespaces := crashNamespaces(t, wrappedSQL)

	service, err := stack.NewService(
		stack.Config{Store: wrappedSQL, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stacks: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	ns, err := namespaces.Get(ctx, ref.Namespace)
	if err != nil {
		t.Fatalf("namespace of the stack: %v", err)
	}

	// The fault wrapper does not serve the placed-store port, so a second
	// service over the store under the wrapper makes the stack in a collection.
	placer, err := stack.NewService(
		stack.Config{Store: innerSQL, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("placing stacks: %v", err)
	}

	created, err := placer.CreatePlaced(ctx, ref, nil, placementtest.Stack(t, innerSQL, ns.Fence()))
	if err != nil {
		t.Fatalf("create stack: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            wrappedSQL,
		Tokens:             nil,
		Checkpoints:        wrappedSQL,
		Events:             wrappedSQL,
		Namespaces:         namespaces,
		Stacks:             service,
		Blobs:              wrappedBlobs,
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates: %v", err)
	}

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

	u, err := updates.Create(ctx, ref, plan)
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	started, err := updates.Start(ctx, ref, u.ID)
	if err != nil {
		t.Fatalf("start update: %v", err)
	}

	return crashFixture{
		updates: updates,
		service: service,
		sql:     wrappedSQL,
		blobs:   wrappedBlobs,
		ref:     ref,
		stackID: created.ID,
		id:      u.ID,
		tok:     started.Token,
		nsID:    ns.ID,
	}
}

// wrapCrashSQL opens a fresh SQLite store and wraps it with faults. Cleanup
// closes the wrapper, which closes the store.
func wrapCrashSQL(t *testing.T, faults []storefault.Fault) *storefault.Store {
	t.Helper()

	wrapped, _ := openCrashSQL(t, faults)

	return wrapped
}

// openCrashSQL is wrapCrashSQL that also returns the store under the wrapper.
// The wrapper does not serve the placed-store port, so a test that makes a
// stack in a collection makes it through the store under the wrapper.
func openCrashSQL(t *testing.T, faults []storefault.Fault) (*storefault.Store, *sqlite.Store) {
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

// checkpointInput returns an unsequenced checkpoint input holding deployment.
func checkpointInput(deployment []byte) stack.CheckpointInput {
	return stack.CheckpointInput{
		Features:      nil,
		Deployment:    deployment,
		SchemaVersion: 1,
		Sequence:      0,
		Invalid:       false,
	}
}

// referencedKeys returns the blob keys every checkpoint of ref names.
func referencedKeys(t *testing.T, s *storefault.Store, ref stack.Ref) []blob.Key {
	t.Helper()

	st, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", ref, err)
	}

	keys, err := s.ReferencedBlobKeys(t.Context(), st.ID)
	if err != nil {
		t.Fatalf("referenced keys of %s: %v", ref, err)
	}

	return keys
}

// listUpdateBlobs returns the stored blobs of one update.
func listUpdateBlobs(
	t *testing.T, blobs *blobfault.Store, ns namespace.ID, stackID stack.ID, id stack.UpdateID,
) []blob.Object {
	t.Helper()

	prefix := "namespaces/" + ns.String() + "/pulumi/" + stackID.String() + "/" + id.String() + "/"

	listed, err := blobtest.ListAll(t.Context(), blobs, prefix)
	if err != nil {
		t.Fatalf("list blobs of update %s: %v", id, err)
	}

	return listed
}

// assertBlobsReferenced checks the crash invariant: every blob key SQL names
// opens in the blob store, so no row names a missing blob.
func assertBlobsReferenced(t *testing.T, blobs *blobfault.Store, s *storefault.Store, ref stack.Ref) {
	t.Helper()

	for _, key := range referencedKeys(t, s, ref) {
		rc, err := blobs.Get(t.Context(), key)
		if err != nil {
			t.Fatalf("open referenced blob of %s: %v", ref, err)
		}

		if err := rc.Close(); err != nil {
			t.Fatalf("close referenced blob of %s: %v", ref, err)
		}
	}
}

// exportDeployment returns the deployment the stack currently exports.
func exportDeployment(t *testing.T, updates *stack.Updates, ref stack.Ref) []byte {
	t.Helper()

	cp, err := updates.Export(t.Context(), ref)
	if err != nil {
		t.Fatalf("export %s: %v", ref, err)
	}

	return cp.Deployment
}
