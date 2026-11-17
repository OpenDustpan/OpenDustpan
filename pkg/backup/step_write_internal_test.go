package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
)

// stepID is the backup of the step-write tests.
const stepID = ID("bkp-step-write")

// stepTarget is an in-memory blob store for the step-write tests.
type stepTarget struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newStepTarget() *stepTarget {
	return &stepTarget{mu: sync.Mutex{}, objects: map[string][]byte{}}
}

func (m *stepTarget) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.objects[key.String()] = data

	return nil
}

func (m *stepTarget) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key.String()]
	if !ok {
		return nil, fmt.Errorf("get %s: %w", key, blob.ErrNotFound)
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *stepTarget) Delete(ctx context.Context, key blob.Key) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key.String())

	return nil
}

func (m *stepTarget) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, fmt.Errorf("list %q: %w", req.Prefix, err)
	}

	if err := ctx.Err(); err != nil {
		return blob.Page{}, fmt.Errorf("list %q: %w", req.Prefix, err)
	}

	dir := strings.TrimSuffix(req.Prefix, "/") + "/"

	m.mu.Lock()
	defer m.mu.Unlock()

	var objects []blob.Object

	for name := range m.objects {
		if !strings.HasPrefix(name, dir) {
			continue
		}

		key, err := blob.ParseKey(name)
		if err != nil {
			return blob.Page{}, fmt.Errorf("list %q: %w", req.Prefix, err)
		}

		objects = append(objects, blob.Object{Key: key, ModifiedAt: time.Time{}})
	}

	slices.SortFunc(objects, func(a, b blob.Object) int {
		return strings.Compare(a.Key.String(), b.Key.String())
	})

	return blob.PageOf(objects, req), nil
}

func (m *stepTarget) Ping(context.Context) error { return nil }

func (m *stepTarget) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.objects[key]

	return ok
}

// stepOps returns one running operation with the live token live. A fence
// with another token loses the claim.
type stepOps struct {
	durable.Store

	mu   sync.Mutex
	live int64
	op   durable.Operation
}

func newStepOps(op durable.Operation, live int64) *stepOps {
	return &stepOps{Store: nil, mu: sync.Mutex{}, live: live, op: op}
}

func (f *stepOps) Get(context.Context, durable.ID) (durable.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	op := f.op
	op.State = durable.StateRunning
	op.Claim = durable.Claim{Operation: op.ID, Owner: "test", Token: f.live, ExpiresAt: claimTestTime()}
	op.CancelRequested = false

	return op, nil
}

func (f *stepOps) setLive(token int64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.live = token
}

// stepRegistry records the attempt of one backup. A second record of another
// attempt never overwrites the first, as the store does.
type stepRegistry struct {
	Registry

	mu   sync.Mutex
	rows map[ID]Record
}

func newStepRegistry() *stepRegistry {
	return &stepRegistry{Registry: nil, mu: sync.Mutex{}, rows: map[ID]Record{}}
}

func (r *stepRegistry) RecordBackupAttempt(
	_ context.Context, _ durable.StepFence, id ID, attempt string,
) error {
	if err := ValidAttempt(attempt); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id]
	if !ok {
		return ErrNotFound
	}

	if row.State != StateRunning {
		return ErrNotRunning
	}

	if row.Attempt != "" && row.Attempt != attempt {
		return durable.ErrClaimLost
	}

	row.Attempt = attempt
	r.rows[id] = row

	return nil
}

// FinishBackup ends the running row id with state, as the registry does for a
// failed finish. A succeeded finish is not used by the tests of this fake.
//
//nolint:revive // The method implements backup.Registry, whose FinishBackup signature has seven parameters.
func (r *stepRegistry) FinishBackup(
	_ context.Context, _ durable.StepFence, id ID, state State, errText string, endedAt time.Time, _ string,
) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id]
	if !ok {
		return Record{}, ErrNotFound
	}

	if row.State != StateRunning {
		return Record{}, ErrNotRunning
	}

	row.State = state
	row.Error = errText
	row.EndedAt = endedAt
	r.rows[id] = row

	return row, nil
}

func (r *stepRegistry) GetBackup(_ context.Context, id ID) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id]
	if !ok {
		return Record{}, ErrNotFound
	}

	return row, nil
}

// stepExporter writes one small table file per table.
type stepExporter struct{}

