package jobtest_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/history/historytest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// TestMemRedactor runs the history conformance suite of history.Redactor
// against the reference adapter of plans and log versions in memory, with
// the table of each item kind.
func TestMemRedactor(t *testing.T) {
	t.Parallel()

	t.Run("plans", func(t *testing.T) {
		t.Parallel()
		historytest.RunRedactor(t, historytest.JobArtifactSituations(), func(*testing.T) historytest.RedactorFixture {
			return newMemArtifacts(secret.ItemKindPlan)
		})
	})

	t.Run("job logs", func(t *testing.T) {
		t.Parallel()
		historytest.RunRedactor(t, historytest.JobLogSituations(), func(*testing.T) historytest.RedactorFixture {
			return newMemArtifacts(secret.ItemKindJobLog)
		})
	})
}

// memPlan is one row of the plans table: the plan and the state of its
// job.
type memPlan struct {
	ref  job.PlanRef
	job  job.State
	mark history.RedactionID
}

// memLogVersion is one row of the log versions table. current is true when
// no later version of its log exists.
type memLogVersion struct {
	ref     job.LogRef
	current bool
	mark    history.RedactionID
}

// memArtifacts is the reference adapter of history.Redactor for the items
// of one kind of the job model, with the item key index, the redaction log
// and the audit log that the redaction writes in its transaction. One mutex
// stands for the transaction. It checks every rule before it changes
// anything, so a redaction that fails changes nothing.
type memArtifacts struct {
	namespaces map[namespace.Name]namespace.Namespace
	keys       map[secret.ItemRef]secret.ItemKey
	redactions map[history.RedactionID]history.Redaction
	plans      map[history.ItemRef]*memPlan
	logs       map[history.ItemRef]*memLogVersion
	kind       secret.ItemKind
	entries    []audit.Entry
	mu         sync.Mutex
}

func newMemArtifacts(kind secret.ItemKind) *memArtifacts {
	return &memArtifacts{
		namespaces: map[namespace.Name]namespace.Namespace{},
		keys:       map[secret.ItemRef]secret.ItemKey{},
		redactions: map[history.RedactionID]history.Redaction{},
		plans:      map[history.ItemRef]*memPlan{},
		logs:       map[history.ItemRef]*memLogVersion{},
		kind:       kind,
		entries:    nil,
		mu:         sync.Mutex{},
	}
}

// Redact implements history.Redactor for plans and log versions, with the
// rules of job.CheckPlanRedaction and job.CheckLogRedaction.
func (m *memArtifacts) Redact(
	_ context.Context, r history.Redaction, targets []string, e audit.Entry, f namespace.Fence,
) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return history.Redaction{}, err
	}

	mark, err := m.checkLocked(r.Item, r.Cause)
	if err != nil {
		return history.Redaction{}, err
	}

	if stored, findErr := m.redactionOfLocked(r.Item); findErr == nil {
		return stored, history.ErrRedacted
	}

	if validErr := r.Validate(); validErr != nil {
		return history.Redaction{}, validErr
	}

	e.At = r.At
	if validErr := e.Validate(); validErr != nil {
		return history.Redaction{}, fmt.Errorf("audit entry: %w", validErr)
	}

	ref, err := r.Item.KeyRef()
	if err != nil {
		return history.Redaction{}, err
	}

	if _, ok := m.keys[ref]; !ok {
		return history.Redaction{}, secret.ErrKeyNotFound
	}

	k := m.keys[ref]
	if !k.Shredded() {
		k.ShreddedAt = r.At
		m.keys[ref] = k
	}

	e.Hash = m.appendEntryLocked(e, f)
	r.AuditHash = e.Hash
	r.Targets = newShredTargets(targets)
	m.redactions[r.ID] = r
	*mark = r.ID

	return r, nil
}

