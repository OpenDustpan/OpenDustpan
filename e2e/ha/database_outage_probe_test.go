package ha_test

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/ha"
)

type probeProxyFake struct {
	stops  int
	starts int
}

func (p *probeProxyFake) Stop(tb testing.TB) {
	tb.Helper()
	p.stops++
}

func (p *probeProxyFake) Start(tb testing.TB) {
	tb.Helper()
	p.starts++
}

type probeInstanceFake struct {
	name string
}

func (i probeInstanceFake) Name() string {
	return i.name
}

func (i probeInstanceFake) Send(_ context.Context, _ ha.InstanceRequest) (ha.InstanceResponse, error) {
	return ha.InstanceResponse{Status: http.StatusOK, Header: http.Header{}, Body: ""}, nil
}

// probeReadyFake answers GET /health/ready with 503 until the proxy started
// the database, then with 200. A stuck instance answers 503 always.
type probeReadyFake struct {
	proxy *probeProxyFake
	stuck bool
}

func (i probeReadyFake) Name() string {
	return "i0"
}

func (i probeReadyFake) Send(_ context.Context, _ ha.InstanceRequest) (ha.InstanceResponse, error) {
	if i.stuck || i.proxy.starts == 0 {
		return ha.InstanceResponse{Status: http.StatusServiceUnavailable, Header: http.Header{}, Body: ""}, nil
	}

	return ha.InstanceResponse{Status: http.StatusOK, Header: http.Header{}, Body: ""}, nil
}

// probeTBFake records a fatal failure instead of ending the test.
type probeTBFake struct {
	testing.TB

	fatal string
}

func (f *probeTBFake) Helper() {}

func (f *probeTBFake) Fatalf(format string, args ...any) {
	f.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// restoreWith runs Restore in its own goroutine, so that a fatal failure ends
// only that goroutine, and returns the fatal message.
func restoreWith(ctx context.Context, t *testing.T, probe *ha.DatabaseOutageProbe) string {
	t.Helper()

	tb := &probeTBFake{TB: t, fatal: ""}
	done := make(chan struct{})

	go func() {
		defer close(done)
		probe.Restore(ctx, tb)
	}()

	<-done

	return tb.fatal
}

// TestDatabaseOutageProbeRestore proves that Restore starts the database and
// waits until every instance is ready, and that it reports a blocked restart
// when an instance does not turn ready.
func TestDatabaseOutageProbeRestore(t *testing.T) {
	t.Parallel()

	matrix := []ha.CLIVersion{{Tool: "pulumi", Version: "3.262.0"}}

	tests := []struct {
		name  string
		stuck bool
		want  string
	}{
		{name: "instance turns ready", stuck: false, want: ""},
		{name: "instance stays unready", stuck: true, want: "blocked database restart"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proxy := &probeProxyFake{stops: 0, starts: 0}
			instance := probeReadyFake{proxy: proxy, stuck: tt.stuck}

			probe, err := ha.NewDatabaseOutageProbe(proxy, []ha.Instance{instance}, matrix)
			if err != nil {
				t.Fatalf("NewDatabaseOutageProbe: %v", err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()

			fatal := restoreWith(ctx, t, probe)

			if proxy.starts != 1 {
				t.Errorf("Restore started the database %d times, want 1", proxy.starts)
			}

			if tt.want == "" && fatal != "" {
				t.Errorf("Restore failed: %s", fatal)
			}

			if tt.want != "" && !strings.Contains(fatal, tt.want) {
				t.Errorf("Restore failure is %q, want it to contain %q", fatal, tt.want)
			}
		})
	}
}

// probePartFake answers the state version write with writeStatus and the lock
// request with lockStatus, each with Retry-After 5.
type probePartFake struct {
	writeStatus int
	lockStatus  int
}

func (i probePartFake) Name() string {
	return "i0"
}

func (i probePartFake) Send(_ context.Context, req ha.InstanceRequest) (ha.InstanceResponse, error) {
	status := i.writeStatus
	if strings.HasSuffix(req.Path, "/actions/lock") {
		status = i.lockStatus
	}

	return ha.InstanceResponse{Status: status, Header: http.Header{"Retry-After": {"5"}}, Body: ""}, nil
}

// TestDatabaseOutageProbeCapabilities proves that the capability probe names
// the missing server part when a write or a lock during the stop does not
// answer 503, and accepts a server that has both parts.
func TestDatabaseOutageProbeCapabilities(t *testing.T) {
	t.Parallel()

	matrix := []ha.CLIVersion{{Tool: "pulumi", Version: "3.262.0"}}
	write := ha.InstanceRequest{
		Method: http.MethodPost, Path: "/api/v2/workspaces/ws-outage/state-versions", Header: nil, Body: "",
	}
	lock := ha.InstanceRequest{
		Method: http.MethodPost, Path: "/api/v2/workspaces/ws-outage/actions/lock", Header: nil, Body: "",
	}

	tests := []struct {
		name        string
		writeStatus int
		lockStatus  int
		want        string
	}{
		{
			name: "both parts", writeStatus: http.StatusServiceUnavailable,
			lockStatus: http.StatusServiceUnavailable, want: "",
		},
		{
			name: "no 503 shape", writeStatus: http.StatusInternalServerError,
			lockStatus: http.StatusServiceUnavailable,
			want:       "missing part database-outage-503-shape",
		},
		{
			name: "no atomic state locks", writeStatus: http.StatusServiceUnavailable,
			lockStatus: http.StatusInternalServerError,
			want:       "missing part database-outage-atomic-state-locks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proxy := &probeProxyFake{stops: 0, starts: 0}
			instance := probePartFake{writeStatus: tt.writeStatus, lockStatus: tt.lockStatus}

			probe, err := ha.NewDatabaseOutageProbe(proxy, []ha.Instance{instance}, matrix)
			if err != nil {
				t.Fatalf("NewDatabaseOutageProbe: %v", err)
			}

			err = probe.ProbeCapabilities(t.Context(), t, write, lock, "5")

			if proxy.stops != 1 {
				t.Errorf("ProbeCapabilities stopped the database %d times, want 1", proxy.stops)
			}

			checkPartError(t, err, tt.want)
		})
	}
}

// checkPartError checks that err is nil when want is empty, and that it
// contains want otherwise.
func checkPartError(t *testing.T, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Errorf("ProbeCapabilities: %v", err)
		}

		return
	}

	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("ProbeCapabilities error is %v, want it to contain %q", err, want)
	}
}

