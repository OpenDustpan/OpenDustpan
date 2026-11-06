package tofuhttp

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/requestid"
)

// patternMux is an http.ServeMux that records every pattern registered on
// it, so that RegisteredPatterns lists what the mux serves and not what the
// route table declares. The inner mux stays private, so no code in this file
// set can serve a pattern without recording it.
type patternMux struct {
	servemux *http.ServeMux

	patterns []string
}

// newPatternMux returns a patternMux ready to register routes.
func newPatternMux() *patternMux {
	return &patternMux{servemux: http.NewServeMux(), patterns: nil}
}

// HandleFunc registers pattern and records it.
func (m *patternMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.Handle(pattern, requestid.WithRoute(pattern, http.HandlerFunc(handler)))
}

// ServeHTTP implements http.Handler.
func (m *patternMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.servemux.ServeHTTP(w, r)
}

// Patterns returns a copy of the recorded patterns.
func (m *patternMux) Patterns() []string {
	return append([]string(nil), m.patterns...)
}
