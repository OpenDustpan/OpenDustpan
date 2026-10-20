package drifttest_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/drift/drifttest"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// jobKind is the kind of a job that the reference knows.
type jobKind int

const (
	kindDriftCheck jobKind = iota
	kindRunning
	kindPlain
	kindFailed
	kindCanceled
)

type knownJob struct {
	unit graph.Unit
	kind jobKind
}

// mem is the reference adapter of Store and PolicyStore. It runs the suite, so
// each case runs before any SQL adapter exists. It keeps the projection, the
// outbox and the audit log that a SQL adapter writes in the same transaction.
type mem struct {
	fences     map[namespace.Name]int64
	latest     map[graph.Unit]drift.Latest
	used       map[job.ID]bool
	jobs       map[job.ID]knownJob
	policies   map[drift.Scope]drift.Policy
	projection map[graph.Unit]drift.Status
	notified   map[namespace.Name][]string
	audited    map[namespace.Name]int
	next       int
	mu         sync.Mutex
}

func newMem() *mem {
	return &mem{
		fences:     map[namespace.Name]int64{},
		latest:     map[graph.Unit]drift.Latest{},
		used:       map[job.ID]bool{},
		jobs:       map[job.ID]knownJob{},
		policies:   map[drift.Scope]drift.Policy{},
		projection: map[graph.Unit]drift.Status{},
		notified:   map[namespace.Name][]string{},
		audited:    map[namespace.Name]int{},
		next:       0,
		mu:         sync.Mutex{},
	}
}

func (m *mem) Record(_ context.Context, r drift.Result, fence namespace.Fence) (drift.Transition, error) {
	none := drift.Transition{At: r.CheckedAt, Unit: r.Unit, JobID: r.JobID, From: "", To: ""}

	if err := r.Validate(); err != nil {
		return none, fmt.Errorf("record: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, r.Unit.Namespace); err != nil {
		return none, err
	}

	if err := m.checkJob(r); err != nil {
		return none, err
	}

	prev, had := m.latest[r.Unit]
	if m.used[r.JobID] || (had && !r.CheckedAt.After(prev.CheckedAt)) {
		return none, drift.ErrStale
	}

	from := drift.StatusUnknown
	if had {
		from = prev.Status
	}

	tr := drift.Transition{At: r.CheckedAt, Unit: r.Unit, JobID: r.JobID, From: from, To: r.Status()}

	m.used[r.JobID] = true
	m.latest[r.Unit] = drift.Latest{
		CheckedAt: r.CheckedAt,
		Unit:      r.Unit,
		JobID:     r.JobID,
		Status:    tr.To,
		Resources: slices.Clone(r.Resources),
		Truncated: r.Truncated,
	}
	m.projection[r.Unit] = tr.To

	if tr.BecameDrifted() || tr.Cleared() {
		m.notified[r.Unit.Namespace] = append(m.notified[r.Unit.Namespace], "drift:"+r.Unit.ID+":"+string(r.JobID))
	}

	return tr, nil
}

func (m *mem) GetLatest(_ context.Context, unit graph.Unit) (drift.Latest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, ok := m.latest[unit]
	if !ok {
		return drift.Latest{}, drift.ErrNotFound
	}

	l.Resources = slices.Clone(l.Resources)

	return l, nil
}

func (m *mem) DeleteLatest(_ context.Context, unit graph.Unit, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, unit.Namespace); err != nil {
		return err
	}

	delete(m.latest, unit)
	delete(m.projection, unit)

	return nil
}

