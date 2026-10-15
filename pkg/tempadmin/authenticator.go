package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Expirer drops the record of one temporary administrator and disables its
// principal. Store implements it.
type Expirer interface {
	// ExpireTempAdmin disables the principal principalID and drops its
	// record at e.At, with the entry e. It returns ErrNotFound when no
	// record exists.
	ExpireTempAdmin(ctx context.Context, principalID string, e audit.Entry) error
}

// DecoratorConfig holds the dependencies of a Decorator.
type DecoratorConfig struct {
	// Next resolves the token first. Required.
	Next identity.Authenticator
	// Records reads the temporary administrator records. Required.
	Records Reader
	// Expirer disables an expired record. Required: the first use after
	// the expiry writes the expiry entry at once.
	Expirer Expirer
	// Audit appends the sign-in entry of each use. Required.
	Audit audit.ServerStore
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
}

// Decorator is an identity.Authenticator that marks the temporary
// administrators among the callers that Next resolves. A bootstrap
// administrator never carries the flag: it has no stored record.
//
// Each use of a temporary administrator writes a sign-in audit entry, so
// each action traces to the named person. A use at or after the expiry
// disables the principal with an expiry entry first, then answers
// identity.ErrUnauthenticated wrapping ErrExpired. A failed store read
// fails closed.
type Decorator struct {
	next    identity.Authenticator
	records Reader
	expirer Expirer
	audit   audit.ServerStore
	now     func() time.Time
}

var _ identity.Authenticator = (*Decorator)(nil)

// NewDecorator returns a Decorator. Every dependency but Now is required.
func NewDecorator(cfg DecoratorConfig) (*Decorator, error) {
	switch {
	case cfg.Next == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Records == nil:
		return nil, fmt.Errorf("%w: temporary administrator reader", ErrMissingDependency)
	case cfg.Expirer == nil:
		return nil, fmt.Errorf("%w: temporary administrator expirer", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Decorator{
		next: cfg.Next, records: cfg.Records, expirer: cfg.Expirer, audit: cfg.Audit, now: now,
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

// AuthenticateToken resolves token with Next, then marks a temporary
// administrator and writes its sign-in entry. A caller with no record, and
// a bootstrap administrator, pass through unchanged.
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

	now := d.Now()

	if !rec.ActiveAt(now) {
		return identity.Principal{}, d.expire(ctx, rec)
	}

	p.TemporaryAdministrator = true
	p.TemporaryExpiresAt = rec.ExpiresAt

	if err := d.appendSignIn(ctx, p, rec, now); err != nil {
		return identity.Principal{}, err
	}

	return p, nil
}

// expire disables the principal of the expired record rec with an expiry
// entry, then denies the use. A record that a sweep already dropped still
// denies the use.
func (d *Decorator) expire(ctx context.Context, rec TempAdmin) error {
	now := d.Now()

	err := d.expirer.ExpireTempAdmin(ctx, rec.PrincipalID, expireEntry(audit.Entry{
		At:         time.Time{},
		Namespace:  "",
		Subject:    rec.Subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     "",
		Object:     "",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "",
		UserAgent:  "",
		Channel:    audit.ChannelAPI,
		Kind:       "",
		Outcome:    "",
		RequestID:  "",
		ObjectID:   "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}, now, Ref(rec.PrincipalID)))
	if err != nil {
		return fmt.Errorf("%w: %w", identity.ErrUnauthenticated, ErrExpired)
	}

	return fmt.Errorf("%w: %w", identity.ErrUnauthenticated, ErrExpired)
}

// appendSignIn writes the sign-in entry of one use of a temporary
// administrator: who signed in, with which token, and when. A failed write
// denies the use, so no use passes without its entry.
func (d *Decorator) appendSignIn(
	ctx context.Context, p identity.Principal, rec TempAdmin, now time.Time,
) error {
	e := audit.Entry{
		At:         now,
		Namespace:  audit.ServerScope,
		Subject:    p.Subject,
		IDPSubject: "",
		TokenID:    p.TokenID,
		Action:     Action,
		Object:     Ref(rec.PrincipalID),
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "",
		UserAgent:  "",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "",
		ObjectID:   "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}

	if _, err := d.audit.AppendServer(ctx, e); err != nil {
		return fmt.Errorf("write the sign-in audit entry: %w", err)
	}

	return nil
}
