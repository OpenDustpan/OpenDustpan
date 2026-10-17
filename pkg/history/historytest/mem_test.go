package historytest_test

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/history/historytest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// TestSuites runs every suite of the package against an in-memory store, so
// the cases state their rules before a SQL adapter implements the ports.
func TestSuites(t *testing.T) {
	t.Parallel()

	t.Run("redactions", func(t *testing.T) {
		t.Parallel()
		historytest.RunRedactions(t, func(*testing.T) historytest.RedactionStore { return newMem() })
	})
	t.Run("plans", func(t *testing.T) {
		t.Parallel()
		historytest.RunPlans(t, func(*testing.T) historytest.PlanSuiteStore { return newMem() })
	})
	t.Run("job logs", func(t *testing.T) {
		t.Parallel()
		historytest.RunJobLogs(t, func(*testing.T) historytest.JobLogSuiteStore { return newMem() })
	})
}

// memLog is one job log with its chunks.
type memLog struct {
	log    history.JobLog
	chunks []history.Chunk
}

// mem keeps every port of the suites in memory, under one lock, as one SQL
// transaction would.
type mem struct {
	namespaces map[namespace.Name]namespace.Namespace
	keys       map[secret.ContentRef]secret.ContentKey
	redactions map[history.RedactionID]history.Redaction
	plans      map[history.PlanID]history.Plan
	logs       map[string]*memLog
	entries    []audit.Entry
	mu         sync.Mutex
}

func newMem() *mem {
	return &mem{
		namespaces: map[namespace.Name]namespace.Namespace{},
		keys:       map[secret.ContentRef]secret.ContentKey{},
		redactions: map[history.RedactionID]history.Redaction{},
		plans:      map[history.PlanID]history.Plan{},
		logs:       map[string]*memLog{},
		entries:    nil,
		mu:         sync.Mutex{},
	}
}

func (m *mem) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *mem) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *mem) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (m *mem) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (m *mem) Ping(context.Context) error { return nil }

// fenced answers namespace.ErrFenced for a stale fence. The caller holds the
// lock.
func (m *mem) CreateContentKey(_ context.Context, k secret.ContentKey, f namespace.Fence) (secret.ContentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return secret.ContentKey{}, err
	}

	if stored, ok := m.keys[k.Ref]; ok {
		if stored.Shredded() {
			return stored, secret.ErrShredded
		}

		return stored, nil
	}

	m.keys[k.Ref] = k

	return k, nil
}

func (m *mem) GetContentKey(_ context.Context, ref secret.ContentRef) (secret.ContentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[ref]
	switch {
	case !ok:
		return secret.ContentKey{}, secret.ErrKeyNotFound
	case k.Shredded():
		return k, secret.ErrShredded
	default:
		return k, nil
	}
}

func (m *mem) ShredContentKey(
	_ context.Context, ref secret.ContentRef, at time.Time, f namespace.Fence,
) (secret.ContentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return secret.ContentKey{}, err
	}

	return m.shredLocked(ref, at)
}

// shredLocked marks the key of ref shredded. The caller holds the lock.
func (m *mem) ListContentKeys(context.Context, secret.Scope, secret.ContentKeyID, int) ([]secret.ContentKey, error) {
	return nil, nil
}

func (m *mem) ListContentWrapperIDs(context.Context) ([]string, error) { return nil, nil }

func (m *mem) AppendRedaction(
	_ context.Context, r history.Redaction, targets []string, f namespace.Fence,
) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return history.Redaction{}, err
	}

	return m.appendLocked(r, targets)
}

// appendLocked appends r. The caller holds the lock.
func (m *mem) GetRedaction(_ context.Context, ns namespace.Name, id history.RedactionID) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.redactions[id]
	if !ok || r.Item.Unit.Namespace != ns {
		return history.Redaction{}, history.ErrRedactionNotFound
	}

	return r, nil
}

func (m *mem) RedactionOf(_ context.Context, item history.ItemRef) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for n := range m.redactions {
		r := m.redactions[n]
		if r.Item == item {
			return r, nil
		}
	}

	return history.Redaction{}, history.ErrRedactionNotFound
}

// newestFirstAt orders r against the cursor c, newest first.
func newestFirstAt(r history.Redaction, c history.RedactionCursor) int {
	if n := c.At.Compare(r.At); n != 0 {
		return n
	}

	return cmp.Compare(c.ID, r.ID)
}

// planAfter orders p against the cursor c, newest first.
func planAfter(p history.Plan, c history.PlanCursor) int {
	if n := c.CreatedAt.Compare(p.CreatedAt); n != 0 {
		return n
	}

	return cmp.Compare(c.ID, p.ID)
}

// newestFirst orders redactions newest first by time, then by ID.
func newestFirst(a, b history.Redaction) int {
	if c := b.At.Compare(a.At); c != 0 {
		return c
	}

	return cmp.Compare(b.ID, a.ID)
}

