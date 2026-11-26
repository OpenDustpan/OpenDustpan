package mfasvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// windowedService returns a service of the fixture that checks the action with
// authz and the sign-in with the window function. A nil window keeps the window
// out, so the service fails closed.
func windowedService(
	t *testing.T, f *fixture, authz identity.Authorizer, window func(context.Context) time.Duration,
) *mfasvc.Service {
	t.Helper()

	svc, err := mfasvc.New(mfasvc.Config{
		Store: f.store, RP: relyingPartyOf(t), Audit: f.store, Now: f.clock.now, Log: nil,
		Authorizer: authz, Guard: nil, FreshWindow: window,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// addRefusedWithCause adds a break-glass person as the caller a, and checks that the
// service refuses with one refusal entry that names the cause, and that no
// person is stored.
func addRefusedWithCause(t *testing.T, f *fixture, svc *mfasvc.Service, a *mfasvc.Actor, cause string) {
	t.Helper()

	got, _, err := svc.AddBreakGlassPerson(t.Context(), a, principal.Input{
		Subject: "corp:new-glass", DisplayName: "", Kind: principal.KindPerson,
	}, newPasskey("added"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("add = %v, want an error that matches identity.ErrDenied", err)
	}

	if got.ID != "" {
		t.Fatalf("add stored the person %q, want no person", got.ID)
	}

	refusals := f.entries(t, mfasvc.ActionAddRefused)
	if len(refusals) != 1 {
		t.Fatalf("add refusal entries = %d, want 1", len(refusals))
	}

	if changes := refusals[0].Changes; len(changes) != 1 || changes[0].New != cause {
		t.Fatalf("refusal changes = %+v, want the cause %q", changes, cause)
	}

	if rotated := f.entries(t, mfasvc.ActionRotateRefused); len(rotated) != 0 {
		t.Fatalf("rotate refusal entries = %d, want 0: the add writes its own action", len(rotated))
	}

	if added := f.entries(t, mfasvc.ActionPasskeyAdded); len(added) != 0 {
		t.Fatalf("passkey added entries = %d, want 0", len(added))
	}
}

// TestBreakGlassAddNeedsActionAndFreshSignIn sends the add straight to the
// service, as a caller that skips the route and the HTTP adapter does. Each
// row is refused by the service alone, with one refusal entry of the add.
func TestBreakGlassAddNeedsActionAndFreshSignIn(t *testing.T) {
	t.Parallel()

	t.Run("a caller with no action", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		authz, _ := accessPortsOf(t, f)
		svc := windowedService(t, f, authz, testFreshWindow)

		addRefusedWithCause(t, f, svc, reachCaller(f, "prn-none", "corp:none"), "the caller does not hold the action")
	})

	t.Run("no authorizer refuses a caller that is not an administrator", func(t *testing.T) {
		t.Parallel()

		f := newBreakGlassFixture(t)
		svc := windowedService(t, f, nil, testFreshWindow)

		addRefusedWithCause(t, f, svc, reachCaller(f, "prn-ann", "corp:ann"), "the caller does not hold the action")
	})

	t.Run("a sign-in older than the window", func(t *testing.T) {
		t.Parallel()

		f, svc, a := siteAdministratorAdd(t, testFreshWindow)
		a.SignedInAt = f.clock.now().Add(-16 * time.Minute)

		addRefusedWithCause(t, f, svc, a, "no fresh sign-in")
	})

	t.Run("a window shorter than the default", func(t *testing.T) {
		t.Parallel()

		f, svc, a := siteAdministratorAdd(t, func(context.Context) time.Duration { return 5 * time.Minute })
		a.SignedInAt = f.clock.now().Add(-10 * time.Minute)

		addRefusedWithCause(t, f, svc, a, "no fresh sign-in")
	})

	t.Run("no window is wired", func(t *testing.T) {
		t.Parallel()

		f, svc, a := siteAdministratorAdd(t, nil)

		addRefusedWithCause(t, f, svc, a, "no fresh sign-in")
	})

	t.Run("a window of 0", func(t *testing.T) {
		t.Parallel()

		f, svc, a := siteAdministratorAdd(t, func(context.Context) time.Duration { return 0 })

		addRefusedWithCause(t, f, svc, a, "no fresh sign-in")
	})

	t.Run("a site administrator with a fresh sign-in adds", func(t *testing.T) {
		t.Parallel()

		_, svc, a := siteAdministratorAdd(t, testFreshWindow)

		got, key, err := svc.AddBreakGlassPerson(t.Context(), a, principal.Input{
			Subject: "corp:new-glass", DisplayName: "", Kind: principal.KindPerson,
		}, newPasskey("added"))
		if err != nil {
			t.Fatalf("add by a site administrator: %v", err)
		}

		if got.ID == "" || key.PersonID != got.ID || !got.BreakGlass {
			t.Fatalf("add = %+v, %+v, want a break-glass person with its passkey", got, key)
		}
	})
}

// TestBreakGlassRotateUsesTheWindowOfTheConfig rotates with a sign-in that is
// inside the default window and outside the window of the Config. Only the
// window of the Config can refuse it.
func TestBreakGlassRotateUsesTheWindowOfTheConfig(t *testing.T) {
	t.Parallel()

	f := newBreakGlassFixture(t)
	authz, _ := accessPortsOf(t, f)
	svc := windowedService(t, f, authz, func(context.Context) time.Duration { return 5 * time.Minute })
	seedCaller(t, f, "prn-site", "corp:site")
	grantInstallationTo(t, f, "prn-site", access.RoleSiteAdministrator, "grt-site")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	a := reachCaller(f, "prn-site", "corp:site")
	a.SignedInAt = f.clock.now().Add(-10 * time.Minute)

	err := svc.RotatePasskey(t.Context(), a, glass, old.ID, newPasskey("new"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate = %v, want an error that matches identity.ErrDenied", err)
	}

	if changes := oneRefusal(t, f).Changes; len(changes) != 1 || changes[0].New != "no fresh sign-in" {
		t.Fatalf("refusal changes = %+v, want the cause of the fresh check", changes)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
}

// siteAdministratorAdd returns a fixture, a service with the window, and the
// actor of a site administrator that signed in at the time of the clock.
func siteAdministratorAdd(
	t *testing.T, window func(context.Context) time.Duration,
) (*fixture, *mfasvc.Service, *mfasvc.Actor) {
	t.Helper()

	f := newBreakGlassFixture(t)
	authz, _ := accessPortsOf(t, f)
	svc := windowedService(t, f, authz, window)
	seedCaller(t, f, "prn-site", "corp:site")
	grantInstallationTo(t, f, "prn-site", access.RoleSiteAdministrator, "grt-site")

	return f, svc, reachCaller(f, "prn-site", "corp:site")
}
