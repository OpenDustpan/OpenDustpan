// Package forgelinksvc is the service of the links from Git host accounts to
// principals. The ports and the domain types stay in package forgelink.
package forgelinksvc

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
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// The keys of the changes of an audit entry of a link.
const (
	keyChange    = "forgeLink.change"
	keyPrincipal = "forgeLink.principal"
)

// tokenBytes is the number of random bytes of a link token.
const tokenBytes = 32

// Service starts, shows and accepts link requests, takes the proof of a Git
// host account, lists and deletes links, and finds the principal of a linked
// account. A caller that makes a link must be a person in a console session.
type Service struct {
	store  forgelink.Store
	people forgelink.People
	now    func() time.Time
	random io.Reader
	intake bool
}

// NewService returns a Service. now gives the time of a change, and nil means
// time.Now. random gives the bytes of a token, and nil means crypto/rand.
func NewService(store forgelink.Store, people forgelink.People, now func() time.Time, random io.Reader) *Service {
	if now == nil {
		now = time.Now
	}

	if random == nil {
		random = rand.Reader
	}

	return &Service{store: store, people: people, now: now, random: random, intake: false}
}

// WithIntake returns a copy of s that reports an intake: the code that checks the
// signed events of a Git host and calls Prove exists. Until that code exists, a
// request can never get a proof, so the routes that start, show and accept a
// request stay unmounted. The server that has the intake sets this once.
func (s *Service) WithIntake() *Service {
	c := *s
	c.intake = true

	return &c
}

// HasIntake reports whether s has an intake that proves link requests.
func (s *Service) HasIntake() bool {
	return s.intake
}

// Start starts a link request for the person that caller is. It returns
// ErrNotPerson for a caller that is not a person in a console session, and for
// a principal that is disabled or a break-glass account.
func (s *Service) Start(ctx context.Context, caller identity.Principal) (forgelink.Started, error) {
	person, err := s.person(ctx, caller)
	if err != nil {
		return forgelink.Started{}, err
	}

	raw := make([]byte, tokenBytes)
	if _, readErr := io.ReadFull(s.random, raw); readErr != nil {
		return forgelink.Started{}, fmt.Errorf("make a link token: %w", readErr)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)

	r, err := forgelink.NewRequest(token, person.ID, s.now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return forgelink.Started{}, err
	}

	if err := s.store.CreateRequest(ctx, r); err != nil {
		return forgelink.Started{}, fmt.Errorf("store the link request: %w", err)
	}

	return forgelink.Started{Token: token, Request: r}, nil
}

// Prove records the proof p on the request of token. Only the code that
// checked the signature of an event of the Git host calls it, with the account
// from that event. It returns ErrNotFound for a token that no request holds,
// and the other errors of Store.ProveRequest.
//
// Defense in depth: the caller checked the signature, and the store checks the
// proof and the request again. This method does not rely on either.
func (s *Service) Prove(ctx context.Context, token string, p forgelink.Proof) (forgelink.Request, error) {
	if !forgelink.ValidToken(token) {
		return forgelink.Request{}, fmt.Errorf("%w: a link token has %d to %d characters",
			forgelink.ErrNotFound, forgelink.MinTokenLen, forgelink.MaxTokenLen)
	}

	if err := p.Validate(); err != nil {
		return forgelink.Request{}, err
	}

	r, err := s.store.ProveRequest(
		ctx, forgelink.HashToken(token), p, s.now().UTC().Truncate(time.Millisecond),
	)
	if err != nil {
		return forgelink.Request{}, fmt.Errorf("prove the link request: %w", err)
	}

	return r, nil
}

