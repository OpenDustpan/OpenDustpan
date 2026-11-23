package job

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"opendustpan.org/opendustpan/internal/blob/blobfault"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

var errInjectedPartDelete = errors.New("injected plan part delete failure")

// noRefusalsInternal drops every refusal of the artifact blobs.
type noRefusalsInternal struct{}

func (noRefusalsInternal) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

// TestAppendLogCappedChecksTheKey proves the name and number check of the
// recorder alone: the recorder has no store, so a chunk that passed the check
// would panic. A step name or a chunk number that is not valid is a ChunkError
// of the rule ChunkNameOrNumberInvalid, and no store call happens.
func TestAppendLogCappedChecksTheKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step string
		seq  int
	}{
		{"a step name that is not valid", "Init", 0},
		{"a negative chunk number", "init", -1},
		{"a chunk number at the cap", "init", MaxLogChunks},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				r DurableRecorder
				c PhaseClaim
			)

			err := r.AppendLogCapped(t.Context(), c, CappedChunk{
				Step: tt.step, Seq: tt.seq, Body: strings.NewReader("x"),
				Caps: LogCaps{ChunksPerStep: 1, BytesPerStep: 1, ChunksPerPhase: 1, BytesPerPhase: 1},
			})

			chunkErr, ok := errors.AsType[*ChunkError](err)
			if !ok || chunkErr.Rule != ChunkNameOrNumberInvalid || !errors.Is(err, ErrInvalid) {
				t.Fatalf("AppendLogCapped(%q, %d) = %v, want a ChunkError of the rule %s", tt.step, tt.seq, err,
					ChunkNameOrNumberInvalid)
			}
		})
	}
}

// chunkJobs is a job store that holds one job, or returns err. It counts the
// reads.
type chunkJobs struct {
	Store

	job   Job
	err   error
	reads int
}

func (s *chunkJobs) Get(context.Context, ID) (Job, error) {
	s.reads++

	if s.err != nil {
		return Job{}, s.err
	}

	return s.job, nil
}

// chunkReserves is a record store that counts the reserves of a chunk and
// refuses each one.
type chunkReserves struct {
	RecordStore

	reserves int
}

func (s *chunkReserves) ReserveLogChunk(context.Context, PhaseClaim, string, ChunkReserve, *NewLog) (ChunkPlan, error) {
	s.reserves++

	return ChunkPlan{}, errInjectedReserve
}

var (
	errInjectedRead    = errors.New("injected chunk read failure")
	errInjectedReserve = errors.New("injected chunk reserve")
)

// TestStoreLogChunkStopsAtEachError proves each error return of
// storeLogChunk before the reserve: a chunk that the recorder cannot read, a
// job that the store does not find, and a log version that the recorder
// cannot make. Each case returns its own error and does not reach the next
// step. With an error return removed, the next step runs and the case fails.
func TestStoreLogChunkStopsAtEachError(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	// The unit has no unit ID, so the item key of a new log version cannot be
	// made.
	var noUnitID Job

	noUnitID.ID = "job-1"
	noUnitID.Spec.Unit = unit
	noUnitID.Phases = make([]Phase, 1)
	noUnitID.Phases[0].Steps = []StepRecord{{
		Name: "init", Token: 0, ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, Log: nil,
	}}

	tests := []struct {
		name      string
		chunk     io.Reader
		jobErr    error
		want      error
		wantReads int
	}{
		{"a chunk that the recorder cannot read", iotest.ErrReader(errInjectedRead), nil, errInjectedRead, 0},
		{"a job that the store does not find", strings.NewReader("x"), ErrNotFound, ErrNotFound, 1},
		{"a log version that the recorder cannot make", strings.NewReader("x"), nil, secret.ErrInvalidScope, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			jobs := &chunkJobs{Store: nil, job: noUnitID, err: tt.jobErr, reads: 0}
			store := &chunkReserves{RecordStore: nil, reserves: 0}
			r := &DurableRecorder{
				store: store, jobs: jobs, blobs: nil, sealer: nil, namespaces: nil,
				retention: history.RetentionSettings{Plan: 0, JobLog: 0},
				now:       time.Now, log: slog.New(slog.DiscardHandler),
			}

			var c PhaseClaim

			c.Job, c.Fence.Namespace = "job-1", "acme"

			err := r.AppendLog(t.Context(), c, "init", 0, tt.chunk)
			if !errors.Is(err, tt.want) {
				t.Fatalf("AppendLog = %v, want %v", err, tt.want)
			}

			if jobs.reads != tt.wantReads || store.reserves != 0 {
				t.Fatalf("job reads = %d, reserves = %d; want %d and 0", jobs.reads, store.reserves, tt.wantReads)
			}
		})
	}
}

