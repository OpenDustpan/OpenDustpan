package catalogtest_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/catalog/catalogtest"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memDB is the reference adapter. One mutex stands for the transaction of
// a SQL adapter: each write checks the fence, changes the rows and appends
// the audit entries, or does nothing.
type memDB struct {
	mu         sync.Mutex
	fences     map[namespace.Name]int64
	audit      map[namespace.Name][]audit.Entry
	operations map[string]durable.Kind
	requests   map[catalog.RequestRef]catalog.Request
	revisions  map[string]catalog.Revision
	drafts     map[catalog.BlueprintRef]draft
	versions   map[catalog.BlueprintRef][]catalog.Version
	nextID     int
}

type draft struct {
	blueprint catalog.Blueprint
	version   int64
}

func newMemDB() *memDB {
	return &memDB{
		mu:         sync.Mutex{},
		fences:     map[namespace.Name]int64{},
		audit:      map[namespace.Name][]audit.Entry{},
		operations: map[string]durable.Kind{},
		requests:   map[catalog.RequestRef]catalog.Request{},
		revisions:  map[string]catalog.Revision{},
		drafts:     map[catalog.BlueprintRef]draft{},
		versions:   map[catalog.BlueprintRef][]catalog.Version{},
		nextID:     0,
	}
}

// CreateNamespace implements catalogtest.Domain.
func (m *memDB) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[name] = 1

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

// CreateUnit implements catalogtest.Domain.
func (m *memDB) CreateUnit(_ context.Context, fence namespace.Fence, tool, name string) (identity.Object, error) {
	o, err := identity.UnitObject(tool, fence.Namespace, name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("create unit: %w", err)
	}

	return o.WithUnitID("ws-" + name), nil
}

// BumpFence implements catalogtest.Domain.
func (m *memDB) BumpFence(_ context.Context, fence namespace.Fence) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[fence.Namespace]++

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: m.fences[fence.Namespace]}, nil
}

// AuditEntries implements catalogtest.Domain.
func (m *memDB) AuditEntries(_ context.Context, ns namespace.Name) ([]audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.audit[ns]), nil
}

// Operation implements catalogtest.Domain.
func (m *memDB) Operation(_ context.Context, key string) (durable.Kind, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.operations[key]

	return k, ok, nil
}

// Submit implements catalog.Store.
func (m *memDB) Submit(_ context.Context, s *catalog.Submission, fence namespace.Fence) (catalog.Request,
	catalog.Revision, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return catalog.Request{}, catalog.Revision{}, err
	}

	req := s.Request
	have, exists := m.requests[req.Ref]

	switch {
	case exists && have.Version != req.Version, !exists && req.Version != 0:
		return catalog.Request{}, catalog.Revision{}, fmt.Errorf("%w: %s", catalog.ErrConflict, req.Ref)
	case exists:
		last := m.revisions[catalog.RevisionKey(have.ID, have.Last)]
		if !last.State.Terminal() {
			return catalog.Request{}, catalog.Revision{}, fmt.Errorf(
				"%w: revision %d",
				catalog.ErrInFlight,
				last.Number,
			)
		}

		req = have
	default:
		m.nextID++
		req.ID = catalog.RequestID(catalog.RequestIDPrefix + strconv.Itoa(m.nextID))
		req.CreatedAt = time.Now().UTC()
	}

	rev := s.Revision
	rev.Request = req.ID
	rev.Number = req.Last + 1
	rev.State = catalog.StateAccepted
	rev.CreatedAt = time.Now().UTC()
	req.Last = rev.Number
	req.Version++
	req.UpdatedAt = rev.CreatedAt

	m.requests[req.Ref] = req
	m.revisions[rev.Key()] = rev
	m.operations[rev.Key()] = catalog.KindStart
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], s.Audit...)

	return req, rev, nil
}

// Get implements catalog.Store.
func (m *memDB) Get(_ context.Context, ref catalog.RequestRef) (catalog.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.requests[ref]
	if !ok {
		return catalog.Request{}, fmt.Errorf("%w: %s", catalog.ErrNotFound, ref)
	}

	return req, nil
}

// Revision implements catalog.Store.
func (m *memDB) Revision(_ context.Context, id catalog.RequestID, n int) (catalog.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rev, ok := m.revisions[catalog.RevisionKey(id, n)]
	if !ok {
		return catalog.Revision{}, fmt.Errorf("%w: %s", catalog.ErrNotFound, catalog.RevisionKey(id, n))
	}

	return rev, nil
}

