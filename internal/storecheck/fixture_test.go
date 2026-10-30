package storecheck_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
)

// memBlobs is a blob.Store in memory. With enforce it refuses a second write
// of a key with ErrExists, as a store with conditional writes does. Hooks let
// a test make a store fail.
type memBlobs struct {
	mu      sync.Mutex
	objects map[blob.Key][]byte
	calls   []string
	enforce bool
	// getErr and deleteErr, when they are not nil, are returned by Get and
	// Delete for every key.
	getErr    error
	deleteErr error
	// putFail, when it is not nil, can fail a Put before it stores.
	putFail func(key blob.Key) error
}

func newMemBlobs(enforce bool) *memBlobs {
	return &memBlobs{
		mu:        sync.Mutex{},
		objects:   map[blob.Key][]byte{},
		calls:     nil,
		enforce:   enforce,
		getErr:    nil,
		deleteErr: nil,
		putFail:   nil,
	}
}

func (m *memBlobs) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read the object: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.note("put", key)

	if m.putFail != nil {
		if err := m.putFail(key); err != nil {
			return err
		}
	}

	if _, ok := m.objects[key]; ok && m.enforce {
		return blob.ErrExists
	}

	m.objects[key] = data

	return nil
}

func (m *memBlobs) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.note("get", key)

	if m.getErr != nil {
		return nil, m.getErr
	}

	data, ok := m.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memBlobs) Delete(_ context.Context, key blob.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.note("delete", key)

	if m.deleteErr != nil {
		return m.deleteErr
	}

	delete(m.objects, key)

	return nil
}

func (m *memBlobs) List(_ context.Context, prefix string) ([]blob.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.note("list", blob.Key(prefix))

	var out []blob.Object

	for key := range m.objects {
		if strings.HasPrefix(key.String(), prefix) {
			out = append(out, blob.Object{ModifiedAt: time.Time{}, Key: key})
		}
	}

	slices.SortFunc(out, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return out, nil
}

func (m *memBlobs) Ping(context.Context) error { return nil }

func (m *memBlobs) note(call string, key blob.Key) {
	m.calls = append(m.calls, call+" "+key.String())
}

func (m *memBlobs) seed(keys ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range keys {
		m.objects[blob.Key(key)] = []byte("seed")
	}
}

func (m *memBlobs) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.objects))
	for key := range m.objects {
		out = append(out, key.String())
	}

	slices.Sort(out)

	return out
}

func (m *memBlobs) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.calls)
}

// memRecords is storecheck.Records in memory, on a durable store.
type memRecords struct {
	mu   sync.Mutex
	ops  durable.Store
	rows map[storecheck.ID]*storecheck.Record
}

func newMemRecords(ops durable.Store) *memRecords {
	return &memRecords{mu: sync.Mutex{}, ops: ops, rows: map[storecheck.ID]*storecheck.Record{}}
}

func (m *memRecords) StartCheck(
	ctx context.Context, r storecheck.Record, start durable.StartRequest, coolingSince time.Time,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.rows[r.ID]; ok {
		return storecheck.ErrExists
	}

	for _, row := range m.rows {
		if row.State == storecheck.StateRunning {
			return storecheck.ErrRunning
		}

		if row.Target == r.Target && row.EndedAt.After(coolingSince) {
			return storecheck.ErrCooldown
		}
	}

	if _, created, err := m.ops.Start(ctx, start); err != nil || !created {
		return durable.ErrKeyConflict
	}

	m.rows[r.ID] = &r

	return nil
}

func (m *memRecords) GetCheck(_ context.Context, id storecheck.ID) (storecheck.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	row, ok := m.rows[id]
	if !ok {
		return storecheck.Record{}, storecheck.ErrNotFound
	}

	return *row, nil
}

func (m *memRecords) ListChecks(context.Context) ([]storecheck.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]storecheck.Record, 0, len(m.rows))
	for _, row := range m.rows {
		out = append(out, *row)
	}

	slices.SortFunc(out, func(a, b storecheck.Record) int { return b.StartedAt.Compare(a.StartedAt) })

	return out, nil
}

