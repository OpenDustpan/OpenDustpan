package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/jobgate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// newJobService builds the job service, or returns nil when the server lacks
// a port that the service needs: the job store, the inventory, the runner
// pools, the stores of the gates or the namespace service. A server without
// the service serves the read route of a job and no route that starts one.
//
//nolint:ireturn // The port is the contract of the result.
func newJobService(svc services, cfg *settings) (job.Starter, error) {
	gates, hasGates := cfg.namespaceStore.(gate.Store)

	// Fail closed: with no gate service the start cannot check the access
	// limits of the starter, so the server starts no job.
	if !hasGates || svc.gates == nil || lacksJobPorts(svc, cfg) {
		return nil, nil //nolint:nilnil // No job service: the server lacks a port that it needs.
	}

	facts := newInventoryFacts(cfg)

	decider, err := jobgate.New(jobgate.Config{Config: gates, Members: facts})
	if err != nil {
		return nil, fmt.Errorf("job decider: %w", err)
	}

	executor, err := runner.NewExecutor(cfg.runners)
	if err != nil {
		return nil, fmt.Errorf("runner executor: %w", err)
	}

	log := cfg.logger
	if log == nil {
		log = slog.Default()
	}

	var freezes job.FreezeStore
	if f, ok := cfg.jobStore.(job.FreezeStore); ok {
		freezes = f
	}

	starter, svcErr := job.NewService(job.ServiceConfig{
		Store: cfg.jobStore, Units: jobUnits{facts: facts}, Planner: newJobPlanner(cfg),
		Executors: []job.Executor{executor}, Decider: decider,
		// The access limits of the starter hold for the start action, as for a
		// call of any route. The limits of the lock of the plan are evaluated
		// for the job token, so this is the only check of the person.
		Authorizer: unitLimits{grants: cfg.authorizer, limited: gate.LimitedAuthorizer(cfg.authorizer, svc.gates)},
		Gates:      jobGates{snapshots: gates, members: facts},
		Fences:     jobFences{namespaces: svc.namespaces}, Freezes: freezes, Switches: nil, Now: cfg.now, NewID: nil,
		Log: log,
	})
	if svcErr != nil {
		return nil, fmt.Errorf("job service: %w", svcErr)
	}

	return starter, nil
}

// newJobReconciler builds the reconcile of the job model, or returns nil when
// the server lacks the job store or the namespace service. A server without a
// durable store runs no reconcile, because the schedule job.reconcile is a
// durable schedule.
func newJobReconciler(svc services, cfg *settings) (*job.Reconciler, error) {
	if cfg.jobStore == nil || svc.namespaces == nil || cfg.durableStore == nil {
		return nil, nil //nolint:nilnil // No reconcile: the server lacks a port that it needs.
	}

	log := cfg.logger
	if log == nil {
		log = slog.Default()
	}

	r, err := job.NewReconciler(job.ReconcilerConfig{
		Store:           cfg.jobStore,
		Namespaces:      jobNamespaces{namespaces: svc.namespaces},
		QueueTimeout:    0,
		DispatchTimeout: 0,
		StateTimeout:    0,
		MaxPlanAge:      0,
		Approvers: gate.ApproverPolicy{
			Production: cfg.installationKind != installation.KindDev,
			IsDev:      principal.IsDevSubject,
		},
		Batch: 0,
		Now:   cfg.now,
		Log:   log,
	})
	if err != nil {
		return nil, fmt.Errorf("job reconcile: %w", err)
	}

	return r, nil
}

// jobNamespaces is the job.NamespaceReader over the namespace service.
type jobNamespaces struct {
	namespaces *namespace.Service
}

// GetNamespace implements job.NamespaceReader.
func (n jobNamespaces) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	got, err := n.namespaces.Get(ctx, name)
	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("namespace %s: %w", name, err)
	}

	return got, nil
}

// lacksJobPorts reports whether the server lacks a port that the job service
// needs, apart from the stores of the gates.
func lacksJobPorts(svc services, cfg *settings) bool {
	return cfg.jobStore == nil || cfg.inventoryReader == nil || cfg.runners == nil || svc.namespaces == nil ||
		cfg.authorizer == nil
}

// newInventoryFacts returns the lookup of units in the inventory, with the
// optional ports of the store.
func newInventoryFacts(cfg *settings) inventoryFacts {
	facts := inventoryFacts{reader: cfg.inventoryReader, ids: nil, deleted: nil}
	if deleted, ok := cfg.namespaceStore.(inventory.DeletedFactsReader); ok {
		facts.deleted = deleted
	}

	if ids, ok := cfg.namespaceStore.(unitIDs); ok {
		facts.ids = ids
	}

	return facts
}

