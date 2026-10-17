//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// answerRewrites holds the answer rewrites of one recording proxy, by
// request path. A rewrite changes the body of every answer to a GET of its
// path, so a test can show a client an answer that the server cannot give in
// its current state. The zero value has no rewrite.
type answerRewrites struct {
	mu     sync.Mutex
	byPath map[string]func(body []byte) []byte
}

// set puts fn on path and returns the func that removes it.
func (a *answerRewrites) set(path string, fn func([]byte) []byte) func() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.byPath == nil {
		a.byPath = map[string]func([]byte) []byte{}
	}

	a.byPath[path] = fn

	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()

		delete(a.byPath, path)
	}
}

// lookup returns the rewrite of path, or nil.
func (a *answerRewrites) lookup(path string) func([]byte) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.byPath[path]
}

// modify is the ModifyResponse hook of the proxy: it applies the rewrite of
// the request path to the body of a GET answer.
func (a *answerRewrites) modify(resp *http.Response) error {
	if resp.Request == nil || resp.Request.Method != http.MethodGet {
		return nil
	}

	fn := a.lookup(resp.Request.URL.Path)
	if fn == nil {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		return fmt.Errorf("close answer to rewrite: %w", closeErr)
	}

	if err != nil {
		return fmt.Errorf("read answer to rewrite: %w", err)
	}

	rewritten := fn(body)

	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))

	return nil
}

// RewriteAnswer makes the recording proxy pass every answer to a GET of path
// through fn, until the returned func runs. The real client command still
// runs against the real server; only the answer body changes. It needs
// E2E_SERVER=binary.
func (e *Env) RewriteAnswer(tb testing.TB, path string, fn func(body []byte) []byte) func() {
	tb.Helper()

	if e.rewrites == nil {
		tb.Fatalf("RewriteAnswer needs E2E_SERVER=binary")
	}

	return e.rewrites.set(path, fn)
}

// CheckNoOwnDatabaseHandle fails tb when the test process holds the SQLite
// file of the binary server open. A test calls it after it reads or changes
// the database, to prove that it closed every handle. It does nothing on
// PostgreSQL.
func (e *Env) CheckNoOwnDatabaseHandle(tb testing.TB) {
	tb.Helper()

	if path, ok := strings.CutPrefix(e.backendCfg.dbURL, "sqlite:"); ok {
		checkNoOwnSQLiteHandle(tb, path)
	}
}
