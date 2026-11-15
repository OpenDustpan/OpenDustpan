// Package storefault holds test-only fault wrappers for the SQL store ports
// (ADR 40 decision 1). Tests fail, stall or delay chosen calls to prove the
// crash windows of each write path: a blob written with SQL not committed,
// and SQL committed with an old blob not deleted. Production code must not
// import this package.
//
// A fault names a port method, for example "PutCheckpoint" or
// "FinalizeStateVersion", and the 1-based call count at which it fires. A
// non-positive call count fires on every call. When several faults match one
// call, the first one in the list wins. Every method of Inner is a valid
// name: the namespace, stack, update, checkpoint, event, workspace, state,
// inventory and maintenance ports.
//
// An injected failure keeps the port contract. SQL faults default to
// namespace.ErrUnavailable, the domain sentinel for a store that does not
// answer, except for the port groups with their own contract: maintenance
// faults default to maintenance.ErrUnavailable, the sentinel the maintenance
// port documents, and ListUnits faults answer a plain error, as the inventory
// port defines no sentinel error. No other SQL port documents an
// infra-failure sentinel of its own, and adapters surface driver text there,
// which a test must never assert on, so the wrapper answers the domain
// sentinel instead. A test may set Err to any error the faulted method
// documents to model another outcome: for example, a test that models a
// failed first write on a method whose default is a conflict or exists
// sentinel must pass a custom Err. A stall
// or a delay that outlasts the context answers the context error, which every
// caller already handles. A fault whose method names no port method never
// fires: every test asserts Calls or the faulted error, so a typo surfaces
// as a missing fault.
package storefault

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrNilStore reports a New call without an inner store.
var ErrNilStore = errors.New("storefault needs a store")

// errInjected is the failure a ListUnits fault answers. The inventory port
// defines no sentinel error, so the fault answers a static error shaped like
// an adapter I/O failure, without inventing a new sentinel.
var errInjected = errors.New("injected store fault")

// errNoClose reports an inner store with neither Close signature the wrapper
// accepts.
var errNoClose = errors.New("inner store has no Close")

// Kind selects what a fault does when it fires.
type Kind int

// Fault kinds.
const (
	// KindFail answers the fault error without calling the inner store.
	KindFail Kind = iota
	// KindStall waits until the context ends, then answers its error.
	KindStall
	// KindDelay waits for the configured delay, then calls the inner store.
	// A context that ends first answers its error instead.
	KindDelay
)

// Fault fires at one call of one port method. Method names the method, Call
// is the 1-based invocation count of that method at which the fault fires;
// zero or negative fires on every call. Err is the error a KindFail fault
// answers; nil answers the default of the method: namespace.ErrUnavailable,
// except maintenance.ErrUnavailable for the maintenance methods and a plain
// error for ListUnits. Delay is how long a KindDelay fault waits before it
// calls the inner store.
type Fault struct {
	Method string
	Call   int
	Kind   Kind
	Err    error
	Delay  time.Duration
}

// Inner is every SQL port the wrapper covers. Both built-in adapters serve
// these ports from one database handle.
//
//nolint:interfacebloat // One wrapper for the whole store: every SQL port shares one fault table.
type Inner interface {
	namespace.Store
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
	stack.JournalStore
	stack.BuiltCheckpointStore
	stack.StateAtReader
	workspace.StateAtReader
	workspace.Store
	workspace.StateStore
	inventory.Reader
	inventory.DeletedReader
	inventory.ExpiredDeleted
	inventory.BlobPurges
	inventory.BlobPurgeCounter
	inventory.SweepLocker
	maintenance.Store
	blob.DatabaseClock

	CountRunningJournaledUpdates(ctx context.Context) (int64, error)
}

// Store wraps an Inner store and applies faults before it delegates. The zero
// value is not usable: call New. It is safe for concurrent use.
type Store struct {
	inner  Inner
	mu     sync.Mutex
	faults []Fault
	calls  map[string]int
}

var (
	_ Inner                 = (*Store)(nil)
	_ inventory.SweepLocker = (*Store)(nil)
)

// New returns a Store over inner that applies faults in order.
func New(inner Inner, faults ...Fault) (*Store, error) {
	if inner == nil {
		return nil, fmt.Errorf("%w", ErrNilStore)
	}

	return &Store{inner: inner, mu: sync.Mutex{}, faults: faults, calls: map[string]int{}}, nil
}

// Calls returns how many times method ran through the wrapper, whether a
// fault fired or not. Tests assert it to prove a fault fired.
func (s *Store) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls[method]
}

