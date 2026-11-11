package stack_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// deleteEmptyDeployment holds no managed resources: only the automatic
// stack resource.
const deleteEmptyDeployment = `{"resources":[` +
	`{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev","type":"pulumi:pulumi:Stack"}]}`

// deleteResourcedDeployment holds the automatic stack resource and one
// managed resource, so the stack counts as having resources.
const deleteResourcedDeployment = `{"resources":[` +
	`{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev","type":"pulumi:pulumi:Stack"},` +
	`{"urn":"urn:pulumi:dev::web::custom:resource:Type::res","type":"custom:resource:Type"}]}`

// errDeleteCheckpointBoom fails checkpoint reads in the delete-guard tests.
var errDeleteCheckpointBoom = errors.New("checkpoint boom")

// deleteState is a scripted stack.LatestState for the delete-guard tests:
// each Export answers the next queued result, repeating the last one.
type deleteState struct {
	mu    sync.Mutex
	queue []deleteExportResult
	calls int
}

type deleteExportResult struct {
	cp  stack.Checkpoint
	err error
}

// exportedCheckpoint scripts an Export that returns cp.
func exportedCheckpoint(cp stack.Checkpoint) deleteExportResult {
	return deleteExportResult{cp: cp, err: nil}
}

// failedExport scripts an Export that returns err with no checkpoint.
func failedExport(err error) deleteExportResult {
	var cp stack.Checkpoint

	return deleteExportResult{cp: cp, err: err}
}

// unreadableExport scripts an Export that fails on the blob content, as the
// real Export does: it still returns the stored row identity (Update and
// Digest) with no deployment, so the guard can name the checkpoint it could
// not read.
func unreadableExport(cp stack.Checkpoint, err error) deleteExportResult {
	cp.Deployment = nil

	return deleteExportResult{cp: cp, err: err}
}

func (m *deleteState) Export(_ context.Context, _ stack.Ref) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++

	if len(m.queue) == 0 {
		var cp stack.Checkpoint

		return cp, nil
	}

	if m.calls <= len(m.queue) {
		res := m.queue[m.calls-1]

		return res.cp, res.err
	}

	res := m.queue[len(m.queue)-1]

	return res.cp, res.err
}

// deleteStacks is a stack.Store for the delete-guard tests that records the
// guards DeleteStack receives. Queued errors answer first; once the queue is
// empty it answers from the guard itself, as the real adapters do: a guard
// that counted resources refuses with ErrHasResources, otherwise the delete
// succeeds. No method touches a blob: the service counts outside the
// transaction and the store only compares the guard.
type deleteStacks struct {
	mu     sync.Mutex
	guards []stack.DeleteGuard
	queue  []error
	// restoreCalls counts the calls of RestoreStack.
	restoreCalls int
}

func (m *deleteStacks) DeleteStack(
	_ context.Context, _ stack.Ref, _ namespace.Fence, guard stack.DeleteGuard, _ inventory.Deletion,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.guards = append(m.guards, guard)

	if len(m.queue) > 0 {
		err := m.queue[0]
		m.queue = m.queue[1:]

		return err
	}

	if guard.HasResources {
		return stack.ErrHasResources
	}

	return nil
}

func (m *deleteStacks) CreateStack(_ context.Context, _ stack.Stack, _ namespace.Fence) error {
	return stack.ErrAlreadyExists
}

func (m *deleteStacks) GetStack(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	return stack.Stack{
		CreatedAt:   time.Time{},
		UpdatedAt:   time.Time{},
		Tags:        nil,
		Ref:         ref,
		ID:          "stk-1",
		NamespaceID: "ns-1",
		Version:     0,
	}, nil
}

func (m *deleteStacks) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

// QueryStacks implements stack.Store over the lists of ListStacks.
func (m *deleteStacks) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, m.ListStacks, q)
}

