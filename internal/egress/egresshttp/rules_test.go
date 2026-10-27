package egresshttp_test

import (
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
)

func loopbackRules(t *testing.T) egress.Rules {
	t.Helper()

	return egress.Rules{Allow: nil, LoopbackForTests: true}
}

func TestControlWith(t *testing.T) {
	t.Parallel()

	control := egresshttp.ControlWith(loopbackRules(t))

	tests := []struct {
		address string
		ok      bool
	}{
		{"127.0.0.1:443", true},
		{"[::ffff:127.0.0.1]:443", true},
		{"10.0.0.1:443", false},
		{"[::1]:443", true},
		{"169.254.169.254:80", false},
		{"not-an-address", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			t.Parallel()

			err := control("tcp", tt.address, nil)
			if tt.ok != (err == nil) {
				t.Fatalf("control(%s) = %v, want ok %v", tt.address, err, tt.ok)
			}
		})
	}
}

func tlsServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *x509.CertPool) {
	t.Helper()

	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	return srv, pool
}

// TestClientWithAllowsAnAllowedRange sends a request to a loopback server
// that the rules allow.
func TestClientWithAllowsAnAllowedRange(t *testing.T) {
	t.Parallel()

	srv, pool := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	client := egresshttp.ClientWith(
		loopbackRules(t),
		egress.NoRedirect,
		egress.HTTPSOnly,
		egresshttp.Options{RootCAs: pool, Resolver: nil},
	)

	resp, err := client.Get(srv.URL) //nolint:noctx // a test
	if err != nil {
		t.Fatalf("get = %v", err)
	}

	_ = resp.Body.Close() //nolint:errcheck // A test.

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// TestDialLayer checks the dial guard alone: a dialer with the guard of
// the zero rules refuses a connection to a loopback server, with no URL
// check before it.
func TestDialLayer(t *testing.T) {
	t.Parallel()

	srv, _ := tlsServer(t, func(http.ResponseWriter, *http.Request) {})
	dialer := &net.Dialer{ //nolint:exhaustruct_v5 // the other fields keep their zero defaults
		Control: egresshttp.ControlWith(egress.Rules{Allow: nil, LoopbackForTests: false}),
	}

	conn, err := dialer.DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err == nil {
		_ = conn.Close() //nolint:errcheck // A test.
		t.Fatal("the dialer connected to loopback")
	}

	if !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("dial = %v, want ErrRefused", err)
	}
}

// TestClientWithRedirectLayer checks that a redirect to another host is
// refused with the redirect policy NoRedirect, and that a redirect to a
// refused address is refused with SameHost too.
func TestClientWithRedirectLayer(t *testing.T) {
	t.Parallel()

	target := "https://169.254.169.254/latest"
	srv, pool := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	})

	for _, redirects := range []egress.Redirects{egress.NoRedirect, egress.SameHost} {
		client := egresshttp.ClientWith(
			loopbackRules(t),
			redirects,
			egress.HTTPSOnly,
			egresshttp.Options{RootCAs: pool, Resolver: nil},
		)

		resp, err := client.Get(srv.URL) //nolint:noctx // a test
		if err == nil {
			_ = resp.Body.Close() //nolint:errcheck // A test.
			t.Fatalf("redirects %v: the redirect was followed", redirects)
		}

		if !errors.Is(err, egress.ErrRefused) {
			t.Fatalf("redirects %v: get = %v, want ErrRefused", redirects, err)
		}
	}
}

// TestClientWithURLLayer sends a URL of a bad shape to the client.
func TestClientWithURLLayer(t *testing.T) {
	t.Parallel()

	client := egresshttp.ClientWith(
		loopbackRules(t),
		egress.NoRedirect,
		egress.HTTPSOnly,
		egresshttp.Options{RootCAs: nil, Resolver: nil},
	)

	for _, u := range []string{"http://127.0.0.1/", "https://user@127.0.0.1/", "https://localhost/", "https://2130706433/"} {
		resp, err := client.Get(u) //nolint:noctx // a test
		if err == nil {
			_ = resp.Body.Close() //nolint:errcheck // A test.
			t.Fatalf("get %s passed", u)
		}

		if !errors.Is(err, egress.ErrRefused) {
			t.Fatalf("get %s = %v, want ErrRefused", u, err)
		}
	}
}

// TestControlWithAllowedRangeRefusesLoopback sends loopback and metadata
// addresses to the dial check with an operator range that holds them.
func TestControlWithAllowedRangeRefusesLoopback(t *testing.T) {
	t.Parallel()

	ranges, err := egress.ParseRanges([]string{"127.0.0.0/8", "169.254.0.0/16", "10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}

	control := egresshttp.ControlWith(egress.Rules{Allow: ranges, LoopbackForTests: false})

	for address, ok := range map[string]bool{
		"10.0.0.1:443": true, "127.0.0.1:443": false, "169.254.169.254:80": false, "[fd00:ec2::254]:80": false,
	} {
		if err := control("tcp", address, nil); ok != (err == nil) {
			t.Fatalf("control(%s) = %v, want ok %v", address, err, ok)
		}
	}
}
