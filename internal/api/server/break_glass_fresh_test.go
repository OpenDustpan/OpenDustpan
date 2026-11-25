package server_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
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

// breakGlassBody returns the JSON body of an add or a rotation with the
// passkey fields. A rotation sets oldPasskey, an add sets subject.
func breakGlassBody(t *testing.T, fields map[string]any) []byte {
	t.Helper()

	body := map[string]any{
		"name": "recovery-key", "credential": "AQID", "publicKey": "BAUG", "transports": []string{"usb"},
	}
	maps.Copy(body, fields)

	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode the body: %v", err)
	}

	return b
}

// longBase64 returns the base64url form without padding of n bytes.
func longBase64(n int) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, n))
}

// oversizedBreakGlassBody returns a valid add or rotation body that white
// space makes one byte longer than the 16 KiB limit of the route. Without the
// limit, the server decodes it and accepts the change.
func oversizedBreakGlassBody(valid []byte) []byte {
	const limit = 16 * 1024

	pad := limit + 1 - len(valid)

	return append([]byte("{"+strings.Repeat(" ", pad)), valid[1:]...)
}

// breakGlassHostileCase is one hostile body of the add or the rotation: the
// value of one field replaces the valid value. An empty field sends the valid
// body over the size limit instead.
type breakGlassHostileCase struct {
	name  string
	field string
	value any
}

// body returns the hostile body of the case over the valid fields.
func (c breakGlassHostileCase) body(t *testing.T, valid map[string]any) []byte {
	t.Helper()

	fields := maps.Clone(valid)
	if c.field != "" {
		fields[c.field] = c.value
	}

	body := breakGlassBody(t, fields)
	if c.field == "" {
		return oversizedBreakGlassBody(body)
	}

	return body
}

// breakGlassHostileCases returns the hostile bodies. Each row names the guard
// of break_glass.go that it targets.
func breakGlassHostileCases() []breakGlassHostileCase {
	return []breakGlassHostileCase{
		// Guard: the size limit of readBreakGlassBody (maxBreakGlassBodyBytes).
		{"body over the size limit", "", nil},
		// Guard: the transport allowlist of checkTransports.
		{"unknown transport", "transports", []string{"carrier-pigeon"}},
		// Guard: the transport allowlist of checkTransports, with a CR LF value.
		{"transport with CR LF", "transports", []string{"usb\r\nx"}},
		// Guard: the duplicate check of checkTransports.
		{"transport named twice", "transports", []string{"usb", "usb"}},
		// Guard: the length limit of decodePasskeyValue for the credential.
		{"credential over the length limit", "credential", longBase64(mfa.MaxCredentialIDBytes + 1)},
		// Guard: the lower length limit of decodePasskeyValue for the credential.
		{"empty credential", "credential", ""},
		// Guard: the strict base64url decode of decodePasskeyValue.
		{"credential with padding", "credential", "AQI="},
		// Guard: the length limit of decodePasskeyValue for the public key.
		{"public key over the length limit", "publicKey", longBase64(mfa.MaxPublicKeyBytes + 1)},
		// Guard: the ValidatePasskeyName call of passkeyOf.
		{"empty passkey name", "name", ""},
		// Guard: the ValidatePasskeyName call of passkeyOf.
		{"passkey name with a slash", "name", "../key"},
		// Guard: the ValidatePasskeyName call of passkeyOf.
		{"passkey name over the length limit", "name", strings.Repeat("k", 101)},
	}
}

// expectBreakGlassRefusal marks the test failed unless rec answers 413 with
// request.body-too-large for the size row, and 400 with mfa.passkey-invalid
// for each other row. It does not stop the test, so the caller still checks
// that the refusal changed nothing.
func expectBreakGlassRefusal(t *testing.T, rec *httptest.ResponseRecorder, c breakGlassHostileCase) {
	t.Helper()

	status, code := http.StatusBadRequest, errcode.MFAKeyInvalid
	if c.field == "" {
		status, code = http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge
	}

	if got := decodeErrorBody(t, rec); rec.Code != status || got.ErrorCode != code {
		t.Errorf("answer = %d %s, want %d with errorCode %s", rec.Code, rec.Body.String(), status, code)
	}
}

