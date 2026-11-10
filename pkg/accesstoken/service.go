package accesstoken

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// Lifetimes of a token. RFC 20 keeps the defaults open. The maximum
// lifetime is a setting of the Policy (ADR 53, decision 2).
const (
	// DefaultLifetime is the lifetime of a token when a request names no
	// expiry, or the maximum lifetime of the Policy when that is shorter.
	DefaultLifetime = 90 * 24 * time.Hour
	// DefaultRotationOverlap is how long the old token of a rotation keeps
	// working when the request names no end time. The expiry of the old
	// token bounds it.
	DefaultRotationOverlap = 24 * time.Hour
	// touchEvery limits the writes of the last-used time: one write for
	// each token in this period at most (ADR 53, consequences).
	touchEvery = time.Minute
)

// PrincipalReader reads the principal records. principal.Store implements
// it.
type PrincipalReader interface {
	// GetPrincipal returns the principal with the ID id, or
	// principal.ErrNotFound when no such principal exists or it was deleted.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
	// GetPrincipalBySubject returns the principal that exists with the
	// subject subject, or principal.ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
}

// Freshness asks for a recent MFA step before an operation that shows a
// secret (ADR 56). Built-in MFA (ADR 46) does not exist yet, so the server
// wires no Freshness: with MFA off, ADR 56 adds no step (decision 5).
type Freshness interface {
	// RequireFresh returns nil when the caller subject passed an MFA step
	// inside the freshness window of token creation. Otherwise it returns
	// an error that asks for a new MFA step. The session of the caller
	// comes from ctx.
	RequireFresh(ctx context.Context, subject string) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Principals PrincipalReader
	// Freshness checks the MFA step before a create or a rotation. Nil
	// adds no step: MFA does not exist yet (ADR 56, decision 5).
	Freshness Freshness
	// Authorizer decides principal:manage-tokens on the service principal
	// of a service token. Nil lets only an administrator act on service
	// tokens (fail closed).
	Authorizer identity.Authorizer
	// Reach holds the ports that the reach check reads: the grants, the
	// roles, the callers and the principals of the installation. A make or
	// a rotation of a service token by a caller that holds
	// principal:manage-tokens on the installation compares the reach of
	// the principal with the reach of the caller. A zero or incomplete
	// config refuses each such change: the check never skips.
	Reach access.ReachConfig
	// TempAdmins reads the temporary administrator records for the
	// break-glass exemption of the inactivity checks. Nil turns the
	// exemption off: an inactive token is refused even for a temporary
	// administrator.
	TempAdmins tempadmin.Reader
	// Logger logs a failed write of a last-used time. Nil discards it.
	Logger *slog.Logger
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// NewID returns a new opaque ID with the prefix it takes. Nil means
	// random IDs.
	NewID func(prefix string) string
}

// Service implements the access token use cases, and resolves a secret to
// its principal as an identity.Authenticator.
//
// Each change takes the audit entry of the caller: who acted, from where,
// through which channel, and the action. The Service sets the time from its
// own clock, the scope, the kind change, the outcome success and the record,
// and the store appends the entry in the transaction of the change.
type Service struct {
	store      Store
	principals PrincipalReader
	freshness  Freshness
	authz      identity.Authorizer
	reach      access.ReachConfig
	tempAdmins tempadmin.Reader
	logger     *slog.Logger
	now        func() time.Time
	newID      func(prefix string) string
}

var _ identity.Authenticator = (*Service)(nil)

