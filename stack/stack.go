// Package stack models projects and stacks inside a namespace: their identity,
// tags and lifecycle (Service), and the update cycle the Pulumi CLI drives
// against them: updates, leases, checkpoints and engine events (Updates).
package stack

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/namespace"
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
	// and checkpoints in one fenced transaction. It returns ErrNotFound if the
	// stack does not exist, ErrUpdateConflict while a mutating update of the
	// stack runs, namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale.
	DeleteStack(ctx context.Context, ref Ref, fence namespace.Fence) error
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

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces *namespace.Service
	Now        func() time.Time
}

// Service implements the stack use cases.
type Service struct {
	store      Store
	namespaces *namespace.Service
	now        func() time.Time
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

	return &Service{store: cfg.Store, namespaces: cfg.Namespaces, now: now}, nil
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

// List returns the stacks matching filter.
func (s *Service) List(ctx context.Context, filter Filter) ([]Stack, error) {
	stacks, err := s.store.ListStacks(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list stacks in %q: %w", filter.Namespace, err)
	}

	return stacks, nil
}

// Delete removes the stack identified by ref with its updates, events and
// checkpoints. The write is fenced on the namespace's failover version and
// only allowed in its home region. It returns ErrNotFound if the stack does
// not exist, and ErrUpdateConflict while a mutating update of the stack runs.
func (s *Service) Delete(ctx context.Context, ref Ref) error {
	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return fmt.Errorf("delete stack %s: %w", ref, err)
	}

	if err := s.store.DeleteStack(ctx, ref, ns.Fence()); err != nil {
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
