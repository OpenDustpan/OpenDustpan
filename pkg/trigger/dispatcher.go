package trigger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Versions tells the dispatcher which state version of a unit is the last
// final one. The job store records it from the state writes of every tool.
type Versions interface {
	// LastFinal returns the ID of the last final state version of unit, and
	// false when the store recorded none.
	LastFinal(ctx context.Context, unit graph.Unit) (string, bool, error)
}

// Fences gives the fence of a namespace for the audit entries of the
// dispatcher.
type Fences interface {
	// Fence returns the current fence of ns. It returns namespace.ErrNotFound
	// for a namespace that does not exist.
	Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error)
}

// AuditAppender writes an audit entry. audit.Store implements it.
type AuditAppender interface {
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// DispatcherConfig holds the ports of a Dispatcher.
type DispatcherConfig struct {
	Store   Store
	Starter Starter
	Savers  SaverCheck
	// Units gives the reference of a unit for the audit entry of a refusal.
	Units  Units
	Audit  AuditAppender
	Fences Fences
	// Versions is optional. Without it, Upstream fails closed: it cannot verify
	// a state version, so it starts nothing.
	Versions Versions
	// Now is the clock, and time.Now when nil.
	Now func() time.Time
}

type dispatcher struct {
	store    Store
	starter  Starter
	savers   SaverCheck
	units    Units
	audit    AuditAppender
	fences   Fences
	versions Versions
	now      func() time.Time
}

// NewDispatcher returns the Dispatcher over the ports of cfg. It returns an
// error that wraps ErrInvalid when a required port is nil.
func NewDispatcher(
	cfg DispatcherConfig,
) (Dispatcher, error) { //nolint:ireturn // The port is the contract of the result.
	if cfg.Store == nil || cfg.Starter == nil || cfg.Savers == nil || cfg.Units == nil || cfg.Audit == nil ||
		cfg.Fences == nil {
		return nil, fmt.Errorf("%w: a dispatcher needs a store, a starter, a saver check, units, an audit store "+
			"and fences", ErrInvalid)
	}

	d := &dispatcher{
		store: cfg.Store, starter: cfg.Starter, savers: cfg.Savers, units: cfg.Units, audit: cfg.Audit,
		fences: cfg.Fences, versions: cfg.Versions, now: cfg.Now,
	}

	if d.now == nil {
		d.now = time.Now
	}

	return d, nil
}

// Scheduled implements Dispatcher.
func (d *dispatcher) Scheduled(ctx context.Context, ns namespace.Name, id ID, slot time.Time) (job.ID, error) {
	if slot.IsZero() {
		return "", fmt.Errorf("%w: a schedule slot is not zero", ErrInvalid)
	}

	t, err := d.load(ctx, ns, id)
	if err != nil {
		return "", err
	}

	if t.Kind != KindSchedule {
		return "", fmt.Errorf("%w: trigger %s is not a schedule trigger", ErrInvalid, id)
	}

	return d.start(ctx, &t, startPlan{
		actor: nil, key: ScheduleKey(id, slot), cause: "schedule slot " + slot.UTC().Format(time.RFC3339),
		source: job.SourceSchedule,
	})
}

// Upstream implements Dispatcher.
func (d *dispatcher) Upstream(ctx context.Context, applied Applied) ([]Outcome, error) {
	if err := applied.Validate(); err != nil {
		return nil, err
	}

	// Fail closed: without a source of state versions, nothing proves that the
	// event names an apply.
	if d.versions == nil {
		return nil, fmt.Errorf("%w: this server has no source of final state versions", ErrVersionMismatch)
	}

	last, ok, err := d.versions.LastFinal(ctx, applied.Unit)
	if err != nil {
		return nil, fmt.Errorf("last final state version of %s: %w", applied.Unit, err)
	}

	// An event for a version that is not the last final version is stale or
	// false: a newer version fires, and a version inside a run is no apply.
	if !ok || last != applied.Version {
		return nil, ErrVersionMismatch
	}

	followers, err := d.store.Followers(ctx, applied.Unit)
	if err != nil {
		return nil, fmt.Errorf("followers of %s: %w", applied.Unit, err)
	}

	out := make([]Outcome, 0, len(followers))

	for i := range followers {
		t := followers[i]

		id, err := d.start(ctx, &t, startPlan{
			actor: nil, key: UpstreamKey(t.ID, applied.Version), cause: "unit " + applied.Unit.ID + " applied",
			source: job.SourceTrigger,
		})
		out = append(out, Outcome{Trigger: t.ID, Job: id, Err: err})
	}

	return out, nil
}

// Manual implements Dispatcher.
func (d *dispatcher) Manual(ctx context.Context, actor job.Actor, req ManualRequest) (job.ID, error) {
	t, err := d.load(ctx, req.Namespace, req.Trigger)
	if err != nil {
		return "", err
	}

	return d.start(ctx, &t, startPlan{
		actor: &actor, key: ManualKey(req.Trigger, actor.Principal.Subject, req.Now),
		cause: "manual: " + req.Reason, source: "",
	})
}

// load returns the trigger id after the checks that every method shares.
func (d *dispatcher) load(ctx context.Context, ns namespace.Name, id ID) (Trigger, error) {
	t, err := d.store.Get(ctx, ns, id)
	if err != nil {
		return Trigger{}, fmt.Errorf("trigger %s: %w", quote(string(id)), err)
	}

	// Defense in depth: the store checked the trigger at save, and this check
	// stays for a row that another version wrote or a restore brought back.
	if err := t.Validate(); err != nil {
		return Trigger{}, fmt.Errorf("trigger %s: %w", id, err)
	}

	if !t.Enabled {
		return Trigger{}, ErrDisabled
	}

	return t, nil
}

// startPlan is what one start needs besides the trigger. A nil actor means
// the namespace service principal.
type startPlan struct {
	actor  *job.Actor
	key    string
	cause  string
	source job.SourceKind
}

// start checks the trigger and its last saver, and starts the plan.
func (d *dispatcher) start(ctx context.Context, t *Trigger, p startPlan) (job.ID, error) {
	// Defense in depth: load and the store validated t. A caller of start with
	// a trigger that it built itself gets no start for a value that the rules
	// refuse.
	if err := t.Validate(); err != nil {
		return "", fmt.Errorf("trigger %s: %w", t.ID, err)
	}

	actor, err := d.actorOf(t, p.actor)
	if err != nil {
		return "", err
	}

	if err := d.savers.CheckStart(ctx, *t); err != nil {
		return "", d.refuse(ctx, t, &actor, err)
	}

	id, err := d.starter.StartPlan(ctx, actor, t.Unit, PlanRequest{
		Trigger: t.ID, Kind: t.Kind, IdempotencyKey: p.key,
		Reason: fmt.Sprintf("trigger %s (%s): %s", t.ID, t.Kind, p.cause), Source: p.source, Saver: t.LastSaver,
	})
	if err != nil {
		return "", fmt.Errorf("start the plan of trigger %s: %w", t.ID, err)
	}

	return id, nil
}

// actorOf returns the actor of a start: the caller of a manual fire, or the
// namespace service principal of the namespace of t.
func (d *dispatcher) actorOf(t *Trigger, given *job.Actor) (job.Actor, error) {
	if given != nil {
		return *given, nil
	}

	p, err := identity.NamespaceService(t.Unit.Namespace)
	if err != nil {
		return job.Actor{}, fmt.Errorf("trigger %s: %w", t.ID, err)
	}

	return job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}, nil
}

