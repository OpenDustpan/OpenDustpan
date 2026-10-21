package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/jobgate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	if cfg.jobStore == nil || cfg.inventoryReader == nil || cfg.runners == nil || !hasGates ||
		svc.namespaces == nil || cfg.authorizer == nil {
		return nil, nil //nolint:nilnil // No job service: the server lacks a store that it needs.
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
		Store: cfg.jobStore, Units: jobUnits{facts: facts}, Planner: jobPlanner{pools: cfg.runners},
		Executors: []job.Executor{executor}, Decider: decider, Authorizer: cfg.authorizer,
		Fences: jobFences{namespaces: svc.namespaces}, Freezes: freezes, Switches: nil, Now: cfg.now, NewID: nil,
		Log: log,
	})
	if svcErr != nil {
		return nil, fmt.Errorf("job service: %w", svcErr)
	}

	return starter, nil
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

// jobPlanner is the job.Planner of this version. It starts a plan on a
// runner, with the default steps of the tool of the unit. The pool comes from
// the request, because the server keeps no settings of a unit for jobs yet.
type jobPlanner struct {
	pools runner.PoolStore
}

// Plan implements job.Planner.
func (p jobPlanner) Plan(ctx context.Context, unit identity.Object, req *job.StartRequest) (job.Spec, error) {
	if req.Operation != job.OperationPlan {
		return job.Spec{}, fmt.Errorf("%w: this server starts plan jobs only, not %s", job.ErrUnsupported,
			req.Operation)
	}

	if req.Executor != "" && req.Executor != job.ExecutorRunner {
		return job.Spec{}, fmt.Errorf("%w: this server starts jobs on runners only, not through %s",
			job.ErrUnsupported, req.Executor)
	}

	if len(req.Edits) > 0 || req.Delivery != "" {
		return job.Spec{}, fmt.Errorf("%w: edits and a delivery are for write-back jobs", job.ErrInvalid)
	}

	steps, err := planSteps(unit.Tool())
	if err != nil {
		return job.Spec{}, err
	}

	err = req.Location.Validate()
	if err != nil {
		return job.Spec{}, fmt.Errorf("location: %w", err)
	}

	poolID, err := p.poolID(ctx, unit.Namespace(), req.Pool)
	if err != nil {
		return job.Spec{}, err
	}

	return job.Spec{
		Unit: unit, Operation: job.OperationPlan, Executor: job.ExecutorRunner, Location: req.Location,
		Steps: steps, Pool: poolID, Profile: "", Edits: nil, Delivery: "",
	}, nil
}

// poolID returns the ID of the pool of ns with the name name.
func (p jobPlanner) poolID(ctx context.Context, ns namespace.Name, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: name the runner pool of the job", job.ErrInvalid)
	}

	pools, err := p.pools.Pools(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("pools of %s: %w", ns, err)
	}

	i := slices.IndexFunc(pools, func(pl runner.Pool) bool { return pl.Name == name })
	if i < 0 {
		return "", fmt.Errorf("%w: namespace %s has no runner pool %q", job.ErrInvalid, ns, name)
	}

	return pools[i].ID, nil
}

// tofuProgram is the program of the plan steps of an OpenTofu workspace. The runner
// finds it on its PATH.
const tofuProgram = "tofu"

// planSteps returns the default steps of a plan for the tool. Only the tools
// with a plan step list here start a job.
func planSteps(tool string) ([]job.Step, error) {
	switch tool {
	case "opentofu":
		return []job.Step{
			{
				Name:             "init",
				Role:             job.RoleTool,
				Phase:            job.PhasePlan,
				Args:             []string{tofuProgram, "init", "-input=false"},
				Env:              map[string]string{"TF_IN_AUTOMATION": "1"},
				Dir:              "",
				Timeout:          0,
				AllowFailure:     false,
				ChangesResources: false,
				Outputs:          nil,
			},
			{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan,
				Args: []string{tofuProgram, "plan", "-input=false", "-out=tfplan"},
				Env:  map[string]string{"TF_IN_AUTOMATION": "1"}, Dir: "", Timeout: 0, AllowFailure: false,
				ChangesResources: false, Outputs: []job.Output{{Kind: job.OutputPlanApply, Path: "tfplan"}},
			},
		}, nil
	default:
		return nil, fmt.Errorf("%w: this server has no default plan steps for the tool %q", job.ErrUnsupported, tool)
	}
}
