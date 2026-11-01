package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// fakeEnv is the variable that turns the test binary into a fake server.
// It matches fakeServerEnv in fake_server_test.go.
const fakeEnv = "LOAD_FAKE_SERVER"

// fakeProc answers a process that runs this test binary as a fake server.
func fakeProc(t *testing.T, mode string, startTimeout, stopTimeout time.Duration) *serverProc {
	t.Helper()

	proc := newServerProc(procSpec{
		index: 0, port: 0, bin: os.Args[0], args: []string{"serve"},
		env: []string{fakeEnv + "=" + mode}, dir: t.TempDir(),
		startTimeout: startTimeout, stopTimeout: stopTimeout,
	})

	t.Cleanup(func() {
		if _, err := proc.stop(); err != nil && !errors.Is(err, errKilled) {
			t.Logf("cleanup stop: %v", err)
		}
	})

	return proc
}

func TestServerProcLifecycle(t *testing.T) {
	t.Parallel()

	proc := fakeProc(t, "ready", 10*time.Second, 10*time.Second)

	if proc.pid() != 0 {
		t.Fatalf("pid before start = %d, want 0", proc.pid())
	}

	if err := proc.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	firstURL := proc.url()

	if !proc.healthy.Load() || proc.pid() == 0 || firstURL == "http://127.0.0.1:0" {
		t.Fatalf("after start: healthy=%v pid=%d url=%s", proc.healthy.Load(), proc.pid(), firstURL)
	}

	if _, err := proc.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if proc.healthy.Load() || proc.pid() != 0 {
		t.Fatalf("after stop: healthy=%v pid=%d, want false and 0", proc.healthy.Load(), proc.pid())
	}

	if err := proc.start(t.Context()); err != nil {
		t.Fatalf("restart: %v", err)
	}

	if proc.url() != firstURL {
		t.Fatalf("restart url = %s, want the same port %s", proc.url(), firstURL)
	}
}

func TestServerProcKillsAfterTimeout(t *testing.T) {
	t.Parallel()

	proc := fakeProc(t, "ignoreterm", 10*time.Second, 300*time.Millisecond)

	if err := proc.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := proc.stop(); !errors.Is(err, errKilled) {
		t.Fatalf("stop of a server that ignores SIGTERM = %v, want errKilled", err)
	}

	if proc.pid() != 0 {
		t.Fatalf("pid after kill = %d, want 0", proc.pid())
	}
}

func TestServerProcExitBeforeReady(t *testing.T) {
	t.Parallel()

	proc := fakeProc(t, "exit", 10*time.Second, time.Second)

	if err := proc.start(t.Context()); !errors.Is(err, errProcExited) {
		t.Fatalf("start of a server that exits = %v, want errProcExited", err)
	}
}

func TestServerProcStartTimeoutEndsProcess(t *testing.T) {
	t.Parallel()

	proc := fakeProc(t, "silent", 600*time.Millisecond, 5*time.Second)

	if err := proc.start(t.Context()); !errors.Is(err, errProcTimeout) {
		t.Fatalf("start of a silent server = %v, want errProcTimeout", err)
	}

	if proc.pid() != 0 || proc.healthy.Load() {
		t.Fatalf("after a failed start: pid=%d healthy=%v, want 0 and false", proc.pid(), proc.healthy.Load())
	}
}

func TestBalancerNoHealthyInstance(t *testing.T) {
	t.Parallel()

	lb, err := newBalancer([]*serverProc{newServerProc(procSpec{
		index: 0, port: 1, bin: "x", args: nil, env: nil, dir: t.TempDir(), startTimeout: 0, stopTimeout: 0,
	})})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(lb.transport.CloseIdleConnections)

	rec := httptest.NewRecorder()
	lb.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/anything", http.NoBody))

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("answer = %d with Retry-After %q, want 503 and 1", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestBalancerBadGatewayWhenInstanceDies(t *testing.T) {
	t.Parallel()

	var config net.ListenConfig

	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	tcp, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("the listener address is not TCP")
	}

	port := tcp.Port

	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	proc := newServerProc(procSpec{
		index: 0, port: port, bin: "x", args: nil, env: nil, dir: t.TempDir(), startTimeout: 0, stopTimeout: 0,
	})
	proc.healthy.Store(true)

	lb, err := newBalancer([]*serverProc{proc})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(lb.transport.CloseIdleConnections)

	rec := httptest.NewRecorder()
	lb.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/anything", http.NoBody))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("answer = %d, want 502 for an instance that died", rec.Code)
	}
}

func TestStatFieldsAndCPU(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		line    string
		want    float64
		wantErr bool
	}{
		{
			name: "plain command name",
			line: "42 (serve) S 1 42 42 0 -1 4194304 100 0 0 0 150 50 0 0 20 0 8 0 100 1000 100",
			want: 2, wantErr: false,
		},
		{
			name: "command name with a space and a closing parenthesis",
			line: "42 (my (odd) name) S 1 42 42 0 -1 4194304 100 0 0 0 300 100 0 0 20 0 8 0 100 1000 100",
			want: 4, wantErr: false,
		},
		{name: "no parenthesis", line: "42 serve S 1", want: 0, wantErr: true},
		{name: "too few fields", line: "42 (serve) S 1 42", want: 0, wantErr: true},
		{
			name: "utime is not a number",
			line: "42 (serve) S 1 42 42 0 -1 4194304 100 0 0 0 x 50 0 0 20 0 8 0 100 1000 100", want: 0, wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cpuSecondsOf([]byte(tc.line))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v", err, tc.wantErr)
			}

			if !tc.wantErr && math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("cpu seconds = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPortFromLog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		logs string
		port int
		ok   bool
	}{
		{`{"msg":"listening","addr":"127.0.0.1:41233"}`, 41233, true},
		{`{"msg":"setting","key":"x"}` + "\n" + `{"msg":"listening","addr":"[::1]:8080"}`, 8080, true},
		{`{"msg":"listening","addr":"127.0.0.1:0"}`, 0, false},
		{`{"msg":"listening","addr":"not an address"}`, 0, false},
		{`no address here`, 0, false},
	}

	for _, tc := range cases {
		port, ok := portFromLog(tc.logs)
		if port != tc.port || ok != tc.ok {
			t.Errorf("portFromLog(%q) = %d, %v; want %d, %v", tc.logs, port, ok, tc.port, tc.ok)
		}
	}
}

