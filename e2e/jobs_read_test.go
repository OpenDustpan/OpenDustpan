//go:build e2e

package e2e_test

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// jobListDoc is the page of the job list.
type jobListDoc struct {
	Jobs          []jobDoc `json:"jobs"`
	NextPageToken string   `json:"nextPageToken"`
}

// jobLogLine is one line of the log stream.
type jobLogLine struct {
	Seq  int64  `json:"seq"`
	Step string `json:"step"`
	Text string `json:"text"`
	Done bool   `json:"done"`
	Last int64  `json:"last"`
}

// listAndLog starts a job, drives its first phase with the fake launcher,
// lists the job, and reads its log while the phase runs: the first entry
// arrives before the phase ends. It then reads the log again from a seq.
func listAndLog(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	run := openLaunchedSession(t, env)
	step := run.launcher.Assignment.Steps[0].Name

	run.listsTheJob(ctx, t)

	// The apply phase did not start: it has no log yet.
	status, body, err := env.APIStatus(ctx, http.MethodGet, jobPath(e2e.Namespace, run.job)+"/logs/apply", "")
	if err != nil || status != http.StatusNotFound || !strings.Contains(body, "job.log-not-available") {
		t.Fatalf("log of a phase that did not start = %d %s %v, want 404 job.log-not-available", status, body, err)
	}

	first, second := []byte("first line\n"), []byte("second line\n")
	run.streamsWhileTheStepRuns(t, step, first, second)

	// A resume with after reads the entries after that seq, and the end.
	resumed := openLogStream(ctx, t, env, run.job, "?after=1")
	defer closeStream(t, resumed)

	if line := resumed.next(t); line.Seq != 2 || line.Text != string(second) {
		t.Fatalf("resumed entry = %+v, want seq 2 %q", line, second)
	}

	if line := resumed.next(t); !line.Done {
		t.Fatalf("resumed last line = %+v, want done", line)
	}
}

// streamsWhileTheStepRuns writes two chunks of the log through the launcher and
// reads the log stream meanwhile: the first entry arrives before the phase
// ends, and the second entry and the end line arrive after the finish.
func (r *launched) streamsWhileTheStepRuns(t *testing.T, step string, first, second []byte) {
	t.Helper()

	ctx := t.Context()
	run := r
	endPath := pathOf(protocol.PathSessionStepEnd, step, 0)

	wantNoContent(t, "begin the step", func() (int, []byte) {
		return run.launcher.Post(ctx, t, pathOf(protocol.PathSessionStepBegin, step, 0), nil)
	})
	wantNoContent(t, "store the first chunk", func() (int, []byte) {
		return run.launcher.PutLog(ctx, t, pathOf(protocol.PathSessionLog, step, 0), first)
	})

	stream := openLogStream(ctx, t, run.env, run.job, "")
	defer closeStream(t, stream)

	// The phase runs: the first entry arrives before it ends.
	if line := stream.next(t); line.Seq != 1 || line.Text != string(first) || line.Step != step {
		t.Fatalf("first entry = %+v, want seq 1 %q of step %s", line, first, step)
	}

	wantNoContent(t, "store the second chunk", func() (int, []byte) {
		return run.launcher.PutLog(ctx, t, pathOf(protocol.PathSessionLog, step, 1), second)
	})

	now := time.Now().UTC()
	whole := slices.Concat(first, second)

	wantNoContent(t, "end the step", func() (int, []byte) {
		return run.launcher.Post(ctx, t, endPath, protocol.StepEnd{
			ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second), LogChunks: 2,
			LogSHA256: hex.EncodeToString(sha256Of(whole)),
		})
	})
	wantNoContent(t, "finish the phase", func() (int, []byte) {
		return run.launcher.Post(ctx, t, protocol.PathSessionFinish, protocol.Finish{Succeeded: true, Message: ""})
	})

	if line := stream.next(t); line.Seq != 2 || line.Text != string(second) {
		t.Fatalf("second entry = %+v, want seq 2 %q", line, second)
	}

	if line := stream.next(t); !line.Done || line.Last != 2 {
		t.Fatalf("last line = %+v, want done at 2", line)
	}
}

// listsTheJob lists the jobs of the namespace with the filters, and with a
// page token that was changed.
func (r *launched) listsTheJob(ctx context.Context, t *testing.T) {
	t.Helper()

	list := func(query string) jobListDoc {
		t.Helper()

		var page jobListDoc

		status, body, err := r.env.APIStatus(ctx, http.MethodGet, jobsRoute+query, "")
		if err != nil || status != http.StatusOK {
			t.Fatalf("list %q = %d %s %v, want 200", query, status, body, err)
		}

		if err = json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("decode the list %q: %v", query, err)
		}

		return page
	}

	for query, want := range map[string]bool{
		"": true, "?status=" + r.state(ctx, t): true, "?status=succeeded": false, "?purpose=drift-check": false,
		"?trigger=trg-none": false, "?unit=unt-none": false,
	} {
		page := list(query)

		found := false

		for i := range page.Jobs {
			found = found || page.Jobs[i].ID == r.job
		}

		if found != want {
			t.Fatalf("list %q holds the job = %v, want %v", query, found, want)
		}
	}

	if page := list("?pageSize=1"); len(page.Jobs) != 1 {
		t.Fatalf("a page of size 1 holds %d jobs", len(page.Jobs))
	}

	status, body, err := r.env.APIStatus(ctx, http.MethodGet, jobsRoute+"?pageToken="+url.QueryEscape("e30"), "")
	if err != nil || status != http.StatusBadRequest || !strings.Contains(body, "request.page-token-invalid") {
		t.Fatalf("list with a changed page token = %d %s %v, want 400 request.page-token-invalid", status, body, err)
	}
}

// state returns the state of the job now.
func (r *launched) state(ctx context.Context, t *testing.T) string {
	t.Helper()

	status, body, err := r.env.APIStatus(ctx, http.MethodGet, jobPath(e2e.Namespace, r.job), "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read the job = %d %s %v", status, body, err)
	}

	var doc jobDoc
	if err = json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the job: %v", err)
	}

	return doc.State
}

// logStream reads the lines of a log stream.
type logStream struct {
	resp   *http.Response
	reader *bufio.Reader
}

// openLogStream opens the log of the plan phase of the job.
func openLogStream(ctx context.Context, t *testing.T, env *e2e.Env, jobID, query string) *logStream {
	t.Helper()

	resp, err := env.OpenStream(ctx, jobPath(e2e.Namespace, jobID)+"/logs/plan"+query)
	if err != nil {
		t.Fatalf("open the log stream: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		status := resp.StatusCode
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Logf("close the log stream: %v", closeErr)
		}

		t.Fatalf("log stream status = %d, want 200", status)
	}

	return &logStream{resp: resp, reader: bufio.NewReader(resp.Body)}
}

// next reads the next line of the stream.
func (s *logStream) next(t *testing.T) jobLogLine {
	t.Helper()

	raw, err := s.reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read the log stream: %v", err)
	}

	var line jobLogLine
	if err = json.Unmarshal([]byte(raw), &line); err != nil {
		t.Fatalf("decode the log line %q: %v", raw, fmt.Errorf("%w", err))
	}

	return line
}

func closeStream(t *testing.T, s *logStream) {
	t.Helper()

	if err := s.resp.Body.Close(); err != nil {
		t.Logf("close the log stream: %v", err)
	}
}
