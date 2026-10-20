package preferences

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// People finds the person record of a caller.
type People interface {
	// GetPrincipalBySubject returns the principal that exists with the
	// subject, or principal.ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
}

// Service reads and changes the preferences of the caller. A request that
// acts as a person (a session, a personal token, or an agent run under the
// credential of its person) acts on the person of its subject, never on the
// service principal that makes the call.
type Service struct {
	store  Store
	people People
	now    func() time.Time
}

// NewService returns a Service. now gives the time of a change; nil means
// time.Now.
func NewService(store Store, people People, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}

	return &Service{store: store, people: people, now: now}
}

// Get returns the preferences of the person that caller acts as.
func (s *Service) Get(ctx context.Context, caller identity.Principal) (Document, error) {
	id, err := s.person(ctx, caller)
	if err != nil {
		return Default(), err
	}

	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return Default(), fmt.Errorf("read preferences: %w", err)
	}

	return doc, nil
}

// Update applies patch to the preferences of the person that caller acts as.
// e is the audit entry that the request made: the service sets the scope,
// the time, the object and the kind, and the store sets the action and the
// changed fields. A patch that changes nothing writes no entry.
func (s *Service) Update(
	ctx context.Context, caller identity.Principal, patch Patch, e audit.Entry,
) (Result, error) {
	id, err := s.person(ctx, caller)
	if err != nil {
		return Result{Document: Default(), Changed: nil}, err
	}

	if checkErr := patch.Validate(); checkErr != nil {
		return Result{Document: Default(), Changed: nil}, checkErr
	}

	e.Namespace = audit.ServerScope
	e.At = s.now().UTC().Truncate(time.Millisecond)
	e.Object = principal.Ref(id)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	res, err := s.store.Update(ctx, id, patch, e)
	if err != nil {
		return Result{Document: Default(), Changed: nil}, fmt.Errorf("update preferences: %w", err)
	}

	return res, nil
}

// person returns the ID of the person that caller acts as. It returns
// ErrNotPerson for a caller that is not a person with a record.
//
// Defense in depth: the route refuses a caller that is not a person too, and
// the store refuses a record that is not a live person. This check stays so
// that a new caller of the service cannot skip it.
func (s *Service) person(ctx context.Context, caller identity.Principal) (string, error) {
	if !caller.ActsAsPerson() {
		return "", ErrNotPerson
	}

	p, err := s.people.GetPrincipalBySubject(ctx, caller.Subject)
	if errors.Is(err, principal.ErrNotFound) {
		return "", ErrNotPerson
	}

	if err != nil {
		return "", fmt.Errorf("read the person of the caller: %w", err)
	}

	if p.Kind != principal.KindPerson {
		return "", ErrNotPerson
	}

	return p.ID, nil
}
