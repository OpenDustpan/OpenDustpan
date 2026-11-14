package sessionauth_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// TestSignInFromBodyRefusesAnotherSite sends each request that a page of
// another site can send without a CORS preflight, and each request of the
// console and of a client that is not a browser, to the check against login
// CSRF directly.
func TestSignInFromBodyRefusesAnotherSite(t *testing.T) {
	t.Parallel()

	const (
		json  = "application/json"
		evil  = "https://evil.example"
		cross = "cross-site"
	)

	cases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"a form of text/plain", map[string]string{"Content-Type": "text/plain"}, false},
		{"a form of text/plain with a charset", map[string]string{"Content-Type": "text/plain; charset=utf-8"}, false},
		{"a URL-encoded form", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, false},
		{"a multipart form", map[string]string{"Content-Type": "multipart/form-data; boundary=x"}, false},
		{"no media type", map[string]string{}, false},
		{"a media type that does not parse", map[string]string{"Content-Type": "application/json; ="}, false},
		{"a JSON look-alike", map[string]string{"Content-Type": "application/json-seq"}, false},
		{"JSON from a foreign origin", map[string]string{"Content-Type": json, "Origin": evil}, false},
		{"JSON from the null origin", map[string]string{"Content-Type": json, "Origin": "null"}, false},
		{"JSON from an origin with another port", map[string]string{
			"Content-Type": json, "Origin": consoleOrigin + ":8443",
		}, false},
		{"JSON marked cross-site", map[string]string{"Content-Type": json, "Sec-Fetch-Site": cross}, false},
		{"JSON from the console marked cross-site", map[string]string{
			"Content-Type": json, "Origin": consoleOrigin, "Sec-Fetch-Site": cross,
		}, false},
		{"text/plain from the console", map[string]string{
			"Content-Type": "text/plain", "Origin": consoleOrigin, "Sec-Fetch-Site": "same-origin",
		}, false},
		{"JSON from the console", map[string]string{
			"Content-Type": json, "Origin": consoleOrigin, "Sec-Fetch-Site": "same-origin",
		}, true},
		{"JSON with a charset from the console", map[string]string{
			"Content-Type": "application/json; charset=utf-8", "Origin": consoleOrigin,
		}, true},
		{"JSON marked same-origin with no origin", map[string]string{
			"Content-Type": json, "Sec-Fetch-Site": "same-origin",
		}, true},
		{"JSON of a client that is not a browser", map[string]string{"Content-Type": json}, true},
		{"JSON in upper case", map[string]string{"Content-Type": "APPLICATION/JSON"}, true},
	}

	var none session.Match

	c := newChecker(t, &fakeSessions{err: nil, match: none, calls: 0})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := request(t, http.MethodPost, "", tc.headers)
			if got := c.SignInFromBody(r); got != tc.want {
				t.Fatalf("SignInFromBody = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestSignInFromBodyReadsTheFirstMediaType pins the result for a request with
// two Content-Type headers: the check reads the first one. A browser cannot
// send two, because fetch joins them into one value that does not parse, so
// only a client that is not a browser can send this request.
func TestSignInFromBodyReadsTheFirstMediaType(t *testing.T) {
	t.Parallel()

	var none session.Match

	c := newChecker(t, &fakeSessions{err: nil, match: none, calls: 0})

	for _, tc := range []struct {
		name  string
		types []string
		want  bool
	}{
		{name: "JSON first", types: []string{"application/json", "text/plain"}, want: true},
		{name: "text/plain first", types: []string{"text/plain", "application/json"}, want: false},
		{name: "joined by fetch", types: []string{"application/json, text/plain"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := request(t, http.MethodPost, "", nil)
			for _, v := range tc.types {
				r.Header.Add("Content-Type", v)
			}

			if got := c.SignInFromBody(r); got != tc.want {
				t.Fatalf("SignInFromBody with %q = %t, want %t", tc.types, got, tc.want)
			}
		})
	}
}
