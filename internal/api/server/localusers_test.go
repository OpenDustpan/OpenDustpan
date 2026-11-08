package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// Passwords of the local user tests. Each one passes every default rule.
const (
	routePassword    = "violet-harbor-tin-81-ledger"
	routeNewPassword = "copper-meadow-quiet-47-lantern"
)

// publicPost sends a POST of a JSON body with no credential and returns the
// answer.
func publicPost(t *testing.T, f accessFixture, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// sessionCookieOf returns the session cookie that rec sets, or fails.
func sessionCookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	for _, c := range rec.Result().Cookies() {
		if c.Name == apiserver.SessionCookie && c.Value != "" {
			return c
		}
	}

	t.Fatalf("no session cookie: %v", rec.Header())

	return nil
}

// TestLocalUserSuccessPaths walks each local user route on its success path:
// an administrator makes a user, the person sets the password with the
// set-up code, signs in, changes the password, and an administrator resets
// it.
func TestLocalUserSuccessPaths(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var created struct {
		PrincipalID string `json:"principal"`
		SetUpCode   string `json:"setUpCode"`
	}

	f.asFresh(t, "admin", apiCall{
		method: http.MethodPost, path: "/api/dustpan/v1/local-users",
		body: `{"userName":"carol","displayName":"Carol","reason":"new team member"}`, want: http.StatusCreated,
	}, &created)

	redeemed := publicPost(t, f, "/api/dustpan/v1/local-users/redeem",
		`{"oneTimeCode":"`+created.SetUpCode+`","password":"`+routePassword+`"}`)
	if redeemed.Code != http.StatusNoContent || redeemed.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("redeem = %d %v: %s", redeemed.Code, redeemed.Header(), redeemed.Body)
	}

	signedIn := publicPost(t, f, "/api/dustpan/v1/sign-in/password",
		`{"userName":"carol","password":"`+routePassword+`"}`)
	if signedIn.Code != http.StatusNoContent {
		t.Fatalf("sign in = %d: %s", signedIn.Code, signedIn.Body)
	}

	cookie := sessionCookieOf(t, signedIn)

	read := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/session", http.NoBody)
	read.AddCookie(cookie)

	readRec := httptest.NewRecorder()
	f.handler.ServeHTTP(readRec, read)

	var sess struct {
		CSRFToken string `json:"csrfToken"`
		Provider  string `json:"provider"`
	}

	if err := json.Unmarshal(readRec.Body.Bytes(), &sess); err != nil || sess.Provider != "local" {
		t.Fatalf("session = %d %s (%v)", readRec.Code, readRec.Body, err)
	}

	change := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/me/password",
		strings.NewReader(`{"currentPassword":"`+routePassword+`","newPassword":"`+routeNewPassword+`"}`))
	change.AddCookie(cookie)
	change.Header.Set(apiserver.CSRFHeader, sess.CSRFToken)
	change.Header.Set("Sec-Fetch-Site", "same-origin")

	changeRec := httptest.NewRecorder()
	f.handler.ServeHTTP(changeRec, change)

	if changeRec.Code != http.StatusNoContent {
		t.Fatalf("change = %d: %s", changeRec.Code, changeRec.Body)
	}

	if renewed := sessionCookieOf(t, changeRec); renewed.Value == cookie.Value {
		t.Fatal("the change kept the old session cookie, want a new one")
	}

	var reset struct {
		ResetCode string `json:"resetCode"`
	}

	f.asFresh(t, "admin", apiCall{
		method: http.MethodPost, path: "/api/dustpan/v1/local-users/reset",
		body: `{"principal":"` + created.PrincipalID + `","reason":"lost the password"}`, want: http.StatusCreated,
	}, &reset)

	if len(reset.ResetCode) != 43 {
		t.Fatalf("reset code of %d characters, want 43", len(reset.ResetCode))
	}
}

