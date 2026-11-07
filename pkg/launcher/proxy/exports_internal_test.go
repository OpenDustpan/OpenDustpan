package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// ProbeForms exposes the forms of a secret to the tests.
func ProbeForms(secret string) [][]byte { return formsOf(secret) }

// ProbeScanReader exposes the scan reader to the tests.
func ProbeScanReader(secrets []string, r io.Reader) io.Reader {
	return newScanner(secrets).reader(r)
}

// ProbeScanWindow exposes the window of a scanner to the tests.
func ProbeScanWindow(secrets []string) int { return newScanner(secrets).window }

// ProbeControl exposes the Control check of the outbound guard.
func ProbeControl(rules egress.Rules, expected netip.Addr) func(string, string, syscall.RawConn) error {
	return guardControl(rules, expected)
}

// ProbeBuildPlain runs buildRequest for a plain HTTP request to a rule with
// the named credential and client certificate. It does not go through
// jobproxy.Match, so it tests the check of this layer alone. It returns the
// status and the reason of the refusal, or 0 and ReasonNone.
func ProbeBuildPlain(p *Proxy, credential, clientCertificate string) (int, Reason) {
	return probeBuild(p, kindPlain, credential, clientCertificate)
}

// ProbeBuildInspect is ProbeBuildPlain for a request inside a TLS end.
func ProbeBuildInspect(p *Proxy, credential, clientCertificate string) (int, Reason) {
	return probeBuild(p, kindInspect, credential, clientCertificate)
}

// probeBuild runs buildRequest for a request of kind k.
func probeBuild(p *Proxy, k kind, credential, clientCertificate string) (int, Reason) {
	rule := jobproxy.RenderedRule{
		ID: "r", Operations: nil, Host: "api.example.com", Port: defaultTLSPort, Mode: jobproxy.ModeInspect,
		Methods: nil, Paths: nil, Credential: credential, ClientCertificate: clientCertificate,
		ServerTrust: jobproxy.ServerTrustSystem,
	}
	adm := admitted{rule: rule, path: "/x", query: "", hasQuery: false}
	req := &http.Request{Method: http.MethodGet, Header: http.Header{}}
	out := &outbound{req: &http.Request{}, scan: nil}

	ref := p.buildRequest(req, target{host: rule.Host, port: rule.Port, kind: k}, adm, out)
	if ref == nil {
		return 0, ReasonNone
	}

	return ref.status, ref.reason
}

// Dialed is one connect that the guarded dial tried.
type Dialed struct {
	Addr    netip.AddrPort
	Control func(string, string, syscall.RawConn) error
}

// ProbeGuardedDial builds the guarded dial with a connect function that
// records the address and the Control check instead of opening a socket.
func ProbeGuardedDial(
	rules egress.Rules, res Resolver, timeout time.Duration, seen func(Dialed),
) func(ctx context.Context, host string, port int) (net.Conn, error) {
	g := &guardDialer{
		rules: rules, resolver: res, timeout: timeout,
		dial: func(_ context.Context, d *net.Dialer, addr netip.AddrPort) (net.Conn, error) {
			seen(Dialed{Addr: addr, Control: d.Control})

			return nil, ErrDialFailed
		},
	}

	return g.DialContext
}
