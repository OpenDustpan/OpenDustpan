package historytest_test

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/history/historytest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// TestSuites runs every suite of the package against an in-memory store, so
// the cases state their rules before a SQL adapter implements the ports.
func TestSuites(t *testing.T) {
	t.Parallel()

	t.Run("redactions", func(t *testing.T) {
		t.Parallel()
		historytest.RunRedactions(t, func(*testing.T) historytest.RedactionStore {
			return newMem("opentofu", secret.ItemKindState)
		})
	})

	t.Run("tombstones", func(t *testing.T) {
		t.Parallel()
		historytest.RunTombstones(t, func(*testing.T) historytest.TombstoneStore {
			return newMem("opentofu", secret.ItemKindState)
		})
	})

	tools := []struct {
		name       string
		tool       string
		kind       secret.ItemKind
		situations []historytest.Situation
	}{
		{
			name:       "pulumi redactor",
			tool:       "pulumi",
			kind:       secret.ItemKindState,
			situations: historytest.PulumiSituations(),
		},
		{
			name:       "opentofu redactor",
			tool:       "opentofu",
			kind:       secret.ItemKindState,
			situations: historytest.OpenTofuSituations(),
		},
		{
			name:       "job artifact redactor",
			tool:       "opentofu",
			kind:       secret.ItemKindPlan,
			situations: historytest.JobArtifactSituations(),
		},
		{
			name:       "job log redactor",
			tool:       "opentofu",
			kind:       secret.ItemKindJobLog,
			situations: historytest.JobLogSituations(),
		},
	}

	for _, tc := range tools {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			historytest.RunRedactor(t, tc.situations, func(*testing.T) historytest.RedactorFixture {
				return newMem(tc.tool, tc.kind)
			})
		})
	}
}

// memItem is one row of the tool table of the in-memory store.
type memItem struct {
	situation historytest.Situation
	mark      history.RedactionID
}

// mem keeps every port of the suites in memory, under one lock, as one SQL
// transaction would. It plays one tool table: the items of one tool and one
// kind.
type mem struct {
	namespaces map[namespace.Name]namespace.Namespace
	keys       map[secret.ItemRef]secret.ItemKey
	redactions map[history.RedactionID]history.Redaction
	tombstones map[history.TombstoneID]history.Tombstone
	items      map[history.ItemRef]*memItem
	tool       string
	kind       secret.ItemKind
	entries    []audit.Entry
	mu         sync.Mutex
}

func newMem(tool string, kind secret.ItemKind) *mem {
	return &mem{
		namespaces: map[namespace.Name]namespace.Namespace{},
		keys:       map[secret.ItemRef]secret.ItemKey{},
		redactions: map[history.RedactionID]history.Redaction{},
		tombstones: map[history.TombstoneID]history.Tombstone{},
		items:      map[history.ItemRef]*memItem{},
		tool:       tool,
		kind:       kind,
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

func (m *mem) CreateItemKey(_ context.Context, k secret.ItemKey, f namespace.Fence) (secret.ItemKey, error) {
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

func (m *mem) GetItemKey(_ context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
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

func (m *mem) ShredItemKey(
	_ context.Context, ref secret.ItemRef, at time.Time, f namespace.Fence,
) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return secret.ItemKey{}, err
	}

	return m.shredLocked(ref, at)
}

func (m *mem) ListItemKeys(context.Context, secret.Scope, secret.ItemKeyID, int) ([]secret.ItemKey, error) {
	return nil, nil
}

func (m *mem) ListItemKeyWrapperIDs(context.Context) ([]string, error) { return nil, nil }

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

	return m.redactionOfLocked(item)
}

// newestFirstAt orders r against the cursor c, newest first.
func newestFirstAt(r history.Redaction, c history.RedactionCursor) int {
	if n := c.At.Compare(r.At); n != 0 {
		return n
	}

	return cmp.Compare(c.ID, r.ID)
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

func (m *mem) RequeueShreds(_ context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.redactions {
		r := m.redactions[id]
		r.Targets = requeue(r.Targets, names)
		m.redactions[id] = r
	}

	return nil
}

// requeue returns a copy of targets with each target named in names set
// to pending again.
func requeue(targets []history.ShredTarget, names []string) []history.ShredTarget {
	out := slices.Clone(targets)

	for i := range out {
		if slices.Contains(names, out[i].Target) {
			out[i].State = history.ShredPending
			out[i].DoneAt = time.Time{}
		}
	}

	return out
}

func (m *mem) AppendTombstone(
	_ context.Context, tb history.Tombstone, targets []string, f namespace.Fence,
) (history.Tombstone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return history.Tombstone{}, err
	}

	if err := tb.Validate(); err != nil {
		return history.Tombstone{}, err
	}

	if stored, err := m.tombstoneOfLocked(tb.Unit); err == nil {
		return stored, history.ErrPurged
	}

	tb.Targets = newTargets(targets)
	m.tombstones[tb.ID] = tb

	return tb, nil
}

func (m *mem) TombstoneOf(_ context.Context, unit history.Unit) (history.Tombstone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.tombstoneOfLocked(unit)
}

func (m *mem) ListTombstones(_ context.Context, after history.TombstoneID, limit int) ([]history.Tombstone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return pageTombstones(m.tombstones, after, limit, func(history.Tombstone) bool { return true }), nil
}

func (m *mem) ListPendingTombstones(
	_ context.Context, after history.TombstoneID, limit int,
) ([]history.Tombstone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return pageTombstones(m.tombstones, after, limit, history.Tombstone.Pending), nil
}

func (m *mem) FinishTombstone(_ context.Context, id history.TombstoneID, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tb, i, err := m.tombstoneTarget(id, name)
	if err != nil {
		return err
	}

	if tb.Targets[i].State != history.ShredDone {
		tb.Targets[i].State = history.ShredDone
		tb.Targets[i].DoneAt = at
		m.tombstones[id] = tb
	}

	return nil
}

func (m *mem) NoteTombstoneFailure(_ context.Context, tf history.TombstoneFailure) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tb, i, err := m.tombstoneTarget(tf.ID, tf.Target)
	if err != nil {
		return err
	}

	if tb.Targets[i].State != history.ShredDone {
		tb.Targets[i].Attempts++
		tb.Targets[i].LastError = tf.Text
		tb.Targets[i].LastTryAt = tf.At
		m.tombstones[tf.ID] = tb
	}

	return nil
}