func (m *deleteStacks) ForceDeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ inventory.Deletion,
) error {
	return nil
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (m *deleteStacks) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.restoreCalls++

	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (*deleteStacks) PurgeStack(_ context.Context, _ stack.ID, _ namespace.Fence, _ time.Time, _ audit.Entry) error {
	return stack.ErrNotFound
}

func (m *deleteStacks) UpdateStackTags(
	_ context.Context, _ stack.Ref, _ map[string]string, _ time.Time, _ namespace.Fence,
) error {
	return stack.ErrNotFound
}

func (m *deleteStacks) RenameStack(
	_ context.Context, _ stack.Ref, _ stack.Name, _ namespace.Fence,
) error {
	return stack.ErrNotFound
}

// restores returns the number of RestoreStack calls.
func (m *deleteStacks) restores() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.restoreCalls
}

func (m *deleteStacks) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.guards)
}

func (m *deleteStacks) guardAt(i int) stack.DeleteGuard {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.guards[i]
}

// deleteFixture holds a stack.Service with scripted latest state and store,
// and the reference it deletes.
type deleteFixture struct {
	svc   *stack.Service
	state *deleteState
	store *deleteStacks
	ref   stack.Ref
}

func newDeleteFixture(
	t *testing.T, exports []deleteExportResult, queued []error,
) deleteFixture {
	t.Helper()

	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	namespaces, err := namespace.NewService(
		namespace.Config{
			Store:        &memNamespaces{ns: ns, reads: atomic.Int64{}},
			Now:          func() time.Time { return now },
			Installation: "local",
		},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	st := &deleteState{mu: sync.Mutex{}, queue: exports, calls: 0}
	store := &deleteStacks{mu: sync.Mutex{}, guards: nil, queue: queued, restoreCalls: 0}

	svc, err := stack.NewService(stack.Config{
		Store:       store,
		Namespaces:  namespaces,
		Now:         func() time.Time { return now },
		State:       st,
		Retention:   0,
		ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return deleteFixture{svc: svc, state: st, store: store, ref: ref}
}

// deleteInlineCheckpoint returns an exported checkpoint of deployment: the
// resolved bytes the guard counts, with the digest naming them.
func deleteInlineCheckpoint(ref stack.Ref, id, deployment string) stack.Checkpoint {
	return stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt:       time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC),
		Features:      nil,
		Deployment:    []byte(deployment),
		Update:        stack.UpdateID(id),
		Ref:           ref,
		Lease:         token.Digest{},
		SchemaVersion: 3,
		Sequence:      0,
		Digest:        sha256.Sum256([]byte(deployment)),
		Invalid:       false,
		BlobKey:       "",
		BlobSize:      0,
		BlobMD5:       [16]byte{},

		AttemptStartedAt: time.Time{}, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// deleteSequencedExport returns the export of a sequenced save of deployment:
// the inner deployment the guard counts, with the digest naming the stored
// versioned envelope, as the real export does.
func deleteSequencedExport(ref stack.Ref, id, deployment string) stack.Checkpoint {
	envelope := `{"version":3,"deployment":` + deployment + `}`
	cp := deleteInlineCheckpoint(ref, id, deployment)
	cp.Digest = sha256.Sum256([]byte(envelope))
	cp.Sequence = 2

	return cp
}

// deleteUnreadableRow returns the stored row of a checkpoint whose blob the
// export could not read: only its identity, with no deployment.
func deleteUnreadableRow(ref stack.Ref, id, deployment string) stack.Checkpoint {
	cp := deleteInlineCheckpoint(ref, id, deployment)
	cp.Deployment = nil

	return cp
}

func TestDeleteServiceAllowsEmptyInlineCheckpoint(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{exportedCheckpoint(deleteInlineCheckpoint(ref, "u1", deleteEmptyDeployment))},
		nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); err != nil {
		t.Fatalf("delete a stack with no resources: %v", err)
	}

	if got := f.store.calls(); got != 1 {
		t.Fatalf("store delete calls = %d, want 1: a refusal must not retry", got)
	}

	guard := f.store.guardAt(0)
	if !guard.HasCheckpoint || guard.HasResources || guard.UpdateID != "u1" ||
		guard.Digest != sha256.Sum256([]byte(deleteEmptyDeployment)) {
		t.Fatalf("guard = %+v, want the counted empty checkpoint", guard)
	}
}

func TestDeleteServiceRefusesResourcedInlineCheckpoint(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{exportedCheckpoint(deleteInlineCheckpoint(ref, "u1", deleteResourcedDeployment))},
		nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete a stack with resources: err = %v, want ErrHasResources", err)
	}

	if got := f.store.calls(); got != 1 {
		t.Fatalf("store delete calls = %d, want 1: a refusal must not retry", got)
	}

	if guard := f.store.guardAt(0); !guard.HasCheckpoint || !guard.HasResources {
		t.Fatalf("guard = %+v, want HasCheckpoint and HasResources", guard)
	}
}

func TestDeleteServiceWithoutCheckpointPassesEmptyGuard(t *testing.T) {
	t.Parallel()

	f := newDeleteFixture(t,
		[]deleteExportResult{failedExport(stack.ErrNoState)},
		nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); err != nil {
		t.Fatalf("delete a stack with no checkpoint: %v", err)
	}

	if got := f.store.calls(); got != 1 {
		t.Fatalf("store delete calls = %d, want 1", got)
	}

	if guard := f.store.guardAt(0); guard != stack.EmptyGuard() {
		t.Fatalf("guard = %+v, want EmptyGuard", guard)
	}

	if got := f.state.calls; got != 1 {
		t.Fatalf("state reads = %d, want 1", got)
	}
}

func TestDeleteServiceWithoutStatePassesEmptyGuard(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	namespaces, err := namespace.NewService(
		namespace.Config{
			Store:        &memNamespaces{ns: ns, reads: atomic.Int64{}},
			Now:          func() time.Time { return now },
			Installation: "local",
		},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	st := &deleteStacks{mu: sync.Mutex{}, guards: nil, queue: nil, restoreCalls: 0}

	svc, err := stack.NewService(stack.Config{
		Store:       st,
		Namespaces:  namespaces,
		Now:         func() time.Time { return now },
		State:       nil,
		Retention:   0,
		ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	if err := svc.Delete(t.Context(), ref, deleteEntry()); err != nil {
		t.Fatalf("delete without latest state: %v", err)
	}

	if got := st.calls(); got != 1 {
		t.Fatalf("store delete calls = %d, want 1", got)
	}

	if guard := st.guardAt(0); guard != stack.EmptyGuard() {
		t.Fatalf("guard = %+v, want EmptyGuard", guard)
	}
}

func TestDeleteServiceRetriesStaleGuardOnce(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{
			exportedCheckpoint(deleteInlineCheckpoint(ref, "u1", deleteEmptyDeployment)),
			exportedCheckpoint(deleteInlineCheckpoint(ref, "u2", deleteResourcedDeployment)),
		},
		[]error{stack.ErrConcurrentModification})

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete after a save raced it: err = %v, want ErrHasResources", err)
	}

	if got := f.store.calls(); got != 2 {
		t.Fatalf("store delete calls = %d, want 2: one recount, then decide", got)
	}

	first := f.store.guardAt(0)
	if first.UpdateID != "u1" || first.HasResources {
		t.Fatalf("first guard = %+v, want the stale empty count of u1", first)
	}

	second := f.store.guardAt(1)
	if second.UpdateID != "u2" || !second.HasResources {
		t.Fatalf("second guard = %+v, want the fresh resourced count of u2", second)
	}
}

func TestDeleteServiceStaleGuardThenEmptyDeletes(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{
			exportedCheckpoint(deleteInlineCheckpoint(ref, "u1", deleteResourcedDeployment)),
			exportedCheckpoint(deleteInlineCheckpoint(ref, "u2", deleteEmptyDeployment)),
		},
		[]error{stack.ErrConcurrentModification})

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); err != nil {
		t.Fatalf("delete after the race dropped resources: %v", err)
	}

	if got := f.store.calls(); got != 2 {
		t.Fatalf("store delete calls = %d, want 2: one recount, then decide", got)
	}

	if second := f.store.guardAt(1); second.UpdateID != "u2" || second.HasResources {
		t.Fatalf("second guard = %+v, want the fresh empty count of u2", second)
	}
}

