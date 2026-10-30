package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/jobgate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
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
		Store: cfg.jobStore, Units: jobUnits{facts: facts}, Planner: jobPlanner{pools: cfg.runners, connections: nil},
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

// jobPlanner is the job.Planner of this version. It starts a plan on a
// runner, with the default steps of the tool of the unit.
//
// The pool comes from the request, and then the service has checked
// namespace:manage-runners, or it is the only job pool of the namespace. The
// code location comes from the request, and then the service has checked
// unit:define-steps. The server keeps no settings of a unit for jobs yet, so
// a request with no location fails. The settings of a unit take the place of
// both defaults.
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

	steps, err := stepsOf(unit, req.Purpose)
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
		Location: req.Location, Steps: steps, Pool: poolID, Profile: "", Edits: nil, Delivery: "",
		Defaulted: nil,
	}, nil
}

// checkPlanRequest returns the refusal of a request that this planner cannot
// start: another operation than a plan, or a refresh for a drift check, another
// executor than runners, and the fields of a write-back job.
func checkPlanRequest(req *job.StartRequest) error {
	driftCheck := req.Purpose == job.PurposeDriftCheck

	switch {
	case driftCheck && req.Operation != job.OperationRefresh:
		return fmt.Errorf("%w: a %s has the operation %s, not %s", job.ErrInvalid, req.Purpose,
			job.OperationRefresh, req.Operation)
	case !driftCheck && req.Operation != job.OperationPlan:
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

// stepsOf returns the fixed steps for the unit and the purpose.
func stepsOf(unit identity.Object, purpose job.Purpose) ([]job.Step, error) {
	if purpose != job.PurposeDriftCheck {
		return planSteps(unit)
	}

	return driftSteps(unit.Tool())
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
	tofuNoInput    = "-input=false"
	tofuAutomation = "TF_IN_AUTOMATION"
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
)

// planSteps returns the default steps of a plan for the tool of the unit. Only
// the tools with a plan step list here start a job.
func planSteps(unit identity.Object) ([]job.Step, error) {
	switch tool := unit.Tool(); tool {
	case pulumiTool:
		step, err := pulumiPreviewStep(unit)
		if err != nil {
			return nil, err
		}

		return []job.Step{step}, nil
	case opentofuTool:
		initStep := tofuStep(tofuInit, tofuInit, tofuNoInput)
		planStep := tofuStep(tofuPlan, tofuPlan, tofuNoInput, "-out=tfplan")
		planStep.Outputs = []job.Output{{Kind: job.OutputPlanApply, Path: "tfplan"}}

		return []job.Step{initStep, planStep}, nil
	default:
		return nil, fmt.Errorf("%w: this server has no default plan steps for the tool %q", job.ErrUnsupported, tool)
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

// pulumiPreviewStep returns the plan-phase step that previews the stack of
// unit. The stack goes in one argument with its flag (--stack=value), so a
// name never reads as a flag of its own, and in its full form (namespace,
// project, stack), so the Pulumi CLI never picks a stack from the checkout.
// The step changes no resource and leaves no output: the log of the step
// holds the preview.
func pulumiPreviewStep(unit identity.Object) (job.Step, error) {
	project, name, ok := strings.Cut(unit.UnitName(), "/")

	// Defense in depth: the unit object comes from a checked route and a
	// stored stack, and the Pulumi CLI refuses a bad name too. This check
	// stays so that a name that is not "project/stack" never reaches an
	// argument list.
	if !ok || project == "" || name == "" || strings.ContainsAny(name, "/\x00\r\n ") ||
		strings.ContainsAny(project, "\x00\r\n ") {
		return job.Step{}, fmt.Errorf("%w: the Pulumi stack name %q is not of the form project/stack", job.ErrInvalid,
			unit.UnitName())
	}

	stack := pulumiStackFlag + string(unit.Namespace()) + "/" + project + "/" + name

	return job.Step{
		Name: pulumiPreview, Role: job.RoleTool, Phase: job.PhasePlan,
		Args: []string{pulumiProgram, pulumiPreview, pulumiNonInteractive, stack},
		Env:  map[string]string{pulumiSkipUpdate: "true"}, Dir: "", Timeout: 0, AllowFailure: false,
		ChangesResources: false, Outputs: nil,
	}, nil
}
