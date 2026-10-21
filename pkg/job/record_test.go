package job_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
)

// recordJob returns a running job with a plan phase (step "plan") and an apply
// phase (step "apply"), at the plan phase.
func recordJob() *job.Job {
	return &job.Job{
		ID: "job-1", State: job.StateRunning, Current: 0,
		Spec: job.Spec{Steps: []job.Step{planStep("plan"), {
			Name: "apply", Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", "apply"},
			ChangesResources: true,
		}}},
		Phases: []job.Phase{{Kind: job.PhasePlan}, {Kind: job.PhaseApply}, {Kind: job.PhaseDeliver}},
	}
}

const token = int64(7)

var epoch = time.Date(2026, time.October, 21, 9, 0, 0, 0, time.UTC)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))

	return hex.EncodeToString(h[:])
}

func TestBeginStepRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(j *job.Job)
		phase   int
		step    string
		want    error
		changed bool
	}{
		{"a step of the phase", func(*job.Job) {}, 0, "plan", nil, true},
		{"a step of another phase", func(*job.Job) {}, 0, "apply", job.ErrInvalid, false},
		{"a step that the spec has not", func(*job.Job) {}, 0, "other", job.ErrInvalid, false},
		{"a phase that does not exist", func(*job.Job) {}, 5, "plan", job.ErrInvalid, false},
		{"a negative phase", func(*job.Job) {}, -1, "plan", job.ErrInvalid, false},
		{"a write-back step in a runner phase", func(*job.Job) {}, 0, job.WriteBackStepCommit, job.ErrInvalid, false},
		{"a write-back step in a deliver phase", func(*job.Job) {}, 2, job.WriteBackStepCommit, nil, true},
		{"a runner step in a deliver phase", func(*job.Job) {}, 2, "plan", job.ErrInvalid, false},
		{"during a cancel", func(j *job.Job) { j.State = job.StateCanceling }, 0, "plan", job.ErrCanceled, false},
		{"an open step again", func(j *job.Job) {
			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch}}
		}, 0, "plan", nil, false},
		{"an ended step", func(j *job.Job) {
			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch, FinishedAt: epoch}}
		}, 0, "plan", durable.ErrStepDone, false},
		{"a step of an earlier claim that ended", func(j *job.Job) {
			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token - 1, StartedAt: epoch, FinishedAt: epoch}}
		}, 0, "plan", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := recordJob()
			tt.prepare(j)

			changed, err := job.BeginStepRecord(j, tt.phase, token, tt.step, epoch)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) || changed != tt.changed {
				t.Fatalf("BeginStepRecord = %v, %v; want %v, %v", changed, err, tt.want, tt.changed)
			}
		})
	}
}

