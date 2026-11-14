// Package trustrulesvc is the service of the workload trust rules of package
// trustrule. It adds the ID, the time, the fence and the audit entry to the
// calls of the Store, and it checks, for each new rule, that the caller can
// act as the principal of the rule and that the issuer is not an issuer of
// this installation.
package trustrulesvc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Audit actions of the trust rule changes. They are audit values only, never
// authorization actions: the routes declare namespace:manage-trust-rules.
const (
	ActionCreate = "trust-rule:create"
	ActionDelete = "trust-rule:delete"
)

// ErrSignInRequired reports a create of a rule by a caller that
// authenticated with a stored access token. A rule lets a workload get new
// tokens for its principal, so a token that could make a rule would outlive
// its own expiry and its revoke.
var ErrSignInRequired = errors.New("sign in to create a trust rule: an access token cannot")

// ErrManageTokensDenied marks the refusal of a create whose caller does not
// hold principal:manage-tokens on the principal of the rule. The error that
// carries it also wraps identity.ErrDenied. A denial of the reach check does
// not carry it, so the audit entry of a refused create names the right cause.
var ErrManageTokensDenied = errors.New("the caller does not hold principal:manage-tokens on the principal")

// ErrAccessUnchecked marks the refusal of a create whose access check could
// not run. The error that carries it also wraps the error of the authorizer.
// A denial carries ErrManageTokensDenied instead, so the audit entry names
// the right cause.
var ErrAccessUnchecked = errors.New("the access check could not run")

// ErrReachUnchecked marks the refusal of a create whose reach check could
// not run. The error that carries it also wraps the error of the reach
// check, or identity.ErrDenied when the server has no reach port. A refusal
// that names the missing actions of the principal carries no mark.
var ErrReachUnchecked = errors.New("the reach check could not run")

