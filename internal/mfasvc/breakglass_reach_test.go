package mfasvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// relyingPartyOf returns the relying party of the tests.
func relyingPartyOf(t *testing.T) *gowebauthn.RelyingParty {
	t.Helper()

	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatalf("relying party: %v", err)
	}

	return rp
}

// reachCaller returns the actor of a console session of the caller, signed in
// at the time of the fixture clock. Admin is false: the caller holds its
// actions through grants, as a console session does.
func reachCaller(f *fixture, principalID, subject string) *mfasvc.Actor {
	return &mfasvc.Actor{
		Entry:       base(f.clock.now(), "x"),
		PrincipalID: principalID,
		SessionID:   "ses-" + principalID,
		Caller: identity.Principal{
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                subject,
			DisplayName:            subject,
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			Kind:                   identity.KindPerson,
			Credential:             identity.CredentialSession,
			JobID:                  "",
		},
		SignedInAt: f.clock.now(),
		Fresh:      false,
	}
}

// guardedService returns a service of the fixture that checks the action
// with authz and the reach of the account with guard. A nil port keeps the
// port out, so the service fails closed for a caller that is not an
// administrator.
func guardedService(t *testing.T, f *fixture, authz identity.Authorizer, guard principal.Guard) *mfasvc.Service {
	t.Helper()

	return guardedServiceOn(t, f, f.store, authz, guard)
}