func TestDeleteServiceDoubleRaceReportsConflict(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{
			exportedCheckpoint(deleteInlineCheckpoint(ref, "u1", deleteEmptyDeployment)),
			exportedCheckpoint(deleteInlineCheckpoint(ref, "u2", deleteEmptyDeployment)),
		},
		[]error{stack.ErrConcurrentModification, stack.ErrConcurrentModification})

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); !errors.Is(err, stack.ErrConcurrentModification) {
		t.Fatalf("delete while two saves race it: err = %v, want ErrConcurrentModification", err)
	}

	if got := f.store.calls(); got != 2 {
		t.Fatalf("store delete calls = %d, want 2: the service recounts once, then stops", got)
	}
}

func TestDeleteServiceCheckpointErrorFailsDelete(t *testing.T) {
	t.Parallel()

	f := newDeleteFixture(t, []deleteExportResult{failedExport(errDeleteCheckpointBoom)}, nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); !errors.Is(err, errDeleteCheckpointBoom) {
		t.Fatalf("delete with an unreadable checkpoint: err = %v, want the checkpoint error", err)
	}

	if got := f.store.calls(); got != 0 {
		t.Fatalf("store delete calls = %d, want 0: no count means no delete", got)
	}
}

// TestDeleteServiceUnavailableContentCountsAsResourced proves the fail-safe:
// a checkpoint whose blob cannot be read refuses the delete instead of
// failing it, naming the stored row it could not read.
func TestDeleteServiceUnavailableContentCountsAsResourced(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{
			unreadableExport(
				deleteUnreadableRow(ref, "u1", deleteEmptyDeployment),
				fmt.Errorf("export %s: %w", ref, stack.ErrCheckpointUnavailable),
			),
		},
		nil)

	err = f.svc.Delete(t.Context(), f.ref, deleteEntry())
	if !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete with an unreadable blob: err = %v, want ErrHasResources", err)
	}

	if errors.Is(err, stack.ErrCheckpointUnavailable) {
		t.Fatalf("delete with an unreadable blob leaked the content error: %v", err)
	}

	if got := f.store.calls(); got != 1 {
		t.Fatalf("store delete calls = %d, want 1: a refusal must not retry", got)
	}

	guard := f.store.guardAt(0)
	if !guard.HasCheckpoint || !guard.HasResources || guard.UpdateID != "u1" ||
		guard.Digest != sha256.Sum256([]byte(deleteEmptyDeployment)) {
		t.Fatalf("guard = %+v, want the fail-closed identity of u1", guard)
	}
}

