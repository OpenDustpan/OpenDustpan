// Package mfasvc is the service of the passkeys of a person: the register
// ceremony that adds a passkey, the assertion ceremonies of a sign-in, a
// second factor and a fresh step, and the removal of a passkey.
//
// The service makes each challenge from crypto/rand, stores only its SHA-256
// digest with the kind, the person and the session of the ceremony, and
// takes it back once at the end. The relying party port checks each response
// against the relying party ID and the origin of the external URL. The store
// keeps each passkey and compares the signature counter.
//
// Each change and each refusal writes an audit entry. No entry holds a
// challenge, a key or a response.
package mfasvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Audit actions of the passkeys. They are not authorization actions.
const (
	ActionPasskeyAdded     = "passkey.added"
	ActionPasskeyRemoved   = "passkey.removed"
	ActionPasskeyRotated   = "passkey.rotated"
	ActionPasskeySuspended = "passkey.suspended"
	ActionAddRefused       = "passkey.add-refused"
	ActionRemoveRefused    = "passkey.remove-refused"
	ActionRotateRefused    = "passkey.rotate-refused"
	ActionListRefused      = "passkey.list-refused"
	ActionCeremonyRefused  = "passkey.ceremony-refused"
)

// unknownObject is the audit object of a ceremony that names no passkey that
// the service found.
const unknownObject = "passkey:unknown"

// Object returns the audit object of the passkey id.
func Object(id string) string {
	return "passkey:" + id
}

// Auditor appends an audit entry in the scope of the installation, for a
// refusal that changes nothing.
type Auditor interface {
	// AppendServer appends e in audit.ServerScope.
	AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error)
}

// Config holds the dependencies of a Service. Every field but Now, Guard,
// Authorizer and Log is required.
type Config struct {
	Store mfa.PasskeyStore
	RP    mfa.RelyingParty
	Audit Auditor
	Now   func() time.Time
	// Log receives the errors of the refusals whose audit entry cannot be
	// written. Nil means slog.Default().
	Log *slog.Logger
	// Authorizer decides the action of the route on the installation. Nil
	// means that only an administrator of the identity layer passes the
	// check of the action: the service fails closed.
	Authorizer identity.Authorizer
	// Guard checks that the caller holds the actions that the grants of the
	// target account give, as the disable of a principal does. Nil means the
	// AdminOnlyGuard: only an administrator passes the check. This fails
	// closed.
	Guard principal.Guard
	// FreshWindow returns the token freshness window of the session policy: the
	// oldest console sign-in that a change of a break-glass account accepts.
	// Nil, or a window of 0, means that no sign-in is fresh: the service fails
	// closed.
	FreshWindow func(ctx context.Context) time.Duration
}

// ErrMissingDependency reports a Config with a required field that is nil.
var ErrMissingDependency = errors.New("mfasvc: missing dependency")

// ErrMissingActor reports a call with a nil Actor. The HTTP adapter always
// passes one; this error keeps a new caller from reaching a nil dereference.
var ErrMissingActor = errors.New("mfasvc: missing actor")

// Service is the service of the passkeys.
type Service struct {
	store mfa.PasskeyStore
	rp    mfa.RelyingParty
	audit Auditor
	now   func() time.Time
	log   *slog.Logger
	// authz decides the action of the route on the installation. Nil means
	// that only an administrator passes the check of the action.
	authz identity.Authorizer
	// guard checks that the caller holds the actions that the grants of the
	// target account give. Nil means the AdminOnlyGuard.
	guard principal.Guard
	// freshWindow returns the freshness window. Nil means a window of 0.
	freshWindow func(ctx context.Context) time.Duration
	// watch counts the failed assertions for break-glass accounts. Nil
	// counts nothing.
	watch *breakGlassWatch
}

// New returns a Service. It returns ErrMissingDependency for a required
// dependency that is nil.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: store", ErrMissingDependency)
	case cfg.RP == nil:
		return nil, fmt.Errorf("%w: relying party", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}

	return &Service{
		store: cfg.Store, rp: cfg.RP, audit: cfg.Audit, now: now, log: log,
		authz: cfg.Authorizer, guard: cfg.Guard, freshWindow: cfg.FreshWindow, watch: nil,
	}, nil
}

// Actor is the caller of a step: the audit entry that the HTTP adapter fills
// (subject, source address, user agent, channel, request ID), the principal
// ID and the session ID of the caller from its console session, the principal
// record of the caller, and the time of the last sign-in of its console
// session. Both IDs and the sign-in time are empty for the start and the end
// of a passkey sign-in, which has no session yet.
type Actor struct {
	Entry       audit.Entry
	PrincipalID string
	SessionID   string
	// Caller is the principal record of the caller. The service reads its
	// subject and its administrator flag, never its ID. Zero for a caller
	// with no principal.
	Caller identity.Principal
	// SignedInAt is the time of the last sign-in of the console session of
	// the caller. Zero for an access token, which never gives a fresh
	// sign-in.
	SignedInAt time.Time
}

