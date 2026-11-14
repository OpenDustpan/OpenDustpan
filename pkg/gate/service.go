package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errNotImplemented is the error of each part of the Service that this
// version does not implement yet. It wraps ErrUnavailable, so a caller
// refuses the change or the call and never allows it.
var errNotImplemented = fmt.Errorf("%w: the gate evaluation is not implemented in this version", ErrUnavailable)

// JobClaim is the job of a job token: the job, its phase, and the units of
// the job.
type JobClaim struct {
	Job   string
	Phase string
	// PassedApply is true for a phase that passed change.apply.
	PassedApply bool
	// Units holds the units of the job.
	Units []identity.Object
}

// Subject is the principal of a change or a call, with the facts that the
// server knows from its credential.
type Subject struct {
	ID         string
	Groups     []string
	Kind       PrincipalKind
	Credential CredentialKind
	// AuthAt is the time of the last authentication, or the zero time.
	AuthAt time.Time
	// BreakGlassAccount is true for a session of a break-glass account.
	// Such a session is outside the access limits, and meets each gate.
	BreakGlassAccount bool
	// Job is the job of a job token, and nil for each other credential.
	Job *JobClaim
}

// SubjectSource gives the Subject of an authenticated principal.
//
// Only a session of a break-glass account is outside the access limits.
// identity.Principal.Admin, which the bootstrap admin token gives, grants
// each action through the grants and does not set BreakGlassAccount: the
// access limits hold for it as for each other principal.
type SubjectSource interface {
	// Subject returns the Subject of p. It returns identity.ErrDenied for
	// a principal that the source does not know, and its store errors
	// wrapped.
	Subject(ctx context.Context, p identity.Principal) (Subject, error)
}

// UnitInfo is what the server keeps about a unit for the gates and the
// access limits: its collection and its labels.
type UnitInfo struct {
	// Collection is the collection of the unit, or the empty name for a unit
	// in no collection.
	Collection collection.Name
	// Labels holds the key=value labels of the unit, sorted.
	Labels []string
}

// UnitSource gives the collection and the labels of a unit with one read, so
// that both facts come from one state of the unit.
type UnitSource interface {
	// Unit returns the collection and the labels of unit. It returns
	// ErrNotFound for a unit that does not exist.
	Unit(ctx context.Context, unit identity.Object) (UnitInfo, error)
}

// Membership gives the collection of a unit. A unit is in at most one
// collection.
type Membership interface {
	// Collection returns the collection of unit. It returns ErrNotFound for
	// a unit in no collection.
	Collection(ctx context.Context, unit identity.Object) (collection.Name, error)
}

// FenceSource gives the fence of a namespace, for the decision records that
// the Service writes in their own transaction.
type FenceSource interface {
	// Fence returns the current fence of ns. It returns ErrNotFound for a
	// namespace that does not exist, and its store errors wrapped.
	Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error)
}

// Change is one evaluation request of one decision point.
type Change struct {
	Point     DecisionPoint
	Namespace namespace.Name
	Ref       ChangeRef
	Subject   Subject
	// Source is the kind of the source of the change. At state.write it is
	// cli for a write from the CLI of a tool, and api for a call of the
	// tool-neutral API. The empty value counts as api.
	Source SourceKind
	// Author is the subject of the author, for the rules that do not let
	// the author approve.
	Author    string
	Operation OperationKind
	// Units holds the units of the change. One Cedar request runs for each
	// unit.
	Units []identity.Object
	// Holders holds the extra scopes whose gates apply: the blueprint
	// version or the profile that the change uses.
	Holders []Scope
	// PlanLock is true at state.write for the lock or the update lease of
	// the plan of the job in the token. Only the adapter sets it, and only
	// for that lock or lease.
	PlanLock bool
	// BreakGlass is the ID of the audit entry of a break-glass for gates
	// in error, or empty. It passes only the gates in error.
	BreakGlass string
	// Fence is the fence of Namespace. The record of a refusal is written
	// with it.
	Fence namespace.Fence
	// State is what the write changes in the state of the unit, or nil when
	// the server holds no new state. It is valid at state.write for a rollback
	// of one unit only. Nil gives state.available false.
	State *StateChange
}

