//go:build e2e

package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

func TestRecorderKeepsHeaderNamesAndMarksACutAnswer(t *testing.T) {
	t.Parallel()

	rec := new(e2e.Recorder)
	handler := rec.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Set-Cookie", "session=secret-value")
		w.Header().Set("Location", "/somewhere-secret")
		w.Header().Set("Date", "today")

		if r.URL.Path == "/long" {
			writeBody(w, `{"a":"`+strings.Repeat("x", 5000)+`"}`)

			return
		}

		writeBody(w, `{"a":1}`)
	}))

	for _, path := range []string{"/short", "/long"} {
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody),
		)
	}

	entries := rec.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	want := []string{"Content-Type: application/json", "Location", "Set-Cookie"}
	if !slices.Equal(entries[0].ResponseHeaders, want) {
		t.Errorf("headers = %v, want %v (names only, no Date)", entries[0].ResponseHeaders, want)
	}

	for _, entry := range entries {
		for _, header := range entry.ResponseHeaders {
			if strings.Contains(header, "secret") {
				t.Errorf("header %q holds a value", header)
			}
		}
	}

	if entries[0].ResponseCut || !entries[1].ResponseCut {
		t.Errorf("cut = %t and %t, want false and true", entries[0].ResponseCut, entries[1].ResponseCut)
	}
}

func writeBody(w http.ResponseWriter, body string) {
	if _, err := w.Write([]byte(body)); err != nil {
		panic(err)
	}
}
