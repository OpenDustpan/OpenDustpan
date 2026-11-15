package jobtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// RecorderHarness is one fresh database, blob store and set of item keys for
// one case of RunRecorder. The recorder and the reader share them with the
// job store of the embedded harness, as the adapters of one installation do.
type RecorderHarness struct {
	Harness

	// Recorder is the job.Recorder under test.
	Recorder job.CheckedRecorder
	// Reader is the job.ArtifactReader under test.
	Reader job.ArtifactReader
	// Records is the job.RecordStore under the recorder. The cases call its
	// reserve methods with their own item key function.
	Records job.RecordStore
	// Blobs is the blob store that the recorder writes to. The cases read it
	// and damage it.
	Blobs blob.Store
	// ItemKeys is the index of the item keys that the recorder made. The cases
	// shred a key through it, as a redaction does.
	ItemKeys secret.ItemKeyIndex
	// Retention is the retention that the recorder applies to new plans and
	// log versions.
	Retention history.RetentionSettings
	// Sealer is the item sealer under the recorder. The cases of the chunk keys
	// use its item key function to reserve a log version.
	Sealer secret.ItemSealer
}

// RecorderParts are the stores of one database that NewRecorderHarness builds
// the recorder and the reader over.
type RecorderParts struct {
	// Harness holds the job store, the durable store, the claimer and the
	// domain of the database.
	Harness Harness
	// Records is the job.RecordStore of the database.
	Records job.RecordStore
	// ItemKeys is the index of the item keys of the database.
	ItemKeys secret.ItemKeyIndex
	// Blobs is an empty blob store.
	Blobs blob.Store
	// Wrapper is the key-encryption key of the item keys. A nil Wrapper makes
	// a random one. A backup suite sets it, because a restore proves the key
	// against the backup.
	Wrapper secret.Wrapper
}

// noRefusals is a scopeaudit.Recorder and a scopeaudit.IntegrityRecorder that
// drops every refusal: the cases read a refusal as the error of the call.
type noRefusals struct{}

func (noRefusals) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

func (noRefusals) RecordMismatch(context.Context, namespace.ID, string) error {
	return nil
}

// NewRecorderHarness builds the item sealer, the recorder and the reader over
// parts, with a random key-encryption key and the retention of the suite. A
// SQL adapter calls it with its own stores.
func NewRecorderHarness(t *testing.T, parts RecorderParts) RecorderHarness {
	t.Helper()

	return NewRecorderHarnessWithRetention(
		t,
		parts,
		history.RetentionSettings{Plan: planRetention, JobLog: logRetention},
	)
}