// NewService returns a Service. Store and Principals are required.
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: access token store", ErrMissingDependency)
	case cfg.Principals == nil:
		return nil, fmt.Errorf("%w: principal reader", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	newID := cfg.NewID
	if newID == nil {
		newID = ident.NewID
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Service{
		store: cfg.Store, principals: cfg.Principals, freshness: cfg.Freshness,
		authz: cfg.Authorizer, reach: cfg.Reach, tempAdmins: cfg.TempAdmins, logger: logger, now: now, newID: newID,
	}, nil
}

// Now returns the time of the Service clock, in UTC to the millisecond:
// the time a change and its audit entry carry.
func (s *Service) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Input names the fields of a new token. A zero ExpiresAt takes the default
// lifetime.
type Input struct {
	ExpiresAt   time.Time
	PrincipalID string
	Name        string
}

// Create issues a new token for a principal in scope, with the entry e
// of caller. For ScopeOwn the principal is the person of caller, and
// in.PrincipalID is ignored. ScopePersonal makes nothing: it returns
// ErrNotOwnToken, wrapped with identity.ErrDenied. For ScopeService the
// principal is in.PrincipalID, its kind must fit the scope, and the check of
// mayActFor runs. It returns the
// token and its secret: the only time the server shows the secret.
func (s *Service) Create(
	ctx context.Context, caller identity.Principal, e audit.Entry, scope Scope, in Input,
) (Token, Secret, error) {
	if err := s.requireFresh(ctx, e.Subject); err != nil {
		return Token{}, Secret{}, err
	}

	if err := ValidateName(in.Name); err != nil {
		return Token{}, Secret{}, err
	}

	p, err := s.targetPrincipal(ctx, caller, scope, in.PrincipalID)
	if err != nil {
		return Token{}, Secret{}, fmt.Errorf("create access token: %w", err)
	}

	// Defense in depth: the principal API refuses this subject, so no record
	// should have it. This check stays so that a record from a restore or a
	// row of another version never gets a token.
	if refuseErr := refuseNamespaceServiceOwner(p.Subject); refuseErr != nil {
		return Token{}, Secret{}, fmt.Errorf("create access token: %w", refuseErr)
	}

	kind, err := KindFor(p.Kind)
	if err != nil {
		return Token{}, Secret{}, err
	}

	if kindErr := checkKind(scope, kind); kindErr != nil {
		return Token{}, Secret{}, kindErr
	}

	if scope != ScopeOwn {
		if mayErr := s.mayActFor(ctx, caller, kind, p.ID); mayErr != nil {
			return Token{}, Secret{}, mayErr
		}
	}

	at := s.Now()

	expiresAt, err := s.expiry(ctx, at, in.ExpiresAt)
	if err != nil {
		return Token{}, Secret{}, err
	}

	n, err := s.newToken(
		tokenSpec{
			at:          at,
			expiresAt:   expiresAt,
			principalID: p.ID,
			name:        in.Name,
			kind:        kind,
			createdBy:   caller.Subject,
		},
	)
	if err != nil {
		return Token{}, Secret{}, err
	}

	if err := s.store.CreateToken(ctx, n.token, n.digest, change(e, at, TokenRef(p.ID, n.token.ID))); err != nil {
		return Token{}, Secret{}, fmt.Errorf("create access token %q: %w", in.Name, err)
	}

	return n.token, n.secret, nil
}

// expiry returns the expiry of a token made at the time at under the
// policy p: want, or the default lifetime of p when want is zero. It returns
// ErrInvalid for an expiry that is not after at, or that is more than the
// maximum lifetime of p after at.
func expiry(p Policy, at, want time.Time) (time.Time, error) {
	if want.IsZero() {
		return at.Add(p.DefaultLifetimeUnder()), nil
	}

	want = want.UTC().Truncate(time.Millisecond)

	switch {
	case !want.After(at):
		return time.Time{}, fmt.Errorf("%w: the expiry %s is not in the future", ErrInvalid, want.Format(time.RFC3339))
	case want.Sub(at) > p.MaxLifetime:
		return time.Time{}, fmt.Errorf("%w: the expiry %s is more than the maximum lifetime %s away",
			ErrInvalid, want.Format(time.RFC3339), p.MaxLifetime)
	default:
		return want, nil
	}
}

// Get returns the token with the ID id.
func (s *Service) Get(ctx context.Context, id string) (Token, error) {
	t, err := s.store.GetToken(ctx, id)
	if err != nil {
		return Token{}, fmt.Errorf("get access token %q: %w", id, err)
	}

	return t, nil
}

// CheckActive answers nil when the access token with the ID id is active now
// and its principal still exists and is not disabled. Otherwise it answers an error that wraps
// identity.ErrUnauthenticated, or the store error. It records no use of the
// token: an update checks its token on each lease request (ADR 53).
func (s *Service) CheckActive(ctx context.Context, id string) error {
	t, err := s.store.GetToken(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: access token not found", identity.ErrUnauthenticated)
	}

	if err != nil {
		return fmt.Errorf("check access token %q: %w", id, err)
	}

	if status := t.StatusAt(s.Now()); status != StatusActive {
		return fmt.Errorf("%w: access token %s", identity.ErrUnauthenticated, status)
	}

	owner, err := s.principals.GetPrincipal(ctx, t.PrincipalID)
	if errors.Is(err, principal.ErrNotFound) {
		return fmt.Errorf("%w: principal of access token is gone", identity.ErrUnauthenticated)
	}

	if err != nil {
		return fmt.Errorf("check principal of access token %q: %w", id, err)
	}

	if !owner.DisabledAt.IsZero() {
		return fmt.Errorf("%w: the principal of the access token is disabled", identity.ErrUnauthenticated)
	}

	return nil
}

// List returns one page of tokens, newest first.
func (s *Service) List(ctx context.Context, req PageRequest) (Page, error) {
	q, limit, err := req.query(s.Now())
	if err != nil {
		return Page{}, err
	}

	rows, total, err := s.store.ListTokens(ctx, q)
	if err != nil {
		return Page{}, fmt.Errorf("list access tokens: %w", err)
	}

	return pageOf(rows, total, limit), nil
}

// Revoke ends the token id at once, with the entry e of caller. For
// ScopeOwn the token must belong to the person of caller. A service token
// needs principal:manage-tokens on its principal. A revoke never raises
// access, so it needs no escalation check.
func (s *Service) Revoke(ctx context.Context, caller identity.Principal, e audit.Entry, scope Scope, id string) error {
	t, err := s.store.GetToken(ctx, id)
	if err != nil {
		return fmt.Errorf("revoke access token %q: %w", id, err)
	}

	if err := s.authorizeRevoke(ctx, caller, scope, t); err != nil {
		return fmt.Errorf("revoke access token %q: %w", id, err)
	}

	if err := s.store.RevokeToken(ctx, id, change(e, s.Now(), TokenRef(t.PrincipalID, id))); err != nil {
		return fmt.Errorf("revoke access token %q: %w", id, err)
	}

	return nil
}

// ListOwn returns one page of the tokens of the person of caller, newest
// first. req.PrincipalID is ignored.
func (s *Service) ListOwn(ctx context.Context, caller identity.Principal, req PageRequest) (Page, error) {
	own, err := s.ownPrincipal(ctx, caller)
	if err != nil {
		return Page{}, fmt.Errorf("list own access tokens: %w", err)
	}

	req.PrincipalID = own.ID

	return s.List(ctx, req)
}

// ListOfPrincipal returns one page of the tokens of the service principal
// principalID, newest first, for caller. caller must hold
// principal:manage-tokens on the principal: the route checks it, and this
// check holds for every caller of the Service. req.PrincipalID is ignored.
// The check runs first, so a caller without the action gets
// identity.ErrDenied for every principal and learns nothing about which
// principals exist. Then an unknown principal answers ErrPrincipalNotFound,
// and a principal that is not a service principal ErrWrongKind:
// principal:manage-tokens gives no right on the tokens of a person.
func (s *Service) ListOfPrincipal(
	ctx context.Context, caller identity.Principal, principalID string, req PageRequest,
) (Page, error) {
	if err := s.requireManageTokens(ctx, caller, principalID, useList); err != nil {
		return Page{}, err
	}

	p, err := s.principals.GetPrincipal(ctx, principalID)
	if errors.Is(err, principal.ErrNotFound) {
		return Page{}, fmt.Errorf("%w: %q", ErrPrincipalNotFound, principalID)
	}

	if err != nil {
		return Page{}, fmt.Errorf("read principal: %w", err)
	}

	if p.Kind != principal.KindService {
		return Page{}, fmt.Errorf("%w: the tokens of a %s go through /api/server/tokens", ErrWrongKind, p.Kind)
	}

	req.PrincipalID = p.ID

	return s.List(ctx, req)
}

// MaxLifetime returns the longest lifetime that a create or a rotation can
// ask for: the maximum lifetime of the installation policy, which applies
// to every principal today.
func (s *Service) MaxLifetime(ctx context.Context) (time.Duration, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return 0, err
	}

	return p.MaxLifetime, nil
}