// TestDeleteServiceMismatchedContentCountsAsResourced proves the fail-safe
// for stored bytes that do not match their record: corrupt, oversized or
// foreign content refuses the delete instead of failing it.
func TestDeleteServiceMismatchedContentCountsAsResourced(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{
			unreadableExport(
				deleteUnreadableRow(ref, "u1", deleteEmptyDeployment),
				fmt.Errorf("export %s: %w", ref, stack.ErrCheckpointMismatch),
			),
		},
		nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete with mismatched content: err = %v, want ErrHasResources", err)
	}

	if guard := f.store.guardAt(0); !guard.HasCheckpoint || !guard.HasResources || guard.UpdateID != "u1" {
		t.Fatalf("guard = %+v, want fail-closed HasResources naming u1", guard)
	}
}

func TestDeleteServiceGuardsNameTheCountedCheckpoint(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	deployment := deleteEmptyDeployment
	f := newDeleteFixture(t,
		[]deleteExportResult{
			exportedCheckpoint(deleteInlineCheckpoint(ref, "u7", deployment)),
		},
		nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); err != nil {
		t.Fatalf("delete an empty stack: %v", err)
	}

	guard := f.store.guardAt(0)
	if guard.UpdateID != "u7" {
		t.Fatalf("guard update = %q, want u7", guard.UpdateID)
	}

	if guard.Digest != sha256.Sum256([]byte(deployment)) {
		t.Fatalf("guard digest = %x, want the digest of the counted checkpoint", guard.Digest)
	}
}

