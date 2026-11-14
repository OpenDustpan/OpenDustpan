// Package stack models projects and stacks inside a namespace: their identity,
// tags and lifecycle (Service), and the update cycle the Pulumi CLI drives
// against them: updates, leases, checkpoints and engine events (Updates).
package stack

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports that a stack does not exist.
	ErrNotFound = errors.New("stack not found")
	// ErrBlobsRemain reports that a blob of a purged unit may remain after a
	// delete (ADR 47). The blob purge record stays for the sweep.
	ErrBlobsRemain = errors.New("blobs of the purged unit remain")
	// ErrAlreadyExists reports that a stack with the same reference exists.
	ErrAlreadyExists = errors.New("stack already exists")
	// ErrInvalidName reports a project or stack name that fails validation.
	ErrInvalidName = errors.New("invalid project or stack name")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrHasResources reports that a stack still manages resources, so a
	// delete without force is refused.
	ErrHasResources = errors.New("stack still contains resources")
	// ErrPlacementUnsupported reports that the store cannot make a stack and
	// put it in a collection in one transaction, so the service makes no stack.
	ErrPlacementUnsupported = errors.New("the store cannot put a new stack in a collection")
)

const (
	maxNameLength = 100
	maxTags       = 64
)

// ProjectName is a validated project name.
type ProjectName string

// ParseProjectName validates s as a project name.
func ParseProjectName(s string) (ProjectName, error) {
	if err := ident.Check(s, maxNameLength); err != nil {
		return "", fmt.Errorf("%w: project %w", ErrInvalidName, err)
	}

	return ProjectName(s), nil
}

// String returns the project name as a plain string.
func (p ProjectName) String() string {
	return string(p)
}

// Name is a validated stack name.
type Name string

// ID is a stack's stable, opaque identifier ("stk-..."). A rename keeps it,
// and a new stack with the name of a deleted stack gets a new ID.
type ID string

// String returns the ID as a plain string.
func (id ID) String() string {
	return string(id)
}

// ParseName validates s as a stack name.
func ParseName(s string) (Name, error) {
	if err := ident.Check(s, maxNameLength); err != nil {
		return "", fmt.Errorf("%w: stack %w", ErrInvalidName, err)
	}

	return Name(s), nil
}

// String returns the stack name as a plain string.
func (n Name) String() string {
	return string(n)
}

// Ref is the fully qualified identity of a stack: namespace/project/stack.
type Ref struct {
	Namespace namespace.Name
	Project   ProjectName
	Stack     Name
}

// ParseRef validates the three parts of a stack reference.
func ParseRef(ns, project, stack string) (Ref, error) {
	nsName, err := namespace.ParseName(ns)
	if err != nil {
		return Ref{}, fmt.Errorf("parse stack reference: %w", err)
	}

	projectName, err := ParseProjectName(project)
	if err != nil {
		return Ref{}, err
	}

	stackName, err := ParseName(stack)
	if err != nil {
		return Ref{}, err
	}

	return Ref{Namespace: nsName, Project: projectName, Stack: stackName}, nil
}

// String formats the reference as namespace/project/stack.
func (r Ref) String() string {
	return r.Namespace.String() + "/" + r.Project.String() + "/" + r.Stack.String()
}

// Stack is the aggregate root for a stack.
type Stack struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	Tags      map[string]string
	Ref       Ref
	ID        ID
	// NamespaceID is the ID of the namespace row of Ref. Only GetStack fills
	// it, from the same query as the stack, so a caller that reads the blobs of
	// the stack builds their scope without a second read. A list read leaves it
	// empty.
	NamespaceID namespace.ID
	// Version is the version of the stack's last finished update; a new stack
	// is at version 0.
	Version int64
}

// Filter selects stacks for ListStacks. An empty Project matches every project.
type Filter struct {
	Namespace namespace.Name
	Project   ProjectName
}