// guardedServiceOn is guardedService with the store of the service given, so
// a test can wrap the store of the fixture.
func guardedServiceOn(
	t *testing.T, f *fixture, store mfa.PasskeyStore, authz identity.Authorizer, guard principal.Guard,
) *mfasvc.Service {
	t.Helper()

	rp := relyingPartyOf(t)

	svc, err := mfasvc.New(mfasvc.Config{
		Store: store, RP: rp, Audit: f.store, Now: f.clock.now, Log: nil,
		Authorizer: authz, Guard: guard, FreshWindow: testFreshWindow,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// accessPortsOf returns the authorizer and the access service of the grants
// of the store of f. The access service is the Guard that the disable of a
// principal uses, so the rotation checks the same grants with it.
func accessPortsOf(t *testing.T, f *fixture) (*access.Authorizer, *access.Service) {
	t.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: f.store, Collections: nil, Units: nil, Principals: f.store,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	grants, err := access.NewService(access.ServiceConfig{
		Store: f.store, Directory: f.store, Namespaces: f.store, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"}, Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	return authz, grants
}

// seedCaller stores the person principal with the subject.
func seedCaller(t *testing.T, f *fixture, id, subject string) {
	t.Helper()

	now := f.clock.now()
	p := principal.Principal{
		CreatedAt: now, UpdatedAt: now, DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: subject, Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	if err := f.store.CreatePrincipal(t.Context(), p, base(now, principal.Ref(id))); err != nil {
		t.Fatalf("create principal %s: %v", id, err)
	}
}

// grantInstallationTo grants role on the installation to the principal id,
// directly in the store.
func grantInstallationTo(t *testing.T, f *fixture, id, role, grantID string) {
	t.Helper()

	at := f.clock.now()
	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: grantID, Role: role,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	}

	fence, e := storetest.GrantWrite(t, f.store, identity.ServerObject(), at, "grant:"+grantID)
	if err := f.store.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("seed grant %s: %v", grantID, err)
	}
}

// oneRefusal returns the single refusal entry of a rotation, and fails the
// test when there is not exactly one.
func oneRefusal(t *testing.T, f *fixture) audit.Entry {
	t.Helper()

	refusals := f.entries(t, mfasvc.ActionRotateRefused)
	if len(refusals) != 1 {
		t.Fatalf("refusal entries = %d, want 1", len(refusals))
	}

	return refusals[0]
}

// reachRow is one caller of the reach table. The cause is the reason that the
// refusal entry names.
type reachRow struct {
	name   string
	caller func(t *testing.T, f *fixture) *mfasvc.Actor
	cause  string
}

// reachRows is the hostile table. Each row rotates the passkey of the
// break-glass account glass, which holds the site-administrator role on the
// installation.
func reachRows() []reachRow {
	return []reachRow{
		{
			name: "an account administrator rotates a site administrator",
			caller: func(t *testing.T, f *fixture) *mfasvc.Actor {
				t.Helper()

				seedCaller(t, f, "prn-ann", "corp:ann")
				grantInstallationTo(t, f, "prn-ann", access.RoleAccountAdministrator, "grt-ann")

				return reachCaller(f, "prn-ann", "corp:ann")
			},
			cause: "the caller does not hold the rights of the account",
		},
		{
			name: "a custom role with only server:manage-principals rotates a site administrator",
			caller: func(t *testing.T, f *fixture) *mfasvc.Actor {
				t.Helper()

				_, grants := accessPortsOf(t, f)
				role, err := grants.CreateRole(t.Context(), base(f.clock.now(), "role:rotate-only"),
					identity.Principal{
						TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin",
						DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
						Kind: "", Credential: "", JobID: "",
					}, access.RoleInput{
						Name:        "rotate-only",
						Description: "holds the action of the break-glass routes and nothing else",
						Actions:     []identity.Action{identity.ActionServerManagePrincipals},
					})
				if err != nil {
					t.Fatalf("create the custom role: %v", err)
				}

				seedCaller(t, f, "prn-rot", "corp:rot")
				grantInstallationTo(t, f, "prn-rot", role.ID, "grt-rot")

				return reachCaller(f, "prn-rot", "corp:rot")
			},
			cause: "the caller does not hold the rights of the account",
		},
	}
}

// TestBreakGlassRotateNeedsTheReachOfTheAccount proves that the rotation of
// the passkey of a break-glass account needs a caller whose rights cover the
// grants of the account, the action of the route, and a fresh sign-in. An
// account administrator and a caller whose custom role holds only
// server:manage-principals cannot rotate the passkey of an account that the
// site-administrator role makes a working path: such a rotation would let the
// caller sign in as the account with every action of the account. Each
// refusal is one denial entry, and the old passkey stays.
func TestBreakGlassRotateNeedsTheReachOfTheAccount(t *testing.T) {
	t.Parallel()

	for _, row := range reachRows() {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			rotateRefusedByReach(t, row)
		})
	}

	t.Run("a caller with no action", func(t *testing.T) {
		t.Parallel()

		rotateWithNoAction(t)
	})

	t.Run("no fresh sign-in", func(t *testing.T) {
		t.Parallel()

		rotateWithStaleSignIn(t)
	})

	t.Run("no authorizer refuses a caller that is not an administrator", func(t *testing.T) {
		t.Parallel()

		rotateWithNoAuthorizer(t)
	})

	t.Run("no guard refuses a caller that is not an administrator", func(t *testing.T) {
		t.Parallel()

		rotateWithNoGuard(t)
	})

	t.Run("a site administrator rotates a site administrator", func(t *testing.T) {
		t.Parallel()

		rotateBySiteAdministrator(t)
	})

	t.Run("an unreadable account refuses the rotation", func(t *testing.T) {
		t.Parallel()

		rotateWithUnreadableAccount(t)
	})
}

// rotateRefusedByReach rotates the account with the caller of the row, and
// checks the refusal of the reach check: the denial, one refusal entry with
// the cause of the row, and the old passkey kept.
func rotateRefusedByReach(t *testing.T, row reachRow) {
	t.Helper()

	f := newBreakGlassFixture(t)
	authz, grants := accessPortsOf(t, f)
	svc := guardedService(t, f, authz, grants)

	// The target account holds the site-administrator role, so its grants give
	// actions that neither caller holds.
	grantInstallationTo(t, f, glass, access.RoleSiteAdministrator, "grt-glass")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	a := row.caller(t, f)

	err := svc.RotatePasskey(t.Context(), a, glass, old.ID, newPasskey("new"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate = %v, want an error that matches identity.ErrDenied", err)
	}

	refusal := oneRefusal(t, f)

	if refusal.Outcome != audit.OutcomeDenied {
		t.Fatalf("refusal outcome = %s, want %s", refusal.Outcome, audit.OutcomeDenied)
	}

	if changes := refusal.Changes; len(changes) != 1 || changes[0].New != row.cause {
		t.Fatalf("refusal changes = %+v, want the cause %q", changes, row.cause)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
}

// rotateWithNoAction checks that a caller with no action on any object is
// refused by the check of the action, before the reach check runs.
func rotateWithNoAction(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	authz, grants := accessPortsOf(t, f)
	svc := guardedService(t, f, authz, grants)
	grantInstallationTo(t, f, glass, access.RoleSiteAdministrator, "grt-glass")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	// The caller holds no action on any object, so the check of the action
	// refuses before the reach check can. The row pins the cause, so it fails
	// when the check of the action is removed: the reach check then answers its
	// own cause.
	err := svc.RotatePasskey(t.Context(), reachCaller(f, "prn-none", "corp:none"), glass, old.ID,
		newPasskey("new"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate = %v, want an error that matches identity.ErrDenied", err)
	}

	refusal := oneRefusal(t, f)

	if changes := refusal.Changes; len(changes) != 1 || changes[0].New != "the caller does not hold the action" {
		t.Fatalf("refusal changes = %+v, want the cause of the action check", changes)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
}

// rotateWithStaleSignIn checks that a caller that holds the rights of the
// account is still refused when the sign-in of its session is too old.
func rotateWithStaleSignIn(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	authz, grants := accessPortsOf(t, f)
	svc := guardedService(t, f, authz, grants)
	grantInstallationTo(t, f, glass, access.RoleSiteAdministrator, "grt-glass")
	seedCaller(t, f, "prn-site", "corp:site")
	grantInstallationTo(t, f, "prn-site", access.RoleSiteAdministrator, "grt-site")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	// The caller holds the rights of the account, but the sign-in of its
	// console session is older than the window, so the rotation is refused.
	a := reachCaller(f, "prn-site", "corp:site")
	a.SignedInAt = f.clock.now().Add(-16 * time.Minute)

	err := svc.RotatePasskey(t.Context(), a, glass, old.ID, newPasskey("new"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate = %v, want an error that matches identity.ErrDenied", err)
	}

	refusal := oneRefusal(t, f)

	if changes := refusal.Changes; len(changes) != 1 || changes[0].New != "no fresh sign-in" {
		t.Fatalf("refusal changes = %+v, want the cause of the fresh check", changes)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
}

// rotateWithNoAuthorizer checks the fail-closed path of a service with no
// Authorizer: a caller that is not an administrator is refused.
func rotateWithNoAuthorizer(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	svc := guardedService(t, f, nil, nil)
	seedCaller(t, f, "prn-ann", "corp:ann")
	grantInstallationTo(t, f, "prn-ann", access.RoleAccountAdministrator, "grt-ann")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	err := svc.RotatePasskey(t.Context(), reachCaller(f, "prn-ann", "corp:ann"), glass, old.ID,
		newPasskey("new"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate with no authorizer = %v, want identity.ErrDenied", err)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
}

// rotateWithNoGuard checks the fail-closed path of a service with no Guard: a
// caller that is not an administrator is refused.
func rotateWithNoGuard(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	authz, _ := accessPortsOf(t, f)
	svc := guardedService(t, f, authz, nil)
	seedCaller(t, f, "prn-ann", "corp:ann")
	grantInstallationTo(t, f, "prn-ann", access.RoleAccountAdministrator, "grt-ann")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	err := svc.RotatePasskey(t.Context(), reachCaller(f, "prn-ann", "corp:ann"), glass, old.ID,
		newPasskey("new"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("rotate with no guard = %v, want identity.ErrDenied", err)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
}

// rotateBySiteAdministrator is the positive control: a caller whose reach
// covers the account can rotate. Without it, a seed that refuses every caller
// proves nothing.
func rotateBySiteAdministrator(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	authz, grants := accessPortsOf(t, f)
	svc := guardedService(t, f, authz, grants)
	grantInstallationTo(t, f, glass, access.RoleSiteAdministrator, "grt-glass")
	seedCaller(t, f, "prn-site", "corp:site")
	grantInstallationTo(t, f, "prn-site", access.RoleSiteAdministrator, "grt-site")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	err := svc.RotatePasskey(t.Context(), reachCaller(f, "prn-site", "corp:site"), glass,
		old.ID, newPasskey("new"))
	if err != nil {
		t.Fatalf("rotate by a site administrator: %v", err)
	}

	keys, err := f.store.Passkeys(t.Context(), glass)
	if err != nil || len(keys) != 1 || keys[0].ID == old.ID {
		t.Fatalf("passkeys after the rotation = %+v, %v; want the new one", keys, err)
	}
}

// rotateWithUnreadableAccount checks that a store which cannot read the
// account refuses the rotation: the caller holds every right, but the service
// cannot check the account, so it fails closed with the failure outcome.
func rotateWithUnreadableAccount(t *testing.T) {
	t.Helper()

	f := newBreakGlassFixture(t)
	authz, grants := accessPortsOf(t, f)
	svc := guardedServiceOn(t, f, failingTargetStore{PasskeyStore: f.store}, authz, grants)
	grantInstallationTo(t, f, glass, access.RoleSiteAdministrator, "grt-glass")
	seedCaller(t, f, "prn-site", "corp:site")
	grantInstallationTo(t, f, "prn-site", access.RoleSiteAdministrator, "grt-site")

	old := storedPasskey(t, f, storedPasskeyInput{
		personID: glass, id: "pky-old", body: "old-material", name: "old-key",
	})

	err := svc.RotatePasskey(t.Context(), reachCaller(f, "prn-site", "corp:site"), glass,
		old.ID, newPasskey("new"))
	if !errors.Is(err, mfa.ErrUnavailable) {
		t.Fatalf("rotate with an unreadable account = %v, want mfa.ErrUnavailable", err)
	}

	refusal := oneRefusal(t, f)

	if refusal.Outcome != audit.OutcomeFailure {
		t.Fatalf("refusal outcome = %s, want %s", refusal.Outcome, audit.OutcomeFailure)
	}

	if changes := refusal.Changes; len(changes) != 1 || changes[0].New != "the principal cannot be read" {
		t.Fatalf("refusal changes = %+v, want the cause of the read", changes)
	}

	wantGlassUnchanged(t, f, old, mfasvc.ActionPasskeyRotated)
}

// errTargetUnreadable is the read error of failingTargetStore. It is not the
// ErrNotFound of an unknown record.
var errTargetUnreadable = errors.New("read failed")

// failingTargetStore refuses every read of a principal.
type failingTargetStore struct {
	mfa.PasskeyStore
}

func (s failingTargetStore) GetPrincipal(_ context.Context, _ string) (principal.Principal, error) {
	return principal.Principal{}, errTargetUnreadable
}