// RotateInput names a rotation: the token ID, the scope of the caller, and
// the times. A nil OldEndsAt keeps the old token for
// DefaultRotationOverlap, or until its expiry when that is earlier. A zero
// ExpiresAt gives the new token DefaultLifetime.
type RotateInput struct {
	OldEndsAt *time.Time
	ExpiresAt time.Time
	ID        string
	Scope     Scope
}

// Rotation is the result of a rotation: the old token with its new end
// time, and the new token.
type Rotation struct {
	Old Token
	New Token
}

// Rotate issues a new token for the owner of the token in.ID and sets an end
// time for the old token (ADR 53, decision 8), with the entry e of caller.
// The new token keeps the name and the kind. For in.Scope ScopeOwn the token must
// belong to the person of caller. ScopePersonal rotates nothing: it returns
// ErrNotOwnToken, wrapped with identity.ErrDenied. For ScopeService the token
// must be a service token, and caller needs principal:manage-tokens on its
// principal: the new token acts as its principal. It returns the secret of the new token: the only time the
// server shows it.
func (s *Service) Rotate(
	ctx context.Context, caller identity.Principal, e audit.Entry, in RotateInput,
) (Rotation, Secret, error) {
	if err := s.requireFresh(ctx, e.Subject); err != nil {
		return Rotation{}, Secret{}, err
	}

	id := in.ID

	old, err := s.store.GetToken(ctx, id)
	if err != nil {
		return Rotation{}, Secret{}, fmt.Errorf("rotate access token %q: %w", id, err)
	}

	if authErr := s.authorizeRotate(ctx, caller, in.Scope, old); authErr != nil {
		return Rotation{}, Secret{}, fmt.Errorf("rotate access token %q: %w", id, authErr)
	}

	if ownerErr := s.checkRotateOwner(ctx, old); ownerErr != nil {
		return Rotation{}, Secret{}, fmt.Errorf("rotate access token %q: %w", id, ownerErr)
	}

	at := s.Now()
	if old.StatusAt(at) != StatusActive || old.ReplacedBy != "" {
		return Rotation{}, Secret{}, fmt.Errorf("rotate access token %q: %w", id, ErrEnded)
	}

	oldEndsAt, err := rotationEnd(at, old.ExpiresAt, in.OldEndsAt)
	if err != nil {
		return Rotation{}, Secret{}, err
	}

	expiresAt, err := s.expiry(ctx, at, in.ExpiresAt)
	if err != nil {
		return Rotation{}, Secret{}, err
	}

	n, err := s.newToken(tokenSpec{
		at:          at,
		expiresAt:   expiresAt,
		principalID: old.PrincipalID,
		name:        old.Name,
		kind:        old.Kind,
		createdBy:   caller.Subject,
	})
	if err != nil {
		return Rotation{}, Secret{}, err
	}

	r := Replacement{OldEndsAt: oldEndsAt, OldID: id, Next: n.token, Digest: n.digest}
	if err := s.store.RotateToken(ctx, r, change(e, at, TokenRef(old.PrincipalID, id))); err != nil {
		return Rotation{}, Secret{}, fmt.Errorf("rotate access token %q: %w", id, err)
	}

	old.ExpiresAt = oldEndsAt
	old.ReplacedBy = n.token.ID

	return Rotation{Old: old, New: n.token}, n.secret, nil
}

