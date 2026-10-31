package tfe

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteErrorNamespaceSentinels proves each namespace sentinel answers its
// own JSON:API error, never a 500: a fenced write answers 409, a namespace
// whose primary installation is another installation answers 421, and a database that does not answer 503 with
// a Retry-After time.
func TestWriteErrorNamespaceSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		want       int
		retryAfter bool
	}{
		{name: "fenced", err: namespace.ErrFenced, want: http.StatusConflict, retryAfter: false},
		{name: "not home", err: namespace.ErrNotHome, want: http.StatusMisdirectedRequest, retryAfter: false},
		{name: "unavailable", err: namespace.ErrUnavailable, want: http.StatusServiceUnavailable, retryAfter: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := writeErrorOf(fmt.Errorf("write workspace: %w", tc.err))

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}

			if got := rec.Header().Get("Retry-After") != ""; got != tc.retryAfter {
				t.Fatalf("Retry-After set = %t, want %t", got, tc.retryAfter)
			}

			if tc.retryAfter && rec.Header().Get("Retry-After") != "5" {
				t.Fatalf("Retry-After = %q, want 5", rec.Header().Get("Retry-After"))
			}

			checkErrorDocument(t, rec, tc.want, tc.err.Error())
		})
	}
}

// writeErrorOf answers err through a handler that holds only a logger, the
// one field writeError reads.
func writeErrorOf(err error) *httptest.ResponseRecorder {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)

	rec := httptest.NewRecorder()
	h.writeError(context.Background(), rec, err)

	return rec
}

// checkErrorDocument proves rec holds one JSON:API error with status want and
// a detail that holds text.
func checkErrorDocument(t *testing.T, rec *httptest.ResponseRecorder, want int, text string) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); got != jsonAPIMediaType {
		t.Fatalf("content type = %q, want %q", got, jsonAPIMediaType)
	}

	var doc jsonAPIDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error document: %v", err)
	}

	if len(doc.Errors) != 1 || doc.Errors[0].Status != strconv.Itoa(want) {
		t.Fatalf("errors = %+v, want one error with status %d", doc.Errors, want)
	}

	if !strings.Contains(doc.Errors[0].Detail, text) {
		t.Fatalf("detail = %q, want it to hold %q", doc.Errors[0].Detail, text)
	}
}