// Binding names what an assertion ceremony belongs to: the person and the
// pending sign-in of a second factor. The person and the session of a fresh
// step come from the Actor. A sign-in names nothing.
type Binding struct {
	PersonID  string
	PendingID string
}

// BeginRegistration starts a register ceremony of the caller, who adds a
// passkey to its own account in its session, and returns the JSON of the
// creation options. p is the person of the caller.
func (s *Service) BeginRegistration(ctx context.Context, a *Actor, p mfa.Person) ([]byte, error) {
	if a == nil {
		return nil, ErrMissingActor
	}
	object := principal.Ref(p.ID)

	if a.PrincipalID == "" || a.SessionID == "" || p.ID != a.PrincipalID {
		return nil, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "not the own account in a session",
			mfa.ErrWrongPerson)
	}

	keys, err := s.store.Passkeys(ctx, p.ID)
	if err != nil {
		return nil, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "passkeys cannot be read", err)
	}

	// Defense in depth: the store counts the passkeys again in the
	// transaction of the add. This check stays so that a person at the limit
	// gets no ceremony at all.
	if len(keys) >= mfa.MaxPasskeys {
		return nil, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "passkey limit reached", mfa.ErrLimit)
	}

	c, err := s.newChallenge(ctx, &mfa.Challenge{
		CreatedAt: time.Time{}, ExpiresAt: time.Time{}, Kind: mfa.CeremonyRegister, PersonID: p.ID, PendingID: "",
		SessionID: a.SessionID, Source: "", SourceGroup: "",
	})
	if err != nil {
		return nil, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "challenge cannot be stored", err)
	}

	options, err := s.rp.CreationOptions(p, c, keys)
	if err != nil {
		return nil, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "creation options cannot be made", err)
	}

	return options, nil
}

// FinishRegistration ends the register ceremony of the response, checks
// it, and stores the passkey for the caller under the given name. p is the
// person of the caller.
func (s *Service) FinishRegistration(
	ctx context.Context, a *Actor, p mfa.Person, name string, response []byte,
) (mfa.Passkey, error) {
	if a == nil {
		return mfa.Passkey{}, ErrMissingActor
	}
	var none mfa.Passkey

	object := principal.Ref(p.ID)

	if err := mfa.ValidatePasskeyName(name); err != nil {
		return none, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "name not valid", err)
	}

	h, c, err := s.take(ctx, response)
	if err != nil {
		return none, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "no open register ceremony", err)
	}

	// The challenge is gone now, so a response works once. The checks below
	// tie the ceremony to the caller, the person and the session.
	//
	// Defense in depth: the store refuses a register challenge with no person
	// or no session, and the relying party checks the type of the client
	// data. This check stays so that a challenge of another kind, or a caller
	// with no person or no session, never reaches the check of a
	// registration.
	if !registerBoundToCaller(c, a, p) {
		return none, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "ceremony of another person or session",
			mfa.ErrWrongPerson)
	}

	reg, err := s.rp.VerifyCreation(p, h.Challenge, response)
	if err != nil {
		return none, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "response failed a check", err)
	}

	// Defense in depth: the relying party adapter requires user
	// verification of a registration. This check stays so that a passkey
	// that can sign in alone always had it.
	if !reg.UserVerified {
		return none, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "no user verification", mfa.ErrVerification)
	}

	key := mfa.Passkey{
		CreatedAt:      s.now(),
		LastUsedAt:     time.Time{},
		SuspendedAt:    time.Time{},
		ID:             ident.NewID(mfa.PasskeyIDPrefix),
		PersonID:       p.ID,
		Name:           name,
		CredentialID:   reg.CredentialID,
		PublicKey:      reg.PublicKey,
		Transports:     reg.Transports,
		AAGUID:         reg.AAGUID,
		SignCount:      reg.SignCount,
		BackupEligible: reg.BackupEligible,
	}

	e := s.entry(a.Entry, ActionPasskeyAdded, Object(key.ID), audit.KindChange, audit.OutcomeSuccess)
	e.Changes = []audit.Change{
		{Key: "name", Old: nil, New: name, Layer: "", TakesEffect: ""},
		{Key: "person", Old: nil, New: p.ID, Layer: "", TakesEffect: ""},
		{Key: "backupEligible", Old: nil, New: strconv.FormatBool(key.BackupEligible), Layer: "", TakesEffect: ""},
	}

	if err := s.store.AddPasskey(ctx, key, e); err != nil {
		return none, s.refuse(ctx, a.Entry, on(ActionAddRefused, object), "store refused the passkey", err)
	}

	return key, nil
}

// registerBoundToCaller reports whether the challenge c is a register ceremony
// of the caller a, in its session, for the person p. A caller with no person
// or no session is never bound.
func registerBoundToCaller(c mfa.Challenge, a *Actor, p mfa.Person) bool {
	return c.Kind == mfa.CeremonyRegister && a.PrincipalID != "" && a.SessionID != "" &&
		c.PersonID == a.PrincipalID && c.SessionID == a.SessionID && p.ID == a.PrincipalID
}