// TestDeleteServiceRefusesSequencedEnvelopeWithResources proves the guard
// counts the inner deployment of a sequenced save, not the versioned
// envelope that holds it: the envelope has no resources array, so counting
// it would read 0 and allow removing a stack that still manages resources.
// The guard still names the stored envelope identity for the
// compare-and-swap check.
func TestDeleteServiceRefusesSequencedEnvelopeWithResources(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{
			exportedCheckpoint(deleteSequencedExport(ref, "u1", deleteResourcedDeployment)),
		},
		nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete a sequenced stack with resources: err = %v, want ErrHasResources", err)
	}

	guard := f.store.guardAt(0)
	if !guard.HasCheckpoint || !guard.HasResources {
		t.Fatalf("guard = %+v, want the unwrapped resourced count", guard)
	}

	envelope := `{"version":3,"deployment":` + deleteResourcedDeployment + `}`
	if guard.Digest != sha256.Sum256([]byte(envelope)) {
		t.Fatalf("guard digest = %x, want the stored envelope digest", guard.Digest)
	}
}

// TestDeleteServiceAllowsSequencedEnvelopeWithoutResources proves the guard
// allows deleting a stack whose sequenced save manages no resources.
func TestDeleteServiceAllowsSequencedEnvelopeWithoutResources(t *testing.T) {
	t.Parallel()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	f := newDeleteFixture(t,
		[]deleteExportResult{
			exportedCheckpoint(deleteSequencedExport(ref, "u1", deleteEmptyDeployment)),
		},
		nil)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); err != nil {
		t.Fatalf("delete a sequenced stack with no resources: %v", err)
	}

	if guard := f.store.guardAt(0); !guard.HasCheckpoint || guard.HasResources {
		t.Fatalf("guard = %+v, want the unwrapped empty count", guard)
	}
}

// sequencedDeleteFixture wires the real update cycle over in-memory stores
// to a stack Service with a recording store, so a test saves a checkpoint
// through the real save path and deletes through the real guard.
type sequencedDeleteFixture struct {
	updates *stack.Updates
	svc     *stack.Service
	store   *deleteStacks
	ref     stack.Ref
}

func newSequencedDeleteFixture(t *testing.T) sequencedDeleteFixture {
	t.Helper()

	return newSequencedDeleteFixtureWithLimit(t, 1024)
}