// DeleteGuard carries the domain count of the latest checkpoint into the
// store delete transaction. The store keeps no resource count, so the service
// counts the one latest deployment outside any transaction (reading and
// gunzipping its blob) and the store checks the checkpoint is still the one
// counted. UpdateID with Digest identify the counted checkpoint;
// HasCheckpoint is false when the stack had no checkpoint; HasResources
// reports whether it managed resources.
type DeleteGuard struct {
	UpdateID      UpdateID
	Digest        [sha256.Size]byte
	HasCheckpoint bool
	HasResources  bool
}

// EmptyGuard returns the DeleteGuard of a stack without a checkpoint. The
// store refuses it with ErrConcurrentModification when the stack has one.
func EmptyGuard() DeleteGuard {
	//nolint:exhaustruct_v5 // An empty guard means the stack had no checkpoint to count.
	return DeleteGuard{}
}

// Placement puts a new stack in a collection in the transaction that makes the
// stack. The caller has already chosen the collection and checked the right to
// make a unit in it.
type Placement struct {
	// Collection is the collection of the new stack. It must be in the
	// namespace of the stack.
	Collection collection.Ref
	// CollectionID is the stable ID of Collection that the caller checked. The
	// store refuses the create when the collection has another ID now.
	CollectionID string
	// Entry is the audit entry of the placement. The store appends it in the
	// same transaction. The service sets its time.
	Entry audit.Entry
}

// PlacedStore is the port of a store that makes a stack, its inventory entry,
// its membership of a collection and the audit entry of the placement in one
// fenced transaction. A failure of any part makes none of them, so no stack
// exists without its collection.
type PlacedStore interface {
	// CreateStackPlaced persists st as CreateStack does and puts it in the
	// collection of p, in the same transaction. It also returns
	// collection.ErrNotFound when the collection does not exist,
	// collection.ErrCollectionChanged when it has another ID than
	// p.CollectionID, and the errors of a refused join (for example a gate).
	CreateStackPlaced(ctx context.Context, st Stack, p Placement, fence namespace.Fence) error
}

// Store is the persistence port for stacks. Tags live in their own table
// keyed by the stack ID, so a rename keeps them without moving any tag row.
//
//nolint:interfacebloat // One adapter contract for one table set: every stack read and write shares its rows and its fence.
type Store interface {
	// CreateStack persists st if fence matches the namespace's current failover
	// version. It returns ErrAlreadyExists, namespace.ErrNotFound or
	// namespace.ErrFenced.
	CreateStack(ctx context.Context, st Stack, fence namespace.Fence) error
	// GetStack returns the stack identified by ref, or ErrNotFound.
	GetStack(ctx context.Context, ref Ref) (Stack, error)
	// ListStacks returns the stacks matching filter, ordered by project then name.
	ListStacks(ctx context.Context, filter Filter) ([]Stack, error)
	// QueryStacks returns at most q.PageLimit() live stacks that match q,
	// ordered by namespace, project, then name, with their tags. PageStacks
	// defines the result.
	QueryStacks(ctx context.Context, q PageQuery) ([]Stack, error)
	// DeleteStack soft-deletes the stack identified by ref at the time e.At,
	// in one fenced transaction (ADR 47), and appends the audit entry e to the
	// chain of the namespace in the same transaction. If the append fails,
	// nothing changes (ADR 60, fail closed). The stack leaves every lookup by name
	// and every list, and its name is free at once for a new stack. Its
	// updates, checkpoints, events, tags and data key stay until the final
	// delete. When the latest checkpoint manages resources, it returns
	// ErrHasResources and changes nothing. The store answers from guard,
	// which the caller counted outside the transaction. When the latest
	// checkpoint is no longer the one guard names, it returns
	// ErrConcurrentModification so the caller recounts once. The check runs
	// in the same transaction as the delete, so an update that finishes
	// concurrently cannot drop resources.
	// It returns ErrNotFound if no live stack has ref, ErrUpdateConflict while
	// a mutating update of the stack runs (a *UpdateConflictError that names
	// that update), namespace.ErrNotFound if the namespace does not exist,
	// namespace.ErrFenced if fence is stale, or audit.ErrInvalidEntry if e
	// fails validation or names another namespace.
	// While a manual lock covers the stack, its collection or its namespace,
	// the error matches ErrUpdateConflict and manuallock.ErrLocked.
	DeleteStack(
		ctx context.Context,
		ref Ref,
		fence namespace.Fence,
		guard DeleteGuard,
		d inventory.Deletion,
	) error
	// ForceDeleteStack soft-deletes the stack identified by ref at the time
	// e.At and appends e, as DeleteStack does, even when the latest checkpoint
	// manages resources. It returns ErrNotFound if no live stack has ref,
	// ErrUpdateConflict while a mutating update of the stack runs (a
	// *UpdateConflictError that names that update),
	// namespace.ErrNotFound if the namespace does not exist,
	// namespace.ErrFenced if fence is stale, or audit.ErrInvalidEntry if e
	// fails validation or names another namespace. While a manual lock covers
	// the stack, its collection or its namespace, the error matches
	// ErrUpdateConflict and manuallock.ErrLocked.
	ForceDeleteStack(ctx context.Context, ref Ref, fence namespace.Fence, d inventory.Deletion) error
	DeletedStore
	// UpdateStackTags replaces the tags of the stack identified by ref with
	// tags, keyed by its stable ID, and stamps updatedAt. It returns
	// ErrNotFound, namespace.ErrNotFound or namespace.ErrFenced.
	UpdateStackTags(
		ctx context.Context, ref Ref, tags map[string]string, updatedAt time.Time, fence namespace.Fence,
	) error
	// RenameStack changes the stack name of oldRef to newName if fence matches
	// the namespace's current failover version. The stack ID and every child
	// row stay with the stack. It appends the audit entry e in the same fenced
	// transaction, with the stable stack ID and the old and the new name. A
	// rename to the current name writes no audit entry. It
	// returns ErrNotFound for a missing stack, ErrAlreadyExists for a taken
	// name, ErrUpdateConflict while a mutating update of the stack runs (a
	// *UpdateConflictError that names that update), namespace.ErrNotFound for a
	// missing namespace, namespace.ErrFenced for a stale fence or
	// audit.ErrInvalidEntry if e fails validation or names another namespace.
	// The adapter checks for the running update in the transaction of the
	// rename, after it locks the stack, so an update that starts at the same
	// time cannot run under a name that no stack has.
	RenameStack(ctx context.Context, oldRef Ref, newName Name, fence namespace.Fence, e audit.Entry) error
}

