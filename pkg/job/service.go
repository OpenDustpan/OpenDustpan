package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// PhaseVersion is the version of the durable kinds of the phases that the
// service dispatches.
const PhaseVersion = 1

// MaxIdempotencyKey is the longest idempotency key of a start, in bytes.
const MaxIdempotencyKey = 255

// UnitResolver finds a unit for the service. It is the only port that reads
// the inventory, so the service never learns the model of a tool.
type UnitResolver interface {
	// Unit returns the unit reference ref with its stable unit ID set
	// (identity.Object.WithUnitID). It returns ErrNotFound for a unit that
	// does not exist, and its store errors wrapped.
	Unit(ctx context.Context, ref identity.Object) (identity.Object, error)
}

// Planner completes a start request into the spec of a job. It reads the
// settings of the unit and the defaults of its tool, and it is the only
// place that knows a command of a tool. The service calls it after it checked
// the actions of the caller on the unit, so a caller without them learns
// nothing from its errors.
type Planner interface {
	// Plan returns the spec for req on unit, the unit reference with its
	// unit ID. It takes the executor, the code location and the pool from
	// req or from the settings of the unit, and the steps from the settings
	// of the unit, its blueprint or its tool. It returns ErrInvalid, wrapped
	// with the reason, for a request that has no usable value, and
	// ErrUnsupported for an operation that the tool of the unit cannot do.
	Plan(ctx context.Context, unit identity.Object, req *StartRequest) (Spec, error)
}

// FenceSource gives the fence of a namespace for the writes of the service.
type FenceSource interface {
	// Fence returns the current fence of ns. It returns namespace.ErrNotFound
	// for a namespace that does not exist.
	Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error)
}

// EnforcedGate names a gate that the server enforces at a decision point that
// a job start meets.
type EnforcedGate struct {
	// Scope is the scope key of the gate.
	Scope string
	// ID is the ID of the gate in its scope.
	ID string
	// Point is the decision point of the gate: change.propose or state.write.
	Point string
}

// GateError is the error of a start that an enforced gate covers. It wraps
// ErrGateEnforced and names the gates.
type GateError struct {
	Gates []EnforcedGate
}

// Error implements error.
func (e *GateError) Error() string {
	names := make([]string, 0, len(e.Gates))
	for _, g := range e.Gates {
		names = append(names, fmt.Sprintf("%s at %s (%s)", g.ID, g.Point, g.Scope))
	}

	return fmt.Sprintf(
		"%s: %s; this server cannot evaluate it for a job yet",
		ErrGateEnforced,
		strings.Join(names, ", "),
	)
}

// Unwrap returns ErrGateEnforced.
func (*GateError) Unwrap() error { return ErrGateEnforced }

// GateGuard tells which enforced gates cover a unit. A start meets the gates
// of change.propose and state.write for the unit, and so does each claim of a
// phase of the job (DurableClaimer). The gate service does not evaluate
// change.propose yet, and the job token passes state.write with no
// evaluation, so the start and the claim refuse when such a gate covers the
// unit and never skip it.
type GateGuard interface {
	// Enforced returns the gates in enforce mode at change.propose or
	// state.write on the scope chain of unit: the installation, the
	// namespace, the collection of the unit and the unit. A gate in
	// evaluate (audit) mode is not in the list. Any error refuses the start.
	Enforced(ctx context.Context, unit identity.Object) ([]EnforcedGate, error)
}

// ServiceConfig holds the ports of a service. Every port but Freezes,
// Switches, Now and NewID is required.
type ServiceConfig struct {
	Store     Store
	Units     UnitResolver
	Planner   Planner
	Executors []Executor
	Decider   Decider
	// Authorizer decides the start actions. The server gives it the access
	// limits of the starter (gate.LimitedAuthorizer).
	Authorizer identity.Authorizer
	// Gates tells which enforced gates cover a unit. It is required: a
	// service with no guard would start jobs that skip the gates.
	Gates  GateGuard
	Fences FenceSource
	// Freezes names the active freeze of a break-glass start. Nil refuses
	// every break-glass.
	Freezes FreezeStore
	// Switches gives the switches of AgentModeDirect for a user in a
	// namespace. Nil keeps all three off.
	Switches func(ctx context.Context, ns namespace.Name, user string) DirectSwitches
	// Now is the clock, and time.Now when nil.
	Now func() time.Time
	// NewID makes the ID of a job, and ident.NewID(IDPrefix) when nil.
	NewID func() ID
	Log   *slog.Logger
}