// TestPasswordSignInRefusals sends hostile requests to the public routes:
// each one is refused with its code, sets no cookie and echoes no password.
func TestPasswordSignInRefusals(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	cases := []struct {
		name, path, body, code string
		status                 int
	}{
		{
			name: "unknown name", path: "/api/dustpan/v1/sign-in/password",
			body: `{"userName":"nobody","password":"` + routePassword + `"}`, status: http.StatusUnauthorized,
			code: "local-user.sign-in-failed",
		},
		{
			name: "name with CR and LF", path: "/api/dustpan/v1/sign-in/password",
			body: `{"userName":"a\r\nb","password":"` + routePassword + `"}`, status: http.StatusUnauthorized,
			code: "local-user.sign-in-failed",
		},
		{
			name: "unknown field", path: "/api/dustpan/v1/sign-in/password",
			body: `{"userName":"a","password":"b","admin":true}`, status: http.StatusBadRequest,
			code: "request.body-invalid",
		},
		{
			name: "lone surrogate", path: "/api/dustpan/v1/sign-in/password",
			body: `{"userName":"a","password":"\ud800"}`, status: http.StatusBadRequest, code: "request.body-invalid",
		},
		{
			name: "code of a wrong form", path: "/api/dustpan/v1/local-users/redeem",
			body: `{"oneTimeCode":"../../etc","password":"` + routePassword + `"}`, status: http.StatusBadRequest,
			code: "local-user.code-invalid",
		},
	}

	for _, tc := range cases {
		rec := publicPost(t, f, tc.path, tc.body)

		var body struct {
			ErrorCode string `json:"errorCode"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != tc.status ||
			body.ErrorCode != tc.code {
			t.Fatalf("%s: answer = %d %s, want %d %s", tc.name, rec.Code, rec.Body, tc.status, tc.code)
		}

		if len(rec.Result().Cookies()) != 0 || strings.Contains(rec.Body.String(), routePassword) {
			t.Fatalf("%s: the answer sets a cookie or holds the password", tc.name)
		}
	}
}

// createRouteLocalUser makes the local user carol with a password through
// the routes, and returns its principal ID.
func createRouteLocalUser(t *testing.T, f accessFixture) string {
	t.Helper()

	var created struct {
		PrincipalID string `json:"principal"`
		SetUpCode   string `json:"setUpCode"`
	}

	f.asFresh(t, "admin", apiCall{
		method: http.MethodPost, path: "/api/dustpan/v1/local-users",
		body: `{"userName":"carol","displayName":"Carol","reason":"new team member"}`, want: http.StatusCreated,
	}, &created)

	redeemed := publicPost(t, f, "/api/dustpan/v1/local-users/redeem",
		`{"oneTimeCode":"`+created.SetUpCode+`","password":"`+routePassword+`"}`)
	if redeemed.Code != http.StatusNoContent {
		t.Fatalf("redeem = %d: %s", redeemed.Code, redeemed.Body)
	}

	return created.PrincipalID
}

// TestPasswordChecksSpendThePasswordBudget proves that each failed check of
// a password or a code spends one unit of the password budget of the
// address: a wrong password at the sign-in, a code that is not valid, and a
// wrong current password at a change of the own password.
func TestPasswordChecksSpendThePasswordBudget(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createRouteLocalUser(t, f)

	before := f.passwords.count()

	wrong := publicPost(t, f, "/api/dustpan/v1/sign-in/password",
		`{"userName":"carol","password":"`+routeNewPassword+`"}`)
	if wrong.Code != http.StatusUnauthorized || f.passwords.count() != before+1 {
		t.Fatalf("wrong password = %d with %d failures, want 401 and %d", wrong.Code, f.passwords.count(), before+1)
	}

	code := publicPost(t, f, "/api/dustpan/v1/local-users/redeem",
		`{"oneTimeCode":"`+strings.Repeat("A", 43)+`","password":"`+routePassword+`"}`)
	if code.Code != http.StatusBadRequest || f.passwords.count() != before+2 {
		t.Fatalf("unknown code = %d with %d failures, want 400 and %d", code.Code, f.passwords.count(), before+2)
	}

	signedIn := publicPost(t, f, "/api/dustpan/v1/sign-in/password",
		`{"userName":"carol","password":"`+routePassword+`"}`)
	cookie := sessionCookieOf(t, signedIn)

	read := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/session", http.NoBody)
	read.AddCookie(cookie)

	readRec := httptest.NewRecorder()
	f.handler.ServeHTTP(readRec, read)

	var sess struct {
		CSRFToken string `json:"csrfToken"`
	}

	decodeInto(t, readRec.Body.String(), &sess)

	change := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/me/password",
		strings.NewReader(`{"currentPassword":"`+routeNewPassword+`","newPassword":"`+routeNewPassword+`"}`))
	change.AddCookie(cookie)
	change.Header.Set(apiserver.CSRFHeader, sess.CSRFToken)
	change.Header.Set("Sec-Fetch-Site", "same-origin")

	changeRec := httptest.NewRecorder()
	f.handler.ServeHTTP(changeRec, change)

	if got := errorCodeOf(t, changeRec.Body.String()); changeRec.Code != http.StatusForbidden ||
		got != "local-user.current-password-wrong" || f.passwords.count() != before+3 {
		t.Fatalf("wrong current password = %d %s with %d failures, want 403 and %d", changeRec.Code, got,
			f.passwords.count(), before+3)
	}
}

// TestPasswordRoutesRefuseAFullBudget proves that each route that checks a
// password or a code refuses a request when the password budget of the
// address refuses it, before it reads the body.
func TestPasswordRoutesRefuseAFullBudget(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createRouteLocalUser(t, f)

	f.passwords.mu.Lock()
	f.passwords.refuse = true
	f.passwords.mu.Unlock()

	for _, path := range []string{"/api/dustpan/v1/sign-in/password", "/api/dustpan/v1/local-users/redeem"} {
		rec := publicPost(t, f, path, `{"userName":"carol","password":"`+routePassword+`"}`)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("%s with a full budget = %d, want 429: %s", path, rec.Code, rec.Body)
		}
	}
}

// TestResetNeedsTheReachOfThePerson proves that an administrator who holds
// the action of a reset, but not every action of the person, gets 403 with
// the code local-user.outside-reach.
func TestResetNeedsTheReachOfThePerson(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	carol := createRouteLocalUser(t, f)

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	role := access.Role{
		CreatedAt: at, UpdatedAt: at, ID: "rol-signin", Name: "Sign-in manager", Description: "Manages the sign-in.",
		Actions: []identity.Action{identity.ActionServerManageSignIn}, BuiltIn: false,
	}

	if err := f.store.CreateRole(t.Context(), role, storetest.ServerChange(at, "role:rol-signin")); err != nil {
		t.Fatalf("create role: %v", err)
	}

	f.seed(t, "sam", "rol-signin", identity.ServerObject())
	grantInstallation(t, f, carol, access.RoleSiteAdministrator, "grt-carol")

	rec := f.sessionPost(t, "sam", "/api/dustpan/v1/local-users/reset",
		`{"principal":"`+carol+`","reason":"lost the password"}`)
	if got := errorCodeOf(t, rec.Body.String()); rec.Code != http.StatusForbidden || got != "local-user.outside-reach" {
		t.Fatalf("reset outside the reach = %d %s, want 403 local-user.outside-reach", rec.Code, got)
	}
}

// TestUnlockNeedsTheReachOfThePerson proves that an administrator who holds
// the action of an unlock and of the read of the counts, but not every
// action of the person, gets 403 with the code local-user.outside-reach on
// both routes.
func TestUnlockNeedsTheReachOfThePerson(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	carol := createRouteLocalUser(t, f)

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	role := access.Role{
		CreatedAt: at, UpdatedAt: at, ID: "rol-signin", Name: "Sign-in manager", Description: "Manages the sign-in.",
		Actions: []identity.Action{identity.ActionServerManageSignIn}, BuiltIn: false,
	}

	if err := f.store.CreateRole(t.Context(), role, storetest.ServerChange(at, "role:rol-signin")); err != nil {
		t.Fatalf("create role: %v", err)
	}

	f.seed(t, "sam", "rol-signin", identity.ServerObject())
	grantInstallation(t, f, carol, access.RoleSiteAdministrator, "grt-carol")

	rec := f.sessionPost(t, "sam", "/api/dustpan/v1/local-users/unlock",
		`{"principal":"`+carol+`","reason":"the person called"}`)
	if got := errorCodeOf(t, rec.Body.String()); rec.Code != http.StatusForbidden || got != "local-user.outside-reach" {
		t.Fatalf("unlock outside the reach = %d %s, want 403 local-user.outside-reach", rec.Code, got)
	}

	cookie, _ := f.sessionOf(t, "sam", f.sessions.Now())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/dustpan/v1/local-users/locks?principal="+carol, nil)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	read := httptest.NewRecorder()
	f.handler.ServeHTTP(read, req)

	if got := errorCodeOf(t, read.Body.String()); read.Code != http.StatusForbidden ||
		got != "local-user.outside-reach" {
		t.Fatalf("read outside the reach = %d %s, want 403 local-user.outside-reach", read.Code, got)
	}
}

// grantInstallation gives the principal principalID the role on the
// installation, directly in the store.
func grantInstallation(t *testing.T, f accessFixture, principalID, role, grantID string) {
	t.Helper()

	obj := identity.ServerObject()
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: grantID, Role: role, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}

	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:"+grantID)
	if err := f.store.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("seed grant %s: %v", grantID, err)
	}
}

// sessionPost sends a POST as a fresh console session of subject and
// returns the answer.
func (f accessFixture) sessionPost(t *testing.T, subject, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	cookie, csrf := f.sessionOf(t, subject, f.sessions.Now())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set(apiserver.CSRFHeader, csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// TestStalePasswordSessionGetsNoStartLink proves that a change that needs a
// fresh authentication, from a session of the password sign-in with an old
// sign-in, answers 401 with no link to a start route: the password
// sign-in has none to open.
func TestStalePasswordSessionGetsNoStartLink(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	carol := createRouteLocalUser(t, f)
	f.seedSessionAdmin(t, "admin")

	now := f.sessions.Now()
	e := audit.Entry{
		At: now, Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "session:start", Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelConsole, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	_, secret, err := f.sessions.Start(t.Context(), e, session.Start{
		AuthTime: now.Add(-24 * time.Hour), PrincipalID: "prn-admin", Provider: localuser.ProviderID,
		SourceIP: "192.0.2.1", UserAgent: "test", Generation: 0, Stored: false, Alert: nil,
	})
	if err != nil {
		t.Fatalf("start the session: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/local-users/reset",
		strings.NewReader(`{"principal":"`+carol+`","reason":"lost the password"}`))
	req.AddCookie(&http.Cookie{
		Name: apiserver.SessionCookie, Value: secret.Reveal(), Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	req.Header.Set(apiserver.CSRFHeader, session.CSRFValue(secret))
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "reauthenticate") ||
		!strings.Contains(rec.Body.String(), "with the password") {
		t.Fatalf("stale password session = %d %s, want 401 with no start link", rec.Code, rec.Body)
	}
}

// localSignedIn returns the result of a good password sign-in of the
// principal principalID with a valid Assertion.
func localSignedIn(principalID string) localusersvc.SignedIn {
	return localusersvc.SignedIn{
		Assertion: session.Assertion{
			AuthTime: time.Now(), ProviderID: session.LocalProviderID, PrincipalID: principalID, Subject: "usr-carol",
			UserName: "carol", Email: "", Method: session.MethodLocalPassword, Names: nil, Groups: nil,
			Factors: []session.Factor{session.FactorPassword}, EmailVerified: false, GroupsPresent: false,
			AuthTimeKnown: true,
		},
		PrincipalID: principalID, UserID: "usr-carol", SessionGeneration: 1, ChangeRequired: false,
	}
}

// TestPasswordSignInRefusesAHostileAssertion sends a hostile Assertion of a
// password sign-in straight to the check of the password route, as if the
// service had made it. The route refuses it with 401 and the answer of a
// wrong password, writes the refusal with the reason of an Assertion that is
// not valid, counts one failure of the password budget, and sets no session
// cookie. A valid Assertion passes, so the check is not vacuous.
func TestPasswordSignInRefusesAHostileAssertion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		change func(in *localusersvc.SignedIn)
		name   string
		ok     bool
	}{
		{func(*localusersvc.SignedIn) {}, "valid", true},
		{func(in *localusersvc.SignedIn) { in.Assertion.Subject = "" }, "empty subject", false},
		{
			func(in *localusersvc.SignedIn) { in.Assertion.AuthTime = time.Now().Add(24 * time.Hour) },
			"future auth time", false,
		},
		{func(in *localusersvc.SignedIn) { in.Assertion.PrincipalID = "prn-other" }, "other principal", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			principalID := createRouteLocalUser(t, f)
			in := localSignedIn(principalID)

			// The session of a valid sign-in carries the generation of the record.
			state, err := f.store.PrincipalState(t.Context(), principalID)
			if err != nil {
				t.Fatalf("principal state: %v", err)
			}

			in.SessionGeneration = state.Generation
			tc.change(&in)

			failures := f.passwords.count()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/sign-in/password",
				http.NoBody)
			rec := httptest.NewRecorder()

			if got := f.handler.CompleteLocalForTest(rec, req, in); got != tc.ok {
				t.Fatalf("accepted = %v, want %v", got, tc.ok)
			}

			if tc.ok {
				return
			}

			checkLocalRefusal(t, f, rec, failures)
		})
	}
}

// checkLocalRefusal fails unless rec is the 401 of a wrong password with no
// cookie, the password budget counts one more failure than before, and the
// last refused sign-in entry has the reason of an Assertion that is not
// valid.
func checkLocalRefusal(t *testing.T, f accessFixture, rec *httptest.ResponseRecorder, before int) {
	t.Helper()

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "local-user.sign-in-failed") {
		t.Fatalf("answer = %d %s, want 401 local-user.sign-in-failed", rec.Code, rec.Body.String())
	}

	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("the refusal sets cookies: %v", rec.Result().Cookies())
	}

	if got := f.passwords.count(); got != before+1 {
		t.Fatalf("password failures = %d, want %d", got, before+1)
	}

	reason, found := lastRefusalReason(serverEntries(t, f.store))
	if !found {
		t.Fatal("no refused sign-in entry")
	}

	if reason != "sign-in assertion is not valid" {
		t.Fatalf("audit reason = %q, want the reason of an Assertion that is not valid", reason)
	}
}

// lastRefusalReason returns the reason of the last refused sign-in entry of
// entries, and false when there is none.
func lastRefusalReason(entries []audit.Entry) (string, bool) {
	for i := range slices.Backward(entries) {
		en := &entries[i]
		if en.Action != session.ActionSignIn || en.Outcome == audit.OutcomeSuccess {
			continue
		}

		for _, c := range en.Changes {
			if c.Key == "reason" {
				return c.New, true
			}
		}
	}

	return "", false
}
