package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/internal/store/sqlite"
)

// newNoPublicURLFixture builds a handler that keeps local users and has no
// console sign-in, as a server with no public URL does.
func newNoPublicURLFixture(t *testing.T) accessFixture {
	t.Helper()

	return newAccessFixtureAll(t, accessFixtureParts{
		entries: func(st *sqlite.Store) auditStore { return st }, passkeys: nil, tweak: nil,
		links: noGitHostLinks, ports: accessPorts{collections: nil, settings: nil}, now: nil, logger: nil,
		noSignIn: true, getGrantErr: nil,
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

// publicURLRoutes returns the two public routes that a server with no console
// sign-in refuses.
func publicURLRoutes() []hostileCase {
	return []hostileCase{
		{"redeem", "/api/dustpan/v1/local-users/redeem", `{"code":"abc","password":"x"}`, "application/json", ""},
		{"sign in", "/api/dustpan/v1/sign-in/password", `{"username":"alice","password":"x"}`, "application/json", ""},
	}
}

// deniedPublicURLEntries returns the denied sign-in entries of the scope
// @server that name the missing public URL.
func deniedPublicURLEntries(t *testing.T, f accessFixture) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	entries := serverEntries(t, f.store)
	for i := range entries {
		e := entries[i]
		if e.Outcome == audit.OutcomeDenied && e.Action == session.ActionSignIn &&
			strings.Contains(e.Reason, "without a usable public URL") {
			out = append(out, e)
		}
	}

	return out
}

// TestNoPublicURLWritesADeniedEntry proves that each refused request writes
// its denied entry, so an operator can ask why a sign-in failed. Each route
// gets its own handler, so one entry for each refused request is the answer.
// The entry holds the subject of the password sign-in, and the secret of the
// request is not in it.
func TestNoPublicURLWritesADeniedEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range publicURLRoutes() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newNoPublicURLFixture(t)

			rec := sendHostile(t, f, tc)
			wantPublicURLAnswer(t, rec, "never-in-the-body")

			got := deniedPublicURLEntries(t, f)
			if len(got) != 1 {
				t.Fatalf("denied entries = %d, want 1: %+v", len(got), got)
			}

			if got[0].Subject != "sign-in:local" || strings.Contains(got[0].Reason, tc.body) {
				t.Fatalf("entry = %+v, want the subject sign-in:local and no body", got[0])
			}
		})
	}
}

// TestNoPublicURLAuditIsBounded proves that a flood of refused requests
// writes a bounded number of entries: the recorder keeps one entry for each
// subject and window, so the caller cannot fill the audit log.
func TestNoPublicURLAuditIsBounded(t *testing.T) {
	t.Parallel()

	f := newNoPublicURLFixture(t)

	for range 50 {
		for _, tc := range publicURLRoutes() {
			if rec := sendHostile(t, f, tc); rec.Code != http.StatusForbidden {
				t.Fatalf("%s = %d, want 403", tc.name, rec.Code)
			}
		}
	}

	if got := deniedPublicURLEntries(t, f); len(got) != 1 {
		t.Fatalf("denied entries after 100 refusals = %d, want 1", len(got))
	}
}