// service is the Service of this package. This version starts a job and
// reads one. It refuses the other methods, so no caller thinks that a
// missing rule is a rule.
type service struct {
	store      Store
	units      UnitResolver
	planner    Planner
	executors  map[ExecutorKind]Executor
	decider    Decider
	authorizer identity.Authorizer
	gates      GateGuard
	fences     FenceSource
	freezes    FreezeStore
	switches   func(ctx context.Context, ns namespace.Name, user string) DirectSwitches
	now        func() time.Time
	newID      func() ID
	log        *slog.Logger
}

// Starter is a Service that tells a retry from a first start. The REST API
// uses it to mark a replay of a start.
type Starter interface {
	Service
	// StartOnce is Start, and created is false when req.IdempotencyKey named
	// a job that exists already: the answer is that job.
	StartOnce(ctx context.Context, a Actor, req StartRequest) (j Job, created bool, err error)
}

var _ Starter = (*service)(nil)

// NewService returns the Service over the ports of cfg. It returns
// ErrInvalid when a required port is nil, or when two executors have the
// same kind.
func NewService(cfg ServiceConfig) (Starter, error) { //nolint:ireturn // The port is the contract of the result.
	if cfg.missingPort() {
		return nil, fmt.Errorf("%w: the job service needs each port, an executor and a logger", ErrInvalid)
	}

	executors, err := indexExecutors(cfg.Executors)
	if err != nil {
		return nil, err
	}

	s := &service{
		store:      cfg.Store,
		units:      cfg.Units,
		planner:    cfg.Planner,
		executors:  executors,
		decider:    cfg.Decider,
		authorizer: cfg.Authorizer,
		gates:      cfg.Gates,
		fences:     cfg.Fences,
		freezes:    cfg.Freezes,
		switches:   cfg.Switches,
		now:        cfg.Now,
		newID:      cfg.NewID,
		log:        cfg.Log,
	}

	if s.now == nil {
		s.now = time.Now
	}

	if s.newID == nil {
		s.newID = func() ID { return ID(ident.NewID(IDPrefix)) }
	}

	return s, nil
}

// Start implements Service.
func (s *service) Start(ctx context.Context, a Actor, req StartRequest) (Job, error) {
	j, _, err := s.StartOnce(ctx, a, req)

	return j, err
}

// StartOnce implements Starter.
//
// The order of the checks is the order of what a caller may learn. A request
// that is not valid fails before any read. Then the unit, then the actions of
// the caller on it: a caller without them learns nothing about the settings of
// the unit. Then the extra actions of the pool and the location that the
// request names. Then the spec, the gates that this version cannot evaluate,
// the agent mode, the executor, the policy and last the store, which holds the
// freezes, the manual locks and the limits.
func (s *service) StartOnce(ctx context.Context, a Actor, req StartRequest) (Job, bool, error) {
	err := ctx.Err()
	if err != nil {
		return Job{}, false, fmt.Errorf("start job: %w", err)
	}

	source, err := checkRequest(a, &req)
	if err != nil {
		return Job{}, false, err
	}

	unit, err := s.unitOf(ctx, req.Unit)
	if err != nil {
		return Job{}, false, err
	}

	// The executor may come from the settings of the unit, which the planner
	// reads. A caller must not learn those settings without an action, so the
	// planner runs only for a caller who holds the start action of at least
	// one executor that can do the operation. The exact actions of the spec
	// are checked after it.
	err = s.preAuthorize(ctx, a, unit, &req)
	if err != nil {
		return Job{}, false, err
	}

	err = s.authorizeFields(ctx, a, unit, &req)
	if err != nil {
		return Job{}, false, err
	}

	spec, err := s.planner.Plan(ctx, unit, &req)
	if err != nil {
		return Job{}, false, fmt.Errorf("plan job: %w", err)
	}

	err = checkPlanned(unit, &spec, &req)
	if err != nil {
		return Job{}, false, err
	}

	// The service sets the list itself, so a planner can neither hide a
	// default nor invent one.
	spec.Defaulted = DefaultedValues(&req, &spec)
	spec.LocationSource = locationSource(&req, &spec)

	err = s.authorize(ctx, a, &spec, &req)
	if err != nil {
		return Job{}, false, err
	}

	// Fail closed on the gates that this version cannot evaluate. The caller
	// holds the start actions, so the answer names the gate. From here on a
	// refusal names the start action of the planned job.
	err = s.checkGates(ctx, unit)
	if err != nil {
		return Job{}, false, startRefusal(&spec, err)
	}

	j, created, err := s.create(ctx, a, source, &req, spec)
	if err != nil {
		return Job{}, false, startRefusal(&spec, err)
	}

	return j, created, nil
}

