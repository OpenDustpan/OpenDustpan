//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Lock and unlock methods the OpenTofu http backend sends. They are not
// standard HTTP methods, but net/http carries them as plain method names.
const (
	httpLockMethod   = "LOCK"
	httpUnlockMethod = "UNLOCK"
)

// HTTPBackendCounts holds the request counts of one HTTPBackend by method.
type HTTPBackendCounts struct {
	Gets    int
	Posts   int
	Deletes int
	Locks   int
	Unlocks int
}

// HTTPBackend is a small in-memory server for the OpenTofu http backend
// (ADR 29 source T1). It holds one state document: GET returns it, POST
// replaces it, DELETE removes it, LOCK takes the lock and UNLOCK releases it.
// It counts every request, so a test can prove the source got no write during
// an import. It uses net/http only. Use NewHTTPBackend to start it.
type HTTPBackend struct {
	mu      sync.Mutex
	server  *httptest.Server
	url     string
	state   []byte
	lock    []byte
	gets    int
	posts   int
	deletes int
	locks   int
	unlocks int
	hasDocs bool
	locked  bool
}

// NewHTTPBackend starts an HTTPBackend on a random local port. The test server
// closes when the test ends. The address, lock address and unlock address are
// the same URL: the backend dispatches on the request method, as the OpenTofu
// http backend defines.
func NewHTTPBackend(tb testing.TB) *HTTPBackend {
	tb.Helper()

	backend := new(HTTPBackend)
	server := httptest.NewServer(backend)
	backend.server = server
	backend.url = server.URL

	tb.Cleanup(server.Close)

	return backend
}

// Address returns the URL the http backend reads and writes: the value of its
// address, lock_address and unlock_address settings.
func (b *HTTPBackend) Address() string {
	return b.url
}

// Writes returns how many state writes the backend received. A later import
// test proves its source got no write with this count.
func (b *HTTPBackend) Writes() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.posts
}

// Counts returns the request counts of the backend by method.
func (b *HTTPBackend) Counts() HTTPBackendCounts {
	b.mu.Lock()
	defer b.mu.Unlock()

	return HTTPBackendCounts{
		Gets:    b.gets,
		Posts:   b.posts,
		Deletes: b.deletes,
		Locks:   b.locks,
		Unlocks: b.unlocks,
	}
}

// Config returns a project with an http backend on this server, a built-in
// terraform_data resource and a greeting output, so apply needs no provider
// download.
func (b *HTTPBackend) Config() string {
	return fmt.Sprintf(`terraform {
  backend "http" {
    address        = %q
    lock_address   = %q
    unlock_address = %q
  }
}

resource "terraform_data" "smoke" {
  input = "hello"
}

output "greeting" {
  value = "hello"
}
`, b.url, b.url, b.url)
}

// WriteProject writes Config into dir/main.tf in a fresh temporary directory
// and returns the directory, so a scenario can run the CLI in it.
func (b *HTTPBackend) WriteProject(tb testing.TB) string {
	tb.Helper()

	dir := tb.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(b.Config()), 0o600); err != nil {
		tb.Fatalf("write http backend config: %v", err)
	}

	return dir
}

// ServeHTTP dispatches one backend request by method. It is the httptest
// handler NewHTTPBackend installed.
func (b *HTTPBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		b.serveGet(w)
	case http.MethodPost, http.MethodPut:
		b.servePost(w, r)
	case http.MethodDelete:
		b.serveDelete(w)
	case httpLockMethod:
		b.serveLock(w, r)
	case httpUnlockMethod:
		b.serveUnlock(w)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveGet answers the state, or 404 when no state was posted yet.
func (b *HTTPBackend) serveGet(w http.ResponseWriter) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.gets++

	if !b.hasDocs {
		http.Error(w, "no state", http.StatusNotFound)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if _, err := w.Write(b.state); err != nil {
		http.Error(w, "write state", http.StatusInternalServerError)
	}
}

// servePost stores the posted state as the current document.
func (b *HTTPBackend) servePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read state", http.StatusInternalServerError)

		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = append([]byte(nil), body...)
	b.hasDocs = true
	b.posts++
}

// serveDelete removes the current document. It answers 200 even when no
// state was posted yet.
func (b *HTTPBackend) serveDelete(w http.ResponseWriter) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = nil
	b.hasDocs = false
	b.deletes++

	w.WriteHeader(http.StatusOK)
}

// serveLock takes the lock with the posted lock info. A second lock answers
// 409 with the holding lock info, as the http backend defines.
func (b *HTTPBackend) serveLock(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read lock", http.StatusInternalServerError)

		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.locks++

	if b.locked {
		w.WriteHeader(http.StatusConflict)

		if _, werr := w.Write(b.lock); werr != nil {
			http.Error(w, "write lock", http.StatusInternalServerError)
		}

		return
	}

	b.lock = append([]byte(nil), body...)
	b.locked = true
}

// serveUnlock releases the lock. It answers 409 when no lock is held.
func (b *HTTPBackend) serveUnlock(w http.ResponseWriter) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.unlocks++

	if !b.locked {
		http.Error(w, "not locked", http.StatusConflict)

		return
	}

	b.lock = nil
	b.locked = false
}
