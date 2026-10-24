package dustpan

import (
	"fmt"
	"mime"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
)

// withJSONMethodNotAllowed answers the 405 of the mux of next in the error
// shape of the tool-neutral API, with the code server.method-not-allowed. The
// mux of an adapter answers a method that a path does not serve with a plain
// text 405. This wrapper replaces that body and keeps the Allow header that
// the mux sets. A 405 that next answers as JSON, and every other status, pass
// through untouched.
func withJSONMethodNotAllowed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catch := &methodNotAllowedWriter{ResponseWriter: w, refused: false}

		next.ServeHTTP(catch, r)

		if !catch.refused {
			return
		}

		w.Header().Del("Content-Length")
		writeRouteError(
			w, http.StatusMethodNotAllowed, "method not allowed", routeAnswer(errcode.ServerMethodNotAllowed),
		)
	})
}

// methodNotAllowedWriter swallows a 405 that does not carry the JSON media
// type, and its body, so that withJSONMethodNotAllowed can answer it as JSON.
type methodNotAllowedWriter struct {
	http.ResponseWriter

	refused bool
}

// WriteHeader swallows a 405 without the JSON media type and records it.
// Every other status passes through.
func (m *methodNotAllowedWriter) WriteHeader(status int) {
	if status == http.StatusMethodNotAllowed && !isJSON(m.Header().Get("Content-Type")) {
		m.refused = true

		return
	}

	m.ResponseWriter.WriteHeader(status)
}

// Write swallows the body of a swallowed 405. Every other body passes
// through.
func (m *methodNotAllowedWriter) Write(p []byte) (int, error) {
	if m.refused {
		return len(p), nil
	}

	n, err := m.ResponseWriter.Write(p)
	if err != nil {
		return n, fmt.Errorf("write the answer: %w", err)
	}

	return n, nil
}

// Unwrap returns the writer below, so that http.ResponseController reaches
// it.
func (m *methodNotAllowedWriter) Unwrap() http.ResponseWriter { return m.ResponseWriter }

// isJSON reports whether contentType is the JSON media type, with or without
// parameters.
func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)

	return err == nil && mediaType == "application/json"
}