// rotationEnd returns the end time of the old token of a rotation at the
// time at: want, or DefaultRotationOverlap after at. The old expiry bounds
// the default. A wanted time before at, or after the old expiry, answers
// ErrInvalid.
func rotationEnd(at, oldExpiry time.Time, want *time.Time) (time.Time, error) {
	if want == nil {
		return minTime(at.Add(DefaultRotationOverlap), oldExpiry), nil
	}

	end := want.UTC().Truncate(time.Millisecond)

	switch {
	case end.Before(at):
		return time.Time{}, fmt.Errorf("%w: the end time %s of the old token is in the past",
			ErrInvalid, end.Format(time.RFC3339))
	case end.After(oldExpiry):
		return time.Time{}, fmt.Errorf("%w: the end time %s of the old token is after its expiry %s",
			ErrInvalid, end.Format(time.RFC3339), oldExpiry.Format(time.RFC3339))
	default:
		return end, nil
	}
}

// minTime returns the earlier of a and b.
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}

	return b
}

// Policy returns the token life cycle policy of the installation.
func (s *Service) Policy(ctx context.Context) (Policy, error) {
	p, err := s.store.GetPolicy(ctx)
	if err != nil {
		return Policy{}, fmt.Errorf("read access token policy: %w", err)
	}

	return p, nil
}

