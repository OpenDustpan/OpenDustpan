package triggertest_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/trigger/triggertest"
)

// knownVersion is a state version that the reference knows.
type knownVersion struct {
	unit  graph.Unit
	final bool
}

type key struct {
	ns namespace.Name
	id trigger.ID
}

// mem is the reference adapter of Store and Dispatcher. It runs the suite, so
// each case runs before any SQL adapter exists. It keeps the audit log that a
// SQL adapter writes in the same transaction.
type mem struct {
	starter  trigger.Starter
	fences   map[namespace.Name]int64
	triggers map[key]trigger.Trigger
	versions map[string]knownVersion
	audited  map[namespace.Name]int
	next     int
	mu       sync.Mutex
}

func newMem(starter trigger.Starter) *mem {
	return &mem{
		starter:  starter,
		fences:   map[namespace.Name]int64{},
		triggers: map[key]trigger.Trigger{},
		versions: map[string]knownVersion{},
		audited:  map[namespace.Name]int{},
		next:     0,
		mu:       sync.Mutex{},
	}
}

func (m *mem) Put(_ context.Context, t trigger.Trigger, fence namespace.Fence, _ audit.Entry) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, t.Unit.Namespace); err != nil {
		return err
	}

	k := key{ns: t.Unit.Namespace, id: t.ID}

	if old, ok := m.triggers[k]; ok && (old.Unit != t.Unit || old.Kind != t.Kind) {
		return fmt.Errorf("%w: a trigger keeps its unit and its kind", trigger.ErrInvalid)
	}

	if err := trigger.CheckCycle(m.inNamespace(k.ns), t); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	m.triggers[k] = t
	m.audited[k.ns]++

	return nil
}

