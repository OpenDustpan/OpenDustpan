package proxy_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// TestPathLayer is the path layer on its own. The request goes through the
// real TLS end, with the bytes of the test, so the proxy sees the raw path.
func TestPathLayer(t *testing.T) {
	t.Parallel()

	hostile := []string{
		"/v1/../admin",
		"/v1/%2e%2e/admin",
		"/v1/%2E%2E/admin",
		"/v1/a%2Fb",
		"/v1/a%5Cb",
		"//v1/x",
		"/v1//x",
		"/v1/x;y=1",
		"/v1/x/",
		"/v1/./x",
		"/v1/a%00b",
		"/v1/a%2fb",
	}

	e := newEnv(t)

	for i, p := range hostile {
		// A refusal closes the connection, so each path gets its own.
		c, br := e.rawTLS(t)

		rep := rawRequest(t, c, br, "GET "+p+" HTTP/1.1\r\n"+hostHeader(e)+"\r\n")
		if rep.status != http.StatusBadRequest {
			t.Errorf("path %q: status %d, want 400", p, rep.status)
		}

		rec := e.sink.lastReq(t, i+1)
		if rec.Reason != proxy.ReasonPathNotCanonical || rec.Allowed || rec.Path != "" {
			t.Errorf("path %q: record %+v, want a path-not-canonical denial with no path", p, rec)
		}
	}

	if got := e.up.requests(); len(got) != 0 {
		t.Errorf("the host got %d requests for hostile paths: %+v", len(got), got)
	}
}

// TestCanonicalPathPassesUnchanged checks that a canonical path and its query
// reach the host byte for byte.
func TestCanonicalPathPassesUnchanged(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	c, br := e.rawTLS(t)

	targets := []string{"/v1/a%20b", "/v1/caf%C3%A9", "/v1/x?q=1&r=%2F%2e%2e", "/v1/x?", "/v1/a!b"}

	for _, p := range targets {
		rep := rawRequest(t, c, br, "GET "+p+" HTTP/1.1\r\n"+hostHeader(e)+"\r\n")
		if rep.status != http.StatusOK {
			t.Fatalf("path %q: status %d", p, rep.status)
		}
	}

	got := e.up.requests()
	if len(got) != len(targets) {
		t.Fatalf("the host got %d requests, want %d", len(got), len(targets))
	}

	for i, p := range targets {
		if got[i].URI != p {
			t.Errorf("the host got %q, want %q", got[i].URI, p)
		}
	}
}

// TestNoRuleMatchedBody checks the denial body of the spec.
func TestNoRuleMatchedBody(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	rep := e.get(t, http1, "/admin")

	if rep.status != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rep.status)
	}

	want := "blocked by the job proxy: GET " + testHost + "/admin: no rule matched"
	if rep.body != want {
		t.Errorf("body %q, want %q", rep.body, want)
	}

	if rec := e.sink.lastReq(t, 1); rec.Reason != proxy.ReasonNoRuleMatched || rec.Path != "/admin" {
		t.Errorf("record %+v", rec)
	}
}

// smugglingAttacks returns raw requests that attack the framing. The key is
// the name of the attack.
func smugglingAttacks(e *env) map[string]string {
	inner := "GET /admin HTTP/1.1\r\n" + hostHeader(e) + "\r\n"
	n := strconv.Itoa(len(inner))

	return map[string]string{
		"CL and TE": "POST /v1/x HTTP/1.1\r\n" + hostHeader(e) +
			"Content-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n" + inner,
		"TE obfuscated": "POST /v1/x HTTP/1.1\r\n" + hostHeader(e) +
			"Transfer-Encoding: xchunked\r\nContent-Length: 4\r\n\r\n0\r\n\r\n" + inner,
		"TE with space": "POST /v1/x HTTP/1.1\r\n" + hostHeader(e) +
			"Transfer-Encoding : chunked\r\n\r\n0\r\n\r\n" + inner,
		"two content lengths": "POST /v1/x HTTP/1.1\r\n" + hostHeader(e) +
			"Content-Length: 0\r\nContent-Length: " + n + "\r\n\r\n" + inner,
		"body holds a request": "POST /v1/x HTTP/1.1\r\n" + hostHeader(e) +
			"Content-Length: " + n + "\r\n\r\n" + inner,
		"bare LF": "GET /v1/x HTTP/1.1\n" + hostHeader(e) + "\n" + inner,
	}
}

// TestSmuggling sends framing attacks. Whatever the proxy answers, the host
// must never get a request for a path that no rule allows, and it must get
// no more requests than the records show as allowed.
func TestSmuggling(t *testing.T) {
	t.Parallel()

	names := []string{
		"CL and TE", "TE obfuscated", "TE with space", "two content lengths", "body holds a request", "bare LF",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			c, br := e.rawTLS(t)

			// The proxy may close the connection at once.
			if !trySend(c, smugglingAttacks(e)[name]) {
				t.Log("the proxy closed the connection")
			}

			for range 3 {
				if _, ok := readReply(br); !ok {
					break
				}
			}

			assertNoSmuggledRequest(t, e)
		})
	}
}