// BeginAssertion starts an assertion ceremony of the kind k and returns the
// JSON of the request options. A sign-in names no person. A second factor
// names the person and the pending sign-in in b. A fresh step is of the
// person and the session of the caller.
func (s *Service) BeginAssertion(ctx context.Context, a *Actor, k mfa.Ceremony, b Binding) ([]byte, error) {
	if a == nil {
		return nil, ErrMissingActor
	}
	var (
		personID, pendingID, sessionID string
		allow                          []mfa.Passkey
	)

	switch k {
	case mfa.CeremonySignIn:
	case mfa.CeremonySecondFactor:
		personID, pendingID = b.PersonID, b.PendingID
	case mfa.CeremonyFresh:
		personID, sessionID = a.PrincipalID, a.SessionID
	case mfa.CeremonyRegister:
		return nil, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject), "register is no assertion",
			mfa.ErrInvalid)
	default:
		return nil, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject), "unknown ceremony", mfa.ErrInvalid)
	}

	if k != mfa.CeremonySignIn {
		keys, err := s.usable(ctx, personID)
		if err != nil {
			return nil, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, principal.Ref(personID)),
				"no passkey to assert", err)
		}

		allow = keys
	}

	keys := challengeSource(k, a.Entry.SourceIP)

	c, err := s.newChallenge(ctx, &mfa.Challenge{
		CreatedAt: time.Time{}, ExpiresAt: time.Time{}, Kind: k, PersonID: personID, PendingID: pendingID,
		SessionID: sessionID, Source: keys.source, SourceGroup: keys.group,
	})
	if errors.Is(err, mfa.ErrSourceChallengeLimit) {
		return nil, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject),
			"source holds the most open sign-in challenges", err)
	}

	if err != nil {
		return nil, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject), "challenge cannot be stored", err)
	}

	options, err := s.rp.RequestOptions(k, c, allow)
	if err != nil {
		return nil, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject), "request options cannot be made",
			err)
	}

	return options, nil
}

// bound reports whether the challenge c of a ceremony of the kind k belongs
// to the caller a and the binding b: the stored person, pending sign-in and
// session of c are the ones that the kind asks for.
func bound(k mfa.Ceremony, c mfa.Challenge, a *Actor, b Binding) bool {
	binds := map[mfa.Ceremony]mfa.Challenge{
		mfa.CeremonySignIn: {
			CreatedAt: time.Time{},
			ExpiresAt: time.Time{},
			Kind:      k,
			PersonID:  "",
			PendingID: "",
			SessionID: "",
			Source:    "",

			SourceGroup: "",
		},
		mfa.CeremonySecondFactor: {
			CreatedAt: time.Time{}, ExpiresAt: time.Time{}, Kind: k, PersonID: b.PersonID, PendingID: b.PendingID,
			SessionID: "", Source: "", SourceGroup: "",
		},
		mfa.CeremonyFresh: {
			CreatedAt: time.Time{}, ExpiresAt: time.Time{}, Kind: k, PersonID: a.PrincipalID, PendingID: "",
			SessionID: a.SessionID, Source: "", SourceGroup: "",
		},
	}

	want, known := binds[k]
	if !known {
		return false
	}

	// Defense in depth: the store refuses a challenge of a second factor or
	// a fresh step with no person. This check stays so that an empty
	// binding never matches an empty stored value.
	if k != mfa.CeremonySignIn && (want.PersonID == "" || want.PendingID+want.SessionID == "") {
		return false
	}

	return c.Kind == k && c.PersonID == want.PersonID && c.PendingID == want.PendingID &&
		c.SessionID == want.SessionID
}

// FinishAssertion ends the assertion ceremony of the kind k of the
// response, checks it and records the use. It returns the passkey with the
// new counter and time of use. A counter that goes back suspends the
// passkey: the store writes the security entry, and the error is
// mfa.ErrCounterRegressed.
//
// The service does not read the principal of the passkey. Before it makes a
// session or gives a fresh step, the caller checks that the principal exists
// and is not disabled.
func (s *Service) FinishAssertion(
	ctx context.Context, a *Actor, k mfa.Ceremony, b Binding, response []byte,
) (mfa.Passkey, error) {
	if a == nil {
		return mfa.Passkey{}, ErrMissingActor
	}
	key, _, err := finishCeremony(ctx, s, &finishInput{response: response, bind: b, actor: a, kind: k})

	return key, err
}

// SignedIn is the result of a passkey sign-in that passed each check of the
// service: the passkey with its new counter, and whether the check saw user
// verification.
type SignedIn struct {
	Passkey      mfa.Passkey
	UserVerified bool
}

// FinishSignIn ends the sign-in ceremony of the response. It is
// FinishAssertion for mfa.CeremonySignIn, and it also returns the user
// verification that the relying party saw, so that the sign-in service can
// check it again. The person of the passkey is the person of the user handle
// of the response. Before it makes a session, the caller checks that the
// principal exists and is not disabled.
func (s *Service) FinishSignIn(ctx context.Context, a *Actor, response []byte) (SignedIn, error) {
	var none SignedIn
	if a == nil {
		return none, ErrMissingActor
	}

	key, got, err := finishCeremony(ctx, s, &finishInput{
		response: response, bind: Binding{PersonID: "", PendingID: ""}, actor: a, kind: mfa.CeremonySignIn,
	})
	if err != nil {
		return none, err
	}

	return SignedIn{Passkey: key, UserVerified: got.UserVerified}, nil
}

