package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Timing of the server processes and of the health checks.
const (
	procStartTimeout  = 90 * time.Second
	procStopTimeout   = 90 * time.Second
	procPollEvery     = 100 * time.Millisecond
	healthEvery       = 250 * time.Millisecond
	healthTimeout     = time.Second
	balancerRetryWait = "1"
	clockTicks        = 100
	idleConns         = 512
	idleConnsPerHost  = 256
)

// errProcExited reports a server process that ended before it was ready.
var errProcExited = errors.New("the server process ended before it was ready")

// errProcTimeout reports a server process that was not ready in time.
var errProcTimeout = errors.New("the server process was not ready in time")

// serverProc is one `opendustpan serve` process of the scale run. Its port
// stays the same over restarts, so the balancer needs no change.
type serverProc struct {
	index   int
	port    int
	bin     string
	args    []string
	env     []string
	logPath string

	healthy atomic.Bool

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	exitErr error
}

// procSpec says how to run one server process.
type procSpec struct {
	index int
	port  int
	bin   string
	args  []string
	env   []string
	dir   string
}

// newServerProc answers a process that is not started.
func newServerProc(spec procSpec) *serverProc {
	return &serverProc{
		index: spec.index, port: spec.port, bin: spec.bin, args: spec.args, env: spec.env,
		logPath: filepath.Join(spec.dir, "server-"+strconv.Itoa(spec.index)+".log"),
		healthy: atomic.Bool{}, mu: sync.Mutex{}, cmd: nil, done: nil, exitErr: nil,
	}
}

// url answers the address of the process.
func (p *serverProc) url() string {
	return "http://127.0.0.1:" + strconv.Itoa(p.port)
}

// pid answers the process ID, or 0 when the process does not run.
func (p *serverProc) pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}

	return p.cmd.Process.Pid
}

// start runs the process and waits until /health/ready answers 200. The
// process ends with stop, not with ctx.
func (p *serverProc) start(ctx context.Context) error {
	logFile, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open server log: %w", err)
	}

	//nolint:gosec // The binary path is a flag of the operator who runs the harness.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), p.bin, p.args...)
	cmd.Env = p.env
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = procStopTimeout

	if err := cmd.Start(); err != nil {
		closeQuietly(logFile)

		return fmt.Errorf("start server %d: %w", p.index, err)
	}

	done := make(chan struct{})

	go func() {
		// The exit error of a stopped process is the expected end.
		exitErr := cmd.Wait()

		p.mu.Lock()
		p.exitErr = exitErr
		p.mu.Unlock()

		closeQuietly(logFile)

		close(done)
	}()

	p.mu.Lock()
	p.cmd, p.done = cmd, done
	p.mu.Unlock()

	deadline := time.Now().Add(procStartTimeout)

	for time.Now().Before(deadline) {
		if probeReady(ctx, p.url()) {
			p.healthy.Store(true)

			return nil
		}

		select {
		case <-done:
			return fmt.Errorf("%w: server %d\n%s", errProcExited, p.index, p.logTail())
		case <-ctx.Done():
			return fmt.Errorf("wait for server %d: %w", p.index, ctx.Err())
		case <-time.After(procPollEvery):
		}
	}

	return fmt.Errorf("%w: server %d\n%s", errProcTimeout, p.index, p.logTail())
}

// stop sends SIGTERM and waits until the process ends. It answers the time
// the stop took. After procStopTimeout it kills the process.
func (p *serverProc) stop() (time.Duration, error) {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()

	if cmd == nil || done == nil {
		return 0, nil
	}

	began := time.Now()

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return 0, fmt.Errorf("signal server %d: %w", p.index, err)
	}

	select {
	case <-done:
	case <-time.After(procStopTimeout):
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return time.Since(began), fmt.Errorf("kill server %d: %w", p.index, err)
		}

		<-done
	}

	p.healthy.Store(false)

	p.mu.Lock()
	defer p.mu.Unlock()

	// SIGTERM ends a server with exit 0. Any other end is worth a line in the
	// log of the run.
	if p.exitErr != nil {
		return time.Since(began), fmt.Errorf("server %d exit: %w", p.index, p.exitErr)
	}

	return time.Since(began), nil
}

// logTail answers the last lines of the server log, for a start error.
func (p *serverProc) logTail() string {
	const tailBytes = 4096

	data, err := os.ReadFile(p.logPath)
	if err != nil {
		return ""
	}

	if len(data) > tailBytes {
		data = data[len(data)-tailBytes:]
	}

	return string(data)
}

// probeReady reports whether base answers 200 on /health/ready.
func probeReady(ctx context.Context, base string) bool {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/ready", http.NoBody)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}

	defer closeQuietly(resp.Body)

	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		return false
	}

	return resp.StatusCode == http.StatusOK
}

// freePort answers a loopback TCP port that nobody listens on.
func freePort(ctx context.Context) (int, error) {
	listener, err := listenLoopback(ctx)
	if err != nil {
		return 0, fmt.Errorf("find a free port: %w", err)
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		closeQuietly(listener)

		return 0, errNotTCP
	}

	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("close probe listener: %w", err)
	}

	return addr.Port, nil
}

