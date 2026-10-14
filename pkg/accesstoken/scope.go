package accesstoken

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Scope names who acts on a token, and so which action allows the change
// (ADR 53, decision 3). The routes of each scope declare their own action.
type Scope int

// Scopes of a token change.
const (
	// ScopeOwn is a person who acts on its own personal tokens. Its action,
	// token:manage-own, needs no grant: every authenticated person holds it.
	ScopeOwn Scope = iota + 1
	// ScopePersonal is an administrator who acts on the personal tokens of
	// other persons, with server:manage-tokens.
	ScopePersonal
	// ScopeService is an administrator who acts on service tokens, with
	// server:manage-service-tokens.
	ScopeService
)

// Errors of the scope checks.
var (
	// ErrWrongKind reports a token or a principal whose kind does not fit
	// the scope, for example a service token through the personal routes.
	ErrWrongKind = errors.New("access token kind does not fit the route")
	// ErrSignInRequired reports a create or a rotation of a token, in any
	// scope, by a caller that authenticated with an access token. A token
	// comes from a sign-in (ADR 53, decision 10): a token that could make
	// new tokens would outlive its maximum lifetime and a revoke.
	ErrSignInRequired = errors.New("sign in to create or rotate an access token: an access token cannot")
	// ErrNoOwnPrincipal reports a caller that has no person record, so it
	// cannot own personal tokens: a bootstrap admin token, a service or a
	// workload.
	ErrNoOwnPrincipal = errors.New("the caller is not a person with a principal record")
)

// GrantCheck is the escalation check of a token for another principal. A
// token acts as its principal, so to create or rotate one is to grant, by
// proxy, everything that the principal holds.
type GrantCheck interface {
	// RequireHeld returns nil when caller holds, on each object, every
	// action that the grants of the principal principalID give it, through
	// its groups too. Otherwise it returns an error that wraps
	// identity.ErrDenied.
	RequireHeld(ctx context.Context, caller identity.Principal, principalID string) error
}

// requireSignIn returns ErrSignInRequired when caller authenticated with a
// stored access token. Every create and rotation runs it, in each scope.
// Sign-in does not exist yet (ADR 55), so until it does only a bootstrap
// admin token (ADR 6), which is not a stored token, can make or rotate a
// token; and it has no person record, so it cannot own a personal token.
func requireSignIn(caller identity.Principal) error {
	if caller.TokenID != "" {
		return ErrSignInRequired
	}

	return nil
}

// ownPrincipal returns the person record of caller, found by its subject,
// or ErrNoOwnPrincipal.
func (s *Service) ownPrincipal(ctx context.Context, caller identity.Principal) (principal.Principal, error) {
	p, err := s.principals.GetPrincipalBySubject(ctx, caller.Subject)
	if errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, fmt.Errorf(
			"%w: create a person principal for this subject first",
			ErrNoOwnPrincipal,
		)
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read the principal of the caller: %w", err)
	}

	if p.Kind != principal.KindPerson {
		return principal.Principal{}, fmt.Errorf("%w: the principal is a %s", ErrNoOwnPrincipal, p.Kind)
	}

	return p, nil
}

// targetPrincipal returns the principal that a create of scope acts for:
// the person of caller for ScopeOwn, else the principal id.
func (s *Service) targetPrincipal(
	ctx context.Context, caller identity.Principal, scope Scope, id string,
) (principal.Principal, error) {
	if err := requireSignIn(caller); err != nil {
		return principal.Principal{}, err
	}

	if scope == ScopeOwn {
		return s.ownPrincipal(ctx, caller)
	}

	p, err := s.principals.GetPrincipal(ctx, id)
	if errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, fmt.Errorf("%w: %q", ErrPrincipalNotFound, id)
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read principal: %w", err)
	}

	return p, nil
}

// checkKind returns ErrWrongKind when a token of kind does not fit scope.
func checkKind(scope Scope, kind Kind) error {
	switch {
	case scope == ScopeService && kind != KindService:
		return fmt.Errorf("%w: a %s token goes through /api/server/tokens", ErrWrongKind, kind)
	case scope != ScopeService && kind != KindPersonal:
		return fmt.Errorf("%w: a %s token goes through /api/server/service-tokens", ErrWrongKind, kind)
	default:
		return nil
	}
}

// checkOwner returns ErrNotFound when the token t does not belong to the
// person of caller, so an own route tells nothing about another token.
func (s *Service) checkOwner(ctx context.Context, caller identity.Principal, t Token) error {
	own, err := s.ownPrincipal(ctx, caller)
	if err != nil {
		return err
	}

	if t.PrincipalID != own.ID {
		return ErrNotFound
	}

	return nil
}

// authorizeRevoke checks that caller can revoke the token t in scope. For
// ScopeOwn, t must belong to the person of caller. An administrator scope
// needs nothing more: a revoke never raises access.
func (s *Service) authorizeRevoke(ctx context.Context, caller identity.Principal, scope Scope, t Token) error {
	if scope == ScopeOwn {
		return s.checkOwner(ctx, caller, t)
	}

	return nil
}

// authorizeRotate checks that caller can rotate the token t in scope. The
// caller must not use an access token. For ScopeOwn, t must belong to the
// person of caller. For the other scopes, t must have the kind of the
// scope, and the escalation check runs: the new token acts as its
// principal.
func (s *Service) authorizeRotate(ctx context.Context, caller identity.Principal, scope Scope, t Token) error {
	if err := requireSignIn(caller); err != nil {
		return err
	}

	if scope == ScopeOwn {
		return s.checkOwner(ctx, caller, t)
	}

	if err := checkKind(scope, t.Kind); err != nil {
		return err
	}

	return s.requireHeld(ctx, caller, t.PrincipalID)
}

// requireHeld runs the escalation check. With no GrantCheck wired (a store
// that keeps no grants), only an administrator can act for another
// principal: an administrator holds every action, so it raises nothing.
// This fails closed.
func (s *Service) requireHeld(ctx context.Context, caller identity.Principal, principalID string) error {
	if s.grants != nil {
		if err := s.grants.RequireHeld(ctx, caller, principalID); err != nil {
			return fmt.Errorf("token for principal %q: %w", principalID, err)
		}

		return nil
	}

	if !caller.Admin {
		return fmt.Errorf("%w: %s may not make a token for principal %q: "+
			"only an administrator can when the server keeps no grants", identity.ErrDenied,
			caller.Subject, principalID)
	}

	return nil
}
