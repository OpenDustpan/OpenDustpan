package dustpan_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/logsink/slogsink"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// canaryKey is the fixed key-encryption key of the canary server, so the
// encrypt route succeeds. It is a test value only.
const canaryKey = "0123456789abcdef0123456789abcdef"

// errCanaryStore reports a failed store read in the canary test. The test
// wraps it with a driver canary, so the wrapped text looks like the driver
// detail that must never reach the log.
var errCanaryStore = errors.New("driver: connection reset")

// failingStackStore wraps a stack store and fails GetStack with err, so one
// request answers 500. Every other method passes through to the wrapped
// store.
type failingStackStore struct {
	stack.Store

	err error
}

// GetStack implements stack.Store. It always fails with the wrapped error.
func (f failingStackStore) GetStack(_ context.Context, _ stack.Ref) (stack.Stack, error) {
	return stack.Stack{}, f.err
}

// canaryServer starts a server whose request log goes to sink and whose
// encrypt route has a key. A nil stackErr uses the SQLite store for stacks;
// a set stackErr fails stack reads with it, so one request answers 500. It
// returns the test server, the dustpan server, and the store for seeding.
func canaryServer(
	t *testing.T, sink io.Writer, stackErr error, extra ...dustpan.Option,
) (*httptest.Server, *dustpan.Server, *sqlite.Store) {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(canaryKey))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	var stacks stack.Store = st
	if stackErr != nil {
		stacks = failingStackStore{Store: st, err: stackErr}
	}

	logger := slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	opts := []dustpan.Option{
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(stacks),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithLogger(logger),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	}

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return ts, srv, st
}