// Namespaces gives the namespace of a read, and the namespace of a write
// with its current fence. *namespace.Service meets it.
type Namespaces interface {
	// Get returns the namespace called name, or namespace.ErrNotFound.
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
	// WritableHere returns the namespace called name when this installation
	// may write to it. It returns namespace.ErrNotFound for an unknown
	// namespace.
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Principals reads the principal records. principal.Store meets it.
type Principals interface {
	// GetPrincipal returns the principal with the ID id, or
	// principal.ErrNotFound when no such principal exists or it was deleted.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// Reach compares the reach of a caller with the reach of the principal of a
// new rule, as the create and the rotate paths of a token do. It returns an
// error that wraps identity.ErrDenied when the caller holds
// principal:manage-tokens on the installation only and does not hold every
// action of the reach of the principal. *accesstoken.Service meets it.
type Reach interface {
	CheckReach(ctx context.Context, caller identity.Principal, principalID string) error
}

// Config holds the dependencies of a Service. Store, Namespaces, Principals
// and Authorizer are required.
type Config struct {
	Store      trustrule.Store
	Namespaces Namespaces
	Principals Principals
	// Authorizer decides principal:manage-tokens on the principal of a new
	// rule.
	Authorizer identity.Authorizer
	// Reach compares the reach of the caller with the reach of the
	// principal of a new rule. A rule lets a workload get the tokens of
	// its principal, so the right to make a rule is the right to make its
	// tokens. Nil refuses every create: without the port the Service
	// cannot compare the reaches.
	Reach Reach
	// InstallationURLs are the base URLs of the issuers of this
	// installation, for example its public URL. A rule refuses an issuer at
	// or under each of them.
	InstallationURLs []string
	// OwnIssuer is the check of the issuers that this installation signs for
	// itself: its public URL, its issuer base URL and the issuerURL of each
	// namespace. Nil means that the installation has no issuer, and only
	// InstallationURLs apply.
	OwnIssuer OwnIssuer
	// CheckURL checks the issuer of a discovery rule against the egress
	// rules of the server. Nil means the egress rules with no allowed
	// range.
	CheckURL func(raw string) error
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// OwnIssuer decides whether an issuer is an issuer of this installation.
// *issuersvc.Service meets it. An error means that the answer is unknown, and
// the caller refuses.
type OwnIssuer interface {
	OwnsIssuer(ctx context.Context, issuer string) (bool, error)
}

// Service implements the use cases of the trust rules.
type Service struct {
	store      trustrule.Store
	namespaces Namespaces
	principals Principals
	authz      identity.Authorizer
	reach      Reach
	ownIssuer  OwnIssuer
	ownURLs    []*url.URL
	checkURL   func(raw string) error
	now        func() time.Time
}

// NewService returns a Service. It returns trustrule.ErrMissingDependency
// when a required port is missing, and trustrule.ErrInvalid for an
// installation URL that does not parse as an absolute URL.
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: trust rule store", trustrule.ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespaces", trustrule.ErrMissingDependency)
	case cfg.Principals == nil:
		return nil, fmt.Errorf("%w: principals", trustrule.ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", trustrule.ErrMissingDependency)
	}

	own := make([]*url.URL, 0, len(cfg.InstallationURLs))

	for _, raw := range cfg.InstallationURLs {
		if raw == "" {
			continue
		}

		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("%w: the installation URL %q is not an absolute URL", trustrule.ErrInvalid, raw)
		}

		own = append(own, u)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	checkURL := cfg.CheckURL
	if checkURL == nil {
		checkURL = func(raw string) error { return egress.CheckURL(raw, egress.HTTPSOnly) }
	}

	return &Service{
		store: cfg.Store, namespaces: cfg.Namespaces, principals: cfg.Principals, authz: cfg.Authorizer,
		reach: cfg.Reach, ownIssuer: cfg.OwnIssuer, ownURLs: own, checkURL: checkURL, now: now,
	}, nil
}

// Input names the fields of a new rule. The service sorts the conditions by
// their claim names.
type Input struct {
	Name        string
	Issuer      string
	Audience    string
	Keys        trustrule.KeySource
	StaticKeys  string
	PrincipalID string
	Conditions  []trustrule.Condition
}

// Create makes a rule in the namespace ns for the caller, with the audit
// entry template e: the caller fills where the request came from, and the
// service fills the time, the action, the object and the changes. It
// returns:
//   - ErrSignInRequired for a caller that authenticated with a stored access
//     token or with a token of a trust rule.
//   - trustrule.ErrInvalid for a rule that fails validation, or for a
//     discovery rule whose issuer the egress rules refuse.
//   - trustrule.ErrIssuerRefused for an issuer of this installation.
//   - trustrule.ErrPrincipal for a principal that is unknown, deleted,
//     disabled, a person, or not a principal of ns.
//   - identity.ErrDenied when the caller does not hold principal:manage-tokens
//     on the principal, and when the caller holds it on the installation only
//     and the reach of the principal is not inside the reach of the caller.
//   - the errors of the namespace read and of Store.Create.
func (s *Service) Create(
	ctx context.Context, caller identity.Principal, e audit.Entry, ns namespace.Name, in Input,
) (trustrule.Rule, error) {
	// A token of a trust rule always has a token ID. The second test stays so
	// that a caller with a trust token and no ID never makes a rule.
	if caller.TokenID != "" || caller.Credential == identity.CredentialTrustToken {
		return trustrule.Rule{}, ErrSignInRequired
	}

	r := trustrule.Rule{
		CreatedAt: s.now().UTC().Truncate(time.Millisecond), ID: trustrule.NewID(), Namespace: ns, Name: in.Name,
		Issuer: in.Issuer, Audience: in.Audience, Keys: in.Keys, StaticKeys: in.StaticKeys,
		PrincipalID: in.PrincipalID, CreatedBy: caller.Subject, Conditions: trustrule.SortConditions(in.Conditions),
	}

	if err := r.Validate(); err != nil {
		return trustrule.Rule{}, err
	}

	if err := s.CheckOwnIssuer(ctx, r.Issuer); err != nil {
		return trustrule.Rule{}, err
	}

	if r.Keys == trustrule.KeySourceDiscovery {
		if err := s.checkURL(r.Issuer); err != nil {
			return trustrule.Rule{}, fmt.Errorf("%w: the issuer %q: %w", trustrule.ErrInvalid, r.Issuer, err)
		}
	}

	if err := s.authorizeCreate(ctx, caller, r.PrincipalID); err != nil {
		return trustrule.Rule{}, err
	}

	if principalErr := s.checkPrincipal(ctx, r.PrincipalID, ns); principalErr != nil {
		return trustrule.Rule{}, principalErr
	}

	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return trustrule.Rule{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	if createErr := s.store.Create(ctx, r, n.Fence(), s.createEntry(e, &r)); createErr != nil {
		return trustrule.Rule{}, fmt.Errorf("create trust rule: %w", createErr)
	}

	return r, nil
}

// CheckIssuer returns trustrule.ErrIssuerRefused when issuer is an issuer of
// this installation: it is one of the installation URLs, or a URL under one
// of them, compared with the scheme and the host in lower case. So a token
// that this installation signs never becomes an access token of it. An
// issuer that is not a URL is no URL of this installation.
func (s *Service) CheckIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err == nil && s.ownsURL(u) {
		return fmt.Errorf("%w: %q", trustrule.ErrIssuerRefused, issuer)
	}

	return nil
}

// CheckOwnIssuer returns trustrule.ErrIssuerRefused when issuer is an issuer of
// this installation, by CheckIssuer or by the OwnIssuer of the config. When the
// OwnIssuer cannot answer, the issuer is refused: the check fails closed. A
// later exchange of a token calls it again, because a namespace can set an
// issuerURL after a rule exists.
//
// Defense in depth: the static check of CheckIssuer and the check of the
// issuer service stay both, so a missing issuer service never lets an issuer
// of the public URL through, and the lookup of the issuerURLs never depends on
// the form of the public URL.
func (s *Service) CheckOwnIssuer(ctx context.Context, issuer string) error {
	if err := s.CheckIssuer(issuer); err != nil {
		return err
	}

	if s.ownIssuer == nil {
		return nil
	}

	owns, err := s.ownIssuer.OwnsIssuer(ctx, issuer)
	if err != nil {
		return fmt.Errorf("%w: %q: the server cannot tell whether it signs for this issuer: %w",
			trustrule.ErrIssuerRefused, issuer, err)
	}

	if owns {
		return fmt.Errorf("%w: %q", trustrule.ErrIssuerRefused, issuer)
	}

	return nil
}

// Get returns the rule id of ns. An unknown namespace gives
// namespace.ErrNotFound.
func (s *Service) Get(ctx context.Context, ns namespace.Name, id trustrule.ID) (trustrule.Rule, error) {
	if _, err := s.namespaces.Get(ctx, ns); err != nil {
		return trustrule.Rule{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	r, err := s.store.Get(ctx, ns, id)
	if err != nil {
		return trustrule.Rule{}, fmt.Errorf("get trust rule: %w", err)
	}

	return r, nil
}

// List returns the rules of ns, ordered by name, then ID. An unknown
// namespace gives namespace.ErrNotFound, not an empty list.
func (s *Service) List(ctx context.Context, ns namespace.Name) ([]trustrule.Rule, error) {
	if _, err := s.namespaces.Get(ctx, ns); err != nil {
		return nil, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	rules, err := s.store.List(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list trust rules: %w", err)
	}

	return rules, nil
}

// Delete removes the rule id of ns, with the audit entry template e. A
// delete only takes access away, so it needs no check on the principal of
// the rule.
func (s *Service) Delete(ctx context.Context, e audit.Entry, ns namespace.Name, id trustrule.ID) error {
	if err := id.Validate(); err != nil {
		return trustrule.ErrNotFound
	}

	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return fmt.Errorf("read namespace %s: %w", ns, err)
	}

	if err := s.store.Delete(ctx, ns, id, n.Fence(), s.entry(e, ns, id, ActionDelete)); err != nil {
		return fmt.Errorf("delete trust rule: %w", err)
	}

	return nil
}

// checkPrincipal returns trustrule.ErrPrincipal when the principal id cannot
// be the principal of a rule of ns.
//
// Defense in depth: the store checks the principal again in the transaction
// of the create. This check stays so that a disabled principal, which the
// store does not refuse, never gets a rule, and so that a refused principal
// costs no write.
func (s *Service) checkPrincipal(ctx context.Context, id string, ns namespace.Name) error {
	p, err := s.principals.GetPrincipal(ctx, id)
	if errors.Is(err, principal.ErrNotFound) {
		return fmt.Errorf("%w: no principal %q", trustrule.ErrPrincipal, id)
	}

	if err != nil {
		return fmt.Errorf("read principal %q: %w", id, err)
	}

	switch {
	case p.Kind != principal.KindService:
		return fmt.Errorf("%w: principal %q is a %s, not a service principal", trustrule.ErrPrincipal, id, p.Kind)
	case p.Namespace != ns:
		return fmt.Errorf("%w: principal %q is not a principal of the namespace %q", trustrule.ErrPrincipal, id, ns)
	case !p.DisabledAt.IsZero():
		return fmt.Errorf("%w: principal %q is disabled", trustrule.ErrPrincipal, id)
	default:
		return nil
	}
}

// authorizeCreate checks that caller may make a rule for the principal
// principalID: it holds principal:manage-tokens on the principal, and, when
// it holds that action on the installation only, the reach of the principal
// is inside its own reach. The right to make a rule is the right to make its
// tokens, so a create runs the same checks as a create of a token. The checks
// come before the read of the principal, so a caller that fails one never
// learns whether the ID names a principal, or its kind.
//
// A denial of the action returns ErrManageTokensDenied, and a check that
// cannot run returns ErrAccessUnchecked. The refusal of the reach check
// returns unwrapped, so that the adapter answers its text, as the token route
// does; a reach check that cannot run returns ErrReachUnchecked.
func (s *Service) authorizeCreate(ctx context.Context, caller identity.Principal, principalID string) error {
	obj, err := identity.ParsePrincipalObject(principalID)
	if err != nil {
		return fmt.Errorf("%w: %w", trustrule.ErrPrincipal, err)
	}

	if authzErr := s.authz.Authorize(ctx, caller, identity.ActionPrincipalManageTokens, obj); authzErr != nil {
		if errors.Is(authzErr, identity.ErrDenied) {
			return fmt.Errorf(
				"make a trust rule for principal %q: %w: %w",
				principalID,
				ErrManageTokensDenied,
				authzErr,
			)
		}

		return fmt.Errorf(
			"make a trust rule for principal %q: %w: %w",
			principalID,
			ErrAccessUnchecked,
			authzErr,
		)
	}

	return s.checkReach(ctx, caller, principalID)
}

// checkReach asks the reach port whether the reach of the caller holds every
// action of the reach of the principal of a new rule. Without it, a caller
// that holds principal:manage-tokens on the installation only makes a rule
// for a principal that reaches past it, and gets its tokens through the
// exchange, while the token route refuses them.
//
// The refusal of the port returns unwrapped when it names at least one
// missing action, so that the adapter answers its text. A missing port, and
// every other error of the port, return ErrReachUnchecked: the check could
// not run. The missing port also wraps identity.ErrDenied.
//
// Defense in depth: the create and the rotate paths of a token run the same
// compare, in package accesstoken. This check stays so that the rule path
// cannot get the tokens of a principal that the token path refuses, and so
// that a new caller of the store cannot skip the compare.
func (s *Service) checkReach(ctx context.Context, caller identity.Principal, principalID string) error {
	// Defense in depth: NewService allows a missing Reach, for an
	// installation that keeps no token service. This check stays so that a
	// Service built any other way fails closed: without the port it
	// refuses every create.
	if s.reach == nil {
		return fmt.Errorf(
			"%w: %w: the server cannot check the reach of the principal of a trust rule",
			ErrReachUnchecked,
			identity.ErrDenied,
		)
	}

	err := s.reach.CheckReach(ctx, caller, principalID)
	if err == nil {
		return nil
	}

	if refused, ok := errors.AsType[access.ReachRefusal](err); ok && len(refused.MissingActions()) > 0 {
		return err
	}

	return fmt.Errorf("%w: %w", ErrReachUnchecked, err)
}

// ownsURL reports whether u is at or under one of the installation URLs.
func (s *Service) ownsURL(u *url.URL) bool {
	for _, own := range s.ownURLs {
		if !strings.EqualFold(u.Scheme, own.Scheme) || !strings.EqualFold(u.Host, own.Host) {
			continue
		}

		base := strings.TrimSuffix(own.Path, "/")
		if base == "" || u.Path == base || strings.HasPrefix(u.Path, base+"/") {
			return true
		}
	}

	return false
}

// entry completes the template e for one change of the rule id in ns.
func (s *Service) entry(e audit.Entry, ns namespace.Name, id trustrule.ID, action string) audit.Entry {
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Action = action
	e.Object = identity.NamespaceObject(ns).String()
	e.ObjectID = string(id)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	return e
}

// createEntry is the entry of the create of r, with a change for each field
// that decides which tokens the rule accepts. The static keys are public,
// but long, so the entry names the key source and not the keys.
func (s *Service) createEntry(e audit.Entry, r *trustrule.Rule) audit.Entry {
	out := s.entry(e, r.Namespace, r.ID, ActionCreate)

	// Each value is quoted, so a value that holds the separator ", " still
	// reads as one value.
	conds := make([]string, 0, len(r.Conditions))
	for _, c := range r.Conditions {
		quoted := make([]string, 0, len(c.Values))
		for _, v := range c.Values {
			quoted = append(quoted, strconv.Quote(v))
		}

		conds = append(conds, c.Claim+" in ["+strings.Join(quoted, ", ")+"]")
	}

	out.Changes = []audit.Change{
		{Key: "name", Old: nil, New: r.Name, Layer: "", TakesEffect: ""},
		{Key: "issuer", Old: nil, New: r.Issuer, Layer: "", TakesEffect: ""},
		{Key: "audience", Old: nil, New: r.Audience, Layer: "", TakesEffect: ""},
		{Key: "keySource", Old: nil, New: string(r.Keys), Layer: "", TakesEffect: ""},
		{Key: "principal", Old: nil, New: r.PrincipalID, Layer: "", TakesEffect: ""},
		{Key: "conditions", Old: nil, New: strings.Join(conds, "; "), Layer: "", TakesEffect: ""},
	}

	return out
}
