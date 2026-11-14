package ipallow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrDisabled reports that IP allowlists are off for this installation.
var ErrDisabled = errors.New("ipallow: IP allowlists are off for this installation")

// Store is the persistence port of the lists. It is served by the SQL
// adapters from the same connection as the other ports.
type Store interface {
	// NamespaceList returns the list of the namespace ns. The zero List means
	// the namespace has no rule. It returns namespace.ErrNotFound for an
	// unknown namespace.
	NamespaceList(ctx context.Context, ns namespace.Name) (List, error)
	// SetNamespaceList replaces the list of ns; the zero List removes the rule.
	// It checks fence in the same transaction (namespace.ErrFenced) and appends
	// the audit entry e to the chain of ns in that transaction (ADR 60, fail
	// closed): the change and the entry commit together or not at all. It
	// returns namespace.ErrNotFound for an unknown namespace.
	SetNamespaceList(ctx context.Context, ns namespace.Name, l List, fence namespace.Fence, e audit.Entry) error
	// NamespacesWithList returns the names of the namespaces that have a
	// non-zero list, in name order, and an empty slice when no namespace has
	// a list. It returns namespace.ErrInvalidName (wrapped) when a stored name
	// is not a valid namespace name, and a wrapped store error when the read
	// fails. A caller fails closed on any error: it treats the set of left-out
	// namespaces as unknown and does not cover every object.
	NamespacesWithList(ctx context.Context) ([]namespace.Name, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces *namespace.Service
	Now        func() time.Time
	Enabled    bool
}

// Service implements the IP allowlist use cases.
type Service struct {
	store      Store
	namespaces *namespace.Service
	now        func() time.Time
	enabled    bool
}

// NewService returns a Service. Store and Namespaces are required; Now
// defaults to time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: ipallow store", namespace.ErrMissingDependency)
	}

	if cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: namespace service", namespace.ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{store: cfg.Store, namespaces: cfg.Namespaces, now: now, enabled: cfg.Enabled}, nil
}

// Enabled reports whether IP allowlists are on for this installation.
func (s *Service) Enabled() bool {
	return s.enabled
}

// Get returns the list of the namespace ns. The zero List means the namespace
// has no rule.
func (s *Service) Get(ctx context.Context, ns namespace.Name) (List, error) {
	l, err := s.store.NamespaceList(ctx, ns)
	if err != nil {
		return List{}, fmt.Errorf("get IP allowlist of namespace %q: %w", ns, err)
	}

	return l, nil
}

// Set replaces the list of ns with the parsed items; a list of zero items is
// refused with ErrInvalidList (use Remove to opt out). When the feature is off
// it returns ErrDisabled. The write needs the namespace to be writable in this
// installation and is fenced. e is the audit entry of the caller; Set stamps e.At
// with the time in UTC and sets e.Namespace to ns.
func (s *Service) Set(ctx context.Context, ns namespace.Name, items []string, e audit.Entry) (List, error) {
	if !s.enabled {
		return List{}, ErrDisabled
	}

	if len(items) == 0 {
		return List{}, fmt.Errorf("%w: no addresses, use Remove to opt out", ErrInvalidList)
	}

	l, err := ParseList(items)
	if err != nil {
		return List{}, err
	}

	writable, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return List{}, fmt.Errorf("set IP allowlist of namespace %q: %w", ns, err)
	}

	e.At = s.now().UTC()
	e.Namespace = ns

	if err := s.store.SetNamespaceList(ctx, ns, l, writable.Fence(), e); err != nil {
		return List{}, fmt.Errorf("set IP allowlist of namespace %q: %w", ns, err)
	}

	return l, nil
}

// Remove removes the rule of ns (opt out). The same checks as Set, except
// that the feature switch does not matter: an administrator can always remove
// a rule.
func (s *Service) Remove(ctx context.Context, ns namespace.Name, e audit.Entry) error {
	writable, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return fmt.Errorf("remove IP allowlist of namespace %q: %w", ns, err)
	}

	e.At = s.now().UTC()
	e.Namespace = ns

	if err := s.store.SetNamespaceList(ctx, ns, List{prefixes: nil}, writable.Fence(), e); err != nil {
		return fmt.Errorf("remove IP allowlist of namespace %q: %w", ns, err)
	}

	return nil
}