// NewRecorderHarnessWithRetention is NewRecorderHarness with the retention
// periods that the recorder applies to new plans and log versions.
func NewRecorderHarnessWithRetention(
	t *testing.T, parts RecorderParts, retention history.RetentionSettings,
) RecorderHarness {
	t.Helper()

	wrapper := parts.Wrapper
	if wrapper == nil {
		key := make([]byte, keyBytes)
		if _, err := rand.Read(key); err != nil {
			t.Fatalf("key-encryption key: %v", err)
		}

		var err error

		wrapper, err = secret.NewBuiltinWrapper(key)
		if err != nil {
			t.Fatalf("wrapper: %v", err)
		}
	}

	itemBlobs, err := secret.NewItemKeyBlobs(parts.Blobs, noRefusals{}, noRefusals{})
	if err != nil {
		t.Fatalf("item key blobs: %v", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: parts.ItemKeys, Blobs: itemBlobs, Wrapper: wrapper, Namespaces: parts.Harness.Namespaces,
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("item sealer: %v", err)
	}

	artifacts, err := job.NewArtifactBlobs(parts.Blobs, noRefusals{})
	if err != nil {
		t.Fatalf("artifact blobs: %v", err)
	}

	rec, err := job.NewRecorder(job.RecorderConfig{
		Store: parts.Records, Jobs: parts.Harness.Jobs, Blobs: artifacts, Sealer: sealer,
		Namespaces: parts.Harness.Namespaces, Retention: retention, Now: time.Now,
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}

	reader, err := job.NewArtifactReader(parts.Records, artifacts, sealer, parts.Harness.Namespaces)
	if err != nil {
		t.Fatalf("artifact reader: %v", err)
	}

	return RecorderHarness{
		Harness: parts.Harness, Recorder: rec, Reader: reader, Records: parts.Records, Blobs: parts.Blobs,
		ItemKeys:  parts.ItemKeys,
		Retention: retention, Sealer: sealer,
	}
}

// RecorderFactory returns a fresh harness for one case.
type RecorderFactory func(t *testing.T) RecorderHarness

// recorderCase is one named case of the recorder suite.
type recorderCase struct {
	name string
	run  func(t *testing.T, h RecorderHarness)
}

// RunRecorder executes the conformance suite of job.Recorder and
// job.ArtifactReader against harnesses from newHarness:
//
//	func TestRecorderConformance(t *testing.T) {
//		t.Parallel()
//		jobtest.RunRecorder(t, func(t *testing.T) jobtest.RecorderHarness { return newHarness(t) })
//	}
//
// The reference adapter in memory, in the tests of this package, runs it
// first. Each SQL adapter runs it after.
func RunRecorder(t *testing.T, newHarness RecorderFactory) {
	t.Helper()

	cases := slices.Concat(stepCases(), claimRuleCases(), logCases(), logReadCases(), planCases(), planPartCases(),
		planReadCases(), reserveBindingCases(), deliveryCases(), keyTxCases(), chunkKeyCases())

	runRecorderCases(t, newHarness, cases)

	// A state version comes from the store of a tool. A harness without one
	// runs no case that reads a version.
	if newHarness(t).WriteState == nil {
		return
	}

	runRecorderCases(t, newHarness, slices.Concat(statePlanCases(), statePlanPartCases(), statePlanReadCases()))
}

// runRecorderCases runs each case against a fresh harness.
func runRecorderCases(t *testing.T, newHarness RecorderFactory, cases []recorderCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// stepCases returns the cases of BeginStep and EndStep.
func stepCases() []recorderCase {
	return []recorderCase{
		{"steps: begin and end record the step with the token and the exit code", testStepBeginEnd},
		{"steps: a second begin of an open step changes nothing", testStepBeginTwice},
		{"steps: a step that the phase does not have is ErrInvalid", testStepUnknown},
		{"steps: a begin after the end under the same claim is ErrStepDone", testStepBeginAfterEnd},
		{"steps: an end with no begin, or with a bad exit code or token, is ErrInvalid", testStepEndInvalid},
		{"steps: a second end with the same exit code changes nothing, another is ErrConflict", testStepEndTwice},
		{"steps: during a cancel a begin is ErrCanceled and an end is recorded", testStepCancel},
		{"steps: the steps of an earlier claim stay after a takeover", testStepTakeover},
		{"steps: after a takeover a begin of a step that the replaced claim ended is new", testStepTakeoverEnded},
		{"steps: the steps of a deliver phase are commit and review-request", testStepDeliver},
		{"steps: a checked end with another log is ErrConflict and changes nothing", testStepEndChecked},
	}
}

// Values that the cases of the recorder share.
const (
	// stepPlan is the step of the plan phase of the default spec.
	stepPlan = "plan"
	// stepApply is the step of the refresh job that changes resources.
	stepApply = "apply"
	// stepPrepare is the step of the refresh job that changes nothing.
	stepPrepare = "prepare"
	// valueOther is a value that no case stored.
	valueOther = "other"
	// commitDef is the commit of the delivery of the cases.
	commitDef = "def456"
	// keyBytes is the size of a key-encryption key.
	keyBytes = 32
	// planRetention and logRetention are the retention periods of the suite.
	planRetention = 48 * time.Hour
	logRetention  = 72 * time.Hour
	// exitUsage and exitFailure are exit codes of a failed tool, and
	// beyondExit is the first one that a process cannot have.
	exitUsage   = 2
	exitFailure = 3
	beyondExit  = 256
	// planBlobs is the number of blobs of a plan with two parts, and
	// chunksPair is the number of chunks of a log that the cases make from two
	// writes.
	planBlobs  = 2
	chunksPair = 2
	// gapSeq is a chunk number above the next one after chunk 1, thirdChunk is
	// the number of the third chunk.
	gapSeq     = 2
	thirdChunk = 2
	// bigChunk, repeatSmall, repeatMid and repeatPlan are the sizes of the
	// contents of the cases, in bytes.
	bigChunk    = 100 << 10
	repeatSmall = 1000
	repeatMid   = 5000
	repeatPlan  = 100
	// longCommit is a commit text above the limit of 128 bytes.
	longCommit = 129
	// hiddenRune is a bidirectional override, which a text must not hold.
	hiddenRune = 0x202e
)

// claimOf is claim, with the assignment by pointer: an assignment is large.
func claimOf(t *testing.T, h Harness, owner string, lease time.Duration, k durable.Kind) *job.Assignment {
	t.Helper()

	a := claim(t, h, owner, lease, k)

	return &a
}

// claimAgainOf is claimAgain, with the assignment by pointer. It returns the
// error of the claim too, which a case that expects none reads.
func claimAgainOf(t *testing.T, h Harness, owner string, k durable.Kind) (*job.Assignment, error) {
	t.Helper()

	a, err := claimAgain(t, h, owner, k)
	if err != nil {
		return nil, err
	}

	return &a, nil
}

// openPlan stores a plan job on the unit of f and claims its plan phase.
func openPlan(t *testing.T, h RecorderHarness, f fixture, name string) *job.Assignment {
	t.Helper()

	j := dispatched(t, h.Harness, f, plannedJob{unit: name, op: job.OperationPlan, steps: nil})

	return claimOf(t, h.Harness, runnerOne, time.Minute, runnerKind(&j))
}

// openApply stores a refresh job and claims its apply phase.
func openApply(t *testing.T, h RecorderHarness, f fixture, name string) *job.Assignment {
	t.Helper()

	j := dispatched(t, h.Harness, f, plannedJob{unit: name, op: job.OperationRefresh, steps: changeSteps()})

	return claimOf(t, h.Harness, runnerOne, time.Minute, runnerKind(&j))
}

// stepsOf returns the step records of the phase of a.
func stepsOf(t *testing.T, h RecorderHarness, a *job.Assignment) []job.StepRecord {
	t.Helper()

	return jobOf(t, h.Harness, a.Claim.Job).Phases[a.Claim.Phase].Steps
}

// endRecord returns the record that EndStep takes for a step of a, with the
// exit code exit.
func endRecord(a *job.Assignment, name string, exit int) job.StepRecord {
	return job.StepRecord{
		Name: name, Token: a.Claim.Claim.Token, ExitCode: exit, StartedAt: time.Time{}, FinishedAt: time.Time{},
		Log: nil,
	}
}

// mustBegin begins the step and fails t on an error.
func mustBegin(t *testing.T, h RecorderHarness, a *job.Assignment, name string) {
	t.Helper()

	if err := h.Recorder.BeginStep(t.Context(), a.Claim, name); err != nil {
		t.Fatalf("BeginStep(%s) = %v", name, err)
	}
}

// mustEnd ends the step with the exit code and fails t on an error.
func mustEnd(t *testing.T, h RecorderHarness, a *job.Assignment, name string, exit int) {
	t.Helper()

	if err := h.Recorder.EndStep(t.Context(), a.Claim, endRecord(a, name, exit)); err != nil {
		t.Fatalf("EndStep(%s) = %v", name, err)
	}
}

// versionOf returns the version of the job of a.
func versionOf(t *testing.T, h RecorderHarness, a *job.Assignment) int64 {
	t.Helper()

	return jobOf(t, h.Harness, a.Claim.Job).Version
}

func testStepBeginEnd(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")
	before := time.Now().Add(-time.Minute)

	mustBegin(t, h, a, stepPrepare)

	got := stepsOf(t, h, a)
	if len(got) != 1 || !openRecordOf(&got[0], stepPrepare, a.Claim.Claim.Token) || got[0].StartedAt.Before(before) {
		t.Fatalf("steps after BeginStep = %+v, want one open record of prepare under the token", got)
	}

	mustEnd(t, h, a, stepPrepare, exitFailure)

	got = stepsOf(t, h, a)
	if len(got) != 1 || got[0].ExitCode != exitFailure || got[0].FinishedAt.IsZero() ||
		got[0].FinishedAt.Before(got[0].StartedAt) {
		t.Fatalf("steps after EndStep = %+v, want the record ended with exit code %d", got, exitFailure)
	}

	mustBegin(t, h, a, stepApply)
	mustEnd(t, h, a, stepApply, -1)

	got = stepsOf(t, h, a)
	if len(got) != chunksPair || got[1].Name != stepApply || got[1].ExitCode != -1 {
		t.Fatalf("steps = %+v, want prepare then apply with exit code -1", got)
	}
}

// testStepEndChecked proves that a checked end compares the log before it
// writes: a count or a hash that differs leaves the step and its log open, and
// the right count and hash end the step. A retry of the right end changes
// nothing, and a retry with another log is ErrConflict.
func testStepEndChecked(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")

	mustBegin(t, h, a, stepPrepare)
	mustAppend(t, h, a, 0, "line 0\n")
	mustAppend(t, h, a, 1, "line 1\n")

	sum := sha256.Sum256([]byte("line 0\nline 1\n"))
	right := job.LogExpectation{Chunks: chunksPair, SHA256: hex.EncodeToString(sum[:])}
	wrongHash := job.LogExpectation{Chunks: chunksPair, SHA256: strings.Repeat("0", len(right.SHA256))}

	refused := map[string]job.LogExpectation{
		"hash":  wrongHash,
		"count": {Chunks: 1, SHA256: right.SHA256},
		"none":  {Chunks: 0, SHA256: ""},
	}

	for name, want := range refused {
		err := h.Recorder.EndStepChecked(t.Context(), a.Claim, endRecord(a, stepPrepare, 0), want)
		if !errors.Is(err, job.ErrConflict) {
			t.Fatalf("EndStepChecked(%s differs) = %v, want ErrConflict", name, err)
		}

		if got := stepsOf(t, h, a); !stepAndLogOpen(got) {
			t.Fatalf("steps after the refused end (%s) = %+v, want the step and its log open", name, got)
		}
	}

	endCheckedAgain(t, h, a, right, wrongHash)
}

// stepAndLogOpen reports whether steps is one step with an open log.
func stepAndLogOpen(steps []job.StepRecord) bool {
	return len(steps) == 1 && steps[0].FinishedAt.IsZero() && steps[0].Log != nil && steps[0].Log.ClosedAt.IsZero()
}

// endCheckedAgain ends the step with the right log, retries the end, and
// retries it with the wrong log.
func endCheckedAgain(t *testing.T, h RecorderHarness, a *job.Assignment, right, wrong job.LogExpectation) {
	t.Helper()

	if err := h.Recorder.EndStepChecked(t.Context(), a.Claim, endRecord(a, stepPrepare, 0), right); err != nil {
		t.Fatalf("EndStepChecked(right log) = %v", err)
	}

	got := stepsOf(t, h, a)
	if len(got) != 1 || got[0].FinishedAt.IsZero() || got[0].Log == nil || got[0].Log.ClosedAt.IsZero() ||
		got[0].Log.Chunks != chunksPair || got[0].Log.SHA256 != right.SHA256 {
		t.Fatalf("steps after the end = %+v, want the step ended with the log closed", got)
	}

	if err := h.Recorder.EndStepChecked(t.Context(), a.Claim, endRecord(a, stepPrepare, 0), right); err != nil {
		t.Fatalf("EndStepChecked(retry) = %v, want nil", err)
	}

	err := h.Recorder.EndStepChecked(t.Context(), a.Claim, endRecord(a, stepPrepare, 0), wrong)
	if !errors.Is(err, job.ErrConflict) {
		t.Fatalf("EndStepChecked(retry with another log) = %v, want ErrConflict", err)
	}
}

// openRecordOf reports whether r is the record of the step name under the
// token, with no end and no log.
func openRecordOf(r *job.StepRecord, name string, token int64) bool {
	return r.Name == name && r.Token == token && r.FinishedAt.IsZero() && r.Log == nil
}

func testStepBeginTwice(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")
	mustBegin(t, h, a, stepApply)

	cur := jobOf(t, h.Harness, a.Claim.Job)

	// A retry after a lost answer is safe.
	mustBegin(t, h, a, stepApply)

	got := jobOf(t, h.Harness, a.Claim.Job)
	if len(got.Phases[0].Steps) != 1 || got.Version != cur.Version {
		t.Fatalf("after a second begin: %d records at version %d; want 1 at version %d",
			len(got.Phases[0].Steps), got.Version, cur.Version)
	}
}

func testStepUnknown(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openPlan(t, h, setUp(t, h.Harness), "unit-0")
	cur := versionOf(t, h, a)

	// The apply step is a step of the spec, but of another phase.
	names := []string{"nothing", stepApply, "", "Plan", "plan/../x", "a b", "commit", "review-request"}

	for _, name := range names {
		if err := h.Recorder.BeginStep(t.Context(), a.Claim, name); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("BeginStep(%q) = %v, want ErrInvalid", name, err)
		}
	}

	if got := versionOf(t, h, a); got != cur {
		t.Fatalf("job version after refused begins = %d, want %d", got, cur)
	}
}

func testStepBeginAfterEnd(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")
	mustBegin(t, h, a, stepPrepare)
	mustEnd(t, h, a, stepPrepare, 0)

	if err := h.Recorder.BeginStep(t.Context(), a.Claim, stepPrepare); !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("BeginStep after EndStep = %v, want durable.ErrStepDone", err)
	}

	if got := stepsOf(t, h, a); len(got) != 1 {
		t.Fatalf("records after a refused begin = %d, want 1", len(got))
	}
}

func testStepEndInvalid(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")

	// No begin record.
	if err := h.Recorder.EndStep(t.Context(), a.Claim, endRecord(a, stepApply, 0)); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("EndStep with no begin = %v, want ErrInvalid", err)
	}

	mustBegin(t, h, a, stepApply)

	other := endRecord(a, stepApply, 0)
	other.Token++

	cases := map[string]job.StepRecord{
		"another token":             other,
		"an exit code below -1":     endRecord(a, stepApply, -2),
		"an exit code above 255":    endRecord(a, stepApply, beyondExit),
		"a name that is not a step": endRecord(a, "Not A Step", 0),
	}

	for what, rec := range cases {
		if err := h.Recorder.EndStep(t.Context(), a.Claim, rec); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("EndStep with %s = %v, want ErrInvalid", what, err)
		}
	}

	if got := stepsOf(t, h, a); len(got) != 1 || !got[0].FinishedAt.IsZero() {
		t.Fatalf("records after refused ends = %+v, want one open record", got)
	}
}

