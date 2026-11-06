package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

const breakGlassID = "prn-glass"

// errSettingsDown is the error of a reader of the settings that does not
// answer.
var errSettingsDown = errors.New("settings do not answer")

// addBreakGlass stores a break-glass account in the store of f.
func (f sessionsFixture) addBreakGlass(t *testing.T) {
	t.Helper()

	at := f.clock.Now().Add(-time.Hour)
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: breakGlassID, Subject: "break-glass:ana",
		DisplayName: "Ana", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: true,
	}

	e := signInEntry()
	e.At, e.Namespace, e.Kind, e.Outcome, e.Object = at, audit.ServerScope, audit.KindChange,
		audit.OutcomeSuccess, principal.Ref(breakGlassID)

	if err := f.store.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create the break-glass account: %v", err)
	}
}

// startFor starts a session of the person id and returns its secret.
func (f sessionsFixture) startFor(t *testing.T, id string) session.Secret {
	t.Helper()

	in := startInput(f.clock.Now())
	in.PrincipalID = id

	_, secret, err := f.svc.Start(t.Context(), signInEntry(), in)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return secret
}

// useEvery keeps the session of secret in use every 10 minutes for the time
// d, so that the idle limit never ends it, and fails the test if it ends
// before. It returns the error of the last check.
func (f sessionsFixture) useEvery(t *testing.T, secret session.Secret, d time.Duration) error {
	t.Helper()

	var err error

	for elapsed := time.Duration(0); elapsed < d; elapsed += 10 * time.Minute {
		f.clock.Add(min(10*time.Minute, d-elapsed))

		if _, err = f.svc.Authenticate(t.Context(), secret); err != nil {
			return err
		}
	}

	return err
}

// TestBreakGlassSessionEndsAtTheLimit checks that a session of a break-glass
// account ends at the limit, 1 hour by default, even while the account uses
// it, and that a session of another person keeps the policy limits.
func TestBreakGlassSessionEndsAtTheLimit(t *testing.T) {
	t.Parallel()

	f := newSessionsFixture(t)
	f.addBreakGlass(t)

	glass, normal := f.startFor(t, breakGlassID), f.startFor(t, personID)

	// Both sessions are in use every 10 minutes, so the idle limit ends neither.
	for range 5 {
		f.clock.Add(10 * time.Minute)

		for name, secret := range map[string]session.Secret{"break-glass": glass, "normal": normal} {
			if _, err := f.svc.Authenticate(t.Context(), secret); err != nil {
				t.Fatalf("%s session at %v: %v", name, f.clock.Now(), err)
			}
		}
	}

	f.clock.Add(9 * time.Minute)

	if _, err := f.svc.Authenticate(t.Context(), glass); err != nil {
		t.Fatalf("break-glass session at 59 minutes: %v", err)
	}

	f.clock.Add(2 * time.Minute)

	if _, err := f.svc.Authenticate(t.Context(), glass); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("break-glass session at 61 minutes: err = %v, want ErrUnauthenticated", err)
	}

	// The session of a normal person is not changed: its policy limit is 12 hours.
	if _, err := f.svc.Authenticate(t.Context(), normal); err != nil {
		t.Fatalf("normal session at 61 minutes: %v", err)
	}
}

// TestBreakGlassLimitSetting checks that the reader of the setting sets the
// limit, and that a reader that fails or that answers a value out of bounds
// gives the default of 1 hour, never no limit.
func TestBreakGlassLimitSetting(t *testing.T) {
	t.Parallel()

	failure := errSettingsDown

	cases := []struct {
		read      session.BreakGlassLimit
		name      string
		endsAfter time.Duration
	}{
		{
			func(context.Context) (time.Duration, error) { return 30 * time.Minute, nil },
			"shorter setting",
			30 * time.Minute,
		},
		{func(context.Context) (time.Duration, error) { return 3 * time.Hour, nil }, "longer setting", 3 * time.Hour},
		{func(context.Context) (time.Duration, error) { return 0, failure }, "reader fails", time.Hour},
		{func(context.Context) (time.Duration, error) { return 100 * time.Hour, nil }, "out of bounds", time.Hour},
		{func(context.Context) (time.Duration, error) { return 0, nil }, "zero", time.Hour},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newSessionsFixture(t)
			f.addBreakGlass(t)
			f.svc.SetBreakGlassLimit(tc.read)

			secret := f.startFor(t, breakGlassID)

			if err := f.useEvery(t, secret, tc.endsAfter-time.Minute); err != nil {
				t.Fatalf("before the limit: %v", err)
			}

			f.clock.Add(2 * time.Minute)

			if _, err := f.svc.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
				t.Fatalf("after the limit: err = %v, want ErrUnauthenticated", err)
			}
		})
	}
}

// TestParseBreakGlassMaxLength checks the bounds of the setting.
func TestParseBreakGlassMaxLength(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"1h", "5m", "12h", "90m"} {
		if _, err := session.ParseBreakGlassMaxLength(ok); err != nil {
			t.Errorf("ParseBreakGlassMaxLength(%q) = %v, want nil", ok, err)
		}
	}

	for _, bad := range []string{"", "0", "4m59s", "12h1s", "1h0m0.5s", "-1h", "forever", "3600"} {
		if _, err := session.ParseBreakGlassMaxLength(bad); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("ParseBreakGlassMaxLength(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}
