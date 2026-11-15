package jobs_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
)

// runningLogHandler is a handler for a plan phase that never ends, with the
// log options opts.
func runningLogHandler(
	t *testing.T, opts *jobsapi.LogOptions, authz *recordingAuthorizer,
) testHandler {
	t.Helper()

	jobs := newFakeJobs(logJob(t, 6, phaseRunning))
	logs := newFakeLogs(fakeLog{data: "one\ntw", bad: "", err: nil})

	return newWorkerHandler(t, authz, handlerDeps{
		jobs: jobs, units: nil, starter: nil, entries: nil, artifacts: logs, logs: opts, cursors: nil,
	}, nil)
}

func logOptions(mutate func(o *jobsapi.LogOptions)) *jobsapi.LogOptions {
	o := fastLogs()
	mutate(o)

	return o
}

func TestLogStreamEndsAtItsMaxLife(t *testing.T) {
	t.Parallel()

	h := runningLogHandler(t, logOptions(func(o *jobsapi.LogOptions) { o.MaxLife = 40 * time.Millisecond }),
		newAuthorizer(logHolds()...))

	rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan", token: testToken})
	lines := decodeLines(t, rec.Body.String())
	last := lines[len(lines)-1]

	if len(lines) != 2 || lines[0].Text != "one\n" || last.Done || last.Last != 1 ||
		!strings.Contains(rec.Body.String(), `"resume":true`) {
		t.Errorf("body = %s, want one entry and a resume line at 1", rec.Body.String())
	}
}

func TestLogStreamEndsWhenTheServerStops(t *testing.T) {
	t.Parallel()

	var stopping bool

	var mu sync.Mutex

	opts := logOptions(func(o *jobsapi.LogOptions) {
		o.Stopping = func() bool {
			mu.Lock()
			defer mu.Unlock()

			return stopping
		}
	})
	h := runningLogHandler(t, opts, newAuthorizer(logHolds()...))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	reader := openServerStream(t, srv).reader

	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read the first entry: %v", err)
	}

	mu.Lock()
	stopping = true
	mu.Unlock()

	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), `"resume":true`) {
		t.Errorf("rest = %q %v, want a resume line", rest, err)
	}
}

// testStream is an open log stream of a test server.
type testStream struct {
	status int
	reader *bufio.Reader
	body   io.ReadCloser
}

// openServerStream opens the plan log on the test server and registers the
// close of the stream.
func openServerStream(t *testing.T, srv *httptest.Server) *testStream {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+logURL+"plan", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "token "+testToken)

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Logf("close the stream: %v", closeErr)
		}
	})

	return &testStream{status: resp.StatusCode, reader: bufio.NewReader(resp.Body), body: resp.Body}
}

// deadlineWriter records the write deadlines that the handler sets, and can
// fail every write.
type deadlineWriter struct {
	*httptest.ResponseRecorder

	mu        sync.Mutex
	deadlines []time.Time
	failWrite bool
}

var errClientGone = errors.New("the client does not read")

func (w *deadlineWriter) SetWriteDeadline(t time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.deadlines = append(w.deadlines, t)

	return nil
}

func (w *deadlineWriter) Write(p []byte) (int, error) {
	if w.failWrite {
		return 0, errClientGone
	}

	n, err := w.ResponseRecorder.Write(p)
	if err != nil {
		return n, errClientGone
	}

	return n, nil
}

func newDeadlineWriter(fail bool) *deadlineWriter {
	return &deadlineWriter{ResponseRecorder: httptest.NewRecorder(), mu: sync.Mutex{}, deadlines: nil, failWrite: fail}
}

func logRequest(t *testing.T) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, logURL+"plan", http.NoBody)
	req.Header.Set("Authorization", "token "+testToken)

	return req
}

