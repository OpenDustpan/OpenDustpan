package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const storeCheckTestToken = "test-token"

// storeCheckServer scripts the two routes of a store check. The GET answers
// running once and then the final answer.
type storeCheckServer struct {
	server   *httptest.Server
	requests atomic.Int64
	posts    atomic.Int64
	gets     atomic.Int64
	postBody atomic.Value
}

func newStoreCheckServer(t *testing.T, startStatus int, startBody, final string) *storeCheckServer {
	t.Helper()

	fake := &storeCheckServer{
		server: nil, requests: atomic.Int64{}, posts: atomic.Int64{}, gets: atomic.Int64{}, postBody: atomic.Value{},
	}
	mux := http.NewServeMux()

	authed := func(w http.ResponseWriter, r *http.Request) bool {
		fake.requests.Add(1)

		if r.Header.Get("Authorization") != "Bearer "+storeCheckTestToken {
			w.WriteHeader(http.StatusUnauthorized)

			return false
		}

		return true
	}

	mux.HandleFunc("POST /api/dustpan/v1/store-checks", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}

		fake.posts.Add(1)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the start body: %v", err)
		}

		fake.postBody.Store(string(body))

		w.WriteHeader(startStatus)
		writeBody(t, w, startBody)
	})

	mux.HandleFunc("GET /api/dustpan/v1/store-checks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}

		if fake.gets.Add(1) == 1 {
			writeBody(t, w, `{"id":"chk-aaaa","target":"blobs","state":"running"}`)

			return
		}

		writeBody(t, w, final)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// writeBody writes body to w and fails the test on an error.
func writeBody(t *testing.T, w io.Writer, body string) {
	t.Helper()

	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write the body: %v", err)
	}
}

func storeCheckAnswerJSON(t *testing.T, state, errText string, passed bool) string {
	t.Helper()

	doc := StoreCheckAnswer{ID: "chk-aaaa", Target: "blobs", State: state, Error: errText, Report: nil}
	if state == "succeeded" {
		doc.Report = &StoreCheckReport{
			Prefix: "store-checks/chk-aaaa", ConditionalWrites: "ignored", Passed: passed, PrefixDeleted: true,
			Cases: []StoreCheckCase{
				{Name: "put then get returns the same bytes", Message: "", Passed: true},
				{
					Name:    "delete removes and is idempotent",
					Message: "an object stayed after the delete",
					Passed:  passed,
				},
			},
			PutLatency:        StoreCheckLatency{P50Micros: 1200, P99Micros: 4800},
			GetLatency:        StoreCheckLatency{P50Micros: 900, P99Micros: 3100},
			DeleteLatency:     StoreCheckLatency{P50Micros: 800, P99Micros: 2000},
			PutBytesPerSecond: 52428800, GetBytesPerSecond: 104857600, DeletesPerSecond: 410, ThrottlingSeen: false,
		}
	}

	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode answer: %v", err)
	}

	return string(body)
}

func TestStoreCheckPrintsTheReport(t *testing.T) {
	t.Parallel()

	fake := newStoreCheckServer(t, http.StatusAccepted, `{"id":"chk-aaaa","target":"blobs","state":"running"}`,
		storeCheckAnswerJSON(t, "succeeded", "", true))

	var stdout, stderr bytes.Buffer

	err := runStoreCheck(
		t.Context(),
		[]string{"--server", fake.server.URL, "--target", "blobs"},
		storeCheckTestToken,
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("store check: %v", err)
	}

	if got := fake.postBody.Load(); got != `{"target":"blobs"}` {
		t.Fatalf("start body = %v, want only the target name", got)
	}

	for _, want := range []string{
		"store check chk-aaaa on target blobs: succeeded", "pass  put then get returns the same bytes",
		"conditional writes: ignored", "throttling seen: false", "prefix deleted: true",
		"put latency: p50 1200 us, p99 4800 us", "put throughput: 52428800 bytes per second",
		"delete rate: 410 objects per second",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout.String())
		}
	}

	if fake.gets.Load() < 2 {
		t.Fatalf("the command read the check %d times, want it to wait while the check runs", fake.gets.Load())
	}
}

func TestStoreCheckExitsWithAnErrorWhenACaseFailsOrTheCheckFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		state   string
		failure string
		want    error
		text    string
	}{
		{"a case fails", "succeeded", "", ErrStoreCheckCases, "FAIL  delete removes and is idempotent"},
		{"the check stops", "failed", "the check budget ended", ErrStoreCheckFailed, "stopped: the check budget ended"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			final := storeCheckAnswerJSON(t, tc.state, tc.failure, false)
			fake := newStoreCheckServer(t, http.StatusAccepted, `{"id":"chk-aaaa"}`, final)

			var stdout, stderr bytes.Buffer

			args := []string{"--server", fake.server.URL, "--target", "blobs"}

			err := runStoreCheck(t.Context(), args, storeCheckTestToken, &stdout, &stderr)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}

			if !strings.Contains(stdout.String(), tc.text) {
				t.Fatalf("output lacks %q:\n%s", tc.text, stdout.String())
			}
		})
	}
}

func TestStoreCheckSendsNoRequestForATargetThatIsNotOnTheList(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"", "https://s3.example.test/bucket", "Blobs", "all", "blobs,backup"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			fake := newStoreCheckServer(t, http.StatusAccepted, `{}`, `{}`)

			var stdout, stderr bytes.Buffer

			args := []string{"--server", fake.server.URL, "--target", target}

			err := runStoreCheck(t.Context(), args, storeCheckTestToken, &stdout, &stderr)
			if !errors.Is(err, ErrStoreCheckTarget) {
				t.Fatalf("err = %v, want ErrStoreCheckTarget", err)
			}

			if got := fake.requests.Load(); got != 0 {
				t.Fatalf("the server saw %d requests, want none", got)
			}
		})
	}
}

func TestStoreCheckReportsTheRefusalOfTheServer(t *testing.T) {
	t.Parallel()

	fake := newStoreCheckServer(t, http.StatusConflict,
		`{"code":409,"message":"a store check is running: wait until it ends, then start again"}`, `{}`)

	var stdout, stderr bytes.Buffer

	err := runStoreCheck(
		t.Context(),
		[]string{"--server", fake.server.URL, "--target", "backup"},
		storeCheckTestToken,
		&stdout,
		&stderr,
	)
	if err == nil || !errors.Is(err, ErrStoreCheckStart) || !strings.Contains(err.Error(), "a store check is running") {
		t.Fatalf("err = %v, want ErrStoreCheckStart with the message of the server", err)
	}

	if fake.gets.Load() != 0 {
		t.Fatal("the command read a check that never started")
	}
}

func TestStoreCheckNeedsAServerAndRefusesOversizedAnswers(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	err := runStoreCheck(t.Context(), []string{"--target", "blobs"}, storeCheckTestToken, &stdout, &stderr)
	if !errors.Is(err, ErrStoreCheckNeedsServer) {
		t.Fatalf("err = %v, want ErrStoreCheckNeedsServer", err)
	}

	fake := newStoreCheckServer(t, http.StatusAccepted, `{"id":"chk-aaaa"}`, strings.Repeat("x", MaxClientBodyBytes+10))

	err = runStoreCheck(
		t.Context(),
		[]string{"--server", fake.server.URL, "--target", "blobs"},
		storeCheckTestToken,
		&stdout,
		&stderr,
	)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}

	err = runStoreCheck(
		t.Context(),
		[]string{"--server", fake.server.URL, "--target", "blobs", "extra"},
		storeCheckTestToken,
		&stdout,
		&stderr,
	)
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v, want ErrUsage", err)
	}
}
