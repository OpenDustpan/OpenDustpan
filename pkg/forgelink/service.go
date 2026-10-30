package forgelink

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// tokenBytes is the number of random bytes of a link token.
const tokenBytes = 32

// People finds the principal records that the service needs.
type People interface {
	// GetPrincipalBySubject returns the principal that exists with the subject,
	// or principal.ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
	// GetPrincipal returns the principal with the ID id, or principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// Service starts, shows and accepts link requests, takes the proof of a Git
// host account, lists and deletes links, and finds the principal of a linked
// account. A caller that makes a link must be a person in a console session.
type Service struct {
	store  Store
	people People
	now    func() time.Time
	random io.Reader
}

// NewService returns a Service. now gives the time of a change, and nil means
// time.Now. random gives the bytes of a token, and nil means crypto/rand.
func NewService(store Store, people People, now func() time.Time, random io.Reader) *Service {
	if now == nil {
		now = time.Now
	}

	if random == nil {
		random = rand.Reader
	}

	return &Service{store: store, people: people, now: now, random: random}
}

// Started is a link request that a person started. Token is the one-time token.
// It is shown once and never stored.
type Started struct {
	Token   string
	Request Request
}

// View is what the accept page shows: the request, and the link that the
// account that proved itself has now. HasCurrent is false when the account has
// no link. CurrentName is the display name of the principal of that link.
type View struct {
	Request     Request
	Current     Link
	HasCurrent  bool
	CurrentName string
}

// Start starts a link request for the person that caller is. It returns
// ErrNotPerson for a caller that is not a person in a console session, and for
// a principal that is disabled or a break-glass account.
func (s *Service) Start(ctx context.Context, caller identity.Principal) (Started, error) {
	person, err := s.person(ctx, caller)
	if err != nil {
		return Started{}, err
	}

	raw := make([]byte, tokenBytes)
	if _, readErr := io.ReadFull(s.random, raw); readErr != nil {
		return Started{}, fmt.Errorf("make a link token: %w", readErr)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)

	r, err := NewRequest(token, person.ID, s.now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return Started{}, err
	}

	if err := s.store.CreateRequest(ctx, r); err != nil {
		return Started{}, fmt.Errorf("store the link request: %w", err)
	}

	return Started{Token: token, Request: r}, nil
}

// Prove records the proof p on the request of token. Only the code that
// checked the signature of an event of the Git host calls it, with the account
// from that event. It returns ErrNotFound for a token that no request holds,
// and the other errors of Store.ProveRequest.
//
// Defense in depth: the caller checked the signature, and the store checks the
// proof and the request again. This method does not rely on either.
func (s *Service) Prove(ctx context.Context, token string, p Proof) (Request, error) {
	if !ValidToken(token) {
		return Request{}, fmt.Errorf("%w: a link token has %d to %d characters", ErrNotFound, MinTokenLen, maxTokenLen)
	}

	if err := p.Validate(); err != nil {
		return Request{}, err
	}

	r, err := s.store.ProveRequest(ctx, HashToken(token), p, s.now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return Request{}, fmt.Errorf("prove the link request: %w", err)
	}

	return r, nil
}

// Show returns the request of token for the page that asks the person to accept
// it, with the link that the account has now. A request of another principal
// is ErrNotFound: the page tells nobody else what a token holds.
func (s *Service) Show(ctx context.Context, caller identity.Principal, token string) (View, error) {
	person, err := s.person(ctx, caller)
	if err != nil {
		return View{}, err
	}

	r, err := s.own(ctx, person, token)
	if err != nil {
		return View{}, err
	}

	if err := r.Check(s.now()); err != nil {
		return View{}, err
	}

	v := View{Request: r, Current: Link{}, HasCurrent: false, CurrentName: ""}
	if !r.IsProved() {
		return v, nil
	}

	current, err := s.store.LinkOf(ctx, r.Proved.Account)

	switch {
	case errors.Is(err, ErrNotFound):
		return v, nil
	case err != nil:
		return View{}, fmt.Errorf("read the link of the account: %w", err)
	}

	v.Current, v.HasCurrent = current, true

	if holder, getErr := s.people.GetPrincipal(ctx, current.Principal); getErr == nil {
		v.CurrentName = holder.DisplayName
	}

	return v, nil
}

// Accept makes the link of the account that proved itself for the request of
// token. previous is the principal of the current link that the page showed, or
// empty. e is the audit entry of the request: the service sets the scope, the
// time, the object, the kind and the changes. It returns the link and the
// principal that lost a link, or empty.
func (s *Service) Accept(
	ctx context.Context, caller identity.Principal, token, previous string, e audit.Entry,
) (Link, string, error) {
	person, err := s.person(ctx, caller)
	if err != nil {
		return Link{}, "", err
	}

	r, err := s.own(ctx, person, token)
	if err != nil {
		return Link{}, "", err
	}

	now := s.now().UTC().Truncate(time.Millisecond)

	// The store applies the same rules in its transaction. This read lets the
	// entry name the account, which only a proved request has.
	if err := r.CheckAccept(person.ID, now); err != nil {
		return Link{}, "", err
	}

	e = s.entry(e, now, person.ID)
	e.Changes = append(slices.Clone(e.Changes), accountChanges(r.Proved.Account, r.Proved.Login)...)
	e.Changes = append(
		e.Changes,
		audit.Change{Key: "forgeLink.change", Old: nil, New: "linked", Layer: "", TakesEffect: ""},
		audit.Change{Key: "forgeLink.principal", Old: nil, New: person.ID, Layer: "", TakesEffect: ""},
	)

	if previous != "" {
		e.Changes = append(
			e.Changes,
			audit.Change{Key: "forgeLink.previous", Old: nil, New: previous, Layer: "", TakesEffect: ""},
		)
	}

	link, replaced, err := s.store.AcceptRequest(ctx, HashToken(token), person.ID, previous, now, e)
	if err != nil {
		return Link{}, "", fmt.Errorf("accept the link request: %w", err)
	}

	return link, replaced, nil
}

// List returns the links of the person that caller is.
func (s *Service) List(ctx context.Context, caller identity.Principal) ([]Link, error) {
	person, err := s.personRecord(ctx, caller)
	if err != nil {
		return nil, err
	}

	links, err := s.store.LinksOf(ctx, person.ID)
	if err != nil {
		return nil, fmt.Errorf("list the links of the person: %w", err)
	}

	return links, nil
}

// Delete deletes the link of the account a when it belongs to the person that
// caller is. A link of another principal is ErrNotFound: a person learns nothing
// about the links of others.
func (s *Service) Delete(ctx context.Context, caller identity.Principal, a Account, e audit.Entry) error {
	person, err := s.person(ctx, caller)
	if err != nil {
		return err
	}

	return s.deleteOf(ctx, person.ID, a, e)
}

// DeleteFor deletes the link of the account a when it belongs to the principal
// principalID. The route that calls it checks server:manage-principals on that
// principal. A link of another principal is ErrNotFound.
func (s *Service) DeleteFor(ctx context.Context, principalID string, a Account, e audit.Entry) error {
	return s.deleteOf(ctx, principalID, a, e)
}

// deleteOf deletes the link of a if principalID holds it.
func (s *Service) deleteOf(ctx context.Context, principalID string, a Account, e audit.Entry) error {
	if err := a.Validate(); err != nil {
		return err
	}

	current, err := s.store.LinkOf(ctx, a)
	if err != nil {
		return fmt.Errorf("read the link: %w", err)
	}

	if current.Principal != principalID {
		return ErrNotFound
	}

	e = s.entry(e, s.now().UTC().Truncate(time.Millisecond), principalID)
	e.Changes = append(slices.Clone(e.Changes), accountChanges(a, current.Login)...)
	e.Changes = append(
		e.Changes,
		audit.Change{Key: "forgeLink.change", Old: nil, New: "deleted", Layer: "", TakesEffect: ""},
		audit.Change{Key: "forgeLink.principal", Old: nil, New: principalID, Layer: "", TakesEffect: ""},
	)

	if err := s.store.DeleteLink(ctx, a, e); err != nil {
		return fmt.Errorf("delete the link: %w", err)
	}

	return nil
}

// Resolve returns the principal that the account a is linked to. The comment
// commands call it before they act. It returns ErrNotLinked for an account with
// no link and for a link of a principal that is disabled, deleted or a
// break-glass account, and ErrInvalid for an account that fails Validate. The
// fields of a match exactly.
func (s *Service) Resolve(ctx context.Context, a Account) (principal.Principal, error) {
	if err := a.Validate(); err != nil {
		return principal.Principal{}, err
	}

	l, err := s.store.LinkOf(ctx, a)
	if errors.Is(err, ErrNotFound) {
		return principal.Principal{}, ErrNotLinked
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read the link: %w", err)
	}

	p, err := s.people.GetPrincipal(ctx, l.Principal)
	if errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, ErrNotLinked
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read the principal of the link: %w", err)
	}

	// Defense in depth: the store of principals ends a link with its principal,
	// and this check stays so that a link of a principal that cannot act never
	// runs a command.
	if !p.DisabledAt.IsZero() || p.BreakGlass || p.Kind != principal.KindPerson {
		return principal.Principal{}, ErrNotLinked
	}

	return p, nil
}