// jobUnits is the job.UnitResolver over the inventory.
type jobUnits struct {
	facts inventoryFacts
}

// Unit implements job.UnitResolver. It finds a live unit only: a deleted
// unit is job.ErrNotFound, so no job starts on it.
func (u jobUnits) Unit(ctx context.Context, ref identity.Object) (identity.Object, error) {
	// The reference of a caller never carries a unit ID. Defense in depth: the
	// lookup below trusts an ID when it sees one, so this resolver drops the
	// ID that a caller might send and finds its own.
	bare, err := identity.UnitObject(ref.Tool(), ref.Namespace(), ref.UnitName())
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %w", job.ErrInvalid, err)
	}

	e, err := u.facts.entry(ctx, bare)

	switch {
	case errors.Is(err, gate.ErrNotFound):
		return identity.Object{}, job.ErrNotFound
	case err != nil:
		return identity.Object{}, fmt.Errorf("find unit %s: %w", bare, err)
	case e.UnitID == "":
		return identity.Object{}, fmt.Errorf("%w: the inventory holds unit %s with no ID", errUnitMismatch, bare)
	}

	return bare.WithUnitID(e.UnitID), nil
}

// unitLimits is the identity.Authorizer of the job service: the access limits
// hold for an action on a unit, and the grants alone decide an action on any
// other object. The access limits are conditions on the facts of a unit, and
// the gate service refuses a limit check for another object with an error
// that would deny the action on the namespace for every caller. A request
// that names a pool needs namespace:manage-runners, and no limit can apply to
// it yet.
type unitLimits struct {
	grants  identity.Authorizer
	limited identity.Authorizer
}

// Authorize implements identity.Authorizer.
func (a unitLimits) Authorize(ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if obj.IsUnit() {
		return a.limited.Authorize(ctx, p, action, obj)
	}

	return a.grants.Authorize(ctx, p, action, obj)
}

// ListObjects implements identity.Authorizer. It gives the scope of the
// grants, as LimitedAuthorizer does.
func (a unitLimits) ListObjects(ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	return a.limited.ListObjects(ctx, p, action)
}

// gateSnapshots reads the configuration of a chain of scopes. gate.Store
// implements it.
type gateSnapshots interface {
	Snapshot(ctx context.Context, scopes []gate.Scope) (gate.Config, error)
}

// jobGates is the job.GateGuard over the gate store.
type jobGates struct {
	snapshots gateSnapshots
	members   gate.Membership
}

// Enforced implements job.GateGuard. It lists the gates at change.propose and
// state.write on the scope chain of the unit that are not in evaluate mode.
// A gate in error, or in a state that this code does not know, counts as
// enforced: the answer is a refusal, never a skip.
func (g jobGates) Enforced(ctx context.Context, unit identity.Object) ([]job.EnforcedGate, error) {
	coll, err := g.members.Collection(ctx, unit)

	switch {
	case errors.Is(err, gate.ErrNotFound):
		coll = ""
	case err != nil:
		return nil, fmt.Errorf("collection of %s: %w", unit, err)
	default:
	}

	chain, err := gate.UnitChain(unit, coll)
	if err != nil {
		return nil, fmt.Errorf("scope chain of %s: %w", unit, err)
	}

	cfg, err := g.snapshots.Snapshot(ctx, chain)
	if err != nil {
		return nil, fmt.Errorf("gates of %s: %w", unit, err)
	}

	var enforced []job.EnforcedGate

	for i := range cfg.Gates {
		gt := &cfg.Gates[i]

		if gt.DecisionPoint != gate.PointChangePropose && gt.DecisionPoint != gate.PointStateWrite {
			continue
		}

		if gt.Mode == gate.ModeEvaluate {
			continue
		}

		enforced = append(enforced, job.EnforcedGate{
			Scope: gt.Scope.Key(), ID: string(gt.ID), Point: string(gt.DecisionPoint),
		})
	}

	return enforced, nil
}

// jobFences is the job.FenceSource over the namespace service.
type jobFences struct {
	namespaces *namespace.Service
}

// Fence implements job.FenceSource.
func (f jobFences) Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := f.namespaces.Get(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}

// connectionOwner tells whether a forge connection belongs to a namespace.
type connectionOwner interface {
	// Owns returns true when namespace ns holds the connection id. It
	// returns false, and no error, for a connection of another namespace and
	// for one that does not exist.
	Owns(ctx context.Context, ns namespace.Name, id string) (bool, error)
}