func testStepEndTwice(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")
	mustBegin(t, h, a, stepApply)
	mustEnd(t, h, a, stepApply, exitUsage)

	cur := versionOf(t, h, a)

	mustEnd(t, h, a, stepApply, exitUsage)

	if got := versionOf(t, h, a); got != cur {
		t.Fatalf("job version after a repeated end = %d, want %d", got, cur)
	}

	err := h.Recorder.EndStep(t.Context(), a.Claim, endRecord(a, stepApply, 0))
	if !errors.Is(err, job.ErrConflict) {
		t.Fatalf("EndStep with another exit code = %v, want ErrConflict", err)
	}

	if got := stepsOf(t, h, a); got[0].ExitCode != exitUsage {
		t.Fatalf("exit code after a refused end = %d, want 2", got[0].ExitCode)
	}
}

func testStepCancel(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openApply(t, h, f, "unit-0")
	mustBegin(t, h, a, stepPrepare)
	cancelJob(t, h.Harness, f, a.Claim.Job)

	if err := h.Recorder.BeginStep(t.Context(), a.Claim, stepApply); !errors.Is(err, job.ErrCanceled) {
		t.Fatalf("BeginStep during a cancel = %v, want ErrCanceled", err)
	}

	// The executor still records how the phase stopped.
	mustEnd(t, h, a, stepPrepare, -1)

	got := stepsOf(t, h, a)
	if len(got) != 1 || got[0].FinishedAt.IsZero() || got[0].ExitCode != -1 {
		t.Fatalf("records after the cancel = %+v, want prepare ended with -1 and no apply record", got)
	}
}

