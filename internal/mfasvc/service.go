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
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Audit actions of the passkeys. They are not authorization actions.
const (
	ActionPasskeyAdded     = "passkey.added"
	ActionPasskeyRemoved   = "passkey.removed"
	ActionPasskeySuspended = "passkey.suspended"
	ActionAddRefused       = "passkey.add-refused"
	ActionRemoveRefused    = "passkey.remove-refused"
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

// Config holds the dependencies of a Service. Every field but Now and Log is
// required.
type Config struct {
	Store mfa.PasskeyStore
	RP    mfa.RelyingParty
	Audit Auditor
	Now   func() time.Time
	// Log receives the errors of the refusals whose audit entry cannot be
	// written. Nil means slog.Default().
	Log *slog.Logger
}

// ErrMissingDependency reports a Config with a required field that is nil.
var ErrMissingDependency = errors.New("mfasvc: missing dependency")

// Service is the service of the passkeys.
type Service struct {
	store mfa.PasskeyStore
	rp    mfa.RelyingParty
	audit Auditor
	now   func() time.Time
	log   *slog.Logger
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

	return &Service{store: cfg.Store, rp: cfg.RP, audit: cfg.Audit, now: now, log: log, watch: nil}, nil
}

// Actor is the caller of a step: the audit entry that the HTTP adapter fills
// (subject, source address, user agent, channel, request ID), and the
// principal ID and the session ID of the caller from its console session.
// Both IDs are empty for the start and the end of a passkey sign-in, which
// has no session yet.
type Actor struct {
	Entry       audit.Entry
	PrincipalID string
	SessionID   string
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
func (s *Service) BeginRegistration(ctx context.Context, a Actor, p mfa.Person) ([]byte, error) {
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
		SessionID: a.SessionID, Source: "",
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
	ctx context.Context, a Actor, p mfa.Person, name string, response []byte,
) (mfa.Passkey, error) {
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
	if c.Kind != mfa.CeremonyRegister || a.PrincipalID == "" || a.SessionID == "" ||
		c.PersonID != a.PrincipalID || c.SessionID != a.SessionID || p.ID != a.PrincipalID {
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

// BeginAssertion starts an assertion ceremony of the kind k and returns the
// JSON of the request options. A sign-in names no person. A second factor
// names the person and the pending sign-in in b. A fresh step is of the
// person and the session of the caller.
func (s *Service) BeginAssertion(ctx context.Context, a Actor, k mfa.Ceremony, b Binding) ([]byte, error) {
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

	c, err := s.newChallenge(ctx, &mfa.Challenge{
		CreatedAt: time.Time{}, ExpiresAt: time.Time{}, Kind: k, PersonID: personID, PendingID: pendingID,
		SessionID: sessionID, Source: challengeSource(k, a.Entry.SourceIP),
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
func bound(k mfa.Ceremony, c mfa.Challenge, a Actor, b Binding) bool {
	binds := map[mfa.Ceremony]mfa.Challenge{
		mfa.CeremonySignIn: {
			CreatedAt: time.Time{},
			ExpiresAt: time.Time{},
			Kind:      k,
			PersonID:  "",
			PendingID: "",
			SessionID: "",
			Source:    "",
		},
		mfa.CeremonySecondFactor: {
			CreatedAt: time.Time{}, ExpiresAt: time.Time{}, Kind: k, PersonID: b.PersonID, PendingID: b.PendingID,
			SessionID: "", Source: "",
		},
		mfa.CeremonyFresh: {
			CreatedAt: time.Time{}, ExpiresAt: time.Time{}, Kind: k, PersonID: a.PrincipalID, PendingID: "",
			SessionID: a.SessionID, Source: "",
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
	ctx context.Context, a Actor, k mfa.Ceremony, b Binding, response []byte,
) (mfa.Passkey, error) {
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
func (s *Service) FinishSignIn(ctx context.Context, a Actor, response []byte) (SignedIn, error) {
	var none SignedIn

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
	actor    Actor
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
func (s *Service) Passkeys(ctx context.Context, a Actor) ([]mfa.Passkey, error) {
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
func (s *Service) RemovePasskey(ctx context.Context, a Actor, id string) error {
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

// challengeSource returns the budget key of the source of a challenge of the
// kind k that starts from the address address, which the HTTP adapter took
// with the trusted-proxy rules. Only a sign-in has a source: its start is
// public, so its open challenges are counted for each source. An address that
// is empty, or is not valid text of at most mfa.MaxSourceBytes bytes, gets
// mfa.UnknownSource, which has one shared budget, so a request that hides its
// address is limited and never free.
//
// Defense in depth: the store checks the source of the challenge again.
func challengeSource(k mfa.Ceremony, address string) string {
	if k != mfa.CeremonySignIn {
		return ""
	}

	key := authgate.SourceKey(address)

	if !mfa.ValidSource(key) {
		return mfa.UnknownSource
	}

	return key
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
