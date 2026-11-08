package identity

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// IsJobToken reports whether p is a consistent job token: the credential is
// a job token, the job ID is not empty, and the job unit is a unit reference.
func (p Principal) IsJobToken() bool {
	return p.Credential == CredentialJobToken && p.JobID != "" && p.JobUnit.IsUnit()
}

// carriesJobToken reports whether p carries any part of a job token. A
// principal that carries one part and not the others is inconsistent, so the
// rule denies it every action.
func (p Principal) carriesJobToken() bool {
	var zero Object

	return p.Credential == CredentialJobToken || p.JobID != "" || p.JobUnit != zero
}

// JobTokenRule limits a job token to the unit and the job of its token. It
// decides every check of a principal that carries any part of a job token and
// asks Next for every other principal.
//
// A consistent job token may act only on its own unit, and only with the
// grants of the starter: the rule clears the administrator flags before it
// asks Next. ActionJobWriteCost is the one exception: only a consistent job
// token holds it, on its unit, and Next is never asked. Every other action
// that the token asks for needs a grant, so the token adds no right of its
// own.
type JobTokenRule struct {
	Next Authorizer
}

var _ Authorizer = JobTokenRule{Next: nil}

// Authorize implements Authorizer.
func (r JobTokenRule) Authorize(ctx context.Context, p Principal, action Action, obj Object) error {
	if !p.carriesJobToken() {
		if action == ActionJobWriteCost {
			return &DeniedError{Subject: p.Subject, Action: action, Object: obj}
		}

		return r.Next.Authorize(ctx, p, action, obj)
	}

	if !p.IsJobToken() || !sameUnitObject(obj, p.JobUnit) {
		return &DeniedError{Subject: p.Subject, Action: action, Object: obj}
	}

	if action == ActionJobWriteCost {
		return nil
	}

	return r.Next.Authorize(ctx, withoutElevation(p), action, obj)
}

// withoutElevation returns p with no administrator flag and no temporary
// elevation.
//
// Defense in depth: jobtoken.Service.Authenticate clears the flags, and the
// temporary administrator decorator passes a job token through unchanged.
// This clear stays so that no job token reaches an authorizer with a flag,
// also when a new authenticator builds the principal.
func withoutElevation(p Principal) Principal {
	p.Admin = false
	p.TemporaryAdministrator = false
	p.TemporaryExpiresAt = time.Time{}

	return p
}

// sameUnitObject reports whether obj names the unit unit. When both
// references carry a stable unit ID, the IDs must be equal too.
func sameUnitObject(obj, unit Object) bool {
	if !obj.IsUnit() || !unit.IsUnit() {
		return false
	}

	if obj.Namespace() != unit.Namespace() || obj.Tool() != unit.Tool() ||
		obj.UnitName() != unit.UnitName() {
		return false
	}

	// Defense in depth: a unit name can name another unit after a delete and
	// a recreate. The authorizer of grants checks the stable unit ID too.
	// This check stays so that the rule alone keeps a token to the unit of
	// its job.
	if obj.UnitID() != "" && unit.UnitID() != "" && obj.UnitID() != unit.UnitID() {
		return false
	}

	return true
}

// ListObjects implements Authorizer. A consistent job token lists only its
// own unit, and only when the scope of Next names that unit, its namespace
// or every object. An inconsistent job token lists nothing.
func (r JobTokenRule) ListObjects(ctx context.Context, p Principal, action Action) (Scope, error) {
	if p.carriesJobToken() {
		return r.listJobToken(ctx, p, action)
	}

	if action == ActionJobWriteCost {
		return emptyScope(), nil
	}

	scope, err := r.Next.ListObjects(ctx, p, action)
	if err != nil {
		return emptyScope(), fmt.Errorf("list objects: %w", err)
	}

	return scope, nil
}

// emptyScope returns the scope that names no object.
func emptyScope() Scope {
	return Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: false}
}

// unitScope returns the scope that names only unit.
func unitScope(unit Object) Scope {
	return Scope{Namespaces: nil, Collections: nil, Units: []Object{unit}, Principals: nil, All: false}
}

// scopeHasNamespace reports whether scope names the namespace ns.
func scopeHasNamespace(scope []Object, ns namespace.Name) bool {
	for i := range scope {
		if scope[i].IsNamespace() && scope[i].Namespace() == ns {
			return true
		}
	}

	return false
}

// scopeHasUnit reports whether scope names the unit unit.
func scopeHasUnit(scope []Object, unit Object) bool {
	for i := range scope {
		if sameUnitObject(scope[i], unit) {
			return true
		}
	}

	return false
}

// ScopeExact reports that the scope is exact when the scope of Next is exact.
func (r JobTokenRule) ScopeExact() bool {
	return HasExactScope(r.Next)
}

// listJobToken returns the scope of the list of a principal that carries a
// job token: its unit when Next names that unit, its namespace or every
// object, and nothing else.
func (r JobTokenRule) listJobToken(ctx context.Context, p Principal, action Action) (Scope, error) {
	if !p.IsJobToken() {
		return emptyScope(), nil
	}

	if action == ActionJobWriteCost {
		return unitScope(p.JobUnit), nil
	}

	scope, err := r.Next.ListObjects(ctx, withoutElevation(p), action)
	if err != nil {
		return emptyScope(), fmt.Errorf("list objects: %w", err)
	}

	if scope.All || scopeHasNamespace(scope.Namespaces, p.JobUnit.Namespace()) ||
		scopeHasUnit(scope.Units, p.JobUnit) {
		return unitScope(p.JobUnit), nil
	}

	return emptyScope(), nil
}

// AuthorizeJobClaim checks that a push for the phases of jobID carries the
// job token of that job. It returns nil only when p is a consistent job token
// and its job ID is jobID, which must not be empty. Every other answer is an
// error that wraps ErrDenied and names no job ID.
func AuthorizeJobClaim(p Principal, jobID string) error {
	if jobID != "" && p.IsJobToken() && p.JobID == jobID {
		return nil
	}

	return fmt.Errorf("%w: the job token does not claim this job", ErrDenied)
}