// PolicyChange names the fields of the policy that a change sets. A nil
// field keeps its value. DisableBreakGlass is deprecated: the server
// refuses true, and always keeps false.
type PolicyChange struct {
	MaxLifetime       *time.Duration
	Inactivity        *time.Duration
	DisableTokens     *bool
	DisableAccounts   *bool
	DisableBreakGlass *bool
	ExchangeLifetime  *time.Duration
}

// SetPolicy applies c to the policy, with the entry e, and returns the
// stored policy (ADR 53, decision 6). The store reads and writes the policy
// in one transaction, so two changes cannot lose one of them. A lower
// maximum lifetime applies to the tokens made after the change: it does not
// end a token that exists.
func (s *Service) SetPolicy(ctx context.Context, e audit.Entry, c PolicyChange) (Policy, error) {
	p, err := s.store.UpdatePolicy(ctx, func(old Policy) (Policy, audit.Entry, error) {
		p := c.applyTo(old)
		p.UpdatedAt = s.Now()

		if err := p.Validate(); err != nil {
			return Policy{}, audit.Entry{}, err
		}

		return p, policyEntry(e, p.UpdatedAt, old, p), nil
	})

	if errors.Is(err, ErrInvalid) {
		return Policy{}, err
	}

	if err != nil {
		return Policy{}, fmt.Errorf("set access token policy: %w", err)
	}

	return p, nil
}

// applyTo returns p with the fields of c that are set.
func (c PolicyChange) applyTo(p Policy) Policy {
	if c.MaxLifetime != nil {
		p.MaxLifetime = *c.MaxLifetime
	}

	if c.Inactivity != nil {
		p.Inactivity = *c.Inactivity
	}

	if c.DisableTokens != nil {
		p.DisableTokens = *c.DisableTokens
	}

	if c.DisableAccounts != nil {
		p.DisableAccounts = *c.DisableAccounts
	}

	if c.DisableBreakGlass != nil {
		p.DisableBreakGlass = *c.DisableBreakGlass
	}

	if c.ExchangeLifetime != nil {
		p.ExchangeLifetime = *c.ExchangeLifetime
	}

	return p
}

// SweepInactive runs the inactivity disable once (ADR 53, decision 4), with
// the entry e for each record that it disables. The entry of a record with
// no reason of the caller names the rule and the period of the policy. It
// reads the policy: with
// an inactivity period of zero, or with tokens and accounts both off, it
// changes nothing. The store takes the time of the run from the database
// clock, and the entries carry that time. The run keeps the temporary
// administrators that still work, unless the stored policy turns their
// disable on: only their fixed expiry ends them. A break-glass account is
// always exempt, with no setting. The bootstrap admin tokens have no stored
// record, so a sweep never disables them here.
func (s *Service) SweepInactive(ctx context.Context, e audit.Entry) (SweepResult, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return SweepResult{}, err
	}

	if p.Inactivity == 0 || (!p.DisableTokens && !p.DisableAccounts) {
		return SweepResult{At: time.Time{}, Tokens: nil, Principals: nil}, nil
	}

	sweep := Sweep{Inactivity: p.Inactivity, Tokens: p.DisableTokens, Accounts: p.DisableAccounts}

	res, err := s.store.SweepInactive(ctx, sweep, func(at time.Time, object string) audit.Entry {
		entry := change(e, at, object)
		if entry.Reason == "" {
			entry.Reason = sweepReason(object, p.Inactivity)
		}

		return entry
	})
	if err != nil {
		return SweepResult{}, fmt.Errorf("disable inactive access tokens and accounts: %w", err)
	}

	return res, nil
}

