package session

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/notify"
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
	// ActionGenerationBumped is the action of a bump of the session
	// generation of a person. Its changes name the cause and the new
	// generation.
	ActionGenerationBumped = "session:generation-bumped"
)

// TouchEvery is how often a use of a session records its last use. The
// idle limit is exact to about this period.
const TouchEvery = time.Minute

// ErrUnauthenticated reports a secret that names no session that can
// authenticate: no session, a session that ended, is idle or expired, or a
// session of a disabled principal.
var ErrUnauthenticated = errors.New("session is not valid")

// InactivityRule answers whether the user account of the owner o of a
// session is past the inactivity period of p at now, by the rule that also
// refuses an access token of that account: the account disable is on, the
// period is not zero, o is a person that is not a break-glass account, and
// o.ActiveAt is at or before now minus the period. The policy p comes from
// the session lookup.
type InactivityRule interface {
	AccountInactive(o Owner, p AccountPolicy, now time.Time) bool
}

// SessionsConfig holds the dependencies of a Sessions service. Store is
// required. A nil Inactivity applies no inactivity rule. A nil Now uses
// time.Now.
type SessionsConfig struct {
	Store Store
	// Providers creates the sessions of the stored sign-in providers. It
	// may be nil when no provider is stored; a Start with Stored then
	// fails with ErrMissingDependency.
	Providers  ProviderSessionStore
	Inactivity InactivityRule
	Now        func() time.Time
}

// Sessions starts, checks and ends console sessions, and keeps the sign-in
// requests between the start and the callback of a sign-in.
type Sessions struct {
	store      Store
	providers  ProviderSessionStore
	inactivity InactivityRule
	now        func() time.Time
}

// NewSessions returns a Sessions service. It returns ErrMissingDependency
// when cfg has no store.
func NewSessions(cfg SessionsConfig) (*Sessions, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: session store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Sessions{
		store: cfg.Store, providers: cfg.Providers, inactivity: cfg.Inactivity, now: now,
	}, nil
}

// Now returns the time of the clock of the service, in UTC, to the
// millisecond that a store keeps.
func (s *Sessions) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Start names a new session. AuthTime is the time of the authentication at
// the identity provider; a time after the start of the session takes the
// start. Stored reports that Provider is a stored provider: the session is
// then created through ProviderSessionStore, which checks the provider in
// the same write. Generation is the session generation of the principal
// that the sign-in read in the same read as the credential, or 0 to take the
// stored generation in the write that stores the session. A generation that
// is not the stored one makes Start fail with ErrGenerationChanged.
type Start struct {
	AuthTime    time.Time
	PrincipalID string
	Provider    string
	SourceIP    string
	UserAgent   string
	Generation  uint64
	Stored      bool
	// Alert is a notification that the store appends to the outbox of the
	// installation in the same transaction as the session: both commit, or
	// neither. Start sets its object, its object ID and its dedupe key from
	// the new session. Nil means no alert. A Start with an alert and a store
	// that cannot write one fails with ErrMissingDependency, and writes no
	// session. It cannot be used with Stored.
	Alert *notify.Notification
}

// AlertStore is the part of a Store that starts a session and an alert in one
// transaction. The stores of SQLite and PostgreSQL meet it.
type AlertStore interface {
	// CreateSessionWithAlert is CreateSession, and it appends n to the
	// outbox of audit.ServerScope in the same transaction. A notification
	// that fails validation, or that is not in audit.ServerScope, returns
	// notify.ErrInvalid and changes nothing, no session included. A
	// notification with the dedupe key of a stored one is skipped, as
	// everywhere else in the outbox.
	CreateSessionWithAlert(ctx context.Context, s Session, d Digest, e audit.Entry, n notify.Notification) error
}

// Start begins a session for in and returns it with its secret. e is the
// entry of the caller; Start sets its time, scope, kind, outcome and
// object. It returns the errors of Store.CreateSession, or of
// ProviderSessionStore.CreateProviderSession when in.Stored.
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
		Generation:  in.Generation,
	}

	if err := sess.Validate(); err != nil {
		return Session{}, Secret{}, err
	}

	change := entry(e, at, FullRef(in.PrincipalID, sess.ID))
	if err := s.create(ctx, in, sess, DigestOf(secret), change); err != nil {
		return Session{}, Secret{}, fmt.Errorf("start session: %w", err)
	}

	return sess, secret, nil
}