func testStepTakeover(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	j := dispatched(t, h.Harness, f, plannedJob{unit: "unit-0", op: job.OperationPlan, steps: nil})
	first := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))

	mustBegin(t, h, first, stepPlan)
	lapse(t, h.Harness, first)

	second, err := claimAgainOf(t, h.Harness, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	// The claim that a takeover replaced writes nothing.
	err = h.Recorder.EndStep(t.Context(), first.Claim, endRecord(first, stepPlan, 0))
	if !errors.Is(
		err,
		job.ErrClaimLost,
	) {
		t.Fatalf("EndStep by the replaced claim = %v, want ErrClaimLost", err)
	}

	// The new claim runs the phase again: its begin is a new record.
	mustBegin(t, h, second, stepPlan)

	got := stepsOf(t, h, second)
	if len(got) != 2 || got[0].Token != first.Claim.Claim.Token || got[1].Token != second.Claim.Claim.Token {
		t.Fatalf("records after a takeover = %+v, want one under each token", got)
	}
}

// testStepTakeoverEnded proves that the record of a step is per claim. The
// new claim runs the phase again from its first step, so its begin of a step
// that the replaced claim ended is a new record, not durable.ErrStepDone.
func testStepTakeoverEnded(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	j := dispatched(t, h.Harness, f, plannedJob{unit: "unit-ended", op: job.OperationPlan, steps: nil})
	first := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))

	mustBegin(t, h, first, stepPlan)
	mustEnd(t, h, first, stepPlan, 0)
	lapse(t, h.Harness, first)

	second, err := claimAgainOf(t, h.Harness, runnerThree, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	if err := h.Recorder.BeginStep(t.Context(), second.Claim, stepPlan); err != nil {
		t.Fatalf("BeginStep by the new claim of a step that the replaced claim ended = %v, want nil", err)
	}

	mustEnd(t, h, second, stepPlan, 0)

	got := stepsOf(t, h, second)
	if len(got) != 2 || got[0].Token != first.Claim.Claim.Token || got[1].Token != second.Claim.Claim.Token {
		t.Fatalf("records after a takeover = %+v, want one under each token", got)
	}

	if got[0].FinishedAt.IsZero() || got[1].FinishedAt.IsZero() {
		t.Fatalf("records after a takeover = %+v, want both ended", got)
	}
}

