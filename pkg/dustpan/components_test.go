package dustpan_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// parseCase is one case of TestParseComponents.
type parseCase struct {
	name    string
	raw     string
	want    string
	wantErr bool
}

// parsed returns a case that parses to want.
func parsed(name, raw, want string) parseCase {
	return parseCase{name: name, raw: raw, want: want, wantErr: false}
}

// refused returns a case that the parse refuses.
func refused(name, raw string) parseCase {
	return parseCase{name: name, raw: raw, want: "", wantErr: true}
}

// TestParseComponents proves the value of --components: the default is every
// component, a list picks some, and an unknown name stops the start with an
// error that names the valid components.
func TestParseComponents(t *testing.T) {
	t.Parallel()

	cases := []parseCase{
		parsed("empty means all", "", "api,worker"),
		parsed("all", "all", "api,worker"),
		parsed("api only", "api", "api"),
		parsed("worker only", "worker", "worker"),
		parsed("list with spaces", " worker , api ", "api,worker"),
		parsed("duplicate", "api,api", "api"),
		refused("unknown", "api,scheduler"),
		refused("empty item", "api,"),
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := dustpan.ParseComponents(tc.raw)
			if tc.wantErr {
				checkUnknownComponent(t, err)

				return
			}

			if err != nil {
				t.Fatalf("ParseComponents(%q): %v", tc.raw, err)
			}

			if got.String() != tc.want {
				t.Fatalf("ParseComponents(%q) = %q, want %q", tc.raw, got.String(), tc.want)
			}
		})
	}
}

// checkUnknownComponent fails unless err is ErrUnknownComponent and names the
// valid components and the setting.
func checkUnknownComponent(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, dustpan.ErrUnknownComponent) {
		t.Fatalf("error = %v, want ErrUnknownComponent", err)
	}

	for _, valid := range []string{"all", "api", "worker", "--components", "DUSTPAN_COMPONENTS"} {
		if !strings.Contains(err.Error(), valid) {
			t.Fatalf("error %q does not name %q", err, valid)
		}
	}
}

// TestNewServerRefusesNoComponent proves a set with no component stops the
// start.
func TestNewServerRefusesNoComponent(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)

	_, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithComponents(dustpan.Components{}),
	)...)
	if !errors.Is(err, dustpan.ErrMissingOption) {
		t.Fatalf("NewServer with no component: error = %v, want ErrMissingOption", err)
	}
}

// runningServer is a server that Run serves on a loopback port.
type runningServer struct {
	base string
}

// startComponentServer starts a server over a new store with the components
// set and stops it when the test ends.
func startComponentServer(
	t *testing.T, set dustpan.Components, extra ...dustpan.Option,
) runningServer {
	t.Helper()

	return startComponentServerOn(t, newPorts(t), set, extra...)
}

// ports are the stores of one test server.
type ports struct {
	st    *sqlite.Store
	blobs *fs.Store
	authn *identity.StaticToken
}

// newPorts opens the stores of a test server.
func newPorts(t *testing.T) ports {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)

	return ports{st: st, blobs: blobs, authn: authn}
}

// getStatus sends one GET with the admin token and returns the status and
// the body.
func getStatus(t *testing.T, base, path string) (int, string) {
	t.Helper()

	return sendRequest(t, http.MethodGet, base+path)
}

