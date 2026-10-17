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
	// ScopePersonal is an administrator who revokes the personal tokens of
	// other persons, with server:manage-tokens. No principal makes or
	// rotates a personal token of another person.
	ScopePersonal
	// ScopeService is a principal who acts on service tokens, with
	// principal:manage-tokens on the service principal of each token.
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
	// ErrNotOwnToken reports a create or a rotation of a personal token of
	// another person. Each person makes and rotates only its own personal
	// tokens, after a sign-in. It comes wrapped with identity.ErrDenied.
	ErrNotOwnToken = errors.New(
		"a person makes and rotates only its own personal tokens: the person signs in and uses /api/server/caller/tokens",
	)
)

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

	if scope == ScopePersonal {
		return principal.Principal{}, notOwnToken()
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
// ScopeOwn, t must belong to the person of caller. ScopeService takes a
// service token only (ErrWrongKind): principal:manage-tokens gives no right
// on a personal token. A service token needs principal:manage-tokens on its
// principal, in ScopeService and in ScopePersonal. A personal token of
// another person needs nothing more than server:manage-tokens, the action
// of its route: a revoke never raises access.
func (s *Service) authorizeRevoke(ctx context.Context, caller identity.Principal, scope Scope, t Token) error {
	if scope == ScopeOwn {
		return s.checkOwner(ctx, caller, t)
	}

	if scope == ScopeService && t.Kind != KindService {
		return fmt.Errorf("%w: a %s token goes through /api/server/tokens", ErrWrongKind, t.Kind)
	}

	if t.Kind == KindService {
		return s.requireManageTokens(ctx, caller, t.PrincipalID, useMake)
	}

	return nil
}

// authorizeRotate checks that caller can rotate the token t in scope. The
// caller must not use an access token. For ScopeOwn, t must belong to the
// person of caller. ScopePersonal rotates nothing: no principal rotates a
// personal token of another person. For ScopeService, t must be a service
// token, and the check of mayActFor runs: the new token acts as its
// principal.
func (s *Service) authorizeRotate(ctx context.Context, caller identity.Principal, scope Scope, t Token) error {
	if err := requireSignIn(caller); err != nil {
		return err
	}

	if scope == ScopeOwn {
		return s.checkOwner(ctx, caller, t)
	}

	if scope == ScopePersonal {
		return notOwnToken()
	}

	if err := checkKind(scope, t.Kind); err != nil {
		return err
	}

	return s.mayActFor(ctx, caller, t.Kind, t.PrincipalID)
}

// notOwnToken returns the refusal of a create or a rotation of a personal
// token of another person.
func notOwnToken() error {
	return fmt.Errorf("%w: %w", identity.ErrDenied, ErrNotOwnToken)
}

// mayActFor checks that caller can make a token of kind for the principal
// principalID. Only a service token reaches it: it needs
// principal:manage-tokens on the principal, and its holder can act as the
// principal, with every grant that the principal has now or gets later. A
// personal token of another person never reaches it: the scope refuses it
// first.
func (s *Service) mayActFor(ctx context.Context, caller identity.Principal, kind Kind, principalID string) error {
	if kind != KindService {
		return notOwnToken()
	}

	return s.requireManageTokens(ctx, caller, principalID, useMake)
}

// Uses of principal:manage-tokens, as the text of a refusal names them.
const (
	useMake = "make a token for"
	useList = "list the tokens of"
)

// requireManageTokens checks that caller holds principal:manage-tokens on
// the principal principalID, for use: useMake or useList. The route of the
// change checks it too: this check holds for every caller of the Service.
// With no Authorizer wired, only an administrator passes it. This fails
// closed.
func (s *Service) requireManageTokens(
	ctx context.Context, caller identity.Principal, principalID, use string,
) error {
	if s.authz == nil {
		if caller.Admin {
			return nil
		}

		return fmt.Errorf("%w: %s may not %s principal %q: "+
			"only an administrator can when the server keeps no grants", identity.ErrDenied,
			caller.Subject, use, principalID)
	}

	obj, err := identity.ParsePrincipalObject(principalID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPrincipalNotFound, err)
	}

	if err := s.authz.Authorize(ctx, caller, identity.ActionPrincipalManageTokens, obj); err != nil {
		return fmt.Errorf("%s principal %q: %w", use, principalID, err)
	}

	return nil
}