func (m *mem) ListRedactions(
	_ context.Context, unit history.Unit, after *history.RedactionCursor, limit int,
) ([]history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []history.Redaction

	for i := range m.redactions {
		r := m.redactions[i]
		if r.Item.Unit != unit {
			continue
		}

		if after != nil && newestFirstAt(r, *after) <= 0 {
			continue
		}

		out = append(out, r)
	}

	slices.SortFunc(out, newestFirst)

	return out[:min(limit, len(out))], nil
}

func (m *mem) ListPendingShreds(_ context.Context, after history.RedactionID, limit int) ([]history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []history.Redaction

	for id := range m.redactions {
		r := m.redactions[id]
		if id > after && r.Pending() {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b history.Redaction) int { return cmp.Compare(a.ID, b.ID) })

	return out[:min(limit, len(out))], nil
}

// target returns the index of the target name of the record id. The caller
// holds the lock.
func (m *mem) FinishShred(
	_ context.Context,
	id history.RedactionID,
	name string,
	at time.Time,
	f namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, i, err := m.target(id, name, f)
	if err != nil {
		return err
	}

	if r.Targets[i].State != history.ShredDone {
		r.Targets[i].State = history.ShredDone
		r.Targets[i].DoneAt = at
		m.redactions[id] = r
	}

	return nil
}

func (m *mem) NoteShredFailure(_ context.Context, sf history.ShredFailure, f namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, i, err := m.target(sf.ID, sf.Target, f)
	if err != nil {
		return err
	}

	if r.Targets[i].State != history.ShredDone {
		r.Targets[i].Attempts++
		r.Targets[i].LastError = sf.Text
		r.Targets[i].LastTryAt = sf.At
		m.redactions[sf.ID] = r
	}

	return nil
}

// Redact redacts a plan or a job log in one step under the lock.
func (m *mem) Redact(
	_ context.Context, r history.Redaction, targets []string, e audit.Entry, f namespace.Fence,
) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return history.Redaction{}, err
	}

	mark, err := m.markable(r.Item)
	if err != nil {
		return history.Redaction{}, err
	}

	stored, err := m.appendLocked(r, targets)
	if err != nil {
		return stored, err
	}

	ref, err := r.Item.ContentRef()
	if err != nil {
		return history.Redaction{}, err
	}

	if _, err := m.shredLocked(ref, r.At); err != nil {
		return history.Redaction{}, err
	}

	mark(r.ID)

	e.At = r.At
	m.entries = append(m.entries, e)

	return stored, nil
}

// markable checks that item can be redacted and returns the step that marks
// it. The caller holds the lock.
func (m *mem) CreatePlan(_ context.Context, p history.Plan, f namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return err
	}

	if _, ok := m.plans[p.ID]; ok {
		return history.ErrPlanExists
	}

	p.Parts = slices.Clone(p.Parts)
	m.plans[p.ID] = p

	return nil
}

func (m *mem) GetPlan(_ context.Context, ns namespace.Name, id history.PlanID) (history.Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.plans[id]
	if !ok || p.Unit.Namespace != ns {
		return history.Plan{}, history.ErrPlanNotFound
	}

	return p, nil
}

// planNewestFirst orders plans newest first by creation time, then by ID.
func planNewestFirst(a, b history.Plan) int {
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}

	return cmp.Compare(b.ID, a.ID)
}

func (m *mem) ListPlans(
	_ context.Context,
	unit history.Unit,
	after *history.PlanCursor,
	limit int,
) ([]history.Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []history.Plan

	for n := range m.plans {
		p := m.plans[n]
		if p.Unit != unit {
			continue
		}

		if after != nil && planAfter(p, *after) <= 0 {
			continue
		}

		out = append(out, p)
	}

	slices.SortFunc(out, planNewestFirst)

	return out[:min(limit, len(out))], nil
}

func (m *mem) ListExpiredPlans(_ context.Context, now time.Time, limit int) ([]history.Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []history.Plan

	for i := range m.plans {
		p := m.plans[i]
		if p.Redaction == "" && !p.ExpiresAt.After(now) {
			out = append(out, p)
		}
	}

	slices.SortFunc(out, func(a, b history.Plan) int {
		if c := a.ExpiresAt.Compare(b.ExpiresAt); c != 0 {
			return c
		}

		return cmp.Compare(a.ID, b.ID)
	})

	return out[:min(limit, len(out))], nil
}

func (m *mem) CreateJobLog(_ context.Context, l history.JobLog, f namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return err
	}

	if _, ok := m.logs[l.JobID]; ok {
		return history.ErrJobLogExists
	}

	m.logs[l.JobID] = &memLog{log: l, chunks: nil}

	return nil
}

// logOf returns the log of jobID in ns. The caller holds the lock.
func (m *mem) AppendChunk(
	_ context.Context,
	ns namespace.Name,
	jobID string,
	c history.Chunk,
	f namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return err
	}

	l, err := m.logOf(ns, jobID)
	if err != nil {
		return err
	}

	switch {
	case c.Seq < int64(len(l.chunks)):
		if l.chunks[c.Seq].SHA256 != c.SHA256 {
			return history.ErrChunkConflict
		}

		return nil
	case l.log.Closed():
		return history.ErrJobLogClosed
	case c.Seq != int64(len(l.chunks)):
		return history.ErrChunkOutOfOrder
	default:
		l.chunks = append(l.chunks, c)

		return nil
	}
}