func TestEndStepRecord(t *testing.T) {
	t.Parallel()

	open := func(j *job.Job) {
		j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch}}
	}

	withLog := func(j *job.Job) {
		open(j)
		j.Phases[0].Steps[0].Log = &job.LogRef{ID: "jlv-1", Chunks: 2, Size: 10}
	}

	closing := &job.LogClose{Version: "jlv-1", Chunks: 2, SHA256: sum("x"), Size: 10}

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
		{"exit code 255", open, 255, nil, nil, true},
		{"exit code 256", open, 256, nil, job.ErrInvalid, false},
		{"exit code -2", open, -2, nil, job.ErrInvalid, false},
		{"a step with no begin", func(*job.Job) {}, 0, nil, job.ErrInvalid, false},
		{"a begin of another claim", func(j *job.Job) {
			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token + 1, StartedAt: epoch}}
		}, 0, nil, job.ErrInvalid, false},
		{"the same end again", func(j *job.Job) {
			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch, FinishedAt: epoch}}
		}, 0, nil, nil, false},
		{"another end", func(j *job.Job) {
			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch, FinishedAt: epoch}}
		}, 1, nil, job.ErrConflict, false},
		{"a log that closes", withLog, 0, closing, nil, true},
		{"an open log and no close", withLog, 0, nil, job.ErrConflict, false},
		{"a close of another version", withLog, 0, &job.LogClose{Version: "jlv-2", Chunks: 2, SHA256: sum("x")},
			job.ErrConflict, false},
		{"a close with another chunk count", withLog, 0, &job.LogClose{Version: "jlv-1", Chunks: 3, SHA256: sum("x")},
			job.ErrConflict, false},
		{"a close with no valid hash", withLog, 0, &job.LogClose{Version: "jlv-1", Chunks: 2, SHA256: "xyz"},
			job.ErrInvalid, false},
		{"a close with an upper case hash", withLog, 0, &job.LogClose{
			Version: "jlv-1", Chunks: 2, SHA256: strings.ToUpper(sum("x")),
		}, job.ErrInvalid, false},
		{"a close of a step with no log", open, 0, closing, job.ErrInvalid, false},
		{"a close of a closed log", func(j *job.Job) {
			open(j)
			j.Phases[0].Steps[0].Log = &job.LogRef{ID: "jlv-1", Chunks: 2, ClosedAt: epoch}
		}, 0, closing, job.ErrConflict, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := recordJob()
			tt.prepare(j)

			changed, err := job.EndStepRecord(j, 0, token, "plan", tt.exit, tt.closing, epoch)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) || changed != tt.changed {
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

func TestPlanChunk(t *testing.T) {
	t.Parallel()

	newLog := func() *job.LogRef { return &job.LogRef{ID: "jlv-1", Version: 1} }
	chunk := func(seq int, content string) job.ChunkReserve {
		return job.ChunkReserve{Seq: seq, SHA256: sum(content), Size: int64(len(content))}
	}

	logged := func(chunks int, closed bool) func(j *job.Job) {
		return func(j *job.Job) {
			l := &job.LogRef{ID: "jlv-1", Version: 1, Chunks: chunks}
			if closed {
				l.ClosedAt = epoch
			}

			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch, Log: l}}
		}
	}

	begun := func(j *job.Job) {
		j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch}}
	}

	rows := map[int]job.ChunkRow{
		0: {SHA256: sum("a"), Size: 1, Confirmed: true},
		1: {SHA256: sum("b"), Size: 1, Confirmed: false},
	}

	tests := []struct {
		name    string
		prepare func(j *job.Job)
		c       job.ChunkReserve
		newLog  *job.LogRef
		want    error
		plan    job.ChunkPlan
	}{
		{"chunk 0 makes the log", begun, chunk(0, "a"), newLog(), nil,
			job.ChunkPlan{Version: "jlv-1", CreateLog: true, Insert: true}},
		{"chunk 0 with no new log", begun, chunk(0, "a"), nil, job.ErrInvalid, job.ChunkPlan{}},
		{"chunk 0 with a log that has chunks", begun, chunk(0, "a"), &job.LogRef{ID: "jlv-1", Version: 1, Chunks: 1},
			job.ErrInvalid, job.ChunkPlan{}},
		{"chunk 0 with a closed new log", begun, chunk(0, "a"), &job.LogRef{ID: "jlv-1", Version: 1, ClosedAt: epoch},
			job.ErrInvalid, job.ChunkPlan{}},
		{"chunk 1 before chunk 0", begun, chunk(1, "a"), newLog(), job.ErrInvalid, job.ChunkPlan{}},
		{"a step that did not begin", func(*job.Job) {}, chunk(0, "a"), newLog(), job.ErrInvalid, job.ChunkPlan{}},
		{"a step that ended", func(j *job.Job) {
			j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, FinishedAt: epoch}}
		}, chunk(0, "a"), newLog(), job.ErrInvalid, job.ChunkPlan{}},
		{"a closed log", logged(2, true), chunk(2, "c"), nil, job.ErrInvalid, job.ChunkPlan{}},
		{"the same confirmed chunk", logged(2, false), chunk(0, "a"), nil, nil,
			job.ChunkPlan{Version: "jlv-1", Duplicate: true}},
		{"a chunk with another hash", logged(2, false), chunk(0, "z"), nil, job.ErrConflict, job.ChunkPlan{}},
		{"the same reserved chunk", logged(1, false), chunk(1, "b"), nil, nil, job.ChunkPlan{Version: "jlv-1"}},
		{"the next chunk", logged(2, false), chunk(2, "c"), nil, nil, job.ChunkPlan{Version: "jlv-1", Insert: true}},
		{"a gap", logged(2, false), chunk(3, "c"), nil, job.ErrInvalid, job.ChunkPlan{}},
		{"a negative number", begun, chunk(-1, "a"), newLog(), job.ErrInvalid, job.ChunkPlan{}},
		{"a number at the limit", begun, chunk(job.MaxLogChunks, "a"), newLog(), job.ErrInvalid, job.ChunkPlan{}},
		{"a chunk above MaxLogChunk", begun, job.ChunkReserve{Seq: 0, SHA256: sum("a"), Size: job.MaxLogChunk + 1},
			newLog(), job.ErrInvalid, job.ChunkPlan{}},
		{"a chunk with no hash", begun, job.ChunkReserve{Seq: 0, SHA256: "", Size: 1}, newLog(), job.ErrInvalid,
			job.ChunkPlan{}},
	}

	lookup := func(_ string, seq int) (job.ChunkRow, bool, error) {
		r, ok := rows[seq]

		return r, ok, nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := recordJob()
			tt.prepare(j)

			got, err := job.PlanChunk(j, 0, token, "plan", tt.c, tt.newLog, lookup)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) || got != tt.plan {
				t.Fatalf("PlanChunk = %+v, %v; want %+v, %v", got, err, tt.plan, tt.want)
			}
		})
	}
}