func TestLogStreamSetsAWriteDeadline(t *testing.T) {
	t.Parallel()

	opts := logOptions(func(o *jobsapi.LogOptions) { o.WriteTimeout = 7 * time.Second })
	jobs := newFakeJobs(logJob(t, 15, phaseEnded))
	logs := newFakeLogs(fakeLog{data: "one\ntwo\npartial", bad: "", err: nil})
	h := newWorkerHandler(t, newAuthorizer(logHolds()...), handlerDeps{
		jobs: jobs, units: nil, starter: nil, entries: nil, artifacts: logs, logs: opts, cursors: nil,
	}, nil)

	w := newDeadlineWriter(false)
	before := time.Now()

	h.ServeHTTP(w, logRequest(t))

	if len(w.deadlines) < 2 {
		t.Fatalf("deadlines set = %d, want one before each write and flush", len(w.deadlines))
	}

	for _, d := range w.deadlines {
		if d.Before(before.Add(6*time.Second)) || d.After(time.Now().Add(8*time.Second)) {
			t.Errorf("deadline %v is not about 7 seconds ahead", d)
		}
	}
}

func TestLogStreamEndsWhenAWriteFails(t *testing.T) {
	t.Parallel()

	h := runningLogHandler(t, fastLogs(), newAuthorizer(logHolds()...))
	done := make(chan struct{})

	go func() {
		defer close(done)

		h.ServeHTTP(newDeadlineWriter(true), logRequest(t))
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stream whose writes fail kept running")
	}
}

func TestLogStreamChecksTheGrantsAgain(t *testing.T) {
	t.Parallel()

	authz := newAuthorizer(logHolds()...)
	srv := httptest.NewServer(runningLogHandler(t, fastLogs(), authz))
	t.Cleanup(srv.Close)

	reader := openServerStream(t, srv).reader

	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read the first entry: %v", err)
	}

	authz.revoke(string(identity.ActionJobReadLogs) + " opentofu:acme/net")

	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	lines := decodeLines(t, string(rest))
	last := lines[len(lines)-1]

	if last.Done || last.Error.Reason != errcode.AccessDenied {
		t.Errorf("last line = %+v, want an access.denied error line", last)
	}
}

func TestLogStreamLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(o *jobsapi.LogOptions)
	}{
		{"per principal", func(o *jobsapi.LogOptions) { o.PerPrincipal = 1 }},
		{"server", func(o *jobsapi.LogOptions) { o.Total = 1 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(runningLogHandler(t, logOptions(tc.mutate), newAuthorizer(logHolds()...)))
			t.Cleanup(srv.Close)

			first := openServerStream(t, srv)
			if first.status != http.StatusOK {
				t.Fatalf("first stream = %d", first.status)
			}

			second := openServerStream(t, srv)

			var body struct {
				ErrorCode string `json:"errorCode"`
			}

			if err := json.NewDecoder(second.reader).Decode(&body); err != nil {
				t.Fatal(err)
			}

			if second.status != http.StatusTooManyRequests || body.ErrorCode != errcode.JobLimit {
				t.Fatalf("second stream = %d %q, want 429 job.limit", second.status, body.ErrorCode)
			}

			// Closing the first stream gives its slot back.
			if err := first.body.Close(); err != nil {
				t.Fatal(err)
			}

			waitForASlot(t, srv)
		})
	}
}

// waitForASlot opens streams until one is served: the slot of a closed stream
// comes back when the handler sees the close.
func waitForASlot(t *testing.T, srv *httptest.Server) {
	t.Helper()

	deadline := time.After(5 * time.Second)

	for {
		if openServerStream(t, srv).status == http.StatusOK {
			return
		}

		select {
		case <-deadline:
			t.Fatal("no slot came back")
		case <-time.After(10 * time.Millisecond):
		case <-t.Context().Done():
			return
		}
	}
}

func TestLogSplitsOnARuneBoundary(t *testing.T) {
	t.Parallel()

	// The 16384th byte is the second byte of the rune é.
	line := strings.Repeat("a", 16383) + "é" + strings.Repeat("b", 10) + "\n"
	h := logHandler(t, logHolds(), newFakeJobs(logJob(t, int64(len(line)), phaseEnded)),
		newFakeLogs(fakeLog{data: line, bad: "", err: nil}))

	rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan", token: testToken})
	lines := decodeLines(t, rec.Body.String())

	var joined strings.Builder

	for _, l := range lines[:len(lines)-1] {
		if strings.ContainsRune(l.Text, '�') {
			t.Fatal("an entry holds a broken rune")
		}

		joined.WriteString(l.Text)
	}

	if joined.String() != line || len(lines) != 3 {
		t.Errorf("%d lines, joined %d bytes, want 2 entries that join to the line", len(lines), joined.Len())
	}
}