// LatestState resolves the latest checkpoint of a stack with its deployment
// bytes. *Updates implements it: the guard counts through the same resolve,
// digest check and unwrap path as the export, under the configured checkpoint
// size limit.
type LatestState interface {
	// Export returns the stack's latest checkpoint with its deployment
	// resolved, digest-checked and unwrapped. A sequenced save keeps the
	// versioned envelope bytes, so the digest still names the stored row
	// while the deployment holds the inner bytes. It returns ErrNoState
	// when the stack never saved state, ErrCheckpointUnavailable when the
	// blob cannot be read, ErrCheckpointMismatch when the stored bytes do
	// not match their record, or another store error. On a content error
	// the returned checkpoint still carries the stored row identity (Update
	// and Digest) with no deployment, so the caller can name the checkpoint
	// it could not read.
	Export(ctx context.Context, ref Ref) (Checkpoint, error)
}

// DeletedStore is the part of Store that acts on a soft-deleted stack by its
// stable ID (ADR 47): the restore and the final delete.
type DeletedStore interface {
	// RestoreStack brings back the deleted stack id of the namespace of
	// fence before its retention period ends (ADR 47), under newName in its
	// project, or under its name before the delete when newName is empty. It
	// moves the inventory entry back to the live projection and appends the
	// audit entry e in the same fenced transaction (ADR 60, fail closed). It
	// returns the reference of the restored stack, ErrNotFound if the
	// namespace has no deleted stack id, ErrAlreadyExists if a live stack
	// holds the name, namespace.ErrFenced if fence is stale, or
	// audit.ErrInvalidEntry if e fails validation.
	RestoreStack(ctx context.Context, id ID, newName Name, fence namespace.Fence, e audit.Entry) (Ref, error)
	// PurgeStack removes the deleted stack id of the namespace of fence for
	// good (ADR 47): its updates, checkpoints, events, journal, tags, data
	// key and deleted inventory entry, in one fenced transaction with the
	// audit entry e. It ends the grants on the stack and records the blob
	// purge (inventory.BlobPurge) in that transaction. The caller removes
	// its blobs after the commit; the sweep finishes the record. It
	// returns ErrNotFound if the namespace has no deleted stack id, which
	// also covers a live stack: a purge acts on deleted stacks only. With a
	// non-zero dueBy, it also returns ErrNotFound and changes nothing when
	// the retention period of the stack ends after dueBy, a check in the same
	// transaction. The retention sweep sets dueBy, and a purge by an admin
	// leaves it zero.
	PurgeStack(ctx context.Context, id ID, fence namespace.Fence, dueBy time.Time, e audit.Entry) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces *namespace.Service
	Now        func() time.Time
	// State resolves the latest checkpoint for the delete guard: the
	// service counts its deployment outside any transaction, since the
	// store keeps no resource count. Nil means the service passes
	// EmptyGuard, so it can delete only a stack without a checkpoint.
	State LatestState
	// Retention is how long a deleted stack stays before its final delete
	// (ADR 47, decision 2). Zero means inventory.DeletedRetention.
	Retention time.Duration
	// ManualLocks refuses the delete and the rename of a stack that a manual
	// lock covers. A nil port refuses nothing.
	ManualLocks ManualLocks
}

