package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Expirer ends one temporary administrator and keeps its record as a
// tombstone. Store implements it.
type Expirer interface {
	// ExpireTempAdmin ends the temporary administrator of the principal
	// principalID at e.At, with the entry e, as Store.ExpireTempAdmin
	// does. It returns ErrNotFound when no record exists, and ErrExpired
	// when the record already ended.
	ExpireTempAdmin(ctx context.Context, principalID string, e audit.Entry) error
}

// Principals reads the principal of a caller. Store adapters of
// principal.Store implement it.
type Principals interface {
	// GetPrincipalBySubject returns the live principal with the subject
	// subject, or principal.ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
}

// Request is the client address and the request ID of one request, for
// its audit entries.
type Request struct {
	SourceIP  string
	RequestID string
}

// RequestOf answers the Request of ctx, with empty fields when ctx holds
// none. The HTTP adapter reads them, so this package needs no HTTP code.
type RequestOf func(ctx context.Context) Request

// DecoratorConfig holds the dependencies of a Decorator.
type DecoratorConfig struct {
	// Next resolves the token first. Required.
	Next identity.Authenticator
	// Records reads the temporary administrator records. Required.
	Records Reader
	// Principals reads the principal of a caller with a record, so the
	// Decorator compares the principal of the record with the principal of
	// the caller. Required.
	Principals Principals
	// Request answers the client address and the request ID of a request
	// for its audit entries. Nil means empty values.
	Request RequestOf
	// Expirer disables an expired record. Required: the first use after
	// the expiry writes the expiry entry at once.
	Expirer Expirer
	// Audit appends the sign-in entry of each use. Required.
	Audit audit.ServerStore
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Logger takes a warning for each record that a use after the expiry
	// ends, and for each such expiry that fails. Nil discards them.
	Logger *slog.Logger
}

// Decorator is an identity.Authenticator that marks the temporary
// administrators among the callers that Next resolves. A bootstrap
// administrator never carries the flag: it has no stored record.
//
// Each request of a temporary administrator writes one sign-in audit entry,
// with the client address and the request ID of the request, so each action
// traces to the named person. A use at or after the expiry ends the record
// first, then answers identity.ErrUnauthenticated wrapping ErrExpired, with
// no sign-in entry. The record stays as a tombstone, so a later use is
// still denied: a missing record means the caller never was a temporary
// administrator. A failed store read fails closed.
type Decorator struct {
	next       identity.Authenticator
	records    Reader
	principals Principals
	request    RequestOf
	expirer    Expirer
	audit      audit.ServerStore
	now        func() time.Time
	logger     *slog.Logger
}

var _ identity.Authenticator = (*Decorator)(nil)

// NewDecorator returns a Decorator. Every dependency but Request, Now and
// Logger is required.
func NewDecorator(cfg DecoratorConfig) (*Decorator, error) {
	switch {
	case cfg.Next == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Records == nil:
		return nil, fmt.Errorf("%w: temporary administrator reader", ErrMissingDependency)
	case cfg.Principals == nil:
		return nil, fmt.Errorf("%w: principal reader", ErrMissingDependency)
	case cfg.Expirer == nil:
		return nil, fmt.Errorf("%w: temporary administrator expirer", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	request := cfg.Request
	if request == nil {
		request = func(context.Context) Request { return Request{SourceIP: "", RequestID: ""} }
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Decorator{
		next: cfg.Next, records: cfg.Records, principals: cfg.Principals, request: request,
		expirer: cfg.Expirer, audit: cfg.Audit, now: now, logger: logger,
	}, nil
}

// Now returns the time of the Decorator clock, in UTC to the millisecond:
// the time a sign-in and its audit entry carry.
func (d *Decorator) Now() time.Time {
	return d.now().UTC().Truncate(time.Millisecond)
}

// AdminSubjects returns the subjects of the bootstrap administrators of
// Next, when it names them, so an access-review export shows them behind
// the decorator too.
func (d *Decorator) AdminSubjects() []string {
	if named, ok := d.next.(interface{ AdminSubjects() []string }); ok {
		return named.AdminSubjects()
	}

	return nil
}

// signInScope holds the subjects of one request whose sign-in entry is
// already written. The server seeds one scope for each request, so the
// request log and the adapter resolve the token twice but write one entry.
// A use without a scope writes its entry at once, as before.
type signInScope struct {
	mu       sync.Mutex
	recorded map[string]bool
}

// scopeKey holds the sign-in scope in the context.
type scopeKey struct{}

// WithSignInScope returns ctx with a fresh sign-in scope for one request.
// Call it once for each request, before any authentication.
func WithSignInScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, scopeKey{}, &signInScope{mu: sync.Mutex{}, recorded: map[string]bool{}})
}

// scopeOf returns the sign-in scope of ctx, or nil when ctx holds none.
func scopeOf(ctx context.Context) *signInScope {
	scope, ok := ctx.Value(scopeKey{}).(*signInScope)

	if !ok || scope == nil {
		// No scope is valid: the use writes its entry at once.
		return nil
	}

	return scope
}

// seen reports whether the subject already has its sign-in entry in the
// scope.
func (s *signInScope) seen(subject string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.recorded[subject]
}

// record notes that the subject has its sign-in entry in the scope.
func (s *signInScope) record(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recorded[subject] = true
}

// AuthenticateToken resolves token with Next, then marks a temporary
// administrator and writes its sign-in entry. A caller with no record, and
// a bootstrap administrator, pass through unchanged. A record counts only
// when it names the principal of the caller, so a new person with the
// subject of an old one is not the old temporary administrator. A caller
// whose record is not active is denied: the record stays as a tombstone,
// so the deny holds for every later use too.
func (d *Decorator) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	p, err := d.next.AuthenticateToken(ctx, token)
	if err != nil {
		return identity.Principal{}, err
	}

	if p.Admin {
		return p, nil
	}

	rec, err := d.records.GetTempAdminBySubject(ctx, p.Subject)
	if errors.Is(err, ErrNotFound) {
		return p, nil
	}

	if err != nil {
		return identity.Principal{}, fmt.Errorf("read temporary administrator for %q: %w", p.Subject, err)
	}

	own, err := d.ownsRecord(ctx, p.Subject, rec)
	if err != nil {
		return identity.Principal{}, err
	}

	if !own {
		return p, nil
	}

	now := d.Now()

	if !rec.ActiveAt(now) {
		return identity.Principal{}, d.expire(ctx, rec)
	}

	p.TemporaryAdministrator = true
	p.TemporaryExpiresAt = rec.ExpiresAt

	scope := scopeOf(ctx)
	if scope != nil && scope.seen(p.Subject) {
		return p, nil
	}

	if err := d.appendSignIn(ctx, p, rec, now); err != nil {
		return identity.Principal{}, err
	}

	if scope != nil {
		scope.record(p.Subject)
	}

	return p, nil
}