func (m *memRecords) FinishCheck(
	ctx context.Context,
	f durable.StepFence,
	id storecheck.ID,
	end storecheck.Finish,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ops.RecordStep(ctx, f, nil); err != nil {
		return fmt.Errorf("read the object: %w", err)
	}

	row, ok := m.rows[id]
	if !ok {
		return storecheck.ErrNotFound
	}

	if row.State != storecheck.StateRunning {
		return storecheck.ErrNotRunning
	}

	row.State, row.EndedAt, row.Error, row.Report = end.State, end.EndedAt, end.Error, end.Report

	return nil
}

func (m *memRecords) FailEndedChecks(context.Context, time.Time) (int, error) { return 0, nil }

// memAudit records the entries.
type memAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *memAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, fmt.Errorf("validate the entry: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

func (a *memAudit) all() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.entries)
}

// rig is a service with its fakes.
type rig struct {
	svc    *storecheck.Service
	blobs  *memBlobs
	backup *memBlobs
	rows   *memRecords
	audit  *memAudit
	ops    durable.Store
	admit  error
	clock  *time.Time
}

func newRig(t *testing.T, mutate func(*storecheck.Config)) *rig {
	t.Helper()

	ops := durabletest.NewMem().Store()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	r := &rig{
		svc: nil, blobs: newMemBlobs(true), backup: newMemBlobs(false), rows: newMemRecords(ops),
		audit: &memAudit{mu: sync.Mutex{}, entries: nil}, ops: ops, admit: nil, clock: &now,
	}

	cfg := storecheck.Config{
		Records: r.rows, Operations: ops, Blobs: r.blobs, Backup: r.backup, Audit: r.audit,
		Admit:  func(context.Context) error { return r.admit },
		Limits: storecheck.Limits{MaxObjects: 0, MaxTotalBytes: 0, MaxObjectBytes: 0, Timeout: 0, Cooldown: 0},
		Now:    func() time.Time { return *r.clock }, Owner: "test", Lease: 0, Logger: nil,
	}

	if mutate != nil {
		mutate(&cfg)
	}

	svc, err := storecheck.NewService(cfg)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	r.svc = svc

	return r
}

func (r *rig) actor() storecheck.Actor {
	return storecheck.Actor{Subject: "admin", TokenID: "", PrincipalKind: audit.PrincipalKindPerson}
}

// request returns the start request of target for the actor of the rig.
func (r *rig) request(target string) storecheck.StartRequest {
	return storecheck.StartRequest{Target: target, Actor: r.actor(), Entry: zeroEntry()}
}

// zeroEntry returns an audit entry with no field set.
func zeroEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: "",
		Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// start starts a check of target and fails the test on an error.
func (r *rig) start(t *testing.T, target string) storecheck.ID {
	t.Helper()

	id, err := r.svc.Start(t.Context(), r.request(target))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return id
}

// runOne runs one operation to its end.
func (r *rig) runOne(t *testing.T) {
	t.Helper()

	if err := r.svc.RunOnce(t.Context()); err != nil {
		if errors.Is(err, durable.ErrNoWork) {
			t.Fatal("no operation to run")
		}

		t.Fatalf("run once: %v", err)
	}
}

// listRows returns every row of the service, and fails the test on an error.
func (r *rig) listRows(t *testing.T) []storecheck.Record {
	t.Helper()

	rows, err := r.svc.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return rows
}

// report returns the row of check id and its report, or nil without a report.
func (r *rig) report(t *testing.T, id storecheck.ID) (storecheck.Record, *storecheck.Report) {
	t.Helper()

	rec, err := r.svc.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	report, ok, err := storecheck.DecodeReport(rec)
	if err != nil {
		t.Fatalf("decode report: %v", err)
	}

	if !ok {
		return rec, nil
	}

	return rec, &report
}