// Service implements the stack use cases.
type Service struct {
	store      Store
	namespaces *namespace.Service
	now        func() time.Time
	state      LatestState
	retention  time.Duration
	manual     ManualLocks
}

// NewService returns a Service. Store and Namespaces are required; Now defaults to time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: stack store", ErrMissingDependency)
	}

	if cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	retention := cfg.Retention
	if retention <= 0 {
		retention = inventory.DeletedRetention
	}

	return &Service{
		store: cfg.Store, namespaces: cfg.Namespaces, now: now,
		state: cfg.State, retention: retention, manual: cfg.ManualLocks,
	}, nil
}

// CreatePlaced creates the stack identified by ref and puts it in the
// collection of p in one fenced transaction. The write is fenced on the
// namespace's failover version and only allowed in its primary installation.
// A stack always has a collection: it returns an error that matches
// collection.ErrNoCollection, and makes no stack, when p names no collection,
// no checked collection ID, or a collection of another namespace. It returns
// ErrPlacementUnsupported, and makes no stack, when the store is not a
// PlacedStore.
func (s *Service) CreatePlaced(ctx context.Context, ref Ref, tags map[string]string, p Placement) (Stack, error) {
	// Defense in depth: the handler chooses the collection by a placement rule
	// and the store checks the collection in the transaction. This check stays
	// so that a new caller cannot make a stack that no collection holds.
	if !p.Complete(ref.Namespace) {
		return Stack{}, fmt.Errorf("create stack %s: %w", ref, collection.ErrNoCollection)
	}

	return createStack(ctx, s, ref, tags, p)
}

// createRows makes st in store with the membership of p. A store that is not a
// PlacedStore makes nothing.
func createRows(ctx context.Context, store Store, st Stack, p Placement, fence namespace.Fence) error {
	placed, ok := store.(PlacedStore)
	if !ok {
		return ErrPlacementUnsupported
	}

	p.Entry.At = st.CreatedAt

	return placed.CreateStackPlaced(ctx, st, p, fence)
}

func createStack(ctx context.Context, s *Service, ref Ref, tags map[string]string, p Placement) (Stack, error) {
	if len(tags) > maxTags {
		return Stack{}, fmt.Errorf("%w: more than %d tags", ErrInvalidName, maxTags)
	}

	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return Stack{}, fmt.Errorf("create stack %s: %w", ref, err)
	}

	now := s.now().UTC()
	st := Stack{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        maps.Clone(tags),
		Ref:         ref,
		ID:          ID(ident.NewID("stk-")),
		Version:     0,
	}

	if st.Tags == nil {
		st.Tags = map[string]string{}
	}

	err = createRows(ctx, s.store, st, p, ns.Fence())
	if err != nil {
		return Stack{}, fmt.Errorf("create stack %s: %w", ref, err)
	}

	return st, nil
}