// sendRequest sends one request with the admin token and no body.
func sendRequest(t *testing.T, method, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	authorize(t, req, adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, string(data)
}

// pause waits for d or until the test context ends.
func pause(t *testing.T, d time.Duration) {
	t.Helper()

	select {
	case <-time.After(d):
	case <-t.Context().Done():
	}
}

// mustParse parses a components value or fails the test.
func mustParse(t *testing.T, raw string) dustpan.Components {
	t.Helper()

	set, err := dustpan.ParseComponents(raw)
	if err != nil {
		t.Fatalf("ParseComponents(%q): %v", raw, err)
	}

	return set
}

// componentRouteCase is one case of TestComponentRoutes.
type componentRouteCase struct {
	name string
	raw  string
	path string
	want int
}

// componentRoute returns a case: a process with the components raw answers want on
// path.
func componentRoute(name, raw, path string, want int) componentRouteCase {
	return componentRouteCase{name: name, raw: raw, path: path, want: want}
}

// TestComponentRoutes proves which routes each set of components answers.
// A process with only the worker component answers 404 on each route except
// the probes and the metrics route.
func TestComponentRoutes(t *testing.T) {
	t.Parallel()

	cases := []componentRouteCase{
		componentRoute("all serves the installation API", "all", "/api/server/namespaces", http.StatusOK),
		componentRoute("api serves the installation API", "api", "/api/server/namespaces", http.StatusOK),
		componentRoute(
			"worker answers 404 on the installation API",
			"worker",
			"/api/server/namespaces",
			http.StatusNotFound,
		),
		componentRoute("worker answers 404 on the Pulumi API", "worker", "/api/user", http.StatusNotFound),
		componentRoute("worker answers 404 on the TFE API", "worker", "/api/v2/ping", http.StatusNotFound),
		componentRoute("worker answers 404 on discovery", "worker", "/.well-known/terraform.json", http.StatusNotFound),
		componentRoute("worker answers 404 on an unknown path", "worker", "/nothing", http.StatusNotFound),
		componentRoute("worker serves the metrics route", "worker", "/api/server/metrics", http.StatusOK),
		componentRoute("api serves the metrics route", "api", "/api/server/metrics", http.StatusOK),
		componentRoute("worker serves liveness", "worker", "/health/live", http.StatusNoContent),
		componentRoute("api serves liveness", "api", "/health/live", http.StatusNoContent),
		componentRoute("worker serves readiness", "worker", "/health/ready", http.StatusOK),
		componentRoute("worker serves its own readiness", "worker", "/health/ready/worker", http.StatusOK),
		componentRoute("worker does not run api readiness", "worker", "/health/ready/api", http.StatusNotFound),
		componentRoute("api serves its own readiness", "api", "/health/ready/api", http.StatusOK),
		componentRoute("api does not run worker readiness", "api", "/health/ready/worker", http.StatusNotFound),
		componentRoute("all serves both readiness routes", "all", "/health/ready/worker", http.StatusOK),
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := startComponentServer(t, mustParse(t, tc.raw))

			if got, body := getStatus(t, srv.base, tc.path); got != tc.want {
				t.Fatalf("GET %s: status %d, want %d (body %q)", tc.path, got, tc.want, body)
			}
		})
	}
}

// componentState is the state of one component in the readiness body.
type componentState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// readyBody is the body of GET /health/ready.
type readyBody struct {
	Status     string           `json:"status"`
	Components []componentState `json:"components"`
}

// TestReadinessListsComponents proves GET /health/ready lists each component
// of the process with its state.
func TestReadinessListsComponents(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ raw, want string }{
		{raw: "all", want: "api,worker"},
		{raw: "api", want: "api"},
		{raw: "worker", want: "worker"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			srv := startComponentServer(t, mustParse(t, tc.raw))

			status, body := getStatus(t, srv.base, "/health/ready")
			if status != http.StatusOK {
				t.Fatalf("GET /health/ready: status %d, body %q", status, body)
			}

			var doc readyBody
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatalf("decode %q: %v", body, err)
			}

			if got := listStates(t, doc.Components); got != tc.want {
				t.Fatalf("components = %q, want %q", got, tc.want)
			}
		})
	}
}

// listStates returns the names of the components, comma-separated, and fails
// the test for a component that is not ok.
func listStates(t *testing.T, states []componentState) string {
	t.Helper()

	names := make([]string, 0, len(states))

	for _, c := range states {
		if c.Status != "ok" {
			t.Fatalf("component %s status %q, want ok", c.Name, c.Status)
		}

		names = append(names, c.Name)
	}

	return strings.Join(names, ",")
}

// installationComponents is the body of GET /api/dustpan/v1/components.
type installationComponents struct {
	Components []string `json:"components"`
}