// finishInput is what the end of an assertion ceremony needs: the caller, the
// kind of the ceremony, what the ceremony binds to, and the response.
type finishInput struct {
	response []byte
	bind     Binding
	actor    *Actor
	kind     mfa.Ceremony
}

// openCeremony takes the challenge of the response and returns the passkey of
// the response with the head of the response. It runs the checks that need no
// signature: the kind, the person and the session of the ceremony, the owner
// of the passkey and its suspension. It is a function and not a method, so
// that the exported methods of the service stay before the unexported ones.
func openCeremony(ctx context.Context, s *Service, in *finishInput) (mfa.Passkey, mfa.ResponseHead, error) {
	var (
		none   mfa.Passkey
		noHead mfa.ResponseHead
	)

	a := in.actor

	h, c, err := s.take(ctx, in.response)
	if err != nil {
		return none, noHead, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject), "no open ceremony", err)
	}

	if !bound(in.kind, c, a, in.bind) {
		return none, noHead, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject),
			"ceremony of another kind, person or session", mfa.ErrWrongPerson)
	}

	key, err := s.store.PasskeyByCredentialID(ctx, h.CredentialID)
	if err != nil {
		return none, noHead, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, unknownObject), "unknown passkey", err)
	}

	object := Object(key.ID)

	// From here the passkey is known. A refusal returns it, so that the caller
	// can count the failed assertion for the account of the passkey.
	if ownErr := owns(in.kind, c, h, key); ownErr != nil {
		return key, noHead, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, object), "passkey of another person",
			ownErr)
	}

	// Defense in depth: the store refuses the use of a suspended passkey in
	// the transaction of the use. This check stays so that the relying party
	// never checks a response of a suspended passkey.
	if !key.SuspendedAt.IsZero() {
		return key, noHead, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, object), "passkey is suspended",
			mfa.ErrSuspended)
	}

	return key, h, nil
}

// finishCeremony is FinishAssertion with the result of the relying party. A
// failure after the lookup of the passkey is counted for the account of the
// passkey, when the service watches break-glass accounts.
func finishCeremony(ctx context.Context, s *Service, in *finishInput) (mfa.Passkey, mfa.Assertion, error) {
	var seen mfa.Passkey

	key, got, err := checkCeremony(ctx, s, in, &seen)
	if err != nil && seen.ID != "" {
		s.noteFailure(ctx, in.actor.Entry, &seen)
	}

	return key, got, err
}

// checkCeremony runs the checks of finishCeremony. It sets seen to the passkey
// of the response as soon as it found it, so that the caller can count a
// failure for its account.
func checkCeremony(
	ctx context.Context, s *Service, in *finishInput, seen *mfa.Passkey,
) (mfa.Passkey, mfa.Assertion, error) {
	var (
		none mfa.Passkey
		zero mfa.Assertion
	)

	key, h, err := openCeremony(ctx, s, in)
	*seen = key

	if err != nil {
		return none, zero, err
	}

	a, k, object := in.actor, in.kind, Object(key.ID)

	owner := mfa.Person{ID: key.PersonID, Name: key.PersonID, DisplayName: ""}

	got, err := s.rp.VerifyAssertion(k, owner, key, h.Challenge, in.response)
	if err != nil {
		return none, zero, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, object), "response failed a check", err)
	}

	// Defense in depth: the relying party adapter requires user
	// verification of these kinds. This check stays so that a sign-in and a
	// fresh step never pass without it.
	if mfa.VerificationOf(k) == mfa.VerificationRequired && !got.UserVerified {
		return none, zero, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, object), "no user verification",
			mfa.ErrVerification)
	}

	at := s.now()
	use := mfa.PasskeyUse{At: at, PasskeyID: key.ID, SignCount: got.SignCount}

	suspended := s.entry(a.Entry, ActionPasskeySuspended, object, audit.KindSecurity, audit.OutcomeDenied)
	stored, next := strconv.FormatUint(uint64(key.SignCount), 10), strconv.FormatUint(uint64(got.SignCount), 10)
	suspended.Changes = []audit.Change{{Key: "signCount", Old: &stored, New: next, Layer: "", TakesEffect: ""}}

	if err := s.store.RecordUse(ctx, use, suspended); err != nil {
		if errors.Is(err, mfa.ErrCounterRegressed) {
			s.log.WarnContext(ctx, "a passkey counter went back; the passkey is suspended",
				slog.String("passkey", key.ID))

			return none, zero, err
		}

		return none, zero, s.refuse(ctx, a.Entry, on(ActionCeremonyRefused, object), "store refused the use", err)
	}

	key.SignCount = got.SignCount
	key.LastUsedAt = at

	return key, got, nil
}

