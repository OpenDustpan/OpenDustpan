package jobstore_test

import (
	"errors"
	"fmt"
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

// TestBodyChecksTheEnumValues proves that each source kind, agent mode and
// verification result that this server writes survives the body.
func TestBodyChecksTheEnumValues(t *testing.T) {
	t.Parallel()

	set := map[string]func(j *job.Job, value string){
		"source":       func(j *job.Job, value string) { j.Source = job.SourceKind(value) },
		"agent mode":   func(j *job.Job, value string) { j.AgentMode = job.AgentMode(value) },
		"verification": func(j *job.Job, value string) { j.Verification = job.Verification(value) },
	}
	read := map[string]func(j job.Job) string{
		"source":       func(j job.Job) string { return string(j.Source) },
		"agent mode":   func(j job.Job) string { return string(j.AgentMode) },
		"verification": func(j job.Job) string { return string(j.Verification) },
	}

	tests := []struct {
		field string
		value string
	}{
		{"source", string(job.SourceAPI)},
		{"source", string(job.SourceAgent)},
		{"source", string(job.SourceSelfService)},
		{"source", string(job.SourceForge)},
		{"source", string(job.SourceSchedule)},
		{"source", string(job.SourceTrigger)},
		{"agent mode", ""},
		{"agent mode", string(job.AgentModeApproveFirst)},
		{"agent mode", string(job.AgentModeReviewRequest)},
		{"agent mode", string(job.AgentModeDirect)},
		{"verification", string(job.VerificationNone)},
		{"verification", string(job.VerificationPending)},
		{"verification", string(job.VerificationVerified)},
		{"verification", string(job.VerificationUnverified)},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %q", tt.field, tt.value), func(t *testing.T) {
			t.Parallel()

			j := fullJob(t)
			set[tt.field](&j, tt.value)

			body, err := jobstore.EncodeJobBody(&j)
			if err != nil {
				t.Fatalf("EncodeJobBody = %v", err)
			}

			var got job.Job
			if err := jobstore.DecodeJobBody(body, &got); err != nil {
				t.Fatalf("DecodeJobBody of a %s %q = %v", tt.field, tt.value, err)
			}

			if value := read[tt.field](got); value != tt.value {
				t.Fatalf("decoded %s = %q, want %q", tt.field, value, tt.value)
			}
		})
	}
}

// TestBodyRefusesDamagedEnumValues proves that a body whose source kind,
// agent mode or verification result is not one that this server writes is a
// damaged stored row: the decode wraps ErrDamaged, not job.ErrInvalid, and
// the text names the field and the job ID and never the stored value. A
// stored row is untrusted: a restore or another version may have written it.
func TestBodyRefusesDamagedEnumValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		from  string
		to    string
		value string
	}{
		{"source", `"source":"schedule"`, `"source":"bogus"`, "bogus"},
		{"agent mode", `"agentMode":"direct"`, `"agentMode":"bogus"`, "bogus"},
		{"verification", `"verification":"pending"`, `"verification":"bogus"`, "bogus"},
		{"source", `"source":"schedule"`, `"source":""`, ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %q", tt.field, tt.value), func(t *testing.T) {
			t.Parallel()

			body := damagedBody(t, tt.from, tt.to)

			var got job.Job
			got.ID = "job-1"

			err := jobstore.DecodeJobBody(body, &got)
			if !errors.Is(err, jobstore.ErrDamaged) {
				t.Fatalf("DecodeJobBody of a %s %q = %v, want ErrDamaged", tt.field, tt.value, err)
			}

			if errors.Is(err, job.ErrInvalid) {
				t.Fatalf("DecodeJobBody of a %s %q = %v, want no job.ErrInvalid", tt.field, tt.value, err)
			}

			if !strings.Contains(err.Error(), tt.field) || !strings.Contains(err.Error(), string(got.ID)) {
				t.Fatalf("DecodeJobBody of a %s %q = %v, want the field and the job ID in the text", tt.field,
					tt.value, err)
			}

			if tt.value != "" && strings.Contains(err.Error(), tt.value) {
				t.Fatalf("DecodeJobBody of a %s %q = %v, want no stored value in the text", tt.field, tt.value, err)
			}
		})
	}
}

