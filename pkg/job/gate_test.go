package job_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
)

func TestGateStatus(t *testing.T) {
	t.Parallel()

	approve := func(subject string) job.Approval {
		return job.Approval{
			Subject: subject, Agent: "", Verdict: job.VerdictApprove, Comment: "", Plan: nil, At: time.Time{},
		}
	}
	reject := func(subject string) job.Approval {
		a := approve(subject)
		a.Verdict = job.VerdictReject

		return a
	}

	tests := []struct {
		name      string
		need      int
		allowSelf bool
		approvals []job.Approval
		want      job.GateStatus
	}{
		{"no gate", 0, false, nil, job.GateOpen},
		{"one of one", 1, false, []job.Approval{approve("bo")}, job.GateOpen},
		{"none of one", 1, false, nil, job.GateWaiting},
		{"starter does not count", 1, false, []job.Approval{approve("al")}, job.GateWaiting},
		{"starter counts when allowed", 1, true, []job.Approval{approve("al")}, job.GateOpen},
		{"a subject counts once", 2, false, []job.Approval{approve("bo"), approve("bo")}, job.GateWaiting},
		{"two of two", 2, false, []job.Approval{approve("bo"), approve("cy")}, job.GateOpen},
		{"one rejection rejects", 1, false, []job.Approval{approve("bo"), reject("cy")}, job.GateRejected},
		{"rejection by the starter rejects", 1, false, []job.Approval{reject("al")}, job.GateRejected},
		{"rejection with no gate rejects", 0, false, []job.Approval{reject("bo")}, job.GateRejected},
		{
			"an approval through an agent does not count",
			1, false,
			[]job.Approval{viaAgent(approve("bo"))},
			job.GateWaiting,
		},
		{
			"a rejection through an agent rejects",
			1, false,
			[]job.Approval{viaAgent(reject("bo"))},
			job.GateRejected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := job.Gate{Approvals: tt.need, Groups: nil, AllowSelf: tt.allowSelf, Timeout: 0, BindPlan: false}
			if got := g.Status(tt.approvals, "al"); got != tt.want {
				t.Fatalf("Status() = %s, want %s", got, tt.want)
			}
		})
	}
}

// viaAgent returns a with the agent dusty.
func viaAgent(a job.Approval) job.Approval {
	a.Agent = "dusty"

	return a
}

func TestCheckPlan(t *testing.T) {
	t.Parallel()

	made := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	plan := func(kind job.OutputKind, sha string) *job.PlanRef {
		return &job.PlanRef{
			ID: "pln-" + sha, Kind: kind, SHA256: sha, StateVersion: "sv-7", Size: 10, CreatedAt: made,
			ShreddedAt: time.Time{},
		}
	}
	set := job.PlanSet{Display: plan(job.OutputPlanDisplay, "d1"), Apply: plan(job.OutputPlanApply, "a1")}
	bound := set.Binding()
	other := bound
	other.ApplySHA256 = "a2"

	tests := []struct {
		name      string
		set       job.PlanSet
		bound     *job.PlanBinding
		current   string
		notBefore time.Time
		want      error
	}{
		{"no plan", job.PlanSet{Display: nil, Apply: nil}, nil, "sv-9", made, nil},
		{"fresh and bound", set, &bound, "sv-7", made.Add(-time.Hour), nil},
		{"fresh with no gate", set, nil, "sv-7", made.Add(-time.Hour), nil},
		{"another plan to apply", set, &other, "sv-7", made.Add(-time.Hour), job.ErrPlanChanged},
		{"state changed", set, &bound, "sv-8", made.Add(-time.Hour), job.ErrPlanStale},
		{"too old", set, &bound, "sv-7", made.Add(time.Second), job.ErrPlanStale},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := job.CheckPlan(tt.set, tt.bound, tt.current, tt.notBefore)
			if (tt.want == nil && err != nil) || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("CheckPlan() = %v, want %v", err, tt.want)
			}
		})
	}

	if bound.DisplaySHA256 != "d1" || bound.ApplySHA256 != "a1" || bound.StateVersion != "sv-7" {
		t.Fatalf("Binding() = %+v, want both hashes and the state version", bound)
	}
}

func TestFreeze(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(14 * 24 * time.Hour)
	valid := job.Freeze{
		ID: "frz-1", Namespace: "acme", Reason: "holiday freeze", StartsAt: start, EndsAt: end,
		CreatedBy: "al", EndedBy: "",
	}

	active := []struct {
		name string
		f    job.Freeze
		at   time.Time
		want bool
	}{
		{"before the start", valid, start.Add(-time.Second), false},
		{"at the start", valid, start, true},
		{"inside", valid, start.Add(time.Hour), true},
		{"at the end", valid, end, false},
		{"open ended", job.Freeze{
			ID: "frz-2", Namespace: "acme", Reason: "incident", StartsAt: start, EndsAt: time.Time{},
			CreatedBy: "al", EndedBy: "",
		}, end.Add(1000 * time.Hour), true},
	}

	for _, tt := range active {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.f.ActiveAt(tt.at); got != tt.want {
				t.Fatalf("ActiveAt() = %t, want %t", got, tt.want)
			}
		})
	}

	invalid := map[string]func(f job.Freeze) job.Freeze{
		"no namespace":     func(f job.Freeze) job.Freeze { f.Namespace = ""; return f },
		"no reason":        func(f job.Freeze) job.Freeze { f.Reason = ""; return f },
		"no start":         func(f job.Freeze) job.Freeze { f.StartsAt = time.Time{}; return f },
		"end at start":     func(f job.Freeze) job.Freeze { f.EndsAt = f.StartsAt; return f },
		"end before start": func(f job.Freeze) job.Freeze { f.EndsAt = f.StartsAt.Add(-time.Hour); return f },
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	for name, change := range invalid {
		if err := change(valid).Validate(); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("%s: Validate() = %v, want ErrInvalid", name, err)
		}
	}
}
