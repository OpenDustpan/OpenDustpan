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
	"strings"
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
	// ErrInvalidID reports a namespace ID that fails validation.
	ErrInvalidID = errors.New("invalid namespace ID")
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
	// ErrNoPaging reports a namespace store that does not page namespaces:
	// it does not implement Pager.
	ErrNoPaging = errors.New("the namespace store does not page namespaces")
	// ErrInvalidPage reports a page query with a limit outside 1 to
	// maxPageLimit.
	ErrInvalidPage = errors.New("invalid namespace page")
)

const maxNameLength = 100

// maxIDLength is the longest namespace ID, prefix included.
const maxIDLength = 100

// maxPageLimit is the largest page size a page query can name. It holds one
// row more than the largest page a caller can ask for, so a handler that
// reads limit+1 to find the next page stays inside it.
const maxPageLimit = 1001

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

// idPrefix starts every namespace ID.
const idPrefix = "ns-"

// ID is the stable, opaque identifier of a namespace ("ns-..."). The server
// makes it when it creates the namespace, in the home region, and it never
// changes. A namespace that is created later under the name of a purged
// namespace gets a new ID, so rows that hold the old ID never reach the new
// namespace. The ID is not a secret, and it does not sort by time.
type ID string

// NewID returns a new namespace ID: the prefix "ns-" and 20 random lowercase
// letters and digits.
func NewID() ID {
	return ID(ident.NewID(idPrefix))
}

// ParseID validates s as a namespace ID: the prefix "ns-", then ASCII
// letters, digits, '-', '_' and '.', at most 100 characters in total.
func ParseID(s string) (ID, error) {
	if !strings.HasPrefix(s, idPrefix) || len(s) == len(idPrefix) || !ident.Valid(s, maxIDLength) {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, s)
	}

	return ID(s), nil
}

// String returns the ID as a plain string.
func (id ID) String() string {
	return string(id)
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
	ID              ID
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
	// CreateNamespace persists ns. It returns ErrInvalidID if ns.ID fails
	// ParseID, and ErrAlreadyExists if the name or the ID is taken. A store
	// never changes the ID of a namespace. A purge of a namespace deletes its
	// row. A later namespace of the same name has a new ID, so it gets no row
	// that holds the ID of the purged one.
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

// Filter names which namespaces qualify for a page or a count. With All
// set, every namespace qualifies; otherwise only the namespaces named in
// Names qualify (unknown names are ignored; empty Names gives none).
type Filter struct {
	All   bool
	Names []Name
}

// PageQuery asks for one page of namespaces, in the order of
// ListNamespaces, which is the order of the name column of the store. After
// is the name of the last namespace of the page before, or empty for the
// first page. Limit is 1 to maxPageLimit. Filter names which namespaces
// qualify.
type PageQuery struct {
	After  Name
	Limit  int
	Filter Filter
}

// Pager reads namespaces one page at a time. It is an optional port: a
// Store that does not implement it answers ErrNoPaging through Service.
type Pager interface {
	// PageNamespaces returns at most Limit namespaces after After, in the
	// order of ListNamespaces, which is the order of the name column of the
	// store. Filter names which namespaces qualify.
	PageNamespaces(ctx context.Context, q PageQuery) ([]Namespace, error)
	// CountNamespaces returns how many namespaces qualify under Filter.
	CountNamespaces(ctx context.Context, f Filter) (int64, error)
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

// New returns the namespace that Create stores for name: a new ID, homed in
// the service's region, at failover version 1, created now. It stores nothing,
// so a caller can store it with other rows in one transaction.
func (s *Service) New(name Name) Namespace {
	return Namespace{
		CreatedAt:       s.now().UTC(),
		ID:              NewID(),
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

// PageNamespaces returns one page of namespaces through the Pager port of
// the store. It returns ErrInvalidPage when the limit is not 1 to
// maxPageLimit, and ErrNoPaging when the store does not implement Pager.
// Names are deduplicated before the call, and the slice is not modified.
func (s *Service) PageNamespaces(ctx context.Context, q PageQuery) ([]Namespace, error) {
	if q.Limit < 1 || q.Limit > maxPageLimit {
		return nil, fmt.Errorf("%w: limit %d is not 1 to %d", ErrInvalidPage, q.Limit, maxPageLimit)
	}

	pager, ok := s.store.(Pager)
	if !ok {
		return nil, ErrNoPaging
	}

	page, err := pager.PageNamespaces(ctx, PageQuery{
		After:  q.After,
		Limit:  q.Limit,
		Filter: Filter{All: q.Filter.All, Names: dedupNames(q.Filter.Names)},
	})
	if err != nil {
		return nil, fmt.Errorf("page namespaces: %w", err)
	}

	return page, nil
}

// CountNamespaces returns how many namespaces qualify under Filter through
// the Pager port of the store. It returns ErrNoPaging when the store does
// not implement Pager. Names are deduplicated before the call, and the
// slice is not modified.
func (s *Service) CountNamespaces(ctx context.Context, f Filter) (int64, error) {
	pager, ok := s.store.(Pager)
	if !ok {
		return 0, ErrNoPaging
	}

	total, err := pager.CountNamespaces(ctx, Filter{All: f.All, Names: dedupNames(f.Names)})
	if err != nil {
		return 0, fmt.Errorf("count namespaces: %w", err)
	}

	return total, nil
}

// dedupNames returns the names of in without duplicates, keeping the first
// order. It builds a new slice, so in is not modified.
func dedupNames(in []Name) []Name {
	out := make([]Name, 0, len(in))
	seen := make(map[Name]struct{}, len(in))

	for _, name := range in {
		if _, ok := seen[name]; ok {
			continue
		}

		seen[name] = struct{}{}
		out = append(out, name)
	}

	return out
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
