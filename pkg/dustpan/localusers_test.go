package dustpan_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// localPublicURL is the public URL of the server of the local user tests. A
// console session needs an HTTPS public URL.
const localPublicURL = "https://dustpan.example"

// localPasswords pass every default rule.
const (
	localPassword = "violet-harbor-tin-81-ledger"
	localNewPass  = "copper-meadow-quiet-47-lantern"
)

// localServer is a started server with local users and a console sign-in.
type localServer struct {
	handler http.Handler
}

func newLocalServer(t *testing.T) localServer {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithPublicURL(localPublicURL))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	return localServer{handler: startedHandler(t, srv)}
}

// localRequest is one request of a local user test.
type localRequest struct {
	method, path, body, token, cookie, csrf string
}

func (s localServer) do(t *testing.T, in localRequest) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), in.method, in.path, strings.NewReader(in.body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", localPublicURL)

	if in.token != "" {
		req.Header.Set("Authorization", "Bearer "+in.token)
	}

	if in.cookie != "" {
		req.Header.Add("Cookie", "__Host-dustpan_session="+in.cookie)
	}

	if in.csrf != "" {
		req.Header.Set("X-Dustpan-CSRF", in.csrf)
	}

	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)

	return rec
}

// wantAnswer fails unless rec has the status and, when code is not empty, the
// error code.
func wantAnswer(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("%s: status = %d, want %d: %s", what, rec.Code, status, rec.Body)
	}

	if code == "" {
		return
	}

	var body struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.ErrorCode != code {
		t.Fatalf("%s: error code = %q (%v), want %q: %s", what, body.ErrorCode, err, code, rec.Body)
	}
}

// sessionOf returns the value of the session cookie that rec sets.
func sessionOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-dustpan_session" && c.Value != "" {
			return c.Value
		}
	}

	t.Fatalf("no session cookie in %v", rec.Header())

	return ""
}