// Seed creates a plan or a log version of unit in the situation s, with
// its item key record. A plan is old when its job ended, and running when
// it has not. A log version is old when a later version exists, running
// when it is open, and current when it is closed and no later version
// exists.
func (m *memArtifacts) Seed(
	t *testing.T, unit history.Unit, s historytest.Situation, f namespace.Fence,
) history.ItemRef {
	t.Helper()

	unit.Tool = "opentofu"
	at := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	keyID := secret.NewItemKeyID()

	m.mu.Lock()
	n := strconv.Itoa(len(m.plans) + len(m.logs) + 1)

	var item history.ItemRef

	if m.kind != secret.ItemKindJobLog {
		item = m.seedPlanLocked(unit, n, s, at, keyID)
	} else {
		item = job.LogItem(unit, job.LogVersionIDPrefix+n)
		closed := at
		if s == historytest.SituationRunning {
			closed = time.Time{}
		}

		m.logs[item] = &memLogVersion{
			ref: job.LogRef{
				ID: item.Item, ItemKey: keyID, Version: 1, Chunks: 1, SHA256: "s1", Size: 1, CreatedAt: at,
				ClosedAt: closed, ExpiresAt: history.ExpiresAt(at, history.DefaultRetention),
				RedactedAt: time.Time{}, ReplacedBy: "", Matches: 0,
			},
			current: s == historytest.SituationCurrentLog,
			mark:    "",
		}
	}
	m.mu.Unlock()

	ref, err := item.KeyRef()
	if err != nil {
		t.Fatalf("key ref: %v", err)
	}

	_, err = m.CreateItemKey(t.Context(), secret.ItemKey{
		CreatedAt: at, ShreddedAt: time.Time{}, Ref: ref, ID: keyID, WrapperID: "builtin-1",
		Blob: secret.ItemKeyBlobInfo{MD5: [16]byte{1}, Size: 1, Version: 1, Attempt: "abcdefghij0123456789"},
	}, f)
	if err != nil {
		t.Fatalf("create item key: %v", err)
	}

	return item
}

// Mark implements historytest.RedactorFixture.
func (m *memArtifacts) Mark(_ context.Context, item history.ItemRef) (history.RedactionID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.plans[item]; ok {
		return p.mark, nil
	}

	if v, ok := m.logs[item]; ok {
		return v.mark, nil
	}

	return "", history.ErrItemNotFound
}

// AuditEntries implements historytest.RedactorFixture.
func (m *memArtifacts) AuditEntries(_ context.Context, ns namespace.Name) ([]audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []audit.Entry

	for i := range m.entries {
		if m.entries[i].Namespace == ns {
			out = append(out, m.entries[i])
		}
	}

	return out, nil
}

// CreateNamespace implements namespace.Store.
func (m *memArtifacts) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

// GetNamespace implements namespace.Store.
func (m *memArtifacts) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

// SetOverrides implements namespace.Store. The suite does not use it.
func (m *memArtifacts) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

// ListNamespaces implements namespace.Store. The suite does not use it.
func (m *memArtifacts) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

// Ping implements namespace.Store.
func (m *memArtifacts) Ping(context.Context) error { return nil }

// CreateItemKey implements secret.ItemKeyIndex.
func (m *memArtifacts) CreateItemKey(_ context.Context, k secret.ItemKey, f namespace.Fence) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return secret.ItemKey{}, err
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

// GetItemKey implements secret.ItemKeyIndex.
func (m *memArtifacts) GetItemKey(_ context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[ref]

	switch {
	case !ok:
		return secret.ItemKey{}, secret.ErrKeyNotFound
	case k.Shredded():
		return k, secret.ErrShredded
	default:
		return k, nil
	}
}

// ShredItemKey implements secret.ItemKeyIndex.
func (m *memArtifacts) ShredItemKey(
	_ context.Context, ref secret.ItemRef, at time.Time, f namespace.Fence,
) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return secret.ItemKey{}, err
	}

	k, ok := m.keys[ref]
	if !ok {
		return secret.ItemKey{}, secret.ErrKeyNotFound
	}

	if !k.Shredded() {
		k.ShreddedAt = at
		m.keys[ref] = k
	}

	return k, nil
}

// ListItemKeys implements secret.ItemKeyIndex.
func (m *memArtifacts) ListItemKeys(
	_ context.Context, scope secret.Scope, after secret.ItemKeyID, limit int,
) ([]secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []secret.ItemKey

	for ref := range m.keys {
		if k := m.keys[ref]; ref.Scope == scope && k.ID > after {
			out = append(out, k)
		}
	}

	slices.SortFunc(out, func(a, b secret.ItemKey) int { return cmp.Compare(a.ID, b.ID) })

	return out[:min(limit, len(out))], nil
}

// ListItemKeyWrapperIDs implements secret.ItemKeyIndex.
func (m *memArtifacts) ListItemKeyWrapperIDs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := []string{}

	for ref := range m.keys {
		if k := m.keys[ref]; !k.Shredded() && !slices.Contains(out, k.WrapperID) {
			out = append(out, k.WrapperID)
		}
	}

	slices.Sort(out)

	return out, nil
}

