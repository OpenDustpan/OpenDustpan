package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// flowsScale is the -flows value of the scale run.
const flowsScale = "scale"

// scaleFlags are the flags of the scale run.
type scaleFlags struct {
	binary      string
	instances   int
	clients     int
	duration    time.Duration
	warmup      time.Duration
	think       time.Duration
	restart     bool
	restartTail time.Duration
	storePID    int
}

// Defaults of the scale run.
const (
	defaultScaleClients  = 16
	defaultScaleDuration = 60 * time.Second
	defaultScaleWarmup   = 10 * time.Second
	defaultScaleTail     = 20 * time.Second
	scaleStateBytes      = 8192
	scaleCheckpointBytes = 16384
	unlockTries          = 5
	scaleHTTPTimeout     = 30 * time.Second
	sampleEvery          = 500 * time.Millisecond
	keyBytes             = 32
	kibPerMiB            = 1024
	perThousand          = 1000
	scaleInstallation    = loadProject
)

// Errors of the scale run.
var (
	errScaleNeedsPostgres = errors.New("-flows scale needs DUSTPAN_TEST_POSTGRES_URL")
	errScaleNeedsProc     = errors.New("-flows scale needs /proc (Linux)")
	errScaleBadFlags      = errors.New("-binary, -instances, -clients and -duration must be set")
	errScaleNamespace     = errors.New("create the namespace: unexpected status")
)

// ScaleReport is the output of the scale run. Times use milliseconds, CPU
// uses cores averaged over the steady window.
type ScaleReport struct {
	Time      string        `json:"time"`
	Machine   MachineInfo   `json:"machine"`
	Instances int           `json:"instances"`
	Clients   int           `json:"clients"`
	ThinkMs   float64       `json:"thinkMs"`
	Units     int           `json:"units"`
	Steady    PhaseStats    `json:"steady"`
	Resources ResourceStats `json:"resources"`
	Restart   *RestartStats `json:"restart,omitempty"`
	// ClientErrors counts the client calls that failed, over the whole run.
	ClientErrors int64 `json:"clientErrors"`
}

