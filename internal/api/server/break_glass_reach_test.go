package server_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// grantSiteAdminByPrincipalID grants the site-administrator role on the
// installation to the principal with the ID id.
func grantSiteAdminByPrincipalID(t *testing.T, f accessFixture, id, grantID string) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: grantID,
		Role: access.RoleSiteAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	}

	fence, e := storetest.GrantWrite(t, f.store, identity.ServerObject(), at, "grant:"+grantID)
	if err := f.store.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("seed grant %s: %v", grantID, err)
	}
}

// outsideReachCaller starts a fresh console session of the subject and
// returns a client that sends requests in it.
func outsideReachCaller(t *testing.T, f accessFixture, subject string) passkeyClient {
	t.Helper()

	f.seedSessionAdmin(t, subject)

	return f.freshPasskeyClient(t, subject)
}

// seedRotateOnlyCaller seeds the principal "prn-rot" with a custom role that
// holds only server:manage-principals, so the route and the fresh check pass
// it and the reach checks must refuse it.
func seedRotateOnlyCaller(t *testing.T, f accessFixture) passkeyClient {
	t.Helper()

	var role struct {
		ID string `json:"id"`
	}

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/roles",
		`{"name":"rotate-only","actions":["server:manage-principals"]}`,
		http.StatusCreated,
	}, &role)

	f.seed(t, "rot", "", identity.Object{})
	f.grant(t, "rot", role.ID, identity.ServerObject())

	return outsideReachCaller(t, f, "rot")
}

// wantOutsideReachRefusal fails the test unless the rotation of the account
// by the client answers 403, a denial entry for the manage action exists,
// and the old passkey is the only passkey left.
func wantOutsideReachRefusal(
	t *testing.T, f accessFixture, client passkeyClient, account breakGlassAccountDoc, oldPasskey string,
) {
	t.Helper()

	body := []byte(`{"oldPasskey":"` + oldPasskey + `","name":"next-key",` +
		`"credential":"AgUE","publicKey":"AgUE","transports":["usb"]}`)
	rec := client.do(http.MethodPost, breakGlassPath+"/"+account.ID+"/rotate", body)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("rotation outside the reach = %d %s, want 403", rec.Code, rec.Body.String())
	}

	tokenDenied := len(floorEntries(t, f.store, mfasvc.ActionRotateRefused, audit.OutcomeDenied,
		mfasvc.Object(oldPasskey)))
	accountDenied := len(floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
		principal.Ref(account.ID)))

	if tokenDenied+accountDenied < 1 {
		t.Fatalf("denied entries = 0, want the denial of the rotation: token object %d, account object %d",
			tokenDenied, accountDenied)
	}

	// The refusal kept the passkey of the account.
	accounts := breakGlassAccounts(t, f)
	if len(accounts) != 1 || len(accounts[0].Passkeys) != 1 ||
		accounts[0].Passkeys[0].ID != oldPasskey {
		t.Fatalf("accounts = %+v, want the old passkey %s only", accounts, oldPasskey)
	}
}

// breakGlassReachCase is one hostile caller of the rotation.
type breakGlassReachCase struct {
	name   string
	caller func(t *testing.T, f accessFixture) passkeyClient
}

// breakGlassReachCases returns the hostile callers: an account
// administrator, and a caller whose custom role holds only
// server:manage-principals. Both pass the route and the fresh check, so the
// reach checks must refuse them.
func breakGlassReachCases() []breakGlassReachCase {
	return []breakGlassReachCase{
		{
			name: "an account administrator",
			caller: func(t *testing.T, f accessFixture) passkeyClient {
				t.Helper()

				return outsideReachCaller(t, f, "ann")
			},
		},
		{
			name: "a custom role with only server:manage-principals",
			caller: func(t *testing.T, f accessFixture) passkeyClient {
				t.Helper()

				return seedRotateOnlyCaller(t, f)
			},
		},
	}
}

// TestBreakGlassRotateRouteRefusesOutsideReach sends the rotation of a
// break-glass account that holds the site-administrator role from callers
// whose own rights do not cover the grants of the account. Each answers 403,
// writes a denial entry and keeps the old passkey. The route layer passes
// all callers to the handler: they hold server:manage-principals. A console
// session with an old sign-in is refused at the fresh step with 403 and the
// catalog code.
func TestBreakGlassRotateRouteRefusesOutsideReach(t *testing.T) {
	t.Parallel()

	for _, c := range breakGlassReachCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")
			account := addBreakGlassAccount(t, f)
			grantSiteAdminByPrincipalID(t, f, account.ID, "grt-bg-site")

			wantOutsideReachRefusal(t, f, c.caller(t, f), account, account.Passkeys[0].ID)
		})
	}

	t.Run("no fresh sign-in", func(t *testing.T) {
		t.Parallel()

		f := newAccessFixture(t)
		f.seedSessionAdmin(t, "admin")
		account := addBreakGlassAccount(t, f)
		grantSiteAdminByPrincipalID(t, f, account.ID, "grt-bg-site")

		stale := f.passkeyClientOf(t, "admin", f.sessions.Now().Add(-48*time.Hour))
		body := []byte(`{"oldPasskey":"` + account.Passkeys[0].ID + `","name":"next-key",` +
			`"credential":"AgUE","publicKey":"AgUE","transports":["usb"]}`)
		rec := stale.do(http.MethodPost, breakGlassPath+"/"+account.ID+"/rotate", body)

		got := decodeErrorBody(t, rec)
		if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode {
			t.Fatalf("stale rotation = %d %s, want 403 with errorCode %s", rec.Code, rec.Body.String(),
				freshErrorCode)
		}
	})
}

// TestBreakGlassRotateHandlerRefusesOutsideReach proves the
// defense-in-depth check of the handler itself. The refusal of the handler
// writes one denied entry on the account object before the passkey service
// runs, so the entry is missing when the check is removed: the service then
// answers its own denial on the passkey object, and this test fails on the
// count of the entries on the account object.
func TestBreakGlassRotateHandlerRefusesOutsideReach(t *testing.T) {
	t.Parallel()

	for _, c := range breakGlassReachCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")
			account := addBreakGlassAccount(t, f)
			grantSiteAdminByPrincipalID(t, f, account.ID, "grt-bg-site")
			client := c.caller(t, f)

			wantOutsideReachRefusal(t, f, client, account, account.Passkeys[0].ID)

			denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
				principal.Ref(account.ID))
			if len(denied) != 1 {
				t.Fatalf("denied entries on the account = %d, want the one entry of the handler",
					len(denied))
			}
		})
	}
}
