package apidesctest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// reporter is a TB that keeps the errors instead of failing the test.
type reporter struct {
	errors   []string
	cleanups []func()
}

func (r *reporter) Helper() {}

func (r *reporter) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *reporter) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }

// adapter is a handler with a fixed set of operations. Each pattern answers
// the status in the table.
type adapter struct {
	mux      *http.ServeMux
	ops      []apidesc.Operation
	patterns []string
}

func newAdapter(answers map[string]int, ops ...apidesc.Operation) *adapter {
	a := &adapter{mux: http.NewServeMux(), ops: ops, patterns: nil}

	for pattern, status := range answers {
		a.patterns = append(a.patterns, pattern)
		a.mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		})
	}

	slices.Sort(a.patterns)

	return a
}

func (a *adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.mux.ServeHTTP(w, r) }
func (a *adapter) Operations() []apidesc.Operation                  { return a.ops }
func (a *adapter) RegisteredPatterns() []string                     { return a.patterns }
func operation(id, method, path string, statuses ...int) apidesc.Operation {
	return apidesc.Operation{
		ID: id, Method: method, Path: path, Statuses: statuses,
		Tags: nil, Actions: nil, ActionObjects: nil, Public: false, IdempotencyKey: false, IfMatch: false,
		IfMatchRequired: false, ETag: false, Query: nil, Bodies: nil,
	}
}

func call(t *testing.T, h http.Handler, method, path string) int {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, nil))

	return rec.Code
}

func TestCheckStatusReportsACodeThatTheDescriptionDoesNotGive(t *testing.T) {
	t.Parallel()

	const (
		put  = "PUT /api/dustpan/v1/things/{id}"
		post = "POST /api/dustpan/v1/things"
	)

	a := newAdapter(
		map[string]int{put: http.StatusCreated, post: http.StatusAccepted},
		operation("putThing", "PUT", "/api/dustpan/v1/things/{id}", 200, 201),
		operation("postThing", "POST", "/api/dustpan/v1/things", 201),
	)

	r := &reporter{errors: nil, cleanups: nil}
	h := CheckStatus(r, a)

	if got := call(t, h, "PUT", "/api/dustpan/v1/things/a"); got != http.StatusCreated {
		t.Fatalf("PUT = %d, want the status of the adapter", got)
	}

	if len(r.errors) != 0 {
		t.Fatalf("a described code was reported: %v", r.errors)
	}

	call(t, h, "POST", "/api/dustpan/v1/things")

	if len(r.errors) != 1 || !strings.Contains(r.errors[0], "postThing") || !strings.Contains(r.errors[0], "202") {
		t.Fatalf("errors = %v, want one that names postThing and 202", r.errors)
	}
}

func TestCheckStatusReportsASuccessAnswerOnARouteWithNoDescription(t *testing.T) {
	t.Parallel()

	a := newAdapter(map[string]int{
		"GET /api/dustpan/v1/hidden":      http.StatusOK,
		"GET /other/path":                 http.StatusOK,
		"GET /api/dustpan/v1/only-errors": http.StatusNotFound,
	})
	r := &reporter{errors: nil, cleanups: nil}
	h := CheckStatus(r, a)

	call(t, h, "GET", "/other/path")
	call(t, h, "GET", "/api/dustpan/v1/only-errors")

	if len(r.errors) != 0 {
		t.Fatalf("errors = %v, want none for a route outside the prefix and for an error", r.errors)
	}

	call(t, h, "GET", "/api/dustpan/v1/hidden")

	if len(r.errors) != 1 || !strings.Contains(r.errors[0], "GET /api/dustpan/v1/hidden") {
		t.Fatalf("errors = %v, want one for the route with no description", r.errors)
	}
}

func TestCheckStatusReadsTheImplicitStatus(t *testing.T) {
	t.Parallel()

	a := &adapter{
		mux: http.NewServeMux(), patterns: []string{"GET /api/dustpan/v1/body"},
		ops: []apidesc.Operation{operation("getBody", "GET", "/api/dustpan/v1/body", 200)},
	}
	a.mux.HandleFunc("GET /api/dustpan/v1/body", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x")) //nolint:errcheck // The recorder does not fail.
	})

	r := &reporter{errors: nil, cleanups: nil}
	h := CheckStatus(r, a)

	if got := call(t, h, "GET", "/api/dustpan/v1/body"); got != http.StatusOK || len(r.errors) != 0 {
		t.Fatalf("status = %d, errors = %v; a write with no header is 200", got, r.errors)
	}

	if !slices.Contains(Observed(), "getBody") {
		t.Errorf("observed = %v, want getBody", Observed())
	}
}

func TestCheckStatusPassesTheFlusherOn(t *testing.T) {
	t.Parallel()

	a := &adapter{
		mux: http.NewServeMux(), patterns: []string{"GET /api/dustpan/v1/stream"},
		ops: []apidesc.Operation{operation("streamIt", "GET", "/api/dustpan/v1/stream", 200)},
	}
	a.mux.HandleFunc("GET /api/dustpan/v1/stream", func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	r := &reporter{errors: nil, cleanups: nil}
	h := CheckStatus(r, a)

	if got := call(t, h, "GET", "/api/dustpan/v1/stream"); got != http.StatusOK {
		t.Fatalf("status = %d, want 200: the wrapper must let the handler flush", got)
	}
}

func TestObservationProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                       string
		known, observed, allowlist []string
		want                       int
	}{
		{"all observed", []string{"a", "b"}, []string{"a", "b"}, nil, 0},
		{"unobserved and listed", []string{"a", "b"}, []string{"a"}, []string{"b"}, 0},
		{"unobserved and not listed", []string{"a", "b"}, []string{"a"}, nil, 1},
		{"listed and observed", []string{"a", "b"}, []string{"a", "b"}, []string{"b"}, 1},
		{"listed and unknown", []string{"a"}, []string{"a"}, []string{"gone"}, 1},
		{"two problems", []string{"a", "b", "c"}, []string{"a"}, []string{"a"}, 3},
	}

	for _, tt := range tests {
		if got := observationProblems(tt.known, tt.observed, tt.allowlist); len(got) != tt.want {
			t.Errorf("%s: problems = %v, want %d", tt.name, got, tt.want)
		}
	}
}

// Observed returns the operations that a wrapper saw with a success answer.
func Observed() []string {
	seen.mu.Lock()
	defer seen.mu.Unlock()

	return keys(seen.observed)
}
