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

// newPolicyService makes a token service over the store of f with the stale
// MFA port and the option opts.
func newPolicyService(t *testing.T, f *fixture, opts ...accesstoken.Option) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil,
		Store:      f.store, Principals: f.store, Freshness: staleMFA{}, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: f.clock.Now, NewID: nil,
	}, opts...)
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
		opts    []accesstoken.Option
		wantErr bool
	}{
		{name: "setting off passes", opts: []accesstoken.Option{accesstoken.WithFreshPolicy(off)}, wantErr: false},
		{name: "setting on refuses", opts: []accesstoken.Option{accesstoken.WithFreshPolicy(on)}, wantErr: true},
		{name: "no policy refuses", opts: nil, wantErr: true},
		{name: "nil policy refuses", opts: []accesstoken.Option{accesstoken.WithFreshPolicy(nil)}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			svc := newPolicyService(t, &f, tc.opts...)

			// Create with the service principal, as an administrator.
			_, _, err := svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
			if got := errors.Is(err, errStale); got != tc.wantErr {
				t.Errorf("create: err = %v, want the freshness error: %v", err, tc.wantErr)
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("create: %v", err)
			}

			tok, _ := f.create(t, f.service, "seed")

			_, _, err = svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
				OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService,
			})
			if got := errors.Is(err, errStale); got != tc.wantErr {
				t.Errorf("rotate: err = %v, want the freshness error: %v", err, tc.wantErr)
			}
		})
	}
}

// A service with no Freshness port keeps asking for no step, whatever the
// policy says.
func TestFreshPolicyWithoutPortAddsNoStep(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	svc, err := accesstoken.NewService(accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil,
		Store:      f.store, Principals: f.store, Freshness: nil, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: f.clock.Now, NewID: nil,
	}, accesstoken.WithFreshPolicy(freshstep.PolicyFunc(func(context.Context) bool { return true })))
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	in := accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"}

	_, _, err = svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
}