func testStepDeliver(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	deliverJob(t, h.Harness, f, "unit-0")
	a := claimOf(t, h.Harness, "instance-a", time.Minute, job.KindWriteBack)

	for _, name := range []string{job.WriteBackStepCommit, job.WriteBackStepReview} {
		mustBegin(t, h, a, name)
		mustEnd(t, h, a, name, 0)
	}

	if err := h.Recorder.BeginStep(t.Context(), a.Claim, "plan"); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("BeginStep of a runner step in a deliver phase = %v, want ErrInvalid", err)
	}

	if got := stepsOf(t, h, a); len(got) != chunksPair {
		t.Fatalf("records = %d, want commit and review-request", len(got))
	}
}

// claimRuleCases returns the cases of the checks that every write of the
// recorder makes on its claim.
func claimRuleCases() []recorderCase {
	return []recorderCase{
		{"claim: every write by a replaced claim is ErrClaimLost and changes nothing", testWritesLost},
		{"claim: every write of a phase that ended is ErrClaimLost", testWritesAfterEnd},
		{"claim: every write with a stale fence is ErrFenced and changes nothing", testWritesFenced},
		{"claim: a write to a job of another namespace is ErrNotFound", testWritesOtherNamespace},
	}
}

// write is one write of the recorder, for the cases that try each of them.
type write struct {
	name string
	do   func(t *testing.T, h RecorderHarness, a *job.Assignment) error
}

