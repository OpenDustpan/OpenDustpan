//go:build e2e

package e2e

import (
	"cmp"
	"io"
	"net/http"
	"slices"
	"sync"
)

// maxCapturedBody bounds the request and response bytes kept per entry.
const maxCapturedBody = 4096

// Entry is one HTTP request the CLI made and how the server answered.
//
// Authorization headers are never recorded. Bodies are, truncated, because the
// point of the log is to see the shapes the CLI sends and expects; the report
// directory is therefore git-ignored and must not be published for scenarios
// that carry real secrets.
type Entry struct {
	Step          string `json:"step"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	Query         string `json:"query,omitempty"`
	UserAgent     string `json:"userAgent,omitempty"`
	Accept        string `json:"accept,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	RequestBody   string `json:"requestBody,omitempty"`
	ResponseBody  string `json:"responseBody,omitempty"`
	Seq           int    `json:"seq"`
	Status        int    `json:"status"`
	RequestBytes  int64  `json:"requestBytes"`
	ResponseBytes int64  `json:"responseBytes"`
}

// Recorder logs every request that passes through its middleware.
type Recorder struct {
	step    string
	entries []Entry
	arrived int
	mu      sync.Mutex
}

// SetStep labels the requests recorded from now on.
func (r *Recorder) SetStep(step string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.step = step
}

// Entries returns a copy of the log so far, in arrival order (by Seq).
func (r *Recorder) Entries() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := append([]Entry(nil), r.entries...)
	slices.SortFunc(out, func(a, b Entry) int { return cmp.Compare(a.Seq, b.Seq) })

	return out
}

// Middleware records each request handled by next.
func (r *Recorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seq, step := r.arrive()
		in := &capture{inner: req.Body, kept: nil, n: 0}
		req.Body = in
		out := &captureWriter{ResponseWriter: w, kept: nil, n: 0, status: http.StatusOK}

		// Record in a defer, so a handler that panics still shows in the log,
		// as the server error it is; then let net/http handle the panic.
		defer func() {
			if p := recover(); p != nil {
				out.status = http.StatusInternalServerError
				r.record(seq, step, req, in, out)
				panic(p)
			}
		}()

		next.ServeHTTP(out, req)
		r.record(seq, step, req, in, out)
	})
}

// RedactSteps replaces the captured bodies of steps with a marker, so a
// secret the CLI sent or received never reaches the report. Method, path,
// status and step names stay, so the request log still shows what ran.
func (r *Recorder) RedactSteps(steps ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.entries {
		if slices.Contains(steps, r.entries[i].Step) {
			r.entries[i].RequestBody = redacted
			r.entries[i].ResponseBody = redacted
		}
	}
}

// record appends one finished request to the log.
func (r *Recorder) record(seq int, step string, req *http.Request, in *capture, out *captureWriter) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, Entry{
		Step:          step,
		Method:        req.Method,
		Path:          req.URL.Path,
		Query:         req.URL.RawQuery,
		UserAgent:     req.UserAgent(),
		Accept:        req.Header.Get("Accept"),
		ContentType:   req.Header.Get("Content-Type"),
		RequestBody:   string(in.kept),
		ResponseBody:  string(out.kept),
		Seq:           seq,
		Status:        out.status,
		RequestBytes:  in.n,
		ResponseBytes: out.n,
	})
}

// arrive numbers a request and labels it with the current step.
func (r *Recorder) arrive() (int, string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.arrived++

	return r.arrived, r.step
}

// capture wraps a request body and remembers its first bytes.
type capture struct {
	inner io.ReadCloser
	kept  []byte
	n     int64
}

func (c *capture) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	c.n += int64(n)
	c.kept = keep(c.kept, p[:n])

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see the original error, including io.EOF.
}

func (c *capture) Close() error {
	return c.inner.Close() //nolint:wrapcheck // Transparent wrapper.
}

// captureWriter wraps a ResponseWriter and remembers the status and first bytes.
type captureWriter struct {
	http.ResponseWriter

	kept   []byte
	n      int64
	status int
}

func (w *captureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.n += int64(n)
	w.kept = keep(w.kept, p[:n])

	return n, err //nolint:wrapcheck // Transparent wrapper.
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *captureWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// keep appends p to dst without growing past maxCapturedBody.
func keep(dst, p []byte) []byte {
	if room := maxCapturedBody - len(dst); room > 0 {
		dst = append(dst, p[:min(room, len(p))]...)
	}

	return dst
}
