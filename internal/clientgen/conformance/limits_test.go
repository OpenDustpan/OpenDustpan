package conformance_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// TestReadLimitedRefusesALongBody checks the limit on its own: a body at the
// limit is read, and a body one byte over is an error and not a cut body.
func TestReadLimitedRefusesALongBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int
		want error
	}{
		{"empty", 0, nil},
		{"one under the limit", conformance.MaxProxyBodyBytes - 1, nil},
		{"at the limit", conformance.MaxProxyBodyBytes, nil},
		{"one over the limit", conformance.MaxProxyBodyBytes + 1, conformance.ErrBodyTooLarge},
		{"twice the limit", 2 * conformance.MaxProxyBodyBytes, conformance.ErrBodyTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := conformance.ReadLimited(bytes.NewReader(make([]byte, tt.size)))
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}

			if tt.want == nil && len(got) != tt.size {
				t.Errorf("want %d bytes, got %d", tt.size, len(got))
			}

			if tt.want != nil && got != nil {
				t.Error("a refused body comes with no bytes")
			}
		})
	}
}

// bigServer answers each request with a body over the limit.
func bigServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if _, err := io.Copy(w, io.LimitReader(zeroReader{}, conformance.MaxProxyBodyBytes+1)); err != nil {
			return
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// TestProxyRefusesBodiesOverTheLimit checks both ways: a request body over the
// limit gets 413 and never reaches the server, and an answer over the limit is
// not cut and sent with a correct length: the client gets 502.
func TestProxyRefusesBodiesOverTheLimit(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64

	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(counting.Close)

	routes := []model.Operation{route("putThing", model.MethodPut, "/api/dustpan/v1/things")}

	tests := []struct {
		name     string
		upstream string
		body     int
		want     int
		hits     int64
	}{
		{"request at the limit", counting.URL, conformance.MaxProxyBodyBytes, http.StatusNoContent, 1},
		{"request over the limit", counting.URL, conformance.MaxProxyBodyBytes + 1, http.StatusRequestEntityTooLarge, 0},
		{"answer over the limit", bigServer(t).URL, 0, http.StatusBadGateway, 0},
	}

	for _, tt := range tests {
		hits.Store(0)

		proxy, err := conformance.NewProxy(t.Context(), conformance.ProxyOptions{Upstream: tt.upstream, Operations: routes})
		if err != nil {
			t.Fatalf("%s: new proxy: %v", tt.name, err)
		}

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, proxy.URL()+"/api/dustpan/v1/things",
			strings.NewReader(strings.Repeat("x", tt.body)))
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}

		if _, err = io.Copy(io.Discard, resp.Body); err != nil {
			t.Errorf("%s: read the answer: %v", tt.name, err)
		}

		if err = resp.Body.Close(); err != nil {
			t.Errorf("%s: close: %v", tt.name, err)
		}

		if resp.StatusCode != tt.want {
			t.Errorf("%s: want status %d, got %d", tt.name, tt.want, resp.StatusCode)
		}

		if tt.name != "answer over the limit" && hits.Load() != tt.hits {
			t.Errorf("%s: the server got %d requests, want %d", tt.name, hits.Load(), tt.hits)
		}

		if err = proxy.Close(); err != nil {
			t.Errorf("%s: close the proxy: %v", tt.name, err)
		}
	}
}

// TestHarnessReadsRefuseAnswersOverTheLimit checks the two other places that
// read a body of the server: the description and a direct request.
func TestHarnessReadsRefuseAnswersOverTheLimit(t *testing.T) {
	t.Parallel()

	srv := bigServer(t)

	if _, err := conformance.DevServerAt(srv.URL).Description(t.Context()); !errors.Is(err, conformance.ErrBodyTooLarge) {
		t.Errorf("description: want ErrBodyTooLarge, got %v", err)
	}

	if _, _, err := conformance.Direct(t.Context(), srv.URL, "/x"); !errors.Is(err, conformance.ErrBodyTooLarge) {
		t.Errorf("direct: want ErrBodyTooLarge, got %v", err)
	}
}

// TestHarnessClientsConnectToLoopbackOnly checks the address of each
// connection of the three clients of the harness. The addresses are literals,
// so the check does not depend on a name: each is refused before a connection
// opens.
func TestHarnessClientsConnectToLoopbackOnly(t *testing.T) {
	t.Parallel()

	live := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(live.Close)

	proxy, err := conformance.NewProxy(t.Context(), conformance.ProxyOptions{Upstream: live.URL, Operations: nil})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := proxy.Close(); closeErr != nil {
			t.Errorf("close the proxy: %v", closeErr)
		}
	})

	for _, addr := range []string{"http://192.0.2.1:9", "http://10.255.255.1:9"} {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()

			req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, addr, nil)
			if reqErr != nil {
				t.Fatal(reqErr)
			}

			if resp, doErr := proxy.Client().Do(req); !errors.Is(doErr, conformance.ErrNotLoopback) {
				t.Errorf("proxy upstream client: want ErrNotLoopback, got %v", doErr)
			} else if resp != nil {
				t.Error("a refused connection has no answer")
			}

			if _, descErr := conformance.DevServerAt(addr).Description(t.Context()); !errors.Is(descErr, conformance.ErrNotLoopback) {
				t.Errorf("dev server client: want ErrNotLoopback, got %v", descErr)
			}

			if _, _, directErr := conformance.Direct(t.Context(), addr, "/x"); !errors.Is(directErr, conformance.ErrNotLoopback) {
				t.Errorf("direct client: want ErrNotLoopback, got %v", directErr)
			}
		})
	}

	// The positive control: a loopback address is not refused.
	status, _, err := conformance.Direct(t.Context(), live.URL, "/x")
	if err != nil || status != http.StatusNotFound {
		t.Errorf("a loopback server should answer: %d %v", status, err)
	}
}