// Close releases the inner store. It accepts the Close signatures of both
// built-in adapters, as the conformance suites do.
func (s *Store) Close() error {
	switch inner := any(s.inner).(type) {
	case interface{ Close() error }:
		return inner.Close()
	case interface{ Close() }:
		inner.Close()

		return nil
	default:
		return fmt.Errorf("close storefault: %w", errNoClose)
	}
}

// CreateNamespace persists ns, unless a fault fires first.
func (s *Store) CreateNamespace(ctx context.Context, ns namespace.Namespace) error {
	if err := s.before(ctx, "CreateNamespace"); err != nil {
		return err
	}

	return s.inner.CreateNamespace(ctx, ns)
}

// GetNamespace returns the namespace called name, unless a fault fires first.
func (s *Store) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	if err := s.before(ctx, "GetNamespace"); err != nil {
		return namespace.Namespace{}, err
	}

	return s.inner.GetNamespace(ctx, name)
}

// SetOverrides replaces the override switches of a namespace, unless a fault
// fires first.
func (s *Store) SetOverrides(
	ctx context.Context, name namespace.Name, ov namespace.Overrides, fence namespace.Fence,
) error {
	if err := s.before(ctx, "SetOverrides"); err != nil {
		return err
	}

	return s.inner.SetOverrides(ctx, name, ov, fence)
}

// ListNamespaces returns every namespace, unless a fault fires first.
func (s *Store) ListNamespaces(ctx context.Context) ([]namespace.Namespace, error) {
	if err := s.before(ctx, "ListNamespaces"); err != nil {
		return nil, err
	}

	return s.inner.ListNamespaces(ctx)
}

// Ping checks the database, unless a fault fires first.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.before(ctx, "Ping"); err != nil {
		return err
	}

	return s.inner.Ping(ctx)
}

// CreateStack persists st, unless a fault fires first.
func (s *Store) CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error {
	if err := s.before(ctx, "CreateStack"); err != nil {
		return err
	}

	return s.inner.CreateStack(ctx, st, fence)
}

// GetStack returns the stack identified by ref, unless a fault fires first.
func (s *Store) GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error) {
	if err := s.before(ctx, "GetStack"); err != nil {
		return stack.Stack{}, err
	}

	return s.inner.GetStack(ctx, ref)
}

// QueryStacks returns one page of stacks, unless a fault fires first.
func (s *Store) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	if err := s.before(ctx, "QueryStacks"); err != nil {
		return nil, err
	}

	return s.inner.QueryStacks(ctx, q)
}

// ListStacks returns the stacks matching filter, unless a fault fires first.
func (s *Store) ListStacks(ctx context.Context, filter stack.Filter) ([]stack.Stack, error) {
	if err := s.before(ctx, "ListStacks"); err != nil {
		return nil, err
	}

	return s.inner.ListStacks(ctx, filter)
}

// DeleteStack removes the stack identified by ref when guard allows it,
// unless a fault fires first.
func (s *Store) DeleteStack(
	ctx context.Context,
	ref stack.Ref,
	fence namespace.Fence,
	guard stack.DeleteGuard,
	d inventory.Deletion,
) error {
	if err := s.before(ctx, "DeleteStack"); err != nil {
		return err
	}

	return s.inner.DeleteStack(ctx, ref, fence, guard, d)
}

// ForceDeleteStack removes the stack identified by ref even when it manages
// resources, unless a fault fires first.
func (s *Store) ForceDeleteStack(
	ctx context.Context,
	ref stack.Ref,
	fence namespace.Fence,
	d inventory.Deletion,
) error {
	if err := s.before(ctx, "ForceDeleteStack"); err != nil {
		return err
	}

	return s.inner.ForceDeleteStack(ctx, ref, fence, d)
}

// UpdateStackTags replaces the tags of the stack identified by ref, unless a
// fault fires first.
func (s *Store) UpdateStackTags(
	ctx context.Context, ref stack.Ref, tags map[string]string, updatedAt time.Time, fence namespace.Fence,
) error {
	if err := s.before(ctx, "UpdateStackTags"); err != nil {
		return err
	}

	return s.inner.UpdateStackTags(ctx, ref, tags, updatedAt, fence)
}

// RenameStack changes the stack name of oldRef, unless a fault fires first.
func (s *Store) RenameStack(
	ctx context.Context, oldRef stack.Ref, newName stack.Name, fence namespace.Fence, e audit.Entry,
) error {
	if err := s.before(ctx, "RenameStack"); err != nil {
		return err
	}

	return s.inner.RenameStack(ctx, oldRef, newName, fence, e)
}

