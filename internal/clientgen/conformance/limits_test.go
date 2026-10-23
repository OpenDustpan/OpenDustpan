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
	"time"

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

			got, err := conformance.ExportReadLimited(bytes.NewReader(make([]byte, tt.size)))
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

// putThrough sends a PUT with a body of a size through a new proxy in front of
// an upstream, and returns the status that the client gets.
func putThrough(t *testing.T, upstream string, size int) int {
	t.Helper()

	routes := []model.Operation{route("putThing", model.MethodPut, "/api/dustpan/v1/things")}

	proxy, err := conformance.NewProxy(t.Context(), conformance.ProxyOptions{Upstream: upstream, Operations: routes})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := proxy.Close(); closeErr != nil {
			t.Errorf("close the proxy: %v", closeErr)
		}
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, proxy.URL()+"/api/dustpan/v1/things",
		strings.NewReader(strings.Repeat("x", size)))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	defer resp.Body.Close() //nolint:errcheck // The body is read below.

	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		t.Errorf("read the answer: %v", err)
	}

	return resp.StatusCode
}

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

	got := putThrough(t, counting.URL, conformance.MaxProxyBodyBytes)
	if got != http.StatusNoContent || hits.Load() != 1 {
		t.Errorf("request at the limit: want 204 and 1 hit, got %d and %d", got, hits.Load())
	}

	hits.Store(0)

	got = putThrough(t, counting.URL, conformance.MaxProxyBodyBytes+1)
	if got != http.StatusRequestEntityTooLarge || hits.Load() != 0 {
		t.Errorf("request over the limit: want 413 and no hit, got %d and %d", got, hits.Load())
	}

	if got = putThrough(t, bigServer(t).URL, 0); got != http.StatusBadGateway {
		t.Errorf("answer over the limit: want 502, got %d", got)
	}
}

// TestHarnessReadsRefuseAnswersOverTheLimit checks the two other places that
// read a body of the server: the description and a direct request.
func TestHarnessReadsRefuseAnswersOverTheLimit(t *testing.T) {
	t.Parallel()

	srv := bigServer(t)

	_, err := conformance.DevServerAt(srv.URL).Description(t.Context())
	if !errors.Is(err, conformance.ErrBodyTooLarge) {
		t.Errorf("description: want ErrBodyTooLarge, got %v", err)
	}

	_, _, err = conformance.DirectGet(t.Context(), srv.URL, "/x")
	if !errors.Is(err, conformance.ErrBodyTooLarge) {
		t.Errorf("direct: want ErrBodyTooLarge, got %v", err)
	}
}

// proxyDial sends a GET to an address with the client that the proxy uses for
// its upstream, and returns the error.
func proxyDial(t *testing.T, proxy *conformance.RecordingProxy, addr string) error {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, addr, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := proxy.Client().Do(req)
	if err == nil {
		resp.Body.Close() //nolint:errcheck,gosec // The test fails below.
	}

	return err //nolint:wrapcheck // The test checks the error.
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

			dialErr := proxyDial(t, proxy, addr)
			if !errors.Is(dialErr, conformance.ErrNotLoopback) {
				t.Errorf("proxy upstream client: want ErrNotLoopback, got %v", dialErr)
			}

			_, descErr := conformance.DevServerAt(addr).Description(t.Context())
			if !errors.Is(descErr, conformance.ErrNotLoopback) {
				t.Errorf("dev server client: want ErrNotLoopback, got %v", descErr)
			}

			_, _, directErr := conformance.DirectGet(t.Context(), addr, "/x")
			if !errors.Is(directErr, conformance.ErrNotLoopback) {
				t.Errorf("direct client: want ErrNotLoopback, got %v", directErr)
			}
		})
	}

	// The positive control: a loopback address is not refused.
	status, _, err := conformance.DirectGet(t.Context(), live.URL, "/x")
	if err != nil || status != http.StatusNotFound {
		t.Errorf("a loopback server should answer: %d %v", status, err)
	}
}

// TestDevServerAtCloses checks that Close of a helper with no process returns.
func TestDevServerAtCloses(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)

	go func() { done <- conformance.DevServerAt("http://127.0.0.1:1").Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close of a DevServerAt helper did not return")
	}
}

// TestEnsureProfileWritesTheSeedOnce checks that an Env that no run made still
// writes the seed profile once: the second call must not post it again.
func TestEnsureProfileWritesTheSeedOnce(t *testing.T) {
	t.Parallel()

	var posts atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if posts.Add(1) > 1 {
				w.WriteHeader(http.StatusConflict)

				return
			}

			w.WriteHeader(http.StatusCreated)

			return
		}

		w.Header().Set("ETag", `"1"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	for range 3 {
		if _, err := conformance.SeedProfile(t.Context(), srv.URL); err != nil {
			t.Fatalf("ensure profile: %v", err)
		}
	}

	if got := posts.Load(); got != 1 {
		t.Errorf("the seed profile was posted %d times, want 1", got)
	}
}

// TestEnsureProfileRetriesAFailedSeed checks that a failed seed write is not
// kept: the call after it posts the seed profile again, and the calls after
// a successful write do not post it again.
func TestEnsureProfileRetriesAFailedSeed(t *testing.T) {
	t.Parallel()

	var posts atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if posts.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)

				return
			}

			w.WriteHeader(http.StatusCreated)

			return
		}

		w.Header().Set("ETag", `"1"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	_, err := conformance.SeedProfile(t.Context(), srv.URL)
	if !errors.Is(err, conformance.ErrExpectation) {
		t.Fatalf("first seed: err = %v, want ErrExpectation", err)
	}

	for range 2 {
		etag, err := conformance.SeedProfile(t.Context(), srv.URL)
		if err != nil || etag != `"1"` {
			t.Fatalf("seed after a success: etag %q, err %v", etag, err)
		}
	}

	if got := posts.Load(); got != 2 {
		t.Errorf("the seed profile was posted %d times, want 2", got)
	}
}
