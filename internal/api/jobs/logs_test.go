package jobs_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// fakeLogs is the artifact reader of the tests. A log is its bytes, and the
// error that follows them.
type fakeLogs struct {
	mu   sync.Mutex
	logs map[string]fakeLog
}

type fakeLog struct {
	data string
	// bad is read together with the error, as a chunk that fails its check
	// does: its bytes come with the error.
	bad string
	err error
}

func (f *fakeLogs) OpenPlanPart(context.Context, namespace.Name, string, job.OutputKind) (io.ReadCloser, error) {
	return nil, errUnexpected
}

func (f *fakeLogs) OpenLog(_ context.Context, _ namespace.Name, version string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	l, ok := f.logs[version]
	if !ok {
		return nil, job.ErrNotFound
	}

	return &fakeLogReader{data: l.data, bad: l.bad, err: l.err}, nil
}

func (f *fakeLogs) put(version string, l fakeLog) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.logs[version] = l
}

type fakeLogReader struct {
	data, bad string
	err       error
}

func (r *fakeLogReader) Read(p []byte) (int, error) {
	if r.data != "" {
		n := copy(p, r.data)
		r.data = r.data[n:]

		return n, nil
	}

	if r.err != nil {
		n := copy(p, r.bad)
		err := r.err
		r.err = nil

		return n, err
	}

	return 0, io.EOF
}

func (*fakeLogReader) Close() error { return nil }

// newFakeLogs returns a reader that holds the log version jlv-1, the version of
// the step record of runningJob.
func newFakeLogs(l fakeLog) *fakeLogs {
	return &fakeLogs{mu: sync.Mutex{}, logs: map[string]fakeLog{"jlv-1": l}}
}

// logJob is runningJob with the plan phase in the given state.
func logJob(t *testing.T, size int64, state phaseState) job.Job {
	t.Helper()

	j := runningJob(t, testJob)
	p := &j.Phases[0]
	p.Steps[0].Log.Size = size

	if state == phaseRunning {
		p.Steps[0].Log.ClosedAt = time.Time{}
		p.FinishedAt = time.Time{}
	}

	return j
}

// phaseState is the state of the plan phase of logJob.
type phaseState int

const (
	// phaseRunning is a phase with an open log.
	phaseRunning phaseState = iota
	// phaseEnded is a phase that ended, with a closed log.
	phaseEnded
)

const logURL = "/api/dustpan/v1/namespaces/acme/jobs/" + testJob + "/logs/"

// logHolds are the checks of a caller that can read the job and its logs.
func logHolds() []string {
	return append(holdsBoth(), string(identity.ActionJobReadLogs)+" opentofu:acme/net")
}

func logHandler(t *testing.T, holds []string, jobs *fakeJobs, logs *fakeLogs) testHandler {
	t.Helper()

	return newWorkerHandler(
		t,
		newAuthorizer(holds...),
		handlerDeps{
			jobs: jobs, units: nil, starter: nil, entries: nil, artifacts: logs, logs: fastLogs(), cursors: nil,
		},
		nil,
	)
}

type logLine struct {
	Seq       int64        `json:"seq"`
	Step      string       `json:"step"`
	Text      string       `json:"text"`
	Done      bool         `json:"done"`
	Last      int64        `json:"last"`
	ErrorCode string       `json:"errorCode"`
	Error     logLineError `json:"error"`
}

type logLineError struct {
	Reason string `json:"reason"`
}

func decodeLines(t *testing.T, body string) []logLine {
	t.Helper()

	out := []logLine{}

	for raw := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		var l logLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("decode line %q: %v", raw, err)
		}

		out = append(out, l)
	}

	return out
}