// AppendRedaction implements history.RedactionLog.
func (m *memArtifacts) AppendRedaction(
	_ context.Context, r history.Redaction, targets []string, f namespace.Fence,
) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return history.Redaction{}, err
	}

	if err := r.Validate(); err != nil {
		return history.Redaction{}, err
	}

	if stored, err := m.redactionOfLocked(r.Item); err == nil {
		return stored, history.ErrRedacted
	}

	r.Targets = newShredTargets(targets)
	m.redactions[r.ID] = r

	return r, nil
}

// GetRedaction implements history.RedactionLog.
func (m *memArtifacts) GetRedaction(
	_ context.Context, ns namespace.Name, id history.RedactionID,
) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.redactions[id]
	if !ok || r.Item.Unit.Namespace != ns {
		return history.Redaction{}, history.ErrRedactionNotFound
	}

	return r, nil
}

// RedactionOf implements history.RedactionLog.
func (m *memArtifacts) RedactionOf(_ context.Context, item history.ItemRef) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.redactionOfLocked(item)
}

// ListRedactions implements history.RedactionLog: newest first by time,
// then by ID.
func (m *memArtifacts) ListRedactions(
	_ context.Context, unit history.Unit, after *history.RedactionCursor, limit int,
) ([]history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	newestFirst := func(a, b history.Redaction) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}

		return cmp.Compare(b.ID, a.ID)
	}

	var out []history.Redaction

	for id := range m.redactions {
		r := m.redactions[id]
		if r.Item.Unit != unit {
			continue
		}

		if after != nil && !afterCursor(r, *after) {
			continue
		}

		out = append(out, r)
	}

	slices.SortFunc(out, newestFirst)

	return out[:min(limit, len(out))], nil
}

// afterCursor reports whether r comes after the cursor c, newest first.
func afterCursor(r history.Redaction, c history.RedactionCursor) bool {
	if n := c.At.Compare(r.At); n != 0 {
		return n > 0
	}

	return c.ID > r.ID
}

// ListPendingShreds implements history.RedactionLog.
func (m *memArtifacts) ListPendingShreds(
	_ context.Context, after history.RedactionID, limit int,
) ([]history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []history.Redaction

	for id := range m.redactions {
		if r := m.redactions[id]; id > after && r.Pending() {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b history.Redaction) int { return cmp.Compare(a.ID, b.ID) })

	return out[:min(limit, len(out))], nil
}

