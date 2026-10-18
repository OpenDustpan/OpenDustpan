package runner_test

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// step returns a step of the apply phase with timeout and changes.
func step(name string, timeout time.Duration, changes bool) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", "apply"}, Env: nil, Dir: "",
		Timeout: timeout, AllowFailure: false, ChangesResources: changes, Outputs: nil,
	}
}

func TestLimitsFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		steps []job.Step
		want  runner.Limits
	}{
		{
			"no step", nil,
			runner.Limits{
				Deadline: runner.StartAllowance + runner.ReportTime,
				Grace:    runner.GraceOther + runner.ReportTime,
			},
		},
		{
			"default timeout, no change",
			[]job.Step{step("init", 0, false)},
			runner.Limits{
				Deadline: runner.StartAllowance + time.Hour + runner.GraceOther + runner.ReportTime,
				Grace:    runner.GraceOther + runner.ReportTime,
			},
		},
		{
			"a change",
			[]job.Step{step("init", time.Minute, false), step("apply", 2*time.Hour, true)},
			runner.Limits{
				Deadline: runner.StartAllowance + time.Minute + runner.GraceOther + 2*time.Hour + runner.GraceChanges +
					runner.ReportTime,
				Grace: runner.GraceChanges + runner.ReportTime,
			},
		},
	}

	for _, tc := range cases {
		if got := runner.LimitsFor(tc.steps); got != tc.want {
			t.Errorf("%s: LimitsFor = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestKubernetesJobName(t *testing.T) {
	t.Parallel()

	dns := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	a := runner.KubernetesJobName("op-1", 1)

	if a != runner.KubernetesJobName("op-1", 1) {
		t.Fatal("the name of one claim changes")
	}

	if a == runner.KubernetesJobName("op-1", 2) || a == runner.KubernetesJobName("op-2", 1) {
		t.Fatal("two claims have one name")
	}

	if len(a) != 28 || !dns.MatchString(a) {
		t.Fatalf("name %q is not a DNS label of 28 characters", a)
	}
}

func TestCheckHandoffPod(t *testing.T) {
	t.Parallel()

	want := runner.KubernetesJob{
		Name: "dustpan-abc", Namespace: "runners", Image: "", LauncherImage: "", ServiceAccount: "unit-network",
		Labels: map[string]string{runner.LabelOperation: "op-1", runner.LabelClaim: "1"}, Args: nil,
		ActiveDeadline: 0, TerminationGrace: 0, MemoryVolumeSize: 0,
	}
	pod := func() runner.PodIdentity {
		return runner.PodIdentity{
			Namespace: "runners", Name: "dustpan-abc-x1", UID: "uid-1", ServiceAccount: "unit-network",
			Job: "dustpan-abc", Labels: map[string]string{runner.LabelOperation: "op-1", runner.LabelClaim: "1"},
		}
	}

	cases := []struct {
		name     string
		reviewed func(p *runner.PodIdentity)
		read     func(p *runner.PodIdentity)
		ok       bool
	}{
		{"the pod of the Job", func(*runner.PodIdentity) {}, func(*runner.PodIdentity) {}, true},
		{"another UID", func(p *runner.PodIdentity) { p.UID = "uid-2" }, func(*runner.PodIdentity) {}, false},
		{"no UID", func(p *runner.PodIdentity) { p.UID = "" }, func(p *runner.PodIdentity) { p.UID = "" }, false},
		{
			"another namespace", func(p *runner.PodIdentity) { p.Namespace = "default" },
			func(p *runner.PodIdentity) { p.Namespace = "default" }, false,
		},
		{
			"another service account", func(p *runner.PodIdentity) { p.ServiceAccount = "admin" },
			func(p *runner.PodIdentity) { p.ServiceAccount = "admin" }, false,
		},
		{"a pod of another Job", func(*runner.PodIdentity) {}, func(p *runner.PodIdentity) { p.Job = "other" }, false},
		{
			"a pod with no Job and copied labels",
			func(*runner.PodIdentity) {},
			func(p *runner.PodIdentity) { p.Job = "" },
			false,
		},
		{
			"labels of another claim", func(*runner.PodIdentity) {},
			func(p *runner.PodIdentity) {
				p.Labels = map[string]string{runner.LabelOperation: "op-1", runner.LabelClaim: "2"}
			},
			false,
		},
	}

	for _, tc := range cases {
		reviewed, read := pod(), pod()
		tc.reviewed(&reviewed)
		tc.read(&read)

		err := runner.CheckHandoffPod(reviewed, read, want)
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, runner.ErrBadProof)) {
			t.Errorf("%s: CheckHandoffPod = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestEveryEventHasAnAuditRule(t *testing.T) {
	t.Parallel()

	events := []runner.Event{
		runner.EventPoolCreated,
		runner.EventPoolChanged,
		runner.EventPoolDeleted,
		runner.EventJoinTokenCreated,
		runner.EventJoinTokenRevoked,
		runner.EventRunnerJoined,
		runner.EventRunnerRejoined,
		runner.EventKeyRotated,
		runner.EventRunnerDrained,
		runner.EventRunnerRetired,
		runner.EventCredentialsIssued,
		runner.EventHandoffRefused,
	}
	rules := runner.AuditRules()

	if len(rules) != len(events) {
		t.Fatalf("AuditRules has %d rules, want %d", len(rules), len(events))
	}

	for _, e := range events {
		r, ok := rules[e]
		if !ok || r.Action == "" || r.Kind == "" {
			t.Errorf("event %s has no audit rule", e)
		}
	}

	if rules[runner.EventCredentialsIssued].Kind != audit.KindReveal {
		t.Error("the hand of credentials to a session is a reveal")
	}
}

func TestServiceAccountFor(t *testing.T) {
	t.Parallel()

	sa := runner.ServiceAccounts{
		Own: "dustpan-coordinator", Sweep: "dustpan-sweep-readonly", Default: "units",
		Allowed: []string{"units", "network", "dustpan-coordinator"},
	}

	cases := []struct {
		name    string
		purpose runner.Purpose
		account string
		want    string
		ok      bool
	}{
		{"job with the default", runner.PurposeJobs, "", "units", true},
		{"job with an allowed account", runner.PurposeJobs, "network", "network", true},
		{"job with an account that is not allowed", runner.PurposeJobs, "cluster-admin", "", false},
		{"job as the coordinator, also when listed", runner.PurposeJobs, "dustpan-coordinator", "", false},
		{"sweep takes the read-only account", runner.PurposeSweeps, "", "dustpan-sweep-readonly", true},
		{"sweep that names an account", runner.PurposeSweeps, "network", "", false},
		{"unknown purpose", "scans", "", "", false},
	}

	for _, tc := range cases {
		got, err := runner.ServiceAccountFor(tc.purpose, new(work(tc.account)), &sa)
		if tc.ok != (err == nil) || got != tc.want || (err != nil && !errors.Is(err, runner.ErrInvalid)) {
			t.Errorf("%s: ServiceAccountFor = %q, %v, want %q ok %v", tc.name, got, err, tc.want, tc.ok)
		}
	}

	own := sa
	own.Sweep = own.Own

	_, err := runner.ServiceAccountFor(runner.PurposeSweeps, new(work("")), &own)
	if !errors.Is(err, runner.ErrInvalid) {
		t.Errorf("ServiceAccountFor a sweep as the coordinator = %v, want ErrInvalid", err)
	}
}

func TestCheckRunnerRenew(t *testing.T) {
	t.Parallel()

	claimed := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)

	if err := runner.CheckRunnerRenew(claimed, false, claimed.Add(runner.MaxSessionWait)); err != nil {
		t.Errorf("CheckRunnerRenew at the wait limit = %v, want nil", err)
	}

	err := runner.CheckRunnerRenew(claimed, true, claimed.Add(time.Minute))
	if !errors.Is(err, runner.ErrSessionOpen) || errors.Is(err, runner.ErrClaimLost) {
		t.Errorf("CheckRunnerRenew with a session = %v, want ErrSessionOpen and not ErrClaimLost", err)
	}

	err = runner.CheckRunnerRenew(claimed, false, claimed.Add(runner.MaxSessionWait+time.Second))
	if !errors.Is(err, runner.ErrClaimLost) {
		t.Errorf("CheckRunnerRenew after the wait limit = %v, want ErrClaimLost", err)
	}
}

// work returns the work of a phase with the service account sa.
func work(sa string) runner.Work {
	return runner.Work{
		Operation: "op-1", Token: 1, ExpiresAt: time.Time{}, Lease: runner.DefaultLease, Job: "", Phase: 0, Sweep: "",
		Image: "", ServiceAccount: sa, Deadline: 0, Grace: 0,
	}
}