// csrfOf returns the CSRF value of the session cookie.
func (s localServer) csrfOf(t *testing.T, cookie string) (string, string) {
	t.Helper()

	rec := s.do(t, localRequest{method: http.MethodGet, path: "/api/server/session", cookie: cookie})
	wantAnswer(t, "read the session", rec, http.StatusOK, "")

	var body struct {
		Subject   string `json:"subject"`
		CSRFToken string `json:"csrfToken"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the session: %v", err)
	}

	return body.Subject, body.CSRFToken
}

// TestLocalPasswordRoutes drives the routes of the local users through the
// mux of the server: an administrator makes a user, the person sets the
// password with the set-up code, signs in, and changes the password. Each
// hostile request is refused at its layer.
func TestLocalPasswordRoutes(t *testing.T) {
	t.Parallel()

	s := newLocalServer(t)

	created := s.do(t, localRequest{
		method: http.MethodPost, path: "/api/dustpan/v1/local-users", token: adminToken,
		body: `{"userName":"alice","displayName":"Alice Example","reason":"new team member"}`,
	})
	wantAnswer(t, "create", created, http.StatusCreated, "")

	var user struct {
		Subject   string `json:"subject"`
		SetUpCode string `json:"setUpCode"`
	}

	if err := json.Unmarshal(created.Body.Bytes(), &user); err != nil || len(user.SetUpCode) != 43 {
		t.Fatalf("created = %s (%v)", created.Body, err)
	}

	redeemTests := []struct {
		name, body, code string
		status           int
	}{
		{name: "unknown field", body: `{"code":"x","password":"y","admin":true}`, status: 400, code: "request.body-invalid"},
		{name: "not an object", body: `["x"]`, status: 400, code: "request.body-invalid"},
		{name: "two objects", body: `{"code":"x","password":"y"}{}`, status: 400, code: "request.body-invalid"},
		{name: "code not valid", body: `{"code":"x","password":"` + localPassword + `"}`, status: 400, code: "local-user.code-invalid"},
		{name: "short password", body: `{"code":"` + user.SetUpCode + `","password":"short"}`, status: 400, code: "password.too-short"},
	}

	for _, tc := range redeemTests {
		rec := s.do(t, localRequest{method: http.MethodPost, path: "/api/dustpan/v1/local-users/redeem", body: tc.body})
		wantAnswer(t, "redeem: "+tc.name, rec, tc.status, tc.code)

		if strings.Contains(rec.Body.String(), localPassword) {
			t.Fatalf("redeem: %s: the answer holds the password", tc.name)
		}
	}

	redeemed := s.do(t, localRequest{
		method: http.MethodPost, path: "/api/dustpan/v1/local-users/redeem",
		body: `{"code":"` + user.SetUpCode + `","password":"` + localPassword + `"}`,
	})
	wantAnswer(t, "redeem", redeemed, http.StatusNoContent, "")

	if got := redeemed.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}

	signInTests := []struct {
		name, body, code string
		status           int
	}{
		{name: "wrong password", body: `{"userName":"alice","password":"` + localNewPass + `"}`, status: 401, code: "local-user.sign-in-failed"},
		{name: "unknown name", body: `{"userName":"carol","password":"` + localPassword + `"}`, status: 401, code: "local-user.sign-in-failed"},
		{name: "unknown field", body: `{"userName":"alice","password":"x","otp":"1"}`, status: 400, code: "request.body-invalid"},
		{name: "too large", body: `{"userName":"alice","password":"` + strings.Repeat("a", 9000) + `"}`, status: 400, code: "request.body-invalid"},
	}

	for _, tc := range signInTests {
		rec := s.do(t, localRequest{method: http.MethodPost, path: "/api/dustpan/v1/sign-in/password", body: tc.body})
		wantAnswer(t, "sign in: "+tc.name, rec, tc.status, tc.code)

		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("sign in: %s: the answer sets a cookie", tc.name)
		}
	}

	signedIn := s.do(t, localRequest{
		method: http.MethodPost, path: "/api/dustpan/v1/sign-in/password",
		body: `{"userName":"Alice","password":"` + localPassword + `"}`,
	})
	wantAnswer(t, "sign in", signedIn, http.StatusNoContent, "")

	cookie := sessionOf(t, signedIn)

	subject, csrf := s.csrfOf(t, cookie)
	if subject != user.Subject || !strings.HasPrefix(subject, "local:lus-") {
		t.Fatalf("session subject = %q, want %q", subject, user.Subject)
	}

	// The fresh layer: an access token never stands for a fresh
	// authentication, so the reset and the removal refuse it.
	for _, path := range []string{"/api/dustpan/v1/local-users/reset"} {
		rec := s.do(t, localRequest{
			method: http.MethodPost, path: path, token: adminToken,
			body: `{"principalId":"prn-x","reason":"lost"}`,
		})
		wantAnswer(t, path, rec, http.StatusForbidden, "mfa.fresh-step-required")
	}

	wrong := s.do(t, localRequest{
		method: http.MethodPost, path: "/api/dustpan/v1/me/password", cookie: cookie, csrf: csrf,
		body: `{"currentPassword":"` + localNewPass + `","newPassword":"` + localNewPass + `"}`,
	})
	wantAnswer(t, "change with a wrong current password", wrong, http.StatusForbidden,
		"local-user.current-password-wrong")

	noCSRF := s.do(t, localRequest{
		method: http.MethodPost, path: "/api/dustpan/v1/me/password", cookie: cookie,
		body: `{"currentPassword":"` + localPassword + `","newPassword":"` + localNewPass + `"}`,
	})
	if noCSRF.Code < 400 {
		t.Fatalf("change with no CSRF header = %d, want a refusal", noCSRF.Code)
	}

	changed := s.do(t, localRequest{
		method: http.MethodPost, path: "/api/dustpan/v1/me/password", cookie: cookie, csrf: csrf,
		body: `{"currentPassword":"` + localPassword + `","newPassword":"` + localNewPass + `"}`,
	})
	wantAnswer(t, "change", changed, http.StatusNoContent, "")

	renewed := sessionOf(t, changed)

	// The change added 1 to the session generation: the old cookie no longer
	// authenticates, and the new one does.
	old := s.do(t, localRequest{method: http.MethodGet, path: "/api/server/session", cookie: cookie})
	if old.Code != http.StatusUnauthorized {
		t.Fatalf("old session after a change = %d, want 401: %s", old.Code, old.Body)
	}

	if got, _ := s.csrfOf(t, renewed); got != user.Subject {
		t.Fatalf("renewed session subject = %q, want %q", got, user.Subject)
	}

	policy := s.do(t, localRequest{method: http.MethodGet, path: "/api/dustpan/v1/local-users/policy", token: adminToken})
	wantAnswer(t, "policy", policy, http.StatusOK, "")

	if !strings.Contains(policy.Body.String(), `"minLength":15`) ||
		!strings.Contains(policy.Body.String(), `"enabledSource":"default"`) {
		t.Fatalf("policy = %s", policy.Body)
	}
}