func TestConfirmChunkRecord(t *testing.T) {
	t.Parallel()

	logged := func(chunks int, closed bool) *job.Job {
		j := recordJob()
		l := &job.LogRef{ID: "jlv-1", Version: 1, Chunks: chunks, Size: int64(chunks)}

		if closed {
			l.ClosedAt = epoch
		}

		j.Phases[0].Steps = []job.StepRecord{{Name: "plan", Token: token, StartedAt: epoch, Log: l}}

		return j
	}

	tests := []struct {
		name    string
		j       *job.Job
		version string
		seq     int
		want    error
		changed bool
	}{
		{"the next chunk", logged(2, false), "jlv-1", 2, nil, true},
		{"a chunk that counts already", logged(2, false), "jlv-1", 1, nil, false},
		{"a gap", logged(2, false), "jlv-1", 3, job.ErrInvalid, false},
		{"another version", logged(2, false), "jlv-2", 2, job.ErrInvalid, false},
		{"a closed log", logged(2, true), "jlv-1", 2, job.ErrInvalid, false},
		{"a step with no log", recordJob(), "jlv-1", 0, job.ErrInvalid, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			changed, err := job.ConfirmChunkRecord(tt.j, 0, token, "plan", tt.version, tt.seq,
				job.ChunkRow{SHA256: sum("x"), Size: 5, Confirmed: false})
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) || changed != tt.changed {
				t.Fatalf("ConfirmChunkRecord = %v, %v; want %v, %v", changed, err, tt.want, tt.changed)
			}

			if changed {
				if l := tt.j.Phases[0].Steps[0].Log; l.Chunks != 3 || l.Size != 7 {
					t.Fatalf("log after the confirm = %+v, want 3 chunks and 7 bytes", l)
				}
			}
		})
	}
}

func TestCheckPutPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(j *job.Job)
		phase   int
		current string
		want    error
	}{
		{"a plan phase at the state version", func(j *job.Job) { j.Phases[0].StateVersion = "sv-1" }, 0, "sv-1", nil},
		{"a state write after the claim", func(j *job.Job) { j.Phases[0].StateVersion = "sv-1" }, 0, "sv-2",
			job.ErrPlanStale},
		{"a unit with no state version yet", func(*job.Job) {}, 0, "", nil},
		{"a phase that has a plan", func(j *job.Job) { j.Phases[0].Plan = &job.PlanRef{ID: "pln-1"} }, 0, "",
			job.ErrConflict},
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
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("CheckPutPlan = %v, want %v", err, tt.want)
			}

			// The reason of a stale plan names both versions.
			if tt.want == job.ErrPlanStale && (!strings.Contains(err.Error(), "sv-1") || !strings.Contains(err.Error(), "sv-2")) {
				t.Fatalf("stale error %q does not name both state versions", err)
			}
		})
	}
}