// breakGlassAccounts returns the accounts of the list route.
func breakGlassAccounts(t *testing.T, f accessFixture) []breakGlassAccountDoc {
	t.Helper()

	rec := f.freshPasskeyClient(t, "admin").do(http.MethodGet, breakGlassPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", rec.Code, rec.Body.String())
	}

	var list struct {
		Accounts []breakGlassAccountDoc `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the list %q: %v", rec.Body.String(), err)
	}

	return list.Accounts
}

// TestBreakGlassAddRefusesHostileBody sends each hostile body to the add.
// The handler checks the passkey before it stores the account, so a refusal
// leaves no account. The passkey service checks the passkey too, but only
// after the account is stored: a row whose guard is removed leaves an account
// on the list, and fails.
func TestBreakGlassAddRefusesHostileBody(t *testing.T) {
	t.Parallel()

	for _, c := range breakGlassHostileCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")

			body := c.body(t, map[string]any{"subject": "break-glass:recovery", "displayName": "Recovery"})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, body)
			expectBreakGlassRefusal(t, rec, c)

			if accounts := breakGlassAccounts(t, f); len(accounts) != 0 {
				t.Fatalf("accounts = %+v, want none after a refused add", accounts)
			}
		})
	}
}

// TestBreakGlassRotateRefusesHostileBody sends each hostile body to the
// rotation of a real break-glass account. Each refusal keeps the old passkey.
func TestBreakGlassRotateRefusesHostileBody(t *testing.T) {
	t.Parallel()

	for _, c := range breakGlassHostileCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")
			account := addBreakGlassAccount(t, f)

			body := c.body(t, map[string]any{"oldPasskey": account.Passkeys[0].ID, "name": "next-key"})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost,
				breakGlassPath+"/"+account.ID+"/rotate", body)
			expectBreakGlassRefusal(t, rec, c)

			accounts := breakGlassAccounts(t, f)
			if len(accounts) != 1 || len(accounts[0].Passkeys) != 1 ||
				accounts[0].Passkeys[0].ID != account.Passkeys[0].ID {
				t.Fatalf("accounts = %+v, want the old passkey %s only", accounts, account.Passkeys[0].ID)
			}
		})
	}
}

// addBreakGlassAccount adds a break-glass account with one passkey and
// returns it.
func addBreakGlassAccount(t *testing.T, f accessFixture) breakGlassAccountDoc {
	t.Helper()

	rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, []byte(breakGlassAddBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s, want 201", rec.Code, rec.Body.String())
	}

	var account breakGlassAccountDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode the account %q: %v", rec.Body.String(), err)
	}

	if len(account.Passkeys) != 1 {
		t.Fatalf("account = %+v, want one passkey", account)
	}

	return account
}

// TestBreakGlassRotateRefusesHostilePathID sends a hostile ID in the path of
// the rotation. The handler checks the ID at the boundary, before it asks the
// principal service. Each refusal answers 404 with break-glass.not-found and
// writes one denied entry on the "break-glass" object. The service answers
// 404 for such an ID too, but it names the hostile ID as the object of its
// entry: a row whose guard is removed misses the entry, and fails.
func TestBreakGlassRotateRefusesHostilePathID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
	}{
		{"ID over the length limit", "prn-" + strings.Repeat("a", 200)},
		{"ID with CR LF", "prn-x\r\nX-Injected: 1"},
		// The mux cleans a ".." segment with a redirect before the handler
		// runs, so a dot row starts with a dot instead.
		{"ID that starts with a dot", ".prn"},
		{"ID with an encoded slash", "prn-a/../prn-b"},
		{"ID with a space", "prn- x"},
		{"ID with uppercase and unicode", "PRN-éÉ"},
		{"ID with an SQL quote", "prn-x' OR '1'='1"},
		{"ID with a percent sign", "prn-%41"},
		{"ID that starts with a dash", "-prn"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")
			account := addBreakGlassAccount(t, f)

			body := breakGlassBody(t, map[string]any{"oldPasskey": account.Passkeys[0].ID, "name": "next-key"})
			path := breakGlassPath + "/" + url.PathEscape(c.id) + "/rotate"
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, path, body)

			if got := decodeErrorBody(t, rec); rec.Code != http.StatusNotFound ||
				got.ErrorCode != errcode.BreakGlassNotFound {
				t.Fatalf("rotate %q = %d %s, want 404 with errorCode %s", c.id, rec.Code, rec.Body.String(),
					errcode.BreakGlassNotFound)
			}

			denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
				"break-glass")
			if len(denied) != 1 || denied[0].Reason == "" {
				t.Fatalf("denied entries on break-glass = %+v, want one with a reason", denied)
			}

			// The refusal kept the passkey of the real account.
			accounts := breakGlassAccounts(t, f)
			if len(accounts) != 1 || len(accounts[0].Passkeys) != 1 ||
				accounts[0].Passkeys[0].ID != account.Passkeys[0].ID {
				t.Fatalf("accounts = %+v, want the old passkey %s only", accounts, account.Passkeys[0].ID)
			}
		})
	}
}

// TestBreakGlassRotateRefusesOtherRecords sends a rotation to an ID that is
// not a break-glass account of the installation. Each answers 404 with
// break-glass.not-found and writes one denied entry on the record. The
// rotation of the passkey store also answers 404 for a passkey that the
// record does not hold, but it writes no denied entry: a row whose guard is
// removed misses the entry, and fails.
func TestBreakGlassRotateRefusesOtherRecords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
	}{
		// Guard: the principal.ErrNotFound branch of breakGlassAccount.
		{"unknown ID", "prn-nobody"},
		// Guard: the BreakGlass flag check of breakGlassAccount. The store
		// refuses a break-glass record that is not a person of the
		// installation, so no row here can reach the kind and the namespace
		// checks of the same line.
		{"person without the break-glass flag", "prn-bob"},
		// Guard: the BreakGlass flag check of breakGlassAccount, on the
		// caller's own record.
		{"the caller without the break-glass flag", "prn-admin"},
		// Guard: the principal.ErrNotFound branch of breakGlassAccount, with
		// an ID of another kind of record.
		{"ID of a passkey", "pky-nothing"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")
			account := addBreakGlassAccount(t, f)
			id := c.id

			body := breakGlassBody(t, map[string]any{"oldPasskey": account.Passkeys[0].ID, "name": "next-key"})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath+"/"+id+"/rotate", body)

			if got := decodeErrorBody(t, rec); rec.Code != http.StatusNotFound ||
				got.ErrorCode != errcode.BreakGlassNotFound {
				t.Fatalf("rotate %s = %d %s, want 404 with errorCode %s", id, rec.Code, rec.Body.String(),
					errcode.BreakGlassNotFound)
			}

			denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
				principal.Ref(id))
			if len(denied) != 1 || denied[0].Reason == "" {
				t.Fatalf("denied entries on %s = %+v, want one with a reason", principal.Ref(id), denied)
			}

			// The refusal kept the passkey of the real account.
			accounts := breakGlassAccounts(t, f)
			if len(accounts) != 1 || len(accounts[0].Passkeys) != 1 ||
				accounts[0].Passkeys[0].ID != account.Passkeys[0].ID {
				t.Fatalf("accounts = %+v, want the old passkey %s only", accounts, account.Passkeys[0].ID)
			}
		})
	}
}
