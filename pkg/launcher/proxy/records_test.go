package proxy_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// fullRequest is a plain request that ends at once and leaves one record.
const fullRequest = "http://nohost.example.com/x"

// TestRecordsFullRefusesRequests is the record layer on its own: with the
// sink down and the buffer full, the next request is refused.
func TestRecordsFullRefusesRequests(t *testing.T) {
	t.Parallel()

	e := newEnv(t, withBuffer(2))
	e.sink.fail.Store(true)

	// Two of them fill the buffer of two.
	for i := range 2 {
		if status := plainStatus(t, e, fullRequest); status != http.StatusForbidden {
			t.Fatalf("request %d: status %d, want 403", i, status)
		}
	}

	if status := plainStatus(t, e, fullRequest); status != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503 with a full buffer", status)
	}

	// A CONNECT is refused too, and nothing reaches the host.
	c := dialRaw(t, e.addr)
	send(t, c, "CONNECT "+testHost+":"+portOf(e)+" HTTP/1.1\r\nHost: "+testHost+"\r\n\r\n")

	if rep, _ := readReply(newReader(c)); rep.status != http.StatusServiceUnavailable {
		t.Errorf("CONNECT status %d, want 503 with a full buffer", rep.status)
	}

	if got := e.up.requests(); len(got) != 0 {
		t.Errorf("the host got %d requests", len(got))
	}

	e.sink.fail.Store(false)

	for _, r := range e.sink.waitFor(t, 2) {
		if r.Job != "job-1" || r.Phase != "plan" || r.Unit != "acme/payments" || r.Operation != jobproxy.OperationPlan {
			t.Errorf("record %+v lacks the job fields", r)
		}
	}

	awaitAccepted(t, e)
}

// awaitAccepted waits until the proxy takes requests again.
func awaitAccepted(t *testing.T, e *env) {
	t.Helper()

	status := 0

	if !poll(t, func() bool {
		status = plainStatus(t, e, fullRequest)

		return status == http.StatusForbidden
	}) {
		t.Fatalf("requests stay refused after the sink is back: status %d", status)
	}
}

// TestRecordFields guards the fields of a record. A new field must be a
// decision of the owner: a record never holds a header, a body, a query
// value, a credential or a library error text.
func TestRecordFields(t *testing.T) {
	t.Parallel()

	want := []string{
		"Job", "Phase", "Unit", "Operation", "Mode", "Method", "Host", "Path",
		"RuleID", "Allowed", "Reason", "Status", "Bytes", "Time",
	}

	typ := reflect.TypeFor[proxy.Record]()
	got := make([]string, 0, typ.NumField())

	for f := range typ.Fields() {
		got = append(got, f.Name)
	}

	if !slices.Equal(got, want) {
		t.Errorf("record fields %v, want %v", got, want)
	}
}

// TestRecordHoldsNoQueryOrHeader checks that a request with a query value
// and a header leaves a record with neither.
func TestRecordHoldsNoQueryOrHeader(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	c, br := e.rawTLS(t)

	rawRequest(
		t,
		c,
		br,
		"GET /v1/x?secretquery=hidden-value HTTP/1.1\r\n"+hostHeader(e)+"X-Private: hidden-header\r\n\r\n",
	)

	for _, r := range e.sink.waitFor(t, 1) {
		text := fmt.Sprintf("%+v", r)
		if strings.Contains(text, "hidden") || strings.Contains(text, "secretquery") {
			t.Errorf("record %+v holds a query value or a header", r)
		}
	}
}

// zeroRenderedProxy builds a proxy with the zero rendered rules.
func zeroRenderedProxy(t *testing.T, s *sink) (*proxy.Proxy, net.Listener) {
	t.Helper()

	p, err := proxy.New(baseConfig(s))
	if err != nil {
		t.Fatal(err)
	}

	l := listenLoopback(t)
	done := make(chan struct{})

	go func() {
		defer close(done)

		if serr := p.Serve(l); serr != nil {
			t.Errorf("Serve: %v", serr)
		}
	}()

	t.Cleanup(func() {
		if cerr := p.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}

		<-done
	})

	return p, l
}

