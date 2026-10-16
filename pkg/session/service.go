package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Audit actions of a session. They are not authorization actions: a
// sign-in and a sign-out need no grant.
const (
	// ActionSignIn is the action of a sign-in, of the principal that a
	// first sign-in makes, and of a refused sign-in.
	ActionSignIn = "session:sign-in"
	// ActionSignOut is the action of a sign-out.
	ActionSignOut = "session:sign-out"
)

// TouchEvery is how often a use of a session records its last use. The
// idle limit is exact to about this period.
const TouchEvery = time.Minute

// ErrUnauthenticated reports a secret that names no session that can
// authenticate: no session, a session that ended, is idle or expired, or a
// session of a disabled principal.
var ErrUnauthenticated = errors.New("session is not valid")

// SessionsConfig holds the dependencies of a Sessions service. Store and
// Policies are required. A nil Now uses time.Now.
type SessionsConfig struct {
	Store    Store
	Policies *Policies
	Now      func() time.Time
}

// Sessions starts, checks and ends console sessions, and keeps the sign-in
// requests between the start and the callback of a sign-in.
type Sessions struct {
	store    Store
	policies *Policies
	now      func() time.Time
}

// NewSessions returns a Sessions service. It returns ErrMissingDependency
// when cfg has no store or no policies.
func NewSessions(cfg SessionsConfig) (*Sessions, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: session store", ErrMissingDependency)
	case cfg.Policies == nil:
		return nil, fmt.Errorf("%w: session policies", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Sessions{store: cfg.Store, policies: cfg.Policies, now: now}, nil
}

// Now returns the time of the clock of the service, in UTC, to the
// millisecond that a store keeps.
func (s *Sessions) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Start names a new session. AuthTime is the time of the authentication at
// the identity provider; a time after the start of the session takes the
// start.
type Start struct {
	AuthTime    time.Time
	PrincipalID string
	Provider    string
	SourceIP    string
	UserAgent   string
}

// Start begins a session for in and returns it with its secret. e is the
// entry of the caller; Start sets its time, scope, kind, outcome and
// object. It returns the errors of Store.CreateSession.
func (s *Sessions) Start(ctx context.Context, e audit.Entry, in Start) (Session, Secret, error) {
	secret, err := NewSecret()
	if err != nil {
		return Session{}, Secret{}, err
	}

	at := s.Now()

	authTime := in.AuthTime.UTC().Truncate(time.Millisecond)
	if authTime.IsZero() || authTime.After(at) {
		authTime = at
	}

	sess := Session{
		CreatedAt:   at,
		LastSeenAt:  at,
		AuthTime:    authTime,
		EndedAt:     time.Time{},
		ID:          NewID(),
		PrincipalID: in.PrincipalID,
		Provider:    in.Provider,
		EndReason:   "",
		SourceIP:    in.SourceIP,
		UserAgent:   in.UserAgent,
	}

	if err := sess.Validate(); err != nil {
		return Session{}, Secret{}, err
	}

	change := entry(e, at, FullRef(in.PrincipalID, sess.ID))
	if err := s.store.CreateSession(ctx, sess, DigestOf(secret), change); err != nil {
		return Session{}, Secret{}, fmt.Errorf("start session: %w", err)
	}

	return sess, secret, nil
}

// Authenticate returns the session with the secret and its owner when the
// session can authenticate at the time of the clock: it has not ended, is
// not idle or expired under the current policy, and its owner is a person
// who is not disabled. It records the use at most once in TouchEvery. It
// returns ErrUnauthenticated otherwise.
func (s *Sessions) Authenticate(ctx context.Context, secret Secret) (Match, error) {
	m, err := s.store.GetSessionByDigest(ctx, DigestOf(secret))
	if errors.Is(err, ErrNotFound) {
		return Match{}, ErrUnauthenticated
	}

	if err != nil {
		return Match{}, fmt.Errorf("read session: %w", err)
	}

	policy, err := s.policies.Policy(ctx)
	if err != nil {
		return Match{}, err
	}

	now := s.Now()

	if status := m.Session.StatusAt(now, policy.Limits); status != StatusActive {
		return Match{}, fmt.Errorf("%w: %s", ErrUnauthenticated, status)
	}

	if m.Owner.Kind != principal.KindPerson || !m.Owner.DisabledAt.IsZero() {
		return Match{}, fmt.Errorf("%w: the principal cannot sign in", ErrUnauthenticated)
	}

	if err := s.store.TouchSession(ctx, m.Session.ID, now, TouchEvery); err != nil {
		return Match{}, fmt.Errorf("record session use: %w", err)
	}

	return m, nil
}

// SignOut ends the session id with the reason ReasonSignOut. e is the
// entry of the caller; SignOut sets its time, scope, kind, outcome and
// object. It returns the errors of Store.EndSession.
func (s *Sessions) SignOut(ctx context.Context, e audit.Entry, principalID, id string) error {
	at := s.Now()

	if err := s.store.EndSession(ctx, id, ReasonSignOut, entry(e, at, FullRef(principalID, id))); err != nil {
		return fmt.Errorf("end session: %w", err)
	}

	return nil
}

// Begin names a new sign-in request: the provider, the nonce and the PKCE
// verifier of the authorization request, and the return path.
type Begin struct {
	Provider   string
	Nonce      string
	Verifier   string
	ReturnPath string
}

// BeginRequest stores a new sign-in request and returns its state, the
// secret that the authorization request and the binding cookie carry. The
// request expires after RequestLifetime. It returns ErrInvalid when in
// fails validation.
func (s *Sessions) BeginRequest(ctx context.Context, in Begin) (Secret, error) {
	state, err := NewSecret()
	if err != nil {
		return Secret{}, err
	}

	at := s.Now()
	r := Request{
		CreatedAt:  at,
		ExpiresAt:  at.Add(RequestLifetime),
		Provider:   in.Provider,
		Nonce:      in.Nonce,
		Verifier:   in.Verifier,
		ReturnPath: in.ReturnPath,
	}

	if err := s.store.CreateRequest(ctx, r, DigestOf(state)); err != nil {
		return Secret{}, fmt.Errorf("begin sign-in: %w", err)
	}

	return state, nil
}

// TakeRequest returns the sign-in request with the state and deletes it, so
// that it works once. It returns ErrNotFound when no request has the
// state, or when it expired.
func (s *Sessions) TakeRequest(ctx context.Context, state Secret) (Request, error) {
	r, err := s.store.TakeRequest(ctx, DigestOf(state), s.Now())
	if err != nil {
		return Request{}, fmt.Errorf("take sign-in request: %w", err)
	}

	return r, nil
}

// entry completes the entry of the caller for one change of the record
// object at the time at.
func entry(e audit.Entry, at time.Time, object string) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = object

	return e
}
