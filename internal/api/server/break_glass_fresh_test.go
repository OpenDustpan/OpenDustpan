package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/store/sqlite"
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
	failures := breakGlassFailureCount(t, f)

	rec := f.asToken(t, "admin", http.MethodPost, breakGlassPath, []byte(breakGlassAddBody))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("access token = %d %s, want 403", rec.Code, rec.Body.String())
	}

	failures = expectOneMoreBreakGlassFailure(t, f, failures, "access token add")

	rec = old.do(http.MethodPost, breakGlassPath, []byte(breakGlassAddBody))

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
		got.Reauthenticate != "/api/server/sign-in/corp?fresh=1" {
		t.Fatalf("stale add = %d %s, want 403 with errorCode %s and a path of a new sign-in", rec.Code,
			rec.Body.String(), freshErrorCode)
	}

	expectOneMoreBreakGlassFailure(t, f, failures, "stale add")

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

	failures := breakGlassFailureCount(t, f)

	rec := f.asToken(t, "admin", http.MethodPost, rotatePath, rotateBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("access token on the rotation = %d %s, want 403", rec.Code, rec.Body.String())
	}

	failures = expectOneMoreBreakGlassFailure(t, f, failures, "access token rotation")

	rec = old.do(http.MethodPost, rotatePath, rotateBody)
	if got := decodeErrorBody(t, rec); rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode {
		t.Fatalf("stale rotation = %d %s, want 403 with errorCode %s", rec.Code, rec.Body.String(), freshErrorCode)
	}

	expectOneMoreBreakGlassFailure(t, f, failures, "stale rotation")

	// The stale rotation kept the old passkey: the same rotation works.
	rec = f.freshPasskeyClient(t, "admin").do(http.MethodPost, rotatePath, rotateBody)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("fresh rotation = %d %s, want 204", rec.Code, rec.Body.String())
	}
}

// breakGlassFailureCount returns the number of failure entries on the
// server object for the manage action. The break-glass routes resolve the
// server object, and the refusal writes its entry on that object.
func breakGlassFailureCount(t *testing.T, f accessFixture) int {
	t.Helper()

	return len(floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeFailure,
		identity.ServerObject().String()))
}

