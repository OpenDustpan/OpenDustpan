package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
)

func TestTransport(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(transportHandler))
	t.Cleanup(srv.Close)

	tr, err := newTransport(srv.URL)
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}

	tr.client.Transport = srv.Client().Transport

	resp, err := tr.Do(t.Context(), &session.Request{
		Method: http.MethodPost, Path: "/runner/v1/session/renew", Body: nil,
		Header: map[string]string{"Dustpan-Release": "0.0.1"},
	})
	if err != nil || resp.Status != http.StatusServiceUnavailable || resp.RetryAfter.Seconds() != 7 {
		t.Fatalf("Do = %+v, %v; want 503 with a wait of 7s", resp, err)
	}

	for _, path := range []string{"/big", "/redirect", "//elsewhere.example.com/x", "relative", "@evil/x"} {
		req := &session.Request{Method: http.MethodGet, Path: path, Body: nil, Header: nil}
		if resp, err := tr.Do(t.Context(), req); err == nil {
			t.Errorf("Do(%q) = %+v, want an error", path, resp)
		}
	}

	for _, base := range []string{"http://dustpan.example.com", "dustpan.example.com", "https://"} {
		if _, err := newTransport(base); err == nil {
			t.Errorf("newTransport(%q) = nil error", base)
		}
	}
}

// TestRetryAfterCap checks the wait that the transport reads from a
// Retry-After header. A wait above maxRetryAfter gives the cap, also a value
// that overflows the parser. A missing or negative value gives 0.
func TestRetryAfterCap(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(transportHandler))
	t.Cleanup(srv.Close)

	tr, err := newTransport(srv.URL)
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}

	tr.client.Transport = srv.Client().Transport

	cases := []struct {
		path string
		want time.Duration
	}{
		{"/retry/far", maxRetryAfter},
		{"/retry/overflow", maxRetryAfter},
		{"/retry/at-cap", maxRetryAfter},
		{"/retry/short", 5 * time.Second},
		{"/retry/negative", 0},
		{"/retry/negative-overflow", 0},
		{"/retry/bad", 0},
		{"/retry/none", 0},
	}

	for _, tc := range cases {
		resp, err := tr.Do(t.Context(), &session.Request{
			Method: http.MethodPost, Path: tc.path, Body: nil,
			Header: map[string]string{"Dustpan-Release": "0.0.1"},
		})
		if err != nil {
			t.Fatalf("Do(%q): %v", tc.path, err)
		}

		if resp.RetryAfter != tc.want {
			t.Errorf("Do(%q).RetryAfter = %s, want %s", tc.path, resp.RetryAfter, tc.want)
		}
	}
}

// retryHeader returns the Retry-After value of a path of TestRetryAfterCap,
// and whether the path is one of them. An empty value sends no header.
func retryHeader(path string) (string, bool) {
	switch path {
	case "/retry/far":
		return "86400", true
	case "/retry/overflow":
		return "99999999999999999999", true
	case "/retry/at-cap":
		return "30", true
	case "/retry/short":
		return "5", true
	case "/retry/negative":
		return "-5", true
	case "/retry/negative-overflow":
		return "-99999999999999999999", true
	case "/retry/bad":
		return "soon", true
	case "/retry/none":
		return "", true
	default:
		return "", false
	}
}

// transportHandler answers the paths of TestTransport: a redirect, an answer
// above the cap, and 503 with a Retry-After for a call with the release. The
// paths of TestRetryAfterCap answer 503 with the header of retryHeader.
func transportHandler(w http.ResponseWriter, r *http.Request) {
	if value, ok := retryHeader(r.URL.Path); ok {
		if value != "" {
			w.Header().Set("Retry-After", value)
		}

		w.WriteHeader(http.StatusServiceUnavailable)

		return
	}

	switch r.URL.Path {
	case "/redirect":
		http.Redirect(w, r, "https://elsewhere.example.com/", http.StatusFound)
	case "/big":
		if _, err := w.Write(bytes.Repeat([]byte("x"), maxAnswer+1)); err != nil {
			return
		}
	default:
		if r.Header.Get("Dustpan-Release") != "0.0.1" {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}