func TestResourcesOf(t *testing.T) {
	t.Parallel()

	in := resourceInput{
		begin:    cpuMark{instances: 10, postgres: 20, store: 5, harness: 1},
		end:      cpuMark{instances: 14, postgres: 26, store: 6, harness: 2},
		txBegin:  100,
		txEnd:    700,
		window:   10 * time.Second,
		requests: 2000,
		counts:   []int{4, 8, 6},
		postgres: true,
		store:    true,
	}

	got := resourcesOf(in)

	// 4 core-seconds in 10 s is 0.4 cores. 4 core-seconds for 2000 requests
	// is 2 core-ms for each request, which is 2000 core-ms for 1000 requests.
	checks := []struct {
		name      string
		got, want float64
	}{
		{"instance cores", got.InstanceCores, 0.4},
		{"postgres cores", *got.PostgresCores, 0.6},
		{"store cores", *got.StoreCores, 0.1},
		{"harness cores", got.HarnessCores, 0.1},
		{"transactions", got.DBTransactionsPerSec, 60},
		{"instance cost", got.InstanceCoreMsPer1k, 2000},
		{"postgres cost", *got.PostgresCoreMsPer1k, 3000},
		{"connections mean", got.DBConnectionsMean, 6},
		{"connections max", float64(got.DBConnectionsMax), 8},
	}

	for _, check := range checks {
		if math.Abs(check.got-check.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}

	if len(got.Unmeasured) != 0 {
		t.Errorf("unmeasured = %v, want none", got.Unmeasured)
	}
}

func TestResourcesOfUnmeasuredPartsHaveNoValue(t *testing.T) {
	t.Parallel()

	got := resourcesOf(resourceInput{
		begin: cpuMark{instances: 0, postgres: 0, store: 0, harness: 0},
		end: cpuMark{
			instances: 1,
			postgres:  0,
			store:     0,
			harness:   0,
		},
		txBegin:  0,
		txEnd:    0,
		window:   time.Second,
		requests: 10,
		counts:   nil,
		postgres: false,
		store:    false,
	})

	if got.PostgresCores != nil || got.StoreCores != nil || got.PostgresCoreMsPer1k != nil {
		t.Fatalf("unmeasured parts have values: %+v", got)
	}

	if len(got.Unmeasured) != 2 {
		t.Fatalf("unmeasured = %v, want postgresCores and storeCores", got.Unmeasured)
	}
}

func TestExecuteScaleRefusesBadFlags(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	cases := map[string]scaleFlags{
		"no binary": {
			binary:      "",
			instances:   1,
			clients:     1,
			duration:    time.Second,
			warmup:      0,
			think:       0,
			restart:     false,
			restartTail: 0,
			storePID:    0,
		},
		"no instances": {
			binary:      "x",
			instances:   0,
			clients:     1,
			duration:    time.Second,
			warmup:      0,
			think:       0,
			restart:     false,
			restartTail: 0,
			storePID:    0,
		},
		"no clients": {
			binary:      "x",
			instances:   1,
			clients:     0,
			duration:    time.Second,
			warmup:      0,
			think:       0,
			restart:     false,
			restartTail: 0,
			storePID:    0,
		},
		"no duration": {
			binary:      "x",
			instances:   1,
			clients:     1,
			duration:    0,
			warmup:      0,
			think:       0,
			restart:     false,
			restartTail: 0,
			storePID:    0,
		},
	}

	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := executeScale(context.Background(), logger, newTestFlags(flags), t.TempDir())
			if !errors.Is(err, errScaleBadFlags) {
				t.Fatalf("error = %v, want errScaleBadFlags", err)
			}
		})
	}
}

func TestScaleRunShutdownLeavesNothingRunning(t *testing.T) {
	t.Parallel()

	proc := fakeProc(t, "ready", 10*time.Second, 10*time.Second)
	if err := proc.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	flags := newTestFlags(scaleFlags{
		binary: "x", instances: 1, clients: 1, duration: time.Second,
		warmup: 0, think: 0, restart: false, restartTail: 0, storePID: 0,
	})
	run := newScaleRun(flags, []*serverProc{proc})

	// The fake server has no namespace route, so the run fails at the
	// namespace step, after it started the balancer and the health check.
	// The package leak check then shows whether shutdown ended them.
	if _, err := run.prepare(t.Context(), logger, flags); !errors.Is(err, errScaleNamespace) {
		t.Fatalf("prepare = %v, want errScaleNamespace", err)
	}

	run.shutdown(t.Context(), logger)

	if proc.pid() != 0 {
		t.Fatalf("pid after shutdown = %d, want 0", proc.pid())
	}
}

// newTestFlags answers harness flags for the scale mode.
func newTestFlags(scale scaleFlags) harnessFlags {
	return harnessFlags{
		out: "", units: 1, ns: "load", flows: flowsScale, blobs: "", probe: false, maxDuration: 0,
		blobCfg: DefaultBlobFlowConfig(), scale: scale,
	}
}