// TestZeroRenderedAllowsNothing is the fail closed layer: the zero rendered
// rules allow no CONNECT and no plain request.
func TestZeroRenderedAllowsNothing(t *testing.T) {
	t.Parallel()

	s := newSink()
	_, l := zeroRenderedProxy(t, s)

	for _, target := range []string{testHost + ":443", "other.example:443"} {
		c := dialRaw(t, l.Addr().String())
		send(t, c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")

		if rep, _ := readReply(newReader(c)); rep.status != http.StatusForbidden {
			t.Errorf("CONNECT %s: status %d, want 403", target, rep.status)
		}
	}

	for _, r := range s.waitFor(t, 2) {
		if r.Allowed || r.Reason != proxy.ReasonNoRuleHost {
			t.Errorf("record %+v, want a no-rule-host denial", r)
		}
	}
}

// TestPlainCredentialLayer is the check against a credential over plain HTTP,
// on its own: it runs below jobproxy.Match, as if Match had allowed the
// request.
func TestPlainCredentialLayer(t *testing.T) {
	t.Parallel()

	p, err := proxy.New(baseConfig(newSink()))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if cerr := p.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	for _, c := range [][2]string{{"tok", ""}, {"", "cert"}, {"tok", "cert"}} {
		status, reason := proxy.ProbeBuildPlain(p, c[0], c[1])
		if status != http.StatusForbidden || reason != proxy.ReasonAuthOverHTTP {
			t.Errorf("plain with %q and %q: %d %q, want 403 credential-over-http", c[0], c[1], status, reason)
		}
	}

	if status, reason := proxy.ProbeBuildPlain(p, "", ""); status != 0 || reason != proxy.ReasonNone {
		t.Errorf("plain with no credential: %d %q, want it to pass", status, reason)
	}

	if status, reason := proxy.ProbeBuildInspect(p, "", ""); status != 0 || reason != proxy.ReasonNone {
		t.Errorf("inspect with no credential: %d %q, want it to pass", status, reason)
	}
}

// TestNewRefusesBadConfig checks the configuration errors.
func TestNewRefusesBadConfig(t *testing.T) {
	t.Parallel()

	bad := map[string]func(c *proxy.Config){
		"operation": func(c *proxy.Config) { c.Operation = "deploy" },
		"sink":      func(c *proxy.Config) { c.Records = nil },
		"lifetime":  func(c *proxy.Config) { c.CALifetime = 0 },
	}

	for name, mut := range bad {
		cfg := baseConfig(newSink())
		mut(&cfg)

		if _, err := proxy.New(cfg); err == nil {
			t.Errorf("%s: New accepted the configuration", name)
		}
	}
}

// TestEndpointRefusesOtherListeners checks that an endpoint serves only on
// loopback and only for an endpoint rule.
func TestEndpointRefusesOtherListeners(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	l := listenLoopback(t)

	closeAfter(t, l)

	if err := e.p.ServeEndpoint(l, testHost, e.up.port); err == nil {
		t.Error("an inspect host was served as an endpoint")
	}

	ep := newEnv(t, withRules(endpointRules))

	var lc net.ListenConfig

	wild, err := lc.Listen(t.Context(), "tcp", ":0")
	if err != nil {
		t.Skip("no wildcard listener")
	}

	closeAfter(t, wild)

	if err := ep.p.ServeEndpoint(wild, testHost, ep.up.port); err == nil {
		t.Error("an endpoint was served on a listener beyond loopback")
	}
}

// parseCA decodes the CA certificate of a proxy.
func parseCA(t *testing.T, p *proxy.Proxy) *x509.Certificate {
	t.Helper()

	pemBytes := p.CACertificatePEM()
	if strings.Contains(string(pemBytes), "PRIVATE KEY") {
		t.Fatal("the exposed bytes hold a private key")
	}

	block, rest := pem.Decode(pemBytes)
	if block == nil || len(rest) != 0 || block.Type != "CERTIFICATE" {
		t.Fatal("the exposed bytes are not one certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	return cert
}

// TestCA checks the CA of the claim: ECDSA P-256, name constraints to the
// hosts of the inspect rules, no IP names, no sub-CA, the lifetime given, and
// no private key in the exposed bytes.
func TestCA(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	cfg := baseConfig(newSink())
	cfg.Rendered = render(t, `{"rules":[`+
		`{"id":"a","operations":["plan"],"host":"api.example.com","mode":"inspect","methods":["GET"],"paths":["/**"]},`+
		`{"id":"b","operations":["plan"],"host":"tunnel.example.com","mode":"tunnel"},`+
		`{"id":"c","operations":["apply"],"host":"apply.example.com","mode":"inspect","methods":["GET"],"paths":["/**"]}]}`)
	cfg.CALifetime = 90 * time.Minute
	cfg.Now = func() time.Time { return now }

	p, err := proxy.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if cerr := p.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	cert := parseCA(t, p)

	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || pub.Curve != elliptic.P256() {
		t.Error("the CA key is not ECDSA P-256")
	}

	if !cert.IsCA || cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Errorf("the CA must sign leaves only: %+v", cert)
	}

	if !slices.Equal(cert.PermittedDNSDomains, []string{"api.example.com"}) {
		t.Errorf("permitted names %v, want only the inspect host of this operation", cert.PermittedDNSDomains)
	}

	if len(cert.ExcludedIPRanges) != 2 {
		t.Errorf("excluded IP ranges %v, want all addresses", cert.ExcludedIPRanges)
	}

	if !cert.NotAfter.Equal(now.Add(90 * time.Minute)) {
		t.Errorf("NotAfter %v, want the lifetime of the claim", cert.NotAfter)
	}
}

// TestCAWithoutInspectHostIsConstrained checks that a CA with no inspect host
// still carries a name constraint.
func TestCAWithoutInspectHostIsConstrained(t *testing.T) {
	t.Parallel()

	p, err := proxy.New(baseConfig(newSink()))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if cerr := p.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	if len(parseCA(t, p).PermittedDNSDomains) == 0 {
		t.Error("a CA with no inspect host has no name constraint")
	}
}