// CreateUpdate persists a new update, unless a fault fires first.
func (s *Store) CreateUpdate(
	ctx context.Context, u stack.Update, key stack.UpdateKey, fence namespace.Fence,
) error {
	if err := s.before(ctx, "CreateUpdate"); err != nil {
		return err
	}

	return s.inner.CreateUpdate(ctx, u, key, fence)
}

// GetUpdate returns one update, unless a fault fires first.
func (s *Store) GetUpdate(ctx context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	if err := s.before(ctx, "GetUpdate"); err != nil {
		return stack.Update{}, err
	}

	return s.inner.GetUpdate(ctx, ref, id)
}

// StartUpdate persists the started update, unless a fault fires first.
func (s *Store) StartUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) (stack.Update, error) {
	if err := s.before(ctx, "StartUpdate"); err != nil {
		return stack.Update{}, err
	}

	return s.inner.StartUpdate(ctx, u, fence)
}

// SaveUpdate persists a transition of u, unless a fault fires first.
func (s *Store) SaveUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) error {
	if err := s.before(ctx, "SaveUpdate"); err != nil {
		return err
	}

	return s.inner.SaveUpdate(ctx, u, fence)
}

// ActiveUpdate returns the running mutating update, unless a fault fires first.
func (s *Store) ActiveUpdate(ctx context.Context, ref stack.Ref) (stack.Update, error) {
	if err := s.before(ctx, "ActiveUpdate"); err != nil {
		return stack.Update{}, err
	}

	return s.inner.ActiveUpdate(ctx, ref)
}

// ListUpdates returns the started mutating updates, unless a fault fires first.
func (s *Store) ListUpdates(ctx context.Context, ref stack.Ref, page stack.Page) ([]stack.Update, error) {
	if err := s.before(ctx, "ListUpdates"); err != nil {
		return nil, err
	}

	return s.inner.ListUpdates(ctx, ref, page)
}

// DatabaseNow returns the clock of the database, unless a fault fires first.
func (s *Store) DatabaseNow(ctx context.Context) (time.Time, error) {
	if err := s.before(ctx, "DatabaseNow"); err != nil {
		return time.Time{}, err
	}

	return s.inner.DatabaseNow(ctx)
}

// PutCheckpoint saves cp as the state of its update, unless a fault fires first.
func (s *Store) PutCheckpoint(ctx context.Context, cp stack.Checkpoint, fence namespace.Fence) (bool, error) {
	if err := s.before(ctx, "PutCheckpoint"); err != nil {
		return false, err
	}

	return s.inner.PutCheckpoint(ctx, cp, fence)
}

// LatestCheckpoint returns the checkpoint of the highest version that has
// one, unless a fault fires first.
func (s *Store) LatestCheckpoint(ctx context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	if err := s.before(ctx, "LatestCheckpoint"); err != nil {
		return stack.Checkpoint{}, err
	}

	return s.inner.LatestCheckpoint(ctx, ref)
}

// CheckpointByVersion returns the checkpoint of the given version, unless a
// fault fires first.
func (s *Store) CheckpointByVersion(
	ctx context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	if err := s.before(ctx, "CheckpointByVersion"); err != nil {
		return stack.Checkpoint{}, err
	}

	return s.inner.CheckpointByVersion(ctx, ref, version)
}

// CheckpointByUpdate returns the checkpoint of the given update, unless a
// fault fires first.
func (s *Store) CheckpointByUpdate(
	ctx context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Checkpoint, error) {
	if err := s.before(ctx, "CheckpointByUpdate"); err != nil {
		return stack.Checkpoint{}, err
	}

	return s.inner.CheckpointByUpdate(ctx, ref, id)
}

// ReferencedBlobKeys returns the blob keys that the rows of the stack ID name,
// unless a fault fires first.
func (s *Store) ReferencedBlobKeys(ctx context.Context, stackID stack.ID) ([]blob.Key, error) {
	if err := s.before(ctx, "ReferencedBlobKeys"); err != nil {
		return nil, err
	}

	return s.inner.ReferencedBlobKeys(ctx, stackID)
}

// StateAt returns the update that held the state of the stack at a time,
// unless a fault fires first.
func (s *Store) StateAt(ctx context.Context, id stack.ID, at time.Time) (stack.StateAt, error) {
	if err := s.before(ctx, "StateAt"); err != nil {
		return stack.StateAt{}, err
	}

	return s.inner.StateAt(ctx, id, at)
}