func TestLogRouteIsDeclared(t *testing.T) {
	t.Parallel()

	h := logHandler(t, nil, newFakeJobs(), newFakeLogs(fakeLog{data: "", bad: "", err: nil}))

	for _, rt := range h.Routes() {
		if rt.Path != "/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/logs/{phase}" {
			continue
		}

		if rt.Method != http.MethodGet || rt.Scope != 2 ||
			!slices.Equal(rt.Reads, []identity.Action{identity.ActionJobRead}) ||
			!slices.Equal(rt.Actions, []identity.Action{identity.ActionJobReadLogs}) {
			t.Errorf("log route = %+v", rt)
		}

		return
	}

	t.Fatal("the handler serves no log route")
}

func TestLogRouteNeedsAnArtifactReader(t *testing.T) {
	t.Parallel()

	h := newHandler(t, newAuthorizer(logHolds()...), newFakeJobs(logJob(t, 4, phaseEnded)))
	rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan", token: testToken})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestLogStream(t *testing.T) {
	t.Parallel()

	logs := newFakeLogs(fakeLog{data: "one\ntwo\npartial", bad: "", err: nil})
	h := logHandler(t, logHolds(), newFakeJobs(logJob(t, 15, phaseEnded)), logs)

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"whole log", "", []string{"one\n", "two\n", "partial"}},
		{"resume", "?after=1", []string{"two\n", "partial"}},
		{"resume at the end", "?after=3", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan" + tc.query, token: testToken})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
			}

			lines := decodeLines(t, rec.Body.String())
			last := lines[len(lines)-1]

			if !last.Done || last.Last != 3 {
				t.Errorf("last line = %+v, want done at 3", last)
			}

			got := make([]string, 0, len(lines))
			for _, l := range lines[:len(lines)-1] {
				got = append(got, l.Text)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("texts = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLogSplitsALongLine(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 40*1024) + "\n"
	h := logHandler(t, logHolds(), newFakeJobs(logJob(t, int64(len(long)), phaseEnded)),
		newFakeLogs(fakeLog{data: long, bad: "", err: nil}))

	rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan", token: testToken})
	lines := decodeLines(t, rec.Body.String())

	var joined strings.Builder
	for _, l := range lines[:len(lines)-1] {
		joined.WriteString(l.Text)
	}

	if len(lines) != 4 || joined.String() != long {
		t.Errorf("%d lines, joined length %d, want 3 entries of %d bytes", len(lines), joined.Len(), len(long))
	}
}

func TestLogOfAPhaseThatDidNotStart(t *testing.T) {
	t.Parallel()

	h := logHandler(
		t,
		logHolds(),
		newFakeJobs(runningJob(t, testJob)),
		newFakeLogs(fakeLog{data: "", bad: "", err: nil}),
	)

	rec := serve(t, h, request{method: http.MethodGet, path: logURL + "apply", token: testToken})

	var body listAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if rec.Code != http.StatusNotFound || body.ErrorCode != errcode.JobLogNotAvailable {
		t.Errorf("apply log = %d %s, want 404 job.log-not-available", rec.Code, rec.Body.String())
	}

	for _, phase := range []string{"bogus", "PLAN", "plan%2F..", "deliver"} {
		rec = serve(t, h, request{method: http.MethodGet, path: logURL + phase, token: testToken})
		if rec.Code != http.StatusNotFound {
			t.Errorf("phase %q = %d, want 404", phase, rec.Code)
		}
	}
}

func TestLogOfAnotherNamespaceIsNotFound(t *testing.T) {
	t.Parallel()

	holds := append(logHolds(), string(identity.ActionNamespaceRead)+" other",
		string(identity.ActionJobRead)+" opentofu:other/net", string(identity.ActionJobReadLogs)+" opentofu:other/net")
	h := logHandler(t, holds, newFakeJobs(logJob(t, 4, phaseEnded)),
		newFakeLogs(fakeLog{data: "one\n", bad: "", err: nil}))

	for _, ns := range []string{"other", "absent"} {
		rec := serve(t, h, request{
			method: http.MethodGet, path: "/api/dustpan/v1/namespaces/" + ns + "/jobs/" + testJob + "/logs/plan",
			token: testToken,
		})

		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), errcode.JobNotFound) {
			t.Errorf("namespace %s = %d %s, want the one 404", ns, rec.Code, rec.Body.String())
		}
	}
}

