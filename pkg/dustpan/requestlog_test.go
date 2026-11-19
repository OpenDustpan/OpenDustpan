package dustpan_test

import (
	"bytes"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// requestIDHeader names the response header with the request ID. Issue 161
// fixes the spelling with a lowercase d.
const requestIDHeader = "X-Request-Id"

// requestLogSink is a goroutine-safe log buffer for the request log tests.
type requestLogSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *requestLogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, err := s.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log sink: %w", err)
	}

	return n, nil
}

func (s *requestLogSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

// loggedServer starts a server whose request log goes to sink at level.
func loggedServer(
	t *testing.T, sink io.Writer, level slog.Level,
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

	// The logged server seals OpenTofu state, so it holds a
	// key-encryption key like a server with a key set does.
	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: level}))

	srv, err := dustpan.NewServer(
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
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
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return ts, srv, st
}

// logCall is one request through the logged server.
type logCall struct {
	method    string
	path      string
	auth      string
	requestID string
	body      string
}

// logResponse is the answered status, headers and body.
type logResponse struct {
	status int
	header http.Header
	body   string
}

// doLogged sends call and returns the response.
func doLogged(t *testing.T, ts *httptest.Server, call logCall) logResponse {
	t.Helper()

	var reader io.Reader
	if call.body != "" {
		reader = strings.NewReader(call.body)
	}

	req, err := http.NewRequestWithContext(t.Context(), call.method, ts.URL+call.path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if call.auth != "" {
		req.Header.Set("Authorization", call.auth)
	}

	if call.requestID != "" {
		req.Header.Set(requestIDHeader, call.requestID) //nolint:canonicalheader // Issue 161 requires X-Request-Id.
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", call.method, call.path, err)
	}

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return logResponse{status: resp.StatusCode, header: resp.Header, body: string(data)}
}

// requestLines returns the parsed request log lines in raw.
func requestLines(t *testing.T, raw string) []map[string]any {
	t.Helper()

	var out []map[string]any

	for line := range strings.Lines(strings.TrimSpace(raw)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}

		if entry["msg"] != "request" {
			continue
		}

		out = append(out, entry)
	}

	return out
}

// seedNamespace creates a namespace in st.
func seedNamespace(t *testing.T, st *sqlite.Store, name string) {
	t.Helper()

	err := st.CreateNamespace(t.Context(), namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                namespace.Name(name),
		PrimaryInstallation: namespace.InstallationName(installationName),
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	seedCatchAllPlacement(t, st, namespace.Name(name))
}

func TestRequestIDNewEachResponse(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, _, _ := loggedServer(t, &sink, slog.LevelDebug)
	live := logCall{method: http.MethodGet, path: "/health/live", auth: "", requestID: "", body: ""}

	first := doLogged(t, ts, live)
	second := doLogged(t, ts, live)

	firstID := first.header.Get(requestIDHeader)   //nolint:canonicalheader // Issue 161 requires X-Request-Id.
	secondID := second.header.Get(requestIDHeader) //nolint:canonicalheader // Issue 161 requires X-Request-Id.

	if firstID == "" || secondID == "" {
		t.Fatalf("request IDs = %q, %q, want both set", firstID, secondID)
	}

	if firstID == secondID {
		t.Fatalf("request IDs match %q, want a new ID for each request", firstID)
	}

	ignored := doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/health/live",
		auth:      "",
		requestID: "client-sent-id",
		body:      "",
	})

	ignoredID := ignored.header.Get(requestIDHeader) //nolint:canonicalheader // Issue 161 requires X-Request-Id.
	if ignoredID == "" || ignoredID == "client-sent-id" {
		t.Fatalf("request ID = %q, want a new ID that ignores the client ID", ignoredID)
	}
}

func TestRequestLogFields(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, _, _ := loggedServer(t, &sink, slog.LevelDebug)

	got := doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/api/user",
		auth:      "token " + adminToken,
		requestID: "",
		body:      "",
	})
	if got.status != http.StatusOK {
		t.Fatalf("user status %d, want %d", got.status, http.StatusOK)
	}

	lines := requestLines(t, sink.String())
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(lines))
	}

	entry := lines[0]

	if entry["method"] != http.MethodGet {
		t.Fatalf("method = %v, want GET", entry["method"])
	}

	if entry["route"] != "GET /api/user" {
		t.Fatalf("route = %v, want the registered pattern", entry["route"])
	}

	if entry["status"] != float64(http.StatusOK) {
		t.Fatalf("status = %v, want 200", entry["status"])
	}

	for _, key := range []string{"duration_ms", "bytes", "request_id", "adapter"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("log line lacks %s: %v", key, entry)
		}
	}

	if entry["adapter"] != "pulumi" {
		t.Fatalf("adapter = %v, want pulumi", entry["adapter"])
	}

	if entry["subject"] != "admin" {
		t.Fatalf("subject = %v, want admin", entry["subject"])
	}

	headerID := got.header.Get(requestIDHeader) //nolint:canonicalheader // Issue 161 requires X-Request-Id.
	if entry["request_id"] != headerID {
		t.Fatalf(
			"request_id = %v, want the response header %q",
			entry["request_id"],
			headerID,
		)
	}
}

