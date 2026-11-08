package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
