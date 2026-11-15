package job_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

const (
	token   = int64(7)
	stepRun = "plan"
	logID   = "jlv-1"
)

// epoch is a fixed time for the tables.
func epoch() time.Time { return time.Date(2026, time.October, 21, 9, 0, 0, 0, time.UTC) }

// phaseOf returns a phase of kind k with no record.
func emptyPhase(k job.PhaseKind) job.Phase {
	return job.Phase{
		Kind: k, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil, Gates: nil,
		Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}
}

// recordJob returns a running job with a plan phase (step "plan"), an apply
// phase (step "apply") and a deliver phase, at the plan phase.
func recordJob() *job.Job {
	apply := planStep("apply")
	apply.Phase, apply.ChangesResources = job.PhaseApply, true

	return &job.Job{
		ID:             "job-1",
		NamespaceID:    "",
		State:          job.StateRunning,
		Current:        0,
		Wait:           job.WaitNone,
		Reason:         "",
		StartedBy:      "",
		Source:         "",
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		Trigger:        nil,
		IdempotencyKey: "",
		RequestHash:    "",
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		Spec: job.Spec{
			Unit:      identity.Object{},
			Operation: job.OperationApply,
			Purpose:   job.PurposeNone,
			Executor:  job.ExecutorRunner,
			Location: job.Location{
				Connection: "",
				Repository: "",
				Ref:        "",
				Directory:  "",
				Revision:   "",
			},
			LocationSource: "",
			Steps:          []job.Step{planStep(stepRun), apply},
			Pool:           "",
			Profile:        "",
			Edits:          nil,
			Delivery:       "",
			Defaulted:      nil,
		},
		Phases: []job.Phase{
			emptyPhase(job.PhasePlan),
			emptyPhase(job.PhaseApply),
			emptyPhase(job.PhaseDeliver),
		},
		StateChangedAt: time.Time{},
	}
}

// openRec returns the record of the step plan under the claim token, with no
// end.
func openRec(tok int64, log *job.LogRef) job.StepRecord {
	return job.StepRecord{Name: stepRun, Token: tok, ExitCode: 0, StartedAt: epoch(), FinishedAt: time.Time{}, Log: log}
}

// endedRec returns the record of the step plan under the claim token, ended at
// epoch.
func endedRec(tok int64) job.StepRecord {
	r := openRec(tok, nil)
	r.FinishedAt = epoch()

	return r
}

// openLog returns the open log version logID with the chunks.
func openLog(chunks int) *job.LogRef {
	return &job.LogRef{
		ID: logID, ItemKey: "", Version: 1, Chunks: chunks, SHA256: "", Size: int64(chunks), CreatedAt: time.Time{},
		ClosedAt: time.Time{}, ExpiresAt: time.Time{}, RedactedAt: time.Time{}, ReplacedBy: "", Matches: 0,
	}
}

// closedLog returns the closed log version logID with the chunks.
func closedLog(chunks int) *job.LogRef {
	l := openLog(chunks)
	l.ClosedAt = epoch()

	return l
}

// withSteps sets the step records of the plan phase.
func withSteps(recs ...job.StepRecord) func(j *job.Job) {
	return func(j *job.Job) {
		// Each case gets its own copy, with its own log: the rules change a record
		// in place, and the cases run in parallel.
		own := slices.Clone(recs)

		for i := range own {
			if own[i].Log != nil {
				l := *own[i].Log
				own[i].Log = &l
			}
		}

		j.Phases[0].Steps = own
	}
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))

	return hex.EncodeToString(h[:])
}

// expect reports whether err is the wanted outcome: nil, or an error that wraps
// want.
func expect(err, want error) bool {
	if want == nil {
		return err == nil
	}

	return errors.Is(err, want)
}

