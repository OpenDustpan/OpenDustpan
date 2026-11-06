package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSetHealthyIfIgnoresOldProbes(t *testing.T) {
	t.Parallel()

	proc := fakeProc(t, "ready", 10*time.Second, 10*time.Second)
	if err := proc.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	// A probe begins while the instance is healthy and the instance stops
	// before the probe ends. The answer of the probe is old.
	gen := proc.generation()

	if _, err := proc.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	proc.setHealthyIf(gen, true)

	if proc.healthy.Load() {
		t.Fatal("an old probe put a stopped instance back in the pool")
	}

	// A probe that begins after the stop started must not set the health
	// either, even with the new generation, until the next start.
	proc.setHealthyIf(proc.generation(), true)

	if proc.healthy.Load() {
		t.Fatal("a probe put a stopping instance back in the pool")
	}

	if err := proc.start(t.Context()); err != nil {
		t.Fatalf("restart: %v", err)
	}

	proc.setHealthyIf(proc.generation(), false)

	if proc.healthy.Load() {
		t.Fatal("a current probe that failed did not take the instance out of the pool")
	}
}

func TestMarksReportsFailedReads(t *testing.T) {
	t.Parallel()

	running := fakeProc(t, "ready", 10*time.Second, 10*time.Second)
	if err := running.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	stopped := fakeProc(t, "ready", time.Second, time.Second)

	cases := []struct {
		name     string
		mon      *monitor
		wantInst bool
		wantPG   bool
		wantSt   bool
	}{
		{"a running instance", blankMonitor(nil, []*serverProc{running}, 0), true, false, false},
		{"an instance that does not run", blankMonitor(nil, []*serverProc{running, stopped}, 0), false, false, false},
		{"no instance", blankMonitor(nil, nil, 0), false, false, false},
		{
			"a PID that is not ours",
			monitorWithPIDs(running, 1<<30, 1<<30),
			true,
			false,
			false,
		},
		{"our own PID as the store", monitorWithPIDs(running, 0, os.Getpid()), true, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mark := tc.mon.marks()
			if mark.instancesOK != tc.wantInst || mark.postgresOK != tc.wantPG || mark.storeOK != tc.wantSt {
				t.Fatalf("flags = instances %v, postgres %v, store %v; want %v, %v, %v",
					mark.instancesOK, mark.postgresOK, mark.storeOK, tc.wantInst, tc.wantPG, tc.wantSt)
			}

			if !mark.harnessOK {
				t.Fatal("the CPU time of the harness was not read")
			}
		})
	}
}

func TestMonitorWindowOnPostgres(t *testing.T) {
	t.Parallel()

	pgURL := os.Getenv(postgresURLEnv)
	if pgURL == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the monitor against PostgreSQL")
	}

	proc := fakeProc(t, "ready", 10*time.Second, 10*time.Second)
	if err := proc.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	mon, err := newMonitor(t.Context(), pgURL, []*serverProc{proc}, 0)
	if err != nil {
		t.Fatalf("newMonitor: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := mon.close(context.Background()); closeErr != nil {
			t.Logf("close: %v", closeErr)
		}
	})

	mon.begin(t.Context())
	generateTransactions(t, pgURL, 50)
	sleep(t.Context(), 2*sampleEvery)

	got := mon.end(t.Context(), 2*sampleEvery, 100)

	if got.InstanceCores == nil || got.DBTransactionsPerSec == nil || *got.DBTransactionsPerSec <= 0 {
		t.Fatalf("instance cores or transactions were not measured: %+v", got)
	}

	if mon.pgRoot != 0 && got.PostgresCores == nil {
		t.Fatalf("the postmaster was found but its CPU is missing: %+v", got)
	}

	checkUnmeasured(t, got, []string{"storeCores"})
}

// generateTransactions runs n transactions on a connection of its own. The
// monitor owns its connection while it samples.
func generateTransactions(t *testing.T, pgURL string, n int) {
	t.Helper()

	loader, err := pgx.Connect(t.Context(), pgURL)
	if err != nil {
		t.Fatalf("connect the load connection: %v", err)
	}

	defer func() {
		if closeErr := loader.Close(context.Background()); closeErr != nil {
			t.Logf("close the load connection: %v", closeErr)
		}
	}()

	for range n {
		if _, execErr := loader.Exec(t.Context(), "SELECT 1"); execErr != nil {
			t.Fatalf("exec: %v", execErr)
		}
	}
}