func newSequencedDeleteFixtureWithLimit(t *testing.T, limit int) sequencedDeleteFixture {
	t.Helper()

	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	namespaces, err := namespace.NewService(
		namespace.Config{
			Store:        &memNamespaces{ns: ns, reads: atomic.Int64{}},
			Now:          func() time.Time { return now },
			Installation: "local",
		},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	mem := newMemUpdates()
	blobs := newMemBlob(newClock())

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            mem,
		Tokens:             nil,
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             mem,
		Blobs:              blobs,
		Refusals:           stacktest.NewRefusals(),
		Now:                func() time.Time { return now },
		Logger:             nil,
		MaxCheckpointBytes: limit,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	mem.nsID = ns.ID
	mem.addStack(ref, "stk-test")

	st := &deleteStacks{mu: sync.Mutex{}, guards: nil, queue: nil, restoreCalls: 0}

	svc, err := stack.NewService(stack.Config{
		Store:       st,
		Namespaces:  namespaces,
		Now:         func() time.Time { return now },
		State:       updates,
		Retention:   0,
		ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return sequencedDeleteFixture{updates: updates, svc: svc, store: st, ref: ref}
}

// saveSequencedVerbatimThenDelta saves base as a verbatim save with sequence
// 1 and final as a delta with sequence 2, as the CLI does, and completes the
// update.
func saveSequencedVerbatimThenDelta(
	t *testing.T, f sequencedDeleteFixture, base, final string,
) {
	t.Helper()

	ctx := t.Context()

	u, err := f.updates.Create(
		ctx, f.ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	started, err := f.updates.Start(ctx, f.ref, u.ID)
	if err != nil {
		t.Fatalf("start update: %v", err)
	}

	if saveErr := f.updates.SaveCheckpoint(ctx, f.ref, u.ID, started.Token, checkpoint(1, base)); saveErr != nil {
		t.Fatalf("save verbatim checkpoint: %v", saveErr)
	}

	// The delta edits the saved envelope text: both share the envelope
	// prefix and suffix, so one edit replaces the inner deployment.
	const prefix = `{"version":3,"deployment":`

	edits := "[" + editJSON(len(prefix), len(base)-len(`}`), final[len(prefix):len(final)-len(`}`)]) + "]"
	deltaErr := f.updates.SaveCheckpointDelta(ctx, f.ref, u.ID, started.Token, deltaOf(2, final, edits))
	if deltaErr != nil {
		t.Fatalf("save checkpoint delta: %v", deltaErr)
	}

	_, completeErr := f.updates.Complete(ctx, f.ref, u.ID, started.Token, stack.StatusSucceeded)
	if completeErr != nil {
		t.Fatalf("complete update: %v", completeErr)
	}
}

// savePlainCheckpoint saves deployment as an unsequenced save, as the plain
// checkpoint route does, and completes the update.
func savePlainCheckpoint(t *testing.T, f sequencedDeleteFixture, deployment string) {
	t.Helper()

	ctx := t.Context()

	u, err := f.updates.Create(
		ctx, f.ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	started, err := f.updates.Start(ctx, f.ref, u.ID)
	if err != nil {
		t.Fatalf("start update: %v", err)
	}

	saveErr := f.updates.SaveCheckpoint(ctx, f.ref, u.ID, started.Token, checkpoint(0, deployment))
	if saveErr != nil {
		t.Fatalf("save checkpoint: %v", saveErr)
	}

	_, completeErr := f.updates.Complete(ctx, f.ref, u.ID, started.Token, stack.StatusSucceeded)
	if completeErr != nil {
		t.Fatalf("complete update: %v", completeErr)
	}
}

// checkDeleteProvesCount deletes ref and proves the guard counted inner:
// the export holds inner, and the delete answers wantErr: stack
// ErrHasResources when inner manages resources, else nil.
func checkDeleteProvesCount(t *testing.T, f sequencedDeleteFixture, inner string, wantErr error) {
	t.Helper()

	exported, err := f.updates.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	if got := string(exported.Deployment); got != inner {
		t.Fatalf("export = %q, want the inner deployment %q", got, inner)
	}

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); !errors.Is(err, wantErr) {
		t.Fatalf("delete: err = %v, want %v", err, wantErr)
	}

	if got := f.store.calls(); got != 1 {
		t.Fatalf("store delete calls = %d, want 1: a refusal must not retry", got)
	}

	guard := f.store.guardAt(0)
	if !guard.HasCheckpoint || guard.HasResources != (wantErr != nil) {
		t.Fatalf("guard = %+v, want the counted checkpoint", guard)
	}
}

// TestDeleteAfterVerbatimThenDelta saves a stack's checkpoint through the
// sequenced path, verbatim then delta, and proves `stack rm` without force
// refuses a stack whose inner deployment manages resources and allows an
// empty one.
func TestDeleteAfterVerbatimThenDelta(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		inner   string
		wantErr error
	}{
		{name: "empty", inner: `{"resources":[]}`, wantErr: nil},
		{name: "resourced", inner: deleteResourcedDeployment, wantErr: stack.ErrHasResources},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newSequencedDeleteFixture(t)
			base := `{"version":3,"deployment":{"resources":[]}}`
			final := `{"version":3,"deployment":` + tc.inner + `}`

			saveSequencedVerbatimThenDelta(t, f, base, final)
			checkDeleteProvesCount(t, f, tc.inner, tc.wantErr)
		})
	}
}

// TestDeleteAfterPlainCheckpoint saves a stack's checkpoint through the
// plain checkpoint route and proves `stack rm` without force still refuses
// a stack that manages resources and allows an empty one.
func TestDeleteAfterPlainCheckpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		deployment string
		wantErr    error
	}{
		{name: "empty", deployment: `{"resources":[]}`, wantErr: nil},
		{name: "resourced", deployment: deleteResourcedDeployment, wantErr: stack.ErrHasResources},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newSequencedDeleteFixture(t)

			savePlainCheckpoint(t, f, tc.deployment)
			checkDeleteProvesCount(t, f, tc.deployment, tc.wantErr)
		})
	}
}