func (m *mem) CloseJobLog(
	_ context.Context, ns namespace.Name, jobID string, totals history.JobLogTotals, f namespace.Fence,
) (history.JobLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return history.JobLog{}, err
	}

	l, err := m.logOf(ns, jobID)
	if err != nil {
		return history.JobLog{}, err
	}

	if !l.log.Closed() {
		l.log.ClosedAt = totals.ClosedAt
		l.log.Size = totals.Size
		l.log.SHA256 = totals.SHA256
		l.log.Chunks = int64(len(l.chunks))
	}

	return l.log, nil
}

func (m *mem) GetJobLog(_ context.Context, ns namespace.Name, jobID string) (history.JobLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.logOf(ns, jobID)
	if err != nil {
		return history.JobLog{}, err
	}

	return l.log, nil
}

func (m *mem) ListChunks(
	_ context.Context,
	ns namespace.Name,
	jobID string,
	after int64,
	limit int,
) ([]history.Chunk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, err := m.logOf(ns, jobID)
	if err != nil {
		return nil, err
	}

	start := min(after+1, int64(len(l.chunks)))
	out := slices.Clone(l.chunks[start:])

	return out[:min(limit, len(out))], nil
}

func (m *mem) ListExpiredJobLogs(_ context.Context, now time.Time, limit int) ([]history.JobLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []history.JobLog

	for _, l := range m.logs {
		if l.log.Closed() && l.log.Redaction == "" && !l.log.ExpiresAt.After(now) {
			out = append(out, l.log)
		}
	}

	slices.SortFunc(out, func(a, b history.JobLog) int {
		if c := a.ExpiresAt.Compare(b.ExpiresAt); c != 0 {
			return c
		}

		return cmp.Compare(a.JobID, b.JobID)
	})

	return out[:min(limit, len(out))], nil
}

func (m *mem) fenced(f namespace.Fence) error {
	ns, ok := m.namespaces[f.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != f.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

func (m *mem) shredLocked(ref secret.ContentRef, at time.Time) (secret.ContentKey, error) {
	k, ok := m.keys[ref]
	if !ok {
		return secret.ContentKey{}, secret.ErrKeyNotFound
	}

	if !k.Shredded() {
		k.ShreddedAt = at
		m.keys[ref] = k
	}

	return k, nil
}

func (m *mem) appendLocked(r history.Redaction, targets []string) (history.Redaction, error) {
	if err := r.Validate(); err != nil {
		return history.Redaction{}, err
	}

	for i := range m.redactions {
		stored := m.redactions[i]
		if stored.Item == r.Item {
			return stored, history.ErrRedacted
		}
	}

	r.Targets = nil
	for _, name := range slices.Concat([]string{history.LiveTarget}, targets) {
		r.Targets = append(r.Targets, history.ShredTarget{
			DoneAt:    time.Time{},
			LastTryAt: time.Time{},
			Target:    name,
			State:     history.ShredPending,
			LastError: "",
			Attempts:  0,
		})
	}

	m.redactions[r.ID] = r

	return r, nil
}

func (m *mem) target(id history.RedactionID, name string, f namespace.Fence) (history.Redaction, int, error) {
	if err := m.fenced(f); err != nil {
		return history.Redaction{}, 0, err
	}

	r, ok := m.redactions[id]
	if !ok {
		return history.Redaction{}, 0, history.ErrRedactionNotFound
	}

	i := slices.IndexFunc(r.Targets, func(t history.ShredTarget) bool { return t.Target == name })
	if i < 0 {
		return history.Redaction{}, 0, history.ErrTargetNotFound
	}

	r.Targets = slices.Clone(r.Targets)

	return r, i, nil
}

func (m *mem) markable(item history.ItemRef) (func(history.RedactionID), error) {
	switch item.Kind {
	case secret.ContentKindPlan:
		p, ok := m.plans[history.PlanID(item.Item)]
		if !ok || p.Unit != item.Unit {
			return nil, history.ErrItemNotFound
		}

		return func(id history.RedactionID) {
			if p.Redaction == "" {
				p.Redaction = id
				m.plans[p.ID] = p
			}
		}, nil
	case secret.ContentKindState:
		// State items belong to the tool stores, not to this store.
		return nil, history.ErrInvalidItem
	case secret.ContentKindJobLog:
		l, ok := m.logs[item.Item]
		if !ok || l.log.Unit != item.Unit {
			return nil, history.ErrItemNotFound
		}

		if !l.log.Closed() {
			return nil, history.ErrInProgress
		}

		return func(id history.RedactionID) {
			if l.log.Redaction == "" {
				l.log.Redaction = id
			}
		}, nil
	default:
		return nil, history.ErrItemNotFound
	}
}

func (m *mem) logOf(ns namespace.Name, jobID string) (*memLog, error) {
	l, ok := m.logs[jobID]
	if !ok || l.log.Unit.Namespace != ns {
		return nil, history.ErrJobLogNotFound
	}

	return l, nil
}