// ListRequests implements catalog.Store.
func (m *memDB) ListRequests(_ context.Context, f catalog.ListFilter) ([]catalog.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []catalog.Request

	for ref := range m.requests {
		req := m.requests[ref]
		last := m.revisions[catalog.RevisionKey(req.ID, req.Last)]

		switch {
		case f.Namespace != "" && req.Ref.Namespace != f.Namespace,
			f.Blueprint != "" && req.Ref.Blueprint != f.Blueprint,
			f.UnitID != "" && req.Unit.UnitID() != f.UnitID,
			len(f.States) > 0 && !slices.Contains(f.States, last.State),
			f.After != "" && req.ID <= f.After:
			continue
		default:
			out = append(out, req)
		}
	}

	slices.SortFunc(out, func(a, b catalog.Request) int {
		return compare(a.ID, b.ID)
	})

	return out, nil
}

// Change implements catalog.Store.
func (m *memDB) Change(_ context.Context, c catalog.RevisionChange, fence namespace.Fence,
	e []audit.Entry,
) (catalog.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return catalog.Revision{}, err
	}

	req, ok := m.requests[c.Ref]
	if !ok {
		return catalog.Revision{}, fmt.Errorf("%w: %s", catalog.ErrNotFound, c.Ref)
	}

	key := catalog.RevisionKey(req.ID, c.Number)

	rev, ok := m.revisions[key]
	if !ok {
		return catalog.Revision{}, fmt.Errorf("%w: %s", catalog.ErrNotFound, key)
	}

	if req.Version != c.Version {
		return catalog.Revision{}, fmt.Errorf("%w: %s at %d", catalog.ErrConflict, c.Ref, req.Version)
	}

	if err := catalog.CheckTransition(rev.State, c.To); err != nil {
		return catalog.Revision{}, err
	}

	m.apply(&req, &rev, c)
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return rev, nil
}

// ObserveStateVersion implements catalog.Store.
func (m *memDB) ObserveStateVersion(_ context.Context, v job.StateVersion, fence namespace.Fence) (catalog.Revision,
	bool, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return noRevision(), false, err
	}

	if !v.Final {
		return noRevision(), false, nil
	}

	for ref := range m.requests {
		req := m.requests[ref]
		rev := m.revisions[catalog.RevisionKey(req.ID, req.Last)]

		closes := rev.Origin == catalog.OriginGit && rev.Job == "" && rev.State == catalog.StateAwaitingState &&
			req.Unit.UnitID() == v.Unit.UnitID() &&
			(rev.Commit == v.Commit || (v.Commit == "" && !rev.UpdatedAt.After(v.At)))
		if !closes {
			continue
		}

		m.apply(&req, &rev, catalog.RevisionChange{
			Ref: req.Ref, To: catalog.StateApplied, Job: "", JobVersion: 0, Reason: "", Failures: nil,
			Unit: identity.Object{}, Number: rev.Number, Version: req.Version,
		})

		return rev, true, nil
	}

	return noRevision(), false, nil
}

// PutDraft implements catalog.BlueprintStore.
func (m *memDB) PutDraft(_ context.Context, b catalog.Blueprint, version int64, fence namespace.Fence,
	e []audit.Entry,
) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return 0, err
	}

	ref := catalog.BlueprintRef{Namespace: fence.Namespace, Name: b.Name}
	if m.drafts[ref].version != version {
		return 0, fmt.Errorf("%w: draft %s at %d", catalog.ErrConflict, ref, m.drafts[ref].version)
	}

	m.drafts[ref] = draft{blueprint: b, version: version + 1}
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return version + 1, nil
}

// Draft implements catalog.BlueprintStore.
func (m *memDB) Draft(_ context.Context, ref catalog.BlueprintRef) (catalog.Blueprint, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.drafts[ref]
	if !ok {
		return catalog.Blueprint{}, 0, fmt.Errorf("%w: draft %s", catalog.ErrNotFound, ref)
	}

	return d.blueprint, d.version, nil
}

