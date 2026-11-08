package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
)

// signInOriginCase is one request to the password sign-in of a local user,
// with the headers that a page of another site, the console or a client that
// is not a browser sends.
type signInOriginCase struct {
	headers map[string]string
	name    string
	body    string
	want    int
}

// signInOriginCases returns the requests of TestPasswordSignInRefusesAnotherSite.
// Each hostile request carries the right password of carol.
func signInOriginCases() []signInOriginCase {
	const evil = "https://evil.example"

	// A page of another site can send this exact body as text/plain with
	// fetch in the mode no-cors, with no CORS preflight.
	jsonBody := `{"userName":"carol","password":"` + routePassword + `"}`

	return []signInOriginCase{
		{
			name: "a text/plain request of another site",
			body: jsonBody,
			want: http.StatusUnauthorized,
			headers: map[string]string{
				"Content-Type": "text/plain", "Origin": evil, "Sec-Fetch-Site": "cross-site",
			},
		},
		{name: "a URL-encoded form", body: jsonBody, want: http.StatusUnauthorized, headers: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
		}},
		{name: "a multipart form", body: jsonBody, want: http.StatusUnauthorized, headers: map[string]string{
			"Content-Type": "multipart/form-data; boundary=x",
		}},
		{name: "no media type", body: jsonBody, want: http.StatusUnauthorized, headers: map[string]string{}},
		{name: "JSON from a foreign origin", body: jsonBody, want: http.StatusUnauthorized, headers: map[string]string{
			"Content-Type": "application/json", "Origin": evil,
		}},
		{name: "JSON from the null origin", body: jsonBody, want: http.StatusUnauthorized, headers: map[string]string{
			"Content-Type": "application/json", "Origin": "null",
		}},
		{name: "JSON marked cross-site", body: jsonBody, want: http.StatusUnauthorized, headers: map[string]string{
			"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site",
		}},
		{name: "JSON from the console", body: jsonBody, want: http.StatusNoContent, headers: map[string]string{
			"Content-Type": "application/json", "Origin": signInBase, "Sec-Fetch-Site": "same-origin",
		}},
		{
			name: "JSON of a client that is not a browser", body: jsonBody, want: http.StatusNoContent,
			headers: map[string]string{"Content-Type": "application/json"},
		},
	}
}

// TestPasswordSignInRefusesAnotherSite proves the check against login CSRF at
// the route of the password sign-in of a local user: a request that another
// site can send gets the answer of a failed sign-in, no cookie, and spends no
// unit of the password budget, also with the right password. A request of
// the console or of a client that is not a browser signs in.
func TestPasswordSignInRefusesAnotherSite(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createRouteLocalUser(t, f)

	for _, tc := range signInOriginCases() {
		before := f.passwords.count()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/sign-in/password",
			strings.NewReader(tc.body))
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}

		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Fatalf("%s: answer = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
		}

		if tc.want != http.StatusUnauthorized {
			continue
		}

		if code := errorCodeOf(t, rec.Body.String()); code != "local-user.sign-in-failed" {
			t.Fatalf("%s: code = %q, want the code of a failed sign-in", tc.name, code)
		}

		if len(rec.Result().Cookies()) != 0 || f.passwords.count() != before {
			t.Fatalf("%s: the refusal set a cookie or spent the budget (%d to %d)", tc.name, before,
				f.passwords.count())
		}
	}
}

// TestPasswordSignInOfAnotherSiteIsAudited proves that each refusal of the
// check against login CSRF writes one denied audit entry of the installation:
// the action of a sign-in, the sign-in route as the subject, and the reason.
// Each row has a fresh handler, so the limiter of the denial recorder does not
// count one row in the window of another. A sign-in of the console writes no
// denied entry.
func TestPasswordSignInOfAnotherSiteIsAudited(t *testing.T) {
	t.Parallel()

	for _, tc := range signInOriginCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			createRouteLocalUser(t, f)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/sign-in/password",
				strings.NewReader(tc.body))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			f.handler.ServeHTTP(httptest.NewRecorder(), req)

			want := 0
			if tc.want == http.StatusUnauthorized {
				want = 1
			}

			if got := otherSiteEntries(t, f); got != want {
				t.Fatalf("denied sign-in entries = %d, want %d", got, want)
			}
		})
	}
}

// otherSiteEntries counts the denied entries of a sign-in of another site in
// the installation scope of f.
func otherSiteEntries(t *testing.T, f accessFixture) int {
	t.Helper()

	n := 0

	entries := serverEntries(t, f.store)
	for i := range entries {
		e := &entries[i]
		if e.Outcome == audit.OutcomeDenied && e.Action == session.ActionSignIn && e.Subject == "sign-in:local" &&
			strings.Contains(e.Reason, "another site") {
			n++
		}
	}

	return n
}