// seedCanaryNamespace creates the acme namespace in st. It is separate from
// seedNamespace so the canary tests keep one fixed namespace.
func seedCanaryNamespace(t *testing.T, st *sqlite.Store) {
	t.Helper()

	err := st.CreateNamespace(t.Context(), namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Now(),
		Name:            namespace.Name("acme"),
		HomeRegion:      namespace.Region(region),
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}

// canaryCall sends call and names the canary values it carries, so the test
// fails when any appears in the log or in the metrics output.
type canaryCall struct {
	kind     string
	canaries []string
	call     logCall
}

// canaryCalls lists one row per canary kind, so a new field is easy to add.
// It uses both adapters: Pulumi stack routes and the TFE state upload.
func canaryCalls() []canaryCall {
	//nolint:gosec // Canary value for the never-logged test, not a credential.
	tokenCanary := "canary-token-8d3f2a7b1e9c5a7d2f4b6a8d1e3f5a7b1"
	bearerCanary := "canary-bearer-1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d"
	queryCanary := "canary-query-2b3c4d5e6f7a8b9c0d1e"
	pathCanary := "canary-path-3c4d5e6f7a8b9c0d1e2f"
	bodyCanary := "canary-body-4d5e6f7a8b9c0d1e2f3a"
	uploadCanary := "canary-upload-5e6f7a8b9c0d1e2f3a4b5c6d"
	uploadID := "sv-canary99"
	encryptCanary := base64.StdEncoding.EncodeToString([]byte("canary-encrypt-plaintext-7a8b"))

	return []canaryCall{
		{
			kind:     "token",
			canaries: []string{tokenCanary},
			call: logCall{
				method:    http.MethodGet,
				path:      "/api/user",
				auth:      "token " + tokenCanary,
				requestID: "",
				body:      "",
			},
		},
		{
			kind:     "bearer",
			canaries: []string{bearerCanary},
			call: logCall{
				method:    http.MethodGet,
				path:      "/api/v2/ping",
				auth:      "Bearer " + bearerCanary,
				requestID: "",
				body:      "",
			},
		},
		{
			kind:     "query",
			canaries: []string{queryCanary},
			call: logCall{
				method:    http.MethodGet,
				path:      "/api/user/stacks?organization=" + queryCanary,
				auth:      "token " + adminToken,
				requestID: "",
				body:      "",
			},
		},
		{
			kind:     "path",
			canaries: []string{pathCanary},
			call: logCall{
				method:    http.MethodGet,
				path:      "/api/stacks/acme/web/" + pathCanary,
				auth:      "token " + adminToken,
				requestID: "",
				body:      "",
			},
		},
		{
			kind:     "body",
			canaries: []string{bodyCanary},
			call: logCall{
				method:    http.MethodPost,
				path:      "/api/stacks/acme/web",
				auth:      "token " + adminToken,
				requestID: "",
				body:      `{"stackName":"` + bodyCanary + `"}`,
			},
		},
		{
			kind:     "upload",
			canaries: []string{uploadCanary, uploadID},
			call: logCall{
				method:    http.MethodPut,
				path:      "/api/v2/state-uploads/" + uploadID + "/" + uploadCanary,
				auth:      "",
				requestID: "",
				body:      `{"version":4}`,
			},
		},
		{
			kind:     "encrypt",
			canaries: []string{encryptCanary},
			call: logCall{
				method:    http.MethodPost,
				path:      "/api/stacks/acme/web/prod/encrypt",
				auth:      "token " + adminToken,
				requestID: "",
				body:      `{"plaintext":"` + encryptCanary + `"}`,
			},
		},
	}
}

// requireAbsent fails when haystack holds any canary the calls carry. It
// names the canary kind, so a failure points at the leaking field.
func requireAbsent(t *testing.T, where, haystack string, calls []canaryCall) {
	t.Helper()

	for _, tc := range calls {
		for _, canary := range tc.canaries {
			if strings.Contains(haystack, canary) {
				t.Fatalf("%s holds %s canary value %q", where, tc.kind, canary)
			}
		}
	}
}

// TestCanaryNeverInLogOrMetrics sends canary values through both tool
// adapters and fails when one appears in the log or in the metrics output
// (ADR 32 decision 7). It also checks that one log line exists for each
// request, so a missing log cannot pass the test.
func TestCanaryNeverInLogOrMetrics(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, srv, st := canaryServer(t, &sink, nil)
	seedCanaryNamespace(t, st)

	setup := doLogged(t, ts, logCall{
		method:    http.MethodPost,
		path:      "/api/stacks/acme/web",
		auth:      "token " + adminToken,
		requestID: "",
		body:      `{"stackName":"prod"}`,
	})
	if setup.status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", setup.status, setup.body)
	}

	calls := canaryCalls()
	sinkBefore := len(sink.String())

	for _, tc := range calls {
		doLogged(t, ts, tc.call)
	}

	lines := requestLines(t, sink.String()[sinkBefore:])
	if len(lines) != len(calls) {
		t.Fatalf("log lines = %d, want %d (one per request)", len(lines), len(calls))
	}

	requireAbsent(t, "log", sink.String(), calls)

	var metricsOut strings.Builder
	if err := srv.Metrics().Write(&metricsOut); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	requireAbsent(t, "metrics output", metricsOut.String(), calls)

	if !strings.Contains(metricsOut.String(), "dustpan_http_requests_total") {
		t.Fatal("metrics output holds no request count")
	}
}

// TestCanaryFailingStoreLogsNoDriverText proves a 500 from a failing store
// logs no driver text and answers the generic error (ADR 32 decision 7). It
// also checks that one log line exists for each request, so a missing log
// cannot pass the test.
func TestCanaryFailingStoreLogsNoDriverText(t *testing.T) {
	t.Parallel()

	driverCanary := "canary-driver-7a8b9c0d1e2f3a4b5c6d7e8f"
	storeErr := fmt.Errorf("%w (%s)", errCanaryStore, driverCanary)

	var sink requestLogSink

	ts, _, st := canaryServer(t, &sink, storeErr)
	seedCanaryNamespace(t, st)

	seeded := doLogged(t, ts, logCall{
		method:    http.MethodPost,
		path:      "/api/stacks/acme/web",
		auth:      "token " + adminToken,
		requestID: "",
		body:      `{"stackName":"prod"}`,
	})
	if seeded.status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", seeded.status, seeded.body)
	}

	got := doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/api/stacks/acme/web/prod",
		auth:      "token " + adminToken,
		requestID: "",
		body:      "",
	})
	if got.status != http.StatusInternalServerError {
		t.Fatalf("failing store status %d, want %d", got.status, http.StatusInternalServerError)
	}

	if strings.Contains(got.body, driverCanary) {
		t.Fatalf("error response holds driver text %q", driverCanary)
	}

	if raw := sink.String(); strings.Contains(raw, driverCanary) {
		t.Fatalf("log holds driver text %q", driverCanary)
	}

	lines := requestLines(t, sink.String())
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2 (setup and failing request)", len(lines))
	}
}

