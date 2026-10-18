package runner_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// pool returns a valid pool of the namespace acme for purpose.
func pool(purpose runner.Purpose) runner.Pool {
	return runner.Pool{
		ID: "rp-1", Namespace: "acme", Name: "prod", Purpose: purpose, Lease: 0, Concurrency: 0,
		Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""},
		CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	}
}

func TestPoolValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(p *runner.Pool)
		ok     bool
	}{
		{"valid", func(*runner.Pool) {}, true},
		{"sweeps", func(p *runner.Pool) { p.Purpose = runner.PurposeSweeps }, true},
		{"empty name", func(p *runner.Pool) { p.Name = "" }, false},
		{"upper case name", func(p *runner.Pool) { p.Name = "Prod" }, false},
		{"name ends with dash", func(p *runner.Pool) { p.Name = "prod-" }, false},
		{"unknown purpose", func(p *runner.Pool) { p.Purpose = "scans" }, false},
		{"lease below minimum", func(p *runner.Pool) { p.Lease = 10 * time.Second }, false},
		{"lease at minimum", func(p *runner.Pool) { p.Lease = runner.MinLease }, true},
		{"lease above maximum", func(p *runner.Pool) { p.Lease = runner.MaxLease + time.Second }, false},
		{"negative concurrency", func(p *runner.Pool) { p.Concurrency = -1 }, false},
		{"concurrency at maximum", func(p *runner.Pool) { p.Concurrency = runner.MaxConcurrency }, true},
		{"concurrency above maximum", func(p *runner.Pool) { p.Concurrency = runner.MaxConcurrency + 1 }, false},
		{"unknown isolation", func(p *runner.Pool) { p.Isolation = []runner.Isolation{"vm"} }, false},
		{
			"process isolation listed",
			func(p *runner.Pool) { p.Isolation = []runner.Isolation{runner.IsolationProcess} },
			true,
		},
		{"forge git credentials", func(p *runner.Pool) { p.GitCredentials.Mode = runner.GitCredentialsForge }, true},
		{"custom helper bare", func(p *runner.Pool) {
			p.GitCredentials = runner.GitCredentials{Mode: runner.GitCredentialsCustom, Helper: "git-credential-vault"}
		}, true},
		{"custom helper absolute", func(p *runner.Pool) {
			p.GitCredentials = runner.GitCredentials{Mode: runner.GitCredentialsCustom, Helper: "/usr/bin/helper"}
		}, true},
		{"custom helper shell form", func(p *runner.Pool) {
			p.GitCredentials = runner.GitCredentials{Mode: runner.GitCredentialsCustom, Helper: "!f() { echo; }; f"}
		}, false},
		{"custom helper relative", func(p *runner.Pool) {
			p.GitCredentials = runner.GitCredentials{Mode: runner.GitCredentialsCustom, Helper: "bin/helper"}
		}, false},
		{"custom helper not clean", func(p *runner.Pool) {
			p.GitCredentials = runner.GitCredentials{Mode: runner.GitCredentialsCustom, Helper: "/usr/../bin/helper"}
		}, false},
		{"custom helper empty", func(p *runner.Pool) {
			p.GitCredentials = runner.GitCredentials{Mode: runner.GitCredentialsCustom, Helper: ""}
		}, false},
		{
			"helper without custom mode",
			func(p *runner.Pool) { p.GitCredentials.Helper = "git-credential-vault" },
			false,
		},
		{"unknown git mode", func(p *runner.Pool) { p.GitCredentials.Mode = "ssh" }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := pool(runner.PurposeJobs)
			tc.change(&p)

			err := p.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("Validate = %v, want ok %v", err, tc.ok)
			}

			if err != nil && !errors.Is(err, runner.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCheckUpdatePurposeNeverChanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(p *runner.Pool)
		want   error
	}{
		{"lease", func(p *runner.Pool) { p.Lease = 2 * time.Minute }, nil},
		{"purpose", func(p *runner.Pool) { p.Purpose = runner.PurposeSweeps }, runner.ErrImmutable},
		{"namespace", func(p *runner.Pool) { p.Namespace = "other" }, runner.ErrImmutable},
		{"id", func(p *runner.Pool) { p.ID = "rp-2" }, runner.ErrImmutable},
		{"bad name", func(p *runner.Pool) { p.Name = "" }, runner.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			old := pool(runner.PurposeJobs)
			next := pool(runner.PurposeJobs)
			tc.change(&next)

			err := runner.CheckUpdate(&old, &next)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("CheckUpdate = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestClaimKindsFollowThePurpose(t *testing.T) {
	t.Parallel()

	cases := []struct {
		purpose runner.Purpose
		want    durable.Kind
	}{
		{runner.PurposeJobs, "job.runner.rp-1"},
		{runner.PurposeSweeps, "unmanaged.sweep.rp-1"},
	}

	for _, tc := range cases {
		p := pool(tc.purpose)

		kinds, err := runner.ClaimKinds(&p)
		if err != nil || len(kinds) != 1 || kinds[0] != tc.want {
			t.Errorf("ClaimKinds(%s) = %v, %v, want [%s]", tc.purpose, kinds, err, tc.want)
		}
	}

	p := pool("other")
	if _, err := runner.ClaimKinds(&p); !errors.Is(err, runner.ErrInvalid) {
		t.Errorf("ClaimKinds(other) = %v, want ErrInvalid", err)
	}

	if got := job.RunnerKind("rp-1"); got != "job.runner.rp-1" {
		t.Errorf("job.RunnerKind = %s", got)
	}
}

func TestCheckJobPool(t *testing.T) {
	t.Parallel()

	jobs := pool(runner.PurposeJobs)
	sweeps := pool(runner.PurposeSweeps)

	cases := []struct {
		name string
		pool *runner.Pool
		ns   string
		want error
	}{
		{"same namespace, jobs", &jobs, "acme", nil},
		{"other namespace", &jobs, "other", runner.ErrInvalid},
		{"sweep pool", &sweeps, "acme", runner.ErrPurpose},
	}

	for _, tc := range cases {
		err := runner.CheckJobPool(tc.pool, namespaceName(tc.ns))
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: CheckJobPool = %v, want %v", tc.name, err, tc.want)
		}
	}

	if !sweeps.ReadOnly() || jobs.ReadOnly() {
		t.Error("only a pool for sweeps is read-only")
	}
}

func TestPoolDefaultsAndIsolation(t *testing.T) {
	t.Parallel()

	p := pool(runner.PurposeJobs)
	if p.EffectiveLease() != runner.DefaultLease || p.EffectiveConcurrency() != runner.DefaultConcurrency {
		t.Fatalf("defaults: lease %v, concurrency %d", p.EffectiveLease(), p.EffectiveConcurrency())
	}

	if runner.RenewEvery(runner.DefaultLease) != 30*time.Second {
		t.Fatalf("RenewEvery(%v) = %v, want 30s", runner.DefaultLease, runner.RenewEvery(runner.DefaultLease))
	}

	cases := []struct {
		allowed []runner.Isolation
		kind    runner.Isolation
		want    bool
	}{
		{nil, runner.IsolationKubernetes, true},
		{nil, runner.IsolationFirecracker, true},
		{nil, runner.IsolationGVisor, true},
		{nil, runner.IsolationProcess, false},
		{nil, "vm", false},
		{[]runner.Isolation{runner.IsolationProcess}, runner.IsolationProcess, true},
		{[]runner.Isolation{runner.IsolationFirecracker}, runner.IsolationGVisor, false},
	}

	for _, tc := range cases {
		p.Isolation = tc.allowed
		if got := p.Allows(tc.kind); got != tc.want {
			t.Errorf("Allows(%s) with %v = %v, want %v", tc.kind, tc.allowed, got, tc.want)
		}
	}
}

func TestIsolationFitsAndHandoff(t *testing.T) {
	t.Parallel()

	cases := []struct {
		iso     runner.Isolation
		kind    runner.Kind
		fits    bool
		handoff runner.Handoff
	}{
		{runner.IsolationKubernetes, runner.KindKubernetes, true, runner.HandoffKubernetes},
		{runner.IsolationKubernetes, runner.KindMachine, false, runner.HandoffKubernetes},
		{runner.IsolationFirecracker, runner.KindMachine, true, runner.HandoffVsock},
		{runner.IsolationGVisor, runner.KindMachine, true, runner.HandoffStdin},
		{runner.IsolationGVisor, runner.KindKubernetes, false, runner.HandoffStdin},
		{runner.IsolationProcess, runner.KindMachine, true, runner.HandoffPipe},
	}

	for _, tc := range cases {
		if got := tc.iso.Fits(tc.kind); got != tc.fits {
			t.Errorf("%s.Fits(%s) = %v, want %v", tc.iso, tc.kind, got, tc.fits)
		}

		h, err := runner.HandoffFor(tc.iso)
		if err != nil || h != tc.handoff {
			t.Errorf("HandoffFor(%s) = %s, %v, want %s", tc.iso, h, err, tc.handoff)
		}
	}

	if _, err := runner.HandoffFor("vm"); !errors.Is(err, runner.ErrInvalid) {
		t.Errorf("HandoffFor(vm) = %v, want ErrInvalid", err)
	}
}

func TestRunnerStatus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		change func(r *runner.Runner)
		want   runner.Status
	}{
		{"online", func(*runner.Runner) {}, runner.StatusOnline},
		{
			"offline",
			func(r *runner.Runner) { r.LastSeenAt = now.Add(-runner.OfflineAfter - time.Second) },
			runner.StatusOffline,
		},
		{"draining", func(r *runner.Runner) { r.Draining = true }, runner.StatusDraining},
		{"key expired wins over draining", func(r *runner.Runner) {
			r.Draining = true
			r.Key.CreatedAt = now.Add(-runner.MaxKeyAge - time.Second)
		}, runner.StatusMustRejoin},
		{"retired wins", func(r *runner.Runner) {
			r.RetiredAt = now
			r.Key.CreatedAt = now.Add(-runner.MaxKeyAge - time.Second)
		}, runner.StatusRetired},
	}

	for _, tc := range cases {
		r := member(now)
		tc.change(&r)

		if got := r.StatusAt(now); got != tc.want {
			t.Errorf("%s: StatusAt = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestCanRejoin(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		change func(r *runner.Runner)
		want   bool
	}{
		{"online", func(*runner.Runner) {}, false},
		{"draining and online", func(r *runner.Runner) { r.Draining = true }, false},
		{"offline", func(r *runner.Runner) { r.LastSeenAt = now.Add(-runner.OfflineAfter - time.Second) }, true},
		{"must rejoin", func(r *runner.Runner) { r.Key.CreatedAt = now.Add(-runner.MaxKeyAge - time.Second) }, true},
		{"retired", func(r *runner.Runner) {
			r.RetiredAt = now
			r.LastSeenAt = now.Add(-time.Hour)
		}, false},
	}

	for _, tc := range cases {
		r := member(now)
		tc.change(&r)

		if got := r.CanRejoin(now); got != tc.want {
			t.Errorf("%s: CanRejoin = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestJoinRequestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(r *runner.JoinRequest)
		ok     bool
	}{
		{"valid", func(*runner.JoinRequest) {}, true},
		{"no token", func(r *runner.JoinRequest) { r.Token = "" }, false},
		{"bad name", func(r *runner.JoinRequest) { r.Name = "A_B" }, false},
		{"unknown kind", func(r *runner.JoinRequest) { r.Kind = "vm" }, false},
		{"isolation of the other kind", func(r *runner.JoinRequest) { r.Isolation = runner.IsolationGVisor }, false},
		{"short key", func(r *runner.JoinRequest) { r.Key.Public = []byte{1, 2, 3} }, false},
		{"too many labels", func(r *runner.JoinRequest) {
			r.Labels = make(map[string]string, runner.MaxLabels+1)
			for i := range runner.MaxLabels + 1 {
				r.Labels[string(rune('a'+i%26))+string(rune('a'+i/26))] = "x"
			}
		}, false},
	}

	for _, tc := range cases {
		req := joinRequest(t)
		tc.change(&req)

		err := req.Validate()
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, runner.ErrInvalid)) {
			t.Errorf("%s: Validate = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}