// assertNoSmuggledRequest checks the host and the records after an attack.
func assertNoSmuggledRequest(t *testing.T, e *env) {
	t.Helper()

	got := e.up.requests()
	for _, r := range got {
		if strings.HasPrefix(r.URI, "/admin") {
			t.Fatalf("the host got a smuggled request: %+v", r)
		}
	}

	allowed := 0

	recs := e.sink.all()
	for i := range recs {
		if recs[i].Allowed && recs[i].Method != http.MethodConnect {
			allowed++
		}
	}

	// A request may reach the host before its record reaches the sink, so one
	// request of slack is allowed.
	if len(got) > allowed+1 {
		t.Errorf("the host got %d requests, the records show %d allowed", len(got), allowed)
	}
}

// TestHeaderInjection checks that a header that the client sends cannot
// reach the host as a second header or a second request line.
func TestHeaderInjection(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	c, br := e.rawTLS(t)

	raw := "GET /v1/x HTTP/1.1\r\n" + hostHeader(e) +
		"X-Evil: a\r\nX-Api-Key: attacker\r\nx-api-key: attacker2\r\nX-Bad\r\n: nameless\r\n\r\n"
	if !trySend(c, raw) {
		t.Log("the proxy closed the connection")
	}

	if _, ok := readReply(br); !ok {
		t.Log("no valid response")
	}

	for _, r := range e.up.requests() {
		if got := r.Header.Values("X-Api-Key"); len(got) != 1 || got[0] != probeValue {
			t.Errorf("X-Api-Key at the host: %q, want only the credential", got)
		}
	}
}

// TestOversizedBodyContentLength checks the size limit with a Content-Length.
func TestOversizedBodyContentLength(t *testing.T) {
	t.Parallel()

	limits := noLimits()
	limits.MaxRequestBody = 1024
	e := newEnv(t, withLimits(limits))
	c, br := e.rawTLS(t)

	rep := rawRequest(t, c, br, "POST /v1/x HTTP/1.1\r\n"+hostHeader(e)+"Content-Length: 4096\r\n\r\n")
	if rep.status != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", rep.status)
	}
}

// TestOversizedBodyChunked checks the size limit with a chunked body, which
// has no length to check up front.
func TestOversizedBodyChunked(t *testing.T) {
	t.Parallel()

	limits := noLimits()
	limits.MaxRequestBody = 1024
	e := newEnv(t, withLimits(limits))
	c, br := e.rawTLS(t)

	body := strings.Repeat("a", 4096)
	req := "POST /v1/x HTTP/1.1\r\n" + hostHeader(e) + "Transfer-Encoding: chunked\r\n\r\n" +
		strconv.FormatInt(int64(len(body)), 16) + "\r\n" + body + "\r\n0\r\n\r\n"

	if !trySend(c, req) {
		t.Log("the proxy closed the connection")
	}

	if rep, ok := readReply(br); ok && rep.status < http.StatusBadRequest {
		t.Errorf("status %d, want an error", rep.status)
	}

	for _, r := range e.up.requests() {
		if len(r.Body) >= len(body) {
			t.Error("the host got the whole oversized body")
		}
	}
}

// TestSchemeAndFormConfusion sends plain proxy requests in the wrong form.
func TestSchemeAndFormConfusion(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	port := portOf(e)

	tests := []struct {
		name   string
		target string
		status int
		reason proxy.Reason
	}{
		{
			"https absolute form",
			"https://" + testHost + ":" + port + "/v1/x",
			http.StatusBadRequest,
			proxy.ReasonBadRequest,
		},
		{"origin form", "/v1/x", http.StatusBadRequest, proxy.ReasonBadRequest},
		{
			"plain http with a credential",
			"http://" + testHost + ":" + port + "/v1/x",
			http.StatusForbidden,
			proxy.ReasonAuthOverHTTP,
		},
		{"ip literal host", "http://127.0.0.1:" + port + "/v1/x", http.StatusBadRequest, proxy.ReasonBadTarget},
		{"no path", "http://" + testHost + ":" + port, http.StatusBadRequest, proxy.ReasonBadTarget},
		{"upper case host", "http://API.example.com:" + port + "/v1/x", http.StatusBadRequest, proxy.ReasonBadTarget},
	}

	for i, tt := range tests {
		if got := plainStatus(t, e, tt.target); got != tt.status {
			t.Errorf("%s: status %d, want %d", tt.name, got, tt.status)
		}

		if rec := e.sink.waitFor(t, i+1)[i]; rec.Reason != tt.reason || rec.Allowed {
			t.Errorf("%s: record %+v, want reason %q", tt.name, rec, tt.reason)
		}
	}

	if got := e.up.requests(); len(got) != 0 {
		t.Errorf("the host got %d requests", len(got))
	}
}