// writes returns one write for each method of the recorder, valid for the
// claim of a plan phase whose step plan began. Delivered is not valid in that
// phase, so it fails the same way for a claim that is not live.
func writes() []write {
	return []write{
		{"BeginStep", func(t *testing.T, h RecorderHarness, a *job.Assignment) error {
			t.Helper()

			return h.Recorder.BeginStep(t.Context(), a.Claim, stepPlan)
		}},
		{"EndStep", func(t *testing.T, h RecorderHarness, a *job.Assignment) error {
			t.Helper()

			return h.Recorder.EndStep(t.Context(), a.Claim, endRecord(a, stepPlan, 0))
		}},
		{"AppendLog", func(t *testing.T, h RecorderHarness, a *job.Assignment) error {
			t.Helper()

			return h.Recorder.AppendLog(t.Context(), a.Claim, stepPlan, 0, bytes.NewReader([]byte("line\n")))
		}},
		{"PutPlan", func(t *testing.T, h RecorderHarness, a *job.Assignment) error {
			t.Helper()

			_, err := h.Recorder.PutPlan(t.Context(), a.Claim, planParts("display", "apply"))

			return err
		}},
		{"Delivered", func(t *testing.T, h RecorderHarness, a *job.Assignment) error {
			t.Helper()

			return h.Recorder.Delivered(t.Context(), a.Claim, delivered("abc123", job.DeliveryDirectPush))
		}},
	}
}

