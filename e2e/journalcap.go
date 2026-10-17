//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// JournalVersionCap caps the journal version that start-update answers, so a
// test can drive journal version 1 with a CLI that offers version 2. The
// server still stores the negotiated version on the update; only the answer
// the CLI reads is lowered. A version 1 CLI then journals at version 1 and
// refuses a resource registration that carries a state migration, while the
// same CLI journals at version 2 without the cap. It is safe for concurrent
// use.
type JournalVersionCap struct {
	limit  int
	mu     sync.Mutex
	capped int
}

// NewJournalVersionCap returns a cap that lowers every start-update answer
// above limit to limit.
func NewJournalVersionCap(limit int) *JournalVersionCap {
	return &JournalVersionCap{limit: limit, mu: sync.Mutex{}, capped: 0}
}

// Capped reports how many start-update answers the cap lowered.
func (c *JournalVersionCap) Capped() int {
	if c == nil {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.capped
}

// Wrap returns next with the start-update answers above the maximum lowered.
// Any other request or response passes through untouched.
func (c *JournalVersionCap) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if c == nil || req.Method != http.MethodPost || !strings.Contains(req.URL.Path, "/update/") {
			next.ServeHTTP(w, req)
			return
		}

		buf := &capWriter{ResponseWriter: w, status: http.StatusOK, called: false, body: nil}
		next.ServeHTTP(buf, req)
		buf.finish(c)
	})
}

// capWriter holds the response of one request until finish decides whether to
// rewrite it. Headers stay in the underlying map and are sent with the final
// status, so a rewritten body never keeps a stale length.
type capWriter struct {
	http.ResponseWriter

	status int
	called bool
	body   []byte
}

// WriteHeader records the status without sending it.
func (w *capWriter) WriteHeader(status int) {
	w.status = status
	w.called = true
}

// Write holds one more body chunk.
func (w *capWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}

// finish sends the held response, with the journal version lowered when it is
// above the maximum.
func (w *capWriter) finish(c *JournalVersionCap) {
	body := w.body

	if w.status == http.StatusOK {
		if lowered, ok := lowerJournalVersion(body, c.limit); ok {
			body = lowered

			c.mu.Lock()
			c.capped++
			c.mu.Unlock()
		}
	}

	w.ResponseWriter.Header().Del("Content-Length")

	if !w.called {
		w.status = http.StatusOK
	}

	w.ResponseWriter.WriteHeader(w.status)
	//nolint:errcheck // The test proxy keeps no logger; a failed write ends the response.
	_, _ = io.Copy(w.ResponseWriter, bytes.NewReader(body))
}

// lowerJournalVersion returns the body with the journal version lowered to
// limit, and whether it lowered anything. A body without a higher version, or
// without a journal version at all, passes through unchanged. The other
// members of the answer keep their values.
func lowerJournalVersion(body []byte, limit int) ([]byte, bool) {
	var answer map[string]any
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, false
	}

	version, ok := answer["journalVersion"].(float64)
	if !ok || version <= float64(limit) {
		return nil, false
	}

	answer["journalVersion"] = limit

	lowered, err := json.Marshal(answer)
	if err != nil {
		return nil, false
	}

	return lowered, true
}

// cappedHandler wraps next with the version cap, or returns next unchanged
// when the cap is nil.
func cappedHandler(next http.Handler, versionCap *JournalVersionCap) http.Handler {
	if versionCap == nil {
		return next
	}

	return versionCap.Wrap(next)
}