// TestNewDatabaseOutageProbe proves that the fixture refuses an empty CLI
// matrix and accepts a real one.
func TestNewDatabaseOutageProbe(t *testing.T) {
	t.Parallel()

	proxy := &probeProxyFake{stops: 0, starts: 0}
	targets := []ha.Instance{probeInstanceFake{name: "i0"}}

	if _, err := ha.NewDatabaseOutageProbe(proxy, targets, nil); err == nil {
		t.Fatal("NewDatabaseOutageProbe accepted an empty CLI matrix")
	}

	matrix := []ha.CLIVersion{{Tool: "pulumi", Version: "3.262.0"}}
	probe, err := ha.NewDatabaseOutageProbe(proxy, targets, matrix)
	if err != nil {
		t.Fatalf("NewDatabaseOutageProbe: %v", err)
	}

	if got := len(probe.Matrix()); got != 1 {
		t.Errorf("matrix holds %d CLI versions, want 1", got)
	}

	if got := len(probe.Instances()); got != 1 {
		t.Errorf("instance list holds %d instances, want 1", got)
	}
}

// TestDatabaseOutageProbeShortWindow proves that the fixture sets the short
// stop to half the smallest measured retry window and refuses a longer stop.
func TestDatabaseOutageProbeShortWindow(t *testing.T) {
	t.Parallel()

	proxy := &probeProxyFake{stops: 0, starts: 0}
	matrix := []ha.CLIVersion{
		{Tool: "pulumi", Version: "3.262.0"},
		{Tool: "tofu", Version: "1.12.12"},
	}

	probe, err := ha.NewDatabaseOutageProbe(proxy, []ha.Instance{probeInstanceFake{name: "i0"}}, matrix)
	if err != nil {
		t.Fatalf("NewDatabaseOutageProbe: %v", err)
	}

	report := ha.NewRetryReport(matrix)
	base := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)

	attempts := []ha.WriteAttempt{
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base,
		},
		{
			Version: "pulumi 3.262.0",
			Method:  http.MethodPost,
			Path:    "/api/v2/state-versions",
			Status:  http.StatusServiceUnavailable,
			At:      base.Add(10 * time.Second),
		},
	}

	for _, attempt := range attempts {
		err = report.Add(attempt)
		if err != nil {
			t.Fatalf("record a retry: %v", err)
		}
	}

	window, err := probe.ShortWindow(report)
	if err != nil {
		t.Fatalf("ShortWindow: %v", err)
	}

	if window != 5*time.Second {
		t.Errorf("short window is %s, want 5s", window)
	}

	if err := probe.CheckShortWindow(6*time.Second, report); err == nil {
		t.Error("CheckShortWindow accepted a stop above half the smallest retry window")
	}

	if err := probe.CheckShortWindow(4*time.Second, report); err != nil {
		t.Errorf("CheckShortWindow refused a stop below half the smallest retry window: %v", err)
	}
}
