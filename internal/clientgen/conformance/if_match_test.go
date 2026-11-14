package conformance_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
)

// ifMatchCase is the name of the case under test.
const ifMatchCase = "missing-required-if-match-gives-428"

// seedServer is a Server at the URL of a test server.
type seedServer struct {
	conformance.Server

	url string
}

func (s seedServer) URL() string   { return s.url }
func (s seedServer) Token() string { return "t" }

// fakeProxy is a Proxy that records the exchanges that fakeAdapter sends.
type fakeProxy struct {
	conformance.Proxy

	mu        sync.Mutex
	exchanges []conformance.Exchange
}

func (p *fakeProxy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exchanges = nil
}

func (p *fakeProxy) Exchanges() []conformance.Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]conformance.Exchange(nil), p.exchanges...)
}

func (p *fakeProxy) record(ex conformance.Exchange) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exchanges = append(p.exchanges, ex)
}

// fakeAdapter is an Adapter that refuses a call with no If-Match. When
// refuseAll is true, it refuses a call with If-Match too, as a client that
// refuses the call for another argument does. Else it sends the call with
// If-Match through proxy, and the server answers 412.
type fakeAdapter struct {
	conformance.Adapter

	proxy     *fakeProxy
	refuseAll bool
}

func (a *fakeAdapter) Call(_ context.Context, p conformance.CallParams) (conformance.CallResult, error) {
	if p.IfMatch == "" || a.refuseAll {
		return conformance.CallResult{}, &conformance.RPCError{Code: -32602, Message: "refused"}
	}

	a.proxy.record(conformance.Exchange{
		Method: http.MethodPost, Path: "", RawQuery: "", Headers: map[string]string{"If-Match": p.IfMatch},
		Body: nil, Status: http.StatusPreconditionFailed, At: time.Time{},
	})

	return conformance.CallResult{
		Status: http.StatusPreconditionFailed, ETag: "", Body: nil,
		Error: &conformance.ErrorResult{
			Code: http.StatusPreconditionFailed, Message: "stale", ErrorCode: "", ErrorArgs: nil,
			Transport: false, Invalid: false,
		},
	}, nil
}

// runIfMatchCase runs the case under test against a fakeAdapter.
func runIfMatchCase(t *testing.T, refuseAll bool) conformance.Result {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)

			return
		}

		w.Header().Set("ETag", `"1"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	proxy := &fakeProxy{Proxy: nil, mu: sync.Mutex{}, exchanges: nil}

	e := env(proxy)
	e.Server = seedServer{Server: nil, url: srv.URL}
	e.Adapter = &fakeAdapter{Adapter: nil, proxy: proxy, refuseAll: refuseAll}
	e.Operations = []string{"createProfileRevision"}

	var cases []conformance.Case

	for _, c := range conformance.Cases() {
		if c.Group == conformance.GroupConcurrency && c.Name == ifMatchCase {
			cases = append(cases, c)
		}
	}

	if len(cases) != 1 {
		t.Fatalf("want the case %s, got %d cases", ifMatchCase, len(cases))
	}

	return conformance.Run(t.Context(), e, cases, 0)[0]
}

// staleCase is the name of the case under test.
const staleCase = "stale-if-match-gives-412"

// staleAdapter answers every call with the result of the row.
type staleAdapter struct {
	conformance.Adapter

	res conformance.CallResult
}

func (a *staleAdapter) Call(context.Context, conformance.CallParams) (conformance.CallResult, error) {
	return a.res, nil
}

// runStaleCase runs the stale-If-Match case against an adapter that answers the
// result of the row.
func runStaleCase(t *testing.T, res conformance.CallResult) conformance.Result {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)

			return
		}

		w.Header().Set("ETag", `"1"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	proxy := &fakeProxy{Proxy: nil, mu: sync.Mutex{}, exchanges: nil}

	e := env(proxy)
	e.Server = seedServer{Server: nil, url: srv.URL}
	e.Adapter = &staleAdapter{Adapter: nil, res: res}
	e.Operations = []string{"createProfileRevision"}

	var cases []conformance.Case

	for _, c := range conformance.Cases() {
		if c.Group == conformance.GroupConcurrency && c.Name == staleCase {
			cases = append(cases, c)
		}
	}

	if len(cases) != 1 {
		t.Fatalf("want the case %s, got %d cases", staleCase, len(cases))
	}

	return conformance.Run(t.Context(), e, cases, 0)[0]
}

// TestStaleIfMatchChecksTheErrorCode proves that the stale-If-Match case
// accepts only a 412 with the documented errorCode and a message.
func TestStaleIfMatchChecksTheErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *conformance.ErrorResult
		want conformance.Status
	}{
		{
			"the documented errorCode and a message",
			&conformance.ErrorResult{
				Code: http.StatusPreconditionFailed, Message: "stale",
				ErrorCode: "request.precondition-failed", ErrorArgs: nil, Transport: false, Invalid: false,
			},
			conformance.StatusPassed,
		},
		{
			"no errorCode",
			&conformance.ErrorResult{
				Code: http.StatusPreconditionFailed, Message: "stale",
				ErrorCode: "", ErrorArgs: nil, Transport: false, Invalid: false,
			},
			conformance.StatusFailed,
		},
		{
			"another errorCode",
			&conformance.ErrorResult{
				Code: http.StatusPreconditionFailed, Message: "stale",
				ErrorCode: "profile.conflict", ErrorArgs: nil, Transport: false, Invalid: false,
			},
			conformance.StatusFailed,
		},
		{
			"no message",
			&conformance.ErrorResult{
				Code: http.StatusPreconditionFailed, Message: "",
				ErrorCode: "request.precondition-failed", ErrorArgs: nil, Transport: false, Invalid: false,
			},
			conformance.StatusFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := runStaleCase(
				t,
				conformance.CallResult{Status: http.StatusPreconditionFailed, ETag: "", Body: nil, Error: tc.err},
			)
			if r.Status != tc.want {
				t.Fatalf("status %s (%v), want %s", r.Status, r.Err, tc.want)
			}
		})
	}
}

// TestMissingIfMatchChecksTheReasonOfTheRefusal proves that the case passes a
// client that refuses only the call with no If-Match, and fails a client that
// refuses the call for another argument.
func TestMissingIfMatchChecksTheReasonOfTheRefusal(t *testing.T) {
	t.Parallel()

	t.Run("refuses only the call with no If-Match", func(t *testing.T) {
		t.Parallel()

		if r := runIfMatchCase(t, false); r.Status != conformance.StatusPassed {
			t.Fatalf("status %s (%v), want passed", r.Status, r.Err)
		}
	})

	t.Run("refuses the call with If-Match too", func(t *testing.T) {
		t.Parallel()

		r := runIfMatchCase(t, true)
		if r.Status != conformance.StatusFailed || !errors.Is(r.Err, conformance.ErrExpectation) {
			t.Fatalf("status %s (%v), want failed with ErrExpectation", r.Status, r.Err)
		}
	})
}
