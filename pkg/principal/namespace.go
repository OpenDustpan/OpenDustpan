package principal

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Namespaces reads the namespace of a principal of a namespace, for the
// fence of its writes. namespace.Store meets it.
type Namespaces interface {
	// GetNamespace returns the namespace called name, or
	// namespace.ErrNotFound.
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// NamespaceInput names the fields of a new principal of a namespace. An
// empty display name takes the subject.
type NamespaceInput struct {
	Subject     string
	DisplayName string
}

// CreateNamespacePrincipal validates and stores a new service principal of
// the namespace ns, with the entry e. The entry goes to the audit log of ns.
// It returns namespace.ErrNotFound when ns does not exist.
func (s *Service) CreateNamespacePrincipal(
	ctx context.Context, e audit.Entry, ns namespace.Name, in NamespaceInput,
) (Principal, error) {
	fence, err := s.fenceOf(ctx, ns)
	if err != nil {
		return Principal{}, err
	}

	displayName := in.DisplayName
	if displayName == "" {
		displayName = in.Subject
	}

	at := s.Now()
	p := Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: s.newID(principalIDPrefix),
		Subject: in.Subject, DisplayName: displayName, Kind: KindService, Source: SourceLocal,
		Namespace: ns, GroupCount: 0, BreakGlass: false,
	}

	if validErr := p.Validate(); validErr != nil {
		return Principal{}, validErr
	}

	err = s.store.CreateNamespacePrincipal(ctx, p, fence, namespaceChange(e, at, ns, Ref(p.ID)))
	if err != nil {
		return Principal{}, fmt.Errorf("create principal %q in namespace %q: %w", in.Subject, ns.String(), err)
	}

	return p, nil
}

// GetNamespacePrincipal returns the principal with the ID id of the
// namespace ns. A principal of another namespace, or of the installation,
// answers ErrNotFound.
func (s *Service) GetNamespacePrincipal(ctx context.Context, ns namespace.Name, id string) (Principal, error) {
	p, err := s.store.GetPrincipal(ctx, id)
	if err != nil {
		return Principal{}, fmt.Errorf("get principal %q: %w", id, err)
	}

	if p.Namespace != ns {
		return Principal{}, fmt.Errorf("get principal %q in namespace %q: %w", id, ns.String(), ErrNotFound)
	}

	return p, nil
}

// checkInNamespace returns ErrNotFound when the principal with the ID id is
// not a principal of the namespace ns.
func (s *Service) checkInNamespace(ctx context.Context, ns namespace.Name, id string) error {
	_, err := s.GetNamespacePrincipal(ctx, ns, id)

	return err
}

// ListNamespacePrincipals returns one page of the principals of the
// namespace ns, ordered by subject. The prefix matches the start of the
// subject, exactly.
func (s *Service) ListNamespacePrincipals(
	ctx context.Context, ns namespace.Name, req PageRequest,
) (Page[Principal], error) {
	q, limit, err := req.query(exactKey)
	if err != nil {
		return Page[Principal]{}, err
	}

	rows, total, err := s.store.ListNamespacePrincipals(ctx, ns, q)
	if err != nil {
		return Page[Principal]{}, fmt.Errorf("list principals of namespace %q: %w", ns.String(), err)
	}

	return pageOf(rows, total, limit, func(p Principal) string { return p.Subject }), nil
}

// UpdateNamespacePrincipal applies c to the principal with the ID id of the
// namespace ns, with the entry e. A principal of another namespace answers
// ErrNotFound.
func (s *Service) UpdateNamespacePrincipal(
	ctx context.Context, e audit.Entry, ns namespace.Name, id string, c Change,
) (Principal, error) {
	if err := s.checkInNamespace(ctx, ns, id); err != nil {
		return Principal{}, err
	}

	return s.UpdatePrincipal(ctx, e, id, c)
}

// DeleteNamespacePrincipal ends the principal with the ID id of the
// namespace ns, with the entry e. A principal of another namespace answers
// ErrNotFound.
func (s *Service) DeleteNamespacePrincipal(
	ctx context.Context, e audit.Entry, caller identity.Principal, ns namespace.Name, id string,
) error {
	if err := s.checkInNamespace(ctx, ns, id); err != nil {
		return err
	}

	return s.DeletePrincipal(ctx, e, caller, id)
}

// storeUpdate writes the update of p with the entry e: through the writes
// of a namespace, with its fence, for a principal of a namespace.
func (s *Service) storeUpdate(ctx context.Context, e audit.Entry, p Principal) error {
	if p.Namespace == "" {
		return s.store.UpdatePrincipal(ctx, p, change(e, p.UpdatedAt, Ref(p.ID)))
	}

	fence, err := s.fenceOf(ctx, p.Namespace)
	if err != nil {
		return err
	}

	return s.store.UpdateNamespacePrincipal(ctx, p, fence, namespaceChange(e, p.UpdatedAt, p.Namespace, Ref(p.ID)))
}

// endNamespacePrincipal ends p, a principal of a namespace, with the
// fence of its namespace.
func (s *Service) endNamespacePrincipal(ctx context.Context, e audit.Entry, p Principal) error {
	fence, err := s.fenceOf(ctx, p.Namespace)
	if err != nil {
		return err
	}

	return s.store.DeleteNamespacePrincipal(ctx, p.ID, fence, namespaceChange(e, s.Now(), p.Namespace, Ref(p.ID)))
}

// fenceOf returns the fence of the namespace ns. A Service with no
// Namespaces keeps no principal of a namespace: it returns
// ErrMissingDependency.
func (s *Service) fenceOf(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	if s.namespaces == nil {
		return namespace.Fence{}, fmt.Errorf("%w: namespaces, for the principals of a namespace", ErrMissingDependency)
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %q: %w", ns.String(), err)
	}

	return n.Fence(), nil
}

// namespaceChange completes the entry of the caller for one change of the
// record object of a principal of the namespace ns at the time at: the
// entry goes to the audit log of ns.
func namespaceChange(e audit.Entry, at time.Time, ns namespace.Name, object string) audit.Entry {
	e = change(e, at, object)
	e.Namespace = ns

	return e
}