// expectOneMoreBreakGlassFailure fails the test unless the refusal named
// what wrote exactly one failure entry since before, and returns the new
// count.
func expectOneMoreBreakGlassFailure(t *testing.T, f accessFixture, before int, what string) int {
	t.Helper()

	after := breakGlassFailureCount(t, f)
	if after != before+1 {
		t.Fatalf("failure entries on break-glass after the %s = %d, want %d", what, after, before+1)
	}

	return after
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

// TestBreakGlassNoFactorNamesEnrollmentPath sends a console session with an old
// sign-in and no enrolled passkey to the add and the rotation. Each refusal
// answers 403 with mfa.fresh-step-required, and its message names the path that
// enrolls a passkey. The admin of the fixture holds no passkey. Removing the
// enrollment text from the refusal of break_glass.go fails this test.
func TestBreakGlassNoFactorNamesEnrollmentPath(t *testing.T) {
	t.Parallel()

	const enrollPath = "/api/dustpan/v1/me/passkeys/register/begin"

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")

	stale := f.passkeyClientOf(t, "admin", f.sessions.Now().Add(-48*time.Hour))

	cases := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{"add", http.MethodPost, breakGlassPath, []byte(breakGlassAddBody)},
		{
			"rotate", http.MethodPost, breakGlassPath + "/prn-none/rotate",
			breakGlassBody(t, map[string]any{"oldPasskey": "pky-none", "name": "next-key"}),
		},
	}

	for _, c := range cases {
		rec := stale.do(c.method, c.path, c.body)

		got := decodeErrorBody(t, rec)
		if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode {
			t.Fatalf("%s = %d %s, want 403 with errorCode %s", c.name, rec.Code, rec.Body.String(), freshErrorCode)
		}

		if !strings.Contains(got.Message, enrollPath) {
			t.Fatalf("%s message = %q, want the enrollment path %s", c.name, got.Message, enrollPath)
		}
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
// for each other row. It also asserts one denied audit entry on the
// "break-glass" object for the body refusal. It does not stop the test, so
// the caller still checks that the refusal changed nothing.
func expectBreakGlassRefusal(
	t *testing.T, rec *httptest.ResponseRecorder, c breakGlassHostileCase, st *sqlite.Store,
) {
	t.Helper()

	status, code := http.StatusBadRequest, errcode.MFAKeyInvalid
	if c.field == "" {
		status, code = http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge
	}

	if got := decodeErrorBody(t, rec); rec.Code != status || got.ErrorCode != code {
		t.Errorf("answer = %d %s, want %d with errorCode %s", rec.Code, rec.Body.String(), status, code)
	}

	denied := floorEntries(t, st, identity.ActionServerManagePrincipals, audit.OutcomeDenied, "break-glass")
	if len(denied) == 0 || denied[len(denied)-1].Reason == "" {
		t.Errorf("denied entries on break-glass = %+v, want at least one with a reason", denied)
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
// leaves no account. The passkey service validates the passkey before the
// store too, so this test catches a removed handler guard through the denied
// entry on the "break-glass" object: the service writes a failure entry, not
// a denied one.
func TestBreakGlassAddRefusesHostileBody(t *testing.T) {
	t.Parallel()

	for _, c := range breakGlassHostileCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")

			body := c.body(t, map[string]any{"subject": "break-glass:recovery", "displayName": "Recovery"})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, body)
			expectBreakGlassRefusal(t, rec, c, f.store)

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
			expectBreakGlassRefusal(t, rec, c, f.store)

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

// TestBreakGlassRotateRefusesHostileOldPasskey sends a hostile old passkey ID
// in the body of the rotation of a real break-glass account. The handler
// checks the ID at the boundary, before it asks a service. Each refusal
// answers 404 with break-glass.not-found, writes one denied entry on the
// "break-glass" object and keeps the old passkey. The passkey service checks
// the ID too, but it writes no denied entry on that object: a row whose guard
// is removed misses the entry, and fails.
func TestBreakGlassRotateRefusesHostileOldPasskey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
	}{
		{"empty ID", ""},
		{"ID over the length limit", "pky-" + strings.Repeat("a", mfa.MaxIDLength)},
		{"ID with CR LF", "pky-x\r\nX-Injected: 1"},
		{"ID with a path", "../pky-x"},
		{"ID with a space", "pky- x"},
		{"ID with uppercase and unicode", "PKY-éÉ"},
		{"ID with an SQL quote", "pky-x' OR '1'='1"},
		{"ID with a NUL byte", "pky-x\x00"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")
			account := addBreakGlassAccount(t, f)

			body := breakGlassBody(t, map[string]any{"oldPasskey": c.id, "name": "next-key"})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath+"/"+account.ID+"/rotate", body)

			if got := decodeErrorBody(t, rec); rec.Code != http.StatusNotFound ||
				got.ErrorCode != errcode.BreakGlassNotFound {
				t.Fatalf("rotate with old passkey %q = %d %s, want 404 with errorCode %s", c.id, rec.Code,
					rec.Body.String(), errcode.BreakGlassNotFound)
			}

			denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
				"break-glass")
			if len(denied) != 1 || denied[0].Reason == "" {
				t.Fatalf("denied entries on break-glass = %+v, want one with a reason", denied)
			}

			accounts := breakGlassAccounts(t, f)
			if len(accounts) != 1 || len(accounts[0].Passkeys) != 1 ||
				accounts[0].Passkeys[0].ID != account.Passkeys[0].ID {
				t.Fatalf("accounts = %+v, want the old passkey %s only", accounts, account.Passkeys[0].ID)
			}
		})
	}
}

// TestBreakGlassAddRefusesHostileSubject sends a hostile subject to the add.
// The handler checks the subject at the boundary, before it asks the passkey
// service. Each refusal answers 400, writes one denied entry on the
// "break-glass" object and stores no account. The principal service checks
// the subject too, but its refusal is a failure entry, not a denied one: a
// row whose guard is removed misses the denied entry, and fails.
func TestBreakGlassAddRefusesHostileSubject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		subject string
	}{
		{"empty subject", ""},
		{"subject over the length limit", "a" + strings.Repeat("b", 255)},
		{"subject with CR LF", "bg\r\nX-Injected: 1"},
		{"subject with a space", "bg x"},
		{"subject that starts with a dot", ".bg"},
		{"subject with unicode", "bg-é"},
		{"subject with an SQL quote", "bg' OR '1'='1"},
		{"subject with a NUL byte", "bg\x00"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")

			body := breakGlassBody(t, map[string]any{"subject": c.subject, "displayName": "Recovery"})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("add with subject %q = %d %s, want 400", c.subject, rec.Code, rec.Body.String())
			}

			denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
				"break-glass")
			if len(denied) != 1 || denied[0].Reason == "" {
				t.Fatalf("denied entries on break-glass = %+v, want one with a reason", denied)
			}

			if accounts := breakGlassAccounts(t, f); len(accounts) != 0 {
				t.Fatalf("accounts = %+v, want none after a refused add", accounts)
			}
		})
	}
}

