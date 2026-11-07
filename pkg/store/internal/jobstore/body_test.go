package jobstore_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// fullJob returns a job with every field of the body set to a value that is
// not the zero value of its type, so that a field that the body forgets
// shows in the comparison.
func fullJob(t *testing.T) job.Job {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", "acme", "net")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	unit = unit.WithUnitID("unt-1")
	at := time.Date(2026, 10, 20, 9, 30, 15, 123456789, time.UTC)
	binding := job.PlanBinding{Plan: "pln-1", DisplaySHA256: "aa", ApplySHA256: "bb", StateVersion: "sv-1"}

	return job.Job{
		ID: "job-1",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationApply, Purpose: "", Executor: job.ExecutorWriteBack,
			Location: job.Location{
				Connection: "gh", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: "abc",
			},
			LocationSource: job.LocationSourceUnit,
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"},
				Env: map[string]string{"A": "b"}, Dir: "sub", Timeout: time.Minute, AllowFailure: true,
				ChangesResources: true, Outputs: []job.Output{{Kind: job.OutputPlanDisplay, Path: "plan.json"}},
			}},
			Pool: "pool-1", Profile: "prf-1", Defaulted: []string{"pool", "profile"},
			Edits:    []job.FileEdit{{Path: "a.json", Keys: map[string][]byte{"k": []byte(`"v"`)}}},
			Delivery: job.DeliveryReviewRequest,
		},
		State:  job.StateAwaitingState,
		Wait:   job.WaitFrozen,
		Reason: "because",
		Phases: []job.Phase{{
			Kind: job.PhaseDeliver, Operation: "dop-1", Claims: 2, Executor: "runner-1",
			Steps: []job.StepRecord{{
				Name: "plan", Token: 3, ExitCode: 2, StartedAt: at, FinishedAt: at.Add(time.Second),
				Log: &job.LogRef{
					ID: "jlv-1", ItemKey: "ik-1", Version: 2, Chunks: 3, SHA256: "cc", Size: 99, CreatedAt: at,
					ClosedAt: at, ExpiresAt: at, RedactedAt: at, ReplacedBy: "bo", Matches: 4,
				},
			}},
			StateVersion: "sv-1",
			Plan: &job.PlanRef{
				ID:           "pln-1",
				ItemKey:      "ik-2",
				StateVersion: "sv-1",
				Parts: []job.PlanPart{
					{
						Kind: job.OutputPlanApply, Size: 5, SHA256: "dd", Secret: true, StoredSize: 7,
						StoredMD5: "00112233445566778899aabbccddeeff",
					},
				},
				CreatedAt:  at,
				ExpiresAt:  at,
				RedactedAt: at,
			},
			Gates: []job.Gate{
				{
					Type:      job.GateConfirmation,
					Approvals: 2,
					Groups:    []string{"ops"},
					GroupIDs:  nil,
					AllowSelf: true,
					Timeout:   time.Hour,
					BindPlan:  true,
				},
			},
			Approvals: []job.Approval{{
				Gate: job.GateApproval, Subject: "bo", Agent: "dusty", Verdict: job.VerdictReject, Comment: "no",
				Plan: &binding, At: at, Auth: job.ConfirmAuthMFA, AuthAt: at,
			}},
			StartedAt: at, FinishedAt: at,
		}},
		Current:    1,
		StartedBy:  "ada",
		Source:     job.SourceSchedule,
		Agent:      "dusty",
		AgentMode:  job.AgentModeDirect,
		BreakGlass: &job.BreakGlass{Reason: "outage", Freeze: "frz-1"},
		Trigger:    &job.TriggerRef{ID: "trg-1", Saver: "user:ada"},
		Delivery: &job.DeliveryResult{
			Delivery: job.DeliveryReviewRequest, Commit: "c0ffee", Branch: "b", ReviewRequest: "https://x/1",
			MergedAt: at, DeliveredAt: at,
		},
		Closed: &job.StateVersion{
			Unit: unit, VersionID: "sv-2", Commit: "c0ffee", Job: "job-1", At: at, Final: true,
		},
		Verification:   job.VerificationPending,
		IdempotencyKey: "key",
		RequestHash:    "hash",
		Version:        7,
		CreatedAt:      at,
		UpdatedAt:      at,
		StateChangedAt: time.Time{},
	}
}

