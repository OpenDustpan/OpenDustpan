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

// Passwords of the local user tests. Each one passes every default rule.
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

// localCaller is the credential of one request: the administrator token, a
// session cookie with its CSRF value, or nothing.
type localCaller struct {
	token  string
	cookie string
	csrf   string
}

// anonymous is a request with no credential.
func anonymous() localCaller { return localCaller{token: "", cookie: "", csrf: ""} }

// administrator is a request with the token of the bootstrap administrator.
func administrator() localCaller { return localCaller{token: adminToken, cookie: "", csrf: ""} }

// withCookie is a request with the session cookie and its CSRF value.
func withCookie(cookie, csrf string) localCaller {
	return localCaller{token: "", cookie: cookie, csrf: csrf}
}

func (s localServer) do(t *testing.T, method, path, body string, c localCaller) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", localPublicURL)

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	if c.cookie != "" {
		req.Header.Add("Cookie", "__Host-dustpan_session="+c.cookie)
	}

	if c.csrf != "" {
		req.Header.Set("X-Dustpan-Csrf", c.csrf)
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

// sessionView is the subject and the CSRF value of a console session.
type sessionView struct {
	subject string
	csrf    string
}

// readSession returns the subject and the CSRF value of the session cookie.
func (s localServer) readSession(t *testing.T, cookie string) sessionView {
	t.Helper()

	rec := s.do(t, http.MethodGet, "/api/server/session", "", withCookie(cookie, ""))
	wantAnswer(t, "read the session", rec, http.StatusOK, "")

	var body struct {
		Subject   string `json:"subject"`
		CSRFToken string `json:"csrfToken"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the session: %v", err)
	}

	return sessionView{subject: body.Subject, csrf: body.CSRFToken}
}

// localRefusal is one request that a route refuses with a status and a code.
type localRefusal struct {
	name, body, code string
	status           int
}

// checkRefusals sends each refusal to path with no credential: each one
// answers its status and code, sets no cookie and echoes no password.
func (s localServer) checkRefusals(t *testing.T, path string, cases []localRefusal) {
	t.Helper()

	for _, tc := range cases {
		rec := s.do(t, http.MethodPost, path, tc.body, anonymous())
		wantAnswer(t, path+": "+tc.name, rec, tc.status, tc.code)

		if len(rec.Result().Cookies()) != 0 || strings.Contains(rec.Body.String(), localPassword) {
			t.Fatalf("%s: %s: the answer sets a cookie or holds the password", path, tc.name)
		}
	}
}

// createdUser is the subject and the set-up code of a new local user.
type createdUser struct {
	subject string
	code    string
}

// createLocalUser makes the local user alice and returns her subject and
// her set-up code.
func (s localServer) createLocalUser(t *testing.T) createdUser {
	t.Helper()

	created := s.do(t, http.MethodPost, "/api/dustpan/v1/local-users",
		`{"userName":"alice","displayName":"Alice Example","reason":"new team member"}`, administrator())
	wantAnswer(t, "create", created, http.StatusCreated, "")

	var user struct {
		Subject   string `json:"subject"`
		SetUpCode string `json:"setUpCode"`
	}

	if err := json.Unmarshal(created.Body.Bytes(), &user); err != nil || len(user.SetUpCode) != 43 {
		t.Fatalf("created = %s (%v)", created.Body, err)
	}

	return createdUser{subject: user.Subject, code: user.SetUpCode}
}

// TestLocalPasswordRoutes drives the routes of the local users through the
// mux of the server: an administrator makes a user, the person sets the
// password with the set-up code, signs in, and changes the password. Each
// hostile request is refused at its layer.
func TestLocalPasswordRoutes(t *testing.T) {
	t.Parallel()

	s := newLocalServer(t)
	user := s.createLocalUser(t)
	subject, code := user.subject, user.code

	s.checkRefusals(t, "/api/dustpan/v1/local-users/redeem", []localRefusal{
		{
			name:   "unknown field",
			body:   `{"code":"x","password":"y","admin":true}`,
			status: 400,
			code:   "request.body-invalid",
		},
		{name: "not an object", body: `["x"]`, status: 400, code: "request.body-invalid"},
		{name: "two objects", body: `{"code":"x","password":"y"}{}`, status: 400, code: "request.body-invalid"},
		{
			name: "code not valid", body: `{"code":"x","password":"` + localPassword + `"}`, status: 400,
			code: "local-user.code-invalid",
		},
		{
			name:   "short password",
			body:   `{"code":"` + code + `","password":"short"}`,
			status: 400,
			code:   "password.too-short",
		},
	})

	redeemed := s.do(t, http.MethodPost, "/api/dustpan/v1/local-users/redeem",
		`{"code":"`+code+`","password":"`+localPassword+`"}`, anonymous())
	wantAnswer(t, "redeem", redeemed, http.StatusNoContent, "")

	if got := redeemed.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}

	s.checkRefusals(t, "/api/dustpan/v1/sign-in/password", []localRefusal{
		{
			name: "wrong password", body: `{"userName":"alice","password":"` + localNewPass + `"}`, status: 401,
			code: "local-user.sign-in-failed",
		},
		{
			name: "unknown name", body: `{"userName":"carol","password":"` + localPassword + `"}`, status: 401,
			code: "local-user.sign-in-failed",
		},
		{
			name:   "unknown field",
			body:   `{"userName":"alice","password":"x","otp":"1"}`,
			status: 400,
			code:   "request.body-invalid",
		},
		{
			name: "too large", body: `{"userName":"alice","password":"` + strings.Repeat("a", 9000) + `"}`, status: 400,
			code: "request.body-invalid",
		},
	})

	signedIn := s.do(t, http.MethodPost, "/api/dustpan/v1/sign-in/password",
		`{"userName":"Alice","password":"`+localPassword+`"}`, anonymous())
	wantAnswer(t, "sign in", signedIn, http.StatusNoContent, "")

	cookie := sessionOf(t, signedIn)

	got := s.readSession(t, cookie).subject
	if got != subject || !strings.HasPrefix(got, "local:lus-") {
		t.Fatalf("session subject = %q, want %q", got, subject)
	}

	s.checkChange(t, cookie, subject)
	s.checkAdministratorRoutes(t)
}

// checkChange changes the password of the session cookie: a wrong current
// password and a missing CSRF header are refused, and a good change ends the
// old session and sets a new one.
func (s localServer) checkChange(t *testing.T, cookie, subject string) {
	t.Helper()

	csrf := s.readSession(t, cookie).csrf

	wrong := s.do(t, http.MethodPost, "/api/dustpan/v1/me/password",
		`{"currentPassword":"`+localNewPass+`","newPassword":"`+localNewPass+`"}`, withCookie(cookie, csrf))
	wantAnswer(t, "change with a wrong current password", wrong, http.StatusForbidden, "local-user.current-wrong")

	noCSRF := s.do(t, http.MethodPost, "/api/dustpan/v1/me/password",
		`{"currentPassword":"`+localPassword+`","newPassword":"`+localNewPass+`"}`, withCookie(cookie, ""))
	if noCSRF.Code < http.StatusBadRequest {
		t.Fatalf("change with no CSRF header = %d, want a refusal", noCSRF.Code)
	}

	changed := s.do(t, http.MethodPost, "/api/dustpan/v1/me/password",
		`{"currentPassword":"`+localPassword+`","newPassword":"`+localNewPass+`"}`, withCookie(cookie, csrf))
	wantAnswer(t, "change", changed, http.StatusNoContent, "")

	renewed := sessionOf(t, changed)

	// The change added 1 to the session generation: the old cookie no longer
	// authenticates, and the new one does.
	old := s.do(t, http.MethodGet, "/api/server/session", "", withCookie(cookie, ""))
	if old.Code != http.StatusUnauthorized {
		t.Fatalf("old session after a change = %d, want 401: %s", old.Code, old.Body)
	}

	if got := s.readSession(t, renewed).subject; got != subject {
		t.Fatalf("renewed session subject = %q, want %q", got, subject)
	}
}

// checkAdministratorRoutes reads the policy with the token, and proves that a
// token never stands for the fresh authentication of a reset.
func (s localServer) checkAdministratorRoutes(t *testing.T) {
	t.Helper()

	reset := s.do(t, http.MethodPost, "/api/dustpan/v1/local-users/reset",
		`{"principalID":"prn-x","reason":"lost"}`, administrator())
	wantAnswer(t, "reset with a token", reset, http.StatusForbidden, "mfa.fresh-step-required")

	policy := s.do(t, http.MethodGet, "/api/dustpan/v1/local-users/policy", "", administrator())
	wantAnswer(t, "policy", policy, http.StatusOK, "")

	if !strings.Contains(policy.Body.String(), `"minLength":15`) ||
		!strings.Contains(policy.Body.String(), `"enabledSource":"default"`) {
		t.Fatalf("policy = %s", policy.Body)
	}
}