func TestRequestLogPublicHasNoSubject(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, _, _ := loggedServer(t, &sink, slog.LevelDebug)

	got := doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/health/live",
		auth:      "",
		requestID: "",
		body:      "",
	})
	if got.status != http.StatusNoContent {
		t.Fatalf("liveness status %d, want %d", got.status, http.StatusNoContent)
	}

	lines := requestLines(t, sink.String())
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(lines))
	}

	if _, ok := lines[0]["subject"]; ok {
		t.Fatalf("public request logs subject %v, want none", lines[0]["subject"])
	}

	if lines[0]["route"] != "GET /health/live" {
		t.Fatalf("route = %v, want GET /health/live", lines[0]["route"])
	}
}

func TestRequestLogPatternNotPath(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, _, st := loggedServer(t, &sink, slog.LevelDebug)
	seedNamespace(t, st, "acme")

	created := doLogged(t, ts, logCall{
		method:    http.MethodPost,
		path:      "/api/stacks/acme/web",
		auth:      "token " + adminToken,
		requestID: "",
		body:      `{"stackName":"prod"}`,
	})
	if created.status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", created.status, created.body)
	}

	sinkBefore := len(sink.String())

	got := doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/api/stacks/acme/web/prod",
		auth:      "token " + adminToken,
		requestID: "",
		body:      "",
	})
	if got.status != http.StatusOK {
		t.Fatalf("get stack: status %d, body %s", got.status, got.body)
	}

	lines := requestLines(t, sink.String()[sinkBefore:])
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(lines))
	}

	if lines[0]["route"] != "GET /api/stacks/{namespace}/{project}/{stack}" {
		t.Fatalf("route = %v, want the registered pattern", lines[0]["route"])
	}

	if strings.Contains(sink.String()[sinkBefore:], "/api/stacks/acme/web/prod") {
		t.Fatal("log names the path, want the pattern only")
	}
}

func TestStateUploadLogsPatternNotToken(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, srv, st := loggedServer(t, &sink, slog.LevelDebug)
	seedNamespace(t, st, "acme")

	svc := srv.Workspaces()
	if svc == nil {
		t.Fatal("server has no workspace service")
	}

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w, err := svc.Create(
		t.Context(), workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, st, "acme"),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, lockErr := svc.Lock(t.Context(), w.ID, "admin", "apply"); lockErr != nil {
		t.Fatalf("lock workspace: %v", lockErr)
	}

	content := `{"version":4,"serial":1,"lineage":"l1"}`

	//nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	sum := md5.Sum([]byte(content))

	pending, err := svc.BeginState(t.Context(), w.ID, "admin", workspace.NewState{
		Lineage: "l1",
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	reveal := pending.Upload.Reveal()
	target := "/api/v2/state-uploads/" + pending.Version.ID.String() + "/" + reveal

	sinkBefore := len(sink.String())

	got := doLogged(t, ts, logCall{
		method:    http.MethodPut,
		path:      target,
		auth:      "",
		requestID: "",
		body:      content,
	})
	if got.status != http.StatusOK {
		t.Fatalf("upload status %d, want %d (body %s)", got.status, http.StatusOK, got.body)
	}

	lines := requestLines(t, sink.String()[sinkBefore:])
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(lines))
	}

	if lines[0]["route"] != "PUT /api/v2/state-uploads/{id}/{token}" {
		t.Fatalf("route = %v, want the upload pattern", lines[0]["route"])
	}

	if strings.Contains(sink.String()[sinkBefore:], reveal) {
		t.Fatal("log holds the capability token, want the pattern only")
	}

	if lines[0]["adapter"] != "opentofu" {
		t.Fatalf("adapter = %v, want opentofu", lines[0]["adapter"])
	}

	if _, ok := lines[0]["subject"]; ok {
		t.Fatalf("upload logs subject %v, want none", lines[0]["subject"])
	}
}