// TestBodyRoundTrip proves that the body keeps every field that no column
// holds: a job that is encoded and decoded is the job.
func TestBodyRoundTrip(t *testing.T) {
	t.Parallel()

	want := fullJob(t)

	body, err := jobstore.EncodeJobBody(&want)
	if err != nil {
		t.Fatalf("EncodeBody = %v", err)
	}

	// The columns hold these fields, so the body does not.
	var got job.Job

	got.ID, got.State, got.Wait, got.Current, got.IdempotencyKey = want.ID, want.State, want.Wait, want.Current,
		want.IdempotencyKey
	got.Version, got.CreatedAt, got.UpdatedAt = want.Version, want.CreatedAt, want.UpdatedAt

	if err := jobstore.DecodeJobBody(body, &got); err != nil {
		t.Fatalf("DecodeBody = %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded job differs from the job:\n got %+v\nwant %+v", got, want)
	}
}

// TestBodyRefusesDamagedBodies proves that a body that is not valid JSON, or
// names a unit that is not valid, is an error and never a job: the body can
// come from a restored backup.
func TestBodyRefusesDamagedBodies(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"not JSON":          "{",
		"empty":             "",
		"unit with no tool": `{"spec":{"unit":{"tool":"","namespace":"acme","name":"net","unitID":"u"}}}`,
		"unit in a namespace that is not valid": `{"spec":{"unit":{"tool":"opentofu","namespace":"A B","name":"n",` +
			`"unitID":"u"}}}`,
		"a type that does not fit": `{"spec":{"unit":7}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var j job.Job
			if err := jobstore.DecodeJobBody(body, &j); err == nil {
				t.Fatalf("DecodeBody(%s) = nil, want an error", strings.TrimSpace(body))
			}
		})
	}
}

// TestRowMustAgreeWithItsBody proves that a row whose columns name another
// namespace or another unit than its body is refused: each layer of the
// fence and of the one-change rule reads a different copy of these values.
func TestRowMustAgreeWithItsBody(t *testing.T) {
	t.Parallel()

	j := fullJob(t)

	body, err := jobstore.EncodeJobBody(&j)
	if err != nil {
		t.Fatalf("EncodeBody = %v", err)
	}

	row := func(ns, unit string) jobstore.Row {
		return jobstore.Row{
			ID: j.ID, Namespace: namespace.Name(ns), UnitID: unit, Seq: 1, State: j.State, Wait: j.Wait,
			Phase: j.Current, Changes: false, Operation: "", Key: j.IdempotencyKey, Version: j.Version, Body: body,
			CreatedMs: j.CreatedAt.UnixMilli(), UpdatedMs: j.UpdatedAt.UnixMilli(),
			StateChangedMs: 0,
		}
	}

	agreeing := row("acme", "unt-1")
	if _, err := jobstore.RowToJob(&agreeing); err != nil {
		t.Fatalf("JobOf of a row that agrees = %v", err)
	}

	for name, r := range map[string]jobstore.Row{
		"another namespace": row("beta", "unt-1"),
		"another unit":      row("acme", "unt-2"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := jobstore.RowToJob(&r); !errors.Is(err, jobstore.ErrDamaged) {
				t.Fatalf("JobOf = %v, want ErrDamaged", err)
			}
		})
	}
}

// TestBodyKeepsThePurpose proves that the purpose of a job survives the body:
// the drift store reads it to tell a drift check from any other job, and a job
// that an older server stored has none.
func TestBodyKeepsThePurpose(t *testing.T) {
	t.Parallel()

	for _, purpose := range []job.Purpose{job.PurposeNone, job.PurposeDriftCheck} {
		j := fullJob(t)
		j.Spec.Purpose = purpose

		body, err := jobstore.EncodeJobBody(&j)
		if err != nil {
			t.Fatalf("EncodeBody = %v", err)
		}

		var got job.Job
		if err := jobstore.DecodeJobBody(body, &got); err != nil {
			t.Fatalf("DecodeBody = %v", err)
		}

		if got.Spec.Purpose != purpose {
			t.Errorf("purpose = %q, want %q", got.Spec.Purpose, purpose)
		}
	}

	// A body without the member, as an older server wrote it, has no purpose.
	j := fullJob(t)

	body, err := jobstore.EncodeJobBody(&j)
	if err != nil {
		t.Fatalf("EncodeBody = %v", err)
	}

	old := strings.Replace(body, `"purpose":"",`, "", 1)
	if old == body {
		t.Fatal("the body has no purpose member to remove")
	}

	var got job.Job
	if err := jobstore.DecodeJobBody(old, &got); err != nil || got.Spec.Purpose != job.PurposeNone {
		t.Fatalf("DecodeBody of an old body = purpose %q, %v; want none", got.Spec.Purpose, err)
	}
}