func (m *mem) RequeueTombstones(_ context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.tombstones {
		tb := m.tombstones[id]
		tb.Targets = requeue(tb.Targets, names)
		m.tombstones[id] = tb
	}

	return nil
}

// Redact redacts an item of the tool table in one step under the lock: it
// checks everything first, then changes the table, the key, the log and the
// audit entries together.
func (m *mem) Redact(
	_ context.Context, r history.Redaction, targets []string, e audit.Entry, f namespace.Fence,
) (history.Redaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(f); err != nil {
		return history.Redaction{}, err
	}

	item, ok := m.items[r.Item]
	if !ok {
		return history.Redaction{}, history.ErrItemNotFound
	}

	if stored, findErr := m.redactionOfLocked(r.Item); findErr == nil {
		return stored, history.ErrRedacted
	}

	if err := refusal(item.situation, r.Cause); err != nil {
		return history.Redaction{}, err
	}

	if err := r.Validate(); err != nil {
		return history.Redaction{}, err
	}

	e.At = r.At
	if err := e.Validate(); err != nil {
		return history.Redaction{}, fmt.Errorf("audit entry: %w", err)
	}

	ref, err := r.Item.KeyRef()
	if err != nil {
		return history.Redaction{}, err
	}

	if _, shredErr := m.shredLocked(ref, r.At); shredErr != nil {
		return history.Redaction{}, shredErr
	}

	e.Hash = m.appendEntryLocked(e, f)
	r.AuditHash = e.Hash

	stored, err := m.appendLocked(r, targets)
	if err != nil {
		return history.Redaction{}, err
	}

	item.mark = r.ID

	return stored, nil
}

// Seed creates an item of the tool table in the situation s, with its item
// key record.
func (m *mem) Seed(
	t *testing.T, unit history.Unit, s historytest.Situation, f namespace.Fence,
) history.ItemRef {
	t.Helper()

	unit.Tool = m.tool

	m.mu.Lock()
	item := history.ItemRef{Unit: unit, Kind: m.kind, Item: "item-" + strconv.Itoa(len(m.items)+1)}
	m.items[item] = &memItem{situation: s, mark: ""}
	m.mu.Unlock()

	ref, err := item.KeyRef()
	if err != nil {
		t.Fatalf("key ref: %v", err)
	}

	_, err = m.CreateItemKey(t.Context(), secret.ItemKey{
		CreatedAt: time.Time{}, ShreddedAt: time.Time{}, Ref: ref, ID: secret.NewItemKeyID(), WrapperID: "builtin-1",
		Blob: secret.ItemKeyBlobInfo{MD5: [16]byte{1}, Size: 1, Version: 1, Attempt: "abcdefghij0123456789"},
	}, f)
	if err != nil {
		t.Fatalf("create item key: %v", err)
	}

	return item
}

func (m *mem) Mark(_ context.Context, item history.ItemRef) (history.RedactionID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, ok := m.items[item]
	if !ok {
		return "", history.ErrItemNotFound
	}

	return stored.mark, nil
}

