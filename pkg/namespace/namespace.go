// Package namespace models namespaces, the unit of tenancy in OpenDustpan.
//
// A namespace is what Pulumi Cloud calls an organization. Every namespace is
// authoritative in exactly one region, its home region, and carries a failover
// version that increases each time an administrator promotes another region.
// Writes are fenced on that version so a demoted region cannot keep accepting
// writes after a promotion.
package namespace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports that a namespace does not exist.
	ErrNotFound = errors.New("namespace not found")
	// ErrAlreadyExists reports that a namespace with the same name exists.
	ErrAlreadyExists = errors.New("namespace already exists")
	// ErrInvalidName reports a name that fails validation.
	ErrInvalidName = errors.New("invalid namespace name")
	// ErrInvalidRegion reports a region that fails validation.
	ErrInvalidRegion = errors.New("invalid region")
	// ErrNotHome reports a write attempted outside the namespace's home region.
	ErrNotHome = errors.New("namespace is not homed in this region")
	// ErrFenced reports a write rejected because its fence carries a stale
	// failover version: the namespace was promoted in another region.
	ErrFenced = errors.New("write fenced: stale namespace failover version")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrUnavailable reports that the database does not answer. Adapters
	// return it without driver text. Callers must use errors.Is to check
	// for it, not string matching.
	ErrUnavailable = errors.New("database unavailable")
)

const maxNameLength = 100

// Name is a validated namespace name.
type Name string

// ParseName validates s as a namespace name: 1 to 100 characters of ASCII
// letters, digits, '-', '_' and '.', not starting with '.' or '-'.
func ParseName(s string) (Name, error) {
	if !ident.Valid(s, maxNameLength) {
		return "", fmt.Errorf("%w: %q", ErrInvalidName, s)
	}

	return Name(s), nil
}

// String returns the name as a plain string.
func (n Name) String() string {
	return string(n)
}

// Region identifies a deployment region, for example "eu-west".
type Region string

// ParseRegion validates s as a region identifier.
func ParseRegion(s string) (Region, error) {
	if !ident.Valid(s, maxNameLength) {
		return "", fmt.Errorf("%w: %q", ErrInvalidRegion, s)
	}

	return Region(s), nil
}

// String returns the region as a plain string.
func (r Region) String() string {
	return string(r)
}

// Namespace is the aggregate root for tenancy.
type Namespace struct {
	CreatedAt       time.Time
	Name            Name
	HomeRegion      Region
	Overrides       Overrides
	FailoverVersion int64
}

// Overrides are the switches that allow the overrides of ADR 44. Both are
// off by default. A namespace switch sets the outer bound of its units: a
// unit switch can only narrow it, so an override needs both to be on.
type Overrides struct {
	// Forced allows a forced state write and an unfenced force-unlock of a
	// client-encrypted state (unit:write-forced).
	Forced bool
	// Unlocked allows a write to an unlocked unit (unit:write-unlocked).
	Unlocked bool
}

// And returns the overrides that both o and other allow. A unit switch
// narrows its namespace switch with And.
func (o Overrides) And(other Overrides) Overrides {
	return Overrides{
		Forced:   o.Forced && other.Forced,
		Unlocked: o.Unlocked && other.Unlocked,
	}
}

// Fence returns the fencing token that writes inside this namespace must carry.
func (n Namespace) Fence() Fence {
	return Fence{Namespace: n.Name, FailoverVersion: n.FailoverVersion}
}

// Fence is the fencing token for writes inside a namespace. A Store must reject
// a write whose FailoverVersion does not match the namespace's current one.
type Fence struct {
	Namespace       Name
	FailoverVersion int64
}

// Store is the persistence port for namespaces. It is the root of the data:
// every SQL adapter serves all ports from one database connection, and
// dustpan.go already holds this port, so the readiness check reaches the
// database through it with no new option.
type Store interface {
	// CreateNamespace persists ns. It returns ErrAlreadyExists if the name is taken.
	CreateNamespace(ctx context.Context, ns Namespace) error
	// GetNamespace returns the namespace called name, or ErrNotFound.
	GetNamespace(ctx context.Context, name Name) (Namespace, error)
	// SetOverrides replaces the override switches of the namespace called
	// name. It checks fence in the same transaction, and returns ErrFenced when
	// the fence is stale, and ErrNotFound when the namespace does not exist.
	SetOverrides(ctx context.Context, name Name, ov Overrides, fence Fence) error
	// ListNamespaces returns every namespace, ordered by name.
	ListNamespaces(ctx context.Context) ([]Namespace, error)
	// Ping returns nil when the database answers within ctx. It returns
	// ErrUnavailable otherwise. It never returns driver text.
	Ping(ctx context.Context) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Store  Store
	Now    func() time.Time
	Region Region
}

// Service implements the namespace use cases.
type Service struct {
	store  Store
	now    func() time.Time
	region Region
}

// NewService returns a Service. Store and Region are required; Now defaults to time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: namespace store", ErrMissingDependency)
	}

	if _, err := ParseRegion(cfg.Region.String()); err != nil {
		return nil, err
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{store: cfg.Store, now: now, region: cfg.Region}, nil
}

// New returns the namespace that Create stores for name: homed in the
// service's region, at failover version 1, created now. It stores nothing,
// so a caller can store it with other rows in one transaction.
func (s *Service) New(name Name) Namespace {
	return Namespace{
		CreatedAt:       s.now().UTC(),
		Name:            name,
		HomeRegion:      s.region,
		FailoverVersion: 1,
		Overrides:       Overrides{Forced: false, Unlocked: false},
	}
}

// Create creates a namespace homed in the service's region, at failover version 1.
func (s *Service) Create(ctx context.Context, name Name) (Namespace, error) {
	ns := s.New(name)

	if err := s.store.CreateNamespace(ctx, ns); err != nil {
		return Namespace{}, fmt.Errorf("create namespace %q: %w", name, err)
	}

	return ns, nil
}

// Get returns the namespace called name.
func (s *Service) Get(ctx context.Context, name Name) (Namespace, error) {
	ns, err := s.store.GetNamespace(ctx, name)
	if err != nil {
		return Namespace{}, fmt.Errorf("get namespace %q: %w", name, err)
	}

	return ns, nil
}

// SetOverrides replaces the override switches of the namespace called name
// and returns the namespace as stored.
func (s *Service) SetOverrides(ctx context.Context, name Name, ov Overrides) (Namespace, error) {
	current, err := s.store.GetNamespace(ctx, name)
	if err != nil {
		return Namespace{}, fmt.Errorf("set overrides of namespace %q: %w", name, err)
	}

	if err := s.store.SetOverrides(ctx, name, ov, current.Fence()); err != nil {
		return Namespace{}, fmt.Errorf("set overrides of namespace %q: %w", name, err)
	}

	return s.Get(ctx, name)
}

// List returns every namespace, ordered by name.
func (s *Service) List(ctx context.Context) ([]Namespace, error) {
	namespaces, err := s.store.ListNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	return namespaces, nil
}

// WritableHere returns the namespace if this region may write to it. It returns
// ErrNotHome when the namespace is authoritative in another region.
func (s *Service) WritableHere(ctx context.Context, name Name) (Namespace, error) {
	ns, err := s.Get(ctx, name)
	if err != nil {
		return Namespace{}, err
	}

	if ns.HomeRegion != s.region {
		return Namespace{}, fmt.Errorf("%w: %q is homed in %q", ErrNotHome, name, ns.HomeRegion)
	}

	return ns, nil
}
