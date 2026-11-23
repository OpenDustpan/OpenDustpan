package accesstoken_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/freshstep"
)

// newPolicyService makes a token service over the store of f with the fresh
// step guard g.
func newPolicyService(t *testing.T, f *fixture, g *freshstep.Guard) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil,
		Store:      f.store, Principals: f.store, Freshness: g, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	return svc
}

// zeroGuard returns a guard that NewGuard did not make.
func zeroGuard(*testing.T) *freshstep.Guard {
	return &freshstep.Guard{}
}

func TestFreshPolicyGatesTheStep(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	cases := []struct {
		name    string
		guard   func(t *testing.T) *freshstep.Guard
		wantErr error
	}{
		{
			name:    "setting off passes",
			guard:   func(t *testing.T) *freshstep.Guard { t.Helper(); return freshGuard(t, off) },
			wantErr: nil,
		},
		{
			name:    "setting on refuses",
			guard:   func(t *testing.T) *freshstep.Guard { t.Helper(); return freshGuard(t, on) },
			wantErr: errStale,
		},
		{
			name:    "no policy refuses",
			guard:   func(t *testing.T) *freshstep.Guard { t.Helper(); return freshGuard(t, freshstep.PolicyFunc(nil)) },
			wantErr: errStale,
		},
		{
			// NewGuard refuses a nil policy (see TestFreshGuardRefusesNilPolicy).
			// A guard that NewGuard did not make fails closed.
			name:    "nil policy refuses",
			guard:   zeroGuard,
			wantErr: freshstep.ErrNilCheck,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			svc := newPolicyService(t, &f, tc.guard(t))

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

// A service with no guard keeps asking for no step at this layer.
func TestFreshPolicyWithoutGuardAddsNoStep(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	svc := newPolicyService(t, &f, nil)

	in := accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"}

	_, _, err := svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
}