// listenLoopback listens on a free loopback TCP port.
func listenLoopback(ctx context.Context) (net.Listener, error) {
	var config net.ListenConfig

	listener, err := config.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return listener, nil
}

// closeQuietly closes closer. The harness cannot act on the error of a
// close at the end of use, so it drops the error.
func closeQuietly(closer io.Closer) {
	if err := closer.Close(); err != nil {
		return
	}
}

// errNotTCP reports a listener address that is not TCP.
var errNotTCP = errors.New("listener address is not TCP")

// balancer is a round-robin load balancer with an active health check on
// /health/ready, like the balancer of a supported layout. It sends each
// request to the next healthy instance, and it answers 503 with
// Retry-After when no instance is healthy. It never retries a request.
type balancer struct {
	procs   []*serverProc
	proxies []*httputil.ReverseProxy
	next    atomic.Uint64
}

// newBalancer answers a balancer over procs.
func newBalancer(procs []*serverProc) (*balancer, error) {
	transport := newTransport()

	proxies := make([]*httputil.ReverseProxy, 0, len(procs))

	for _, proc := range procs {
		target, err := url.Parse(proc.url())
		if err != nil {
			return nil, fmt.Errorf("parse server URL: %w", err)
		}

		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = transport
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.WriteHeader(http.StatusBadGateway)
		}

		proxies = append(proxies, proxy)
	}

	return &balancer{procs: procs, proxies: proxies, next: atomic.Uint64{}}, nil
}

// ServeHTTP implements http.Handler.
func (b *balancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	index := b.pick()
	if index < 0 {
		w.Header().Set("Retry-After", balancerRetryWait)
		w.WriteHeader(http.StatusServiceUnavailable)

		return
	}

	b.proxies[index].ServeHTTP(w, r)
}

// pick answers the index of the next healthy instance, or -1. It rotates
// over the healthy instances only, so an instance that is out of the pool
// does not double the share of the next instance.
func (b *balancer) pick() int {
	healthy := make([]int, 0, len(b.procs))

	for index, proc := range b.procs {
		if proc.healthy.Load() {
			healthy = append(healthy, index)
		}
	}

	if len(healthy) == 0 {
		return -1
	}

	return healthy[b.next.Add(1)%uint64(len(healthy))]
}

// watch checks /health/ready of each instance until ctx ends. An instance
// that answers anything but 200 leaves the pool until it answers 200.
func (b *balancer) watch(ctx context.Context) {
	ticker := time.NewTicker(healthEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var wait sync.WaitGroup

			for _, proc := range b.procs {
				wait.Go(func() {
					// A process that is stopped or starting keeps the state
					// that start and stop set; only a running process is checked.
					if proc.pid() != 0 {
						proc.healthy.Store(probeReady(ctx, proc.url()))
					}
				})
			}

			wait.Wait()
		}
	}
}

// Process times from /proc, in seconds. They read the user and the system
// time of one process.

// procCPUSeconds answers the user and system CPU time of process pid.
func procCPUSeconds(pid int) (float64, error) {
	//nolint:gosec // The path is /proc/<number>/stat for a number that the harness parsed.
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, fmt.Errorf("read process stat: %w", err)
	}

	fields, err := statFields(data)
	if err != nil {
		return 0, err
	}

	const utimeAt, stimeAt = 11, 12

	user, userErr := strconv.ParseFloat(fields[utimeAt], 64)
	system, systemErr := strconv.ParseFloat(fields[stimeAt], 64)

	if userErr != nil || systemErr != nil {
		return 0, errBadStat
	}

	return (user + system) / clockTicks, nil
}

// errBadStat reports a /proc stat line that the harness cannot read.
var errBadStat = errors.New("cannot read the process stat line")

// statFields answers the fields of a /proc stat line that follow the
// command name. Index 0 is the state, so utime is index 11 and ppid index 1.
func statFields(data []byte) ([]string, error) {
	_, rest, found := bytes.CutLast(data, []byte{')'})
	if !found {
		return nil, errBadStat
	}

	fields := strings.Fields(string(rest))

	const minFields = 13

	if len(fields) < minFields {
		return nil, errBadStat
	}

	return fields, nil
}

// treeCPUSeconds answers the CPU time of process root and its children.
func treeCPUSeconds(root int) (float64, error) {
	total, err := procCPUSeconds(root)
	if err != nil {
		return 0, err
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("read /proc: %w", err)
	}

	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid == root {
			continue
		}

		data, readErr := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if readErr != nil {
			continue
		}

		fields, fieldsErr := statFields(data)
		if fieldsErr != nil {
			continue
		}

		if parent, parentErr := strconv.Atoi(fields[1]); parentErr != nil || parent != root {
			continue
		}

		if seconds, cpuErr := procCPUSeconds(pid); cpuErr == nil {
			total += seconds
		}
	}

	return total, nil
}

// selfCPUSeconds answers the CPU time of the harness process.
func selfCPUSeconds() float64 {
	var usage syscall.Rusage

	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}

	return float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6 +
		float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6
}

// newTransport answers an HTTP transport that keeps many idle connections,
// so a run with many clients reuses its connections.
func newTransport() *http.Transport {
	return &http.Transport{MaxIdleConns: idleConns, MaxIdleConnsPerHost: idleConnsPerHost, IdleConnTimeout: time.Minute}
}
