package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/session"
)

// TestTokenFreshCheck sends hostile contexts to the check of the fresh step
// guard of the access token service: each one is refused with a denial.
func TestTokenFreshCheck(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)
	check := tokenFreshCheck(nil, func() time.Time { return now }, slog.New(slog.DiscardHandler))

	signedIn := func(at time.Time) func(context.Context) context.Context {
		return func(ctx context.Context) context.Context {
			return sessionauth.WithSignIn(sessionauth.WithSession(ctx), sessionauth.SignIn{At: at, Provider: "test"})
		}
	}

	cases := []struct {
		with   func(context.Context) context.Context
		name   string
		refuse bool
	}{
		{name: "no session", with: func(ctx context.Context) context.Context { return ctx }, refuse: true},
		{name: "session with no sign-in", with: sessionauth.WithSession, refuse: true},
		{
			name: "sign-in with no session", refuse: true,
			with: func(ctx context.Context) context.Context {
				return sessionauth.WithSignIn(ctx, sessionauth.SignIn{At: now, Provider: "test"})
			},
		},
		{name: "old sign-in", with: signedIn(now.Add(-session.DefaultTokenFreshness - time.Second)), refuse: true},
		{name: "sign-in after now", with: signedIn(now.Add(time.Minute)), refuse: true},
		{name: "fresh sign-in", with: signedIn(now.Add(-time.Second)), refuse: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := check(tc.with(t.Context()), "alice")
			if !tc.refuse {
				if err != nil {
					t.Fatalf("check: %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("check: err = %v, want identity.ErrDenied", err)
			}
		})
	}
}