// ownsRecord reports whether rec names the principal of the caller with
// the subject subject. A failed read fails closed.
func (d *Decorator) ownsRecord(ctx context.Context, subject string, rec TempAdmin) (bool, error) {
	caller, err := d.principals.GetPrincipalBySubject(ctx, subject)
	if errors.Is(err, principal.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("read principal %q: %w", subject, err)
	}

	return caller.ID == rec.PrincipalID, nil
}

// expire ends the expired record rec with an expiry entry, then denies
// the use. The record stays as a tombstone, so a later use is denied too.
// Any store failure denies the use as well, so no use passes without its
// record ending first. A failure is logged with the principal and no
// driver text. A record that this use ends logs that no break-glass review
// opens for it.
func (d *Decorator) expire(ctx context.Context, rec TempAdmin) error {
	now := d.Now()
	req := d.request(ctx)

	err := d.expirer.ExpireTempAdmin(ctx, rec.PrincipalID, expireEntry(audit.Entry{
		FenceVersion: 0,
		At:           time.Time{},
		Namespace:    "",
		Subject:      SweepSubject,
		IDPSubject:   "",
		TokenID:      "",
		Action:       "",
		Object:       "",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     req.SourceIP,
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         "",
		Outcome:      "",
		RequestID:    req.RequestID,
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		Reason:       "",
	}, now, Ref(rec.PrincipalID)))

	switch {
	case err == nil:
		warnNoReview(ctx, d.logger, rec.PrincipalID)
	case errors.Is(err, ErrExpired), errors.Is(err, ErrNotFound):
	default:
		d.logger.WarnContext(ctx, "temporary administrator expiry failed", slog.String("principal", rec.PrincipalID))
	}

	return fmt.Errorf("%w: %w", identity.ErrUnauthenticated, ErrExpired)
}

// appendSignIn writes the sign-in entry of one request of a temporary
// administrator: who signed in, with which token, from where, and when. A
// failed write denies the use, so no use passes without its entry.
func (d *Decorator) appendSignIn(
	ctx context.Context, p identity.Principal, rec TempAdmin, now time.Time,
) error {
	req := d.request(ctx)

	e := audit.Entry{
		FenceVersion: 0,
		At:           now,
		Namespace:    audit.ServerScope,
		Subject:      p.Subject,
		IDPSubject:   "",
		TokenID:      p.TokenID,
		Action:       Action,
		Object:       Ref(rec.PrincipalID),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     req.SourceIP,
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    req.RequestID,
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		Reason:       "",
	}

	if _, err := d.audit.AppendServer(ctx, e); err != nil {
		return fmt.Errorf("write the sign-in audit entry: %w", err)
	}

	return nil
}