func TestMonitorEndWithoutPostgresRead(t *testing.T) {
	t.Parallel()

	// A window where the transaction count could not be read at the begin:
	// the rate is absent, never negative or huge.
	got := resourcesOf(resourceInput{
		begin: markOK(0, 0, 0, 0), end: markOK(1, 1, 1, 1), txBegin: 0, txEnd: 9_000_000, txOK: false,
		window: time.Second, requests: 1, counts: []int{1},
	})

	if got.DBTransactionsPerSec != nil {
		t.Fatalf("rate = %v, want no value", *got.DBTransactionsPerSec)
	}
}

// Errors that the tests give to restartMayStart.
var (
	errTestSignal = errors.New("signal process group: operation not permitted")
	errTestExit   = errors.New("server 0 exit: exit status 1")
)

func TestRestartMayStart(t *testing.T) {
	t.Parallel()

	signalErr := errTestSignal

	cases := []struct {
		name    string
		stopErr error
		running bool
		want    bool
	}{
		{"clean stop", nil, false, true},
		{"killed after the timeout", errKilled, false, true},
		{"exit status of a stopped process", errTestExit, false, true},
		{"a failed signal and the process still runs", signalErr, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := restartMayStart(tc.stopErr, tc.running); got != tc.want {
				t.Fatalf("restartMayStart = %v, want %v", got, tc.want)
			}
		})
	}
}

// restartRun answers a run over proc with a recorder, for restartOne.
func restartRun(proc *serverProc) *scaleRun {
	flags := newTestFlags(scaleFlags{
		binary: "x", instances: 1, clients: 1, duration: time.Second,
		warmup: 0, think: 0, restart: true, restartTail: 100 * time.Millisecond, storePID: 0,
	})
	run := newScaleRun(flags, []*serverProc{proc})
	run.rec = newRecorder(http.DefaultTransport)

	return run
}

func TestRestartOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		mode         string
		stopTimeout  time.Duration
		wantStopErr  bool
		wantStartErr bool
	}{
		{"a clean restart", "ready", 10 * time.Second, false, false},
		{
			"a server that ignores SIGTERM is killed and started again",
			"ignoreterm",
			300 * time.Millisecond,
			true,
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			proc := fakeProc(t, tc.mode, 10*time.Second, tc.stopTimeout)
			if err := proc.start(t.Context()); err != nil {
				t.Fatalf("start: %v", err)
			}

			url := proc.url()
			run := restartRun(proc)

			got := run.restartOne(t.Context(), discardLogger())

			if (got.StopError != "") != tc.wantStopErr || (got.StartError != "") != tc.wantStartErr {
				t.Fatalf("stop error %q, start error %q; want stop error %v, start error %v",
					got.StopError, got.StartError, tc.wantStopErr, tc.wantStartErr)
			}

			if got.StartMs <= 0 || got.StopMs <= 0 || proc.url() != url || !proc.healthy.Load() {
				t.Fatalf("restart did not end with the same instance ready: %+v", got)
			}
		})
	}
}

// tfeFake is a server for the lock, upload and unlock requests of one
// workspace. It counts each request.
type tfeFake struct {
	lockStatus   int
	declareOK    bool
	unlockDrops  int32
	unlockStatus int
	locks        atomic.Int32
	unlocks      atomic.Int32
	declares     atomic.Int32
	uploads      atomic.Int32
	server       *httptest.Server
}

// tfeBehavior says how the fake server answers.
type tfeBehavior struct {
	lockStatus   int
	declareOK    bool
	unlockDrops  int32
	unlockStatus int
}