// locationSource returns the source of the effective code location of spec.
// A request that named the location decides. For a location that the server
// took from the settings, the planner names the namespace when the default
// connection of the namespace gave a value, and the source is the unit
// otherwise.
func locationSource(req *StartRequest, spec *Spec) LocationSource {
	if !req.Location.IsZero() {
		return LocationSourceRequest
	}

	if spec.LocationSource == LocationSourceNamespace {
		return LocationSourceNamespace
	}

	return LocationSourceUnit
}

// checkPlanned returns ErrInvalid when the spec that the planner built is for
// another unit, another operation or another purpose than the request.
//
// Defense in depth: the planner builds the spec, and the planner may change the
// unit, the operation, the purpose or the executor. The actions are checked
// again on the spec, so the planner can never widen what the caller may start.
func checkPlanned(unit identity.Object, spec *Spec, req *StartRequest) error {
	if spec.Unit.String() != unit.String() || spec.Unit.UnitID() != unit.UnitID() ||
		spec.Operation != req.Operation || spec.Purpose != req.Purpose {
		return fmt.Errorf("%w: the planner changed the unit, the operation or the purpose", ErrInvalid)
	}

	return nil
}

// Get implements Service. A caller without job:read on the unit of the job
// gets ErrNotFound, the same answer as for a job that does not exist.
func (s *service) Get(ctx context.Context, a Actor, id ID) (Job, error) {
	if !strings.HasPrefix(string(id), IDPrefix) {
		return Job{}, ErrNotFound
	}

	j, err := s.store.Get(ctx, id)
	if err != nil {
		return Job{}, fmt.Errorf("get job: %w", err)
	}

	err = s.authorizer.Authorize(ctx, a.Principal, ActionJobRead, j.Spec.Unit)

	switch {
	case err == nil:
		return j, nil
	case errors.Is(err, identity.ErrDenied):
		return Job{}, ErrNotFound
	default:
		return Job{}, fmt.Errorf("authorize %s: %w", ActionJobRead, err)
	}
}

// errNotYet is the cause of the methods that this version does not have.
var errNotYet = errors.New("the job service of this version does not do this yet")

// Cancel implements Service. This version refuses it.
func (*service) Cancel(context.Context, Actor, ID, string) (Job, error) {
	return Job{}, fmt.Errorf("%w: cancel: %w", ErrUnsupported, errNotYet)
}

// Approve implements Service. This version refuses it.
func (*service) Approve(context.Context, Actor, ID, Verdict, PlanBinding, string) (Job, error) {
	return Job{}, fmt.Errorf("%w: approve: %w", ErrUnsupported, errNotYet)
}

// Confirm implements Service. This version refuses it.
func (*service) Confirm(context.Context, Actor, ID, Verdict, PlanBinding) (Job, error) {
	return Job{}, fmt.Errorf("%w: confirm: %w", ErrUnsupported, errNotYet)
}

// RedactLog implements Service. This version refuses it.
func (*service) RedactLog(context.Context, Actor, ID, string, []Substitution, string) (LogRef, error) {
	return LogRef{}, fmt.Errorf("%w: redact log: %w", ErrUnsupported, errNotYet)
}
