//go:build e2e

package e2e

import (
	"cmp"
	"context"
	"io"
	"mime"
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
	Step      string `json:"step"`
	Method    string `json:"method"`
	Host      string `json:"host,omitempty"`
	Path      string `json:"path"`
	Query     string `json:"query,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	// Backend names the server instance that served the request, when more
	// than one instance runs behind the proxy. It is empty for one server.
	Backend      string `json:"backend,omitempty"`
	Accept       string `json:"accept,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	RequestBody  string `json:"requestBody,omitempty"`
	ResponseBody string `json:"responseBody,omitempty"`
	// ResponseHeaders holds the names of the response headers, in sorted
	// order, and "Content-Type: <media type>" for the content type. It never
	// holds a header value besides the media type, so a secret in a header
	// cannot reach a report.
	ResponseHeaders []string `json:"responseHeaders,omitempty"`
	// ResponseCut is true when the answer was longer than the bytes that
	// ResponseBody keeps, so ResponseBody is the start of the answer.
	ResponseCut   bool  `json:"responseCut,omitempty"`
	Seq           int   `json:"seq"`
	Status        int   `json:"status"`
	RequestBytes  int64 `json:"requestBytes"`
	ResponseBytes int64 `json:"responseBytes"`
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

// backendKey is the context key of the backendNote that Middleware puts on
// each request.
type backendKey struct{}

// backendNote carries the name of the instance that served a request from
// the proxy handler back to the recorder. The handler that sets it runs in
// the goroutine of the request, before ServeHTTP returns, so it needs no lock.
type backendNote struct{ name string }

// noteBackend names the instance that serves req. It does nothing for a
// request that did not pass through Middleware.
func noteBackend(req *http.Request, name string) {
	if note, ok := req.Context().Value(backendKey{}).(*backendNote); ok {
		note.name = name
	}
}

// backendOf returns the name of the instance that served req, or "" when none
// is noted.
func backendOf(req *http.Request) string {
	if note, ok := req.Context().Value(backendKey{}).(*backendNote); ok {
		return note.name
	}

	return ""
}

// Middleware records each request handled by next.
func (r *Recorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seq, step := r.arrive()
		note := &backendNote{name: ""}
		req = req.WithContext(context.WithValue(req.Context(), backendKey{}, note))
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
		Step:            step,
		Method:          req.Method,
		Host:            req.Host,
		Path:            req.URL.Path,
		Query:           req.URL.RawQuery,
		UserAgent:       req.UserAgent(),
		Backend:         backendOf(req),
		Accept:          req.Header.Get("Accept"),
		ContentType:     req.Header.Get("Content-Type"),
		RequestBody:     string(in.kept),
		ResponseBody:    string(out.kept),
		ResponseHeaders: headerNames(out.Header()),
		ResponseCut:     out.n > int64(len(out.kept)),
		Seq:             seq,
		Status:          out.status,
		RequestBytes:    in.n,
		ResponseBytes:   out.n,
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

// volatileHeader reports whether the response header name differs from answer
// to answer by design, so a list of header names does not hold it.
func volatileHeader(name string) bool {
	switch name {
	case "Date", "Content-Length", "Connection", "Keep-Alive", "Transfer-Encoding":
		return true
	default:
		return false
	}
}

// headerNames returns the sorted names of the headers in h, without the
// volatile ones, with the media type for Content-Type and no other value.
func headerNames(h http.Header) []string {
	out := make([]string, 0, len(h))

	for name := range h {
		name = http.CanonicalHeaderKey(name)
		if volatileHeader(name) {
			continue
		}

		if name == "Content-Type" {
			mediaType, _, err := mime.ParseMediaType(h.Get(name))
			if err != nil {
				mediaType = ""
			}

			name += ": " + mediaType
		}

		out = append(out, name)
	}

	slices.Sort(out)

	return out
}