// Get returns the stack identified by ref.
func (s *Service) Get(ctx context.Context, ref Ref) (Stack, error) {
	st, err := s.store.GetStack(ctx, ref)
	if err != nil {
		return Stack{}, fmt.Errorf("get stack %s: %w", ref, err)
	}

	return st, nil
}

// GetStack returns the stack identified by ref. Updates calls it for the
// stable ID in checkpoint blob keys.
func (s *Service) GetStack(ctx context.Context, ref Ref) (Stack, error) {
	return s.Get(ctx, ref)
}

// List returns the stacks matching filter.
func (s *Service) List(ctx context.Context, filter Filter) ([]Stack, error) {
	stacks, err := s.store.ListStacks(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list stacks in %q: %w", filter.Namespace, err)
	}

	return stacks, nil
}

// Page returns one page of the stacks that match q.
func (s *Service) Page(ctx context.Context, q PageQuery) ([]Stack, error) {
	stacks, err := s.store.QueryStacks(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query stacks: %w", err)
	}

	return stacks, nil
}

// Delete soft-deletes the stack identified by ref (ADR 47): the stack leaves
// every lookup by name and every list, and its name is free at once, but its
// history, state and data key stay until the final delete. When the stack
// still manages resources, it returns ErrHasResources and changes nothing.
// The service counts the one latest deployment outside any transaction
// (reading and gunzipping its blob) and the store checks it is still the
// latest in the delete transaction, retrying the count once on a concurrent
// save. The write is fenced on the namespace's failover version and only
// allowed in its primary installation. The delete and the audit entry e, stamped with
// the time of the delete, commit together or not at all (ADR 60, fail
// closed). It returns ErrNotFound if the stack does not exist, and
// ErrUpdateConflict while a mutating update of the stack runs or while a
// manual lock covers the stack.
func (s *Service) Delete(ctx context.Context, ref Ref, e audit.Entry) error {
	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	if lockErr := s.checkManualLock(ctx, ref, ""); lockErr != nil {
		return fmt.Errorf("delete stack %s: %w", ref, lockErr)
	}

	fence := ns.Fence()

	if s.state == nil {
		e.At = s.now().UTC()
		delErr := s.store.DeleteStack(
			ctx,
			ref,
			fence,
			EmptyGuard(),
			inventory.Deletion{Entry: e, PurgeAfter: e.At.Add(s.retention)},
		)
		if delErr != nil {
			return fmt.Errorf("delete stack %s: %w", ref, delErr)
		}

		return nil
	}

	for attempt := range 2 {
		guard, guardErr := s.deleteGuard(ctx, ref)
		if guardErr != nil {
			return fmt.Errorf("delete stack %s: %w", ref, guardErr)
		}

		e.At = s.now().UTC()
		err = s.store.DeleteStack(
			ctx,
			ref,
			fence,
			guard,
			inventory.Deletion{Entry: e, PurgeAfter: e.At.Add(s.retention)},
		)
		if !errors.Is(err, ErrConcurrentModification) || attempt == 1 {
			break
		}
	}

	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	return nil
}

// ForceDelete soft-deletes the stack identified by ref, as Delete does, even
// when the stack still manages resources. The write is fenced on the
// namespace's failover version and only allowed in its primary installation. It
// writes the audit entry e as Delete does. It returns ErrNotFound if the
// stack does not exist, and ErrUpdateConflict while a mutating update of the
// stack runs or while a manual lock covers the stack.
func (s *Service) ForceDelete(ctx context.Context, ref Ref, e audit.Entry) error {
	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	if lockErr := s.checkManualLock(ctx, ref, ""); lockErr != nil {
		return fmt.Errorf("delete stack %s: %w", ref, lockErr)
	}

	e.At = s.now().UTC()
	err = s.store.ForceDeleteStack(
		ctx,
		ref,
		ns.Fence(),
		inventory.Deletion{Entry: e, PurgeAfter: e.At.Add(s.retention)},
	)
	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	return nil
}