// owns returns mfa.ErrWrongPerson unless the passkey key belongs to the
// person of the challenge c, or, for a sign-in, to the person of the user
// handle of the response.
func owns(k mfa.Ceremony, c mfa.Challenge, h mfa.ResponseHead, key mfa.Passkey) error {
	if k == mfa.CeremonySignIn {
		if !bytes.Equal(h.UserHandle, []byte(key.PersonID)) {
			return fmt.Errorf("%w: the user handle is not of the passkey", mfa.ErrWrongPerson)
		}

		return nil
	}

	if key.PersonID != c.PersonID {
		return fmt.Errorf("%w: the passkey is not of the person of the ceremony", mfa.ErrWrongPerson)
	}

	return nil
}

// Passkeys returns the passkeys of the caller.
func (s *Service) Passkeys(ctx context.Context, a *Actor) ([]mfa.Passkey, error) {
	if a == nil {
		return nil, ErrMissingActor
	}
	if a.PrincipalID == "" {
		return nil, s.refuse(ctx, a.Entry, on(ActionListRefused, unknownObject), "no caller", mfa.ErrWrongPerson)
	}

	keys, err := s.store.Passkeys(ctx, a.PrincipalID)
	if err != nil {
		return nil, fmt.Errorf("read the passkeys: %w", err)
	}

	return keys, nil
}

// RemovePasskey deletes the passkey id of the caller. The store refuses the
// last passkey of a local user with no password, and of a break-glass
// person.
func (s *Service) RemovePasskey(ctx context.Context, a *Actor, id string) error {
	if a == nil {
		return ErrMissingActor
	}
	object := Object(id)

	if a.PrincipalID == "" {
		return s.refuse(ctx, a.Entry, on(ActionRemoveRefused, object), "no caller", mfa.ErrWrongPerson)
	}

	// Defense in depth: the HTTP adapter checks the ID of the path. This
	// check stays so that no ID that no passkey can have reaches the store
	// or the audit object.
	if !ident.Valid(id, mfa.MaxIDLength) {
		return s.refuse(ctx, a.Entry, on(ActionRemoveRefused, unknownObject), "passkey ID not valid", mfa.ErrNotFound)
	}

	e := s.entry(a.Entry, ActionPasskeyRemoved, object, audit.KindChange, audit.OutcomeSuccess)

	if err := s.store.RemovePasskey(ctx, a.PrincipalID, id, e); err != nil {
		return s.refuse(ctx, a.Entry, on(ActionRemoveRefused, object), "store refused the removal", err)
	}

	return nil
}

// AddBreakGlassPerson stores a new break-glass person and its first passkey in
// one store transaction. It accepts the add whatever the current count. The
// caller names the new person in its authorization. The service makes the ID
// of the person, checks its input and the passkey, and writes two entries: one
// for the person and one for the passkey. It calls the store once and never
// adds the passkey on its own, so a failed passkey write leaves no person.
func (s *Service) AddBreakGlassPerson(
	ctx context.Context, a *Actor, in principal.Input, first mfa.Passkey,
) (principal.Principal, mfa.Passkey, error) {
	if err := s.checkAddCaller(ctx, a, in.Kind); err != nil {
		return principal.Principal{}, mfa.Passkey{}, err
	}

	displayName := in.DisplayName
	if displayName == "" {
		displayName = in.Subject
	}

	at := s.now()
	id := ident.NewID(principal.IDPrefix)
	p := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     in.Subject,
		DisplayName: displayName,
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  true,
	}

	if err := p.Validate(); err != nil {
		return principal.Principal{}, mfa.Passkey{},
			s.refuse(ctx, a.Entry, on(ActionAddRefused, principal.Ref(id)), "the person is not valid", err)
	}

	first.PersonID = id
	first.CreatedAt = s.now()
	first.LastUsedAt = time.Time{}
	first.SuspendedAt = time.Time{}

	if first.ID == "" {
		first.ID = ident.NewID(mfa.PasskeyIDPrefix)
	}

	// Defense in depth: the store checks the passkey again before it inserts
	// it. This check stays so that a value that no store must hold never
	// reaches the store or an audit entry.
	if err := first.Validate(); err != nil {
		return principal.Principal{}, mfa.Passkey{},
			s.refuse(ctx, a.Entry, on(ActionAddRefused, principal.Ref(id)), "the passkey is not valid", err)
	}

	principalEntry := s.entry(a.Entry, a.Entry.Action, principal.Ref(id), audit.KindChange, audit.OutcomeSuccess)
	passkeyEntry := s.entry(a.Entry, ActionPasskeyAdded, Object(first.ID), audit.KindChange, audit.OutcomeSuccess)

	if err := s.store.AddBreakGlassPerson(ctx, p, first, principalEntry, passkeyEntry); err != nil {
		return principal.Principal{}, mfa.Passkey{}, s.refuse(
			ctx, a.Entry, on(ActionAddRefused, principal.Ref(id)), "store refused the add", err,
		)
	}

	return p, first, nil
}