// planParts returns the uploads of a plan with the contents display and apply.
func planParts(display, apply string) []job.PlanUpload {
	return []job.PlanUpload{
		{Kind: job.OutputPlanDisplay, Body: bytes.NewReader([]byte(display))},
		{Kind: job.OutputPlanApply, Body: bytes.NewReader([]byte(apply))},
	}
}

// delivered returns a valid delivery result.
func delivered(commit string, kind job.Delivery) job.DeliveryResult {
	review := ""
	if kind == job.DeliveryReviewRequest {
		review = "https://git.example.com/acme/infra/pull/7"
	}

	return job.DeliveryResult{
		Delivery: kind, Commit: commit, Branch: "dustpan/job-1", ReviewRequest: review, MergedAt: time.Time{},
		DeliveredAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func testWritesLost(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)

	for i, w := range writes() {
		j := dispatched(t, h.Harness, f, plannedJob{unit: unitName(i), op: job.OperationPlan, steps: nil})
		first := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))

		mustBegin(t, h, first, stepPlan)
		lapse(t, h.Harness, first)

		if _, err := claimAgainOf(t, h.Harness, runnerTwo, runnerKind(&j)); err != nil {
			t.Fatalf("%s: takeover = %v", w.name, err)
		}

		cur := jobOf(t, h.Harness, j.ID)

		if err := w.do(t, h, first); !errors.Is(err, job.ErrClaimLost) {
			t.Errorf("%s by a replaced claim = %v, want ErrClaimLost", w.name, err)
		}

		if got := jobOf(t, h.Harness, j.ID); got.Version != cur.Version || got.Delivery != nil ||
			got.Phases[0].Plan != nil || len(got.Phases[0].Steps) != len(cur.Phases[0].Steps) {
			t.Errorf("%s by a replaced claim changed the job: version %d, want %d", w.name, got.Version, cur.Version)
		}
	}
}

