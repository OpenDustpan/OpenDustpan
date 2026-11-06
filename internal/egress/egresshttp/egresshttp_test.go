package egresshttp_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
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
		{"[::ffff:169.254.169.254]:443", false},
		{"[::ffff:a9fe:a9fe]:443", false},
		{"10.0.0.5:443", false},
		{"169.254.1.1:443", false},
		{"[fd00::1]:443", false},
		{"[fd00:ec2::254]:80", false},
		{"[fe80::1]:443", false},
		{"[fe80::1%eth0]:443", false},
		{"0.0.0.0:443", false},
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

// TestClientRefusesLoopback checks that the client refuses a request to
// the test server, which listens on loopback.
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

// TestClientChecksFirstURL checks that the client runs the URL check on
// the first request, not only on a redirect. No request reaches the
// network: the check refuses each URL before the dial.
func TestClientChecksFirstURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{"http for https only", "http://hooks.example.com/x"},
		{"user information", "https://user:pw@hooks.example.com/x"},
		{"hexadecimal loopback", "https://0x7f000001/x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)
			t.Cleanup(c.CloseIdleConnections)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tt.url, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := c.Do(req)
			if err == nil {
				if cerr := resp.Body.Close(); cerr != nil {
					t.Error(cerr)
				}

				t.Fatalf("Do(%s) = nil error, want ErrRefused", tt.url)
			}

			if !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("Do(%s) = %v, want ErrRefused", tt.url, err)
			}
		})
	}
}

// TestReadBodyGzip checks that ReadBody counts the bytes after the
// transport decompressed the body: a small gzip body that expands past
// egress.MaxBodyBytes gives ErrTooLarge.
func TestReadBodyGzip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(make([]byte, egress.MaxBodyBytes+1)); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if buf.Len() >= egress.MaxBodyBytes {
		t.Fatalf("compressed body is %d bytes, want fewer than %d", buf.Len(), egress.MaxBodyBytes)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")

		if _, err := w.Write(buf.Bytes()); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)

	// The egress client refuses the loopback test server, so this test
	// uses a plain transport, which decompresses the body in the same way.
	c := srv.Client()
	t.Cleanup(c.CloseIdleConnections)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Error(cerr)
		}
	})

	if !resp.Uncompressed {
		t.Fatal("the transport did not decompress the body")
	}

	_, err = egresshttp.ReadBody(resp)
	if !errors.Is(err, egresshttp.ErrTooLarge) {
		t.Fatalf("ReadBody = %v, want ErrTooLarge", err)
	}
}
