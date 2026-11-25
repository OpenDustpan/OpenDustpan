package job_test

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
)

// matchGate returns an approval gate that needs n approvals.
func matchGate(name string, n int, allowSelf bool) job.Gate {
	return job.Gate{
		Type: job.GateApproval, Name: name, Approvals: n, Groups: nil, GroupIDs: nil, AllowSelf: allowSelf,
		Timeout: 0, BindPlan: false,
		Users:      nil,
		UserIDs:    nil,
		UnitOwners: false,
		Scope:      gate.Scope{},
	}
}

// matchApproval returns the approval of subject that can fill the gates in
// names.
func matchApproval(subject string, names ...string) job.Approval {
	return job.Approval{
		Gate: job.GateApproval, For: names, Subject: subject, Agent: "", Verdict: job.VerdictApprove,
		Comment: "", Plan: nil, At: time.Time{}, Auth: "", AuthAt: time.Time{},
	}
}

// matchCase is one row of TestGatesStatusSeveralGates.
type matchCase struct {
	name      string
	gates     []job.Gate
	approvals []job.Approval
	starter   string
	policy    gate.ApproverPolicy
	want      job.GateStatus
	filled    map[string]int
}

// row returns a case whose starter is "al" and whose policy counts everyone.
func row(
	name string, gates []job.Gate, approvals []job.Approval, want job.GateStatus, filled map[string]int,
) matchCase {
	return matchCase{
		name: name, gates: gates, approvals: approvals, starter: "al", policy: gate.ApproverPolicy{
			Production: false, IsDev: nil,
		}, want: want, filled: filled,
	}
}

// byStarter returns c with the starter st.
func (c matchCase) byStarter(st string) matchCase {
	c.starter = st

	return c
}

// underPolicy returns c with the policy p.
func (c matchCase) underPolicy(p gate.ApproverPolicy) matchCase {
	c.policy = p

	return c
}

func matchCases() []matchCase {
	a, b, c := matchGate("a", 1, false), matchGate("b", 1, false), matchGate("c", 1, false)
	a2 := matchGate("a", 2, false)
	selfA := matchGate("a", 1, true)
	d := matchGate("d", 1, false)

	viaAgent := matchApproval("ann", "a")
	viaAgent.Agent = "bot"

	rejected := matchApproval("cy", "a")
	rejected.Verdict = job.VerdictReject

	dev := gate.ApproverPolicy{Production: true, IsDev: func(s string) bool { return s == "dev-ann" }}
	two := []job.Gate{a, b}
	open := job.GateOpen
	wait := job.GateWaiting

	return []matchCase{
		row("one person who holds two gates fills one slot", two,
			[]job.Approval{matchApproval("ann", "a", "b")}, wait, map[string]int{"a": 1, "b": 0}),
		row("a second person opens both", two,
			[]job.Approval{matchApproval("ann", "a", "b"), matchApproval("bo", "b")}, open,
			map[string]int{"a": 1, "b": 1}),
		row("a greedy order misses what a maximum matching finds", two,
			[]job.Approval{matchApproval("ann", "a", "b"), matchApproval("bo", "a")}, open,
			map[string]int{"a": 1, "b": 1}),
		row("the reverse order gives the same status", two,
			[]job.Approval{matchApproval("bo", "a"), matchApproval("ann", "a", "b")}, open,
			map[string]int{"a": 1, "b": 1}),
		row("a chain of three gates needs a longer augmenting path", []job.Gate{a, b, c},
			[]job.Approval{matchApproval("ann", "a", "b"), matchApproval("bo", "b", "c"), matchApproval("cy", "a")},
			open, map[string]int{"a": 1, "b": 1, "c": 1}),
		row("two approvals of one subject fill one slot", []job.Gate{a2},
			[]job.Approval{matchApproval("ann", "a"), matchApproval("ann", "a")}, wait, map[string]int{"a": 1}),
		row("two approvals of one subject count only the gates that both name", two,
			[]job.Approval{matchApproval("ann", "a", "b"), matchApproval("ann", "b"), matchApproval("bo", "b")},
			wait, map[string]int{"a": 0, "b": 1}),
		row("an approval with an empty For fills no slot", []job.Gate{a},
			[]job.Approval{matchApproval("ann")}, wait, map[string]int{"a": 0}),
		row("an approval that names another gate fills no slot", []job.Gate{a},
			[]job.Approval{matchApproval("ann", "other")}, wait, map[string]int{"a": 0}),
		row("an agent approval fills no slot", []job.Gate{a}, []job.Approval{viaAgent}, wait,
			map[string]int{"a": 0}),
		row("a reject verdict rejects", two, []job.Approval{matchApproval("ann", "a", "b"), rejected},
			job.GateRejected, map[string]int{}),
		row("a subject that the policy does not count fills no slot", []job.Gate{a},
			[]job.Approval{matchApproval("dev-ann", "a")}, wait, map[string]int{"a": 0}).underPolicy(dev),
		row("the starter fills no gate without AllowSelf", two,
			[]job.Approval{matchApproval("st", "a", "b")}, wait, map[string]int{"a": 0, "b": 0}).byStarter("st"),
		row("the starter fills only the gate with AllowSelf", []job.Gate{selfA, b},
			[]job.Approval{matchApproval("st", "a", "b"), matchApproval("bo", "b")}, open,
			map[string]int{"a": 1, "b": 1}).byStarter("st"),
		row("the starter fills the AllowSelf gate and not the other", []job.Gate{selfA, b},
			[]job.Approval{matchApproval("st", "a", "b")}, wait, map[string]int{"a": 1, "b": 0}).byStarter("st"),
		row("two gates with one name are never open", []job.Gate{a, a},
			[]job.Approval{matchApproval("ann", "a"), matchApproval("bo", "a")}, wait,
			map[string]int{"a": 0}),
		row("more than the largest set of gates is never open", []job.Gate{a, b, c, d},
			[]job.Approval{
				matchApproval("p1", "a"), matchApproval("p2", "b"), matchApproval("p3", "c"),
				matchApproval("p4", "d"),
			}, wait, map[string]int{"a": 0, "b": 0, "c": 0, "d": 0}),
	}
}

