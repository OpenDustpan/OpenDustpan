// Package apidesctest proves at run time that the success status codes in the
// description of an adapter are the codes that its handlers write. It is for
// tests only: no production package imports it.
//
// CheckStatus wraps the handler of an adapter. It reports each 2xx answer
// whose code the description does not give for the route. RequireObserved
// makes the test binary fail when a described operation has no test that
// observes a success answer, unless an allowlist names the operation. The
// allowlist can only get shorter: the binary fails too when the list names an
// operation that a test observes now.
package apidesctest

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// Handler is what CheckStatus wraps: the handler of an adapter, with the
// operations that it describes and the patterns that it serves.
type Handler interface {
	http.Handler

	Operations() []apidesc.Operation
	RegisteredPatterns() []string
}

// TB is the part of testing.TB that CheckStatus uses.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Cleanup(f func())
}

// state is what the wrappers of one test binary know. A wrapper adds the
// operations that it describes to known, and each success answer adds an
// operation to observed.
type state struct {
	mu       sync.Mutex
	known    map[string]struct{}
	observed map[string]struct{}
	// late counts the wrong answers that came after the end of the test that
	// made the wrapper. The test cannot report them, so Run fails instead.
	late atomic.Int64
}

//nolint:gochecknoglobals // One record for the test binary; the wrappers of many tests add to it.
var seen = &state{mu: sync.Mutex{}, known: map[string]struct{}{}, observed: map[string]struct{}{}, late: atomic.Int64{}}

func (s *state) know(ops []apidesc.Operation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range ops {
		s.known[ops[i].ID] = struct{}{}
	}
}

func (s *state) observe(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.observed[id] = struct{}{}
}

// CheckStatus returns a handler that serves with h, and reports to t each 2xx
// answer that the description of h does not give: a code that is not in the
// set of the operation, and any 2xx answer on a route under the prefix of the
// description that has no description and is not on the list of undescribed
// routes. An answer that is not 2xx is not checked here.
func CheckStatus(t TB, h Handler) http.Handler {
	t.Helper()

	ops := h.Operations()
	byPattern := make(map[string]apidesc.Operation, len(ops))

	for i := range ops {
		byPattern[apidesc.Pattern(ops[i].Method, ops[i].Path)] = ops[i]
	}

	// The probe has the patterns of the adapter and no behavior, so Handler
	// finds the pattern that the adapter would use for a request.
	probe := http.NewServeMux()
	noop := func(http.ResponseWriter, *http.Request) {}

	for _, pattern := range h.RegisteredPatterns() {
		probe.HandleFunc(pattern, noop)
	}

	seen.know(ops)

	var finished atomic.Bool

	t.Cleanup(func() { finished.Store(true) })

	undescribed := apidesc.Undescribed()
	report := func(format string, args ...any) {
		if finished.Load() {
			seen.late.Add(1)
			fmt.Fprintf(os.Stderr, "apidesctest: after the test: "+format+"\n", args...)

			return
		}

		t.Errorf(format, args...)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := probe.Handler(r)
		sw := &statusWriter{ResponseWriter: w, status: 0}

		h.ServeHTTP(sw, r)

		status := sw.code()
		if pattern == "" || status < http.StatusOK || status >= http.StatusMultipleChoices {
			return
		}

		op, described := byPattern[pattern]

		switch {
		case described && slices.Contains(op.Statuses, status):
			seen.observe(op.ID)
		case described:
			report("%s answered %d, and the description of %s gives %v", pattern, status, op.ID, op.Statuses)
		case outsideDescription(pattern, undescribed):
		default:
			report("%s answered %d, and the description does not have the route", pattern, status)
		}
	})
}

// outsideDescription reports whether a pattern is a route that the
// description leaves out on purpose: a path outside the prefix, or a pattern
// on the list of undescribed routes.
func outsideDescription(pattern string, undescribed map[string]string) bool {
	if _, listed := undescribed[pattern]; listed {
		return true
	}

	_, path, found := strings.Cut(pattern, " ")
	if !found {
		return true
	}

	return !strings.HasPrefix(path, apidesc.Prefix)
}

// statusWriter keeps the status of the answer. It passes everything else on.
type statusWriter struct {
	http.ResponseWriter

	status int
}

// WriteHeader implements http.ResponseWriter. It ignores an informational
// status, because the final status follows it.
func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 && (status < http.StatusContinue || status >= http.StatusOK) {
		w.status = status
	}

	w.ResponseWriter.WriteHeader(status)
}

// Write implements http.ResponseWriter.
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		return n, fmt.Errorf("write the answer: %w", err)
	}

	return n, nil
}

// Flush implements http.Flusher when the writer below does.
func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the writer below, for http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// code returns the final status: the one that WriteHeader gave, or 200 when
// the handler wrote a body or nothing at all.
func (w *statusWriter) code() int {
	if w.status == 0 {
		return http.StatusOK
	}

	return w.status
}
