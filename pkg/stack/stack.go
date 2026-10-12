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
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports that a stack does not exist.
	ErrNotFound = errors.New("stack not found")
	// ErrAlreadyExists reports that a stack with the same reference exists.
	ErrAlreadyExists = errors.New("stack already exists")
	// ErrInvalidName reports a project or stack name that fails validation.
	ErrInvalidName = errors.New("invalid project or stack name")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrHasResources reports that a stack still manages resources, so a
	// delete without force is refused.
	ErrHasResources = errors.New("stack still contains resources")
)

const (
	maxNameLength = 100
	maxTags       = 64
)

// ProjectName is a validated project name.
type ProjectName string

// ParseProjectName validates s as a project name.
func ParseProjectName(s string) (ProjectName, error) {
	if !ident.Valid(s, maxNameLength) {
		return "", fmt.Errorf("%w: project %q", ErrInvalidName, s)
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
	if !ident.Valid(s, maxNameLength) {
		return "", fmt.Errorf("%w: stack %q", ErrInvalidName, s)
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
// store delete transaction. Saves store a NULL resource count and blob rows
// keep no deployment in SQL, so the service counts the one latest deployment
// outside any transaction (reading and gunzipping its blob) and the store
// checks the checkpoint is still the one counted. UpdateID with Digest
// identify the counted checkpoint; HasCheckpoint is false when the stack had
// no checkpoint; HasResources reports whether it managed resources.
type DeleteGuard struct {
	UpdateID      UpdateID
	Digest        [sha256.Size]byte
	HasCheckpoint bool
	HasResources  bool
}

// EmptyGuard returns the DeleteGuard for a delete with no domain count: the
// store uses only its SQL fallback. Tests with inline checkpoints and stacks
// without checkpoints use it; production deletes with blob checkpoints pass
// the counted guard instead.
func EmptyGuard() DeleteGuard {
	//nolint:exhaustruct_v5 // An empty guard means the stack had no checkpoint to count.
	return DeleteGuard{}
}

// Store is the persistence port for stacks. Tags live in their own table
// keyed by the stack ID, so a rename keeps them without moving any tag row.
type Store interface {
	// CreateStack persists st if fence matches the namespace's current failover
	// version. It returns ErrAlreadyExists, namespace.ErrNotFound or
	// namespace.ErrFenced.
	CreateStack(ctx context.Context, st Stack, fence namespace.Fence) error
	// GetStack returns the stack identified by ref, or ErrNotFound.
	GetStack(ctx context.Context, ref Ref) (Stack, error)
	// ListStacks returns the stacks matching filter, ordered by project then name.
	ListStacks(ctx context.Context, filter Filter) ([]Stack, error)
	// DeleteStack removes the stack identified by ref with its updates, events
	// and checkpoints in one fenced transaction. When the latest checkpoint
	// manages resources, it returns ErrHasResources and removes nothing.
	// Rows with a stored count use it; rows with SQL deployment content count
	// it; rows with a NULL count and blob content use guard, which the caller
	// counted outside the transaction. When the latest checkpoint is no
	// longer the one guard names, it returns ErrConcurrentModification so the
	// caller recounts once. The check runs in the same transaction as the
	// delete, so an update that finishes concurrently cannot drop resources.
	// It returns ErrNotFound if the stack does not exist, ErrUpdateConflict
	// while a mutating update of the stack runs, namespace.ErrNotFound if the
	// namespace does not exist, or namespace.ErrFenced if fence is stale.
	DeleteStack(ctx context.Context, ref Ref, fence namespace.Fence, guard DeleteGuard) error
	// ForceDeleteStack removes the stack identified by ref with its updates,
	// events and checkpoints in one fenced transaction, even when the latest
	// checkpoint manages resources. It returns ErrNotFound if the stack does
	// not exist, ErrUpdateConflict while a mutating update of the stack runs,
	// namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale.
	ForceDeleteStack(ctx context.Context, ref Ref, fence namespace.Fence) error
	// UpdateStackTags replaces the tags of the stack identified by ref with
	// tags, keyed by its stable ID, and stamps updatedAt. It returns
	// ErrNotFound, namespace.ErrNotFound or namespace.ErrFenced.
	UpdateStackTags(
		ctx context.Context, ref Ref, tags map[string]string, updatedAt time.Time, fence namespace.Fence,
	) error
	// RenameStack changes the stack name of oldRef to newName if fence matches
	// the namespace's current failover version. The stack ID and every child
	// row stay with the stack. It returns ErrNotFound for a missing stack,
	// ErrAlreadyExists for a taken name, namespace.ErrNotFound for a missing
	// namespace or namespace.ErrFenced for a stale fence.
	RenameStack(ctx context.Context, oldRef Ref, newName Name, fence namespace.Fence) error
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

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces *namespace.Service
	Now        func() time.Time
	// State resolves the latest checkpoint for the delete guard: the
	// service counts its deployment outside any transaction, since saves
	// store a NULL count and blob rows keep no deployment in SQL. Nil
	// means the service passes an empty guard and the store uses only its
	// SQL fallback, as in tests with inline checkpoints.
	State LatestState
}

// Service implements the stack use cases.
type Service struct {
	store      Store
	namespaces *namespace.Service
	now        func() time.Time
	state      LatestState
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

	return &Service{
		store: cfg.Store, namespaces: cfg.Namespaces, now: now,
		state: cfg.State,
	}, nil
}

// Create creates the stack identified by ref with the given tags. The write is
// fenced on the namespace's failover version and only allowed in its home region.
func (s *Service) Create(ctx context.Context, ref Ref, tags map[string]string) (Stack, error) {
	if len(tags) > maxTags {
		return Stack{}, fmt.Errorf("%w: more than %d tags", ErrInvalidName, maxTags)
	}

	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return Stack{}, fmt.Errorf("create stack %s: %w", ref, err)
	}

	now := s.now().UTC()
	st := Stack{
		CreatedAt: now,
		UpdatedAt: now,
		Tags:      maps.Clone(tags),
		Ref:       ref,
		ID:        ID(ident.NewID("stk-")),
		Version:   0,
	}

	if st.Tags == nil {
		st.Tags = map[string]string{}
	}

	if err := s.store.CreateStack(ctx, st, ns.Fence()); err != nil {
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

// Delete removes the stack identified by ref with its updates, events and
// checkpoints. When the stack still manages resources, it returns
// ErrHasResources and removes nothing. The service counts the one latest
// deployment outside any transaction (reading and gunzipping its blob) and
// the store checks it is still the latest in the delete transaction, retrying
// the count once on a concurrent save. The write is fenced on the
// namespace's failover version and only allowed in its home region. It
// returns ErrNotFound if the stack does not exist, and ErrUpdateConflict
// while a mutating update of the stack runs.
func (s *Service) Delete(ctx context.Context, ref Ref) error {
	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	fence := ns.Fence()

	if s.state == nil {
		if delErr := s.store.DeleteStack(ctx, ref, fence, EmptyGuard()); delErr != nil {
			return fmt.Errorf("delete stack %s: %w", ref, delErr)
		}

		return nil
	}

	for attempt := range 2 {
		guard, guardErr := s.deleteGuard(ctx, ref)
		if guardErr != nil {
			return fmt.Errorf("delete stack %s: %w", ref, guardErr)
		}

		err = s.store.DeleteStack(ctx, ref, fence, guard)
		if !errors.Is(err, ErrConcurrentModification) || attempt == 1 {
			break
		}
	}

	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	return nil
}

// ForceDelete removes the stack identified by ref with its updates, events
// and checkpoints, even when the stack still manages resources. The write is
// fenced on the namespace's failover version and only allowed in its home
// region. It returns ErrNotFound if the stack does not exist, and
// ErrUpdateConflict while a mutating update of the stack runs.
func (s *Service) ForceDelete(ctx context.Context, ref Ref) error {
	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	if err := s.store.ForceDeleteStack(ctx, ref, ns.Fence()); err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	return nil
}

// UpdateTags replaces the tags of the stack identified by ref with tags, as
// the Pulumi CLI sends them on PATCH, and returns the updated stack. An empty
// or nil map clears every tag. The write is fenced on the namespace's
// failover version and only allowed in its home region.
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
// failover version and only allowed in its home region.
func (s *Service) Rename(ctx context.Context, oldRef Ref, newName Name) (Stack, error) {
	ns, err := s.namespaces.WritableHere(ctx, oldRef.Namespace)
	if err != nil {
		return Stack{}, fmt.Errorf("rename stack %s: %w", oldRef, err)
	}

	renameErr := s.store.RenameStack(ctx, oldRef, newName, ns.Fence())
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
