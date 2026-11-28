package profiletest_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// newPolicyFixture returns a fixture whose service has the options opts.
func newPolicyFixture(t *testing.T, opts ...profile.Option) fixture {
	t.Helper()

	m := newMem()
	m.namespaces["acme"] = namespace.Namespace{
		ID: "", CreatedAt: epochTime(), Name: "acme", PrimaryInstallation: "", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	svc, err := profile.NewService(profile.Config{
		Store: m, Namespaces: memNamespaces{m: m}, Sealer: scopedSealer{}, Audit: memAudit{m: m},
		Authorizer: grants{"admin": {read, write, open, manage}, "writer": {read, write}},
		Sessions:   memSessions{m: m}, Evaluator: stubEvaluator{}, Now: nil, Logger: nil,
	}, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return fixture{svc: svc, m: m, ns: "acme"}
}

// TestRevealFollowsFreshPolicy proves that the service asks for a fresh step
// on a reveal only when the policy requires one. With the setting off, a
// reveal with no step passes and writes no failure entry. With the setting on,
// or with no policy, the same reveal is refused and writes one failure entry.
// The "setting off" row fails when the guard in requireFresh is removed, and
// the "setting on" and "no policy" rows fail when the check of the step is
// removed.
func TestRevealFollowsFreshPolicy(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	for name, tc := range map[string]struct {
		opts     []profile.Option
		want     error
		failures int
	}{
		"setting off":         {opts: []profile.Option{profile.WithFreshPolicy(off)}, want: nil, failures: 0},
		"setting on":          {opts: []profile.Option{profile.WithFreshPolicy(on)}, want: profile.ErrFreshRequired, failures: 1},
		"no policy":           {opts: nil, want: profile.ErrFreshRequired, failures: 1},
		"nil policy":          {opts: []profile.Option{profile.WithFreshPolicy(nil)}, want: profile.ErrFreshRequired, failures: 1},
		"nil policy func":     {opts: []profile.Option{profile.WithFreshPolicy(freshstep.PolicyFunc(nil))}, want: profile.ErrFreshRequired, failures: 1},
		"nil option":          {opts: []profile.Option{nil}, want: profile.ErrFreshRequired, failures: 1},
		"nil option then off": {opts: []profile.Option{nil, profile.WithFreshPolicy(off)}, want: nil, failures: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newPolicyFixture(t, tc.opts...)
			p := f.create(t)
			before := len(f.m.entries[f.ns])

			v, err := f.svc.Read(t.Context(), actor("admin"), f.target(profile.RefByID(p.ID)),
				profile.ReadOptions{Fresh: nil, Revision: 0, Reveal: true})
			if !errors.Is(err, tc.want) {
				t.Fatalf("reveal: error = %v, want %v", err, tc.want)
			}

			checkRevealSecrets(t, v.Secrets, tc.want)

			if got := countRevealFailures(f.m.entries[f.ns][before:]); got != tc.failures {
				t.Fatalf("failure entries = %d, want %d", got, tc.failures)
			}
		})
	}
}

// checkRevealSecrets checks that secrets holds the revealed password when the
// reveal passed (want is nil), and no secret when it was refused.
func checkRevealSecrets(t *testing.T, secrets map[string]string, want error) {
	t.Helper()

	if want != nil && len(secrets) != 0 {
		t.Errorf("secrets left with a refused reveal: %v", secrets)
	}

	if want == nil && secrets["db.password"] == "" {
		t.Errorf("secrets = %v, want the revealed password", secrets)
	}
}

// countRevealFailures counts the entries of the kind reveal with the outcome
// failure.
func countRevealFailures(entries []audit.Entry) int {
	n := 0

	for i := range entries {
		if entries[i].Kind == audit.KindReveal && entries[i].Outcome == audit.OutcomeFailure {
			n++
		}
	}

	return n
}
