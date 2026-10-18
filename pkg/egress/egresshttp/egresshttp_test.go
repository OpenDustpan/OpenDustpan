package egresshttp_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/egress/egresshttp"
)

func TestControl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		address string
		ok      bool
	}{
		{"93.184.215.14:443", true},
		{"127.0.0.1:443", false},
		{"[::1]:8443", false},
		{"169.254.169.254:80", false},
		{"[::ffff:10.0.0.1]:443", false},
		{"not-an-address", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			t.Parallel()

			err := egresshttp.Control("tcp", tt.address, nil)
			if tt.ok != (err == nil) {
				t.Fatalf("Control(%s) = %v, want ok %v", tt.address, err, tt.ok)
			}
		})
	}
}

// TestClientRefusesLoopback checks that the dial guard refuses a
// connection after name resolution: the test server listens on loopback.
func TestClientRefusesLoopback(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	c := egresshttp.Client(egress.NoRedirect, egress.HTTPOrHTTPS)
	t.Cleanup(c.CloseIdleConnections)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.Do(req)
	if err == nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Error(cerr)
		}

		t.Fatal("Do to loopback = nil error, want ErrRefused")
	}

	if !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Do to loopback = %v, want ErrRefused", err)
	}
}