// forgeConnections reads the Git host connections of a namespace. forge.Store
// implements it.
type forgeConnections interface {
	GetConnection(ctx context.Context, ns namespace.Name, id string) (forge.ConnectionRecord, error)
	ListConnections(ctx context.Context, ns namespace.Name) ([]forge.ConnectionRecord, error)
}

// storeConnections is the connectionOwner over the Git host connections.
type storeConnections struct {
	connections forgeConnections
}

// Owns implements connectionOwner.
func (c storeConnections) Owns(ctx context.Context, ns namespace.Name, id string) (bool, error) {
	rec, err := c.connections.GetConnection(ctx, ns, id)

	switch {
	case errors.Is(err, forge.ErrConnectionNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("connection of %s: %w", ns, err)
	}

	// Defense in depth: the store reads the connections of ns only. This check
	// stays so that a store that gives a connection of another namespace never
	// gives access to it.
	return rec.Namespace == ns && rec.ID == id, nil
}

// newJobPlanner returns the planner of the job service. With the store of unit
// settings, it reads the effective settings of a unit when a request names no
// location or no pool. Without the store, a request must name both.
//
//nolint:ireturn // The port is the contract of the result.
func newJobPlanner(cfg *settings) job.Planner {
	var owner connectionOwner

	var conns forgeConnections

	if cfg.gitHost.store != nil {
		conns = cfg.gitHost.store
		owner = storeConnections{connections: conns}
	}

	base := jobPlanner{pools: cfg.runners, connections: owner}
	if cfg.unitSettingsStore == nil {
		return base
	}

	return settingsPlanner{base: base, settings: cfg.unitSettingsStore, connections: conns}
}

// settingsPlanner is the job.Planner that fills the code location and the pool
// of a request from the effective settings of the unit, and then plans as
// jobPlanner does. A value in the request wins over a setting.
type settingsPlanner struct {
	base     jobPlanner
	settings unitsettings.Store
	// connections gives the default connection of the namespace. Nil means the
	// server keeps no connections, so the namespace has no default.
	connections forgeConnections
}

// Plan implements job.Planner.
func (p settingsPlanner) Plan(ctx context.Context, unit identity.Object, req *job.StartRequest) (job.Spec, error) {
	if !req.Location.IsZero() && req.Pool != "" {
		return p.base.Plan(ctx, unit, req)
	}

	eff, err := p.effectiveFor(ctx, unit, req.Location)
	if err != nil {
		return job.Spec{}, err
	}

	filled := *req
	source := fillLocation(&filled, eff)

	if filled.Pool == "" && eff.Pool.Value != "" {
		name, nameErr := p.poolName(ctx, unit.Namespace(), eff.Pool.Value)
		if nameErr != nil {
			return job.Spec{}, nameErr
		}

		filled.Pool = name
	}

	spec, err := p.base.Plan(ctx, unit, &filled)
	if err != nil {
		return job.Spec{}, err
	}

	spec.LocationSource = source

	return spec, nil
}

// effectiveFor returns the effective settings of unit. It reads the default
// connection of the namespace when the request names no code location.
func (p settingsPlanner) effectiveFor(
	ctx context.Context, unit identity.Object, loc job.Location,
) (unitsettings.Effective, error) {
	var defaultConn string

	if loc.IsZero() {
		var err error

		defaultConn, err = p.defaultConnection(ctx, unit.Namespace())
		if err != nil {
			return unitsettings.Effective{}, err
		}
	}

	return p.effective(ctx, unit, defaultConn)
}

// fillLocation sets the effective code location of req when the request names
// none, and returns the source of the location. A request that names the
// location gives the source request.
func fillLocation(req *job.StartRequest, eff unitsettings.Effective) job.LocationSource {
	if !req.Location.IsZero() || eff.Repository.Value == "" {
		return job.LocationSourceRequest
	}

	req.Location = job.Location{
		Connection: eff.Connection.Value, Repository: eff.Repository.Value, Ref: eff.Ref.Value,
		Directory: "", Revision: "",
	}

	if eff.Directory.Origin == unitsettings.OriginUnit {
		req.Location.Directory = eff.Directory.Value
	}

	if eff.Connection.Origin == unitsettings.OriginNamespaceDefault && eff.Connection.Value != "" {
		return job.LocationSourceNamespace
	}

	return job.LocationSourceUnit
}

// effective returns the effective settings of unit. It fails closed: a store
// that fails, or a record that the store refuses, gives an error and no job.
func (p settingsPlanner) effective(ctx context.Context, unit identity.Object, defaultConn string,
) (unitsettings.Effective, error) {
	var none unitsettings.Effective

	rec, err := p.settings.Get(ctx, unit)

	var current *unitsettings.Settings

	switch {
	case errors.Is(err, unitsettings.ErrNotFound):
	case err != nil:
		return none, fmt.Errorf("settings of %s: %w", unit, err)
	default:
		current = &rec
	}

	defaults, err := p.settings.Defaults(ctx, unit.Namespace())
	if err != nil {
		return none, fmt.Errorf("unit defaults of %s: %w", unit.Namespace(), err)
	}

	return unitsettings.Resolve(current, defaults, unitsettings.Context{
		DefaultConnection: defaultConn, HasJobPool: true, DefaultFiles: nil,
	}), nil
}

// defaultConnection returns the ID of the default connection of ns, or empty.
func (p settingsPlanner) defaultConnection(ctx context.Context, ns namespace.Name) (string, error) {
	if p.connections == nil {
		return "", nil
	}

	recs, err := p.connections.ListConnections(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("connections of %s: %w", ns, err)
	}

	for i := range recs {
		if recs[i].Default && recs[i].Namespace == ns {
			return recs[i].ID, nil
		}
	}

	return "", nil
}

// poolName returns the name of the job pool of ns with the ID id. A setting
// that names a pool which does not exist gives job.ErrInvalid.
func (p settingsPlanner) poolName(ctx context.Context, ns namespace.Name, id string) (string, error) {
	pools, err := p.base.pools.Pools(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("pools of %s: %w", ns, err)
	}

	i := slices.IndexFunc(pools, func(pl runner.Pool) bool {
		return pl.ID == id && pl.Namespace == ns && pl.Purpose == runner.PurposeJobs
	})
	if i < 0 {
		return "", fmt.Errorf("%w: the pool in the settings of the unit is not a runner pool for jobs in namespace %s",
			job.ErrInvalid, ns)
	}

	return pools[i].Name, nil
}

// jobPlanner is the job.Planner of this version. It starts a plan, or a drift
// check, on a runner, with the default steps of the tool of the unit. It
// refuses an apply and a destroy until the runner protocol stores the plan of
// the plan phase and gives it to the apply phase.
//
// The pool comes from the request, and then the service has checked
// namespace:manage-runners, or it is the only job pool of the namespace. The
// code location comes from the request, and then the service has checked
// unit:define-steps. A request with no location fails. settingsPlanner fills
// both values from the settings of the unit before this planner runs.
type jobPlanner struct {
	pools runner.PoolStore
	// connections checks the connection that a location names. Nil means the
	// server keeps no forge connections, so a location with a connection fails
	// closed.
	connections connectionOwner
}

// Plan implements job.Planner.
func (p jobPlanner) Plan(ctx context.Context, unit identity.Object, req *job.StartRequest) (job.Spec, error) {
	err := checkPlanRequest(req)
	if err != nil {
		return job.Spec{}, err
	}

	steps, err := stepsOf(unit, req.Operation, req.Purpose)
	if err != nil {
		return job.Spec{}, err
	}

	err = p.checkLocation(ctx, unit.Namespace(), req.Location)
	if err != nil {
		return job.Spec{}, err
	}

	poolID, err := p.poolID(ctx, unit.Namespace(), req.Pool)
	if err != nil {
		return job.Spec{}, err
	}

	return job.Spec{
		Unit: unit, Operation: req.Operation, Purpose: req.Purpose, Executor: job.ExecutorRunner,
		Location: req.Location, LocationSource: job.LocationSourceRequest, Steps: steps, Pool: poolID, Profile: "",
		Edits: nil, Delivery: "", Defaulted: nil,
	}, nil
}

// checkPlanRequest returns the refusal of a request that this planner cannot
// start: an operation other than a plan (an apply and a destroy included), or
// a refresh for a drift check, another executor than runners, and the fields
// of a write-back job.
func checkPlanRequest(req *job.StartRequest) error {
	driftCheck := req.Purpose == job.PurposeDriftCheck

	switch {
	case driftCheck && req.Operation != job.OperationRefresh:
		return fmt.Errorf("%w: a %s has the operation %s, not %s", job.ErrInvalid, req.Purpose,
			job.OperationRefresh, req.Operation)
	case !driftCheck && !startsOnRunner(req.Operation):
		return fmt.Errorf("%w: this server starts plan jobs only, not %s", job.ErrUnsupported, req.Operation)
	case req.Executor != "" && req.Executor != job.ExecutorRunner:
		return fmt.Errorf("%w: this server starts jobs on runners only, not through %s", job.ErrUnsupported,
			req.Executor)
	case len(req.Edits) > 0 || req.Delivery != "":
		return fmt.Errorf("%w: edits and a delivery are for write-back jobs", job.ErrInvalid)
	default:
		return nil
	}
}

// startsOnRunner reports whether this server starts a job of op on a runner,
// apart from a drift check. An apply and a destroy are not started yet: the
// runner protocol does not store the plan of the plan phase or give it to the
// apply phase, so such a job would plan again with no end. The steps of both
// operations (applySteps, destroySteps) stay for the change that lifts this
// refusal.
func startsOnRunner(op job.Operation) bool {
	return op == job.OperationPlan
}

// stepsOf returns the fixed steps for the unit, the operation and the purpose.
func stepsOf(unit identity.Object, op job.Operation, purpose job.Purpose) ([]job.Step, error) {
	if purpose == job.PurposeDriftCheck {
		return driftSteps(unit.Tool())
	}

	switch op {
	case job.OperationApply:
		return applySteps(unit)
	case job.OperationDestroy:
		return destroySteps(unit)
	case job.OperationPlan:
		return planSteps(unit)
	case job.OperationRefresh:
		// Defense in depth: checkPlanRequest refuses a refresh that is no drift
		// check. This case stays so that a new caller of stepsOf gets no steps
		// for it, and it does not rely on that check.
		return nil, fmt.Errorf("%w: this server has no default steps for the operation %s", job.ErrUnsupported, op)
	default:
		return nil, fmt.Errorf("%w: unknown operation %q", job.ErrInvalid, op)
	}
}

// checkLocation checks the code location of a start in the namespace ns.
func (p jobPlanner) checkLocation(ctx context.Context, ns namespace.Name, loc job.Location) error {
	if loc.IsZero() {
		return fmt.Errorf("%w: the unit has no code location; name one in the request", job.ErrUnsupported)
	}

	err := loc.Validate()
	if err != nil {
		return fmt.Errorf("location: %w", err)
	}

	if loc.Connection == "" {
		return nil
	}

	// Defense in depth: the connection decides which credentials fetch the
	// code. A connection of another namespace, a connection that does not
	// exist and a server that keeps no connections all give the answer of a
	// refused unit, so the answer is no oracle for the connections of other
	// namespaces. This check does not rely on the actions of the service.
	if p.connections == nil {
		return fmt.Errorf("%w: the connection cannot be used", identity.ErrDenied)
	}

	owns, err := p.connections.Owns(ctx, ns, loc.Connection)
	if err != nil {
		return fmt.Errorf("connection of %s: %w", ns, err)
	}

	if !owns {
		return fmt.Errorf("%w: the connection cannot be used", identity.ErrDenied)
	}

	return nil
}

// poolID returns the ID of the pool of ns with the name name, or the ID of
// the only pool of ns that runs jobs when name is empty.
func (p jobPlanner) poolID(ctx context.Context, ns namespace.Name, name string) (string, error) {
	pools, err := p.pools.Pools(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("pools of %s: %w", ns, err)
	}

	// Defense in depth: the store lists the pools of ns only, and the runner
	// executor checks the namespace of the pool too. This filter stays so
	// that a store that lists a pool of another namespace never gives its ID.
	pools = slices.DeleteFunc(slices.Clone(pools), func(pl runner.Pool) bool {
		return pl.Namespace != ns || pl.Purpose != runner.PurposeJobs
	})

	if name == "" {
		switch len(pools) {
		case 1:
			return pools[0].ID, nil
		case 0:
			// The next step: the route that makes a pool, and the setting
			// without which the server does not serve that route. The text
			// holds no name, so an API answer that keeps 300 bytes of it
			// always holds the whole step.
			return "", fmt.Errorf("%w: the namespace has no runner pool for jobs: make one with POST "+
				"/api/runners/{namespace}/pools and the purpose jobs (this route needs DUSTPAN_PUBLIC_URL)",
				job.ErrUnsupported)
		default:
			return "", fmt.Errorf("%w: the namespace has more than one runner pool for jobs, so there is no "+
				"default pool", job.ErrUnsupported)
		}
	}

	i := slices.IndexFunc(pools, func(pl runner.Pool) bool { return pl.Name == name })
	if i < 0 {
		return "", fmt.Errorf("%w: namespace %s has no runner pool %q for jobs", job.ErrInvalid, ns, name)
	}

	return pools[i].ID, nil
}

// tofuProgram is the program of the plan steps of an OpenTofu workspace. The runner
// finds it on its PATH.
const tofuProgram = "tofu"

// The words of the OpenTofu steps that this server starts.
const (
	tofuInit       = "init"
	tofuPlan       = "plan"
	tofuApply      = "apply"
	tofuNoInput    = "-input=false"
	tofuDestroy    = "-destroy"
	tofuAutomation = "TF_IN_AUTOMATION"
	// tofuPlanFile is the plan file that the plan phase writes and the apply
	// phase reads, relative to the code directory.
	tofuPlanFile = "tfplan"
)

// pulumiProgram is the program of the plan steps of a Pulumi stack. The runner
// finds it on its PATH.
const pulumiProgram = "pulumi"

// The words of the Pulumi steps that this server starts.
const (
	pulumiPreview        = "preview"
	pulumiNonInteractive = "--non-interactive"
	pulumiStackFlag      = "--stack="
	pulumiSkipUpdate     = "PULUMI_SKIP_UPDATE_CHECK"
	envTrue              = "true"
	pulumiUp             = "up"
	pulumiDestroy        = "destroy"
	pulumiYes            = "--yes"
	pulumiSkipPreview    = "--skip-preview"
	pulumiPreviewOnly    = "--preview-only"
	// The Pulumi CLI saves and reads an update plan only with this variable
	// set.
	pulumiExperimental = "PULUMI_EXPERIMENTAL"
	// pulumiPlanFile is the update plan that the preview saves and the update
	// reads, relative to the code directory.
	pulumiPlanFile = "pulumi-plan.json"
)

// maxPulumiName is the most bytes of the organization, the project or the
// stack of a Pulumi stack in an argument.
const maxPulumiName = 100

// validPulumiName reports whether s is on the allowlist of the organization,
// the project and the stack of a Pulumi stack in an argument: 1 to
// maxPulumiName ASCII letters, digits, '_', '.' and '-', with a letter, a
// digit or '_' first, so a segment is never a flag, '.' or '..'.
func validPulumiName(s string) bool {
	if s == "" || len(s) > maxPulumiName || !isPulumiNameStart(s[0]) {
		return false
	}

	for i := 1; i < len(s); i++ {
		if c := s[i]; !isPulumiNameStart(c) && c != '.' && c != '-' {
			return false
		}
	}

	return true
}

// isPulumiNameStart reports whether c may start a segment of a Pulumi stack:
// an ASCII letter, a digit or '_'.
func isPulumiNameStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// planSteps returns the default steps of a plan for the tool of the unit. Only
// the tools with a plan step list here start a job.
func planSteps(unit identity.Object) ([]job.Step, error) {
	switch tool := unit.Tool(); tool {
	case pulumiTool:
		stack, err := pulumiStackArg(unit)
		if err != nil {
			return nil, err
		}

		return []job.Step{pulumiPreviewStep(stack, pulumiPreview)}, nil
	case opentofuTool:
		return tofuPlanSteps(), nil
	default:
		return nil, fmt.Errorf("%w: this server has no default plan steps for the tool %q", job.ErrUnsupported, tool)
	}
}

// applySteps returns the default steps of an apply for the tool of the unit:
// the steps of the plan, then the steps that apply the plan of the plan phase.
//
// OpenTofu applies the plan file with no -auto-approve: a plan file needs no
// approval, and the apply fails when the state changed after the plan. Pulumi
// saves the plan in the preview and applies it with --plan.
func applySteps(unit identity.Object) ([]job.Step, error) {
	switch tool := unit.Tool(); tool {
	case pulumiTool:
		stack, err := pulumiStackArg(unit)
		if err != nil {
			return nil, err
		}

		// The preview of an apply saves the plan that the apply phase reads.
		preview := pulumiPreviewStep(stack, pulumiPreview)
		preview.Args = append(preview.Args, "--save-plan="+pulumiPlanFile)
		preview.Env[pulumiExperimental] = envTrue
		preview.Outputs = []job.Output{{Kind: job.OutputPlanApply, Path: pulumiPlanFile}}

		update := pulumiApplyStep(pulumiUp, pulumiProgram, pulumiUp, pulumiNonInteractive, stack,
			"--plan="+pulumiPlanFile, pulumiYes, pulumiSkipPreview)
		update.Env[pulumiExperimental] = envTrue

		return []job.Step{preview, update}, nil
	case opentofuTool:
		return tofuApplySteps(), nil
	default:
		return nil, fmt.Errorf("%w: this server has no default apply steps for the tool %q", job.ErrUnsupported, tool)
	}
}

// destroySteps returns the default steps of a destroy for the tool of the unit.
//
// OpenTofu saves a destroy plan (plan -destroy) and applies it. The Pulumi CLI
// cannot save a destroy plan, so the plan phase previews the destroy
// (destroy --preview-only) and stores no plan file, and the apply phase runs
// destroy --yes. The destroy then acts on the state at the time of the apply,
// not on the previewed plan.
func destroySteps(unit identity.Object) ([]job.Step, error) {
	switch tool := unit.Tool(); tool {
	case pulumiTool:
		stack, err := pulumiStackArg(unit)
		if err != nil {
			return nil, err
		}

		preview := pulumiPreviewStep(stack, pulumiDestroy, pulumiPreviewOnly)
		destroy := pulumiApplyStep(pulumiDestroy, pulumiProgram, pulumiDestroy, pulumiNonInteractive, stack,
			pulumiYes, pulumiSkipPreview)

		return []job.Step{preview, destroy}, nil
	case opentofuTool:
		return tofuApplySteps(tofuDestroy), nil
	default:
		return nil, fmt.Errorf("%w: this server has no default destroy steps for the tool %q", job.ErrUnsupported, tool)
	}
}

// tofuPlanSteps returns the plan-phase steps of OpenTofu: init, then a plan
// that writes the plan file. flags go after the word plan: tofuDestroy makes
// a destroy plan.
func tofuPlanSteps(flags ...string) []job.Step {
	initStep := tofuStep(tofuInit, tofuInit, tofuNoInput)

	args := append([]string{tofuPlan}, flags...)
	args = append(args, tofuNoInput, "-out="+tofuPlanFile)

	planStep := tofuStep(tofuPlan, args...)
	planStep.Outputs = []job.Output{{Kind: job.OutputPlanApply, Path: tofuPlanFile}}

	return []job.Step{initStep, planStep}
}

// tofuApplySteps returns the steps of an OpenTofu apply or destroy: the plan
// phase, then an init and an apply of the plan file in the apply phase. The
// apply phase runs on a fresh checkout, so it runs init again.
func tofuApplySteps(flags ...string) []job.Step {
	steps := tofuPlanSteps(flags...)

	initStep := tofuStep("apply-init", tofuInit, tofuNoInput)
	initStep.Phase = job.PhaseApply

	applyStep := tofuStep(tofuApply, tofuApply, tofuNoInput, tofuPlanFile)
	applyStep.Phase = job.PhaseApply
	applyStep.ChangesResources = true

	return append(steps, initStep, applyStep)
}

// pulumiApplyStep returns an apply-phase step that runs the Pulumi program
// with args. It marks the step as one that changes resources.
func pulumiApplyStep(name string, args ...string) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: job.PhaseApply, Args: args,
		Env: map[string]string{pulumiSkipUpdate: envTrue}, Dir: "", Timeout: 0, AllowFailure: false,
		ChangesResources: true, Outputs: nil,
	}
}