func (m *mem) AuditEntries(_ context.Context, ns namespace.Name) ([]audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []audit.Entry

	for i := range m.entries {
		e := m.entries[i]
		if e.Namespace == ns {
			out = append(out, e)
		}
	}

	return out, nil
}

// refusal returns the error of a redaction with the cause c of an item in
// the situation s, or nil when the item can be redacted.
func refusal(s historytest.Situation, c history.Cause) error {
	switch s {
	case historytest.SituationOld:
		return nil
	case historytest.SituationCurrentLog:
		if c == history.CauseRetention {
			return nil
		}

		return history.ErrLatest
	case historytest.SituationLatest, historytest.SituationReplayBase:
		return history.ErrLatest
	case historytest.SituationRunning, historytest.SituationUpdateActive:
		return history.ErrInProgress
	case historytest.SituationBlamePending:
		return history.ErrBlamePending
	default:
		return fmt.Errorf("%w: situation %q", history.ErrInvalidItem, s)
	}
}

// fenced answers namespace.ErrFenced for a stale fence. The caller holds the
// lock.
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

// shredLocked marks the key of ref shredded. The caller holds the lock.
func (m *mem) shredLocked(ref secret.ItemRef, at time.Time) (secret.ItemKey, error) {
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

// redactionOfLocked returns the record of item, or ErrRedactionNotFound.
// The caller holds the lock.
func (m *mem) redactionOfLocked(item history.ItemRef) (history.Redaction, error) {
	for n := range m.redactions {
		r := m.redactions[n]
		if r.Item == item {
			return r, nil
		}
	}

	return history.Redaction{}, history.ErrRedactionNotFound
}

// appendEntryLocked stores e as the next entry of its namespace and returns
// its hash. The caller holds the lock and has validated e.
func (m *mem) appendEntryLocked(e audit.Entry, f namespace.Fence) string {
	prev := ""

	for i := range m.entries {
		stored := m.entries[i]
		if stored.Namespace == e.Namespace {
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

// appendLocked appends r. The caller holds the lock.
func (m *mem) appendLocked(r history.Redaction, targets []string) (history.Redaction, error) {
	if err := r.Validate(); err != nil {
		return history.Redaction{}, err
	}

	if stored, err := m.redactionOfLocked(r.Item); err == nil {
		return stored, history.ErrRedacted
	}

	r.Targets = newTargets(targets)
	m.redactions[r.ID] = r

	return r, nil
}

// target returns the record id and the index of its target name. The
// caller holds the lock.
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

// newTargets returns a pending shred target for LiveTarget, then one for
// each name in targets.
func newTargets(targets []string) []history.ShredTarget {
	out := make([]history.ShredTarget, 0, len(targets)+1)

	for _, name := range slices.Concat([]string{history.LiveTarget}, targets) {
		out = append(out, history.ShredTarget{
			DoneAt:    time.Time{},
			LastTryAt: time.Time{},
			Target:    name,
			State:     history.ShredPending,
			LastError: "",
			Attempts:  0,
		})
	}

	return out
}

// tombstoneOfLocked returns the tombstone of unit, or ErrTombstoneNotFound.
// The caller holds the lock.
func (m *mem) tombstoneOfLocked(unit history.Unit) (history.Tombstone, error) {
	for id := range m.tombstones {
		tb := m.tombstones[id]
		if tb.Unit == unit {
			return tb, nil
		}
	}

	return history.Tombstone{}, history.ErrTombstoneNotFound
}

// tombstoneTarget returns the tombstone id and the index of its target
// name. The caller holds the lock.
func (m *mem) tombstoneTarget(id history.TombstoneID, name string) (history.Tombstone, int, error) {
	tb, ok := m.tombstones[id]
	if !ok {
		return history.Tombstone{}, 0, history.ErrTombstoneNotFound
	}

	i := slices.IndexFunc(tb.Targets, func(t history.ShredTarget) bool { return t.Target == name })
	if i < 0 {
		return history.Tombstone{}, 0, history.ErrTargetNotFound
	}

	tb.Targets = slices.Clone(tb.Targets)

	return tb, i, nil
}

// pageTombstones returns at most limit tombstones of all after the ID
// after, ordered by ID, that keep accepts. The caller holds the lock.
func pageTombstones(
	all map[history.TombstoneID]history.Tombstone, after history.TombstoneID, limit int,
	keep func(history.Tombstone) bool,
) []history.Tombstone {
	var out []history.Tombstone

	for id := range all {
		tb := all[id]
		if id > after && keep(tb) {
			out = append(out, tb)
		}
	}

	slices.SortFunc(out, func(a, b history.Tombstone) int { return cmp.Compare(a.ID, b.ID) })

	return out[:min(limit, len(out))]
}