// TestBreakGlassAddRefusesHostileDisplayName sends a hostile display name to
// the add. The handler checks it at the boundary. Each refusal answers 400,
// writes one denied entry on the "break-glass" object and stores no account.
// The principal service refuses the same values, but its refusal is a failure
// entry, not a denied one: a row whose guard is removed misses the denied
// entry, and fails.
func TestBreakGlassAddRefusesHostileDisplayName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		displayName string
	}{
		{"display name over the length limit", strings.Repeat("a", 256)},
		{"display name with CR LF", "Recovery\r\nX-Injected: 1"},
		{"display name with a NUL byte", "Recovery\x00"},
		{"display name with leading white space", " Recovery"},
		{"display name with trailing white space", "Recovery "},
		{"display name with a bidi override", "Recovery\u202eevil"},
		{"display name with a line separator", "Recovery\u2028x"},
		// Rows that only the allowlist refuses: no other check stops them.
		{"display name with markup", "<script>alert(1)</script>"},
		{"display name with a quote", `Recovery"x`},
		{"display name with a backslash", `Recovery\x`},
		{"display name with a symbol", "Recovery \u2620"},
		{"display name with a no-break space", "Recovery\u00a0x"},
		{"display name with two spaces", "Recovery  x"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")

			body := breakGlassBody(t, map[string]any{"subject": "bg-one", "displayName": c.displayName})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("add with display name %q = %d %s, want 400", c.displayName, rec.Code, rec.Body.String())
			}

			denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied,
				"break-glass")
			if len(denied) != 1 || denied[0].Reason == "" {
				t.Fatalf("denied entries on break-glass = %+v, want one with a reason", denied)
			}

			if accounts := breakGlassAccounts(t, f); len(accounts) != 0 {
				t.Fatalf("accounts = %+v, want none after a refused add", accounts)
			}
		})
	}
}

// TestBreakGlassAddAcceptsDisplayNames shows that the allowlist of the
// display name keeps the names that a person writes: letters of any script,
// digits, single spaces and the listed punctuation.
func TestBreakGlassAddAcceptsDisplayNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "Recovery", "Break-glass account 2", "O'Neil, J. (ops)", "Zoë Ångström", "Équipe d'astreinte", "復旧用"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedSessionAdmin(t, "admin")

			body := breakGlassBody(t, map[string]any{"subject": "bg-one", "displayName": name})
			rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, body)

			if rec.Code != http.StatusCreated {
				t.Fatalf("add with display name %q = %d %s, want 201", name, rec.Code, rec.Body.String())
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

// plantBreakGlassRecord stores a principal row that holds the break-glass flag
// but is not a break-glass person of the installation, as a row of another
// version would. It uses a second connection to the fixture's database file.
func plantBreakGlassRecord(t *testing.T, f accessFixture, id, kind, namespace string) {
	t.Helper()

	conn, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	const query = `INSERT INTO principals
		(id, subject, display_name, kind, source, created_at_ms, updated_at_ms, namespace, break_glass)
		VALUES (?, ?, '', ?, 'local', 0, 0, ?, 1)`

	if _, execErr := conn.ExecContext(t.Context(), query, id, id, kind, namespace); execErr != nil {
		t.Fatalf("plant break-glass record %q: %v", id, execErr)
	}
}

// breakGlassMalformedCase is a record that holds the break-glass flag but is
// not a break-glass person of the installation.
type breakGlassMalformedCase struct {
	name      string
	id        string
	kind      string
	namespace string
}

// TestBreakGlassRotateRefusesMalformedBreakGlassRecord sends a rotation to a
// record that holds the break-glass flag but is not a person of the
// installation. No port writes such a row, so it stands for data of another
// version. The check of the kind and the namespace of breakGlassAccount refuses
// it with 404 and one denied entry, and keeps the real account unchanged.
func TestBreakGlassRotateRefusesMalformedBreakGlassRecord(t *testing.T) {
	t.Parallel()

	cases := []breakGlassMalformedCase{
		{
			name: "service with the flag", id: "prn-fake-service",
			kind: string(principal.KindService), namespace: "",
		},
		{
			name: "person of a namespace with the flag", id: "prn-fake-ns",
			kind: string(principal.KindPerson), namespace: "acme",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBreakGlassRotateMalformed(t, tc)
		})
	}
}