// Show returns the request of token for the page that asks the person to accept
// it, with the link that the account has now. A request of another principal
// is ErrNotFound: the page tells nobody else what a token holds.
func (s *Service) Show(ctx context.Context, caller identity.Principal, token string) (forgelink.View, error) {
	person, err := s.person(ctx, caller)
	if err != nil {
		return forgelink.View{}, err
	}

	r, err := s.own(ctx, person, token)
	if err != nil {
		return forgelink.View{}, err
	}

	if checkErr := r.Check(s.now()); checkErr != nil {
		return forgelink.View{}, checkErr
	}

	var v forgelink.View

	v.Request = r

	if !r.IsProved() {
		return v, nil
	}

	current, err := s.store.LinkOf(ctx, r.Proved.Account)

	switch {
	case errors.Is(err, forgelink.ErrNotFound):
		return v, nil
	case err != nil:
		return forgelink.View{}, fmt.Errorf("read the link of the account: %w", err)
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
) (forgelink.Link, string, error) {
	person, err := s.person(ctx, caller)
	if err != nil {
		return forgelink.Link{}, "", err
	}

	r, err := s.own(ctx, person, token)
	if err != nil {
		return forgelink.Link{}, "", err
	}

	now := s.now().UTC().Truncate(time.Millisecond)

	// The store applies the same rules in its transaction. This read lets the
	// entry name the account, which only a proved request has.
	if checkErr := r.CheckAccept(person.ID, now); checkErr != nil {
		return forgelink.Link{}, "", checkErr
	}

	e = s.entry(e, now, person.ID)
	lost := s.lostEntry(e, now, r, previous)
	e.Changes = append(slices.Clone(e.Changes), accountChanges(r.Proved.Account, r.Proved.Login)...)
	e.Changes = append(
		e.Changes,
		audit.Change{Key: keyChange, Old: nil, New: "linked", Layer: "", TakesEffect: ""},
		audit.Change{Key: keyPrincipal, Old: nil, New: person.ID, Layer: "", TakesEffect: ""},
		audit.Change{Key: "forgeLink.deliveryId", Old: nil, New: r.Proved.DeliveryID, Layer: "", TakesEffect: ""},
	)

	if previous != "" {
		e.Changes = append(
			e.Changes,
			audit.Change{Key: "forgeLink.previous", Old: nil, New: previous, Layer: "", TakesEffect: ""},
		)
	}

	accepted, err := s.store.AcceptRequest(ctx, forgelink.Acceptance{
		TokenSHA256: forgelink.HashToken(token), Principal: person.ID, Previous: previous, At: now,
		Lost: lost,
	}, e)
	if err != nil {
		return forgelink.Link{}, "", fmt.Errorf("accept the link request: %w", err)
	}

	return accepted.Link, accepted.Replaced, nil
}

// List returns the links of the person that caller is.
func (s *Service) List(ctx context.Context, caller identity.Principal) ([]forgelink.Link, error) {
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
func (s *Service) Delete(
	ctx context.Context, caller identity.Principal, a forgelink.Account, e audit.Entry,
) error {
	person, err := s.person(ctx, caller)
	if err != nil {
		return err
	}

	return s.deleteOf(ctx, person.ID, a, e)
}

// DeleteFor deletes the link of the account a when it belongs to the principal
// principalID. The route that calls it checks server:manage-principals on that
// principal. A link of another principal is ErrNotFound.
func (s *Service) DeleteFor(
	ctx context.Context, principalID string, a forgelink.Account, e audit.Entry,
) error {
	return s.deleteOf(ctx, principalID, a, e)
}

// Resolve returns the principal that the account a is linked to. The comment
// commands call it before they act. It returns ErrNotLinked for an account with
// no link and for a link of a principal that is disabled, deleted or a
// break-glass account, and ErrInvalid for an account that fails Validate. The
// fields of a match exactly.
func (s *Service) Resolve(ctx context.Context, a forgelink.Account) (principal.Principal, error) {
	if err := a.Validate(); err != nil {
		return principal.Principal{}, err
	}

	l, err := s.store.LinkOf(ctx, a)
	if errors.Is(err, forgelink.ErrNotFound) {
		return principal.Principal{}, forgelink.ErrNotLinked
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read the link: %w", err)
	}

	p, err := s.people.GetPrincipal(ctx, l.Principal)
	if errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, forgelink.ErrNotLinked
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read the principal of the link: %w", err)
	}

	// Defense in depth: the store of principals ends a link with its principal,
	// and this check stays so that a link of a principal that cannot act never
	// runs a command.
	if !p.DisabledAt.IsZero() || p.BreakGlass || p.Kind != principal.KindPerson {
		return principal.Principal{}, forgelink.ErrNotLinked
	}

	return p, nil
}

// deleteOf deletes the link of a if principalID holds it.
func (s *Service) deleteOf(
	ctx context.Context, principalID string, a forgelink.Account, e audit.Entry,
) error {
	if err := a.Validate(); err != nil {
		return err
	}

	current, err := s.store.LinkOf(ctx, a)
	if err != nil {
		return fmt.Errorf("read the link: %w", err)
	}

	if current.Principal != principalID {
		return forgelink.ErrNotFound
	}

	e = s.entry(e, s.now().UTC().Truncate(time.Millisecond), principalID)
	e.Changes = append(slices.Clone(e.Changes), accountChanges(a, current.Login)...)
	e.Changes = append(
		e.Changes,
		audit.Change{Key: keyChange, Old: nil, New: "deleted", Layer: "", TakesEffect: ""},
		audit.Change{Key: keyPrincipal, Old: nil, New: principalID, Layer: "", TakesEffect: ""},
	)

	if err := s.store.DeleteLink(ctx, a, e); err != nil {
		return fmt.Errorf("delete the link: %w", err)
	}

	return nil
}

// lostEntry returns the audit entry of the principal previous, which loses the
// link of the account of r to the principal of r. The object is the old holder,
// so the old holder finds the loss in its own entries. The entry also records
// the intent to tell the old holder: the notification type for it does not exist
// yet, so no notice goes out, and the entry is the only record of the loss.
func (s *Service) lostEntry(
	base audit.Entry, now time.Time, r forgelink.Request, previous string,
) audit.Entry {
	if previous == "" {
		return base
	}

	e := s.entry(base, now, previous)
	e.Changes = slices.Clone(base.Changes)
	e.Changes = append(e.Changes, accountChanges(r.Proved.Account, r.Proved.Login)...)
	e.Changes = append(
		e.Changes,
		audit.Change{Key: keyChange, Old: nil, New: "replaced", Layer: "", TakesEffect: ""},
		audit.Change{Key: keyPrincipal, Old: nil, New: previous, Layer: "", TakesEffect: ""},
		audit.Change{Key: "forgeLink.replacedBy", Old: nil, New: r.Principal, Layer: "", TakesEffect: ""},
		audit.Change{
			Key:         "forgeLink.notice",
			Old:         nil,
			New:         "intended: no notification type yet",
			Layer:       "",
			TakesEffect: "",
		},
	)

	return e
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
func accountChanges(a forgelink.Account, login string) []audit.Change {
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
func (s *Service) own(
	ctx context.Context, person principal.Principal, token string,
) (forgelink.Request, error) {
	if !forgelink.ValidToken(token) {
		return forgelink.Request{}, forgelink.ErrNotFound
	}

	r, err := s.store.GetRequest(ctx, forgelink.HashToken(token))
	if err != nil {
		return forgelink.Request{}, fmt.Errorf("read the link request: %w", err)
	}

	if r.Principal != person.ID {
		return forgelink.Request{}, forgelink.ErrNotFound
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
		return principal.Principal{}, forgelink.ErrNotPerson
	}

	return s.personRecord(ctx, caller)
}

// personRecord returns the record of the person that caller acts as.
func (s *Service) personRecord(
	ctx context.Context, caller identity.Principal,
) (principal.Principal, error) {
	if !caller.ActsAsPerson() {
		return principal.Principal{}, forgelink.ErrNotPerson
	}

	p, err := s.people.GetPrincipalBySubject(ctx, caller.Subject)
	if errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, forgelink.ErrNotPerson
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read the person of the caller: %w", err)
	}

	if p.Kind != principal.KindPerson || !p.DisabledAt.IsZero() || p.BreakGlass {
		return principal.Principal{}, forgelink.ErrNotPerson
	}

	return p, nil
}
