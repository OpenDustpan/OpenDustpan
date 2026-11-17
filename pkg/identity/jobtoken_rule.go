package identity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrJobNotFound reports that a job ID names no job, or names a job that
// ended. A job token of such a job is bound to no unit, so the rule of job
// tokens refuses every action of it.
var ErrJobNotFound = errors.New("identity: the job of a job token was not found")

// errNoJobUnits reports a rule of job tokens without a port that reads the
// unit of a job. The rule then refuses every action of a job token: a rule
// that cannot read the unit of a job must not guess one.
var errNoJobUnits = errors.New("identity: the rule of job tokens has no job unit port")

// errJobTokenMismatch reports a principal that carries a job ID with another
// credential than the credential of a job token. Such a principal is not a
// job token, and the rule of job tokens refuses every action of it.
var errJobTokenMismatch = errors.New("identity: the principal carries a job ID without the job token credential")

// JobUnit is the unit of a job: the reference of the unit that a job acts on,
// with the stable ID of the unit. The rule of job tokens bounds every action
// of a token to this unit.
//
// The rule compares the stable unit ID and never the name of the unit. A name
// is a label that a user can change, so a rename must not move the reach of a
// token to another unit, and it must not refuse a token of the unit either.
type JobUnit struct {
	object Object
}

// NewJobUnit returns the unit of a job. It refuses a reference that is not a
// unit, a unit without a namespace and a unit without a stable ID: the rule
// cannot bound a token to such a unit, so a store that answers one is a
// failure and never a wider rule.
func NewJobUnit(obj Object) (JobUnit, error) {
	if !obj.IsUnit() || obj.Namespace() == "" || obj.UnitID() == "" {
		return JobUnit{}, fmt.Errorf(
			"%w: the unit of a job is %q, which is not a unit reference with a namespace and a stable ID",
			ErrInvalidObject, obj,
		)
	}

	return JobUnit{object: obj}, nil
}

// Object returns the reference of the unit of the job, with its stable ID.
func (u JobUnit) Object() Object { return u.object }

// UnitID returns the stable ID of the unit of the job.
func (u JobUnit) UnitID() string { return u.object.UnitID() }

// JobUnits answers the unit of the job of a job token, by the job ID of the
// token. The rule of job tokens reads it on every request, so a token that a
// job issued never acts outside the unit that the job holds now.
//
// An implementation returns an error that wraps ErrJobNotFound for a job that
// does not exist or that ended, and another error for a store that cannot
// answer. The rule refuses the token on both: a failed read never widens the
// reach of a token.
type JobUnits interface {
	// UnitOfJob returns the unit of the job with the ID jobID.
	UnitOfJob(ctx context.Context, jobID string) (JobUnit, error)
}

// UnitIDReader returns the stable ID of the unit obj, and false when no such
// unit exists or obj is not a unit. It has the signature of access.Units.
type UnitIDReader interface {
	UnitID(ctx context.Context, obj Object) (string, bool, error)
}

// JobTokenRule decides every check of a job token, and asks Next for every
// other principal. A job token acts on the unit of its job only, and only
// when it is a consistent job token: a principal that carries a job ID with
// another credential, a token of a job that does not exist or that ended, and
// a token of a rule that cannot read the unit of its job are refused every
// action.
//
// Two reads reach Next unchanged, also on another unit: the read of the
// outputs of a unit and the read of its secret outputs. A job of one unit
// reads the outputs of another unit when the sharing rules of that unit and
// the grants of the starter both allow it, and the checker of the read
// decides that. Every other action of another unit, the collection, the
// namespace, the installation and the principals are refused here.
//
// The rule clears the administrator and temporary administrator marks of the
// principal before it asks Next. Defense in depth: the job token service
// clears them too, and no authenticator gives a job token one. This stays so
// that an Authorizer of an embedder that allows by the Admin flag never
// widens a job token, and so that the rule never passes a mark on.
type JobTokenRule struct {
	Next Authorizer
	// Units answers the unit of the job of a job token. A rule without a
	// port refuses every job token: fail closed.
	Units JobUnits
	// UnitIDs resolves the stable ID of a unit that the route names without
	// one. A rule without a port refuses such a unit: fail closed.
	UnitIDs UnitIDReader
}

var _ Authorizer = JobTokenRule{Next: nil, Units: nil, UnitIDs: nil}

// Authorize implements Authorizer.
func (r JobTokenRule) Authorize(ctx context.Context, p Principal, action Action, obj Object) error {
	if !claimsJobToken(p) {
		return r.Next.Authorize(ctx, p, action, obj)
	}

	unit, err := r.unitOfJob(ctx, p)

	switch {
	case jobTokenRefused(err):
		// The rule cannot bound this principal to a unit, so every action of
		// it is refused. A caller reads the reason of the entry from the
		// denial: the subject, the action and the object, never the job.
		return &DeniedError{Subject: p.Subject, Action: action, Object: obj}
	case err != nil:
		// Fail closed: the unit of the job is unknown, so the rule does not
		// decide. The refusal carries no denial, so the entry of the request
		// says that the authorization failed.
		return fmt.Errorf("read the unit of the job of the job token: %w", err)
	}

	if action == ActionUnitReadOutputs || action == ActionUnitReadSecrets {
		return r.Next.Authorize(ctx, withoutElevation(p), action, obj)
	}

	// Resolve the stable ID of a unit that the route names without one. The
	// read is a defense-in-depth layer: the route builds the unit object from
	// the name and the comparison below does not rely on the route resolving
	// it, so a route that forgets this call cannot widen a job token.
	resolved, err := r.resolveUnitID(ctx, p, action, obj)
	if err != nil {
		return err
	}

	if !resolved.IsUnit() || resolved.UnitID() != unit.UnitID() {
		return &DeniedError{Subject: p.Subject, Action: action, Object: obj}
	}

	return r.Next.Authorize(ctx, withoutElevation(p), action, obj)
}