func TestRequestLogHealthzLevel(t *testing.T) {
	t.Parallel()

	var infoSink requestLogSink

	ts, _, _ := loggedServer(t, &infoSink, slog.LevelInfo)
	live := logCall{method: http.MethodGet, path: "/health/live", auth: "", requestID: "", body: ""}

	got := doLogged(t, ts, live)
	if got.status != http.StatusNoContent {
		t.Fatalf("liveness status %d, want %d", got.status, http.StatusNoContent)
	}

	if strings.Contains(infoSink.String(), "GET /health/live") {
		t.Fatal("liveness probe logged at info, want debug only")
	}

	var debugSink requestLogSink

	ts, _, _ = loggedServer(t, &debugSink, slog.LevelDebug)

	got = doLogged(t, ts, live)
	if got.status != http.StatusNoContent {
		t.Fatalf("liveness status %d, want %d", got.status, http.StatusNoContent)
	}

	if !strings.Contains(debugSink.String(), "GET /health/live") {
		t.Fatal("liveness probe missing from the debug log")
	}
}

func TestRequestLogInfoLevel(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, _, _ := loggedServer(t, &sink, slog.LevelInfo)

	got := doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/api/capabilities",
		auth:      "",
		requestID: "",
		body:      "",
	})
	if got.status != http.StatusOK {
		t.Fatalf("capabilities status %d, want %d", got.status, http.StatusOK)
	}

	if !strings.Contains(sink.String(), "GET /api/capabilities") {
		t.Fatal("capabilities route missing from the info log")
	}
}

func TestRequestLogJSONFormat(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, _, _ := loggedServer(t, &sink, slog.LevelDebug)

	doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/health/live",
		auth:      "",
		requestID: "",
		body:      "",
	})

	lines := requestLines(t, sink.String())
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(lines))
	}
}

func TestRequestLogTextFormat(t *testing.T) {
	t.Parallel()

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

	var sink requestLogSink

	logger := slog.New(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv, err := dustpan.NewServer(
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(testWrapper(t)),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/health/live",
		auth:      "",
		requestID: "",
		body:      "",
	})

	raw := sink.String()
	if !strings.Contains(raw, "method=GET") || !strings.Contains(raw, "route=") {
		t.Fatalf("text log line lacks fields: %q", raw)
	}

	if !strings.Contains(raw, "adapter=") || !strings.Contains(raw, "request_id=") {
		t.Fatalf("text log line lacks fields: %q", raw)
	}
}

func TestRequestLogNeverLogsCanaries(t *testing.T) {
	t.Parallel()

	var sink requestLogSink

	ts, _, _ := loggedServer(t, &sink, slog.LevelDebug)

	//nolint:gosec // Canary values for the never-logged test, not credentials.
	tokenCanary := "canary-token-8d3f2a7b1e9c5a7d2f4b6a8d1e3f5a7"
	queryCanary := "canary-query-1a2b3c4d5e6f"
	bodyCanary := "canary-body-9a8b7c6d5e4f3a2b1c"
	stateCanary := strings.Repeat("b", 43)
	contentCanary := "canary-state-content-7f6e5d4c3b2a1"
	headerCanary := "canary-header-4f8e2a1b9c7d"

	doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/api/user",
		auth:      "token " + tokenCanary,
		requestID: "",
		body:      "",
	})

	doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/api/user/stacks?organization=" + queryCanary,
		auth:      "token " + adminToken,
		requestID: "",
		body:      "",
	})
	doLogged(t, ts, logCall{
		method:    http.MethodPost,
		path:      "/api/stacks/acme/web",
		auth:      "token " + adminToken,
		requestID: "",
		body:      `{"stackName":"` + bodyCanary + `"}`,
	})

	doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/api/v2/ping",
		auth:      "Bearer " + tokenCanary,
		requestID: "",
		body:      "",
	})

	doLogged(t, ts, logCall{
		method:    http.MethodPut,
		path:      "/api/v2/state-uploads/sv-canary123/" + stateCanary,
		auth:      "",
		requestID: "",
		body:      contentCanary,
	})
	doLogged(t, ts, logCall{
		method:    http.MethodPut,
		path:      "/api/v2/json-state-uploads/sv-canary123/" + stateCanary,
		auth:      "",
		requestID: "",
		body:      `{}`,
	})

	doLogged(t, ts, logCall{
		method:    http.MethodGet,
		path:      "/health/live",
		auth:      "",
		requestID: headerCanary,
		body:      "",
	})

	raw := sink.String()

	for _, canary := range []string{
		headerCanary,
		tokenCanary,
		queryCanary,
		bodyCanary,
		stateCanary,
		contentCanary,
		"sv-canary123",
	} {
		if strings.Contains(raw, canary) {
			t.Fatalf("log holds canary value %q", canary)
		}
	}
}

func TestRequestIDInContext(t *testing.T) {
	t.Parallel()

	if got := dustpan.RequestID(t.Context()); got != "" {
		t.Fatalf("request ID in an empty context = %q, want none", got)
	}
}