// WorkspaceStateAt returns the state version that was current in a workspace
// at a time, unless a fault fires first.
func (s *Store) WorkspaceStateAt(ctx context.Context, id workspace.ID, at time.Time) (workspace.StateAt, error) {
	if err := s.before(ctx, "WorkspaceStateAt"); err != nil {
		return workspace.StateAt{}, err
	}

	return s.inner.WorkspaceStateAt(ctx, id, at)
}

// AppendEvents stores the batch's events, unless a fault fires first.
func (s *Store) AppendEvents(ctx context.Context, batch stack.EventBatch, fence namespace.Fence) error {
	if err := s.before(ctx, "AppendEvents"); err != nil {
		return err
	}

	return s.inner.AppendEvents(ctx, batch, fence)
}

// JournalKeys returns the blob keys of the update's batches, unless a fault fires first.
func (s *Store) JournalKeys(ctx context.Context, stackID stack.ID, update stack.UpdateID) ([]blob.Key, error) {
	if err := s.before(ctx, "JournalKeys"); err != nil {
		return nil, err
	}

	return s.inner.JournalKeys(ctx, stackID, update)
}

// JournalBatches returns the references of the update's batches, unless a fault fires first.
func (s *Store) JournalBatches(
	ctx context.Context, stackID stack.ID, update stack.UpdateID,
) ([]stack.JournalBatchRef, error) {
	if err := s.before(ctx, "JournalBatches"); err != nil {
		return nil, err
	}

	return s.inner.JournalBatches(ctx, stackID, update)
}

// JournalSweepable reports whether the journal may be swept, unless a fault fires first.
func (s *Store) JournalSweepable(ctx context.Context, stackID stack.ID, update stack.UpdateID) (bool, error) {
	if err := s.before(ctx, "JournalSweepable"); err != nil {
		return false, err
	}

	return s.inner.JournalSweepable(ctx, stackID, update)
}

// PutBuiltCheckpoint writes the built checkpoint and removes the journal, unless a
// fault fires first.
func (s *Store) PutBuiltCheckpoint(
	ctx context.Context,
	cp stack.Checkpoint,
	fence namespace.Fence,
) ([]blob.Key, error) {
	if err := s.before(ctx, "PutBuiltCheckpoint"); err != nil {
		return nil, err
	}

	return s.inner.PutBuiltCheckpoint(ctx, cp, fence)
}

// PutJournalRow stores the row of one batch, unless a fault fires first.
func (s *Store) PutJournalRow(ctx context.Context, row stack.JournalRow, fence namespace.Fence) (bool, error) {
	if err := s.before(ctx, "PutJournalRow"); err != nil {
		return false, err
	}

	return s.inner.PutJournalRow(ctx, row, fence)
}

// RecordJournalRefusal stores one refused or unstored batch, unless a fault
// fires first.
func (s *Store) RecordJournalRefusal(
	ctx context.Context, refusal stack.JournalRefusal, fence namespace.Fence,
) error {
	if err := s.before(ctx, "RecordJournalRefusal"); err != nil {
		return err
	}

	return s.inner.RecordJournalRefusal(ctx, refusal, fence)
}

// JournalRefusals returns the recorded refusals of the update, unless a fault
// fires first.
func (s *Store) JournalRefusals(
	ctx context.Context, stackID stack.ID, update stack.UpdateID,
) ([]stack.JournalRefusal, error) {
	if err := s.before(ctx, "JournalRefusals"); err != nil {
		return nil, err
	}

	return s.inner.JournalRefusals(ctx, stackID, update)
}

// JournalSequences returns the stored sequence IDs of the update, unless a
// fault fires first.
func (s *Store) JournalSequences(
	ctx context.Context, stackID stack.ID, update stack.UpdateID,
) ([]int64, error) {
	if err := s.before(ctx, "JournalSequences"); err != nil {
		return nil, err
	}

	return s.inner.JournalSequences(ctx, stackID, update)
}

// ListEvents returns the events of the update, unless a fault fires first.
func (s *Store) ListEvents(
	ctx context.Context, ref stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	if err := s.before(ctx, "ListEvents"); err != nil {
		return nil, err
	}

	return s.inner.ListEvents(ctx, ref, id, after, limit)
}