// driftSteps returns the fixed steps of a drift check for the tool: a
// refresh-only plan. It sets no variable, passes no argument that a caller
// chose, and keeps the default state lock: no -lock=false, so a check that
// cannot get the lock fails and the last result stays. The plan file stays in
// the checkout of the runner, which a later step reduces to a summary.
func driftSteps(tool string) ([]job.Step, error) {
	switch tool {
	case opentofuTool:
		return []job.Step{
			tofuStep(tofuInit, tofuInit, tofuNoInput),
			tofuStep(tofuPlan, tofuPlan, "-refresh-only", tofuNoInput, "-out=drift.tfplan"),
		}, nil
	default:
		return nil, fmt.Errorf("%w: this server has no drift check steps for the tool %q", job.ErrUnsupported, tool)
	}
}

// tofuStep returns a plan-phase tool step that runs the OpenTofu program with
// args. It changes no resource and leaves no output.
func tofuStep(name string, args ...string) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: job.PhasePlan, Args: append([]string{tofuProgram}, args...),
		Env: map[string]string{tofuAutomation: "1"}, Dir: "", Timeout: 0, AllowFailure: false,
		ChangesResources: false, Outputs: nil,
	}
}

// pulumiPreviewStep returns the plan-phase step that previews the stack
// argument stack (see pulumiStackArg). command is the command and its flags:
// preview, or destroy with --preview-only. The step changes no resource and
// leaves no output: the log of the step holds the preview.
func pulumiPreviewStep(stack string, command ...string) job.Step {
	args := append([]string{pulumiProgram}, command...)
	args = append(args, pulumiNonInteractive, stack)

	return job.Step{
		Name: pulumiPreview, Role: job.RoleTool, Phase: job.PhasePlan, Args: args,
		Env: map[string]string{pulumiSkipUpdate: envTrue}, Dir: "", Timeout: 0, AllowFailure: false,
		ChangesResources: false, Outputs: nil,
	}
}