// RotatePasskey replaces the passkey oldID of the person personID with next,
// in one transaction: the new passkey is stored before the old one ends, so
// no commit leaves the person without a working credential. It calls the
// rotation method of the store, never an add and then a remove. The caller
// names the person in its authorization; the service checks the IDs, the
// action of the route, the fresh sign-in of the session of the caller, that
// the person is a break-glass account, and that the caller holds the actions
// that the grants of the account give. It returns mfa.ErrNotFound for a
// person that is not a break-glass account, and an error that wraps
// identity.ErrDenied for a caller that fails the action, the fresh sign-in
// or the reach check. Each refusal writes one denial entry.
func (s *Service) RotatePasskey(ctx context.Context, a *Actor, personID, oldID string, next mfa.Passkey) error {
	if a == nil {
		return ErrMissingActor
	}
	object := Object(oldID)

	if a.PrincipalID == "" {
		return s.refuse(ctx, a.Entry, on(ActionRotateRefused, object), "no caller", mfa.ErrWrongPerson)
	}

	// Defense in depth: the HTTP adapter checks the IDs of the path and the
	// body. These checks stay so that no invalid ID reaches the store or the
	// audit object.
	if !ident.Valid(personID, mfa.MaxIDLength) || !ident.Valid(oldID, mfa.MaxIDLength) {
		return s.refuse(ctx, a.Entry, on(ActionRotateRefused, unknownObject), "ID not valid", mfa.ErrNotFound)
	}

	// The action, the fresh sign-in and the reach of the account: the route
	// checks the action, and the HTTP adapter checks the fresh sign-in; each
	// check here does not rely on them, so that a new caller of the service
	// cannot skip one. The reach check is the check that the disable of a
	// principal runs: a caller must hold every action that the grants of the
	// account give, or the rotation would hand the caller the account.
	if err := s.checkManagePrincipals(ctx, a, ActionRotateRefused, object); err != nil {
		return err
	}

	if err := s.checkFreshSignIn(ctx, a, ActionRotateRefused, object); err != nil {
		return err
	}

	// Defense in depth: the HTTP handler rotates only the passkey of a
	// break-glass account, and the store checks the locked principal row
	// again. This check does not rely on them. It stays so that a new caller
	// cannot skip it and rotate the passkey of another person.
	if err := s.checkBreakGlassPerson(ctx, a, ActionRotateRefused, personID); err != nil {
		return err
	}

	if err := s.checkTargetReach(ctx, a, object, personID); err != nil {
		return err
	}

	next.PersonID = personID
	next.CreatedAt = s.now()
	next.LastUsedAt = time.Time{}
	next.SuspendedAt = time.Time{}

	if next.ID == "" {
		next.ID = ident.NewID(mfa.PasskeyIDPrefix)
	}

	// Defense in depth: the store checks the passkey again before it inserts
	// it. This check stays so that a value that no store must hold never
	// reaches the store or the audit entry.
	if err := next.Validate(); err != nil {
		return s.refuse(ctx, a.Entry, on(ActionRotateRefused, object), "the new passkey is not valid", err)
	}

	old, newID := oldID, next.ID
	e := s.entry(a.Entry, ActionPasskeyRotated, Object(next.ID), audit.KindChange, audit.OutcomeSuccess)
	e.Changes = []audit.Change{
		{Key: "oldPasskey", Old: &old, New: "", Layer: "", TakesEffect: ""},
		{Key: "newPasskey", Old: nil, New: newID, Layer: "", TakesEffect: ""},
	}

	if err := s.store.RotatePasskey(ctx, personID, oldID, next, e); err != nil {
		return s.refuse(ctx, a.Entry, on(ActionRotateRefused, object), "store refused the rotation", err)
	}

	return nil
}

// checkAddCaller checks the caller and the kind of the add of a break-glass
// person. Each refusal writes one entry of ActionAddRefused.
func (s *Service) checkAddCaller(ctx context.Context, a *Actor, kind principal.Kind) error {
	if a == nil {
		return ErrMissingActor
	}

	if a.PrincipalID == "" {
		return s.refuse(ctx, a.Entry, on(ActionAddRefused, unknownObject), "no caller", mfa.ErrWrongPerson)
	}

	// Defense in depth: the route checks the action and the HTTP adapter checks
	// the fresh sign-in. Each check here does not rely on them, so that a new
	// caller of the service cannot add a break-glass account with a passkey of
	// its choice. The add makes the account, so there is no grant of it to reach.
	if err := s.checkManagePrincipals(ctx, a, ActionAddRefused, unknownObject); err != nil {
		return err
	}

	if err := s.checkFreshSignIn(ctx, a, ActionAddRefused, unknownObject); err != nil {
		return err
	}

	// Defense in depth: the HTTP adapter sends a person only. This check stays
	// so that no caller makes another kind of principal a break-glass account.
	if kind != "" && kind != principal.KindPerson {
		return s.refuse(
			ctx, a.Entry, on(ActionAddRefused, unknownObject), "a break-glass account is a person",
			fmt.Errorf("%w: a break-glass account is a person, not a %s", principal.ErrBreakGlassKind, kind),
		)
	}

	return nil
}