// CountRunningJournaledUpdates counts the running journaled updates, unless a
// fault fires first.
func (s *Store) CountRunningJournaledUpdates(ctx context.Context) (int64, error) {
	if err := s.before(ctx, "CountRunningJournaledUpdates"); err != nil {
		return 0, err
	}

	return s.inner.CountRunningJournaledUpdates(ctx)
}

// CreateWorkspace persists a new workspace, unless a fault fires first.
func (s *Store) CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	if err := s.before(ctx, "CreateWorkspace"); err != nil {
		return err
	}

	return s.inner.CreateWorkspace(ctx, w, fence)
}

// GetWorkspace returns the workspace called ref, unless a fault fires first.
func (s *Store) GetWorkspace(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	if err := s.before(ctx, "GetWorkspace"); err != nil {
		return workspace.Workspace{}, err
	}

	return s.inner.GetWorkspace(ctx, ref)
}

// GetWorkspaceByID returns the workspace with id, unless a fault fires first.
func (s *Store) GetWorkspaceByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	if err := s.before(ctx, "GetWorkspaceByID"); err != nil {
		return workspace.Workspace{}, err
	}

	return s.inner.GetWorkspaceByID(ctx, id)
}

// QueryWorkspaces returns one page of workspaces, unless a fault fires
// first.
func (s *Store) QueryWorkspaces(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	if err := s.before(ctx, "QueryWorkspaces"); err != nil {
		return workspace.ListPage{}, err
	}

	return s.inner.QueryWorkspaces(ctx, q)
}

// ListWorkspaces returns the namespace's workspaces, unless a fault fires first.
func (s *Store) ListWorkspaces(ctx context.Context, ns namespace.Name) ([]workspace.Workspace, error) {
	if err := s.before(ctx, "ListWorkspaces"); err != nil {
		return nil, err
	}

	return s.inner.ListWorkspaces(ctx, ns)
}

// SaveWorkspace persists w, unless a fault fires first.
func (s *Store) SaveWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	if err := s.before(ctx, "SaveWorkspace"); err != nil {
		return err
	}

	return s.inner.SaveWorkspace(ctx, w, fence)
}

// ReleaseUnfenced saves a released unit under the forced-write override, unless a fault fires first.
func (s *Store) ReleaseUnfenced(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	if err := s.before(ctx, "ReleaseUnfenced"); err != nil {
		return err
	}

	return s.inner.ReleaseUnfenced(ctx, w, fence, e)
}

// SaveReleased saves a released unit with its audit entry, unless a fault fires first.
func (s *Store) SaveReleased(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	if err := s.before(ctx, "SaveReleased"); err != nil {
		return err
	}

	return s.inner.SaveReleased(ctx, w, fence, e)
}

// DeleteWorkspace soft-deletes the workspace called ref, unless a fault
// fires first.
func (s *Store) DeleteWorkspace(
	ctx context.Context, ref workspace.Ref, fence namespace.Fence, d inventory.Deletion,
) error {
	if err := s.before(ctx, "DeleteWorkspace"); err != nil {
		return err
	}

	return s.inner.DeleteWorkspace(ctx, ref, fence, d)
}

// RestoreWorkspace brings back a deleted workspace, unless a fault fires
// first.
func (s *Store) RestoreWorkspace(
	ctx context.Context, id workspace.ID, newName workspace.Name, fence namespace.Fence, e audit.Entry,
) (workspace.Ref, error) {
	if err := s.before(ctx, "RestoreWorkspace"); err != nil {
		return workspace.Ref{}, err
	}

	return s.inner.RestoreWorkspace(ctx, id, newName, fence, e)
}

// PurgeWorkspace removes a deleted workspace for good, unless a fault fires
// first.
func (s *Store) PurgeWorkspace(
	ctx context.Context,
	id workspace.ID,
	fence namespace.Fence,
	dueBy time.Time,
	e audit.Entry,
) error {
	if err := s.before(ctx, "PurgeWorkspace"); err != nil {
		return err
	}

	return s.inner.PurgeWorkspace(ctx, id, fence, dueBy, e)
}

// RestoreStack brings back a deleted stack, unless a fault fires first.
func (s *Store) RestoreStack(
	ctx context.Context, id stack.ID, newName stack.Name, fence namespace.Fence, e audit.Entry,
) (stack.Ref, error) {
	if err := s.before(ctx, "RestoreStack"); err != nil {
		return stack.Ref{}, err
	}

	return s.inner.RestoreStack(ctx, id, newName, fence, e)
}