func TestBeginStepRecord(t *testing.T) {
	t.Parallel()

	noop := func(*job.Job) {}

	tests := []struct {
		name    string
		prepare func(j *job.Job)
		phase   int
		step    string
		want    error
		changed bool
	}{
		{"a step of the phase", noop, 0, "plan", nil, true},
		{"a step of another phase", noop, 0, "apply", job.ErrInvalid, false},
		{"a step that the spec has not", noop, 0, "nothing", job.ErrInvalid, false},
		{"a phase that does not exist", noop, 5, "plan", job.ErrInvalid, false},
		{"a negative phase", noop, -1, "plan", job.ErrInvalid, false},
		{"a write-back step in a runner phase", noop, 0, job.WriteBackStepCommit, job.ErrInvalid, false},
		{"a write-back step in a deliver phase", noop, 2, job.WriteBackStepCommit, nil, true},
		{"a runner step in a deliver phase", noop, 2, "plan", job.ErrInvalid, false},
		{"during a cancel", func(j *job.Job) { j.State = job.StateCanceling }, 0, "plan", job.ErrCanceled, false},
		{"an open step again", withSteps(openRec(token, nil)), 0, "plan", nil, false},
		{"an ended step", withSteps(endedRec(token)), 0, "plan", durable.ErrStepDone, false},
		{"a step of an earlier claim that ended", withSteps(endedRec(token - 1)), 0, "plan", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := recordJob()
			tt.prepare(j)

			changed, err := job.BeginStepRecord(j, job.StepKey{Phase: tt.phase, Token: token, Name: tt.step}, epoch())
			if !expect(err, tt.want) || changed != tt.changed {
				t.Fatalf("BeginStepRecord = %v, %v; want %v, %v", changed, err, tt.want, tt.changed)
			}
		})
	}
}

// logClose returns the close of the log version with the chunks.
func logClose(version string, chunks int, hash string) *job.LogClose {
	return &job.LogClose{Version: version, Chunks: chunks, SHA256: hash, Size: int64(chunks)}
}

func TestEndStepRecord(t *testing.T) {
	t.Parallel()

	const (
		maxExit    = 255
		beyondExit = 256
	)

	open := withSteps(openRec(token, nil))
	withLog := withSteps(openRec(token, openLog(2)))
	closing := logClose(logID, 2, sum("x"))

	tests := []struct {
		name    string
		prepare func(j *job.Job)
		exit    int
		closing *job.LogClose
		want    error
		changed bool
	}{
		{"an open step", open, 0, nil, nil, true},
		{"exit code -1", open, -1, nil, nil, true},
		{"the largest exit code", open, maxExit, nil, nil, true},
		{"exit code above the largest", open, beyondExit, nil, job.ErrInvalid, false},
		{"exit code -2", open, -2, nil, job.ErrInvalid, false},
		{"a step with no begin", func(*job.Job) {}, 0, nil, job.ErrInvalid, false},
		{"a begin of another claim", withSteps(openRec(token+1, nil)), 0, nil, job.ErrInvalid, false},
		{"the same end again", withSteps(endedRec(token)), 0, nil, nil, false},
		{"another end", withSteps(endedRec(token)), 1, nil, job.ErrConflict, false},
		{"a log that closes", withLog, 0, closing, nil, true},
		{"an open log and no close", withLog, 0, nil, job.ErrConflict, false},
		{"a close of another version", withLog, 0, logClose("jlv-2", 2, sum("x")), job.ErrConflict, false},
		{"a close with another chunk count", withLog, 0, logClose(logID, 3, sum("x")), job.ErrConflict, false},
		{"a close with no valid hash", withLog, 0, logClose(logID, 2, "xyz"), job.ErrInvalid, false},
		{
			"a close with an upper case hash",
			withLog,
			0,
			logClose(logID, 2, strings.ToUpper(sum("x"))),
			job.ErrInvalid,
			false,
		},
		{"a close of a step with no log", open, 0, closing, job.ErrInvalid, false},
		{
			"a close of a closed log", withSteps(openRec(token, closedLog(2))), 0, closing,
			job.ErrConflict, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := recordJob()
			tt.prepare(j)

			key := job.StepKey{Phase: 0, Token: token, Name: stepRun}

			changed, err := job.EndStepRecord(j, key, tt.exit, tt.closing, epoch())
			if !expect(err, tt.want) || changed != tt.changed {
				t.Fatalf("EndStepRecord = %v, %v; want %v, %v", changed, err, tt.want, tt.changed)
			}

			if changed && tt.closing != nil {
				if l := j.Phases[0].Steps[0].Log; l.ClosedAt.IsZero() || l.SHA256 != tt.closing.SHA256 {
					t.Fatalf("log after the close = %+v, want it closed with the hash", l)
				}
			}
		})
	}
}

// chunkOf returns the reserve of chunk seq with the content.
func chunkOf(seq int, content string) job.ChunkReserve {
	return job.ChunkReserve{Seq: seq, SHA256: sum(content), Size: int64(len(content))}
}