// AuthenticateToken implements identity.Authenticator. It resolves raw to
// the principal of its token. A value that does not have the form of a
// secret, an unknown secret, a token that expired, was revoked, was
// disabled or is inactive under the policy, and a token whose principal was
// deleted or disabled, answer identity.ErrUnauthenticated. The
// principal is never an administrator: grants decide what it can do
// (ADR 52). A failed store read answers its error, so the caller fails
// closed.
func (s *Service) AuthenticateToken(ctx context.Context, raw string) (identity.Principal, error) {
	if !LooksLikeSecret(raw) {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	digest := digestOfRaw(raw)

	m, err := s.store.GetTokenByDigest(ctx, digest)
	if errors.Is(err, ErrNotFound) {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	if err != nil {
		return identity.Principal{}, fmt.Errorf("authenticate access token: %w", err)
	}

	if !m.Digest.Equal(digest) {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	now := s.Now()
	if status := m.Token.StatusAt(now); status != StatusActive {
		return identity.Principal{}, fmt.Errorf("%w: access token %s", identity.ErrUnauthenticated, status)
	}

	if !m.Owner.DisabledAt.IsZero() {
		return identity.Principal{}, fmt.Errorf("%w: the principal of the access token is disabled",
			identity.ErrUnauthenticated)
	}

	// Defense in depth: no token is made for this subject, and the
	// authenticator chain of the server refuses it too. This check stays so
	// that a token row of another version never acts as the namespace
	// service principal.
	if identity.IsNamespaceServiceSubject(m.Owner.Subject) {
		return identity.Principal{}, fmt.Errorf("%w: an access token cannot act as the namespace service principal",
			identity.ErrUnauthenticated)
	}

	if err := s.checkPolicyActive(ctx, m, now); err != nil {
		return identity.Principal{}, err
	}

	s.touch(ctx, m.Token, now)

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        m.Owner.Subject,
		DisplayName:    m.Owner.DisplayName,
		Admin:          false,
		TokenID:        m.Token.ID,
		TokenExpiresAt: m.Token.ExpiresAt,
		Kind:           identity.Kind(m.Owner.Kind),
		Credential:     credentialOf(m.Token.Kind),
		JobID:          "",
	}, nil
}

// credentialOf returns the credential of a token of the kind k: a token of a
// trust rule is a trust token, so no check reads it as a token of a person.
func credentialOf(k Kind) identity.Credential {
	if k == KindTrust {
		return identity.CredentialTrustToken
	}

	return identity.CredentialAccessToken
}

// AccountInactive reports whether the user account o is past the
// inactivity period of the policy at the time of the service, as the token
// lookup decides for the owner of a token: the account disable is on, the
// period is not zero, o is a person that is not a break-glass account, and
// o.ActiveAt is at or before now minus the period. Another sign-in, such as
// a console session, applies it, so that its use does not keep an account
// alive that the sweep would disable. A failed read of the policy answers
// its error, so the caller fails closed.
func (s *Service) AccountInactive(ctx context.Context, o Owner) (bool, error) {
	if o.BreakGlass {
		return false, nil
	}

	now := s.Now()

	p, err := s.Policy(ctx)
	if err != nil {
		return false, fmt.Errorf("account inactivity: %w", err)
	}

	return p.AccountInactiveAt(o, now), nil
}

// checkPolicyActive refuses the token of m when the policy of m disables
// the token, or the user account of its owner, as inactive at now, before
// the sweep writes the disable: the sweep runs once an hour, and a use would
// else move the last use forward and keep the token, or the account, alive.
// A break-glass account is always exempt. A working temporary administrator
// is exempt too: only its fixed expiry ends it. The policy comes from the
// same lookup as the token, so a change applies to the next lookup on every
// instance. A failed record read answers its error, so the caller fails
// closed.
func (s *Service) checkPolicyActive(ctx context.Context, m Match, now time.Time) error {
	t, o, p := m.Token, m.Owner, m.Policy

	if o.BreakGlass {
		return nil
	}

	if p.inactiveAt(t, now) {
		if allow, err := s.tempAdminExempt(ctx, o.Subject, now); err != nil {
			return err
		} else if !allow {
			return fmt.Errorf("%w: access token inactive for %s", identity.ErrUnauthenticated, p.Inactivity)
		}
	}

	if p.AccountInactiveAt(o, now) {
		if allow, err := s.tempAdminExempt(ctx, o.Subject, now); err != nil {
			return err
		} else if !allow {
			return fmt.Errorf("%w: the user account of the access token is inactive for %s",
				identity.ErrUnauthenticated, p.Inactivity)
		}
	}

	return nil
}

// tempAdminExempt reports whether the inactivity disable exempts the person
// subject at now: subject is a working temporary administrator. Without a
// TempAdmins reader it answers false. A failed record read answers its
// error, so the caller fails closed.
func (s *Service) tempAdminExempt(ctx context.Context, subject string, now time.Time) (bool, error) {
	if s.tempAdmins == nil {
		return false, nil
	}

	rec, err := s.tempAdmins.GetTempAdminBySubject(ctx, subject)
	if errors.Is(err, tempadmin.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("authenticate access token: %w", err)
	}

	return rec.ActiveAt(now), nil
}

// touch records the use of t at now, at most once for each touchEvery. A
// failed write does not refuse the request: the server logs it with the
// token ID only.
func (s *Service) touch(ctx context.Context, t Token, now time.Time) {
	if !t.LastUsedAt.IsZero() && now.Sub(t.LastUsedAt) < touchEvery {
		return
	}

	if err := s.store.TouchToken(ctx, t.ID, now, touchEvery); err != nil {
		s.logger.WarnContext(ctx, "record access token use failed", slog.String("token_id", t.ID))
	}
}

// expiry reads the policy and returns the expiry of a token made at the
// time at, as the package function expiry does.
func (s *Service) expiry(ctx context.Context, at, want time.Time) (time.Time, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return time.Time{}, err
	}

	return expiry(p, at, want)
}

// requireFresh runs the MFA freshness check when one is wired.
func (s *Service) requireFresh(ctx context.Context, subject string) error {
	if s.freshness == nil {
		return nil
	}

	if err := s.freshness.RequireFresh(ctx, subject); err != nil {
		return fmt.Errorf("mfa freshness: %w", err)
	}

	return nil
}

// change completes the entry of the caller for one change of the record
// object at the time at.
func change(e audit.Entry, at time.Time, object string) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = object

	return e
}

