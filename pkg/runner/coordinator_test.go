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
			runner.Limits{Deadline: runner.ReportTime, Grace: runner.GraceOther + runner.ReportTime},
		},
		{
			"default timeout, no change",
			[]job.Step{step("init", 0, false)},
			runner.Limits{
				Deadline: time.Hour + runner.GraceOther + runner.ReportTime,
				Grace:    runner.GraceOther + runner.ReportTime,
			},
		},
		{
			"a change",
			[]job.Step{step("init", time.Minute, false), step("apply", 2*time.Hour, true)},
			runner.Limits{
				Deadline: time.Minute + runner.GraceOther + 2*time.Hour + runner.GraceChanges + runner.ReportTime,
				Grace:    runner.GraceChanges + runner.ReportTime,
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
		runner.EventPoolCreated, runner.EventPoolChanged, runner.EventPoolDeleted, runner.EventJoinTokenCreated,
		runner.EventJoinTokenRevoked, runner.EventRunnerJoined, runner.EventKeyRotated, runner.EventRunnerDrained,
		runner.EventRunnerRetired, runner.EventCredentialsIssued, runner.EventHandoffRefused,
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