// entry sets the fields of e that the service owns.
func (*Service) entry(e audit.Entry, now time.Time, principalID string) audit.Entry {
	e.Namespace = audit.ServerScope
	e.At = now
	e.Object = principal.Ref(principalID)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	return e
}

// accountChanges records an account in an audit entry. The login is text for a
// reader of the log: the log never relies on it.
func accountChanges(a Account, login string) []audit.Change {
	changes := []audit.Change{
		{Key: "forgeLink.kind", Old: nil, New: string(a.Kind), Layer: "", TakesEffect: ""},
		{Key: "forgeLink.host", Old: nil, New: a.Host, Layer: "", TakesEffect: ""},
		{Key: "forgeLink.accountId", Old: nil, New: a.ID, Layer: "", TakesEffect: ""},
	}

	if login != "" {
		changes = append(
			changes,
			audit.Change{Key: "forgeLink.login", Old: nil, New: login, Layer: "", TakesEffect: ""},
		)
	}

	return changes
}

// own reads the request of token and returns it only when person started it.
// Any other case is ErrNotFound, so the response does not tell a token that
// exists from one that does not.
func (s *Service) own(ctx context.Context, person principal.Principal, token string) (Request, error) {
	if !ValidToken(token) {
		return Request{}, ErrNotFound
	}

	r, err := s.store.GetRequest(ctx, HashToken(token))
	if err != nil {
		return Request{}, fmt.Errorf("read the link request: %w", err)
	}

	if r.Principal != person.ID {
		return Request{}, ErrNotFound
	}

	return r, nil
}

// person returns the record of the person that caller is, for a change. It
// requires a console session, a person that is not disabled, and no break-glass
// account.
//
// Defense in depth: the route refuses a caller that is not a person in a session
// too. This check stays so that a new caller of the service cannot skip it.
func (s *Service) person(ctx context.Context, caller identity.Principal) (principal.Principal, error) {
	if caller.Credential != identity.CredentialSession {
		return principal.Principal{}, ErrNotPerson
	}

	return s.personRecord(ctx, caller)
}

// personRecord returns the record of the person that caller acts as.
func (s *Service) personRecord(ctx context.Context, caller identity.Principal) (principal.Principal, error) {
	if !caller.ActsAsPerson() {
		return principal.Principal{}, ErrNotPerson
	}

	p, err := s.people.GetPrincipalBySubject(ctx, caller.Subject)
	if errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, ErrNotPerson
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read the person of the caller: %w", err)
	}

	if p.Kind != principal.KindPerson || !p.DisabledAt.IsZero() || p.BreakGlass {
		return principal.Principal{}, ErrNotPerson
	}

	return p, nil
}