// damagedBody returns the body of a full job with the text from replaced by
// to. A write refuses a value that this server never writes, and a stored row
// is untrusted, so the damaged body starts from one that this server writes.
func damagedBody(t *testing.T, from, to string) string {
	t.Helper()

	j := fullJob(t)

	body, err := jobstore.EncodeJobBody(&j)
	if err != nil {
		t.Fatalf("EncodeJobBody = %v", err)
	}

	damaged := strings.Replace(body, from, to, 1)
	if damaged == body {
		t.Fatalf("the body has no %s member", from)
	}

	return damaged
}

// TestEncodeBodyRefusesUnknownEnumValues proves that a write of a source
// kind, an agent mode or a verification result that this server never writes
// fails with job.ErrInvalid: the write never stores a row that cannot decode.
func TestEncodeBodyRefusesUnknownEnumValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		value string
	}{
		{"source", "bogus"},
		{"agent mode", "bogus"},
		{"verification", "bogus"},
		{"source", ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %q", tt.field, tt.value), func(t *testing.T) {
			t.Parallel()

			j := fullJob(t)

			switch tt.field {
			case "source":
				j.Source = job.SourceKind(tt.value)
			case "agent mode":
				j.AgentMode = job.AgentMode(tt.value)
			case "verification":
				j.Verification = job.Verification(tt.value)
			default:
				t.Fatalf("unknown field %q", tt.field)
			}

			if _, err := jobstore.EncodeJobBody(&j); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("EncodeJobBody of a %s %q = %v, want job.ErrInvalid", tt.field, tt.value, err)
			}
		})
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

// TestDamageReasonNamesTheKindOnly proves that the reason of a damaged row is
// one fixed text for each kind of damage: it never echoes a value of the
// stored body, which the error of the decode carries. A reason reaches a log
// line and an API answer.
func TestDamageReasonNamesTheKindOnly(t *testing.T) {
	t.Parallel()

	// A value that a body of another version of the server could hold. It
	// must not reach a log or an API answer.
	const stored = "top-secret-stored-value"

	unit := `"unit":{"tool":"opentofu","namespace":"acme","name":"net","unitID":"u"}`

	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"not JSON": {`{"spec":`, "the body is not JSON"},
		"a source that this version does not know": {
			`{"spec":{` + unit + `},"source":"` + stored + `"}`,
			"the body holds a value that this version does not know",
		},
		"an agent mode that this version does not know": {
			`{"spec":{` + unit + `},"source":"api","agentMode":"` + stored + `"}`,
			"the body holds a value that this version does not know",
		},
		"a verification that this version does not know": {
			`{"spec":{` + unit + `},"source":"api","verification":"` + stored + `"}`,
			"the body holds a value that this version does not know",
		},
		"a unit reference that this version does not know": {
			`{"spec":{"unit":{"tool":"","namespace":"acme","name":"net","unitID":"u"}},"source":"api"}`,
			"the body holds a unit reference that this version does not know",
		},
		"a trigger reference that is not valid": {
			`{"spec":{` + unit + `},"source":"api","trigger":{"id":"","saver":""}}`,
			"the body holds a trigger reference that is not valid",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var j job.Job

			err := jobstore.DecodeJobBody(tc.body, &j)
			if err == nil {
				t.Fatalf("DecodeJobBody(%s) = nil, want an error", tc.body)
			}

			reason := jobstore.DamageReason(err)
			if reason != tc.want {
				t.Fatalf("DamageReason = %q, want %q", reason, tc.want)
			}

			if strings.Contains(reason, stored) {
				t.Fatalf("the reason %q holds a value of the body", reason)
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