// RestoreDeleted brings back the deleted stack id of ns under newName in its
// project, or under its name before the delete when newName is empty (ADR
// 47). The store appends the audit entry e, stamped with the time of the
// restore, in the same fenced transaction (ADR 60, fail closed). It returns
// the reference of the restored stack, ErrNotFound if ns has no deleted
// stack id, and ErrAlreadyExists if a live stack holds the name.
func (s *Service) RestoreDeleted(
	ctx context.Context, ns namespace.Name, id ID, newName Name, e audit.Entry,
) (Ref, error) {
	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return Ref{}, fmt.Errorf("restore stack %s: %w", id, err)
	}

	// Defense in depth: the store refuses a restore under a manual lock in the
	// transaction of the restore, after the stack is live again. This check
	// stays so that the refusal does not rely on the store: a deleted stack has
	// no inventory entry, so it reads the lock of the namespace, and the store
	// check covers the lock of the stack and of its collection.
	if lockErr := refuseRestoreUnderNamespaceLock(ctx, s.manual, ns, id); lockErr != nil {
		return Ref{}, fmt.Errorf("restore stack %s: %w", id, lockErr)
	}

	e.At = s.now().UTC()

	ref, err := s.store.RestoreStack(ctx, id, newName, n.Fence(), e)
	if err != nil {
		return Ref{}, fmt.Errorf("restore stack %s: %w", id, err)
	}

	return ref, nil
}

// PurgeDeleted removes the deleted stack id of ns for good, before its retention
// period ends (ADR 47). The store appends the audit entry e in the same
// fenced transaction. The caller removes the checkpoint blobs after it, in the
// scope of the namespace ID that this call returns. It returns ErrNotFound if
// ns has no deleted stack id.
func (s *Service) PurgeDeleted(ctx context.Context, ns namespace.Name, id ID, e audit.Entry) (namespace.ID, error) {
	return s.purge(ctx, ns, id, time.Time{}, e)
}

// PurgeExpired removes the deleted stack id of ns for good, as PurgeDeleted
// does, only when its retention period ended at or before dueBy. The store
// checks the end of the retention in the purge transaction. It returns
// ErrNotFound if ns has no deleted stack id whose retention period ended by
// then: a restore and a new delete after the caller read the unit gave it a
// new retention period. It returns the ID of the namespace row that it
// resolved, for the delete of the blobs.
func (s *Service) PurgeExpired(
	ctx context.Context, ns namespace.Name, id ID, dueBy time.Time, e audit.Entry,
) (namespace.ID, error) {
	return s.purge(ctx, ns, id, dueBy, e)
}

// UpdateTags replaces the tags of the stack identified by ref with tags, as
// the Pulumi CLI sends them on PATCH, and returns the updated stack. An empty
// or nil map clears every tag. The write is fenced on the namespace's
// failover version and only allowed in its primary installation.
func (s *Service) UpdateTags(
	ctx context.Context, ref Ref, tags map[string]string,
) (Stack, error) {
	if len(tags) > maxTags {
		return Stack{}, fmt.Errorf("%w: more than %d tags", ErrInvalidName, maxTags)
	}

	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return Stack{}, fmt.Errorf("update tags of %s: %w", ref, err)
	}

	now := s.now().UTC()

	storeErr := s.store.UpdateStackTags(ctx, ref, maps.Clone(tags), now, ns.Fence())
	if storeErr != nil {
		return Stack{}, fmt.Errorf("update tags of %s: %w", ref, storeErr)
	}

	st, err := s.store.GetStack(ctx, ref)
	if err != nil {
		return Stack{}, fmt.Errorf("update tags of %s: %w", ref, err)
	}

	return st, nil
}