// TestCanaryNeverInAFileAuditSink sends the canary values through both tool
// adapters with a file audit sink, then a decrypt report that writes one
// audit entry. The sink receives the entry and none of the canary values
// (ADR 32 decision 7, ADR 60 decision 13).
func TestCanaryNeverInAFileAuditSink(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")

	auditFile, err := file.Open(path)
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := auditFile.Close(); closeErr != nil {
			t.Errorf("close audit file: %v", closeErr)
		}
	})

	var sink requestLogSink

	ts, srv, st := canaryServer(t, &sink, nil,
		dustpan.WithListener(openTestListener(t)),
		dustpan.WithAuditSinks(audit.ShipSink{
			Sink: auditFile, Name: "canary", Kind: "file", Interval: 10 * time.Millisecond,
		}),
	)
	seedCanaryNamespace(t, st)

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, srv)

	t.Cleanup(func() {
		cancel()

		if runErr := <-done; runErr != nil {
			t.Errorf("run: %v", runErr)
		}
	})

	created := doLogged(t, ts, logCall{
		method: http.MethodPost, path: "/api/stacks/acme/web", auth: "token " + adminToken,
		requestID: "", body: `{"stackName":"prod"}`,
	})
	if created.status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", created.status, created.body)
	}

	calls := canaryCalls()
	for _, tc := range calls {
		doLogged(t, ts, tc.call)
	}

	reportCanary := "canary-report-6f7a8b9c0d1e2f3a4b5c"
	report := logCall{
		method: http.MethodPost, path: "/api/stacks/acme/web/prod/decrypt/log-decryption",
		auth: "token " + adminToken, requestID: "", body: `{"ciphertext":"` + reportCanary + `"}`,
	}
	calls = append(calls, canaryCall{kind: "decrypt report", canaries: []string{reportCanary}, call: report})

	reported := doLogged(t, ts, report)
	if reported.status != http.StatusNoContent {
		t.Fatalf("decrypt report: status %d, body %s", reported.status, reported.body)
	}

	shipped := waitForFileLine(t, path, `"action":"unit:decrypt"`)

	requireAbsent(t, "audit file sink", shipped, calls)
}

// waitForFileLine waits until the file at path holds want and answers its
// content. It fails the test after a few seconds.
func waitForFileLine(t *testing.T, path, want string) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		data, err := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if strings.Contains(string(data), want) {
			return string(data)
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s lacks %q after 5s: %s", path, want, data)
		}

		timer := time.NewTimer(10 * time.Millisecond)

		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test ended while it waited for the audit file")
		}
	}
}

// TestCanaryNeverInAFileLogSink sends the canary values through both tool
// adapters with the application log on a file log sink (ADR 60 decision
// 14). The file holds one request line for each call and none of the
// canary values (ADR 32 decision 7).
func TestCanaryNeverInAFileLogSink(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "app.jsonl")

	logFile, err := file.Open(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := logFile.Close(); closeErr != nil {
			t.Errorf("close log file: %v", closeErr)
		}
	})

	handler := slogsink.New(logFile, slogsink.Options{
		Level:         slog.LevelDebug,
		OnDrop:        nil,
		QueueSize:     0,
		BatchSize:     0,
		FlushInterval: 0,
		WriteTimeout:  0,
	})

	var unused requestLogSink

	ts, _, st := canaryServer(t, &unused, nil, dustpan.WithLogger(slog.New(handler)))
	seedCanaryNamespace(t, st)

	created := doLogged(t, ts, logCall{
		method: http.MethodPost, path: "/api/stacks/acme/web", auth: "token " + adminToken,
		requestID: "", body: `{"stackName":"prod"}`,
	})
	if created.status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", created.status, created.body)
	}

	calls := canaryCalls()
	for _, tc := range calls {
		doLogged(t, ts, tc.call)
	}

	if closeErr := handler.Close(t.Context()); closeErr != nil {
		t.Fatalf("flush log sink: %v", closeErr)
	}

	data, err := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	if lines := requestLines(t, string(data)); len(lines) != len(calls)+1 {
		t.Fatalf("request lines in the log file = %d, want %d", len(lines), len(calls)+1)
	}

	requireAbsent(t, "log file sink", string(data), calls)
}