// refuse writes the audit entry of a refused start and returns the error of
// the refusal. A failure to write the entry does not let the start through: the
// returned error holds both causes.
func (d *dispatcher) refuse(ctx context.Context, t *Trigger, actor *job.Actor, cause error) error {
	if !errors.Is(cause, ErrSaverDenied) {
		// A failure of a port: the start fails closed with no entry, because
		// the check decided nothing about the saver.
		return fmt.Errorf("trigger %s: %w", t.ID, cause)
	}

	err := d.recordDenial(ctx, t, actor)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("audit the refusal of trigger %s: %w", t.ID, err))
	}

	return cause
}

// recordDenial appends the entry of a start that the saver check refused. It
// names the trigger and its saver, and holds no text of the cause.
func (d *dispatcher) recordDenial(ctx context.Context, t *Trigger, actor *job.Actor) error {
	fence, err := d.fences.Fence(ctx, t.Unit.Namespace)
	if err != nil {
		return fmt.Errorf("fence of %s: %w", t.Unit.Namespace, err)
	}

	object := t.Unit.String()
	if obj, err := d.units.Object(ctx, t.Unit); err == nil {
		object = obj.String()
	}

	_, err = d.audit.Append(ctx, fence, audit.Entry{
		At: d.now().UTC(), Namespace: t.Unit.Namespace, Subject: actor.Principal.Subject, IDPSubject: "",
		TokenID: actor.Principal.TokenID, Action: string(StartAction), Object: object, Version: "", ValuePath: "",
		ValueCount: nil,
		Changes: []audit.Change{
			{Key: "trigger", Old: nil, New: string(t.ID), Layer: "", TakesEffect: ""},
			{Key: "trigger-saver", Old: nil, New: t.LastSaver, Layer: "", TakesEffect: ""},
		},
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeDenied, RequestID: "", ObjectID: t.Unit.ID, PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: string(actor.Principal.Kind), JobID: "",
		Reason: "the last saver of the trigger no longer holds the actions of a start",
	})
	if err != nil {
		return fmt.Errorf("append: %w", err)
	}

	return nil
}
