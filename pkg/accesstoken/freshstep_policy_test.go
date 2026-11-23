package accesstoken_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/freshstep"
)

// policyConfig returns the config of a token service over the store of f
// with the fresh step guard g and the logger logger.
func policyConfig(f *fixture, g freshstep.Guard, logger *slog.Logger) accesstoken.Config {
	return accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil,
		Store:      f.store, Principals: f.store, Freshness: g, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: logger, Now: f.clock.Now, NewID: nil,
	}
}

// newPolicyService makes a token service over the store of f with the fresh
// step guard g.
func newPolicyService(t *testing.T, f *fixture, g freshstep.Guard) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(policyConfig(f, g, nil))
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	return svc
}

func TestFreshPolicyGatesTheStep(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	cases := []struct {
		name    string
		policy  freshstep.Policy
		wantErr error
	}{
		{name: "setting off passes", policy: off, wantErr: nil},
		{name: "setting on refuses", policy: on, wantErr: errStale},
		{name: "no policy refuses", policy: freshstep.PolicyFunc(nil), wantErr: errStale},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			svc := newPolicyService(t, &f, *freshGuard(t, tc.policy))

			// Create with the service principal, as an administrator.
			_, _, err := svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
			checkFreshErr(t, "create", err, tc.wantErr)

			tok, _ := f.create(t, f.service, "seed")

			_, _, err = svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
				OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService,
			})
			checkFreshErr(t, "rotate", err, tc.wantErr)
		})
	}
}

// checkFreshErr fails the test when err of the operation op is not want. A
// nil want means no error.
func checkFreshErr(t *testing.T, op string, err, want error) {
	t.Helper()

	if want == nil && err != nil {
		t.Fatalf("%s: err = %v, want no error", op, err)
	}

	if want != nil && !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want the freshness error %v", op, err, want)
	}
}

// NewGuard refuses a nil policy, so a service never holds a guard that skips
// the step for want of a policy.
func TestFreshGuardRefusesNilPolicy(t *testing.T) {
	t.Parallel()

	_, err := freshstep.NewGuard(nil, staleMFA{}.RequireFresh)
	if !errors.Is(err, freshstep.ErrNilPolicy) {
		t.Fatalf("new guard: err = %v, want ErrNilPolicy", err)
	}
}

// NewService refuses a zero guard: a service never runs without the fresh
// step guard.
func TestNewServiceRefusesZeroFreshGuard(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	svc, err := accesstoken.NewService(policyConfig(&f, freshstep.Guard{}, nil))
	if !errors.Is(err, accesstoken.ErrMissingDependency) {
		t.Fatalf("new service: err = %v, want ErrMissingDependency", err)
	}

	if svc != nil {
		t.Fatal("new service: got a service, want nil")
	}
}

// With the step required and missing, requireFresh refuses the create and
// the rotation, writes a denial record for each, and changes nothing.
func TestRequireFreshRefusesWithDenialRecord(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{
		AddSource: false, Level: slog.LevelInfo, ReplaceAttr: nil,
	}))

	svc, err := accesstoken.NewService(policyConfig(&f, *freshGuard(t, freshstep.PolicyFunc(nil)), logger))
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	tok, _ := f.create(t, f.service, "seed")

	_, _, err = svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
	checkFreshErr(t, "create", err, errStale)

	_, _, err = svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
		OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService,
	})
	checkFreshErr(t, "rotate", err, errStale)

	records := strings.Count(logs.String(), `"msg":"access token change denied: a fresh step is required"`)
	if records != 2 {
		t.Fatalf("denial records = %d, want 2; log:\n%s", records, logs.String())
	}

	if !strings.Contains(logs.String(), `"subject":"`+caller().Subject+`"`) ||
		!strings.Contains(logs.String(), errStale.Error()) {
		t.Fatalf("denial record has no subject or reason; log:\n%s", logs.String())
	}

	got, err := svc.Get(t.Context(), tok.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.ReplacedBy != "" {
		t.Fatalf("replaced by = %q, want no rotation", got.ReplacedBy)
	}
}
