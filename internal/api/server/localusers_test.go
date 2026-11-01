package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
)

// Passwords of the local user tests. Each one passes every default rule.
const (
	routePassword    = "violet-harbor-tin-81-ledger"
	routeNewPassword = "copper-meadow-quiet-47-lantern"
)

// publicPost sends a POST with no credential and returns the answer.
func publicPost(t *testing.T, f accessFixture, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
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