// policyEntry returns e as the audit entry of a change of the policy from
// old to updated at the time at. It keeps the changes of the caller, for
// example the session ID of a console session, and adds the changed
// settings.
func policyEntry(e audit.Entry, at time.Time, old, updated Policy) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = PolicyRef
	e.Changes = slices.Clone(e.Changes)

	for _, c := range []struct {
		key        string
		old, value time.Duration
	}{
		{key: "maxLifetimeSeconds", old: old.MaxLifetime, value: updated.MaxLifetime},
		{key: "inactivitySeconds", old: old.Inactivity, value: updated.Inactivity},
		{key: "exchangeLifetimeSeconds", old: old.ExchangeLifetime, value: updated.ExchangeLifetime},
	} {
		if c.old == c.value {
			continue
		}

		before := seconds(c.old)
		e.Changes = append(e.Changes, audit.Change{
			Key: c.key, Old: &before, New: seconds(c.value), Layer: "", TakesEffect: "",
		})
	}

	for _, c := range []struct {
		key        string
		old, value bool
	}{
		{key: "disableTokens", old: old.DisableTokens, value: updated.DisableTokens},
		{key: "disableAccounts", old: old.DisableAccounts, value: updated.DisableAccounts},
		{key: "disableBreakGlass", old: old.DisableBreakGlass, value: updated.DisableBreakGlass},
	} {
		if c.old == c.value {
			continue
		}

		before := strconv.FormatBool(c.old)
		e.Changes = append(e.Changes, audit.Change{
			Key: c.key, Old: &before, New: strconv.FormatBool(c.value), Layer: "", TakesEffect: "",
		})
	}

	return e
}

