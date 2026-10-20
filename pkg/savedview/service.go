package savedview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// idPrefix starts the ID of every view.
const idPrefix = "view-"

// ErrNotPerson reports a caller that is not a person. Only a person makes a
// saved view: every other kind of principal, and a kind that the server does
// not know, is refused.
var ErrNotPerson = errors.New("only a person has saved views: sign in as a person")

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// NewID returns a new view ID: the prefix and 32 lowercase hex digits from
// the system random source.
func NewID() ID {
	var b [16]byte

	// crypto/rand.Read does not return an error on any supported platform.
	_, _ = rand.Read(b[:]) //nolint:errcheck // See above.

	return ID(idPrefix + hex.EncodeToString(b[:]))
}

// Namespaces reads the namespace that a write fences. namespace.Store
// implements it.
type Namespaces interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
	ListNamespaces(ctx context.Context) ([]namespace.Namespace, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces Namespaces
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Service implements the use cases of saved views. It adds the fence, the
// time, the ID and the audit entry to the calls of the Store, and it refuses a
// caller that is not a person (defense in depth: the HTTP adapter refuses it
// too).
type Service struct {
	store      Store
	namespaces Namespaces
	now        func() time.Time
}

// NewService returns a Service. It returns ErrMissingDependency when the
// store or the namespace port is missing.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: store and namespaces", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{store: cfg.Store, namespaces: cfg.Namespaces, now: now}, nil
}

// Caller is the person who calls a use case. Owner is the ID of the principal
// record of the caller. Principal is the identity of the request. By is the
// template of the audit entry: the caller fills where the request came from,
// and the service fills the action, the object and the time.
type Caller struct {
	Owner     string
	Principal identity.Principal
	By        audit.Entry
}

// check refuses a caller that is not a person with a valid owner.
func (c Caller) check() error {
	// Defense in depth: the HTTP adapter checks the kind too. This check stays
	// so that a new caller of the service cannot act for a service principal,
	// a job or a bootstrap token. It does not rely on the adapter or on the
	// store. It is an allowlist: a kind that is not "person", also an empty or
	// unknown kind, is refused.
	if c.Principal.Kind != identity.KindPerson || c.Principal.JobID != "" {
		return ErrNotPerson
	}

	if !validOwner(c.Owner) {
		return ErrNotPerson
	}

	return nil
}

// Create makes a view of the caller in ns. It returns ErrNotPerson,
// ErrInvalid, ErrExists, ErrLimit, namespace.ErrNotFound or
// namespace.ErrFenced.
func (s *Service) Create(ctx context.Context, c Caller, ns namespace.Name, name string, q Query) (View, error) {
	if err := c.check(); err != nil {
		return View{}, err
	}

	v := View{CreatedAt: s.now().UTC(), ID: NewID(), Namespace: ns, Name: name, Owner: c.Owner, Query: q}
	if err := v.Validate(); err != nil {
		return View{}, err
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return View{}, err
	}

	if err := s.store.Create(ctx, v, fence, s.entry(c, ns, v.ID, "create")); err != nil {
		return View{}, fmt.Errorf("create saved view: %w", err)
	}

	return v, nil
}

// Get returns the view id of the caller in ns, or ErrNotFound.
func (s *Service) Get(ctx context.Context, c Caller, ns namespace.Name, id ID) (View, error) {
	if err := c.check(); err != nil {
		return View{}, err
	}

	if err := id.Validate(); err != nil {
		return View{}, ErrNotFound
	}

	v, err := s.store.Get(ctx, ns, id, c.Owner)
	if err != nil {
		return View{}, fmt.Errorf("get saved view: %w", err)
	}

	return v, nil
}

// List returns the views of the caller in ns, ordered by name, then ID.
func (s *Service) List(ctx context.Context, c Caller, ns namespace.Name) ([]View, error) {
	if err := c.check(); err != nil {
		return nil, err
	}

	views, err := s.store.List(ctx, ns, c.Owner)
	if err != nil {
		return nil, fmt.Errorf("list saved views: %w", err)
	}

	return views, nil
}

// Delete removes the view id of the caller in ns. It returns ErrNotFound for
// a view that the caller does not own.
func (s *Service) Delete(ctx context.Context, c Caller, ns namespace.Name, id ID) error {
	if err := c.check(); err != nil {
		return err
	}

	if err := id.Validate(); err != nil {
		return ErrNotFound
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return err
	}

	if err := s.store.Delete(ctx, ns, id, c.Owner, fence, s.entry(c, ns, id, "delete")); err != nil {
		return fmt.Errorf("delete saved view: %w", err)
	}

	return nil
}

// RemoveOwner deletes the views of the principal with the ID owner in every
// namespace. Each namespace runs in its own transaction with its own fence
// and appends one audit entry for each view, with the view ID and not the
// name. It is cleanup and not the security control: the owner ID already
// hides these views from every reader. The caller runs it outside the
// transaction that deletes the principal, for example as a durable operation.
//
// It does not stop at the first failure: it tries every namespace and returns
// the number of views removed with the joined errors, so a retry finishes the
// rest. The retry is safe because a namespace with no view of the owner gives
// 0 and no entry.
func (s *Service) RemoveOwner(ctx context.Context, owner string) (int, error) {
	if !validOwner(owner) {
		return 0, fmt.Errorf("%w: the owner is not a principal ID", ErrInvalid)
	}

	all, err := s.namespaces.ListNamespaces(ctx)
	if err != nil {
		return 0, fmt.Errorf("list namespaces: %w", err)
	}

	var (
		total int
		errs  []error
	)

	for _, n := range all {
		e := s.systemEntry(n.Name)

		removed, delErr := s.store.DeleteOwned(ctx, n.Name, owner, n.Fence(), e)
		total += removed

		if delErr != nil {
			errs = append(errs, fmt.Errorf("remove saved views in namespace %s: %w", n.Name, delErr))
		}
	}

	return total, errors.Join(errs...)
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}

// entry makes the audit entry of one write. It names the view by ID and never
// by name: a name can hold private text.
func (s *Service) entry(c Caller, ns namespace.Name, id ID, verb string) audit.Entry {
	e := c.By
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Subject = c.Principal.Subject
	e.TokenID = c.Principal.TokenID
	e.Action = "view:" + verb
	e.Object = identity.NamespaceObject(ns).String()
	e.ObjectID = string(id)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.PrincipalKind = audit.PrincipalKindPerson

	return e
}

// systemEntry makes the template of the audit entries of the cleanup of a
// principal. The store sets the view ID on each copy.
func (s *Service) systemEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At:            s.now().UTC(),
		Namespace:     ns,
		Subject:       cleanupSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        "view:delete",
		Object:        identity.NamespaceObject(ns).String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "the owner of the view was removed",
	}
}

// cleanupSubject is the subject of the audit entries of the cleanup. It starts
// with '@', which no subject of a principal holds, so it never names a
// principal.
const cleanupSubject = "@server"
