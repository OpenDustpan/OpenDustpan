//go:build e2e && soak

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/soak"
)

// Soak run settings from the environment. The make soak target sets them.
const (
	soakDurationEnv = "SOAK_DURATION"
	soakPulumiEnv   = "SOAK_PULUMI"
	soakTofuEnv     = "SOAK_TOFU"
	soakReportEnv   = "SOAK_REPORT_DIR"
	// defaultSoakDuration is the nightly run length. Short runs such as
	// SOAK_DURATION=60s prove the harness only.
	defaultSoakDuration = "30m"
	defaultSoakWorkers  = 4
	defaultSoakReports  = "soak-reports"
)

// soakConfig is one run: how long it lasts, how many CLI workers per tool
// it runs, and where it writes the CSV and the summary.
type soakConfig struct {
	duration time.Duration
	pulumi   int
	tofu     int
	reports  string
	store    e2e.Store
}

// soakCounters counts CLI runs and their failures across workers.
type soakCounters struct {
	runs     atomic.Int64
	failures atomic.Int64
}

// soakTools holds one installed CLI per tool: the version that Single
// returns, so E2E_PULUMI_VERSIONS and E2E_TOFU_VERSIONS select them.
type soakTools struct {
	pulumi e2e.Install
	tofu   e2e.Install
}

// TestSoak runs many concurrent real Pulumi CLI and OpenTofu processes
// against one server binary until the duration ends, samples the server
// every 10 s, and fails on growth or errors (ADR 40 decision 4). Run it
// with `make soak`; it needs E2E_SERVER=binary and downloaded CLIs, so it
// stays behind the soak build tag and out of `make e2e`.
//
//nolint:paralleltest // The run owns one server for its full duration, so it never runs beside other tests.
func TestSoak(t *testing.T) {
	if !e2e.UseBinaryServer() {
		t.Fatal("TestSoak needs E2E_SERVER=binary: run it with make soak")
	}

	cfg := soakConfigFromEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), cfg.duration+10*time.Minute)
	defer cancel()

	tools := soakInstallTools(ctx, t, cfg)
	env := e2e.NewEnv(ctx, t, tools.pulumi, cfg.store)

	workerCtx, stopWorkers := context.WithCancel(ctx)

	driver := soakDriver{env: env, counters: new(soakCounters), workers: &sync.WaitGroup{}}
	driver.startPulumiWorkers(workerCtx, t, tools.pulumi, cfg.pulumi)
	driver.startTofuWorkers(workerCtx, t, tools.tofu, cfg.tofu)

	samples := soakSampleUntil(ctx, t, env, cfg.duration, driver.counters)

	// Stop the worker loops now that sampling ended: in-flight CLI runs
	// drain and the test ends instead of burning the 10-minute headroom.
	stopWorkers()
	driver.workers.Wait()

	assessed, err := soak.Assess(string(cfg.store), samples)
	if err != nil {
		t.Fatalf("assess soak samples: %v", err)
	}

	paths := soakWriteReports(t, cfg, samples, assessed)
	t.Logf("soak: %d samples, %d runs, %d failures, pass=%v\ncsv: %s\nsummary: %s",
		assessed.Samples, assessed.TotalRuns, assessed.TotalFailures, assessed.Pass, paths[0], paths[1])

	if !assessed.Pass {
		t.Fatalf("soak run failed growth or error checks, see %s", paths[1])
	}
}