// PurgeStack removes a deleted stack for good, unless a fault fires first.
func (s *Store) PurgeStack(
	ctx context.Context,
	id stack.ID,
	fence namespace.Fence,
	dueBy time.Time,
	e audit.Entry,
) error {
	if err := s.before(ctx, "PurgeStack"); err != nil {
		return err
	}

	return s.inner.PurgeStack(ctx, id, fence, dueBy, e)
}

// CreateStateVersion persists a new, pending version, unless a fault fires first.
func (s *Store) CreateStateVersion(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int,
) error {
	if err := s.before(ctx, "CreateStateVersion"); err != nil {
		return err
	}

	return s.inner.CreateStateVersion(ctx, sv, fence, maxPending)
}

// GetStateVersion returns the state version, unless a fault fires first.
func (s *Store) GetStateVersion(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if err := s.before(ctx, "GetStateVersion"); err != nil {
		return workspace.StateVersion{}, err
	}

	return s.inner.GetStateVersion(ctx, id)
}

// FinalizeStateVersion saves the finalized version and makes it current,
// unless a fault fires first.
func (s *Store) FinalizeStateVersion(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	if err := s.before(ctx, "FinalizeStateVersion"); err != nil {
		return err
	}

	return s.inner.FinalizeStateVersion(ctx, sv, fence)
}

// FinalizeStateVersionExpecting finalizes as FinalizeStateVersion does and
// also checks the current version, unless a fault fires first. The fault name
// is the one of FinalizeStateVersion.
func (s *Store) FinalizeStateVersionExpecting(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, expect workspace.StateVersionID,
) error {
	if err := s.before(ctx, "FinalizeStateVersion"); err != nil {
		return err
	}

	return s.inner.FinalizeStateVersionExpecting(ctx, sv, fence, expect)
}

// FenceAndUnlock releases the lock and makes the fence version current,
// unless a fault fires first.
//
//nolint:revive // The signature of the workspace.StateStore port.
func (s *Store) FenceAndUnlock(
	ctx context.Context, w workspace.Workspace, sv workspace.StateVersion,
	expect workspace.StateVersionID, fence namespace.Fence, e audit.Entry,
) error {
	if err := s.before(ctx, "FenceAndUnlock"); err != nil {
		return err
	}

	return s.inner.FenceAndUnlock(ctx, w, sv, expect, fence, e)
}

// CurrentStateVersion returns the workspace's current state, unless a fault
// fires first.
func (s *Store) CurrentStateVersion(
	ctx context.Context, id workspace.ID,
) (workspace.StateVersion, error) {
	if err := s.before(ctx, "CurrentStateVersion"); err != nil {
		return workspace.StateVersion{}, err
	}

	return s.inner.CurrentStateVersion(ctx, id)
}

// ListStateVersions returns the finalized versions newest first, unless a
// fault fires first.
func (s *Store) ListStateVersions(
	ctx context.Context, id workspace.ID, page workspace.Page,
) ([]workspace.StateVersion, int, error) {
	if err := s.before(ctx, "ListStateVersions"); err != nil {
		return nil, 0, err
	}

	return s.inner.ListStateVersions(ctx, id, page)
}

// CountPendingStateVersions returns how many pending versions the workspace
// holds, unless a fault fires first.
func (s *Store) CountPendingStateVersions(ctx context.Context, id workspace.ID) (int, error) {
	if err := s.before(ctx, "CountPendingStateVersions"); err != nil {
		return 0, err
	}

	return s.inner.CountPendingStateVersions(ctx, id)
}

// ReferencedContentKeys returns the blob keys every version of the workspace
// names, unless a fault fires first.
func (s *Store) ReferencedContentKeys(ctx context.Context, id workspace.ID) ([]blob.Key, error) {
	if err := s.before(ctx, "ReferencedContentKeys"); err != nil {
		return nil, err
	}

	return s.inner.ReferencedContentKeys(ctx, id)
}

// RecordUploadAttempt notes the blob key of one upload attempt, unless a
// fault fires first.
func (s *Store) RecordUploadAttempt(
	ctx context.Context, id workspace.StateVersionID, key blob.Key, fence namespace.Fence,
) (blob.Key, error) {
	if err := s.before(ctx, "RecordUploadAttempt"); err != nil {
		return "", err
	}

	return s.inner.RecordUploadAttempt(ctx, id, key, fence)
}

