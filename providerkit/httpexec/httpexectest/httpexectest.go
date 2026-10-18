// Package httpexectest is the conformance suite for httpexec.Caller
// adapters. An adapter runs it with a factory:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		httpexectest.Run(t, func(t *testing.T, o httpexectest.Options) httpexec.Caller {
//			return newCaller(o.Origin, o.MaxReplyBytes, o.Authorizer)
//		})
//	}
//
// The suite starts local HTTP servers, so it needs no network.
package httpexectest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"opendustpan.org/providerkit/httpexec"
)

// Options configure the caller under test.
type Options struct {
	// Origin is the only origin that the caller may send to.
	Origin httpexec.Origin
	// MaxReplyBytes is the limit of a reply body.
	MaxReplyBytes int64
	// Authorizer adds the credential to each request.
	Authorizer httpexec.Authorizer
}

// Factory returns a caller for one subtest.
type Factory func(t *testing.T, o Options) httpexec.Caller

// maxReply is the reply limit of the suite: small, so the too-large case
// stays fast.
const maxReply = 1024

// testToken is the credential of the suite's Authorizer.
const testToken = "Bearer test-credential"

// Run executes the conformance suite against callers from newCaller.
func Run(t *testing.T, newCaller Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, newCaller Factory)
	}{
		{name: "request arrives unchanged, with the credential", test: testRoundTrip},
		{name: "a 500 is a reply, not an error", test: testServerError},
		{name: "a redirect comes back and is not followed", test: testRedirect},
		{name: "a reply over the limit is ErrTooLarge", test: testTooLarge},
		{name: "an Authorizer error stops the call", test: testAuthorizerError},
		{name: "a canceled context ends the call", test: testCanceled},
		{name: "a request to another origin is refused and gets no credential", test: testOtherOrigin},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.test(t, newCaller)
		})
	}
}

// staticAuth sets testToken, or fails with err. It counts its calls.
type staticAuth struct {
	err   error
	calls *atomic.Int32
}

func (a staticAuth) Authorize(_ context.Context, h http.Header) error {
	a.calls.Add(1)

	if a.err != nil {
		return a.err
	}

	h.Set("Authorization", testToken)

	return nil
}

// options returns the options of a caller for the origin of srv.
func options(t *testing.T, srv *httptest.Server) Options {
	t.Helper()

	o, err := httpexec.OriginOf(srv.URL)
	if err != nil {
		t.Fatalf("OriginOf(%q) error = %v", srv.URL, err)
	}

	return Options{Origin: o, MaxReplyBytes: maxReply, Authorizer: staticAuth{err: nil, calls: new(atomic.Int32)}}
}

func server(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv
}

// write writes a reply body from a handler. The client of a test can close
// the connection early, so a failure is a log line, not a test failure.
func write(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()

	if _, err := w.Write(body); err != nil {
		t.Logf("write reply: %v", err)
	}
}

func get(url string) httpexec.Request {
	return httpexec.Request{Method: http.MethodGet, URL: url, Header: http.Header{}, Body: nil}
}

func testRoundTrip(t *testing.T, newCaller Factory) {
	t.Helper()

	const mark = "yes"

	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		if !sameRequest(r, mark) {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		w.Header().Set("X-Reply", mark)
		write(t, w, []byte(`{"id":"1"}`))
	})

	req := httpexec.Request{
		Method: http.MethodPut, URL: srv.URL + "/v1/items/a%2Fb?x=1",
		Header: http.Header{"X-Test": {mark}}, Body: []byte(`{"name":"n"}`),
	}

	got, err := newCaller(t, options(t, srv)).Call(t.Context(), req)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	if got.Status != http.StatusOK || string(got.Body) != `{"id":"1"}` || got.Header.Get("X-Reply") != mark {
		t.Fatalf("Call() = %d %q, want 200 and the body; the server saw a changed request", got.Status, got.Body)
	}

	if req.Header.Get("Authorization") != "" {
		t.Fatal("Call() added the credential to the caller's request, want a copy of the headers")
	}
}

// sameRequest reports whether the server got the request of testRoundTrip
// unchanged, with the credential.
func sameRequest(r *http.Request, mark string) bool {
	body, err := io.ReadAll(r.Body)

	return err == nil && r.Method == http.MethodPut && r.URL.EscapedPath() == "/v1/items/a%2Fb" &&
		r.URL.RawQuery == "x=1" && r.Header.Get("X-Test") == mark &&
		r.Header.Get("Authorization") == testToken && string(body) == `{"name":"n"}`
}

func testServerError(t *testing.T, newCaller Factory) {
	t.Helper()

	srv := server(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	got, err := newCaller(t, options(t, srv)).Call(t.Context(), get(srv.URL))
	if err != nil || got.Status != http.StatusInternalServerError {
		t.Fatalf("Call() = %d, %v, want 500 and nil", got.Status, err)
	}
}

func testRedirect(t *testing.T, newCaller Factory) {
	t.Helper()

	var hits atomic.Int32

	other := server(t, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	})

	got, err := newCaller(t, options(t, srv)).Call(t.Context(), get(srv.URL))
	if err != nil || got.Status != http.StatusFound {
		t.Fatalf("Call() = %d, %v, want 302 and nil", got.Status, err)
	}

	if hits.Load() != 0 {
		t.Fatal("Call() followed the redirect to another host")
	}
}

func testTooLarge(t *testing.T, newCaller Factory) {
	t.Helper()

	srv := server(t, func(w http.ResponseWriter, _ *http.Request) {
		write(t, w, bytes.Repeat([]byte("x"), maxReply+1))
	})

	if _, err := newCaller(t, options(t, srv)).Call(t.Context(), get(srv.URL)); !errors.Is(err, httpexec.ErrTooLarge) {
		t.Fatalf("Call() error = %v, want ErrTooLarge", err)
	}
}

var errNoToken = errors.New("no token")

func testAuthorizerError(t *testing.T, newCaller Factory) {
	t.Helper()

	var hits atomic.Int32

	srv := server(t, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
	o := options(t, srv)
	o.Authorizer = staticAuth{err: errNoToken, calls: new(atomic.Int32)}

	if _, err := newCaller(t, o).Call(t.Context(), get(srv.URL)); !errors.Is(err, errNoToken) {
		t.Fatalf("Call() error = %v, want the Authorizer's error", err)
	}

	if hits.Load() != 0 {
		t.Fatal("Call() sent the request without a credential")
	}
}

func testCanceled(t *testing.T, newCaller Factory) {
	t.Helper()

	srv := server(t, func(http.ResponseWriter, *http.Request) {})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := newCaller(t, options(t, srv)).Call(ctx, get(srv.URL)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Call() error = %v, want context.Canceled", err)
	}
}

func testOtherOrigin(t *testing.T, newCaller Factory) {
	t.Helper()

	var hits atomic.Int32

	other := server(t, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
	srv := server(t, func(http.ResponseWriter, *http.Request) {})

	o := options(t, srv)
	auth := staticAuth{err: nil, calls: new(atomic.Int32)}
	o.Authorizer = auth

	if _, err := newCaller(t, o).Call(t.Context(), get(other.URL+"/next")); !errors.Is(err, httpexec.ErrOtherOrigin) {
		t.Fatalf("Call() error = %v, want ErrOtherOrigin", err)
	}

	if hits.Load() != 0 {
		t.Fatal("Call() sent the request to another origin")
	}

	if auth.calls.Load() != 0 {
		t.Fatal("Call() asked for the credential of a request to another origin")
	}
}