// Decision is the result of one evaluation. Record is empty for a decision
// that writes no record.
type Decision struct {
	Record       RecordID
	Result       Result
	Requirements []Requirement
	Warnings     []string
	AutoApply    bool
}

// AccessCall is one call that the grants allowed, for the access limits.
type AccessCall struct {
	Principal identity.Principal
	Action    identity.Action
	Object    identity.Object
}

// Service evaluates the gates of each decision point and the access limits
// of each call. Each caller of a decision point uses it: the job model, the
// change orders, Self-service, the profiles, the tool-neutral state
// operations and each tool adapter at state.write. A tool adapter calls
// Evaluate before the write and writes a refusal in the error shape of its
// own tool.
//
// This version implements the checks of the request, and the job token at
// state.write. Each other evaluation returns an error that wraps
// ErrUnavailable, so it refuses and never allows.
type Service struct {
	store     Store
	engine    Engine
	producers []Producer
	units     UnitSource
	subjects  SubjectSource
	fences    FenceSource
	now       func() time.Time
	log       *slog.Logger
}

// ServiceConfig holds the ports of a Service.
type ServiceConfig struct {
	Store     Store
	Engine    Engine
	Producers []Producer
	Units     UnitSource
	Subjects  SubjectSource
	Fences    FenceSource
	// Now is the clock, and time.Now when nil.
	Now func() time.Time
	Log *slog.Logger
}

// NewService returns a Service. It returns ErrInvalid when a port or the
// logger is nil.
func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Store == nil || cfg.Engine == nil || cfg.Units == nil || cfg.Subjects == nil || cfg.Fences == nil ||
		cfg.Log == nil {
		return nil, fmt.Errorf("%w: the gate service needs each port and a logger", ErrInvalid)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		store: cfg.Store, engine: cfg.Engine, producers: slices.Clone(cfg.Producers), units: cfg.Units,
		subjects: cfg.Subjects, fences: cfg.Fences, now: now, log: cfg.Log,
	}, nil
}

// Evaluate produces the facts, evaluates the gates of c.Point for each
// unit, combines the results, and writes the decision record.
//
// It returns the decision and a nil error for allow, warn and
// awaiting-approval. At state.write, a CLI cannot wait, so an approval
// outcome refuses. A refusal is a *RefusalError that wraps ErrDenied for a
// deny or an approval at state.write, ErrRuleFailed for a gate in error
// in enforce mode, or ErrUnavailable when the engine does not answer.
// It returns ErrLimit when the fact document does not fit with each
// large set empty. It returns ErrInvalid for a change that names no
// unit or an unknown decision point. The record of a refusal is
// written in its own transaction. A store error comes back wrapped,
// and refuses the change.
//
// A job token at state.write. A write with the job token of a phase that
// passed change.apply, to a unit of its job, gives the decision allow and
// writes no record, because its change met each earlier decision point.
// So does the lock or the update lease of the plan (PlanLock) of a unit of
// its job. Each other write with a job token returns an error that wraps
// identity.ErrDenied, and writes no record.
//
// This version evaluates the gates of state.write for a unit. It returns an
// error that wraps ErrUnavailable for each other decision point, and for a
// change with a BreakGlass: no route gives a break-glass yet, so none passes
// a gate in error.
func (s *Service) Evaluate(ctx context.Context, c Change) (Decision, error) {
	point, err := ParseDecisionPoint(string(c.Point))
	if err != nil {
		return Decision{}, err
	}

	if len(c.Units) == 0 {
		return Decision{}, fmt.Errorf("%w: the change names no unit", ErrInvalid)
	}

	for i := range c.Units {
		if !c.Units[i].IsUnit() {
			return Decision{}, fmt.Errorf(
				"%w: %q (unit %d of the change) is not the reference of a unit",
				ErrInvalid, c.Units[i].String(), i,
			)
		}
	}

	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("evaluate %s: %w", point, err)
	}

	if point == PointStateWrite && c.Subject.Credential == CredentialJobToken {
		return jobTokenWrite(c)
	}

	if point == PointStateWrite {
		return s.evaluateStateWrite(ctx, c)
	}

	return Decision{}, errNotImplemented
}

