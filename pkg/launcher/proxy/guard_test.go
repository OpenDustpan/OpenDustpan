package proxy_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// strictRules are the outbound rules with no allowed range and no loopback.
func strictRules() egress.Rules {
	return egress.Rules{Allow: []netip.Prefix{}, LoopbackForTests: false}
}

// refusedAddrs are addresses that the outbound guard must refuse.
func refusedAddrs() []string {
	return []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "::1", "::ffff:10.0.0.1"}
}

// TestOutboundGuardTunnel is the guard layer on its own, at the CONNECT of a
// tunnel. The rule host resolves to an address that the rules refuse; the
// proxy must not connect.
func TestOutboundGuardTunnel(t *testing.T) {
	t.Parallel()

	for _, addr := range refusedAddrs() {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, withRules(tunnelRules), withResolver(answering(addr)), withEgress(strictRules()))

			_, _, status := e.connectRaw(t, testHost+":"+portOf(e))
			if status != http.StatusForbidden {
				t.Fatalf("status %d, want 403", status)
			}

			if rec := e.sink.waitFor(t, 1)[0]; rec.Reason != proxy.ReasonGuardRefused {
				t.Errorf("record %+v, want guard-refused", rec)
			}
		})
	}
}

// TestOutboundGuardInspect is the guard layer on its own, at a request in an
// inspected connection: the TLS end works, the guard stops the request.
func TestOutboundGuardInspect(t *testing.T) {
	t.Parallel()

	for _, addr := range refusedAddrs() {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, withResolver(answering(addr)), withEgress(strictRules()))
			c, br := e.rawTLS(t)

			rep := rawRequest(t, c, br, "GET /v1/x HTTP/1.1\r\n"+hostHeader(e)+"\r\n")
			if rep.status != http.StatusForbidden {
				t.Errorf("status %d, want 403", rep.status)
			}

			if got := e.up.requests(); len(got) != 0 {
				t.Errorf("the host got %d requests", len(got))
			}

			if rec := e.sink.lastReq(t, 1); rec.Reason != proxy.ReasonGuardRefused {
				t.Errorf("record %+v, want guard-refused", rec)
			}
		})
	}
}

// changingResolver gives one answer, then another.
type changingResolver struct {
	first, then netip.Addr
	calls       atomic.Int32
}

func (r *changingResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if r.calls.Add(1) == 1 {
		return []netip.Addr{r.first}, nil
	}

	return []netip.Addr{r.then}, nil
}

// TestGuardDialUsesCheckedAddress checks that a DNS answer that changes
// after the check cannot change the target: the dial uses the checked IP and
// the Control check refuses any other address.
func TestGuardDialUsesCheckedAddress(t *testing.T) {
	t.Parallel()

	checked := netip.MustParseAddr("93.184.216.34")
	res := &changingResolver{first: checked, then: netip.MustParseAddr("127.0.0.1"), calls: atomic.Int32{}}

	var dialed []proxy.Dialed

	dial := proxy.ProbeGuardedDial(egress.Rules{Allow: nil, LoopbackForTests: false}, res, time.Second,
		func(d proxy.Dialed) { dialed = append(dialed, d) })

	if _, err := dial(t.Context(), testHost, 443); !errors.Is(err, proxy.ErrDialFailed) {
		t.Fatalf("dial error %v", err)
	}

	if got := res.calls.Load(); got != 1 {
		t.Errorf("the resolver ran %d times, want 1", got)
	}

	if len(dialed) != 1 || dialed[0].Addr.Addr() != checked || dialed[0].Addr.Port() != 443 {
		t.Fatalf("dialed %+v, want only %s:443", dialed, checked)
	}

	// The second answer would be 127.0.0.1. The Control check of the dial
	// refuses it, and any other socket address than the checked one.
	ctl := dialed[0].Control

	for _, addr := range []string{"127.0.0.1:443", "10.0.0.1:443", "[::1]:443", "93.184.216.35:443", "bad"} {
		if err := ctl("tcp", addr, nil); !errors.Is(err, proxy.ErrGuardRefused) {
			t.Errorf("Control(%q) = %v, want ErrGuardRefused", addr, err)
		}
	}

	if err := ctl("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("Control of the checked address: %v", err)
	}
}