// Rename changes the stack name of oldRef to newName and returns the renamed
// stack. The stack ID stays the same. The write is fenced on the namespace's
// failover version and only allowed in its primary installation. The rename and
// the audit entry e, stamped with the time of the rename, commit together or
// not at all. It returns ErrUpdateConflict while a mutating update of the
// stack runs (a *UpdateConflictError that names that update), and
// ErrUpdateConflict, matching manuallock.ErrLocked, while a manual lock covers
// the stack.
func (s *Service) Rename(ctx context.Context, oldRef Ref, newName Name, e audit.Entry) (Stack, error) {
	ns, err := s.namespaces.WritableHere(ctx, oldRef.Namespace)
	if err != nil {
		return Stack{}, fmt.Errorf("rename stack %s: %w", oldRef, err)
	}

	// A rename that a manual lock allowed would leave the state of the stack
	// under the old name: the import that a rename needs next meets the lock.
	if lockErr := s.checkManualLock(ctx, oldRef, ""); lockErr != nil {
		return Stack{}, fmt.Errorf("rename stack %s: %w", oldRef, lockErr)
	}

	e.At = s.now().UTC()

	renameErr := s.store.RenameStack(ctx, oldRef, newName, ns.Fence(), e)
	if renameErr != nil {
		return Stack{}, fmt.Errorf("rename stack %s: %w", oldRef, renameErr)
	}

	st, err := s.store.GetStack(
		ctx,
		Ref{Namespace: oldRef.Namespace, Project: oldRef.Project, Stack: newName},
	)
	if err != nil {
		return Stack{}, fmt.Errorf("rename stack %s: %w", oldRef, err)
	}

	return st, nil
}

// checkManualLock refuses the request of job jobID on the stack ref while a
// manual lock covers the stack, and returns nil when the service has no manual
// lock port. A lock that cannot be read refuses the request: the check fails
// closed.
func (s *Service) checkManualLock(ctx context.Context, ref Ref, jobID string) error {
	if s.manual == nil {
		return nil
	}

	st, err := s.store.GetStack(ctx, ref)
	if err != nil {
		return fmt.Errorf("read stack %s: %w", ref, err)
	}

	return refuseManualLock(ctx, s.manual, st, jobID)
}

// deleteGuard counts the latest checkpoint of ref outside any transaction,
// through the export: resolved, digest-checked and unwrapped under the
// configured checkpoint size limit.
func (s *Service) deleteGuard(ctx context.Context, ref Ref) (DeleteGuard, error) {
	cp, err := s.state.Export(ctx, ref)
	if errors.Is(err, ErrNoState) {
		return EmptyGuard(), nil
	}

	if errors.Is(err, ErrCheckpointUnavailable) || errors.Is(err, ErrCheckpointMismatch) {
		// A blob read failure counts as resourced, not a delete failure.
		return DeleteGuard{
			UpdateID: cp.Update, Digest: cp.Digest,
			HasCheckpoint: true, HasResources: true,
		}, nil
	}

	if err != nil {
		return DeleteGuard{}, err
	}

	return DeleteGuard{
		UpdateID: cp.Update, Digest: cp.Digest,
		HasCheckpoint: true, HasResources: CountManagedResources(cp.Deployment) > 0,
	}, nil
}

// purge removes the deleted stack id of ns with the retention check of
// dueBy, which zero turns off.
func (s *Service) purge(
	ctx context.Context, ns namespace.Name, id ID, dueBy time.Time, e audit.Entry,
) (namespace.ID, error) {
	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("purge stack %s: %w", id, err)
	}

	e.At = s.now().UTC()

	if err := s.store.PurgeStack(ctx, id, n.Fence(), dueBy, e); err != nil {
		return "", fmt.Errorf("purge stack %s: %w", id, err)
	}

	return n.ID, nil
}

// refuseRestoreUnderNamespaceLock returns an error that matches
// manuallock.ErrLocked while a manual lock covers the namespace ns, and nil when
// locks is nil or the namespace has no lock. A lock that cannot be read refuses
// the restore: the check fails closed.
func refuseRestoreUnderNamespaceLock(ctx context.Context, locks ManualLocks, ns namespace.Name, id ID) error {
	if locks == nil {
		return nil
	}

	l, err := locks.NamespaceLock(ctx, ns)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of namespace %s: %w", ns, err)
	}

	// A deleted stack has no live name yet, so the refused unit names its
	// stable ID.
	return &manuallock.LockedError{
		Lock: l, Unit: collection.UnitRef{Namespace: ns, Tool: InventoryTool, Name: id.String()},
		Noun: "stack", Name: ns.String() + "/" + id.String(),
	}
}