// SaveSensitiveMarks stores the secret paths of one version, unless a fault
// fires first.
func (s *Store) SaveSensitiveMarks(
	ctx context.Context, id workspace.StateVersionID, marks workspace.SensitiveMarks, fence namespace.Fence,
) error {
	if err := s.before(ctx, "SaveSensitiveMarks"); err != nil {
		return err
	}

	return s.inner.SaveSensitiveMarks(ctx, id, marks, fence)
}

// DiscardStateVersion discards the pending version, unless a fault fires first.
func (s *Store) DiscardStateVersion(ctx context.Context, id workspace.StateVersionID, fence namespace.Fence) error {
	if err := s.before(ctx, "DiscardStateVersion"); err != nil {
		return err
	}

	return s.inner.DiscardStateVersion(ctx, id, fence)
}

// DeleteExpiredPendingStateVersions deletes the expired pending versions,
// unless a fault fires first.
func (s *Store) DeleteExpiredPendingStateVersions(
	ctx context.Context, id workspace.ID, expiredBefore time.Time, fence namespace.Fence,
) ([]workspace.StateVersion, error) {
	if err := s.before(ctx, "DeleteExpiredPendingStateVersions"); err != nil {
		return nil, err
	}

	return s.inner.DeleteExpiredPendingStateVersions(ctx, id, expiredBefore, fence)
}

// GetUnit returns the live unit unitID, unless a fault fires first.
func (s *Store) GetUnit(ctx context.Context, unitID string) (inventory.Entry, error) {
	if err := s.before(ctx, "GetUnit"); err != nil {
		return inventory.Entry{}, err
	}

	return s.inner.GetUnit(ctx, unitID)
}

// ListUnits returns the inventory entries of ns, unless a fault fires first.
func (s *Store) ListUnits(ctx context.Context, ns namespace.Name) ([]inventory.Entry, error) {
	if err := s.before(ctx, "ListUnits"); err != nil {
		return nil, err
	}

	return s.inner.ListUnits(ctx, ns)
}

// QueryUnits returns one page of the inventory entries, unless a fault fires
// first.
func (s *Store) QueryUnits(ctx context.Context, q inventory.UnitQuery) ([]inventory.Entry, error) {
	if err := s.before(ctx, "QueryUnits"); err != nil {
		return nil, err
	}

	return s.inner.QueryUnits(ctx, q)
}

// ListDeletedUnits returns the deleted inventory entries of ns, unless a fault
// fires first.
func (s *Store) ListDeletedUnits(ctx context.Context, ns namespace.Name) ([]inventory.DeletedEntry, error) {
	if err := s.before(ctx, "ListDeletedUnits"); err != nil {
		return nil, err
	}

	return s.inner.ListDeletedUnits(ctx, ns)
}

// QueryDeletedUnits returns one page of deleted units, unless a fault fires
// first.
func (s *Store) QueryDeletedUnits(ctx context.Context, q inventory.DeletedQuery) (inventory.DeletedPage, error) {
	if err := s.before(ctx, "QueryDeletedUnits"); err != nil {
		return inventory.DeletedPage{}, err
	}

	return s.inner.QueryDeletedUnits(ctx, q)
}

// GetDeletedUnit returns one deleted unit, unless a fault fires first.
func (s *Store) GetDeletedUnit(
	ctx context.Context, ns namespace.Name, tool, unitID string,
) (inventory.DeletedEntry, error) {
	if err := s.before(ctx, "GetDeletedUnit"); err != nil {
		return inventory.DeletedEntry{}, err
	}

	return s.inner.GetDeletedUnit(ctx, ns, tool, unitID)
}

// GetDeletedUnitFacts returns the facts of one deleted unit, unless a fault
// fires first. It fails closed when the inner store cannot give them.
func (s *Store) GetDeletedUnitFacts(
	ctx context.Context, ns namespace.Name, tool, unitID string,
) (inventory.DeletedFacts, error) {
	if err := s.before(ctx, "GetDeletedUnitFacts"); err != nil {
		return inventory.DeletedFacts{}, err
	}

	inner, ok := s.inner.(inventory.DeletedFactsReader)
	if !ok {
		return inventory.DeletedFacts{}, inventory.ErrNotFound
	}

	return inner.GetDeletedUnitFacts(ctx, ns, tool, unitID)
}

// ListExpiredDeleted returns one page of expired deleted units, unless a
// fault fires first.
func (s *Store) ListExpiredDeleted(
	ctx context.Context, now time.Time, after *inventory.ExpiredCursor, limit int,
) ([]inventory.DeletedEntry, error) {
	if err := s.before(ctx, "ListExpiredDeleted"); err != nil {
		return nil, err
	}

	return s.inner.ListExpiredDeleted(ctx, now, after, limit)
}