func testWritesAfterEnd(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)

	for i, w := range writes() {
		a := openPlan(t, h, f, unitName(i))
		mustBegin(t, h, a, stepPlan)

		err := h.Claimer.Finish(t.Context(), a.Claim, job.Outcome{Succeeded: false, Message: "stopped"})
		if err != nil {
			t.Fatalf("%s: Finish = %v", w.name, err)
		}

		if err := w.do(t, h, a); !errors.Is(err, job.ErrClaimLost) {
			t.Errorf("%s after the end of the phase = %v, want ErrClaimLost", w.name, err)
		}
	}
}

func testWritesFenced(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)

	for i, w := range writes() {
		a := openPlan(t, h, f, unitName(i))
		mustBegin(t, h, a, stepPlan)

		cur := jobOf(t, h.Harness, a.Claim.Job)

		bumped, err := h.Domain.BumpFence(t.Context(), a.Claim.Fence)
		if err != nil {
			t.Fatalf("%s: BumpFence = %v", w.name, err)
		}

		if err = w.do(t, h, a); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("%s with a stale fence = %v, want ErrFenced", w.name, err)
		}

		// The fence of the namespace moved for every later case too.
		f.fence = bumped

		if got := jobOf(t, h.Harness, cur.ID); got.Version != cur.Version {
			t.Errorf("%s with a stale fence changed the job: version %d, want %d", w.name, got.Version, cur.Version)
		}

		if entries, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace); err != nil {
			t.Fatalf("AuditEntries = %v", err)
		} else if hasAction(entries, "unit:write-back") {
			t.Errorf("%s with a stale fence wrote an audit entry of a delivery", w.name)
		}
	}
}

func testWritesOtherNamespace(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "unit-0")

	otherFence, err := h.Domain.CreateNamespace(t.Context(), "other")
	if err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	// The claim names a job of acme and the fence of another namespace.
	forged := a
	forged.Claim.Fence = otherFence

	for _, w := range writes() {
		if err := w.do(t, h, forged); !errors.Is(err, job.ErrNotFound) {
			t.Errorf("%s with the fence of another namespace = %v, want ErrNotFound", w.name, err)
		}
	}
}

// hasAction reports whether entries holds an entry with the action.
func hasAction(entries []audit.Entry, action string) bool {
	for i := range entries {
		if entries[i].Action == action {
			return true
		}
	}

	return false
}

// readAll reads rc to the end, closes it, and returns the content.
func readAll(rc io.ReadCloser) ([]byte, error) {
	data, err := io.ReadAll(rc)
	closeErr := rc.Close()

	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close: %w", closeErr)
	}

	return data, nil
}

// itemKeyOf returns the reference of the item key of the plan or the log
// version with the ID item, of the kind kind, of the unit of j.
func itemKeyOf(t *testing.T, j *job.Job, kind secret.ItemKind, item string) secret.ItemRef {
	t.Helper()

	unit := history.Unit{Namespace: j.Spec.Unit.Namespace(), Tool: j.Spec.Unit.Tool(), ID: j.Spec.Unit.UnitID()}

	ref, err := history.ItemRef{Unit: unit, Kind: kind, Item: item}.KeyRef()
	if err != nil {
		t.Fatalf("item key reference: %v", err)
	}

	return ref
}

// blobsUnder lists the plan and log blobs of the namespace ns.
func blobsUnder(t *testing.T, h RecorderHarness, ns namespace.Name) []blob.Object {
	t.Helper()

	n, err := h.Namespaces.GetNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("GetNamespace(%s) = %v", ns, err)
	}

	objects, err := listBlobs(t.Context(), h.Blobs, "namespaces/"+n.ID.String()+"/job/")
	if err != nil {
		t.Fatalf("list the plan and log blobs: %v", err)
	}

	return objects
}

// listBlobs walks every page under prefix and returns the objects.
func listBlobs(ctx context.Context, store blob.Store, prefix string) ([]blob.Object, error) {
	var out []blob.Object

	err := blob.Walk(ctx, store, prefix, func(o blob.Object) error {
		out = append(out, o)

		return nil
	})

	return out, err
}