func TestDeliveryResult(t *testing.T) {
	t.Parallel()

	good := func() job.DeliveryResult {
		return job.DeliveryResult{
			Delivery: job.DeliveryDirectPush, Commit: "abc123", Branch: "dustpan/job-1", DeliveredAt: epoch,
		}
	}

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
		{"a commit that is too long", func(d *job.DeliveryResult) { d.Commit = strings.Repeat("a", 129) }, false},
		{"no branch", func(d *job.DeliveryResult) { d.Branch = "" }, false},
		{"a merge time", func(d *job.DeliveryResult) { d.MergedAt = epoch }, false},
		{"no delivery time", func(d *job.DeliveryResult) { d.DeliveredAt = time.Time{} }, false},
		{"a line break in the commit", func(d *job.DeliveryResult) { d.Commit = "abc\n123" }, false},
		{"a bidirectional override in the branch", func(d *job.DeliveryResult) { d.Branch = "a‮b" }, false},
		{"a reason that is too long", func(d *job.DeliveryResult) {
			d.Delivery, d.ReviewRequest = job.DeliveryReviewRequest, "https://git.example.com/"+strings.Repeat("p", 200)
			d.Branch, d.Commit = strings.Repeat("b", 255), strings.Repeat("c", 128)
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := good()
			tt.change(&d)

			err := d.Validate()
			if (err == nil) != tt.ok || (err != nil && !errors.Is(err, job.ErrInvalid)) {
				t.Fatalf("Validate = %v, want ok %v", err, tt.ok)
			}
		})
	}

	a, b := good(), good()
	if !a.Same(&b) {
		t.Fatal("two equal results are not the same")
	}

	b.DeliveredAt = a.DeliveredAt.In(time.FixedZone("x", 3600))
	if !a.Same(&b) {
		t.Fatal("the same instant in another zone is not the same")
	}

	b.Commit = "other"
	if a.Same(&b) {
		t.Fatal("results with another commit are the same")
	}
}

func TestCheckDelivered(t *testing.T) {
	t.Parallel()

	d := job.DeliveryResult{Delivery: job.DeliveryDirectPush, Commit: "abc", Branch: "b", DeliveredAt: epoch}
	other := d
	other.Commit = "def"

	deliver := func(j *job.Job, with *job.DeliveryResult) *job.Job {
		j.Phases[0].Kind, j.Delivery = job.PhaseDeliver, with

		return j
	}

	tests := []struct {
		name    string
		j       *job.Job
		want    error
		changed bool
	}{
		{"a deliver phase", deliver(recordJob(), nil), nil, true},
		{"the same result again", deliver(recordJob(), &d), nil, false},
		{"another result", deliver(recordJob(), &other), job.ErrConflict, false},
		{"a plan phase", recordJob(), job.ErrInvalid, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			changed, err := job.CheckDelivered(tt.j, 0, &d)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) || changed != tt.changed {
				t.Fatalf("CheckDelivered = %v, %v; want %v, %v", changed, err, tt.want, tt.changed)
			}
		})
	}
}

func TestValidHash(t *testing.T) {
	t.Parallel()

	for h, want := range map[string]bool{
		sum("a"):                          true,
		"":                                false,
		strings.Repeat("0", 63):           false,
		strings.Repeat("0", 65):           false,
		strings.Repeat("g", 64):           false,
		strings.ToUpper(sum("a")):         false,
		strings.Repeat("a", 32) + "\x00z": false,
	} {
		if got := job.ValidHash(h); got != want {
			t.Errorf("ValidHash(%q) = %v, want %v", h, got, want)
		}
	}
}