// soakConfigFromEnv reads the run settings. SQLite is the default store;
// PostgreSQL runs when DUSTPAN_TEST_POSTGRES_URL is set.
func soakConfigFromEnv(t *testing.T) soakConfig {
	t.Helper()

	duration := soakEnvOr(soakDurationEnv, defaultSoakDuration)

	parsed, err := time.ParseDuration(duration)
	if err != nil || parsed <= 0 {
		t.Fatalf("parse %s %q: want a positive duration", soakDurationEnv, duration)
	}

	store := e2e.StoreSQLite
	if e2e.PostgresURL() != "" {
		store = e2e.StorePostgres
	}

	pulumi := soakWorkerCount(t, soakPulumiEnv)
	tofu := soakWorkerCount(t, soakTofuEnv)

	if pulumi == 0 && tofu == 0 {
		t.Fatalf("need at least one worker: set %s or %s above 0", soakPulumiEnv, soakTofuEnv)
	}

	return soakConfig{
		duration: parsed,
		pulumi:   pulumi,
		tofu:     tofu,
		reports:  soakEnvOr(soakReportEnv, defaultSoakReports),
		store:    store,
	}
}

// soakEnvOr answers the environment value, or fallback when it is empty.
func soakEnvOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

// soakWorkerCount reads one worker count, or the default when it is unset.
func soakWorkerCount(t *testing.T, name string) int {
	t.Helper()

	raw := os.Getenv(name)
	if raw == "" {
		return defaultSoakWorkers
	}

	count, err := strconv.Atoi(raw)
	if err != nil || count < 0 {
		t.Fatalf("parse %s %q: want a count of at least 0", name, raw)
	}

	return count
}

// soakInstallTools downloads one Pulumi CLI and one OpenTofu CLI release.
// The tofu CLI is skipped when no tofu worker runs, and off Linux, where
// the tofu workers stay down because the suite trusts the test server on
// Linux only.
func soakInstallTools(ctx context.Context, t *testing.T, cfg soakConfig) soakTools {
	t.Helper()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	pulumiTool := e2e.Pulumi()
	pulumiVersion := pulumiTool.Single()
	if pulumiVersion == "" {
		t.Fatalf("empty %s %q: want at least one version", "E2E_PULUMI_VERSIONS", os.Getenv("E2E_PULUMI_VERSIONS"))
	}

	pulumi, err := fetcher.Install(ctx, pulumiTool, pulumiVersion)
	if err != nil {
		t.Fatalf("install pulumi: %v", err)
	}

	var tools soakTools

	tools.pulumi = pulumi

	if cfg.tofu > 0 && runtime.GOOS == "linux" {
		tofuTool := e2e.OpenTofu()
		tofuVersion := tofuTool.Single()
		if tofuVersion == "" {
			t.Fatalf("empty %s %q: want at least one version", "E2E_TOFU_VERSIONS", os.Getenv("E2E_TOFU_VERSIONS"))
		}

		tools.tofu, err = fetcher.Install(ctx, tofuTool, tofuVersion)
		if err != nil {
			t.Fatalf("install opentofu: %v", err)
		}
	}

	return tools
}

// soakPulumiProgram returns the soak Pulumi program with one changing
// output, so every up writes a new checkpoint without provider downloads.
func soakPulumiProgram(greeting string) string {
	return "name: " + e2e.Project + "\nruntime: yaml\noutputs:\n  greeting: " + greeting + "\n"
}

// soakTofuConfig returns the soak OpenTofu configuration of one workspace
// with one changing output, so every apply writes a new state version.
func soakTofuConfig(host, workspace, greeting string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = ` + strconv.Quote(workspace) + `
    }
  }
}