// pulumiStackArg returns the argument that names the stack of unit. The stack
// goes in one argument with its flag (--stack=value), so a name never reads as
// a flag of its own, and in its full form (namespace, project, stack), so the
// Pulumi CLI never picks a stack from the checkout.
func pulumiStackArg(unit identity.Object) (string, error) {
	project, name, _ := strings.Cut(unit.UnitName(), "/")

	// Defense in depth: this allowlist stays so that a new caller of the
	// planner cannot put a hostile name in an argument. It does not rely on
	// the other layers: identity.UnitObject checks the unit name, the route
	// and the stored stack check the name when the stack is made, the argument
	// list passes the name to the program with no shell, and the Pulumi CLI
	// checks the name too.
	for _, part := range []string{string(unit.Namespace()), project, name} {
		if !validPulumiName(part) {
			return "", fmt.Errorf("%w: the Pulumi stack %s in the namespace %s is not of the form "+
				"project/stack with letters, digits, '_', '.' and '-'", job.ErrInvalid, ident.Echo(unit.UnitName()),
				ident.Echo(string(unit.Namespace())))
		}
	}

	return pulumiStackFlag + string(unit.Namespace()) + "/" + project + "/" + name, nil
}

// newJobWorkers builds the periodic work of the job model: the reconcile and
// the sweep of the artifact blobs. Each is nil when the server lacks a port
// that it needs.
func newJobWorkers(svc services, cfg *settings) (*job.Reconciler, *job.ArtifactSweeper, error) {
	reconciler, err := newJobReconciler(svc, cfg)
	if err != nil {
		return nil, nil, err
	}

	sweeper, enabled, err := newArtifactSweeper(svc, cfg)
	if err != nil || !enabled {
		return reconciler, nil, err
	}

	return reconciler, sweeper, nil
}