// MachineInfo describes the host of the run.
type MachineInfo struct {
	CPUs     int    `json:"cpus"`
	CPUModel string `json:"cpuModel"`
	MemoryMB int    `json:"memoryMb"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Go       string `json:"go"`
}

// ResourceStats holds what the run used, averaged over the steady window.
type ResourceStats struct {
	// InstanceCores is the CPU of all server processes together.
	InstanceCores *float64 `json:"instanceCores,omitempty"`
	// PostgresCores is the CPU of the postmaster and its children. It is
	// absent when the harness could not find the postmaster or could not
	// read its CPU.
	PostgresCores *float64 `json:"postgresCores,omitempty"`
	// StoreCores is the CPU of the object store process. It is absent
	// when -store-pid is not set or the harness could not read its CPU.
	StoreCores *float64 `json:"storeCores,omitempty"`
	// HarnessCores is the CPU of clients, balancer and sampler.
	HarnessCores *float64 `json:"harnessCores,omitempty"`
	// DBConnectionsMean and DBConnectionsMax count client connections.
	// They are absent when the window held no sample.
	DBConnectionsMean *float64 `json:"dbConnectionsMean,omitempty"`
	DBConnectionsMax  *int     `json:"dbConnectionsMax,omitempty"`
	// DBTransactionsPerSec counts commits and rollbacks of the database. It
	// is absent when a read of the count failed.
	DBTransactionsPerSec *float64 `json:"dbTransactionsPerSec,omitempty"`
	// InstanceCoreMsPer1k and PostgresCoreMsPer1k are the CPU cost of 1000
	// requests, in core-milliseconds.
	InstanceCoreMsPer1k *float64 `json:"instanceCoreMsPer1kRequests,omitempty"`
	PostgresCoreMsPer1k *float64 `json:"postgresCoreMsPer1kRequests,omitempty"`
	// Unmeasured names the fields that nobody measured in this run. A field
	// that is absent is always in this list.
	Unmeasured []string `json:"unmeasured,omitempty"`
}

// RestartStats holds the restart of one instance while the clients run.
type RestartStats struct {
	Instance int     `json:"instance"`
	StopMs   float64 `json:"stopMs"`
	StartMs  float64 `json:"startMs"`
	// StopError and StartError are set when the stop or the start failed.
	// The report is then not the report of a restart.
	StopError  string `json:"stopError,omitempty"`
	StartError string `json:"startError,omitempty"`
	// During covers the requests that began from the SIGTERM until the
	// instance was ready again. After covers the tail that follows.
	During PhaseStats `json:"during"`
	After  PhaseStats `json:"after"`
}

// scaleRun holds the running parts of one scale run.
type scaleRun struct {
	flags     scaleFlags
	procs     []*serverProc
	rec       *recorder
	target    target
	clients   []*scaleClient
	server    *http.Server
	lb        *balancer
	transport *http.Transport
	stop      atomic.Bool
	// background holds the goroutines that shutdown waits for: the health
	// check and the balancer server. watchStop ends the health check.
	background sync.WaitGroup
	watchStop  context.CancelFunc
	// failed counts the calls of a client that did not end well. The
	// samples hold the failed requests; this count also holds the calls
	// that failed before or after a request, such as a body that did not decode.
	failed atomic.Int64
}

// scaleClient is one simulated user. Each client has its own workspace, and
// makes a new stack for each update, so no client waits for a lock of
// another client or for a stuck update of its own after an error.
type scaleClient struct {
	id        int
	workspace string
	serial    int64
}

// executeScale runs the scale run: it starts the server processes, drives the
// recorded traffic through a balancer, and answers the report.
func executeScale(ctx context.Context, logger *slog.Logger, f harnessFlags, dir string) (ScaleReport, error) {
	sf := f.scale
	if sf.binary == "" || sf.instances < 1 || sf.clients < 1 || sf.duration <= 0 {
		return ScaleReport{}, errScaleBadFlags
	}

	// The mode reads the CPU time from /proc. Without it every CPU figure
	// would be missing, so the run stops before it starts a process.
	if _, err := procCPUSeconds(os.Getpid()); err != nil {
		return ScaleReport{}, fmt.Errorf("%w: %w", errScaleNeedsProc, err)
	}

	pgURL := os.Getenv(postgresURLEnv)
	if pgURL == "" {
		return ScaleReport{}, errScaleNeedsPostgres
	}

	signer, err := s3Signer()
	if err != nil {
		return ScaleReport{}, err
	}

	procs, err := startProcs(
		ctx,
		sf,
		procStore{pgURL: pgURL, endpoint: signer.Endpoint, bucket: signer.Bucket, dir: dir},
	)

	run := newScaleRun(f, procs)
	defer run.shutdown(ctx, logger)

	if err != nil {
		return ScaleReport{}, err
	}

	report, err := run.prepare(ctx, logger, f)
	if err != nil {
		return ScaleReport{}, err
	}

	return run.measure(ctx, logger, report, pgURL)
}

// newScaleRun answers a run over procs that nothing serves yet.
func newScaleRun(f harnessFlags, procs []*serverProc) *scaleRun {
	return &scaleRun{
		flags: f.scale, procs: procs, rec: nil, target: newTarget(nil, harnessToken(), f.ns),
		clients: nil, server: nil, lb: nil, transport: nil, stop: atomic.Bool{},
		background: sync.WaitGroup{}, watchStop: nil, failed: atomic.Int64{},
	}
}

// shutdown ends everything the run started, in the order that leaves no
// goroutine and no idle connection: the health check and the balancer
// server first, then the idle connections, then the server processes.
func (r *scaleRun) shutdown(ctx context.Context, logger *slog.Logger) {
	r.stop.Store(true)

	if r.watchStop != nil {
		r.watchStop()
	}

	if r.server != nil {
		if err := r.server.Close(); err != nil {
			logger.ErrorContext(ctx, "close balancer", slog.String("error", err.Error()))
		}
	}

	r.background.Wait()

	if r.lb != nil {
		r.lb.transport.CloseIdleConnections()
	}

	if r.transport != nil {
		r.transport.CloseIdleConnections()
	}

	http.DefaultClient.CloseIdleConnections()
	stopProcs(ctx, logger, r.procs)
}

// procStore names what all server processes share: the database, the
// bucket and the scratch directory of their logs.
type procStore struct {
	pgURL    string
	endpoint string
	bucket   string
	dir      string
}

// startProcs starts sf.instances server processes on one database and one
// bucket prefix. The first process migrates the database alone.
func startProcs(ctx context.Context, sf scaleFlags, shared procStore) ([]*serverProc, error) {
	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("make a key: %w", err)
	}

	prefix := make([]byte, s3RunBytes)
	if _, err := rand.Read(prefix); err != nil {
		return nil, fmt.Errorf("make a prefix: %w", err)
	}

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"DUSTPAN_ADMIN_TOKEN=" + harnessToken(),
		"DUSTPAN_DATABASE=" + shared.pgURL,
		"DUSTPAN_KEY_ENCRYPTION_KEY=" + base64.StdEncoding.EncodeToString(key),
		"AWS_ACCESS_KEY_ID=" + os.Getenv(s3AccessEnv),
		"AWS_SECRET_ACCESS_KEY=" + os.Getenv("DUSTPAN_TEST_S3_SECRET_KEY"),
	}

	store := "s3://" + shared.bucket + "/scale-" + hex.EncodeToString(prefix)
	args := []string{
		"serve", "--installation", scaleInstallation, "--blob-store", store, "--s3-endpoint", shared.endpoint,
	}

	procs := make([]*serverProc, 0, sf.instances)

	for index := range sf.instances {
		procs = append(procs, newServerProc(procSpec{
			index: index, port: 0, bin: sf.binary, args: args, env: env, dir: shared.dir,
			startTimeout: procStartTimeout, stopTimeout: procStopTimeout,
		}))
	}

	if err := procs[0].start(ctx); err != nil {
		return procs, err
	}

	var (
		wait     sync.WaitGroup
		mu       sync.Mutex
		startErr error
	)

	for _, proc := range procs[1:] {
		wait.Go(func() {
			if err := proc.start(ctx); err != nil {
				mu.Lock()
				startErr = errors.Join(startErr, err)
				mu.Unlock()
			}
		})
	}

	wait.Wait()

	return procs, startErr
}

// stopProcs stops every process at the same time, so the drain of all
// instances takes the time of one.
func stopProcs(ctx context.Context, logger *slog.Logger, procs []*serverProc) {
	var wait sync.WaitGroup

	for _, proc := range procs {
		wait.Go(func() {
			if _, err := proc.stop(); err != nil {
				logger.ErrorContext(
					ctx,
					"stop server",
					slog.Int("instance", proc.index),
					slog.String("error", err.Error()),
				)
			}
		})
	}

	wait.Wait()
}

// prepare serves the balancer, makes the namespace, seeds units and makes
// the clients. It answers a report that holds the settings of the run.
func (r *scaleRun) prepare(ctx context.Context, logger *slog.Logger, f harnessFlags) (ScaleReport, error) {
	var empty ScaleReport

	lb, err := newBalancer(r.procs)
	if err != nil {
		return empty, err
	}

	r.lb = lb

	listener, err := listenLoopback(ctx)
	if err != nil {
		return empty, err
	}

	r.server = &http.Server{Handler: lb, ReadHeaderTimeout: scaleHTTPTimeout}

	r.background.Go(func() {
		if serveErr := r.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "balancer", slog.String("error", serveErr.Error()))
		}
	})

	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.watchStop = cancel

	r.background.Go(func() { lb.watch(watchCtx) })

	base := "http://" + listener.Addr().String()
	r.transport = newTransport()

	r.target = newTarget([]string{base}, harnessToken(), f.ns)
	r.target.client = &http.Client{Transport: r.transport, Timeout: scaleHTTPTimeout}

	if err := r.makeNamespace(ctx, base, f.ns); err != nil {
		return empty, err
	}

	logger.InfoContext(ctx, "seed units", slog.Int("units", f.units))

	if err := r.target.seedUnits(ctx, f.units, defaultSeedWorkers); err != nil {
		return empty, err
	}

	r.rec = newRecorder(r.transport)
	r.target.client = &http.Client{Transport: r.rec, Timeout: scaleHTTPTimeout}

	if err := r.makeClients(ctx); err != nil {
		return empty, err
	}

	var (
		steady    PhaseStats
		resources ResourceStats
	)

	return ScaleReport{
		Time: time.Now().UTC().Format(time.RFC3339), Machine: machineInfo(), Instances: len(r.procs),
		Clients: f.scale.clients, ThinkMs: ms(f.scale.think), Units: f.units,
		Steady: steady, Resources: resources, Restart: nil, ClientErrors: 0,
	}, nil
}

// makeClients makes the workspace of each client.
func (r *scaleRun) makeClients(ctx context.Context) error {
	for id := range r.flags.clients {
		workspaceID, err := r.target.createWorkspace(ctx, "scale-ws-"+padNumber(id))
		if err != nil {
			return err
		}

		r.clients = append(r.clients, &scaleClient{id: id, workspace: workspaceID, serial: 0})
	}

	return nil
}

// makeNamespace creates the namespace of the run through the API.
func (r *scaleRun) makeNamespace(ctx context.Context, base, ns string) error {
	resp, err := r.target.do(ctx, request{
		method: http.MethodPost, url: base + "/api/server/namespaces", auth: tfeScheme + r.target.token,
		mediaType: jsonMediaType, body: []byte(`{"name":` + strconv.Quote(ns) + `}`),
	})
	if err != nil {
		return err
	}

	if resp.status != http.StatusCreated {
		return fmt.Errorf("%w %d: %s", errScaleNamespace, resp.status, string(resp.body))
	}

	return nil
}

// measure runs the warm-up, the steady window and the restart, and fills
// the report.
func (r *scaleRun) measure(
	ctx context.Context,
	logger *slog.Logger,
	report ScaleReport,
	pgURL string,
) (ScaleReport, error) {
	var wait sync.WaitGroup

	for _, one := range r.clients {
		wait.Go(func() { r.loop(ctx, one) })
	}

	defer func() {
		r.stop.Store(true)
		wait.Wait()
	}()

	logger.InfoContext(ctx, "warm up", slog.Duration("for", r.flags.warmup))
	sleep(ctx, r.flags.warmup)

	monitor, err := newMonitor(ctx, pgURL, r.procs, r.flags.storePID)
	if err != nil {
		return report, err
	}

	defer func() {
		if closeErr := monitor.close(ctx); closeErr != nil {
			logger.ErrorContext(ctx, "close sampler", slog.String("error", closeErr.Error()))
		}
	}()

	if monitor.pgRoot == 0 {
		logger.WarnContext(ctx, "the postmaster file is not readable: the PostgreSQL CPU is not measured")
	}

	if r.flags.storePID == 0 {
		logger.WarnContext(ctx, "no -store-pid: the object store CPU is not measured")
	}

	from := r.rec.since()

	monitor.begin(ctx)
	logger.InfoContext(ctx, "steady window", slog.Duration("for", r.flags.duration))
	sleep(ctx, r.flags.duration)

	to := r.rec.since()
	steady := r.rec.window(from, to)
	report.Steady = summarize(steady, to-from)
	report.Resources = monitor.end(ctx, to-from, len(steady))

	if r.flags.restart {
		report.Restart = r.restartOne(ctx, logger)
	}

	report.ClientErrors = r.failed.Load()

	return report, nil
}

// restartOne stops instance 0 with SIGTERM, starts it again, and answers the
// statistics of the requests around it. The clients keep running. A stop or
// a start that failed shows in StopError and StartError, and the times then
// are not the times of a restart.
func (r *scaleRun) restartOne(ctx context.Context, logger *slog.Logger) *RestartStats {
	proc := r.procs[0]
	began := r.rec.since()

	logger.InfoContext(ctx, "restart instance", slog.Int("instance", proc.index))

	stopTook, stopErr := proc.stop()
	startBegan := time.Now()

	var startErr error

	if restartMayStart(stopErr, proc.pid() != 0) {
		startErr = proc.start(ctx)
	} else {
		startErr = errRestartSkipped
	}

	startTook := time.Since(startBegan)
	ready := r.rec.since()

	sleep(ctx, r.flags.restartTail)

	end := r.rec.since()

	out := &RestartStats{
		Instance: proc.index, StopMs: ms(stopTook), StartMs: ms(startTook), StopError: "", StartError: "",
		During: summarize(r.rec.window(began, ready), ready-began),
		After:  summarize(r.rec.window(ready, end), end-ready),
	}

	if stopErr != nil {
		out.StopError = stopErr.Error()

		logger.ErrorContext(ctx, "restart: stop", slog.String("error", stopErr.Error()))
	}

	if startErr != nil {
		out.StartError = startErr.Error()

		logger.ErrorContext(ctx, "restart: start", slog.String("error", startErr.Error()))
	}

	return out
}

// errRestartSkipped is the start error of a restart that did not start the
// new process, because the old process may still run.
var errRestartSkipped = errors.New("the new process was not started because the stop failed")

// restartMayStart says whether a restart may start the new process. After a
// stop error the old process can still hold the port, and a start then fails
// on the bind with a second error for one cause. A process that was killed,
// or that ended with an error, is gone, so the start goes on.
func restartMayStart(stopErr error, stillRunning bool) bool {
	return stopErr == nil || errors.Is(stopErr, errKilled) || !stillRunning
}

// sleep waits for d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// loop runs the traffic of one client until the run stops.
func (r *scaleRun) loop(ctx context.Context, one *scaleClient) {
	deployment := versionedEnvelope(buildDeployment(scaleCheckpointBytes))

	for i := 0; !r.stop.Load() && ctx.Err() == nil; i++ {
		stack := "scale-" + strconv.Itoa(one.id) + "-" + strconv.Itoa(i)

		// A failed call shows in the sample of its request. The client goes
		// on with the next step, as a CLI retry or the next run would.
		if err := r.target.createStack(ctx, stack); err != nil {
			r.failed.Add(1)
		} else if _, saveErr := r.target.saveOneCheckpoint(ctx, stack, deployment); saveErr != nil {
			r.failed.Add(1)
		}

		r.tofuCycle(ctx, one)
		r.reads(ctx)

		if r.flags.think > 0 {
			sleep(ctx, r.flags.think)
		}
	}
}

// tofuCycle replays one OpenTofu apply on the workspace of the client:
// lock, state upload, unlock. A lock request that failed or was refused may
// leave a lock that the client holds, for example when the answer was lost
// at a restart. The client then releases the workspace, so one error does
// not fail the later cycles of the client.
func (r *scaleRun) tofuCycle(ctx context.Context, one *scaleClient) {
	status, err := r.target.lockWorkspace(ctx, one.workspace)
	if err != nil || status != http.StatusOK {
		r.failed.Add(1)
		r.release(ctx, one)

		return
	}

	one.serial++

	state := buildState(scaleStateBytes, one.serial, "load-"+one.workspace)
	if uploadErr := r.target.uploadState(ctx, one.workspace, state, one.serial); uploadErr != nil {
		r.failed.Add(1)
	}

	r.release(ctx, one)
}

// release unlocks the workspace of the client. It tries again while the
// request fails to get an answer. Any answer of the server ends it,
// also 409 for a workspace that has no lock.
func (r *scaleRun) release(ctx context.Context, one *scaleClient) {
	for range unlockTries {
		resp, err := r.target.do(ctx, request{
			method: http.MethodPost, url: r.target.firstURL(tfeByID + one.workspace + tfeUnlock),
			auth: tfeScheme + r.target.token, mediaType: jsonMediaType, body: []byte(emptyJSONBody),
		})
		if err == nil {
			if resp.status != http.StatusOK && resp.status != http.StatusConflict {
				r.failed.Add(1)
			}

			return
		}

		r.failed.Add(1)
		sleep(ctx, time.Second)
	}
}

// reads replays the two hot list reads: one page of workspaces and the
// inventory.
func (r *scaleRun) reads(ctx context.Context) {
	page := tfeWorkspaces + r.target.ns + tfeWorkspaceMid + "?page[size]=" + strconv.Itoa(defaultTFEPageSize)

	_, err := r.target.do(ctx, request{
		method: http.MethodGet, url: r.target.firstURL(page),
		auth: tfeScheme + r.target.token, mediaType: "", body: nil,
	})
	if err != nil {
		r.failed.Add(1)
	}

	if _, _, inventoryErr := r.target.measureInventory(ctx); inventoryErr != nil {
		r.failed.Add(1)
	}
}

// monitor samples the database and reads the CPU time of the processes.
type monitor struct {
	conn     *pgx.Conn
	procs    []*serverProc
	storePID int
	pgRoot   int

	startedAt time.Time
	cpu       cpuMark
	txBegin   int64
	txBeginOK bool

	mu      sync.Mutex
	counts  []int
	cancel  context.CancelFunc
	sampled sync.WaitGroup
}

// cpuMark is the CPU time of the groups at one moment. Each group has a flag
// that says the read worked. A group whose read failed has no time.
type cpuMark struct {
	instances, postgres, store, harness         float64
	instancesOK, postgresOK, storeOK, harnessOK bool
}

// blankMonitor answers a monitor that has not begun a window.
func blankMonitor(conn *pgx.Conn, procs []*serverProc, storePID int) *monitor {
	return &monitor{
		conn: conn, procs: procs, storePID: storePID, pgRoot: 0, startedAt: time.Time{},
		cpu: cpuMark{
			instances: 0, postgres: 0, store: 0, harness: 0,
			instancesOK: false, postgresOK: false, storeOK: false, harnessOK: false,
		},
		txBegin: 0, txBeginOK: false, mu: sync.Mutex{}, counts: nil, cancel: nil, sampled: sync.WaitGroup{},
	}
}

// newMonitor connects to the database and finds the postmaster.
func newMonitor(ctx context.Context, pgURL string, procs []*serverProc, storePID int) (*monitor, error) {
	// The pool settings in the URL (pool_max_conns and others) belong to the
	// pool of the server, so the sampler reads the connection part only.
	poolConfig, err := pgxpool.ParseConfig(pgURL)
	if err != nil {
		return nil, fmt.Errorf("parse the database URL: %w", err)
	}

	conn, err := pgx.ConnectConfig(ctx, poolConfig.ConnConfig)
	if err != nil {
		return nil, fmt.Errorf("connect the sampler: %w", err)
	}

	mon := blankMonitor(conn, procs, storePID)
	mon.pgRoot = postmasterPID(ctx, conn)

	return mon, nil
}

// postmasterPID answers the PID of the postmaster, or 0 when unknown.
func postmasterPID(ctx context.Context, conn *pgx.Conn) int {
	var dataDir string

	if err := conn.QueryRow(ctx, "SHOW data_directory").Scan(&dataDir); err != nil {
		return 0
	}

	//nolint:gosec // The path is the data directory that the database reports, as the harness user.
	data, err := os.ReadFile(dataDir + "/postmaster.pid")
	if err != nil {
		return 0
	}

	first, _, _ := strings.Cut(string(data), "\n")

	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return 0
	}

	return pid
}

// marks answers the CPU time of the groups now. A read that failed leaves
// the flag of its group false: the group is then not measured.
func (m *monitor) marks() cpuMark {
	var out cpuMark

	out.instancesOK = len(m.procs) > 0

	for _, proc := range m.procs {
		pid := proc.pid()
		if pid == 0 {
			out.instancesOK = false

			continue
		}

		seconds, err := procCPUSeconds(pid)
		if err != nil {
			out.instancesOK = false

			continue
		}

		out.instances += seconds
	}

	if m.pgRoot != 0 {
		out.postgres, out.postgresOK = okSeconds(treeCPUSeconds(m.pgRoot))
	}

	if m.storePID != 0 {
		out.store, out.storeOK = okSeconds(procCPUSeconds(m.storePID))
	}

	out.harness, out.harnessOK = okSeconds(selfCPUSeconds())

	return out
}

// okSeconds answers seconds with true when err is nil.
func okSeconds(seconds float64, err error) (float64, bool) {
	return seconds, err == nil
}

// transactions answers the commits and rollbacks of the database so far.
func (m *monitor) transactions(ctx context.Context) (int64, bool) {
	var total int64

	err := m.conn.QueryRow(ctx,
		"SELECT xact_commit + xact_rollback FROM pg_stat_database WHERE datname = current_database()").Scan(&total)
	if err != nil {
		return 0, false
	}

	return total, true
}

// begin starts the samples.
func (m *monitor) begin(ctx context.Context) {
	m.startedAt = time.Now()
	m.cpu = m.marks()
	m.txBegin, m.txBeginOK = m.transactions(ctx)

	sampleCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel

	m.sampled.Go(func() {
		ticker := time.NewTicker(sampleEvery)
		defer ticker.Stop()

		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
				var count int

				err := m.conn.QueryRow(sampleCtx, `SELECT count(*) FROM pg_stat_activity
					WHERE datname = current_database() AND backend_type = 'client backend'
					AND pid <> pg_backend_pid()`).Scan(&count)
				if err == nil {
					m.mu.Lock()
					m.counts = append(m.counts, count)
					m.mu.Unlock()
				}
			}
		}
	})
}

// end stops the samples and answers the resources of the window.
func (m *monitor) end(ctx context.Context, window time.Duration, requests int) ResourceStats {
	// The sampler and the transaction read share one connection, and a
	// connection serves one query at a time. Stop the sampler first.
	m.cancel()
	m.sampled.Wait()

	now := m.marks()
	tx, txOK := m.transactions(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	return resourcesOf(resourceInput{
		begin: m.cpu, end: now, txBegin: m.txBegin, txEnd: tx, txOK: m.txBeginOK && txOK,
		window: window, requests: requests, counts: m.counts,
	})
}

// resourceInput is what resourcesOf turns into the resources of a window.
type resourceInput struct {
	begin, end cpuMark
	txBegin    int64
	txEnd      int64
	// txOK says that both reads of the transaction count worked.
	txOK     bool
	window   time.Duration
	requests int
	// counts are the samples of the client connections.
	counts []int
}

// resourcesOf answers the resources of one window. A part that nobody
// measured, or whose read failed at the begin or the end of the window, has
// no value, not the value 0, and the report names it in Unmeasured.
func resourcesOf(in resourceInput) ResourceStats {
	var out ResourceStats

	seconds := in.window.Seconds()
	note := func(name string) { out.Unmeasured = append(out.Unmeasured, name) }
	cores := func(name string, ok bool, from, to float64) *float64 {
		if !ok || seconds <= 0 {
			note(name)

			return nil
		}

		value := (to - from) / seconds

		return &value
	}

	out.InstanceCores = cores("instanceCores", in.begin.instancesOK && in.end.instancesOK,
		in.begin.instances, in.end.instances)
	out.PostgresCores = cores("postgresCores", in.begin.postgresOK && in.end.postgresOK,
		in.begin.postgres, in.end.postgres)
	out.StoreCores = cores("storeCores", in.begin.storeOK && in.end.storeOK, in.begin.store, in.end.store)
	out.HarnessCores = cores("harnessCores", in.begin.harnessOK && in.end.harnessOK,
		in.begin.harness, in.end.harness)

	out.DBTransactionsPerSec = transactionRate(in, seconds)
	out.DBConnectionsMean, out.DBConnectionsMax = connectionStats(in.counts)
	out.InstanceCoreMsPer1k = costPer1k(in.requests, out.InstanceCores, seconds)
	out.PostgresCoreMsPer1k = costPer1k(in.requests, out.PostgresCores, seconds)

	for name, missing := range map[string]bool{
		"dbTransactionsPerSec":        out.DBTransactionsPerSec == nil,
		"dbConnections":               out.DBConnectionsMean == nil,
		"instanceCoreMsPer1kRequests": out.InstanceCoreMsPer1k == nil,
		"postgresCoreMsPer1kRequests": out.PostgresCoreMsPer1k == nil,
	} {
		if missing {
			note(name)
		}
	}

	slices.Sort(out.Unmeasured)

	return out
}

// transactionRate answers the transactions each second, or nil when a read
// of the count failed or the count went down.
func transactionRate(in resourceInput, seconds float64) *float64 {
	if !in.txOK || in.txEnd < in.txBegin || seconds <= 0 {
		return nil
	}

	rate := float64(in.txEnd-in.txBegin) / seconds

	return &rate
}

// connectionStats answers the mean and the maximum of the samples, or nil
// without a sample.
func connectionStats(counts []int) (*float64, *int) {
	if len(counts) == 0 {
		return nil, nil
	}

	sum, peak := 0, 0

	for _, count := range counts {
		sum += count
		peak = max(peak, count)
	}

	mean := float64(sum) / float64(len(counts))

	return &mean, &peak
}

// costPer1k answers the core-milliseconds for 1000 requests of a part that
// used cores over seconds, or nil when the CPU or the requests are missing.
func costPer1k(requests int, cores *float64, seconds float64) *float64 {
	if cores == nil || requests <= 0 {
		return nil
	}

	cost := *cores * seconds * perThousand * perThousand / float64(requests)

	return &cost
}

// close ends the database connection.
func (m *monitor) close(ctx context.Context) error {
	if err := m.conn.Close(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("close the sampler connection: %w", err)
	}

	return nil
}

// machineInfo describes the host.
func machineInfo() MachineInfo {
	return MachineInfo{
		CPUs: runtime.NumCPU(), CPUModel: cpuModel(), MemoryMB: memoryMB(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version(),
	}
}

// cpuModel answers the model name of the first CPU, or "unknown".
func cpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if name, found := strings.CutPrefix(line, "model name"); found {
			_, value, _ := strings.Cut(name, ":")

			return strings.TrimSpace(value)
		}
	}

	return "unknown"
}

// memoryMB answers the memory of the host in MiB, or 0.
func memoryMB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, found := strings.CutPrefix(line, "MemTotal:"); found {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				kib, convErr := strconv.Atoi(fields[0])
				if convErr != nil {
					return 0
				}

				return kib / kibPerMiB
			}
		}
	}

	return 0
}
