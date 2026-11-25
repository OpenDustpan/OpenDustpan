package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// breakGlassPath is the path of the add and the list of the break-glass
// accounts.
const breakGlassPath = "/api/dustpan/v1/break-glass/accounts"

// breakGlassAddBody is a valid add of a break-glass account with one
// passkey. The credential ID and the public key are three bytes each in
// base64url without padding.
const breakGlassAddBody = `{"subject":"break-glass:recovery","displayName":"Recovery",` +
	`"name":"recovery-key","credential":"AQID","publicKey":"BAUG","transports":["usb"]}`

// breakGlassPasskeyDoc is the part of a passkey that this test reads.
type breakGlassPasskeyDoc struct {
	ID string `json:"id"`
}

// breakGlassAccountDoc is the part of the account answer that this test
// reads.
type breakGlassAccountDoc struct {
	ID       string                 `json:"id"`
	Subject  string                 `json:"subject"`
	Passkeys []breakGlassPasskeyDoc `json:"passkeys"`
}

// TestBreakGlassRouteFreshStep proves that the add and the rotation of a
// break-glass account need a fresh authentication: an access token is
// refused with 403 and mfa.fresh-step-required, a console session with an
// old sign-in is refused with the path of a new sign-in, and each refusal
// changes nothing. A console session with a recent sign-in adds the account
// at zero working accounts and then rotates its passkey.
func TestBreakGlassRouteFreshStep(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")

	old := f.passkeyClientOf(t, "admin", f.sessions.Now().Add(-48*time.Hour))
	account := checkBreakGlassAddFreshStep(t, f, old)
	checkBreakGlassRotateFreshStep(t, f, old, account)
	checkBreakGlassList(t, f)
}

// checkBreakGlassAddFreshStep proves the fresh step of the add and returns
// the new account.
func checkBreakGlassAddFreshStep(t *testing.T, f accessFixture, old passkeyClient) breakGlassAccountDoc {
	t.Helper()

	// The access token carries the actions of the route, so only the fresh
	// step can refuse it.
	rec := f.asToken(t, "admin", http.MethodPost, breakGlassPath, []byte(breakGlassAddBody))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("access token = %d %s, want 403", rec.Code, rec.Body.String())
	}

	rec = old.do(http.MethodPost, breakGlassPath, []byte(breakGlassAddBody))

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
		got.Reauthenticate != "/api/server/sign-in/corp?fresh=1" {
		t.Fatalf("stale add = %d %s, want 403 with errorCode %s and a path of a new sign-in", rec.Code,
			rec.Body.String(), freshErrorCode)
	}

	// The stale add stored nothing: the same subject is free.
	rec = f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, []byte(breakGlassAddBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("fresh add = %d %s, want 201", rec.Code, rec.Body.String())
	}

	var account breakGlassAccountDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode the account %q: %v", rec.Body.String(), err)
	}

	if account.ID == "" || account.Subject != "break-glass:recovery" || len(account.Passkeys) != 1 ||
		account.Passkeys[0].ID == "" {
		t.Fatalf("account = %+v, want the new account with one passkey", account)
	}

	return account
}

// checkBreakGlassRotateFreshStep proves the fresh step of the rotation.
func checkBreakGlassRotateFreshStep(
	t *testing.T, f accessFixture, old passkeyClient, account breakGlassAccountDoc,
) {
	t.Helper()

	rotatePath := breakGlassPath + "/" + account.ID + "/rotate"
	rotateBody := []byte(`{"oldPasskey":"` + account.Passkeys[0].ID + `","name":"next-key",` +
		`"credential":"BwgJ","publicKey":"CgsM","transports":["nfc"]}`)

	rec := f.asToken(t, "admin", http.MethodPost, rotatePath, rotateBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("access token on the rotation = %d %s, want 403", rec.Code, rec.Body.String())
	}

	rec = old.do(http.MethodPost, rotatePath, rotateBody)
	if got := decodeErrorBody(t, rec); rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode {
		t.Fatalf("stale rotation = %d %s, want 403 with errorCode %s", rec.Code, rec.Body.String(), freshErrorCode)
	}

	// The stale rotation kept the old passkey: the same rotation works.
	rec = f.freshPasskeyClient(t, "admin").do(http.MethodPost, rotatePath, rotateBody)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("fresh rotation = %d %s, want 204", rec.Code, rec.Body.String())
	}
}

// checkBreakGlassList proves the live count of the list.
func checkBreakGlassList(t *testing.T, f accessFixture) {
	t.Helper()

	rec := f.freshPasskeyClient(t, "admin").do(http.MethodGet, breakGlassPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", rec.Code, rec.Body.String())
	}

	var list struct {
		Working int64 `json:"working"`
		Missing int64 `json:"missing"`
		Floor   int   `json:"floor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the list %q: %v", rec.Body.String(), err)
	}

	// The new account holds a passkey but no administration grant, so the
	// working count stays at 0 and the list names the two that the floor
	// needs.
	if list.Working != 0 || list.Missing != 2 || list.Floor != 2 {
		t.Fatalf("counts = %+v, want 0 working, 2 missing and the floor 2", list)
	}
}