func (stepExporter) ExportPortable(_ context.Context, sink PortableSink) (Summary, []PortableFile, error) {
	files := make([]PortableFile, 0, len(Tables))

	for _, table := range Tables {
		path := PortablePath(table)
		data := []byte("step-write-table")

		w, err := sink(path)
		if err != nil {
			return Summary{}, nil, fmt.Errorf("open test table file: %w", err)
		}

		if _, err := w.Write(data); err != nil {
			return Summary{}, nil, fmt.Errorf("write test table file: %w", err)
		}

		if err := w.Close(); err != nil {
			return Summary{}, nil, fmt.Errorf("close test table file: %w", err)
		}

		digest := sha256.Sum256(data)
		files = append(files, PortableFile{
			Table: table, Path: path, SHA256: hex.EncodeToString(digest[:]),
			Columns: nil, Rows: 0, Size: int64(len(data)),
		})
	}

	counts := make(map[string]int64, len(Tables))
	for _, table := range Tables {
		counts[table] = 0
	}

	sum := Summary{
		Dialect:              DialectSQLite,
		PrimaryInstallations: []string{},
		WrapperIDs:           []string{},
		RowCounts:            counts,
		Blobs:                nil,
		RegistryFiles:        map[string][]RegistryFileEntry{},
		SchemaVersion:        BaselineSchemaVersion,
		Unverified:           nil,
	}

	return sum, files, nil
}

// stepKinds reads a production kind.
type stepKinds struct{}

func (stepKinds) ReadKind(context.Context) (installation.Kind, error) {
	return installation.KindProduction, nil
}

// stepService builds a service over target, reg and ops for the step-write
// tests. The row of stepID starts running with no attempt.
func stepService(target blob.Store, reg *stepRegistry, ops *stepOps) *Service {
	reg.mu.Lock()
	reg.rows[stepID] = Record{
		StartedAt: claimTestTime(), EndedAt: time.Time{}, ID: stepID, Target: "test", Error: "",
		State: StateRunning, Attempt: "",
	}
	reg.mu.Unlock()

	return &Service{
		exporter: stepExporter{}, kinds: stepKinds{}, registry: reg,
		source: newStepTarget(), opener: nil, target: target, targetName: "test",
		now: time.Now, version: "test", maxSQL: 0, maxUnverified: 0, runner: nil,
		operations: ops, lease: 0, logger: slog.New(slog.DiscardHandler),
	}
}

// stepOperation returns the operation that stepWrite reads: the input names
// stepID.
func stepOperation(opID durable.ID) durable.Operation {
	input := []byte(`{"id":"` + stepID.String() + `","startedAt":"2026-01-01T00:00:00Z"}`)

	return durable.Operation{
		ID: opID, Key: OperationKey(stepID), Kind: OperationKind, Version: OperationVersion,
		Input: input, State: durable.StateRunning, Steps: nil, Attempt: 1, Failures: 0, Expiries: 0,
		LastError: "",
		Claim: durable.Claim{
			Operation: opID, Owner: "test", Token: 0, ExpiresAt: time.Time{},
		},
		CancelRequested: false, NotBefore: time.Time{}, CreatedAt: claimTestTime(), UpdatedAt: claimTestTime(),
	}
}

// checkRowAttempt fails when the row of stepID does not record want.
func checkRowAttempt(t *testing.T, reg *stepRegistry, want string) {
	t.Helper()

	row, err := reg.GetBackup(t.Context(), stepID)
	if err != nil || row.Attempt != want {
		t.Fatalf("row after the write step = %+v, %v, want attempt %q", row, err, want)
	}
}

// checkManifestUnder fails when the target holds no manifest under attempt.
func checkManifestUnder(t *testing.T, target *stepTarget, attempt string) {
	t.Helper()

	prefix, err := newAttemptPrefix(stepID, attempt)
	if err != nil {
		t.Fatalf("attempt prefix of %s: %v", attempt, err)
	}

	manifestKey, err := prefix.manifestKey()
	if err != nil {
		t.Fatalf("manifest key: %v", err)
	}

	if !target.has(manifestKey.String()) {
		t.Fatalf("target holds no manifest under %q", prefix.dir())
	}
}

// TestStepWriteLinksTheAttemptToTheClaim proves the write-step link: the
// files land under backups/<id>/<T>/ where T is the claim token, and the row
// records T. A takeover writes a new directory.
func TestStepWriteLinksTheAttemptToTheClaim(t *testing.T) {
	t.Parallel()

	opID := durable.ID("op-step-write")
	target := newStepTarget()
	reg := newStepRegistry()
	ops := newStepOps(stepOperation(opID), 42)
	svc := stepService(target, reg, ops)

	fence := durable.StepFence{Operation: opID, Token: 42, Step: StepWrite}
	if err := svc.StepWriteForTest(t.Context(), stepOperation(opID), fence); err != nil {
		t.Fatalf("write step with token 42: %v", err)
	}

	checkRowAttempt(t, reg, "42")
	checkManifestUnder(t, target, "42")

	// A takeover runs the write step under a new token. The new run writes a
	// new directory; the row keeps the first attempt until the live holder
	// records again, and the second record is refused.
	ops.setLive(43)
	next := durable.StepFence{Operation: opID, Token: 43, Step: StepWrite}

	if err := svc.StepWriteForTest(t.Context(), stepOperation(opID), next); !errors.Is(
		err, durable.ErrClaimLost,
	) {
		t.Fatalf("write step after a takeover = %v, want ErrClaimLost", err)
	}

	checkManifestUnder(t, target, "43")
	checkRowAttempt(t, reg, "42")
}