func checkBreakGlassRotateMalformed(t *testing.T, tc breakGlassMalformedCase) {
	t.Helper()

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")
	account := addBreakGlassAccount(t, f)
	plantBreakGlassRecord(t, f, tc.id, tc.kind, tc.namespace)

	body := breakGlassBody(t, map[string]any{"oldPasskey": account.Passkeys[0].ID, "name": "next-key"})
	rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath+"/"+tc.id+"/rotate", body)

	if got := decodeErrorBody(t, rec); rec.Code != http.StatusNotFound || got.ErrorCode != errcode.BreakGlassNotFound {
		t.Fatalf("rotate %s = %d %s, want 404 with errorCode %s", tc.id, rec.Code, rec.Body.String(),
			errcode.BreakGlassNotFound)
	}

	object := principal.Ref(tc.id)
	denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied, object)
	if len(denied) != 1 || denied[0].Reason == "" {
		t.Fatalf("denied entries on %s = %+v, want one with a reason", object, denied)
	}

	checkBreakGlassKeptPasskey(t, f, account)
}

// readFailPrincipals is the SQLite store with a read of one ID that fails with
// a store error that is not principal.ErrNotFound. With an empty failID it
// fails no read. It embeds the concrete store, so each optional method of the
// store stays visible to the principal service.
type readFailPrincipals struct {
	*sqlite.Store

	failID string
}

var errPrincipalReadFailed = errors.New("select principal: the database is not available")

func (s readFailPrincipals) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	if s.failID != "" && id == s.failID {
		return principal.Principal{}, errPrincipalReadFailed
	}

	return s.Store.GetPrincipal(ctx, id)
}

// TestBreakGlassRotateReadFailureReason makes the read of the principal fail
// with a store error. The denied entry names the failed
// read as its reason, not an ID that names no account. The failing store hides
// the list route of the fixture, so the test does not read the passkeys back.
func TestBreakGlassRotateReadFailureReason(t *testing.T) {
	t.Parallel()

	const id = "prn-unreadable"

	f := newAccessFixtureAll(t, accessFixtureParts{
		entries:  func(st *sqlite.Store) auditStore { return st },
		passkeys: nil, tweak: nil, links: noGitHostLinks,
		ports: accessPorts{collections: nil, settings: nil}, now: nil, logger: nil,
		noSignIn: false, getGrantErr: nil, afterRevoke: nil, tokenList: nil, principalReadFails: id,
	})
	f.seedSessionAdmin(t, "admin")
	account := addBreakGlassAccount(t, f)

	body := breakGlassBody(t, map[string]any{"oldPasskey": account.Passkeys[0].ID, "name": "next-key"})
	rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath+"/"+id+"/rotate", body)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("rotate %s = %d %s, want 500", id, rec.Code, rec.Body.String())
	}

	denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied, principal.Ref(id))
	if len(denied) != 1 || denied[0].Reason != "the read of the principal failed" {
		t.Fatalf("denied entries on %s = %+v, want one with the read-failure reason",
			principal.Ref(id), denied)
	}

	checkBreakGlassKeptPasskey(t, f, account)
}

// checkBreakGlassKeptPasskey proves that a refusal left the real account with
// its old passkey and no other working credential.
func checkBreakGlassKeptPasskey(t *testing.T, f accessFixture, account breakGlassAccountDoc) {
	t.Helper()

	accounts := breakGlassAccounts(t, f)

	var kept *breakGlassAccountDoc
	for i := range accounts {
		if accounts[i].ID == account.ID {
			kept = &accounts[i]
			break
		}
	}

	if kept == nil || len(kept.Passkeys) != 1 || kept.Passkeys[0].ID != account.Passkeys[0].ID {
		t.Fatalf("accounts = %+v, want the old passkey %s of the real account only", accounts,
			account.Passkeys[0].ID)
	}
}
