package proxy_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// hostileRequest is a request that carries values an attacker would send.
func hostileRequest(e *env) string {
	return "GET /v1/x?a=b&token=evil&%74oken=evil2 HTTP/1.1\r\n" + hostHeader(e) +
		"Authorization: Bearer attacker\r\nauthorization: attacker2\r\nCookie: session=attacker\r\n" +
		"Proxy-Authorization: Basic attacker\r\nX-Api-Key: attacker\r\nx-api-key: attacker2\r\n\r\n"
}

// checkInjected checks the request that the host got for the credential name.
func checkInjected(t *testing.T, r seen, name string) {
	t.Helper()

	for _, h := range []string{"Cookie", "Proxy-Authorization"} {
		if v := r.Header.Values(h); len(v) != 0 {
			t.Errorf("%s = %q, want none", h, v)
		}
	}

	for h, vs := range r.Header {
		// Only a credential that sets X-Api-Key replaces the X-Api-Key of the client.
		if h == "X-Api-Key" && name != "X-Api-Key" {
			continue
		}

		for _, v := range vs {
			if strings.Contains(v, "attacker") {
				t.Errorf("header %s = %q holds a value of the client", h, v)
			}
		}
	}
}

// TestInjectionHeader is the injection layer on its own for a header
// credential: the headers of the client that could carry a credential are
// deleted, the credential is added, and no record holds it.
func TestInjectionHeader(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"X-Api-Key", "Authorization"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			value := probeValue
			if name == "Authorization" {
				value = "Bearer " + probeValue
			}

			e := newEnv(t, withCreds(creds{"tok": hdr(name, value)}))
			c, br := e.rawTLS(t)

			if rep := rawRequest(t, c, br, hostileRequest(e)); rep.status != http.StatusOK {
				t.Fatalf("status %d", rep.status)
			}

			got := e.up.requests()
			if len(got) != 1 {
				t.Fatalf("the host got %d requests", len(got))
			}

			checkInjected(t, got[0], name)

			if v := got[0].Header.Values(name); len(v) != 1 || v[0] != value {
				t.Errorf("%s = %q", name, v)
			}

			assertNoProbe(t, "a record", fmt.Sprintf("%+v", e.sink.waitFor(t, 1)))
		})
	}
}

// TestInjectionBasic is the injection layer on its own for a basic
// credential.
func TestInjectionBasic(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withCreds(creds{"tok": basicCred("svc", probeValue)}))
	c, br := e.rawTLS(t)

	if rep := rawRequest(t, c, br, hostileRequest(e)); rep.status != http.StatusOK {
		t.Fatalf("status %d", rep.status)
	}

	got := e.up.requests()
	if len(got) != 1 {
		t.Fatalf("the host got %d requests", len(got))
	}

	checkInjected(t, got[0], "Authorization")

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("svc:"+probeValue))
	if v := got[0].Header.Values("Authorization"); len(v) != 1 || v[0] != want {
		t.Errorf("Authorization = %q", v)
	}

	assertNoProbe(t, "a record", fmt.Sprintf("%+v", e.sink.waitFor(t, 1)))
}

// TestInjectionQuery is the injection layer on its own for a query
// credential: a parameter of the client with the same name does not stay.
func TestInjectionQuery(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withCreds(creds{"tok": queryCred("token", probeValue)}))
	c, br := e.rawTLS(t)

	if rep := rawRequest(t, c, br, hostileRequest(e)); rep.status != http.StatusOK {
		t.Fatalf("status %d", rep.status)
	}

	got := e.up.requests()
	if len(got) != 1 {
		t.Fatalf("the host got %d requests", len(got))
	}

	checkInjected(t, got[0], "Authorization")

	u, err := url.Parse(got[0].URI)
	if err != nil {
		t.Fatal(err)
	}

	if v := u.Query()["token"]; len(v) != 1 || v[0] != probeValue {
		t.Errorf("token = %q in %q", v, got[0].URI)
	}

	if v := u.Query().Get("a"); v != "b" {
		t.Errorf("the other parameter was lost: %q", got[0].URI)
	}
}