// newLogRef returns the log version that a chunk 0 creates.
func newLogRef() *job.LogRef {
	l := openLog(0)
	l.Size = 0

	return l
}

// chunkPlanOf returns the plan of a reserve of the log logID.
func chunkPlanOf(create, insert, dup bool) job.ChunkPlan {
	return job.ChunkPlan{Version: logID, CreateLog: create, Insert: insert, Duplicate: dup}
}

func TestPlanChunk(t *testing.T) {
	t.Parallel()

	begun := withSteps(openRec(token, nil))
	logged := func(chunks int) func(j *job.Job) {
		return withSteps(openRec(token, openLog(chunks)))
	}

	tooBig := job.ChunkReserve{Seq: 0, SHA256: sum("a"), Size: job.MaxLogChunk + 1}
	noHash := job.ChunkReserve{Seq: 0, SHA256: "", Size: 1}
	none := job.ChunkPlan{Version: "", CreateLog: false, Insert: false, Duplicate: false}

	tests := []struct {
		name    string
		prepare func(j *job.Job)
		c       job.ChunkReserve
		newLog  *job.LogRef
		want    error
		plan    job.ChunkPlan
	}{
		{"chunk 0 makes the log", begun, chunkOf(0, "a"), newLogRef(), nil, chunkPlanOf(true, true, false)},
		{"chunk 0 with no new log", begun, chunkOf(0, "a"), nil, job.ErrInvalid, none},
		{"chunk 0 with a log that has chunks", begun, chunkOf(0, "a"), openLog(1), job.ErrInvalid, none},
		{"chunk 0 with a closed new log", begun, chunkOf(0, "a"), closedLog(0), job.ErrInvalid, none},
		{"chunk 1 before chunk 0", begun, chunkOf(1, "a"), newLogRef(), job.ErrInvalid, none},
		{"a step that did not begin", func(*job.Job) {}, chunkOf(0, "a"), newLogRef(), job.ErrInvalid, none},
		{
			"a step that ended", withSteps(endedRec(token)), chunkOf(0, "a"), newLogRef(),
			job.ErrInvalid, none,
		},
		{
			"a closed log", withSteps(openRec(token, closedLog(2))), chunkOf(2, "c"), nil,
			job.ErrInvalid, none,
		},
		{"the same confirmed chunk", logged(2), chunkOf(0, "a"), nil, nil, chunkPlanOf(false, false, true)},
		{"a chunk with another hash", logged(2), chunkOf(0, "z"), nil, job.ErrConflict, none},
		{"the same reserved chunk", logged(1), chunkOf(1, "b"), nil, nil, chunkPlanOf(false, false, false)},
		{"the next chunk", logged(2), chunkOf(2, "c"), nil, nil, chunkPlanOf(false, true, false)},
		{"a gap", logged(2), chunkOf(3, "c"), nil, job.ErrInvalid, none},
		{"a negative number", begun, chunkOf(-1, "a"), newLogRef(), job.ErrInvalid, none},
		{"a number at the limit", begun, chunkOf(job.MaxLogChunks, "a"), newLogRef(), job.ErrInvalid, none},
		{"a chunk above MaxLogChunk", begun, tooBig, newLogRef(), job.ErrInvalid, none},
		{"a chunk with no hash", begun, noHash, newLogRef(), job.ErrInvalid, none},
	}

	rows := map[int]job.ChunkRow{
		0: {SHA256: sum("a"), Size: 1, Confirmed: true, BlobKey: ""},
		1: {SHA256: sum("b"), Size: 1, Confirmed: false, BlobKey: ""},
	}

	//nolint:unparam // The signature is the one that PlanChunk takes.
	lookup := func(_ string, seq int) (job.ChunkRow, bool, error) {
		r, ok := rows[seq]

		return r, ok, nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := recordJob()
			tt.prepare(j)

			key := job.StepKey{Phase: 0, Token: token, Name: stepRun}

			got, err := job.PlanChunk(j, key, tt.c, tt.newLog, lookup)
			if !expect(err, tt.want) || got != tt.plan {
				t.Fatalf("PlanChunk = %+v, %v; want %+v, %v", got, err, tt.plan, tt.want)
			}
		})
	}
}

