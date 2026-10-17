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
		return job.Approval{Subject: subject, Verdict: job.VerdictApprove, Comment: "", PlanSHA256: "", At: time.Time{}}
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
