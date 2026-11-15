package proxy_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// TestParseConnectTarget is the boundary layer on its own: the parser.
func TestParseConnectTarget(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 64) + ".example.com:443"

	tests := []struct {
		name   string
		target string
		ok     bool
	}{
		{name: "valid", target: "api.example.com:443", ok: true},
		{name: "valid high port", target: "api.example.com:65535", ok: true},
		{name: "ipv4 literal", target: "127.0.0.1:443", ok: false},
		{name: "ipv4 short form", target: "127.1:443", ok: false},
		{name: "ipv6 literal", target: "[::1]:443", ok: false},
		{name: "port zero", target: "api.example.com:0", ok: false},
		{name: "port too high", target: "api.example.com:65536", ok: false},
		{name: "port leading zero", target: "api.example.com:0443", ok: false},
		{name: "port sign", target: "api.example.com:+443", ok: false},
		{name: "port empty", target: "api.example.com:", ok: false},
		{name: "no port", target: "api.example.com", ok: false},
		{name: "trailing dot", target: "api.example.com.:443", ok: false},
		{name: "upper case", target: "API.example.com:443", ok: false},
		{name: "user info", target: "user@api.example.com:443", ok: false},
		{name: "space", target: "api.example.com :443", ok: false},
		{name: "one label", target: "localhost:443", ok: false},
		{name: "empty label", target: "api..example.com:443", ok: false},
		{name: "label too long", target: long, ok: false},
		{name: "leading hyphen", target: "-api.example.com:443", ok: false},
		{name: "non ASCII", target: "apï.example.com:443", ok: false},
		{name: "empty", target: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, port, err := proxy.ParseConnectTarget(tt.target)
			if (err == nil) != tt.ok {
				t.Fatalf("ParseConnectTarget(%q) = %q, %d, %v; ok want %v", tt.target, host, port, err, tt.ok)
			}
		})
	}
}

// TestConnectBoundary sends hostile CONNECT targets to the proxy itself. The
// parser is not the only layer here: each target must also leave no request
// at the host and write a fixed reason.
func TestConnectBoundary(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	port := portOf(e)

	targets := []string{
		"127.0.0.1:" + port,
		testHost + ":0",
		testHost + ".:" + port,
		strings.ToUpper(testHost) + ":" + port,
		"[::1]:" + port,
	}

	for i, target := range targets {
		_, _, status := e.connectRaw(t, target)
		if status != http.StatusBadRequest {
			t.Errorf("CONNECT %q: status %d, want 400", target, status)
		}

		rec := e.sink.waitFor(t, i+1)[i]
		if rec.Allowed || rec.Reason != proxy.ReasonBadTarget || rec.Host != "" {
			t.Errorf("CONNECT %q: record %+v, want a bad-target denial with no host", target, rec)
		}
	}

	if got := e.up.requests(); len(got) != 0 {
		t.Errorf("the host got %d requests", len(got))
	}
}

// TestHugeRequestLine sends a 70 KiB request line. The server limit refuses
// it before any handler runs, and nothing reaches the host.
func TestHugeRequestLine(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	c := dialRaw(t, e.addr)

	line := "CONNECT " + strings.Repeat("a", 70<<10) + ".example.com:443 HTTP/1.1\r\nHost: x\r\n\r\n"
	if !trySend(c, line) {
		t.Log("the proxy closed the connection before the end of the line")
	}

	rep, _ := readReply(newReader(c))
	if rep.status != 0 && rep.status != http.StatusRequestHeaderFieldsTooLarge && rep.status != http.StatusBadRequest {
		t.Errorf("status %d, want a refusal", rep.status)
	}

	if got := e.up.requests(); len(got) != 0 {
		t.Errorf("the host got %d requests", len(got))
	}

	for _, r := range e.sink.all() {
		if r.Allowed {
			t.Errorf("an oversized request line was allowed: %+v", r)
		}
	}
}

// TestSNIBinding is the name layer on its own: the SNI must be the host of the
// CONNECT.
func TestSNIBinding(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	c, br, status := e.connectRaw(t, testHost+":"+portOf(e))

	if status != http.StatusOK {
		t.Fatalf("CONNECT status %d", status)
	}

	hs := handshake(t, &bufferedConn{Conn: c, br: br}, e.caPool(), "other.example")
	if hs == nil {
		t.Fatal("the handshake with another SNI worked")
	}

	rec := e.sink.waitFor(t, 1)[0]
	if rec.Reason != proxy.ReasonHostMismatch || rec.Allowed || rec.Method != "TLS" {
		t.Errorf("record %+v, want a host-mismatch denial", rec)
	}
}

// TestHostHeaderBinding is the name layer on its own for the Host header
// (HTTP/1.1) and :authority (HTTP/2).
func TestHostHeaderBinding(t *testing.T) {
	t.Parallel()

	for name, proto := range map[string]protocol{"http1": http1, "http2": http2} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			rep := getVia(t, e.client(t, proto), e.base()+"/v1/x", "other.example")

			if rep.status != http.StatusMisdirectedRequest {
				t.Errorf("status %d, want 421", rep.status)
			}

			if got := e.up.requests(); len(got) != 0 {
				t.Errorf("the host got %d requests", len(got))
			}

			rec := e.sink.lastReq(t, 1)
			if rec.Reason != proxy.ReasonHostMismatch || rec.Host != testHost {
				t.Errorf("record %+v, want a host-mismatch denial", rec)
			}
		})
	}
}
