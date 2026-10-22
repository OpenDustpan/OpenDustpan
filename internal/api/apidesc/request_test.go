package apidesc_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// TestRequestHeadersAreTheListed pins the list of the request headers and of
// the cookies that a handler may read. A change of the list shows in this
// test, so a reviewer sees each header that joins the allowlist, with the part
// of the description that holds it.
func TestRequestHeadersAreTheListed(t *testing.T) {
	t.Parallel()

	wantHeaders := map[string]string{
		"Authorization":   "the security scheme bearerAuth",
		"Content-Type":    "the media type of the request body",
		"Idempotency-Key": "the shared parameter IdempotencyKey",
		"If-Match":        "the shared parameters IfMatch and IfMatchRequired",
		"X-Dustpan-Csrf":  "the security scheme csrfHeader",
		"Dustpan-Channel": "not described: the audit record only",
		"User-Agent":      "not described: the audit record only",
		"Origin":          "not described: set by the browser; the server checks it against the allowed origins",
		"Sec-Fetch-Site":  "not described: set by the browser; the server refuses a cross-site request with a session",
		"Forwarded":       "not described: set by the proxy that the server trusts, for the address of the client",
		"X-Forwarded-For": "not described: set by the proxy that the server trusts, for the address of the client",
	}
	if got := apidesc.RequestHeaders(); !maps.Equal(got, wantHeaders) {
		t.Errorf("RequestHeaders() = %v, want %v", got, wantHeaders)
	}

	wantCookies := map[string]string{"__Host-dustpan_session": "the security scheme sessionCookie"}
	if got := apidesc.RequestCookies(); !maps.Equal(got, wantCookies) {
		t.Errorf("RequestCookies() = %v, want %v", got, wantCookies)
	}
}

// TestDocumentDescribesTheSession proves that the description holds the
// session cookie and the CSRF header, so that a client made from it can send a
// write with a session of the console.
func TestDocumentDescribesTheSession(t *testing.T) {
	t.Parallel()

	doc := string(apidesc.Document())
	for _, want := range []string{
		`"sessionCookie"`, `"in": "cookie"`, `"name": "__Host-dustpan_session"`,
		`"csrfHeader"`, `"in": "header"`, `"name": "X-Dustpan-Csrf"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the description does not hold %s", want)
		}
	}
}

// TestHeaderAndCookieReads proves the reads of the listed names, and that a
// name that is not listed panics in a test.
func TestHeaderAndCookieReads(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("Origin", "https://console.example")
	r.Header.Add("X-Forwarded-For", "192.0.2.1")
	r.Header.Add("X-Forwarded-For", "192.0.2.2")
	r.AddCookie(&http.Cookie{
		Name: apidesc.SessionCookie, Value: "secret", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})

	if got := apidesc.Header(r, "Origin"); got != "https://console.example" {
		t.Errorf("Header(Origin) = %q", got)
	}

	if got := apidesc.HeaderValues(r, "x-forwarded-for"); len(got) != 2 {
		t.Errorf("HeaderValues(x-forwarded-for) = %q, want two lines", got)
	}

	if got, ok := apidesc.Cookie(r, apidesc.SessionCookie); !ok || got != "secret" {
		t.Errorf("Cookie() = %q, %v", got, ok)
	}

	for name, read := range map[string]func(){
		"Header":       func() { apidesc.Header(r, "X-Evil") },
		"HeaderValues": func() { apidesc.HeaderValues(r, "X-Evil") },
		"Cookie":       func() { apidesc.Cookie(r, "evil") },
	} {
		func() {
			defer func() {
				v := recover()
				if v == nil || !strings.Contains(fmt.Sprint(v), apidesc.FixCommand) {
					t.Errorf("%s of an unlisted name: recover() = %v, want the fix command", name, v)
				}
			}()

			read()
		}()
	}
}

// TestUndeclaredReadInTheServer proves that in the server an undeclared name is
// absent, and does not panic: Get gives "", Has gives false and All gives nil.
func TestUndeclaredReadInTheServer(t *testing.T) {
	t.Parallel()

	labels := thingLabels()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/things?evil=1", http.NoBody,
	)
	r.Pattern = "GET " + listPath

	q := apidesc.QueryOfServer(r, labels)
	if q.Get("evil") != "" || q.Has("evil") || q.All("evil") != nil {
		t.Errorf("an undeclared name is present in the server: Get = %q, Has = %v, All = %v",
			q.Get("evil"), q.Has("evil"), q.All("evil"))
	}

	// A route under the prefix with no label reads no name.
	r = httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/unlabeled?pageSize=5", http.NoBody,
	)
	r.Pattern = "GET /api/dustpan/v1/namespaces/{namespace}/unlabeled"

	if got := apidesc.QueryOfServer(r, labels).Get("pageSize"); got != "" {
		t.Errorf("a route with no label gave %q, want no value", got)
	}
}

// TestUndeclaredReadIsLoggedOnce proves that the server logs a read of a name
// that the description does not hold once for each route and name, with the
// route, the name and the fix command.
func TestUndeclaredReadIsLoggedOnce(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	report := apidesc.NewMisuseLog(slog.New(slog.NewTextHandler(&buf, nil)))

	for range 3 {
		report.ReportInServer("query parameter", "GET /api/dustpan/v1/things", "evil")
	}

	report.ReportInServer("query parameter", "GET /api/dustpan/v1/things", "other")

	out := buf.String()
	if got := strings.Count(out, "name=evil"); got != 1 {
		t.Errorf("the log holds name=evil %d times, want once:\n%s", got, out)
	}

	for _, want := range []string{"name=other", "level=ERROR", "GET /api/dustpan/v1/things", apidesc.FixCommand} {
		if !strings.Contains(out, want) {
			t.Errorf("the log does not hold %q:\n%s", want, out)
		}
	}
}