// TestInstallationAPIListsComponents proves the installation API records the
// components of the process.
func TestInstallationAPIListsComponents(t *testing.T) {
	t.Parallel()

	srv := startComponentServer(t, mustParse(t, "api"))

	status, body := getStatus(t, srv.base, "/api/dustpan/v1/components")
	if status != http.StatusOK {
		t.Fatalf("GET /api/dustpan/v1/components: status %d, body %q", status, body)
	}

	var doc installationComponents
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	if got := strings.Join(doc.Components, ","); got != "api" {
		t.Fatalf("components = %q, want api", got)
	}
}

// stuckLease is the lease period of the stuck worker tests. Two periods are
// one second.
const stuckLease = 500 * time.Millisecond

// stuckWait is a wait that is past 2 lease periods.
const stuckWait = 2*stuckLease + 300*time.Millisecond

// dueSchedule is a due schedule in the worker report.
type dueSchedule struct {
	Name string `json:"name"`
}

// workerDoc is the body of GET /api/dustpan/v1/worker.
type workerDoc struct {
	Stuck     bool          `json:"stuck"`
	Schedules []dueSchedule `json:"schedules"`
}

// startWorkerlessServer starts a server with a durable store
// and the short lease of the stuck worker tests.
func startWorkerlessServer(t *testing.T, raw string) runningServer {
	t.Helper()

	return startComponentServer(t, mustParse(t, raw),
		dustpan.WithOperationLease(stuckLease),
	)
}

// TestNoWorkerRefusesTheStartOfAnOperation proves that an api process
// refuses the approval of an audit chain change with 503 and the command that helps after 2 lease
// periods with no worker claim, and lists the due schedules in the report.
func TestNoWorkerRefusesTheStartOfAnOperation(t *testing.T) {
	t.Parallel()

	srv := startWorkerlessServer(t, "api")
	pause(t, stuckWait)

	status, body := sendRequest(t, http.MethodPost, srv.base+"/api/server/audit/chain")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("POST /api/server/audit/chain: status %d, want 503 (body %q)", status, body)
	}

	if !strings.Contains(body, "opendustpan serve --components worker") {
		t.Fatalf("body %q does not name the command that helps", body)
	}

	status, body = getStatus(t, srv.base, "/api/dustpan/v1/worker")
	if status != http.StatusOK {
		t.Fatalf("GET /api/dustpan/v1/worker: status %d, body %q", status, body)
	}

	var doc workerDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	if !doc.Stuck || len(doc.Schedules) == 0 {
		t.Fatalf("worker report %q: want stuck with the due schedules", body)
	}
}

// TestStartIsAdmittedWhenAWorkerMayRun proves that the approval of a change is
// admitted before 2 lease periods pass without a claim, and always on a
// process that runs the worker.
func TestStartIsAdmittedWhenAWorkerMayRun(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		wait time.Duration
	}{
		{name: "api before 2 lease periods", raw: "api", wait: 0},
		{name: "all components", raw: "all", wait: stuckWait},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := startWorkerlessServer(t, tc.raw)
			pause(t, tc.wait)

			status, body := sendRequest(t, http.MethodPost, srv.base+"/api/server/audit/chain")
			if status == http.StatusServiceUnavailable {
				t.Fatalf("POST /api/server/audit/chain: status %d, want any other status (body %q)", status, body)
			}
		})
	}
}

// TestApiRunsNoSchedules proves a process with only the api component starts
// no operation.
func TestApiRunsNoSchedules(t *testing.T) {
	t.Parallel()

	p := newPorts(t)

	startComponentServerOn(t, p, mustParse(t, "api"), dustpan.WithOperationLease(stuckLease))
	pause(t, stuckWait)

	ops, err := p.st.DurableOperations().List(
		t.Context(), durable.Filter{Kind: "", States: nil, Limit: 0},
	)
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}

	if len(ops) != 0 {
		t.Fatalf("api process started %d operations, want none", len(ops))
	}
}