// checkManagePrincipals refuses a caller that does not hold
// server:manage-principals on the installation. The refusal entry has the
// action refused. With no Authorizer wired, only an administrator of the
// identity layer passes. This fails closed.
func (s *Service) checkManagePrincipals(ctx context.Context, a *Actor, refused, object string) error {
	if s.authz == nil {
		// Defense in depth: the Config keeps the Authorizer optional, so a
		// Service can be built without one. This check stays so that such a
		// Service lets only an administrator rotate a break-glass passkey.
		if a.Caller.Admin {
			return nil
		}

		return s.refuse(ctx, a.Entry, on(refused, object), "the caller does not hold the action",
			fmt.Errorf("%w: only an administrator changes a break-glass account "+
				"when the service cannot check the action", identity.ErrDenied))
	}

	err := s.authz.Authorize(ctx, a.Caller, identity.ActionServerManagePrincipals, identity.ServerObject())
	if err != nil {
		return s.refuse(ctx, a.Entry, on(refused, object), "the caller does not hold the action",
			fmt.Errorf("%w: %s does not hold %s on the installation: %w",
				identity.ErrDenied, a.Caller.Subject, identity.ActionServerManagePrincipals, err))
	}

	return nil
}

// checkFreshSignIn refuses a caller whose console session has no sign-in in
// the window: a zero time, a sign-in older than the window, and a sign-in
// after now all refuse. The HTTP adapter checks the same session with the
// window of the session policy; this check does not rely on it. The window
// comes from Config.FreshWindow. With no window wired, or a window of 0, no
// sign-in is fresh: this fails closed. The refusal entry has the action refused.
func (s *Service) checkFreshSignIn(ctx context.Context, a *Actor, refused, object string) error {
	var window time.Duration
	if s.freshWindow != nil {
		window = s.freshWindow(ctx)
	}

	if sessionauth.Fresh(s.now(), a.SignedInAt, window) {
		return nil
	}

	return s.refuse(ctx, a.Entry, on(refused, object), "no fresh sign-in",
		fmt.Errorf("%w: the sign-in of the console session of the caller is missing or older than %s",
			identity.ErrDenied, window))
}

// checkTargetReach refuses a caller that does not hold every action that the
// grants of the account personID give: the same check that the disable of a
// principal runs. A rotation hands the caller the account at its next
// sign-in, so the caller must already hold what the account holds. With no
// Guard wired, only an administrator passes. This fails closed.
func (s *Service) checkTargetReach(ctx context.Context, a *Actor, object, personID string) error {
	guard := s.guard

	// Defense in depth: New accepts a Config without a Guard. This check
	// stays so that such a Service lets only an administrator rotate a
	// break-glass passkey.
	if guard == nil {
		guard = principal.AdminOnlyGuard{}
	}

	if err := guard.CheckPrincipalGrants(ctx, a.Caller, personID); err != nil {
		return s.refuse(ctx, a.Entry, on(ActionRotateRefused, object),
			"the caller does not hold the rights of the account",
			fmt.Errorf("%w: %s does not hold the actions that the grants of %s give: %w",
				identity.ErrDenied, a.Caller.Subject, principal.Ref(personID), err))
	}

	return nil
}

// entry returns the entry of base of the action on object at the time of the
// service, with the kind and the outcome.
func (s *Service) entry(base audit.Entry, action, object string, kind audit.Kind, outcome audit.Outcome) audit.Entry {
	e := base
	e.At = s.now()
	e.Namespace = audit.ServerScope
	e.Kind = kind
	e.Outcome = outcome
	e.Action = action
	e.Object = object

	return e
}

// target is the action and the object of an audit entry.
type target struct {
	action string
	object string
}

// on returns the target of action on object.
func on(action, object string) target {
	return target{action: action, object: object}
}

// refuse appends the entry of a refusal of the target with the cause, and
// returns err. A refusal that shows a check of a response or of the owner of
// a ceremony is a security entry. A refused append joins its error to err:
// the refusal changed nothing, and the caller still refuses.
func (s *Service) refuse(ctx context.Context, base audit.Entry, tg target, cause string, err error) error {
	outcome := audit.OutcomeDenied
	if errors.Is(err, mfa.ErrUnavailable) {
		outcome = audit.OutcomeFailure
	}

	kind := audit.KindChange
	if errors.Is(err, mfa.ErrVerification) || errors.Is(err, mfa.ErrWrongPerson) ||
		errors.Is(err, mfa.ErrSuspended) || errors.Is(err, mfa.ErrSourceChallengeLimit) {
		kind = audit.KindSecurity
	}

	e := s.entry(base, tg.action, tg.object, kind, outcome)
	e.Changes = []audit.Change{{Key: "cause", Old: nil, New: cause, Layer: "", TakesEffect: ""}}

	if _, appendErr := s.audit.AppendServer(ctx, e); appendErr != nil {
		s.log.ErrorContext(ctx, "append the audit entry of a passkey refusal", slog.String("action", tg.action),
			slog.String("error", appendErr.Error()))

		return errors.Join(err, fmt.Errorf("append the audit entry of a refusal: %w", appendErr))
	}

	return err
}