// TestDeleteAllowsLargeEmptyCheckpointAboveDefaultLimit proves the delete
// guard reads under the configured checkpoint limit, not the package
// default: a deployment above the default with no managed resources still
// deletes without force. A guard bound to the default would fail the read
// and its fail-safe would refuse the stack.
func TestDeleteAllowsLargeEmptyCheckpointAboveDefaultLimit(t *testing.T) {
	t.Parallel()

	const limit = (32 + 8) << 20

	size := stack.DefaultMaxCheckpointBytes + (1 << 20)
	deployment := deleteEmptyDeployment + strings.Repeat(" ", size-len(deleteEmptyDeployment))

	f := newSequencedDeleteFixtureWithLimit(t, limit)

	savePlainCheckpoint(t, f, deployment)

	if err := f.svc.Delete(t.Context(), f.ref, deleteEntry()); err != nil {
		t.Fatalf("delete a large stack with no resources: %v", err)
	}

	if got := f.store.calls(); got != 1 {
		t.Fatalf("store delete calls = %d, want 1", got)
	}

	guard := f.store.guardAt(0)
	if !guard.HasCheckpoint || guard.HasResources {
		t.Fatalf("guard = %+v, want the counted empty checkpoint", guard)
	}

	if guard.Digest != sha256.Sum256([]byte(deployment)) {
		t.Fatalf("guard digest = %x, want the digest of the counted checkpoint", guard.Digest)
	}
}

// TestStartWithJournalVersionKeepsIt checks that a start keeps the journal
// version that the server negotiated, and that Start keeps version 0 (ADR 43,
// decision 1.3).
func TestStartWithJournalVersionKeepsIt(t *testing.T) {
	t.Parallel()

	f := newSequencedDeleteFixture(t)
	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

	journaled, err := f.updates.Create(ctx, f.ref, plan)
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	started, err := f.updates.StartWithJournalVersion(ctx, f.ref, journaled.ID, 2)
	if err != nil {
		t.Fatalf("start with journal version 2: %v", err)
	}

	if started.Update.JournalVersion != 2 {
		t.Fatalf("started journal version = %d, want 2", started.Update.JournalVersion)
	}

	// A dry run does not hold the stack lock, so it can start while the
	// update above runs.
	dryRun := stack.Plan{Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

	plain, err := f.updates.Create(ctx, f.ref, dryRun)
	if err != nil {
		t.Fatalf("create second update: %v", err)
	}

	startedPlain, err := f.updates.Start(ctx, f.ref, plain.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if startedPlain.Update.JournalVersion != 0 {
		t.Fatalf("Start journal version = %d, want 0", startedPlain.Update.JournalVersion)
	}
}

// SetOverrides is not used by this fake.
func (*memNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

// deleteEntry returns the audit entry that the tests pass to a delete. The
// fake store ignores it.
func deleteEntry() audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     "acme",
		Subject:       "alice@example.com",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:manage",
		Object:        "pulumi:acme/web/prod",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "stack_test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}