func TestConfirmChunkRecord(t *testing.T) {
	t.Parallel()

	logged := func(log *job.LogRef) *job.Job {
		j := recordJob()
		withSteps(openRec(token, log))(j)

		return j
	}

	const (
		chunkBytes = 5
		wantChunks = 3
		wantBytes  = 7
	)

	tests := []struct {
		name    string
		j       *job.Job
		version string
		seq     int
		want    error
		changed bool
	}{
		{"the next chunk", logged(openLog(2)), logID, 2, nil, true},
		{"a chunk that counts already", logged(openLog(2)), logID, 1, nil, false},
		{"a gap", logged(openLog(2)), logID, 3, job.ErrInvalid, false},
		{"another version", logged(openLog(2)), "jlv-2", 2, job.ErrInvalid, false},
		{"a closed log", logged(closedLog(2)), logID, 2, job.ErrInvalid, false},
		{"a step with no log", recordJob(), logID, 0, job.ErrInvalid, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row := job.ChunkRow{SHA256: sum("x"), Size: chunkBytes, Confirmed: false, BlobKey: ""}
			key := job.StepKey{Phase: 0, Token: token, Name: stepRun}

			changed, err := job.ConfirmChunkRecord(tt.j, key, tt.version, tt.seq, row)
			if !expect(err, tt.want) || changed != tt.changed {
				t.Fatalf("ConfirmChunkRecord = %v, %v; want %v, %v", changed, err, tt.want, tt.changed)
			}

			if changed {
				if l := tt.j.Phases[0].Steps[0].Log; l.Chunks != wantChunks || l.Size != wantBytes {
					t.Fatalf("log after the confirm = %+v, want 3 chunks and 7 bytes", l)
				}
			}
		})
	}
}

func TestCheckPutPlan(t *testing.T) {
	t.Parallel()

	atVersion := func(j *job.Job) { j.Phases[0].StateVersion = "sv-1" }

	hasPlan := func(j *job.Job) {
		j.Phases[0].Plan = &job.PlanRef{
			ID: "pln-1", ItemKey: "", StateVersion: "", Parts: nil, CreatedAt: time.Time{}, ExpiresAt: time.Time{},
			RedactedAt: time.Time{},
		}
	}

	tests := []struct {
		name    string
		prepare func(j *job.Job)
		phase   int
		current string
		want    error
	}{
		{"a plan phase at the state version", atVersion, 0, "sv-1", nil},
		{"a state write after the claim", atVersion, 0, "sv-2", job.ErrPlanStale},
		{"a unit with no state version yet", func(*job.Job) {}, 0, "", nil},
		{"a phase that has a plan", hasPlan, 0, "", job.ErrConflict},
		{"an apply phase", func(*job.Job) {}, 1, "", job.ErrInvalid},
		{"a deliver phase", func(*job.Job) {}, 2, "", job.ErrInvalid},
		{"a phase that does not exist", func(*job.Job) {}, 3, "", job.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := recordJob()
			tt.prepare(j)

			err := job.CheckPutPlan(j, tt.phase, tt.current)
			if !expect(err, tt.want) {
				t.Fatalf("CheckPutPlan = %v, want %v", err, tt.want)
			}

			// The reason of a stale plan names both versions.
			if errors.Is(tt.want, job.ErrPlanStale) &&
				(!strings.Contains(err.Error(), "sv-1") || !strings.Contains(err.Error(), "sv-2")) {
				t.Fatalf("stale error %q does not name both state versions", err)
			}
		})
	}
}

// goodDelivery returns a valid result of a direct push.
func goodDelivery() job.DeliveryResult {
	return job.DeliveryResult{
		Delivery: job.DeliveryDirectPush, Commit: "abc123", Branch: "dustpan/job-1", ReviewRequest: "",
		MergedAt: time.Time{}, DeliveredAt: epoch(),
	}
}