output "greeting" {
  value = ` + strconv.Quote(greeting) + `
}
`
}

// soakDriver runs the worker loops of one soak run against one server.
type soakDriver struct {
	env      *e2e.Env
	counters *soakCounters
	workers  *sync.WaitGroup
}

// soakPulumiWorker is one Pulumi loop: its CLI with its directory and stack.
type soakPulumiWorker struct {
	install e2e.Install
	dir     string
	stack   string
}

// startPulumiWorkers sets one stack per worker up and starts the loops.
func (d *soakDriver) startPulumiWorkers(ctx context.Context, t *testing.T, install e2e.Install, count int) {
	t.Helper()

	for i := range count {
		worker := soakPulumiWorker{
			install: install,
			dir:     t.TempDir(),
			stack:   fmt.Sprintf("%s/%s/soak-pulumi-%d", e2e.Namespace, e2e.Project, i),
		}

		soakWriteFile(t, worker.dir, "Pulumi.yaml", soakPulumiProgram("hello-0"))
		d.setupPulumi(ctx, t, worker)

		d.workers.Go(func() {
			d.runPulumiWorker(ctx, worker)
		})
	}
}

// setupPulumi logs in and creates the worker stack.
func (d *soakDriver) setupPulumi(ctx context.Context, t *testing.T, worker soakPulumiWorker) {
	t.Helper()

	setupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if res, err := d.env.RunAs(setupCtx, worker.install, worker.dir, nil, "login"); err != nil ||
		res.ExitCode != 0 {
		t.Fatalf("soak: pulumi login: %v (exit %d)\n%s", err, res.ExitCode, res.Stderr)
	}

	res, err := d.env.RunAs(setupCtx, worker.install, worker.dir, nil, "stack", "init", worker.stack)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("soak: pulumi stack init %s: %v (exit %d)\n%s", worker.stack, err, res.ExitCode, res.Stderr)
	}
}

// runPulumiWorker runs up with a changed output until the context ends.
func (d *soakDriver) runPulumiWorker(ctx context.Context, worker soakPulumiWorker) {
	for iteration := 1; ctx.Err() == nil; iteration++ {
		content := soakPulumiProgram("hello-" + strconv.Itoa(iteration))
		if err := os.WriteFile(filepath.Join(worker.dir, "Pulumi.yaml"), []byte(content), 0o600); err != nil {
			d.counters.failures.Add(1)
			d.counters.runs.Add(1)

			continue
		}

		res, err := d.env.RunAs(ctx, worker.install, worker.dir, nil,
			"up", "--yes", "--non-interactive", "--stack", worker.stack)

		d.counters.runs.Add(1)

		if err != nil || res.ExitCode != 0 {
			d.counters.failures.Add(1)
		}
	}
}

// soakTofuWorker is one OpenTofu loop: its CLI with its directory and workspace.
type soakTofuWorker struct {
	install   e2e.Install
	dir       string
	workspace string
}

// startTofuWorkers sets one workspace per worker up and starts the loops.
// Off Linux the workers stay down: OpenTofu trusts the test server through
// SSL_CERT_FILE, which Go honors on Linux only.
func (d *soakDriver) startTofuWorkers(ctx context.Context, t *testing.T, install e2e.Install, count int) {
	t.Helper()

	if count == 0 {
		return
	}

	if runtime.GOOS != "linux" {
		t.Logf("soak: no tofu workers: the suite trusts the test server on Linux only, not %s", runtime.GOOS)

		return
	}

	for i := range count {
		worker := soakTofuWorker{install: install, dir: t.TempDir(), workspace: fmt.Sprintf("soak-tofu-%d", i)}

		soakWriteFile(t, worker.dir, "main.tf", soakTofuConfig(d.env.Host(), worker.workspace, "hello-0"))
		d.setupTofu(ctx, t, worker)

		d.workers.Go(func() {
			d.runTofuWorker(ctx, worker)
		})
	}
}

// setupTofu initializes the worker directory against the server.
func (d *soakDriver) setupTofu(ctx context.Context, t *testing.T, worker soakTofuWorker) {
	t.Helper()

	setupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	res, err := d.env.RunAs(setupCtx, worker.install, worker.dir, nil, "init", "-input=false", "-no-color")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("soak: tofu init in %s: %v (exit %d)\n%s", worker.dir, err, res.ExitCode, res.Stderr)
	}
}

// runTofuWorker runs apply with a changed output until the context ends.
func (d *soakDriver) runTofuWorker(ctx context.Context, worker soakTofuWorker) {
	for iteration := 1; ctx.Err() == nil; iteration++ {
		content := soakTofuConfig(d.env.Host(), worker.workspace, "hello-"+strconv.Itoa(iteration))
		if err := os.WriteFile(filepath.Join(worker.dir, "main.tf"), []byte(content), 0o600); err != nil {
			d.counters.failures.Add(1)
			d.counters.runs.Add(1)

			continue
		}

		res, err := d.env.RunAs(ctx, worker.install, worker.dir, nil,
			"apply", "-input=false", "-auto-approve", "-no-color")

		d.counters.runs.Add(1)

		if err != nil || res.ExitCode != 0 {
			d.counters.failures.Add(1)
		}
	}
}

// soakWriteFile writes one setup file, failing the test on error.
func soakWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("soak: write %s: %v", name, err)
	}
}

// soakSampleUntil samples the server every 10 s until the duration ends and
// answers the samples.
func soakSampleUntil(
	ctx context.Context, t *testing.T, env *e2e.Env, duration time.Duration, counters *soakCounters,
) []soak.Sample {
	t.Helper()

	deadline := time.Now().Add(duration)
	start := time.Now()

	var samples []soak.Sample

	previousRuns, previousFailures := counters.runs.Load(), counters.failures.Load()

	for {
		wait := time.Until(deadline)
		if wait <= 0 {
			return samples
		}

		timer := time.NewTimer(min(wait, soak.SampleInterval))
		select {
		case <-ctx.Done():
			timer.Stop()

			return samples
		case <-timer.C:
		}

		runs, failures := counters.runs.Load(), counters.failures.Load()
		samples = append(
			samples,
			soakTakeSample(ctx, env, time.Since(start), runs-previousRuns, failures-previousFailures),
		)
		previousRuns, previousFailures = runs, failures
	}
}

// soakTakeSample reads one 10 s sample: the gauges from /metrics, the open
// file descriptors of the server process, and the interval error rate.
func soakTakeSample(ctx context.Context, env *e2e.Env, elapsed time.Duration, runs, failures int64) soak.Sample {
	sample := soak.Sample{
		ElapsedSeconds: elapsed.Seconds(),
		Goroutines:     -1,
		HeapBytes:      -1,
		FDs:            -1,
		DBConns:        -1,
		Runs:           int(runs),
		Failures:       int(failures),
		ErrorRate:      0,
	}

	if runs > 0 {
		sample.ErrorRate = float64(failures) / float64(runs)
	}

	if status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/metrics", ""); err == nil &&
		status == http.StatusOK {
		if gauges, parseErr := soak.ParseMetrics(body); parseErr == nil {
			sample.Goroutines, sample.HeapBytes, sample.DBConns = gauges.Goroutines, gauges.HeapBytes, gauges.DBConns
		}
	}

	sample.FDs = soakServerFDs(env)

	return sample
}

// soakServerFDs counts the open file descriptors of the server process, or
// -1 when the process is unknown or the count is unreadable (off Linux).
func soakServerFDs(env *e2e.Env) float64 {
	pid, ok := env.ServerPID()
	if !ok {
		return -1
	}

	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return -1
	}

	return float64(len(entries))
}

// soakWriteReports creates the report directory and writes the CSV and the
// summary, answering their paths.
func soakWriteReports(
	t *testing.T, cfg soakConfig, samples []soak.Sample, assessed soak.Assessment,
) [2]string {
	t.Helper()

	if err := os.MkdirAll(cfg.reports, 0o750); err != nil {
		t.Fatalf("soak: create report directory %s: %v", cfg.reports, err)
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	csvPath := filepath.Join(cfg.reports, "soak-"+string(cfg.store)+"-"+stamp+".csv")
	summaryPath := filepath.Join(cfg.reports, "soak-"+string(cfg.store)+"-"+stamp+".md")

	if err := soak.WriteReport(csvPath, summaryPath, samples, assessed); err != nil {
		t.Fatalf("soak: write reports: %v", err)
	}

	return [2]string{csvPath, summaryPath}
}
