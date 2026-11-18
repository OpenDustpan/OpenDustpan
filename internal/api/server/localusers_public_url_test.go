package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// newNoPublicURLFixture builds a handler that keeps local users and has no
// console sign-in, as a server with no public URL does.
func newNoPublicURLFixture(t *testing.T) accessFixture {
	t.Helper()

	return newAccessFixtureAll(t, accessFixtureParts{
		entries: func(st *sqlite.Store) auditStore { return st }, passkeys: nil, tweak: nil,
		links: noGitHostLinks, ports: accessPorts{collections: nil, settings: nil}, now: nil, logger: nil,
		noSignIn: true,
	})
}

// TestNoPublicURLAnswersTheCause sends hostile requests to the two public
// password routes of a handler with no console sign-in. Each one gets 403 and
// the code that names the cause, never 404, and no session cookie. The handler
// answers before it reads the body, the media type or the origin, so a hostile
// request gets the same answer as a good one and nothing reaches the store.
func TestNoPublicURLAnswersTheCause(t *testing.T) {
	t.Parallel()

	f := newNoPublicURLFixture(t)

	const secret = "hostile-secret-value-81"

	cases := []hostileCase{
		{
			"redeem good body",
			"/api/dustpan/v1/local-users/redeem",
			`{"code":"abc","password":"` + secret + `"}`,
			"application/json",
			"",
		},
		{"redeem not json", "/api/dustpan/v1/local-users/redeem", `not json ` + secret, "application/json", ""},
		{"redeem wrong media type", "/api/dustpan/v1/local-users/redeem", `{"code":"abc"}`, "text/plain", ""},
		{
			"redeem foreign origin",
			"/api/dustpan/v1/local-users/redeem",
			`{"code":"abc"}`,
			"application/json",
			"https://evil.example",
		},
		{
			"sign in good body",
			"/api/dustpan/v1/sign-in/password",
			`{"username":"alice","password":"` + secret + `"}`,
			"application/json",
			"",
		},
		{"sign in not json", "/api/dustpan/v1/sign-in/password", `{` + secret, "application/json", ""},
		{
			"sign in wrong media type",
			"/api/dustpan/v1/sign-in/password",
			`username=alice`,
			"application/x-www-form-urlencoded",
			"",
		},
		{
			"sign in foreign origin",
			"/api/dustpan/v1/sign-in/password",
			`{"username":"alice","password":"x"}`,
			"application/json",
			"https://evil.example",
		},
		{"sign in empty body", "/api/dustpan/v1/sign-in/password", ``, "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := sendHostile(t, f, tc)
			wantPublicURLAnswer(t, rec, secret)
		})
	}
}

// hostileCase is one request of the table.
type hostileCase struct {
	name, path, body, mediaType, origin string
}

// sendHostile sends a POST with the given body, media type and origin, and no
// credential.
func sendHostile(t *testing.T, f accessFixture, tc hostileCase) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.path, strings.NewReader(tc.body))
	if tc.mediaType != "" {
		req.Header.Set("Content-Type", tc.mediaType)
	}

	if tc.origin != "" {
		req.Header.Set("Origin", tc.origin)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// wantPublicURLAnswer fails unless rec is the 403 answer that names the code
// and the flag, echoes none of the request and sets no session cookie.
func wantPublicURLAnswer(t *testing.T, rec *httptest.ResponseRecorder, secret string) {
	t.Helper()

	body := rec.Body.String()

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, body)
	}

	if !strings.Contains(body, errcode.LocalUserPublicURLRequired) || !strings.Contains(body, "--public-url") {
		t.Fatalf("body = %s, want the code and the flag", body)
	}

	if strings.Contains(body, secret) {
		t.Fatalf("body echoes the request: %s", body)
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == apiserver.SessionCookie {
			t.Fatalf("session cookie set: %v", c)
		}
	}
}