// ListBlobPurges returns one page of blob purge records, unless a fault
// fires first.
func (s *Store) ListBlobPurges(
	ctx context.Context, after *inventory.BlobPurgeCursor, limit int,
) ([]inventory.BlobPurge, error) {
	if err := s.before(ctx, "ListBlobPurges"); err != nil {
		return nil, err
	}

	return s.inner.ListBlobPurges(ctx, after, limit)
}

// FinishBlobPurge removes one blob purge record, unless a fault fires first.
func (s *Store) FinishBlobPurge(ctx context.Context, tool, unitID string) error {
	if err := s.before(ctx, "FinishBlobPurge"); err != nil {
		return err
	}

	return s.inner.FinishBlobPurge(ctx, tool, unitID)
}

// CountBlobPurges returns the size of the blob purge backlog, unless a fault
// fires first.
func (s *Store) CountBlobPurges(ctx context.Context) (inventory.BlobPurgeBacklog, error) {
	if err := s.before(ctx, "CountBlobPurges"); err != nil {
		return inventory.BlobPurgeBacklog{}, err
	}

	return s.inner.CountBlobPurges(ctx)
}

// TryLockSweep takes the sweep lock of the inner store without a wait, unless
// a fault fires first. A fault answers its error and takes no lock.
func (s *Store) TryLockSweep(ctx context.Context) (func(), error) {
	if err := s.before(ctx, "TryLockSweep"); err != nil {
		return nil, err
	}

	return s.inner.TryLockSweep(ctx)
}

// GetState returns the installation state, unless a fault fires first.
func (s *Store) GetState(ctx context.Context) (maintenance.State, error) {
	if err := s.before(ctx, "GetState"); err != nil {
		return "", err
	}

	return s.inner.GetState(ctx)
}

// SetState stores the installation state, unless a fault fires first.
func (s *Store) SetState(ctx context.Context, state maintenance.State) error {
	if err := s.before(ctx, "SetState"); err != nil {
		return err
	}

	return s.inner.SetState(ctx, state)
}

// SetStateIf stores next only when the stored state is expected, unless a
// fault fires first.
func (s *Store) SetStateIf(ctx context.Context, expected, next maintenance.State) error {
	if err := s.before(ctx, "SetStateIf"); err != nil {
		return err
	}

	return s.inner.SetStateIf(ctx, expected, next)
}

// before counts one call of method and applies the matching fault, if any.
// It returns nil when the call proceeds to the inner store.
func (s *Store) before(ctx context.Context, method string) error {
	fault, ok := s.match(method)
	if !ok {
		return nil
	}

	switch fault.Kind {
	case KindFail:
		if fault.Err != nil {
			return fmt.Errorf("fault %s: %w", method, fault.Err)
		}

		return fmt.Errorf("fault %s: %w", method, defaultErr(method))
	case KindStall:
		return s.stall(ctx, method)
	case KindDelay:
		return s.delay(ctx, method, fault.Delay)
	default:
		return fmt.Errorf("fault %s: %w", method, defaultErr(method))
	}
}

// defaultErr returns the failure of method where the port documents one:
// maintenance.ErrUnavailable for the maintenance methods, a plain error for
// ListUnits and GetUnit, which define no failure sentinel, and namespace.ErrUnavailable
// otherwise.
func defaultErr(method string) error {
	switch method {
	case "GetState", "SetState", "SetStateIf":
		return maintenance.ErrUnavailable
	case "ListUnits", "GetUnit":
		return errInjected
	default:
		return namespace.ErrUnavailable
	}
}

// match counts one call of method and returns the first fault for it, if any.
func (s *Store) match(method string) (Fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls[method]++

	n := s.calls[method]

	for _, fault := range s.faults {
		if fault.Method == method && (fault.Call <= 0 || fault.Call == n) {
			return fault, true
		}
	}

	var none Fault

	return none, false
}

// stall waits until ctx ends, then answers its error.
func (s *Store) stall(ctx context.Context, method string) error {
	<-ctx.Done()

	return fmt.Errorf("fault %s stall: %w", method, ctx.Err())
}

// delay waits for d, then lets the call proceed. A context that ends first
// answers its error instead. A non-positive delay proceeds at once.
func (s *Store) delay(ctx context.Context, method string, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("fault %s delay: %w", method, ctx.Err())
	case <-timer.C:
		return nil
	}
}
