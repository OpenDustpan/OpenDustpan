package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// Suffixes of the two extra namespaces of a run. The list namespace has an
// IP allowlist that holds the harness client. The deny namespace has one that
// refuses it.
const (
	listSuffix = "-list"
	denySuffix = "-deny"
)

// fixtureStack names the stack that holds the job fixture of a namespace.
const fixtureStack = "fixture"

// errFixtureUnit reports a fixture stack that the inventory does not list.
var errFixtureUnit = errors.New("fixture unit not in the inventory")

// seedFixtures creates the harness namespace, the list namespace and the deny
// namespace on the first instance, each with one stack and one queued job on
// it, and answers the names and job IDs the access steps read. The two extra
// namespaces get their IP allowlist. The REST API starts a job only through a
// pool, a code location and a gate store, and changes a list only in a console
// session with a recent sign-in. The harness measures neither, so the
// fixtures go in through the store ports.
func seedFixtures(ctx context.Context, p ports, ns string) (servers, error) {
	srv := servers{
		urls: nil, gateURL: "", listNS: ns + listSuffix, denyNS: ns + denySuffix,
		jobID: "", listJobID: "", denyJobID: "",
	}

	var err error

	if srv.jobID, err = seedNamespaceJob(ctx, p, ns, nil); err != nil {
		return servers{}, err
	}

	if srv.listJobID, err = seedNamespaceJob(ctx, p, srv.listNS, allowlistPrefixes()); err != nil {
		return servers{}, err
	}

	if srv.denyJobID, err = seedNamespaceJob(ctx, p, srv.denyNS, refusingPrefixes()); err != nil {
		return servers{}, err
	}

	return srv, nil
}

// seedNamespaceJob creates namespace ns with one stack and one queued runner
// job on that stack, and answers the job ID. A non-empty allowlist becomes the
// IP allowlist of the namespace.
func seedNamespaceJob(ctx context.Context, p ports, ns string, allowlist []string) (string, error) {
	if err := createLoadNamespace(ctx, p.namespaces, ns); err != nil {
		return "", err
	}

	name, err := namespace.ParseName(ns)
	if err != nil {
		return "", fmt.Errorf("parse namespace: %w", err)
	}

	found, err := p.namespaces.GetNamespace(ctx, name)
	if err != nil {
		return "", fmt.Errorf("get namespace: %w", err)
	}

	fence := found.Fence()
	now := time.Now()

	st := stack.Stack{
		NamespaceID: "",
		CreatedAt:   now, UpdatedAt: now, Tags: map[string]string{},
		Ref: stack.Ref{
			Namespace: name, Project: stack.ProjectName(loadProject), Stack: stack.Name(fixtureStack),
		},
		ID: stack.ID(ident.NewID("stk-")), Version: 0,
	}

	if createErr := p.stacks.CreateStack(ctx, st, fence); createErr != nil {
		return "", fmt.Errorf("create fixture stack: %w", createErr)
	}

	unit, err := fixtureUnit(ctx, p, name)
	if err != nil {
		return "", err
	}

	if len(allowlist) > 0 {
		if listErr := setAllowlist(ctx, p, name, allowlist, fence); listErr != nil {
			return "", listErr
		}
	}

	return seedFixtureJob(ctx, p, unit, fence)
}

// setAllowlist stores the IP allowlist prefixes on namespace ns.
func setAllowlist(
	ctx context.Context, p ports, ns namespace.Name, prefixes []string, fence namespace.Fence,
) error {
	list, err := ipallow.ParseList(prefixes)
	if err != nil {
		return fmt.Errorf("parse allowlist: %w", err)
	}

	var entry audit.Entry

	entry.At = time.Now().UTC()
	entry.Namespace = ns
	entry.Subject = loadProject
	entry.Action = "namespace:set-ip-allowlist"
	entry.Object = identity.NamespaceObject(ns).String()
	entry.Channel = audit.ChannelStartup
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeSuccess

	if setErr := p.allowlists.SetNamespaceList(ctx, ns, list, fence, entry); setErr != nil {
		return fmt.Errorf("set allowlist: %w", setErr)
	}

	return nil
}

// fixtureUnit answers the unit reference, with its stable ID, of the fixture
// stack in ns.
func fixtureUnit(ctx context.Context, p ports, ns namespace.Name) (identity.Object, error) {
	entries, err := p.units.ListUnits(ctx, ns)
	if err != nil {
		return identity.Object{}, fmt.Errorf("list units: %w", err)
	}

	want := loadProject + "/" + fixtureStack

	for i := range entries {
		e := &entries[i]
		if e.Tool != "pulumi" || e.Name != want {
			continue
		}

		obj, objErr := identity.UnitObject(e.Tool, ns, e.Name)
		if objErr != nil {
			return identity.Object{}, fmt.Errorf("unit object: %w", objErr)
		}

		return obj.WithUnitID(e.UnitID), nil
	}

	return identity.Object{}, fmt.Errorf("%w: %s", errFixtureUnit, want)
}

// seedFixtureJob stores one queued plan job on unit, with no dispatch, and answers its ID.
func seedFixtureJob(ctx context.Context, p ports, unit identity.Object, fence namespace.Fence) (string, error) {
	kinds, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		return "", fmt.Errorf("job phases: %w", err)
	}

	phases := make([]job.Phase, len(kinds))
	for i, kind := range kinds {
		phases[i].Kind = kind
	}

	queued := job.Job{
		ID:          job.ID(ident.NewID(job.IDPrefix)),
		NamespaceID: "",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Purpose: job.PurposeNone, Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "github", Repository: "load/infra", Ref: "refs/heads/main",
				Directory: "network", Revision: "0123456789abcdef",
			},
			Steps: nil, Pool: "rpl-load", Profile: "prf-load", Edits: nil, Delivery: "",
			Defaulted: nil,
		},
		State: job.StateQueued, Wait: job.WaitNone, Reason: "", Phases: phases, Current: 0,
		StartedBy: loadProject, Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil, Trigger: nil,
		Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
		StateChangedAt: time.Time{},
	}

	stored, _, err := p.jobs.Create(ctx, queued, nil, fence, nil)
	if err != nil {
		return "", fmt.Errorf("create fixture job: %w", err)
	}

	return string(stored.ID), nil
}
