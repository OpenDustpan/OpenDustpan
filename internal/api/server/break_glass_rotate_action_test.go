package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// rotateDenyAuthz refuses every action, as a caller with no grant does. Only
// Authorize is called by the refusal this test checks.
type rotateDenyAuthz struct {
	identity.Authorizer
}

func (rotateDenyAuthz) Authorize(_ context.Context, _ identity.Principal, _ identity.Action, _ identity.Object) error {
	return fmt.Errorf("%w: test denial", identity.ErrDenied)
}

// rotateAuditRecorder keeps the entries of the server scope. No other method
// of the store is called by the refusal this test checks.
type rotateAuditRecorder struct {
	audit.ServerStore

	entries []audit.Entry
}

func (a *rotateAuditRecorder) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	a.entries = append(a.entries, e)

	return e, nil
}

const rotateRequestBody = `{"oldPasskey":"pky-old","name":"next-key","credential":"AgUE",` +
	`"publicKey":"AgUE","transports":["usb"]}`

// TestBreakGlassRotateHandlerRefusesWithoutAction calls the handler without
// the route in front of it. The route checks the action too, so the handler
// check is the only layer that this test reaches. Each row answers 403, writes
// one denied entry on the account ID, and never reaches the passkey service.
// The passkey service is nil here: a removed handler check reads the account
// first and fails on the nil store, so the test does not pass by accident.
func TestBreakGlassRotateHandlerRefusesWithoutAction(t *testing.T) {
	t.Parallel()

	const accountID = "prn-bg-one"

	tests := []struct {
		name  string
		authz identity.Authorizer
	}{
		{name: "the authorizer refuses the action", authz: rotateDenyAuthz{Authorizer: nil}},
		{name: "no authorizer is wired", authz: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &rotateAuditRecorder{ServerStore: nil, entries: nil}
			h := apiserver.NewBreakGlassRotateRefusalHandlerForTest(tc.authz, rec,
				time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC))

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/api/dustpan/v1/break-glass/accounts/"+accountID+"/rotate", strings.NewReader(rotateRequestBody))
			r.SetPathValue("id", accountID)
			w := httptest.NewRecorder()

			var caller identity.Principal

			apiserver.ServeBreakGlassRotateForTest(h, w, r, caller)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusForbidden, w.Body.String())
			}

			if len(rec.entries) != 1 {
				t.Fatalf("entries = %d, want one denied entry", len(rec.entries))
			}

			got := rec.entries[0]
			if got.Outcome != audit.OutcomeDenied || got.Object != principal.Ref(accountID) ||
				got.Action != string(identity.ActionServerManagePrincipals) {
				t.Fatalf("entry = outcome %q object %q action %q, want a denied rotation of %q",
					got.Outcome, got.Object, got.Action, principal.Ref(accountID))
			}
		})
	}
}

// TestBreakGlassRotateHandlerRefusesWithoutAccessService rotates a readable
// account with a handler that has no access service. The caller holds the
// action, so only the reach check can refuse: it fails closed. The answer is
// 403, one denied entry is on the account, and the passkey stays.
func TestBreakGlassRotateHandlerRefusesWithoutAccessService(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")
	account := addBreakGlassAccount(t, f)

	h := apiserver.WithoutAccessForTest(f.handler.Handler)
	body := breakGlassBody(t, map[string]any{"oldPasskey": account.Passkeys[0].ID, "name": "next-key"})
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		breakGlassPath+"/"+account.ID+"/rotate", strings.NewReader(string(body)))
	r.SetPathValue("id", account.ID)
	w := httptest.NewRecorder()

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "admin", DisplayName: "admin", Admin: true,
		TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	apiserver.ServeBreakGlassRotateForTest(h, w, r, admin)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}

	denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
		principal.Ref(account.ID))
	if len(denied) != 1 {
		t.Fatalf("denied entries on %s = %+v, want one", principal.Ref(account.ID), denied)
	}

	accounts := breakGlassAccounts(t, f)
	if len(accounts) != 1 || len(accounts[0].Passkeys) != 1 ||
		accounts[0].Passkeys[0].ID != account.Passkeys[0].ID {
		t.Fatalf("accounts = %+v, want the old passkey %s only", accounts, account.Passkeys[0].ID)
	}
}