// TestStoreLogChunkStopsAtTheReserveError proves the error return of
// storeLogChunk after the reserve: a reserve that fails returns its error, and
// the recorder writes no chunk. Chunk 1 needs no new log version, so the
// reserve is the first store call and the clock is not read before it. A
// chunk write reads the clock. With the error return removed, writeChunk runs
// with the empty plan of the failed reserve and returns another error.
func TestStoreLogChunkStopsAtTheReserveError(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	var j Job

	j.ID = "job-1"
	j.Spec.Unit = unit
	j.Phases = make([]Phase, 1)
	j.Phases[0].Steps = []StepRecord{{
		Name: "init", Token: 0, ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, Log: nil,
	}}

	jobs := &chunkJobs{Store: nil, job: j, err: nil, reads: 0}
	store := &chunkReserves{RecordStore: nil, reserves: 0}
	clockReads := 0
	r := &DurableRecorder{
		store: store, jobs: jobs, blobs: nil, sealer: nil, namespaces: nil,
		retention: history.RetentionSettings{Plan: 0, JobLog: 0},
		now: func() time.Time {
			clockReads++

			return time.Now()
		},
		log: slog.New(slog.DiscardHandler),
	}

	var c PhaseClaim

	c.Job, c.Fence.Namespace = "job-1", "acme"

	err = r.AppendLog(t.Context(), c, "init", 1, strings.NewReader("x"))
	if !errors.Is(err, errInjectedReserve) {
		t.Fatalf("AppendLog = %v, want %v", err, errInjectedReserve)
	}

	if store.reserves != 1 || clockReads != 0 {
		t.Fatalf("reserves = %d, clock reads = %d; want 1 and 0 (no chunk write)", store.reserves, clockReads)
	}
}

// TestDeletePartsLogsAFailedDelete proves that the recorder logs a warning for
// each plan part that it did not store and could not delete, and logs nothing
// for a delete that succeeds. The logger is injected through the recorder.
func TestDeletePartsLogsAFailedDelete(t *testing.T) {
	t.Parallel()

	failing := []blobfault.Fault{{
		Method: "Delete", Call: 0, Kind: blobfault.KindFail, Err: errInjectedPartDelete, Delay: 0,
	}}

	tests := []struct {
		name     string
		faults   []blobfault.Fault
		wantLogs bool
	}{
		{name: "a failed delete logs a warning", faults: failing, wantLogs: true},
		{name: "a successful delete logs nothing", faults: nil, wantLogs: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := runDeletePartsWithLog(t, tc.faults)

			if got := strings.Contains(out, "level=WARN"); got != tc.wantLogs {
				t.Fatalf("warning logged = %t, want %t: %q", got, tc.wantLogs, out)
			}

			if tc.wantLogs && !strings.Contains(out, "kind=plan-apply") {
				t.Errorf("warning does not name the part: %q", out)
			}
		})
	}
}

// runDeletePartsWithLog deletes one plan part that the recorder did not store,
// through a blob store that applies faults. It returns the text that the
// injected logger wrote.
func runDeletePartsWithLog(t *testing.T, faults []blobfault.Fault) string {
	t.Helper()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	store, err := blobfault.New(inner, faults...)
	if err != nil {
		t.Fatalf("blobfault: %v", err)
	}

	artifacts, err := NewArtifactBlobs(store, noRefusalsInternal{})
	if err != nil {
		t.Fatalf("NewArtifactBlobs: %v", err)
	}

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelWarn, AddSource: false, ReplaceAttr: nil,
	}))

	r := &DurableRecorder{
		store: nil, jobs: nil, blobs: artifacts, sealer: nil, namespaces: nil,
		retention: history.RetentionSettings{Plan: 0, JobLog: 0},
		now:       nil, log: logger,
	}

	ns := namespace.NewID()
	at := &planAt{
		ns:   ns,
		job:  "job-1",
		plan: "pln-1",
		item: secret.ItemRef{Scope: secret.Scope{Namespace: "", ID: ""}, Kind: "", Item: ""},
	}
	part := PlanPart{
		Kind: OutputPlanApply, Size: 0, SHA256: "", Secret: false, StoredSize: 0, StoredMD5: "",
	}

	r.deleteParts(t.Context(), at, []PlanPart{part})

	return buf.String()
}