func (m *mem) Get(_ context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	if err := id.Validate(); err != nil {
		return trigger.Trigger{}, fmt.Errorf("get: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.triggers[key{ns: ns, id: id}]
	if !ok {
		return trigger.Trigger{}, trigger.ErrNotFound
	}

	return t, nil
}

func (m *mem) List(_ context.Context, unit graph.Unit) ([]trigger.Trigger, error) {
	if err := unit.Validate(); err != nil {
		return nil, fmt.Errorf("list: %w: %w", trigger.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []trigger.Trigger

	all := m.inNamespace(unit.Namespace)
	for i := range all {
		if all[i].Unit == unit {
			out = append(out, all[i])
		}
	}

	return out, nil
}

func (m *mem) Followers(_ context.Context, source graph.Unit) ([]trigger.Trigger, error) {
	if err := source.Validate(); err != nil {
		return nil, fmt.Errorf("followers: %w: %w", trigger.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []trigger.Trigger

	all := m.inNamespace(source.Namespace)
	for i := range all {
		if t := &all[i]; t.Kind == trigger.KindFollows && t.Enabled && t.Source == source {
			out = append(out, *t)
		}
	}

	return out, nil
}

func (m *mem) Delete(_ context.Context, ns namespace.Name, id trigger.ID, fence namespace.Fence, _ audit.Entry) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, ns); err != nil {
		return err
	}

	k := key{ns: ns, id: id}
	if _, ok := m.triggers[k]; !ok {
		return trigger.ErrNotFound
	}

	delete(m.triggers, k)
	m.audited[ns]++

	return nil
}

func (m *mem) Scheduled(ctx context.Context, ns namespace.Name, id trigger.ID, slot time.Time) (job.ID, error) {
	if slot.IsZero() {
		return "", fmt.Errorf("%w: a schedule slot is not zero", trigger.ErrInvalid)
	}

	t, err := m.fire(ctx, ns, id)
	if err != nil {
		return "", err
	}

	if t.Kind != trigger.KindSchedule {
		return "", fmt.Errorf("%w: trigger %s is not a schedule trigger", trigger.ErrInvalid, id)
	}

	return m.start(ctx, triggertest.ActorOf("trigger:test"), t, trigger.ScheduleKey(id, slot), "schedule slot")
}

func (m *mem) Upstream(ctx context.Context, applied trigger.Applied) ([]trigger.Outcome, error) {
	if err := applied.Validate(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	known, ok := m.versions[applied.Version]
	m.mu.Unlock()

	if !ok || known.unit != applied.Unit || !known.final {
		return nil, trigger.ErrVersionMismatch
	}

	followers, err := m.Followers(ctx, applied.Unit)
	if err != nil {
		return nil, err
	}

	out := make([]trigger.Outcome, 0, len(followers))

	for i := range followers {
		t := followers[i]
		key := trigger.UpstreamKey(t.ID, applied.Version)

		id, err := m.start(ctx, triggertest.ActorOf("trigger:test"), t, key, "unit "+applied.Unit.ID+" applied")
		out = append(out, trigger.Outcome{Trigger: t.ID, Job: id, Err: err})
	}

	return out, nil
}

func (m *mem) Manual(ctx context.Context, actor job.Actor, req trigger.ManualRequest) (job.ID, error) {
	t, err := m.fire(ctx, req.Namespace, req.Trigger)
	if err != nil {
		return "", err
	}

	return m.start(ctx, actor, t, trigger.ManualKey(req.Trigger, req.Now), "manual: "+req.Reason)
}

func (m *mem) checkFence(f namespace.Fence, ns namespace.Name) error {
	if f.Namespace != ns || m.fences[ns] != f.FailoverVersion {
		return fmt.Errorf("fence of %q: %w", ns, namespace.ErrFenced)
	}

	return nil
}

// inNamespace returns the triggers of ns.
func (m *mem) inNamespace(ns namespace.Name) []trigger.Trigger {
	var out []trigger.Trigger

	for _, k := range slices.SortedFunc(maps.Keys(m.triggers), func(a, b key) int { return strings.Compare(string(a.id), string(b.id)) }) {
		if k.ns == ns {
			out = append(out, m.triggers[k])
		}
	}

	return out
}

// fire returns the trigger id after the checks that every method of the
// dispatcher shares.
func (m *mem) fire(ctx context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	t, err := m.Get(ctx, ns, id)
	if err != nil {
		return t, err
	}

	if !t.Enabled {
		return t, trigger.ErrDisabled
	}

	return t, nil
}

// start starts the plan of t. The kind of t decides nothing here: every kind
// starts a plan of its unit.
func (m *mem) start(ctx context.Context, actor job.Actor, t trigger.Trigger, key, cause string) (job.ID, error) {
	id, err := m.starter.StartPlan(ctx, actor, t.Unit, trigger.PlanRequest{
		Trigger:        t.ID,
		Kind:           t.Kind,
		IdempotencyKey: key,
		Reason:         fmt.Sprintf("trigger %s (%s): %s", t.ID, t.Kind, cause),
	})
	if err != nil {
		return "", fmt.Errorf("start the plan of trigger %s: %w", t.ID, err)
	}

	return id, nil
}

func (m *mem) writeVersion(u graph.Unit, final bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.next++
	id := fmt.Sprintf("version-%d", m.next)
	m.versions[id] = knownVersion{unit: u, final: final}

	return id
}

func TestConformance(t *testing.T) {
	t.Parallel()

	triggertest.Run(t, func(_ *testing.T, starter trigger.Starter) triggertest.Harness {
		m := newMem(starter)

		return triggertest.Harness{
			Store:      m,
			Dispatcher: m,
			Namespace: func(_ *testing.T, name string) namespace.Fence {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[namespace.Name(name)] = 1

				return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 1}
			},
			Versions: triggertest.Versions{
				Final:        func(_ *testing.T, u graph.Unit) string { return m.writeVersion(u, true) },
				Intermediate: func(_ *testing.T, u graph.Unit) string { return m.writeVersion(u, false) },
			},
			Audited: func(_ *testing.T, ns string) int {
				m.mu.Lock()
				defer m.mu.Unlock()

				return m.audited[namespace.Name(ns)]
			},
		}
	})
}
