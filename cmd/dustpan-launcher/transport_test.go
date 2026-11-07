package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

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

// transportHandler answers the paths of TestTransport: a redirect, an answer
// above the cap, and 503 with a Retry-After for a call with the release.
func transportHandler(w http.ResponseWriter, r *http.Request) {
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