func (m *mem) GetPolicy(_ context.Context, scope drift.Scope) (drift.Policy, error) {
	if err := scope.Validate(); err != nil {
		return drift.Policy{}, fmt.Errorf("get policy: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.policies[scope]
	if !ok {
		return drift.Policy{}, drift.ErrNotFound
	}

	return p, nil
}

func (m *mem) PutPolicy(
	_ context.Context,
	scope drift.Scope,
	p drift.Policy,
	fence namespace.Fence,
	_ audit.Entry,
) error {
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("put policy: %w", err)
	}

	if err := p.Validate(); err != nil {
		return fmt.Errorf("put policy: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, scope.Namespace); err != nil {
		return err
	}

	m.policies[scope] = p
	m.audited[scope.Namespace]++

	return nil
}

func (m *mem) DeletePolicy(_ context.Context, scope drift.Scope, fence namespace.Fence, _ audit.Entry) error {
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("delete policy: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, scope.Namespace); err != nil {
		return err
	}

	if _, ok := m.policies[scope]; !ok {
		return drift.ErrNotFound
	}

	delete(m.policies, scope)
	m.audited[scope.Namespace]++

	return nil
}

func (m *mem) EffectivePolicy(_ context.Context, unit graph.Unit, collection string) (drift.Policy, error) {
	if err := unit.Validate(); err != nil {
		return drift.Policy{}, fmt.Errorf("effective policy: %w", drift.ErrInvalid)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	get := func(kind drift.ScopeKind, name string) *drift.Policy {
		p, ok := m.policies[drift.Scope{Namespace: unit.Namespace, Kind: kind, Name: name}]
		if !ok {
			return nil
		}

		return &p
	}

	var coll *drift.Policy
	if collection != "" {
		coll = get(drift.ScopeCollection, collection)
	}

	return drift.Resolve(get(drift.ScopeUnit, unit.ID), coll, get(drift.ScopeNamespace, "")), nil
}

// checkJob must run with m.mu held.
func (m *mem) checkJob(r drift.Result) error {
	j, ok := m.jobs[r.JobID]
	if !ok || j.unit != r.Unit || j.kind != kindDriftCheck {
		return fmt.Errorf("job %s: %w", r.JobID, drift.ErrJobMismatch)
	}

	if w := drifttest.JobWindow(); r.CheckedAt.Before(w.Start) || r.CheckedAt.After(w.End) {
		return fmt.Errorf("job %s: checked outside the run of the job: %w", r.JobID, drift.ErrJobMismatch)
	}

	return nil
}

// mintJob makes a job of the given kind and returns its ID.
func (m *mem) mintJob(unit graph.Unit, kind jobKind) job.ID {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.next++
	id := job.ID(fmt.Sprintf("%s%d", job.IDPrefix, m.next))
	m.jobs[id] = knownJob{unit: unit, kind: kind}

	return id
}

// checkFence must run with m.mu held.
func (m *mem) checkFence(f namespace.Fence, ns namespace.Name) error {
	if f.Namespace != ns {
		return fmt.Errorf("fence of %q for a write in %q: %w", f.Namespace, ns, namespace.ErrFenced)
	}

	if v, ok := m.fences[ns]; !ok || v != f.FailoverVersion {
		return fmt.Errorf("fence version %d: %w", f.FailoverVersion, namespace.ErrFenced)
	}

	return nil
}

func TestConformance(t *testing.T) {
	t.Parallel()

	drifttest.Run(t, func(*testing.T) drifttest.Harness {
		m := newMem()

		return drifttest.Harness{
			Store:    m,
			Policies: m,
			Checker:  nil,
			Namespace: func(_ *testing.T, name string) namespace.Fence {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[namespace.Name(name)] = 1

				return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 1}
			},
			Jobs: drifttest.Jobs{
				DriftCheck: func(_ *testing.T, u graph.Unit) job.ID { return m.mintJob(u, kindDriftCheck) },
				Running:    func(_ *testing.T, u graph.Unit) job.ID { return m.mintJob(u, kindRunning) },
				Failed:     func(_ *testing.T, u graph.Unit) job.ID { return m.mintJob(u, kindFailed) },
				Canceled:   func(_ *testing.T, u graph.Unit) job.ID { return m.mintJob(u, kindCanceled) },
				Plain:      func(_ *testing.T, u graph.Unit) job.ID { return m.mintJob(u, kindPlain) },
			},
			Projection: func(_ *testing.T, u graph.Unit) drift.Status {
				m.mu.Lock()
				defer m.mu.Unlock()

				if s, ok := m.projection[u]; ok {
					return s
				}

				return drift.StatusUnknown
			},
			Notified: func(_ *testing.T, ns string) []string {
				m.mu.Lock()
				defer m.mu.Unlock()

				return slices.Clone(m.notified[namespace.Name(ns)])
			},
			Audited: func(_ *testing.T, ns string) int {
				m.mu.Lock()
				defer m.mu.Unlock()

				return m.audited[namespace.Name(ns)]
			},
		}
	})
}