// seconds returns d as a decimal count of whole seconds.
func seconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}

// sweepReason returns the reason of the audit entry of the record object that
// the inactivity sweep disables under the period. A token object holds the
// principal and the token, joined by '#' (TokenRef); a principal object is a
// person account.
func sweepReason(object string, period time.Duration) string {
	kind := "account"
	if strings.Contains(object, "#") {
		kind = "access token"
	}

	return fmt.Sprintf("%s not used for at least %s (token policy: inactivity)", kind, periodText(period))
}

// periodText returns d in days or in hours when it is a whole number of them,
// for example "90 days" or "2 hours". Another period keeps the form of
// time.Duration.
func periodText(d time.Duration) string {
	const day = 24 * time.Hour

	switch {
	case d%day == 0:
		return countText(int64(d/day), "day")
	case d%time.Hour == 0:
		return countText(int64(d/time.Hour), "hour")
	default:
		return d.String()
	}
}

// countText returns n with the unit in the singular for 1 and in the plural
// for any other count.
func countText(n int64, unit string) string {
	if n == 1 {
		return "1 " + unit
	}

	return strconv.FormatInt(n, 10) + " " + unit + "s"
}

// issued is a new token with its secret and the digest of the secret.
type issued struct {
	token  Token
	secret Secret
	digest Digest
}

// tokenSpec names the fields of a new token record.
type tokenSpec struct {
	at          time.Time
	expiresAt   time.Time
	principalID string
	name        string
	kind        Kind
	createdBy   string
}

// newToken returns a new token record with its secret and digest.
func (s *Service) newToken(spec tokenSpec) (issued, error) {
	secret, err := newSecret()
	if err != nil {
		return issued{}, err
	}

	t := Token{
		CreatedAt:   spec.at,
		ExpiresAt:   spec.expiresAt,
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          s.newID(idPrefix),
		PrincipalID: spec.principalID,
		Name:        spec.name,
		Kind:        spec.kind,
		ReplacedBy:  "",
		CreatedBy:   spec.createdBy,
	}

	return issued{token: t, secret: secret, digest: DigestOf(secret)}, nil
}

// checkRotateOwner returns ErrPrincipalNotFound when the principal of t
// was deleted, and the error of refuseNamespaceServiceOwner when it is the
// namespace service principal.
func (s *Service) checkRotateOwner(ctx context.Context, t Token) error {
	p, err := s.principals.GetPrincipal(ctx, t.PrincipalID)
	if errors.Is(err, principal.ErrNotFound) {
		return fmt.Errorf("%w: %q", ErrPrincipalNotFound, t.PrincipalID)
	}

	if err != nil {
		return fmt.Errorf("read principal: %w", err)
	}

	return refuseNamespaceServiceOwner(p.Subject)
}

// refuseNamespaceServiceOwner returns ErrNoTokenForKind, wrapped with
// principal.ErrReservedSubject, for a principal record with the subject of
// the namespace service principal: it has no token.
func refuseNamespaceServiceOwner(subject string) error {
	if identity.IsNamespaceServiceSubject(subject) {
		return fmt.Errorf("%w: the namespace service principal gets no access token: %w",
			ErrNoTokenForKind, principal.ErrReservedSubject)
	}

	return nil
}