// jobTokenWrite decides a write at state.write with a job token.
func jobTokenWrite(c Change) (Decision, error) {
	job := c.Subject.Job
	if job == nil {
		return Decision{}, fmt.Errorf("%w: a job token with no job writes no state", identity.ErrDenied)
	}

	for i := range c.Units {
		unit := c.Units[i].String()
		if !slices.ContainsFunc(job.Units, func(j identity.Object) bool { return j.String() == unit }) {
			return Decision{}, fmt.Errorf("%w: job %s does not hold the unit %q", identity.ErrDenied, job.Job, unit)
		}
	}

	if !job.PassedApply && !c.PlanLock {
		return Decision{}, fmt.Errorf(
			"%w: phase %s of job %s did not pass change.apply, and writes only the lock of its plan",
			identity.ErrDenied, job.Phase, job.Job,
		)
	}

	return Decision{Record: "", Result: ResultAllow, Requirements: nil, Warnings: nil, AutoApply: false}, nil
}

// CheckAccess evaluates the access limits of each scope of the chain of the
// object of call, after the grants allowed the call. Access limits use the
// linked engine only.
//
// It returns nil when no limit forbids the call, and for a session of a
// break-glass account. A refusal is a *RefusalError that wraps ErrLimited
// and names the limit, or ErrRuleFailed for a limit that gave an engine
// error. It writes the record of a refusal with Records.NoteRefusal and
// AccessRefusalWindow.
//
// This version evaluates the limits for a call on a unit. It returns nil for
// a break-glass account, and an error that wraps ErrUnavailable for a call
// on any other object.
func (s *Service) CheckAccess(ctx context.Context, call AccessCall) error {
	subject, err := s.subjects.Subject(ctx, call.Principal)
	if err != nil {
		return fmt.Errorf("access limits of %s: %w", call.Action, err)
	}

	if subject.BreakGlassAccount {
		return nil
	}

	return s.checkLimits(ctx, subject, call)
}

// limitedAuthorizer is the identity.Authorizer that LimitedAuthorizer gives.
type limitedAuthorizer struct {
	inner identity.Authorizer
	svc   *Service
}

// LimitedAuthorizer returns an identity.Authorizer that asks inner first,
// and asks svc for the access limits when inner allows. ListObjects gives
// the scope of inner and does not filter it by the access limits, so its
// callers Authorize the namespace of the list first.
//
//nolint:ireturn // The decorator is an identity.Authorizer, and its callers take that port.
func LimitedAuthorizer(inner identity.Authorizer, svc *Service) identity.Authorizer {
	return limitedAuthorizer{inner: inner, svc: svc}
}

// Authorize returns the error of the grants, then the error of the access
// limits. A refusal of a limit wraps ErrLimited and identity.ErrDenied.
func (a limitedAuthorizer) Authorize(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if err := a.inner.Authorize(ctx, p, action, obj); err != nil {
		return fmt.Errorf("grants: %w", err)
	}

	err := a.svc.CheckAccess(ctx, AccessCall{Principal: p, Action: action, Object: obj})
	if err == nil {
		return nil
	}

	if errors.Is(err, identity.ErrDenied) {
		return err
	}

	// Only a refusal of a limit is a denial. A store or engine failure keeps its
	// own cause and does not match identity.ErrDenied. It still refuses the
	// call, because Authorize returns a non-nil error.
	// Defense in depth: the caller denies on any error; this check stays so that
	// a failure is never classified as an access refusal.
	if _, ok := errors.AsType[*RefusalError](err); ok {
		return fmt.Errorf("%w: %w", identity.ErrDenied, err)
	}

	return err
}

// ListObjects returns the scope of inner, with no filter of the access
// limits.
func (a limitedAuthorizer) ListObjects(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
) (identity.Scope, error) {
	scope, err := a.inner.ListObjects(ctx, p, action)
	if err != nil {
		return identity.Scope{}, fmt.Errorf("grants: %w", err)
	}

	return scope, nil
}