// Authenticate returns the session with the secret and its owner when the
// session can authenticate at the time of the clock: it has not ended, is
// not idle or expired under the current policy, and its owner is a person
// who is not disabled and whose account is not past the inactivity period.
// The policies come from the same read as the session. It records the use
// at most once in TouchEvery. It returns ErrUnauthenticated otherwise, and
// then records no use.
func (s *Sessions) Authenticate(ctx context.Context, secret Secret) (Match, error) {
	m, err := s.store.GetSessionByDigest(ctx, DigestOf(secret))
	if errors.Is(err, ErrNotFound) {
		return Match{}, ErrUnauthenticated
	}

	if err != nil {
		return Match{}, fmt.Errorf("read session: %w", err)
	}

	now := s.Now()
	limits := m.Policy.Limits

	if status := m.Session.StatusAt(now, limits); status != StatusActive {
		return Match{}, fmt.Errorf("%w: %s", ErrUnauthenticated, status)
	}

	if m.Owner.Kind != principal.KindPerson || !m.Owner.DisabledAt.IsZero() {
		return Match{}, fmt.Errorf("%w: the principal cannot sign in", ErrUnauthenticated)
	}

	// A password write, a reset or another change of the credentials adds 1
	// to the session generation of the person, so each session of the person
	// that carries an older generation ends here. A generation of 0 is a
	// session from before the generation existed: no principal has it, so it
	// fails closed too.
	if m.Session.Generation == 0 || m.Session.Generation != m.Generation {
		return Match{}, fmt.Errorf("%w: %w", ErrUnauthenticated, ErrGenerationChanged)
	}

	if s.inactivity != nil && s.inactivity.AccountInactive(m.Owner, m.Accounts, now) {
		return Match{}, fmt.Errorf("%w: the user account is inactive", ErrUnauthenticated)
	}

	if err := s.touch(ctx, m.Session, now, limits); err != nil {
		return Match{}, err
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

// Begin names a new sign-in request: its state, the secret that the
// authorization request and the binding cookie carry (NewSecret makes
// one), the provider, the nonce and the PKCE verifier of the authorization
// request, the return path, and the max_age of the authorization request
// when HasMaxAge is true.
type Begin struct {
	State      Secret
	Provider   string
	Nonce      string
	Verifier   string
	ReturnPath string
	MaxAge     time.Duration
	HasMaxAge  bool
}

// BeginRequest stores a new sign-in request, keyed by the digest of its
// state. The request expires after RequestLifetime. It returns ErrInvalid
// when in fails validation, and ErrAlreadyExists when the state is taken.
func (s *Sessions) BeginRequest(ctx context.Context, in Begin) error {
	at := s.Now()
	r := Request{
		CreatedAt:  at,
		ExpiresAt:  at.Add(RequestLifetime),
		Provider:   in.Provider,
		Nonce:      in.Nonce,
		Verifier:   in.Verifier,
		ReturnPath: in.ReturnPath,
		MaxAge:     in.MaxAge,
		HasMaxAge:  in.HasMaxAge,
	}

	if err := s.store.CreateRequest(ctx, r, DigestOf(in.State)); err != nil {
		return fmt.Errorf("begin sign-in: %w", err)
	}

	return nil
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

// PruneRequests deletes the sign-in requests that expired at or before
// now, and returns how many it deleted.
func (s *Sessions) PruneRequests(ctx context.Context) (int64, error) {
	n, err := s.store.PruneRequests(ctx, s.Now())
	if err != nil {
		return 0, fmt.Errorf("prune sign-in requests: %w", err)
	}

	return n, nil
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

// create stores sess, through the provider session store when in.Stored.
func (s *Sessions) create(ctx context.Context, in Start, sess Session, d Digest, e audit.Entry) error {
	if in.Alert != nil {
		return s.createWithAlert(ctx, in, sess, d, e)
	}

	if !in.Stored {
		return s.store.CreateSession(ctx, sess, d, e)
	}

	if s.providers == nil {
		return fmt.Errorf("%w: provider session store", ErrMissingDependency)
	}

	return s.providers.CreateProviderSession(ctx, sess, d, e)
}

// createWithAlert stores sess and the alert of in in one transaction. It
// fails closed: a store that cannot write an alert, or a Start with a stored
// provider, stores no session.
func (s *Sessions) createWithAlert(ctx context.Context, in Start, sess Session, d Digest, e audit.Entry) error {
	alerts, ok := s.store.(AlertStore)
	if !ok || in.Stored {
		return fmt.Errorf("%w: a store that writes a session and an alert in one transaction", ErrMissingDependency)
	}

	n := *in.Alert
	n.ObjectID = sess.ID
	n.DedupeKey = string(n.Type) + "/" + sess.ID

	// Copy the map: the caller keeps its own notification.
	n.Attributes = maps.Clone(n.Attributes)
	if n.Attributes == nil {
		n.Attributes = map[string]string{}
	}

	n.Attributes["session"] = sess.ID
	n.Attributes["principal"] = sess.PrincipalID

	return alerts.CreateSessionWithAlert(ctx, sess, d, e, n.Fit())
}

// touch records a use of sess at now. A use inside TouchEvery of the last
// one writes nothing, so a console request sends no write to the database
// in the common case. The store applies the same bound, so a race between
// two instances writes at most one use. In maintenance mode the store refuses
// the write, and touch skips the use instead of failing the request.
func (s *Sessions) touch(ctx context.Context, sess Session, now time.Time, l Limits) error {
	if !sess.LastSeenAt.IsZero() && now.Sub(sess.LastSeenAt) < TouchEvery {
		return nil
	}

	err := s.store.TouchSession(ctx, sess.ID, now, TouchEvery, l)
	if errors.Is(err, maintenance.ErrInMaintenance) {
		// Maintenance mode refuses the write of a use. The session stays valid
		// and the console keeps reading, for example the progress of a restore.
		return nil
	}

	if err != nil {
		return fmt.Errorf("record session use: %w", err)
	}

	return nil
}