// TestInvalidCredentialIsRefused checks that a credential that could split a
// header is refused, and nothing reaches the host.
func TestInvalidCredentialIsRefused(t *testing.T) {
	t.Parallel()

	bad := map[string]proxy.Credential{
		"CRLF in value":  hdr("X-Api-Key", "a\r\nX-Evil: 1"),
		"LF in value":    hdr("X-Api-Key", "a\nb"),
		"NUL in value":   hdr("X-Api-Key", "a\x00b"),
		"space in name":  hdr("X Api", "v"),
		"colon in name":  hdr("X:Api", "v"),
		"CRLF in name":   hdr("X-A\r\nB", "v"),
		"empty value":    hdr("X-Api-Key", ""),
		"colon in user":  basicCred("a:b", "p"),
		"CRLF in pass":   basicCred("a", "p\r\nX: 1"),
		"query amp name": queryCred("a&b", "v"),
		"unknown kind":   {Kind: 0, Name: "X-Api-Key", Value: "v", User: "", Password: "", Certificate: nil},
		"cert as request": {
			Kind:        proxy.KindClientCertificate,
			Name:        "",
			Value:       "",
			User:        "",
			Password:    "",
			Certificate: nil,
		},
	}

	for name, cred := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, withCreds(creds{"tok": cred}))
			rep := e.get(t, http1, "/v1/x")

			if rep.status != http.StatusBadGateway {
				t.Errorf("status %d, want 502", rep.status)
			}

			if rec := e.sink.lastReq(t, 1); rec.Reason != proxy.ReasonAuthUnavailable {
				t.Errorf("record %+v, want credential-unavailable", rec)
			}

			if got := e.up.requests(); len(got) != 0 {
				t.Errorf("the host got %d requests", len(got))
			}
		})
	}
}

// TestMissingCredentialSource checks that a rule with a credential and no
// source fails closed, and the error text of a source is not kept.
func TestMissingCredentialSource(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withCreds(creds{"other": hdr("X", "v")}))
	rep := e.get(t, http1, "/v1/x")

	if rep.status != http.StatusBadGateway {
		t.Errorf("status %d, want 502", rep.status)
	}

	assertNoProbe(t, "the body", rep.body)
	assertNoProbe(t, "the records", fmt.Sprintf("%+v", e.sink.waitFor(t, 1)))
}

// TestRedirectIsNotFollowed checks that a 302 goes back to the client and the
// proxy sends no second request.
func TestRedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/v1/other")
		w.WriteHeader(http.StatusFound)
	}))

	rep := e.get(t, http1, "/v1/x")
	if rep.status != http.StatusFound || rep.header.Get("Location") != "/v1/other" {
		t.Errorf("status %d, Location %q", rep.status, rep.header.Get("Location"))
	}

	if got := e.up.requests(); len(got) != 1 {
		t.Errorf("the host got %d requests, want 1", len(got))
	}
}

// TestUpstreamHTTP2 checks that the proxy speaks HTTP/2 to a host that offers
// it, whatever the client speaks.
func TestUpstreamHTTP2(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	for _, proto := range []protocol{http1, http2} {
		if rep := e.get(t, proto, "/v1/x"); rep.status != http.StatusOK {
			t.Fatalf("protocol %d: status %d", proto, rep.status)
		}
	}

	for _, r := range e.up.requests() {
		if r.Proto != "HTTP/2.0" {
			t.Errorf("the host got %s, want HTTP/2.0", r.Proto)
		}
	}
}

// TestHopByHopIsNotForwarded checks the headers that name one connection.
func TestHopByHopIsNotForwarded(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	c, br := e.rawTLS(t)

	req := "GET /v1/x HTTP/1.1\r\n" + hostHeader(e) +
		"Connection: keep-alive, X-Secret-Hop\r\nX-Secret-Hop: 1\r\nKeep-Alive: 5\r\nUpgrade: websocket\r\nTE: trailers\r\n\r\n"
	if rep := rawRequest(t, c, br, req); rep.status != http.StatusOK {
		t.Fatalf("status %d", rep.status)
	}

	r := e.up.requests()[0]
	for _, h := range []string{"X-Secret-Hop", "Keep-Alive", "Upgrade", "Te"} {
		if v := r.Header.Values(h); len(v) != 0 {
			t.Errorf("%s = %q reached the host", h, v)
		}
	}
}

// TestEndpointMode checks the loopback endpoint: injection, name binding, and
// no CONNECT for an endpoint host.
func TestEndpointMode(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withRules(endpointRules))
	l := listenLoopback(t)
	done := make(chan error, 1)

	go func() { done <- e.p.ServeEndpoint(l, testHost, e.up.port) }()

	c := &http.Client{Transport: &http.Transport{}, Timeout: 2 * ioWait, Jar: nil, CheckRedirect: nil}
	t.Cleanup(c.CloseIdleConnections)

	target := "http://" + l.Addr().String() + "/v1/x"

	rep := getVia(t, c, target, testHost+":"+portOf(e))
	if rep.status != http.StatusOK {
		t.Fatalf("status %d", rep.status)
	}

	if got := e.up.requests()[0].Header.Get("X-Api-Key"); got != probeValue {
		t.Error("the credential did not reach the host")
	}

	if rep := getVia(t, c, target, "other.example"); rep.status != http.StatusMisdirectedRequest {
		t.Errorf("status %d, want 421", rep.status)
	}

	if _, _, status := e.connectRaw(t, testHost+":"+portOf(e)); status != http.StatusForbidden {
		t.Errorf("CONNECT to an endpoint host: status %d, want 403", status)
	}

	if err := e.p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if err := <-done; err != nil {
		t.Errorf("ServeEndpoint: %v", err)
	}
}