// TestGuardControlRefusesRefusedAddress checks the Control layer alone: even
// when the expected address is a refused one, Control refuses it.
func TestGuardControlRefusesRefusedAddress(t *testing.T) {
	t.Parallel()

	for _, a := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "0.0.0.0", "224.0.0.1"} {
		ctl := proxy.ProbeControl(egress.Rules{Allow: nil, LoopbackForTests: false}, netip.MustParseAddr(a))

		if err := ctl("tcp", net.JoinHostPort(a, "443"), nil); !errors.Is(err, proxy.ErrGuardRefused) {
			t.Errorf("Control(%s) = %v, want ErrGuardRefused", a, err)
		}
	}
}

// TestGuardMixedAnswer checks that one refused address refuses the host.
func TestGuardMixedAnswer(t *testing.T) {
	t.Parallel()

	res := answering("93.184.216.34", "10.0.0.1")
	dial := proxy.ProbeGuardedDial(egress.Rules{Allow: nil, LoopbackForTests: false}, res, time.Second,
		func(proxy.Dialed) { t.Error("the dial connected although one address was refused") })

	if _, err := dial(t.Context(), testHost, 443); !errors.Is(err, proxy.ErrGuardRefused) {
		t.Errorf("dial error %v, want ErrGuardRefused", err)
	}
}

// TestGuardChecksEveryAddress checks that an address late in a long answer is
// checked too, although the dial tries only the first few.
func TestGuardChecksEveryAddress(t *testing.T) {
	t.Parallel()

	res := answering("93.184.216.34", "93.184.216.35", "93.184.216.36", "93.184.216.37", "93.184.216.38", "10.0.0.1")
	dial := proxy.ProbeGuardedDial(egress.Rules{Allow: nil, LoopbackForTests: false}, res, time.Second,
		func(proxy.Dialed) { t.Error("the dial connected although the sixth address was refused") })

	if _, err := dial(t.Context(), testHost, 443); !errors.Is(err, proxy.ErrGuardRefused) {
		t.Errorf("dial error %v, want ErrGuardRefused", err)
	}
}

// TestResolverErrorIsFixedReason checks that the text of a resolver error
// reaches neither the client nor the record.
func TestResolverErrorIsFixedReason(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withRules(tunnelRules), withResolver(failing(errLookup)))

	_, _, status := e.connectRaw(t, testHost+":"+portOf(e))
	if status != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", status)
	}

	rec := e.sink.waitFor(t, 1)[0]
	if rec.Reason != proxy.ReasonResolveFailed {
		t.Errorf("reason %q, want resolve-failed", rec.Reason)
	}

	if text := strings.ToLower(rec.Host + string(rec.Reason)); strings.Contains(text, "internal.corp") {
		t.Errorf("the record holds the text of the resolver error: %q", text)
	}
}

// TestTunnelPassesBytes is the positive case of the tunnel mode.
func TestTunnelPassesBytes(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withRules(tunnelRules))

	c, br, status := e.connectRaw(t, testHost+":"+portOf(e))
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}

	// The tunnel passes the TLS of the host, which the test pool trusts.
	if err := handshake(t, &bufferedConn{Conn: c, br: br}, e.up.pool, testHost); err != nil {
		t.Fatalf("handshake through the tunnel: %v", err)
	}

	closeNow(t, c)

	rec := e.sink.waitFor(t, 1)[0]
	if !rec.Allowed || rec.Bytes == 0 || rec.Mode != "tunnel" {
		t.Errorf("record %+v, want an allowed tunnel record with bytes", rec)
	}
}