// FinishShred implements history.RedactionLog.
func (m *memArtifacts) FinishShred(
	_ context.Context, id history.RedactionID, name string, at time.Time, f namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, i, err := m.targetLocked(id, name, f)
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

// NoteShredFailure implements history.RedactionLog.
func (m *memArtifacts) NoteShredFailure(_ context.Context, sf history.ShredFailure, f namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, i, err := m.targetLocked(sf.ID, sf.Target, f)
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

// RequeueShreds implements history.RedactionLog: each target named in
// names is pending again, with its count of tries.
func (m *memArtifacts) RequeueShreds(_ context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.redactions {
		r := m.redactions[id]
		r.Targets = slices.Clone(r.Targets)

		for i := range r.Targets {
			if slices.Contains(names, r.Targets[i].Target) {
				r.Targets[i].State = history.ShredPending
				r.Targets[i].DoneAt = time.Time{}
			}
		}

		m.redactions[id] = r
	}

	return nil
}

// seedPlanLocked creates a plan of unit in the situation s: of a job that
// ended for SituationOld, of a job that waits for SituationRunning. The
// caller holds the lock.
func (m *memArtifacts) seedPlanLocked(
	unit history.Unit, n string, s historytest.Situation, at time.Time, keyID secret.ItemKeyID,
) history.ItemRef {
	item := job.PlanItem(unit, job.PlanIDPrefix+n)
	state := job.StateSucceeded
	if s == historytest.SituationRunning {
		state = job.StateAwaitingApproval
	}

	m.plans[item] = &memPlan{
		ref: job.PlanRef{
			ID: item.Item, ItemKey: keyID, StateVersion: "sv-1", Parts: nil, CreatedAt: at,
			ExpiresAt: history.ExpiresAt(at, history.DefaultRetention), RedactedAt: time.Time{},
		},
		job:  state,
		mark: "",
	}

	return item
}

// checkLocked returns the mark of item, or the error of the rule of the job
// model for a redaction of it with the cause c. The caller holds the lock.
func (m *memArtifacts) checkLocked(item history.ItemRef, c history.Cause) (*history.RedactionID, error) {
	if p, ok := m.plans[item]; ok && item.Kind == secret.ItemKindPlan {
		if p.mark == "" {
			if err := job.CheckPlanRedaction(p.job); err != nil {
				return nil, err
			}
		}

		return &p.mark, nil
	}

	if v, ok := m.logs[item]; ok && item.Kind == secret.ItemKindJobLog {
		if v.mark == "" {
			if err := job.CheckLogRedaction(&v.ref, v.current, c); err != nil {
				return nil, err
			}
		}

		return &v.mark, nil
	}

	return nil, history.ErrItemNotFound
}

// fenced answers namespace.ErrFenced for a stale fence. The caller holds
// the lock.
func (m *memArtifacts) fenced(f namespace.Fence) error {
	ns, ok := m.namespaces[f.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != f.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// redactionOfLocked returns the record of item, or ErrRedactionNotFound.
// The caller holds the lock.
func (m *memArtifacts) redactionOfLocked(item history.ItemRef) (history.Redaction, error) {
	for id := range m.redactions {
		if r := m.redactions[id]; r.Item == item {
			return r, nil
		}
	}

	return history.Redaction{}, history.ErrRedactionNotFound
}

// targetLocked returns the record id with a copy of its targets, and the
// index of the target name. The caller holds the lock.
func (m *memArtifacts) targetLocked(
	id history.RedactionID, name string, f namespace.Fence,
) (history.Redaction, int, error) {
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

// appendEntryLocked stores e as the next entry of its namespace and returns
// its hash. The caller holds the lock and has validated e.
func (m *memArtifacts) appendEntryLocked(e audit.Entry, f namespace.Fence) string {
	prev := ""

	for i := range m.entries {
		if stored := m.entries[i]; stored.Namespace == e.Namespace {
			prev = stored.Hash
			e.Sequence = stored.Sequence
		}
	}

	e.Sequence++
	e.PrevHash = prev
	e.FenceVersion = f.FailoverVersion
	e.Hash = audit.ComputeHash(e, prev)
	m.entries = append(m.entries, e)

	return e.Hash
}

// newShredTargets returns a pending shred target for history.LiveTarget,
// then one for each name in targets.
func newShredTargets(targets []string) []history.ShredTarget {
	out := make([]history.ShredTarget, 0, len(targets)+1)

	for _, name := range slices.Concat([]string{history.LiveTarget}, targets) {
		out = append(out, history.ShredTarget{
			DoneAt: time.Time{}, LastTryAt: time.Time{}, Target: name, State: history.ShredPending,
			LastError: "", Attempts: 0,
		})
	}

	return out
}

// TestRedactionRules checks the rules of the job model for a redaction of
// a plan and of a log version, with each cause.
func TestRedactionRules(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	closed := job.LogRef{
		ID: "jlv-1", ItemKey: "ik-1", Version: 1, Chunks: 1, SHA256: "s1", Size: 1, CreatedAt: at,
		ClosedAt: at, ExpiresAt: history.ExpiresAt(at, history.DefaultRetention), RedactedAt: time.Time{},
		ReplacedBy: "", Matches: 0,
	}
	open := closed
	open.ClosedAt = time.Time{}

	logs := []struct {
		name    string
		v       job.LogRef
		current bool
		cause   history.Cause
		want    error
	}{
		{"an older version on request", closed, false, history.CauseRequest, nil},
		{"the current version on request", closed, true, history.CauseRequest, history.ErrLatest},
		{"the current version at the end of retention", closed, true, history.CauseRetention, nil},
		{"an open version on request", open, true, history.CauseRequest, history.ErrInProgress},
		{"an open version at the end of retention", open, true, history.CauseRetention, history.ErrInProgress},
	}

	for _, tt := range logs {
		t.Run("log: "+tt.name, func(t *testing.T) {
			t.Parallel()

			if err := job.CheckLogRedaction(&tt.v, tt.current, tt.cause); !errors.Is(err, tt.want) ||
				(tt.want == nil && err != nil) {
				t.Errorf("CheckLogRedaction() = %v, want %v", err, tt.want)
			}
		})
	}

	for _, s := range []job.State{job.StateQueued, job.StateAwaitingApproval, job.StateRunning, job.StateCanceling} {
		if err := job.CheckPlanRedaction(s); !errors.Is(err, history.ErrInProgress) {
			t.Errorf("CheckPlanRedaction(%s) = %v, want ErrInProgress", s, err)
		}
	}

	for _, s := range []job.State{job.StateSucceeded, job.StateFailed, job.StateCanceled, job.StateExpired} {
		if err := job.CheckPlanRedaction(s); err != nil {
			t.Errorf("CheckPlanRedaction(%s) = %v, want nil", s, err)
		}
	}
}