// TestStepWriteClearNeedsTheClaim proves the fenced clear: a holder that
// lost the claim deletes nothing.
func TestStepWriteClearNeedsTheClaim(t *testing.T) {
	t.Parallel()

	opID := durable.ID("op-step-clear")
	target := newStepTarget()
	reg := newStepRegistry()
	ops := newStepOps(stepOperation(opID), 42)
	svc := stepService(target, reg, ops)

	// The stale prefix holds a file from an earlier run.
	stale, err := newAttemptPrefix(stepID, "41")
	if err != nil {
		t.Fatalf("attempt prefix of 41: %v", err)
	}

	staleKey, err := stale.key("tables/a.ndjson")
	if err != nil {
		t.Fatalf("stale key: %v", err)
	}

	if err := target.Put(t.Context(), staleKey, bytes.NewReader([]byte("stale"))); err != nil {
		t.Fatalf("seed the stale file: %v", err)
	}

	// The claim moved to 42, but the stale holder runs the write step with
	// token 41. The fenced clear must delete nothing.
	staleFence := durable.StepFence{Operation: opID, Token: 41, Step: StepWrite}

	if err := svc.StepWriteForTest(t.Context(), stepOperation(opID), staleFence); !errors.Is(
		err, durable.ErrClaimLost,
	) {
		t.Fatalf("write step with a stale claim = %v, want ErrClaimLost", err)
	}

	if !target.has(staleKey.String()) {
		t.Fatalf("the stale holder deleted %q without a claim", staleKey)
	}
}

// TestNewAttemptPrefixRefusesInvalidAttempts proves the boundary check of
// the finish step: the prefix builder refuses an empty attempt and attempts
// outside the allowlist with ErrInvalidAttempt, so the finish step never
// checks or commits an attempt that the row does not name. The failRow call
// of the finish step runs against the registry fake of the service tests,
// which marks the row failed.
func TestNewAttemptPrefixRefusesInvalidAttempts(t *testing.T) {
	t.Parallel()

	for _, attempt := range []string{"", "a/b", "../x", "A", "a\nb", strings.Repeat("a", 65)} {
		if _, err := newAttemptPrefix(stepID, attempt); !errors.Is(err, ErrInvalidAttempt) {
			t.Fatalf("attempt prefix of %q = %v, want ErrInvalidAttempt", attempt, err)
		}
	}
}

// TestStepFinishRefusesARowThatIsNotRunning proves the state check of the
// finish step: a row that already ended is refused with ErrNotRunning and
// keeps its state.
func TestStepFinishRefusesARowThatIsNotRunning(t *testing.T) {
	t.Parallel()

	opID := durable.ID("op-finish-done")
	target := newStepTarget()
	reg := newStepRegistry()
	ops := newStepOps(stepOperation(opID), 7)
	svc := stepService(target, reg, ops)

	reg.mu.Lock()
	reg.rows[stepID] = Record{
		StartedAt: claimTestTime(), EndedAt: claimTestTime(), ID: stepID, Target: "test", Error: "",
		State: StateSucceeded, Attempt: "5",
	}
	reg.mu.Unlock()

	fence := durable.StepFence{Operation: opID, Token: 7, Step: StepFinish}

	if err := svc.StepFinishForTest(t.Context(), stepOperation(opID), fence); !errors.Is(
		err, ErrNotRunning,
	) {
		t.Fatalf("finish step on a finished row = %v, want ErrNotRunning", err)
	}

	row, err := reg.GetBackup(t.Context(), stepID)
	if err != nil || row.State != StateSucceeded || row.Attempt != "5" {
		t.Fatalf("row after a refused finish = %+v, %v, want unchanged succeeded with attempt 5", row, err)
	}
}

// TestStepFinishRefusesARowWithoutAnAttempt proves the attempt check of the
// finish step: a running row that records no attempt fails with
// ErrInvalidAttempt in its error and ends as failed, so the finish step never
// checks or commits an attempt that the row does not name.
func TestStepFinishRefusesARowWithoutAnAttempt(t *testing.T) {
	t.Parallel()

	opID := durable.ID("op-finish-no-attempt")
	target := newStepTarget()
	reg := newStepRegistry()
	ops := newStepOps(stepOperation(opID), 7)
	svc := stepService(target, reg, ops)

	reg.mu.Lock()
	reg.rows[stepID] = Record{
		StartedAt: claimTestTime(), EndedAt: time.Time{}, ID: stepID, Target: "test", Error: "",
		State: StateRunning, Attempt: "",
	}
	reg.mu.Unlock()

	fence := durable.StepFence{Operation: opID, Token: 7, Step: StepFinish}

	if err := svc.StepFinishForTest(t.Context(), stepOperation(opID), fence); !errors.Is(
		err, ErrInvalidAttempt,
	) {
		t.Fatalf("finish step on a row without an attempt = %v, want ErrInvalidAttempt", err)
	}

	row, err := reg.GetBackup(t.Context(), stepID)
	if err != nil || row.State != StateFailed {
		t.Fatalf("row after a refused finish = %+v, %v, want a failed row", row, err)
	}
}