// ListObjects gives a job token the unit of its job only, and nothing when
// the scope of Next does not name that unit by its stable ID. It asks Next
// for every other principal.
//
// A scope that covers a namespace, a collection or every object is not
// narrowed to the unit of the job here: an Object does not carry the
// collections of a unit, so the rule cannot prove that such a scope names the
// unit of the job. It answers the empty scope then, which is the fail-closed
// answer for a list.
func (r JobTokenRule) ListObjects(ctx context.Context, p Principal, action Action) (Scope, error) {
	if !claimsJobToken(p) {
		return r.Next.ListObjects(ctx, p, action)
	}

	unit, err := r.unitOfJob(ctx, p)

	switch {
	case jobTokenRefused(err):
		return emptyScope(), nil
	case err != nil:
		return emptyScope(), fmt.Errorf("list objects: %w", err)
	}

	scope, err := r.Next.ListObjects(ctx, withoutElevation(p), action)
	if err != nil {
		return emptyScope(), fmt.Errorf("list objects: %w", err)
	}

	units := make([]Object, 0, len(scope.Units))
	for i := range scope.Units {
		if scope.Units[i].IsUnit() && scope.Units[i].UnitID() == unit.UnitID() {
			units = append(units, scope.Units[i])
		}
	}

	if len(units) == 0 {
		return emptyScope(), nil
	}

	return Scope{
		Namespaces: nil, Collections: nil, Units: units, Principals: nil,
		every: everyObject{set: false, except: nil},
	}, nil
}

// ScopeExact reports that the scope is exact when the scope of Next is exact.
// The rule never names an object that Next does not name, and Next allows
// every object of its own exact scope, so no object that the rule lists is
// refused.
func (r JobTokenRule) ScopeExact() bool {
	return HasExactScope(r.Next)
}

// claimsJobToken reports whether p claims to be a job token: it carries a job
// ID, or the credential of a job token. A principal that carries one without
// the other is not a consistent job token, and the rule refuses every action
// of it. Defense in depth: only the job token service sets either field, and
// it sets both, so the rule decides no request of another authenticator.
func claimsJobToken(p Principal) bool {
	return p.JobID != "" || p.Credential == CredentialJobToken
}

// unitOfJob returns the unit of the job of the job token of p. It returns
// errJobTokenMismatch for a principal that is not a consistent job token,
// errNoJobUnits for a rule without a port, ErrJobNotFound for a job that does
// not exist or that ended, and the error of the port otherwise.
func (r JobTokenRule) unitOfJob(ctx context.Context, p Principal) (JobUnit, error) {
	if p.JobTokenID() == "" {
		return JobUnit{}, errJobTokenMismatch
	}

	if r.Units == nil {
		return JobUnit{}, errNoJobUnits
	}

	unit, err := r.Units.UnitOfJob(ctx, p.JobID)
	if err != nil {
		return JobUnit{}, err
	}

	return unit, nil
}

// jobTokenRefused reports whether err says that the rule refuses the
// principal, rather than that the rule could not read the unit of its job.
func jobTokenRefused(err error) bool {
	return errors.Is(err, errJobTokenMismatch) || errors.Is(err, errNoJobUnits) || errors.Is(err, ErrJobNotFound)
}

// withoutElevation returns p with no administrator mark. The rule asks Next
// with the result, so no Authorizer of an embedder sees a job token as an
// administrator.
func withoutElevation(p Principal) Principal {
	p.Admin = false
	p.TemporaryAdministrator = false
	p.TemporaryExpiresAt = time.Time{}

	return p
}

// emptyScope returns the scope of a principal that may list no object.
func emptyScope() Scope {
	return Scope{
		Namespaces: nil, Collections: nil, Units: nil, Principals: nil,
		every: everyObject{set: false, except: nil},
	}
}

// resolveUnitID returns obj with its stable ID filled in when it is a unit
// that the route names without one. It asks UnitIDs for the ID. A rule with
// no port, or a unit that the port does not find, is refused. A port error
// is returned as an error that is not a denial.
//
// The read is a defense-in-depth layer: the route builds the unit object from
// the name and the comparison in Authorize does not rely on the route
// resolving it, so a route that forgets this call cannot widen a job token.
func (r JobTokenRule) resolveUnitID(
	ctx context.Context, p Principal, action Action, obj Object,
) (Object, error) {
	if !obj.IsUnit() || obj.UnitID() != "" {
		return obj, nil
	}

	if r.UnitIDs == nil {
		return obj, &DeniedError{Subject: p.Subject, Action: action, Object: obj}
	}

	id, found, err := r.UnitIDs.UnitID(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("read the stable ID of the unit %q: %w", obj, err)
	}

	if !found {
		return obj, &DeniedError{Subject: p.Subject, Action: action, Object: obj}
	}

	return obj.WithUnitID(id), nil
}
