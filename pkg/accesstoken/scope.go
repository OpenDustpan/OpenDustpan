package accesstoken

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/access"
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
	// ErrReachExceeded reports a caller that holds principal:manage-tokens
	// on the installation and makes or rotates a token of a service
	// principal that holds an action on an object outside the reach of the
	// caller. A holder on the principal itself is not compared. A zero or
	// incomplete reach config, and a read of the reach that fails, refuse
	// the request too. It comes wrapped with identity.ErrDenied.
	ErrReachExceeded = errors.New("the reach of the principal is not inside the reach of the caller")
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
		return fmt.Errorf("%w: its owner makes and rotates a %s token through /api/server/caller/tokens",
			ErrWrongKind, kind)
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
// service token or a token of a trust rule only (ErrWrongKind):
// principal:manage-tokens gives no right on a personal token. Both kinds
// need principal:manage-tokens on their principal, in ScopeService and in
// ScopePersonal. A personal token of
// another person needs nothing more than server:manage-tokens, the action
// of its route: a revoke never raises access.
func (s *Service) authorizeRevoke(ctx context.Context, caller identity.Principal, scope Scope, t Token) error {
	if scope == ScopeOwn {
		return s.checkOwner(ctx, caller, t)
	}

	if scope == ScopeService && t.Kind != KindService && t.Kind != KindTrust {
		return fmt.Errorf("%w: revoke a %s token through /api/server/tokens/{id} or, as its owner, through "+
			"/api/server/caller/tokens/{id}", ErrWrongKind, t.Kind)
	}

	if t.Kind == KindService || t.Kind == KindTrust {
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
// holder of the action on the installation also needs a reach that covers
// the reach of the principal. A personal token of another person never
// reaches it: the scope refuses it first.
func (s *Service) mayActFor(ctx context.Context, caller identity.Principal, kind Kind, principalID string) error {
	if kind != KindService {
		return notOwnToken()
	}

	if err := s.requireManageTokens(ctx, caller, principalID, useMake); err != nil {
		return err
	}

	return s.requireReach(ctx, caller, principalID)
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

// holds names where a caller holds principal:manage-tokens for one service
// principal: on the installation, and on the principal itself. A hold
// through a group counts for each. A hold through the namespace edge is
// neither: that edge decides the change alone.
type holds struct {
	installation bool
	principal    bool
}

// requireReach checks the reach rule of a token change for a service
// principal: a caller that holds principal:manage-tokens on the
// installation can make or rotate the token only when the reach of the
// principal fits inside the reach of the caller. A holder on the principal
// itself is not compared. An administrator holds every action and passes.
// A zero or incomplete reach config, and a read that fails, refuse the
// request: the check never skips.
func (s *Service) requireReach(ctx context.Context, caller identity.Principal, principalID string) error {
	if caller.Admin {
		return nil
	}

	held, err := s.tokenHolds(ctx, caller, principalID)
	if err != nil {
		return refuseReach(principalID, err)
	}

	if !held.installation || held.principal {
		return nil
	}

	holder, missing, err := s.missingActions(ctx, caller, principalID)
	if err != nil {
		return refuseReach(principalID, err)
	}

	if len(missing) > 0 {
		return refuseMissing(ctx, holder, principalID, missing)
	}

	return nil
}

// CheckReach answers the reach check of a token change for the service
// principal principalID: it refuses a caller that holds
// principal:manage-tokens on the installation only and does not hold each
// action of the reach of the principal, with the same error as the create
// and the rotate paths of a token. A holder of the action on the principal
// itself is not compared, as for a token.
//
// The check of the hold on the action stays with the caller: this method
// compares the reaches alone.
func (s *Service) CheckReach(ctx context.Context, caller identity.Principal, principalID string) error {
	return s.requireReach(ctx, caller, principalID)
}

// missingActions returns the reach of the caller and the actions of the
// reach of the principal principalID that it does not hold.
func (s *Service) missingActions(
	ctx context.Context, caller identity.Principal, principalID string,
) (access.Reach, []access.HeldAction, error) {
	holder, err := s.reachOf(ctx, caller)
	if err != nil {
		return access.Reach{}, nil, err
	}

	target, err := access.ReachOf(ctx, s.reach, principalID)
	if err != nil {
		return holder, nil, fmt.Errorf("read the reach of the principal: %w", err)
	}

	missing, err := holder.Missing(ctx, target)
	if err != nil {
		return holder, nil, fmt.Errorf("compare the reaches: %w", err)
	}

	return holder, missing, nil
}

// reachOf returns the reach of the caller: the reach of the principal
// record that exists with its subject. A caller with no record has no
// reach, so the request is refused.
func (s *Service) reachOf(ctx context.Context, caller identity.Principal) (access.Reach, error) {
	p, err := s.principals.GetPrincipalBySubject(ctx, caller.Subject)
	if err != nil {
		return access.Reach{}, fmt.Errorf("read the principal of the caller %q: %w", caller.Subject, err)
	}

	return access.ReachOf(ctx, s.reach, p.ID)
}

// tokenHolds reports where the caller holds principal:manage-tokens for
// the service principal principalID, directly or through a group. It reads
// the grants of the caller and its groups, and the role of each grant. A
// port that the reach config does not hold, and a read that fails, answer
// an error, so the caller refuses the request.
func (s *Service) tokenHolds(ctx context.Context, caller identity.Principal, principalID string) (holds, error) {
	var out holds

	if err := s.reachReady(); err != nil {
		return out, err
	}

	grants, err := s.grantsOf(ctx, caller.Subject)
	if err != nil {
		return out, err
	}

	roles, err := rolesOf(ctx, grants, s.reach.Roles)
	if err != nil {
		return out, err
	}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if !ok || !role.Has(identity.ActionPrincipalManageTokens) {
			continue
		}

		if grants[i].Object.IsServer() {
			out.installation = true
		}

		if grants[i].Object.IsPrincipal() && grants[i].Object.PrincipalID() == principalID {
			out.principal = true
		}
	}

	return out, nil
}

// reachReady returns an error when the reach config does not hold the ports
// that the reach check reads. The caller then refuses the change: the check
// never skips.
func (s *Service) reachReady() error {
	if s.reach.Grants == nil || s.reach.Roles == nil || s.reach.Callers == nil || s.reach.Principals == nil {
		return fmt.Errorf("%w: the reach needs the grants, the roles, the callers and the principals",
			access.ErrMissingDependency)
	}

	return nil
}

// grantsOf returns the grants of the caller and of its groups.
func (s *Service) grantsOf(ctx context.Context, subject string) ([]access.Grant, error) {
	subjects, err := s.reach.Callers.SubjectsOf(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("read the subjects of the caller: %w", err)
	}

	var out []access.Grant

	for _, of := range subjects {
		grants, err := s.reach.Grants.GrantsOf(ctx, of)
		if err != nil {
			return nil, fmt.Errorf("read the grants of %s: %w", of.String(), err)
		}

		out = append(out, grants...)
	}

	return out, nil
}

// rolesOf returns the roles that grants name, by ID: the built-in roles
// from code and the custom roles in one query of store. A role that no
// longer exists is left out, so its grants give nothing.
func rolesOf(ctx context.Context, grants []access.Grant, store access.RoleStore) (map[string]access.Role, error) {
	out := make(map[string]access.Role, len(grants))

	var missing []string

	for i := range grants {
		id := grants[i].Role
		if _, done := out[id]; done {
			continue
		}

		if role, ok := access.BuiltInRole(id); ok {
			out[id] = role

			continue
		}

		if !slices.Contains(missing, id) {
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return out, nil
	}

	custom, err := store.GetRoles(ctx, missing)
	if err != nil {
		return nil, fmt.Errorf("read roles: %w", err)
	}

	for i := range custom {
		out[custom[i].ID] = custom[i]
	}

	return out, nil
}

// reachCheckError reports a change that the reach check refused because the
// server cannot compare the reaches. It wraps identity.ErrDenied and
// ErrReachExceeded. Its text names the principal and not the cause, which
// may hold driver detail.
type reachCheckError struct {
	principalID string
	cause       error
}

// Error implements error.
func (r *reachCheckError) Error() string {
	return fmt.Sprintf("cannot check the reach of principal %q", r.principalID)
}

// Unwrap lets errors.Is match the two sentinels and the cause.
func (r *reachCheckError) Unwrap() []error {
	return []error{identity.ErrDenied, ErrReachExceeded, r.cause}
}

// reachExceededError reports a caller whose reach does not hold every
// action of the principal principalID. It wraps identity.ErrDenied and
// ErrReachExceeded. Its text names the principal and the actions that the
// caller does not hold, sorted, with the object of each. It names the
// action and the object only when the reach of the caller holds the read
// action of that object, so the text never names an object that the caller
// cannot read. All the other actions go into one phrase at the end, so the
// text does not tell how many objects the caller cannot read.
type reachExceededError struct {
	principalID string
	missing     []access.HeldAction
	// readable tells, for each entry of missing, whether the reach of the
	// caller holds the read action of its object.
	readable []bool
}

// Error implements error.
func (r *reachExceededError) Error() string {
	parts := make([]string, 0, len(r.missing))
	hidden := false

	for i := range r.missing {
		if !r.readable[i] {
			hidden = true

			continue
		}

		parts = append(parts, string(r.missing[i].Action)+" on "+objectText(r.missing[i].Object))
	}

	switch {
	case !hidden:
		return fmt.Sprintf("principal %q holds actions that you do not hold: %s",
			r.principalID, strings.Join(parts, ", "))
	case len(parts) == 0:
		return fmt.Sprintf("principal %q holds actions that you do not hold on objects that you cannot read",
			r.principalID)
	default:
		return fmt.Sprintf(
			"principal %q holds actions that you do not hold: %s, and actions on other objects that you cannot read",
			r.principalID, strings.Join(parts, ", "),
		)
	}
}

// Unwrap lets errors.Is match the two sentinels.
func (r *reachExceededError) Unwrap() []error { return []error{identity.ErrDenied, ErrReachExceeded} }

// MissingActions returns the actions of the reach of the principal that the
// caller does not hold, with the object of each, sorted. The text of the
// refusal hides an object that the caller cannot read; the audit entry of
// the refusal names it.
func (r *reachExceededError) MissingActions() []access.HeldAction {
	return slices.Clone(r.missing)
}

// refuseReach returns the fail-closed refusal of the reach check: the
// server cannot compare the reaches, so it refuses the request.
func refuseReach(principalID string, err error) error {
	return &reachCheckError{principalID: principalID, cause: err}
}

// refuseMissing returns the refusal of a caller whose reach does not hold
// every action of the principal principalID. It reads, for each action, the
// read action of its object with the reach of the caller, so the text names
// no object that the caller cannot read.
func refuseMissing(
	ctx context.Context, holder access.Reach, principalID string, missing []access.HeldAction,
) error {
	readable := make([]bool, len(missing))
	for i := range missing {
		readable[i] = readsObject(ctx, holder, missing[i].Object)
	}

	return &reachExceededError{principalID: principalID, missing: missing, readable: readable}
}

// readsObject reports whether the reach of holder holds the read action of
// obj, so a refusal may name it. The installation and the caller answer
// true: a caller always reads them. A read that fails answers false, so the
// refusal names no object that it cannot check.
func readsObject(ctx context.Context, holder access.Reach, obj identity.Object) bool {
	if obj.IsServer() || obj.IsCaller() {
		return true
	}

	action, ok := readActionOf(obj)
	if !ok {
		return false
	}

	holds, err := holder.Has(ctx, action, obj)
	if err != nil {
		return false
	}

	return holds
}

// readActionOf returns the action that reads an object of the kind of obj.
// A principal reads as the principals of the installation, which is an
// action on the installation.
func readActionOf(obj identity.Object) (identity.Action, bool) {
	switch {
	case obj.IsNamespace():
		return identity.ActionNamespaceRead, true
	case obj.IsCollection():
		return identity.ActionCollectionRead, true
	case obj.IsUnit():
		return identity.ActionUnitRead, true
	case obj.IsPrincipal():
		return identity.ActionServerReadPrincipals, true
	default:
		return "", false
	}
}

// objectText returns the object of an action for a refusal: "the
// installation", "the caller", "namespace \"team\"", "principal
// \"prn-x\"", "collection \"team/payments\"" and "unit
// \"pulumi:team/web/prod\"".
func objectText(obj identity.Object) string {
	switch {
	case obj.IsServer():
		return "the installation"
	case obj.IsCaller():
		return "the caller"
	case obj.IsPrincipal():
		return fmt.Sprintf("principal %q", obj.PrincipalID())
	case obj.IsCollection():
		return fmt.Sprintf("collection %q", obj.String())
	case obj.IsUnit():
		return fmt.Sprintf("unit %q", obj.String())
	default:
		return fmt.Sprintf("namespace %q", obj.Namespace())
	}
}