func TestDeliveryResult(t *testing.T) {
	t.Parallel()

	const (
		longCommit = 129
		maxCommit  = 128
		maxBranch  = 255
		longURL    = 200
		override   = 0x202e
	)

	tests := []struct {
		name   string
		change func(d *job.DeliveryResult)
		ok     bool
	}{
		{"a direct push", func(*job.DeliveryResult) {}, true},
		{"a review request", func(d *job.DeliveryResult) {
			d.Delivery, d.ReviewRequest = job.DeliveryReviewRequest, "https://git.example.com/pull/1"
		}, true},
		{"a review request with no URL", func(d *job.DeliveryResult) { d.Delivery = job.DeliveryReviewRequest }, false},
		{"a direct push with a URL", func(d *job.DeliveryResult) { d.ReviewRequest = "https://x.example/1" }, false},
		{"an unknown delivery", func(d *job.DeliveryResult) { d.Delivery = "mail" }, false},
		{"no commit", func(d *job.DeliveryResult) { d.Commit = "" }, false},
		{
			"a commit that is too long",
			func(d *job.DeliveryResult) { d.Commit = strings.Repeat("a", longCommit) },
			false,
		},
		{"no branch", func(d *job.DeliveryResult) { d.Branch = "" }, false},
		{"a merge time", func(d *job.DeliveryResult) { d.MergedAt = epoch() }, false},
		{"no delivery time", func(d *job.DeliveryResult) { d.DeliveredAt = time.Time{} }, false},
		{"a line break in the commit", func(d *job.DeliveryResult) { d.Commit = "abc\n123" }, false},
		{"a bidirectional override in the branch", func(d *job.DeliveryResult) {
			d.Branch = "a" + string(rune(override)) + "b"
		}, false},
		{"a reason that is too long", func(d *job.DeliveryResult) {
			d.Delivery, d.ReviewRequest = job.DeliveryReviewRequest, "https://git.example.com/"+strings.Repeat(
				"p",
				longURL,
			)
			d.Branch, d.Commit = strings.Repeat("b", maxBranch), strings.Repeat("c", maxCommit)
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := goodDelivery()
			tt.change(&d)

			err := d.Validate()
			if (err == nil) != tt.ok || (err != nil && !errors.Is(err, job.ErrInvalid)) {
				t.Fatalf("Validate = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

func TestDeliveryResultSame(t *testing.T) {
	t.Parallel()

	const offset = 3600

	a, b := goodDelivery(), goodDelivery()
	if !a.Same(&b) {
		t.Fatal("two equal results are not the same")
	}

	b.DeliveredAt = a.DeliveredAt.In(time.FixedZone("x", offset))
	if !a.Same(&b) {
		t.Fatal("the same instant in another zone is not the same")
	}

	b.Commit = "other"
	if a.Same(&b) {
		t.Fatal("results with another commit are the same")
	}
}

func TestVerificationValid(t *testing.T) {
	t.Parallel()

	for result, want := range map[job.Verification]bool{
		job.VerificationNone: true, job.VerificationPending: true, job.VerificationVerified: true,
		job.VerificationUnverified: true, "done": false, "VERIFIED": false,
	} {
		if got := result.Valid(); got != want {
			t.Errorf("Verification(%q).Valid() = %t, want %t", result, got, want)
		}
	}
}

func TestCheckDelivered(t *testing.T) {
	t.Parallel()

	d := goodDelivery()
	other := goodDelivery()
	other.Commit = "def"

	deliver := func(with *job.DeliveryResult) *job.Job {
		j := recordJob()
		j.Phases[0].Kind, j.Delivery = job.PhaseDeliver, with

		return j
	}

	tests := []struct {
		name    string
		j       *job.Job
		want    error
		changed bool
	}{
		{"a deliver phase", deliver(nil), nil, true},
		{"the same result again", deliver(&d), nil, false},
		{"another result", deliver(&other), job.ErrConflict, false},
		{"a plan phase", recordJob(), job.ErrInvalid, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			changed, err := job.CheckDelivered(tt.j, 0, &d)
			if !expect(err, tt.want) || changed != tt.changed {
				t.Fatalf("CheckDelivered = %v, %v; want %v, %v", changed, err, tt.want, tt.changed)
			}
		})
	}
}

func TestValidHash(t *testing.T) {
	t.Parallel()

	const (
		short = 63
		long  = 65
		half  = 32
	)

	for h, want := range map[string]bool{
		sum("a"):                            true,
		"":                                  false,
		strings.Repeat("0", short):          false,
		strings.Repeat("0", long):           false,
		strings.Repeat("g", short+1):        false,
		strings.ToUpper(sum("a")):           false,
		strings.Repeat("a", half) + "\x00z": false,
	} {
		if got := job.ValidHash(h); got != want {
			t.Errorf("ValidHash(%q) = %v, want %v", h, got, want)
		}
	}
}