func TestGatesStatusSeveralGates(t *testing.T) {
	t.Parallel()

	for _, tt := range matchCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkMatchCase(t, &tt)
		})
	}
}

func checkMatchCase(t *testing.T, tt *matchCase) {
	t.Helper()

	got, filled := job.GatesProgress(tt.gates, tt.approvals, tt.starter, tt.policy)
	if got != tt.want {
		t.Fatalf("GatesProgress() status = %s, want %s", got, tt.want)
	}

	if status := job.GatesStatus(tt.gates, tt.approvals, tt.starter, tt.policy); status != got {
		t.Fatalf("GatesStatus() = %s, want the status of GatesProgress, %s", status, got)
	}

	for name, want := range tt.filled {
		if filled[name] != want {
			t.Errorf("filled[%q] = %d, want %d (all: %v)", name, filled[name], want, filled)
		}
	}
}

// TestGatesStatusSeveralGatesOrderFree proves that every order of the same
// approvals gives the same status.
func TestGatesStatusSeveralGatesOrderFree(t *testing.T) {
	t.Parallel()

	gates := []job.Gate{matchGate("a", 2, false), matchGate("b", 1, false), matchGate("c", 1, false)}
	approvals := []job.Approval{
		matchApproval("ann", "a", "b"), matchApproval("bo", "a"), matchApproval("cy", "b", "c"),
		matchApproval("di", "c"),
	}

	var perms [][]job.Approval

	var permute func(k int, in []job.Approval)

	permute = func(k int, in []job.Approval) {
		if k == len(in) {
			perms = append(perms, slices.Clone(in))

			return
		}

		for i := k; i < len(in); i++ {
			in[k], in[i] = in[i], in[k]
			permute(k+1, in)
			in[k], in[i] = in[i], in[k]
		}
	}

	permute(0, slices.Clone(approvals))

	if len(perms) != 24 {
		t.Fatalf("orders = %d, want 24", len(perms))
	}

	for i, p := range perms {
		if got := job.GatesStatus(gates, p, "al", devInstallation); got != job.GateOpen {
			t.Fatalf("order %d (%v) = %s, want open: a needs bo and ann, b and c need cy and di", i, p, got)
		}
	}
}