// newArtifactSweeper builds the sweep of the plan and log blobs of jobs, the
// schedule job.artifact-sweep. It returns false when the server lacks a port
// that the sweep needs: the job store, the durable store, the blob store or
// the namespace service. A job store that cannot read the names of the
// artifact blobs, or a store that cannot tell whether a backup runs, logs a
// warning and runs no sweep, so the server deletes no artifact blob.
func newArtifactSweeper(svc services, cfg *settings) (*job.ArtifactSweeper, bool, error) {
	if cfg.jobStore == nil || svc.namespaces == nil || cfg.durableStore == nil || cfg.blobStore == nil {
		return nil, false, nil
	}

	log := cfg.logger
	if log == nil {
		log = slog.Default()
	}

	ports, ok := artifactSweepPortsOf(cfg, log)
	if !ok {
		return nil, false, nil
	}

	refusals, err := blobRefusals(cfg, job.ArtifactBlobFamily())
	if err != nil {
		return nil, false, err
	}

	blobs, err := job.NewArtifactBlobs(cfg.blobStore, refusals)
	if err != nil {
		return nil, false, fmt.Errorf("job artifact blobs: %w", err)
	}

	s, err := job.NewArtifactSweeper(job.ArtifactSweeperConfig{
		Jobs: cfg.jobStore, Names: ports.names, Blobs: blobs, Namespaces: jobNamespaces{namespaces: svc.namespaces},
		Guard: ports.guard, Clock: ports.clock, Operations: cfg.durableStore, Audit: ports.entries, Logger: log,
		Now: cfg.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("job artifact sweep: %w", err)
	}

	return s, true, nil
}

// artifactSweepPorts are the ports of the stores that the artifact sweep needs.
type artifactSweepPorts struct {
	names   job.ArtifactKeyStore
	guard   blobguard.Watch
	clock   blob.DatabaseClock
	entries audit.ServerStore
}

// artifactSweepPortsOf returns the ports of the stores of cfg that the artifact
// sweep needs. It logs a warning and returns false when a store lacks one.
func artifactSweepPortsOf(cfg *settings, log *slog.Logger) (artifactSweepPorts, bool) {
	var p artifactSweepPorts

	var ok bool

	if p.names, ok = cfg.jobStore.(job.ArtifactKeyStore); !ok {
		log.Warn("the job store cannot read the names of the artifact blobs: the server deletes no artifact blob")

		return p, false
	}

	if p.guard, ok = cfg.namespaceStore.(blobguard.Watch); !ok {
		log.Warn("the store cannot tell whether a backup or a restore runs: the server deletes no artifact blob")

		return p, false
	}

	if p.clock, ok = cfg.namespaceStore.(blob.DatabaseClock); !ok {
		log.Warn("the store cannot read the clock of the database: the server deletes no artifact blob")

		return p, false
	}

	if p.entries, ok = cfg.namespaceStore.(audit.ServerStore); !ok {
		log.Warn("the store cannot append entries of the scope of the server: the server deletes no artifact blob")

		return p, false
	}

	return p, true
}