func newTFEFake(t *testing.T, behavior tfeBehavior) *tfeFake {
	t.Helper()

	fake := &tfeFake{
		lockStatus: behavior.lockStatus, declareOK: behavior.declareOK, unlockDrops: behavior.unlockDrops,
		unlockStatus: behavior.unlockStatus, locks: atomic.Int32{}, unlocks: atomic.Int32{},
		declares: atomic.Int32{}, uploads: atomic.Int32{}, server: nil,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/workspaces/{id}/actions/lock", func(w http.ResponseWriter, _ *http.Request) {
		fake.locks.Add(1)
		w.WriteHeader(fake.lockStatus)
	})
	mux.HandleFunc("POST /api/v2/workspaces/{id}/actions/unlock", func(w http.ResponseWriter, _ *http.Request) {
		if fake.unlocks.Add(1) <= fake.unlockDrops {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				return
			}

			conn, _, err := hijacker.Hijack()
			if err != nil {
				return
			}

			if closeErr := conn.Close(); closeErr != nil {
				return
			}

			return
		}

		w.WriteHeader(fake.unlockStatus)
	})
	mux.HandleFunc("POST /api/v2/workspaces/{id}/state-versions", func(w http.ResponseWriter, _ *http.Request) {
		fake.declares.Add(1)

		if !fake.declareOK {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusCreated)

		body := stateUploadResponse{
			Data: stateUploadData{Attributes: stateUploadAttributes{UploadURL: fake.server.URL + "/upload"}},
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			return
		}
	})
	mux.HandleFunc("PUT /upload", func(w http.ResponseWriter, _ *http.Request) {
		fake.uploads.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// tfeRun answers a run whose target is the fake server.
func tfeRun(fake *tfeFake) (*scaleRun, *scaleClient) {
	run := newScaleRun(newTestFlags(scaleFlags{
		binary: "x", instances: 1, clients: 1, duration: time.Second,
		warmup: 0, think: 0, restart: false, restartTail: 0, storePID: 0,
	}), nil)
	run.target = newTarget([]string{fake.server.URL}, "token", "load")

	return run, &scaleClient{id: 0, workspace: "ws-1", serial: 0}
}

func TestTofuCycle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		lockStatus   int
		declareOK    bool
		unlockDrops  int32
		unlockStatus int
		wantFailed   int64
		wantUnlocks  int32
		wantUploads  int32
	}{
		{"a full cycle", http.StatusOK, true, 0, http.StatusOK, 0, 1, 1},
		{"a refused lock releases a stale lock", http.StatusConflict, true, 0, http.StatusConflict, 1, 1, 0},
		{"a failed declare still unlocks", http.StatusOK, false, 0, http.StatusOK, 1, 1, 0},
		{"an unlock answer that is lost is sent again", http.StatusOK, true, 1, http.StatusOK, 1, 2, 1},
		{
			"an unlock that the server refuses is not sent again",
			http.StatusOK,
			true,
			0,
			http.StatusInternalServerError,
			1,
			1,
			1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newTFEFake(t, tfeBehavior{
				lockStatus:   tc.lockStatus,
				declareOK:    tc.declareOK,
				unlockDrops:  tc.unlockDrops,
				unlockStatus: tc.unlockStatus,
			})
			run, one := tfeRun(fake)

			run.tofuCycle(t.Context(), one)

			if got := run.failed.Load(); got != tc.wantFailed {
				t.Errorf("failed = %d, want %d", got, tc.wantFailed)
			}

			if got := fake.unlocks.Load(); got != tc.wantUnlocks {
				t.Errorf("unlock requests = %d, want %d", got, tc.wantUnlocks)
			}

			if got := fake.uploads.Load(); got != tc.wantUploads {
				t.Errorf("uploads = %d, want %d", got, tc.wantUploads)
			}
		})
	}
}

func TestParseHarnessFlagsScale(t *testing.T) {
	t.Parallel()

	got, err := parseHarnessFlags([]string{
		"-flows", "scale", "-binary", "/bin/serve", "-instances", "3", "-clients", "8", "-duration", "30s",
		"-warmup", "2s", "-think", "250ms", "-restart", "-restart-tail", "5s", "-store-pid", "42", "-units", "10",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := scaleFlags{
		binary: "/bin/serve", instances: 3, clients: 8, duration: 30 * time.Second, warmup: 2 * time.Second,
		think: 250 * time.Millisecond, restart: true, restartTail: 5 * time.Second, storePID: 42,
	}

	if got.scale != want || got.flows != flowsScale || got.units != 10 {
		t.Fatalf("flags = %+v (flows %q, units %d), want %+v", got.scale, got.flows, got.units, want)
	}

	defaults, err := parseHarnessFlags([]string{"-flows", "scale"})
	if err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if defaults.scale.instances != 1 || defaults.scale.clients != defaultScaleClients ||
		defaults.scale.duration != defaultScaleDuration || defaults.scale.restart {
		t.Fatalf("defaults = %+v", defaults.scale)
	}

	if _, badErr := parseHarnessFlags([]string{"-flows", "other"}); !errors.Is(badErr, errBadFlows) {
		t.Fatalf("a bad -flows value gave %v, want errBadFlows", badErr)
	}
}

// monitorWithPIDs answers a monitor over proc that reads the CPU of the
// process pgRoot as PostgreSQL and of the process storePID as the store.
func monitorWithPIDs(proc *serverProc, pgRoot, storePID int) *monitor {
	mon := blankMonitor(nil, []*serverProc{proc}, storePID)
	mon.pgRoot = pgRoot

	return mon
}