// challengeSource returns the budget key of the source and the key of the site
// group of a challenge of the kind k that starts from the address address,
// which the HTTP adapter took with the trusted-proxy rules. Only a sign-in has
// them: its start is public, so its open challenges are counted for each
// source (an IPv4 address or the /64 of an IPv6 address) and for each site
// group (the /48 of an IPv6 address).
//
// Defense in depth: this check does not rely on the HTTP adapter or on
// clientip. Only text that parses as an IP address, with no zone, gets a key
// of its own. Any other text, whatever its characters, gets mfa.UnknownSource
// for both keys, which has one shared budget. A sender that varies the text
// cannot open a new budget with each value, and a request that hides its
// address is limited and never free. The store checks both keys again.
func challengeSource(k mfa.Ceremony, address string) sourceKeys {
	unknown := sourceKeys{source: mfa.UnknownSource, group: mfa.UnknownSource}

	if k != mfa.CeremonySignIn {
		return sourceKeys{source: "", group: ""}
	}

	addr, err := netip.ParseAddr(address)
	if err != nil || addr.Zone() != "" {
		return unknown
	}

	keys := sourceKeys{source: authgate.SourceKey(addr.String()), group: authgate.SourceGroupKey(addr.String())}

	if !mfa.ValidSource(keys.source) || !mfa.ValidSource(keys.group) {
		return unknown
	}

	return keys
}

// sourceKeys are the budget key of the source of a sign-in start and the key
// of its site group.
type sourceKeys struct {
	source string
	group  string
}

// newChallenge stores a new challenge for the kind, the person, the pending
// sign-in, the session and the source of want, and returns its bytes. It sets
// the times of the challenge.
func (s *Service) newChallenge(ctx context.Context, want *mfa.Challenge) ([]byte, error) {
	b := make([]byte, mfa.ChallengeBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("%w: random bytes: %w", mfa.ErrUnavailable, err)
	}

	at := s.now()
	c := *want
	c.CreatedAt, c.ExpiresAt = at, mfa.ChallengeExpiry(at)

	if err := s.store.CreateChallenge(ctx, c, mfa.DigestOf(b)); err != nil {
		return nil, fmt.Errorf("store the challenge: %w", err)
	}

	return b, nil
}

// take reads the head of response and takes its challenge from the store,
// so that the challenge works once, also when a later check fails.
func (s *Service) take(ctx context.Context, response []byte) (mfa.ResponseHead, mfa.Challenge, error) {
	// Defense in depth: the HTTP adapter limits the body, and the relying
	// party adapter checks the size too. This check stays so that no parser
	// reads a larger response from a new caller.
	if len(response) == 0 || len(response) > mfa.MaxResponseBytes {
		return mfa.ResponseHead{}, mfa.Challenge{}, fmt.Errorf("%w: the response has %d bytes", mfa.ErrInvalid,
			len(response))
	}

	h, err := s.rp.Head(response)
	if err != nil {
		return mfa.ResponseHead{}, mfa.Challenge{}, fmt.Errorf("read the response: %w", err)
	}

	c, err := s.store.TakeChallenge(ctx, mfa.DigestOf(h.Challenge), s.now())
	if err != nil {
		return mfa.ResponseHead{}, mfa.Challenge{}, fmt.Errorf("take the challenge: %w", err)
	}

	return h, c, nil
}

// usable returns the passkeys of the person that are not suspended, or
// mfa.ErrNotFound when none is.
func (s *Service) usable(ctx context.Context, personID string) ([]mfa.Passkey, error) {
	if personID == "" {
		return nil, fmt.Errorf("%w: the ceremony names no person", mfa.ErrInvalid)
	}

	keys, err := s.store.Passkeys(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("read the passkeys: %w", err)
	}

	out := make([]mfa.Passkey, 0, len(keys))

	for i := range keys {
		if keys[i].SuspendedAt.IsZero() {
			out = append(out, keys[i])
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the person has no passkey that is not suspended", mfa.ErrNotFound)
	}

	return out, nil
}

// checkBreakGlassPerson refuses a person that the add or the rotation of
// another account's passkey must not change: an unknown principal, a
// principal that is not a person of the installation, and a person that is
// not a break-glass account. It answers mfa.ErrUnavailable when the store
// cannot read the principal. The refusal entry has the action refusal.
func (s *Service) checkBreakGlassPerson(ctx context.Context, a *Actor, refusal, personID string) error {
	object := Object(personID)

	reader, ok := s.store.(PrincipalReader)
	if !ok {
		return s.refuse(ctx, a.Entry, on(refusal, object), "the store cannot read the principal",
			mfa.ErrUnavailable)
	}

	person, err := reader.GetPrincipal(ctx, personID)
	if err != nil && !errors.Is(err, principal.ErrNotFound) {
		return s.refuse(ctx, a.Entry, on(refusal, object), "the principal cannot be read",
			mfa.ErrUnavailable)
	}

	if err != nil || !person.BreakGlass || person.Kind != principal.KindPerson || person.Namespace != "" {
		return s.refuse(ctx, a.Entry, on(refusal, object), "the person is not a break-glass account",
			mfa.ErrNotFound)
	}

	return nil
}