// startComponentServerOn starts a server over p and stops it when the test
// ends. The server has a durable store.
func startComponentServerOn(
	t *testing.T, p ports, set dustpan.Components, extra ...dustpan.Option,
) runningServer {
	t.Helper()

	//nolint:exhaustruct_v5 // The test listens with defaults; only the context matters.
	lc := &net.ListenConfig{}

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	registerConsole(t, ln.Addr().String(), p.st)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn),
		dustpan.WithComponents(set),
		dustpan.WithListener(ln),
		dustpan.WithDurableStore(p.st.DurableOperations()),
	)

	opts = append(opts, signInOptions(t, p.st)...)

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- srv.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		if runErr := <-done; runErr != nil {
			t.Errorf("run server: %v", runErr)
		}
	})

	base := "http://" + ln.Addr().String()
	waitLive(t, base)

	return runningServer{base: base}
}

// waitLive polls the liveness route until the server answers.
func waitLive(t *testing.T, base string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/health/live", http.NoBody)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close() //nolint:errcheck // The probe reads only the status.

			return
		}

		pause(t, 10*time.Millisecond)
	}

	t.Fatal("the server did not answer liveness in time")
}

// workerMetricsServer starts a server with only the worker component and the
// extra options, and a debug logger that writes to the returned buffer.
func workerMetricsServer(t *testing.T, extra ...dustpan.Option) (runningServer, *syncBuffer) {
	t.Helper()

	logs := &syncBuffer{mu: sync.Mutex{}, buf: strings.Builder{}}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := startComponentServer(t, mustParse(t, "worker"), append(extra, dustpan.WithLogger(logger))...)

	return srv, logs
}

// getMetrics sends one GET to the metrics route with the admin token and the
// headers, and answers the response headers.
func getMetrics(t *testing.T, base string, headers map[string]string) http.Header {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/api/server/metrics", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET metrics: %v", err)
	}

	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		t.Fatalf("read body: %v", copyErr)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	return resp.Header
}

// TestWorkerOnlyAppliesCORSToMetrics proves that a process with only the
// worker component applies the allowed origins to its metrics route.
func TestWorkerOnlyAppliesCORSToMetrics(t *testing.T) {
	t.Parallel()

	srv, _ := workerMetricsServer(t, dustpan.WithAllowedOrigins("https://console.example"))

	allowed := getMetrics(t, srv.base, map[string]string{"Origin": "https://console.example"})
	if got := allowed.Get("Access-Control-Allow-Origin"); got != "https://console.example" {
		t.Fatalf("allowed origin: Access-Control-Allow-Origin = %q, want the origin", got)
	}

	other := getMetrics(t, srv.base, map[string]string{"Origin": "https://other.example"})
	if got := other.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("other origin: Access-Control-Allow-Origin = %q, want none", got)
	}
}

// TestWorkerOnlyAppliesTrustedProxyRule proves that a process with only the
// worker component reads the client address by the trusted proxy rule: the
// request log names the forwarded address from a trusted peer, and names the
// peer when no proxy is trusted.
func TestWorkerOnlyAppliesTrustedProxyRule(t *testing.T) {
	t.Parallel()

	forwarded := map[string]string{"X-Forwarded-For": "198.51.100.7"}

	trusted, trustedLogs := workerMetricsServer(t, dustpan.WithTrustedProxies(netip.MustParsePrefix("127.0.0.0/8")))
	getMetrics(t, trusted.base, forwarded)

	waitLog(t, trustedLogs, "client_ip=198.51.100.7")

	plain, plainLogs := workerMetricsServer(t)
	getMetrics(t, plain.base, forwarded)

	waitLog(t, plainLogs, "client_ip=127.0.0.1")

	if strings.Contains(plainLogs.String(), "198.51.100.7") {
		t.Fatalf("no trusted proxy: log names the forwarded address:\n%s", plainLogs.String())
	}
}

// waitLog waits until the log holds want. The request log line comes after
// the response, so the test polls.
func waitLog(t *testing.T, logs *syncBuffer, want string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), want) {
			return
		}

		pause(t, 20*time.Millisecond)
	}

	t.Fatalf("log lacks %q:\n%s", want, logs.String())
}

// syncBuffer is a log buffer that the server goroutines and the test share.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

// Write implements io.Writer.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // strings.Builder never fails.
}

// String answers the text written so far.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
