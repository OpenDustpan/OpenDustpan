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
	FailoverVersion int64
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

// Store is the persistence port for namespaces.
type Store interface {
	// CreateNamespace persists ns. It returns ErrAlreadyExists if the name is taken.
	CreateNamespace(ctx context.Context, ns Namespace) error
	// GetNamespace returns the namespace called name, or ErrNotFound.
	GetNamespace(ctx context.Context, name Name) (Namespace, error)
	// ListNamespaces returns every namespace, ordered by name.
	ListNamespaces(ctx context.Context) ([]Namespace, error)
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

// Create creates a namespace homed in the service's region, at failover version 1.
func (s *Service) Create(ctx context.Context, name Name) (Namespace, error) {
	ns := Namespace{
		CreatedAt:       s.now().UTC(),
		Name:            name,
		HomeRegion:      s.region,
		FailoverVersion: 1,
	}

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