// Publish implements catalog.BlueprintStore.
func (m *memDB) Publish(_ context.Context, p catalog.Publication, fence namespace.Fence,
	e []audit.Entry,
) (catalog.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return catalog.Version{}, err
	}

	if m.drafts[p.Ref].version != p.DraftVersion {
		return catalog.Version{}, fmt.Errorf("%w: draft %s", catalog.ErrConflict, p.Ref)
	}

	v := catalog.Version{
		PublishedAt: p.At, Blueprint: p.Blueprint, Namespace: p.Ref.Namespace, PublishedBy: p.By,
		Number: len(m.versions[p.Ref]) + 1, Retired: false,
	}
	m.versions[p.Ref] = append(m.versions[p.Ref], v)
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return v, nil
}

// Version implements catalog.BlueprintStore.
func (m *memDB) Version(_ context.Context, ref catalog.BlueprintRef, n int) (catalog.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vs := m.versions[ref]
	if n < 1 || n > len(vs) {
		return catalog.Version{}, fmt.Errorf("%w: %s version %d", catalog.ErrNotFound, ref, n)
	}

	return vs[n-1], nil
}

// ListBlueprints implements catalog.BlueprintStore.
func (m *memDB) ListBlueprints(_ context.Context, ns namespace.Name) ([]catalog.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []catalog.Version

	for ref, vs := range m.versions {
		if ref.Namespace == ns && len(vs) > 0 {
			out = append(out, vs[len(vs)-1])
		}
	}

	slices.SortFunc(out, func(a, b catalog.Version) int {
		return compare(a.Blueprint.Name, b.Blueprint.Name)
	})

	return out, nil
}

// Retire implements catalog.BlueprintStore.
func (m *memDB) Retire(_ context.Context, ref catalog.BlueprintRef, n int, fence namespace.Fence,
	e []audit.Entry,
) error {
	return m.changeVersion(ref, n, fence, e, func(v *catalog.Version) { v.Retired = true })
}

// SetExprVersion implements catalog.BlueprintStore.
func (m *memDB) SetExprVersion(_ context.Context, ref catalog.BlueprintRef, n int, expr string,
	fence namespace.Fence,
) error {
	return m.changeVersion(ref, n, fence, nil, func(v *catalog.Version) { v.Blueprint.ExprVersion = expr })
}

func (m *memDB) changeVersion(ref catalog.BlueprintRef, n int, fence namespace.Fence, e []audit.Entry,
	f func(v *catalog.Version),
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	vs := m.versions[ref]
	if n < 1 || n > len(vs) {
		return fmt.Errorf("%w: %s version %d", catalog.ErrNotFound, ref, n)
	}

	f(&vs[n-1])
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return nil
}

// apply writes c to req and rev. The caller holds the mutex and checked the
// transition.
func (m *memDB) apply(req *catalog.Request, rev *catalog.Revision, c catalog.RevisionChange) {
	now := time.Now().UTC()

	if c.To != rev.State && c.To.Terminal() && rev.Operation == catalog.OperationCreate {
		m.operations[catalog.QuotaKey(rev.Key())] = catalog.KindQuota
	}

	rev.State = c.To
	rev.UpdatedAt = now

	if c.Job != "" {
		rev.Job, rev.JobVersion = c.Job, c.JobVersion
	}

	if c.Reason != "" || c.Failures != nil {
		rev.Reason, rev.Failures = c.Reason, c.Failures
	}

	if c.Unit.IsUnit() {
		req.Unit = c.Unit
	}

	if c.To == catalog.StateApplied {
		req.Applied = rev.Number

		req.Ensure = rev.Document.Ensure
		if req.Ensure == "" {
			req.Ensure = catalog.EnsurePresent
		}
	}

	req.Version++
	req.UpdatedAt = now
	m.requests[req.Ref] = *req
	m.revisions[rev.Key()] = *rev
}

func (m *memDB) checkFence(fence namespace.Fence) error {
	if m.fences[fence.Namespace] != fence.FailoverVersion {
		return fmt.Errorf("catalog: %w", namespace.ErrFenced)
	}

	return nil
}

func noRevision() catalog.Revision {
	var r catalog.Revision

	return r
}

func compare[T ~string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func TestMemConformance(t *testing.T) {
	t.Parallel()

	catalogtest.Run(t, func(_ *testing.T) catalogtest.Harness {
		db := newMemDB()

		return catalogtest.Harness{Requests: db, Blueprints: db, Domain: db}
	})
}