func TestLogNeedsReadLogs(t *testing.T) {
	t.Parallel()

	h := logHandler(t, holdsBoth(), newFakeJobs(logJob(t, 4, phaseEnded)),
		newFakeLogs(fakeLog{data: "one\n", bad: "", err: nil}))

	rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan", token: testToken})
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "one") {
		t.Errorf("job:read without job:read-logs = %d %s, want 403 and no log", rec.Code, rec.Body.String())
	}
}

func TestLogRefusesBadAfter(t *testing.T) {
	t.Parallel()

	h := logHandler(t, logHolds(), newFakeJobs(logJob(t, 4, phaseEnded)),
		newFakeLogs(fakeLog{data: "one\n", bad: "", err: nil}))

	for _, q := range []string{"?after=-1", "?after=x", "?after=1.5", "?after=99999999999999999999"} {
		rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan" + q, token: testToken})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, rec.Code)
		}
	}
}

// TestLogChunkThatFailsItsCheck proves that the bytes of a chunk that fails
// its check are never written, and that the stream ends with an error line
// and no done line.
var errBlobDriver = errors.New("blob driver: /srv/secret")

func TestLogChunkThatFailsItsCheck(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{secret.ErrDecrypt, secret.ErrShredded, errBlobDriver} {
		logs := newFakeLogs(fakeLog{data: "good\n", bad: "EVIL-BYTES\n", err: cause})
		h := logHandler(t, logHolds(), newFakeJobs(logJob(t, 20, phaseEnded)), logs)

		rec := serve(t, h, request{method: http.MethodGet, path: logURL + "plan", token: testToken})
		body := rec.Body.String()

		if strings.Contains(body, "EVIL") || strings.Contains(body, "/srv/secret") {
			t.Errorf("the body holds bytes of the failed chunk or a driver text: %s", body)
		}

		lines := decodeLines(t, body)
		last := lines[len(lines)-1]

		if len(lines) != 2 || lines[0].Text != "good\n" || last.Done || last.Error.Reason == "" {
			t.Errorf("lines = %+v, want one entry and an error line", lines)
		}
	}
}

// TestLogFollowsARunningPhase proves that the stream sends the first entry
// before the phase ends, then the rest, then done.
func TestLogFollowsARunningPhase(t *testing.T) {
	t.Parallel()

	jobs := newFakeJobs(logJob(t, 6, phaseRunning))
	logs := newFakeLogs(fakeLog{data: "one\ntw", bad: "", err: nil})
	h := logHandler(t, logHolds(), jobs, logs)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

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

	reader := bufio.NewReader(resp.Body)

	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read the first entry: %v", err)
	}

	if !strings.Contains(first, `"text":"one\n"`) {
		t.Fatalf("first line = %s", first)
	}

	// The phase is still running here. Now it writes more and ends.
	logs.put("jlv-1", fakeLog{data: "one\ntwo\n", bad: "", err: nil})
	finished := logJob(t, 8, phaseEnded)
	jobs.set(&finished)

	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the rest: %v", err)
	}

	lines := decodeLines(t, string(rest))
	if len(lines) != 2 || lines[0].Seq != 2 || lines[0].Text != "two\n" || !lines[1].Done || lines[1].Last != 2 {
		t.Errorf("rest = %+v", lines)
	}
}

// fastLogs returns the log options of a test: a short poll.
func fastLogs() *jobsapi.LogOptions {
	return &jobsapi.LogOptions{
		Poll: 5 * time.Millisecond, MaxLife: 0, WriteTimeout: 0, PerPrincipal: 0, Total: 0, Stopping: nil,
	}
}
