//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	// instancesEnv names the variable that sets how many server binaries of
	// one installation the suite starts. Empty means one.
	instancesEnv = "E2E_INSTANCES"
	// maxInstances bounds E2E_INSTANCES, so a typo does not start a crowd of
	// processes.
	maxInstances = 9
	// healthPath is the readiness probe that the proxy checks.
	healthPath = "/health/ready"
	// healthEvery sets how often the proxy checks the probe of each instance.
	healthEvery = 250 * time.Millisecond
	// healthTimeout bounds one probe request.
	healthTimeout = 2 * time.Second
	// instanceStopTimeout bounds the wait for a stopped instance to exit.
	instanceStopTimeout = 2 * time.Minute
	// instanceKillTimeout bounds the wait for a killed instance to exit.
	instanceKillTimeout = time.Minute
	// maxBufferedBody bounds the request body that the proxy keeps, so that it
	// can send the request to another instance after a failure.
	maxBufferedBody = 512 << 20
	// instanceHost is the placeholder host that the proxy puts in a request
	// until it has chosen an instance.
	instanceHost = "instance.invalid"
)

// ErrInstances reports an E2E_INSTANCES value that is not a whole number
// from 1 to maxInstances, or a store that cannot hold several instances.
var ErrInstances = errors.New("invalid E2E_INSTANCES")

// Errors of the HA cluster.
var (
	// errNoHealthyInstance is the answer of the proxy when no instance is ready.
	errNoHealthyInstance = errors.New("no healthy instance")
	// errInstanceDown reports a call that needs a running instance.
	errInstanceDown = errors.New("the instance is not running")
	// errInstanceUp reports a start of an instance that runs already.
	errInstanceUp = errors.New("the instance runs already")
	// errInstanceExit reports an instance that did not exit in time.
	errInstanceExit = errors.New("the instance did not exit in time")
	// errProcessExited reports a process that ended while the suite waited for it.
	errProcessExited = errors.New("the process exited")
	// errWaitTimeout reports a wait that ran out of time.
	errWaitTimeout = errors.New("timed out")
	// errBadCertificate reports a certificate file with no certificate.
	errBadCertificate = errors.New("no certificate in the file")
	// errBelowMinimum reports an instance that refused a request because its
	// binary is below the minimum binary version of the schema.
	errBelowMinimum = errors.New("the instance is below the minimum binary version")
	// errBodyTooLarge reports a request body that the proxy cannot keep.
	errBodyTooLarge = errors.New("the request body is too large for the proxy")
)

// Instances returns the number of server binaries of one installation that
// each environment starts: E2E_INSTANCES, or 1 when it is unset. More than
// one needs the server binary and PostgreSQL.
func Instances() (int, error) {
	raw := strings.TrimSpace(os.Getenv(instancesEnv))
	if raw == "" {
		return 1, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxInstances {
		return 0, fmt.Errorf("%w: %q (want a whole number from 1 to %d)", ErrInstances, raw, maxInstances)
	}

	return n, nil
}

// instanceCount returns Instances, or 1 for a value that is not valid. The
// run reports the error from Databases, so a typo still stops the suite.
func instanceCount() int {
	n, err := Instances()
	if err != nil {
		return 1
	}

	return n
}

// instanceIDPattern finds the instance ID that the server logs at start, in
// the JSON and in the text log format.
var instanceIDPattern = regexp.MustCompile(`instance_id"?[=:]\s*"?([A-Za-z0-9_-]+)`)

// haInstance is one server binary of an HA cluster. The port stays the same
// over restarts, so the proxy target and the saved CLI logins stay valid.
type haInstance struct {
	port    string
	url     string
	healthy atomic.Bool
	// transport sends the requests of the proxy to this instance. Its idle
	// connections close when the instance fails.
	transport *http.Transport

	mu sync.Mutex
	// cmd is the running process, or nil while the instance is down.
	cmd *exec.Cmd
	// done closes when the process that cmd names has exited.
	done chan struct{}
	// logs holds the output of each process that ran as this instance, oldest
	// first.
	logs []*lockedBuffer
	// exits holds the exit error of each process that ended, oldest first.
	exits []error
}

// HACluster is the server binaries of one installation behind the
// round-robin proxy of the suite. The binaries share one database, one blob
// store and one set of settings.
type HACluster struct {
	cfg       backendConfig
	instances []*haInstance
	next      atomic.Int64
	stop      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// registerHACluster returns a cluster of n instances of cfg, not yet started,
// and registers its cleanup: the end of the processes and the log of each.
func registerHACluster(tb testing.TB, cfg backendConfig, n int) *HACluster {
	tb.Helper()

	c, err := newHACluster(cfg, n)
	if err != nil {
		tb.Fatalf("create the HA cluster: %v", err)
	}

	tb.Cleanup(func() {
		if err := c.close(); err != nil {
			tb.Logf("end the HA cluster: %v", err)
		}

		for i := range c.instances {
			tb.Logf("instance %d output:\n%s", i, c.Logs(i))
		}
	})

	return c
}

// newHACluster returns a cluster of n instances of cfg, not yet started. The
// proxy trusts the certificate of the instances, which all share one file.
func newHACluster(cfg backendConfig, n int) (*HACluster, error) {
	certPEM, err := os.ReadFile(cfg.certFile)
	if err != nil {
		return nil, fmt.Errorf("read the instance certificate: %w", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		return nil, fmt.Errorf("%w: %s", errBadCertificate, cfg.certFile)
	}

	c := &HACluster{
		cfg:       cfg,
		instances: make([]*haInstance, 0, n),
		next:      atomic.Int64{},
		stop:      make(chan struct{}),
		wg:        sync.WaitGroup{},
		closeOnce: sync.Once{},
	}

	for range n {
		inst := &haInstance{
			port:      "0",
			url:       "",
			healthy:   atomic.Bool{},
			transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}},
			mu:        sync.Mutex{},
			cmd:       nil,
			done:      nil,
			logs:      nil,
			exits:     nil,
		}
		c.instances = append(c.instances, inst)
	}

	return c, nil
}

// reserveHAProxy binds the TLS listener of the proxy in front of cluster. It
// has the recording, the version cap and the fault of every other recording
// proxy. The caller starts it once the instances run.
func reserveHAProxy(
	tb testing.TB, rec *Recorder, refuser *JournalRefuser, versionCap *JournalVersionCap, cluster *HACluster,
) *httptest.Server {
	tb.Helper()

	handler := rec.Middleware(cappedHandler(faultedHandler(cluster.handler(), refuser), versionCap))

	ts := httptest.NewUnstartedServer(handler)
	tb.Cleanup(ts.Close)

	return ts
}

// HA returns the cluster behind the proxy of the environment. It fails the
// test when the environment runs one server (E2E_INSTANCES is unset or 1).
func (e *Env) HA(tb testing.TB) *HACluster {
	tb.Helper()

	if e.ha == nil {
		tb.Fatalf("HA needs E2E_INSTANCES above 1 (make e2e-ha)")
	}

	return e.ha
}

// HAEnabled reports whether the environment runs more than one instance.
func (e *Env) HAEnabled() bool {
	return e.ha != nil
}

// Size returns the number of instances.
func (c *HACluster) Size() int {
	return len(c.instances)
}

// URL returns the base URL of instance i, for a request that must reach one
// instance and not the proxy.
func (c *HACluster) URL(i int) string {
	return c.instances[i].url
}

// Alive reports whether the process of instance i runs.
func (c *HACluster) Alive(i int) bool {
	inst := c.instances[i]

	inst.mu.Lock()
	defer inst.mu.Unlock()

	return inst.cmd != nil
}

// Healthy reports whether the proxy sends requests to instance i.
func (c *HACluster) Healthy(i int) bool {
	return c.instances[i].healthy.Load()
}

// Logs returns the output of every process that ran as instance i, oldest
// first, with the start of each in its own block.
func (c *HACluster) Logs(i int) string {
	inst := c.instances[i]

	inst.mu.Lock()
	defer inst.mu.Unlock()

	var out strings.Builder

	for n, logs := range inst.logs {
		fmt.Fprintf(&out, "--- instance %d, process %d ---\n%s", i, n, logs.String())

		if n < len(inst.exits) {
			fmt.Fprintf(&out, "--- exit: %v ---\n", inst.exits[n])
		}
	}

	return out.String()
}

// IDs returns the instance IDs that the processes of instance i logged at
// start, oldest first. A restart gives the instance a new ID.
func (c *HACluster) IDs(i int) []string {
	inst := c.instances[i]

	inst.mu.Lock()
	defer inst.mu.Unlock()

	ids := make([]string, 0, len(inst.logs))

	for _, logs := range inst.logs {
		if m := instanceIDPattern.FindStringSubmatch(logs.String()); m != nil {
			ids = append(ids, m[1])
		}
	}

	return ids
}

// IndexOfID returns the instance that ran a process with the instance ID id,
// or -1.
func (c *HACluster) IndexOfID(id string) int {
	for i := range c.instances {
		if slices.Contains(c.IDs(i), id) {
			return i
		}
	}

	return -1
}

// Kill ends instance i with SIGKILL, reaps the process and takes the
// instance out of the proxy at once. The database and the blob store stay.
func (c *HACluster) Kill(i int) error {
	return c.end(i, syscall.SIGKILL, instanceKillTimeout)
}

// Stop ends instance i with SIGTERM and waits for the process to exit.
func (c *HACluster) Stop(i int) error {
	return c.end(i, syscall.SIGTERM, instanceStopTimeout)
}

// StopAll ends every running instance with SIGTERM, one after the other, and
// fails the test when one of them exits with a status other than 0. The proxy
// has no instance to send requests to afterwards. A test that writes to the
// database while no server holds it uses this, as it does with one server.
func (c *HACluster) StopAll(tb testing.TB) {
	tb.Helper()

	for i, inst := range c.instances {
		if !c.Alive(i) {
			continue
		}

		if err := c.Stop(i); err != nil {
			tb.Fatalf("stop instance %d: %v", i, err)
		}

		inst.mu.Lock()
		last := inst.exits[len(inst.exits)-1]
		inst.mu.Unlock()

		if last != nil {
			tb.Fatalf("instance %d did not exit with status 0: %v\n%s", i, last, c.Logs(i))
		}
	}
}

// SetComponents sets the components that every instance runs from its next
// start on: raw is the value of --components, for example "api". A running
// instance keeps its components. Call it while no instance starts or stops.
func (c *HACluster) SetComponents(raw string) {
	c.cfg.components = raw
}

// SetBinary sets the server binary that every instance runs from its next
// start on: bin is the path of the binary. A running instance keeps its
// binary. A rolling deploy stops an instance, calls SetBinary and starts the
// instance again. Call it while no instance starts or stops.
func (c *HACluster) SetBinary(bin string) {
	c.cfg.bin = bin
}

// InstanceRequest sends one request with the bootstrap token to instance i itself,
// not through the proxy, and returns the status and the body. It shows what a
// single instance answers, also when the proxy would send the request to
// another one.
func (e *Env) InstanceRequest(ctx context.Context, i int, method, path, body string) (int, string, error) {
	cluster := e.ha
	if cluster == nil || i < 0 || i >= len(cluster.instances) {
		return 0, "", fmt.Errorf("%w: instance %d", errInstanceDown, i)
	}

	inst := cluster.instances[i]

	req, err := http.NewRequestWithContext(ctx, method, inst.url+path, strings.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("build %s %s: %w", method, path, err)
	}

	req.Header.Set("Authorization", "token "+e.token)

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := inst.transport.RoundTrip(req)
	if err != nil {
		return 0, "", fmt.Errorf("%s %s on instance %d: %w", method, path, i, err)
	}

	raw, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, "", fmt.Errorf("read %s %s on instance %d: %w", method, path, i, err)
	}

	return resp.StatusCode, string(raw), nil
}

// StartAll starts every instance that does not run, one after the other, and
// waits until the proxy sends requests to all of them.
func (c *HACluster) StartAll(ctx context.Context, tb testing.TB) {
	tb.Helper()

	for i := range c.instances {
		if c.Alive(i) {
			continue
		}

		if err := c.Start(ctx, i); err != nil {
			tb.Fatalf("start instance %d: %v", i, err)
		}
	}

	if err := c.WaitHealthy(ctx); err != nil {
		tb.Fatalf("%v", err)
	}
}

// Start starts instance i again on its port and waits until it answers the
// readiness probe. The proxy sends requests to it from then on.
func (c *HACluster) Start(ctx context.Context, i int) error {
	inst := c.instances[i]

	inst.mu.Lock()
	running := inst.cmd != nil
	inst.mu.Unlock()

	if running {
		return fmt.Errorf("%w: instance %d", errInstanceUp, i)
	}

	return c.boot(ctx, inst, inst.port)
}

// WaitHealthy waits until the proxy sends requests to every instance that
// runs.
func (c *HACluster) WaitHealthy(ctx context.Context) error {
	for ctx.Err() == nil {
		all := true

		for i, inst := range c.instances {
			if c.Alive(i) && !inst.healthy.Load() {
				all = false
			}
		}

		if all {
			return nil
		}

		select {
		case <-ctx.Done():
		case <-time.After(healthEvery):
		}
	}

	return fmt.Errorf("wait for healthy instances: %w", ctx.Err())
}

// end sends sig to the process of instance i and waits for it to exit.
func (c *HACluster) end(i int, sig syscall.Signal, wait time.Duration) error {
	inst := c.instances[i]

	inst.mu.Lock()
	cmd, done := inst.cmd, inst.done
	inst.cmd = nil
	inst.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("%w: instance %d", errInstanceDown, i)
	}

	inst.healthy.Store(false)

	if err := cmd.Process.Signal(sig); err != nil {
		return fmt.Errorf("signal instance %d: %w", i, err)
	}

	select {
	case <-done:
	case <-time.After(wait):
		return fmt.Errorf("%w: instance %d after %s within %s", errInstanceExit, i, sig, wait)
	}

	inst.transport.CloseIdleConnections()

	return nil
}

// bootAll starts the instances one after the other, the first of which
// migrates the database, and starts the health checks.
func (c *HACluster) bootAll(ctx context.Context) error {
	for i, inst := range c.instances {
		if err := c.boot(ctx, inst, "0"); err != nil {
			return fmt.Errorf("start instance %d: %w", i, err)
		}
	}

	c.wg.Add(1)

	go c.checkLoop(context.WithoutCancel(ctx))

	return nil
}

// boot starts the process of inst on port and waits until it is ready. A port
// of "0" lets the server pick one, and the instance keeps it for restarts.
// The process ends with the cluster, not with ctx.
func (c *HACluster) boot(ctx context.Context, inst *haInstance, port string) error {
	cmd, logs, err := spawnBinaryBackend(context.WithoutCancel(ctx), c.cfg, port)
	if err != nil {
		return err
	}

	done := make(chan struct{})

	go func() {
		// The exit error of a killed process is the expected outcome. The
		// log of the instance shows it.
		exitErr := cmd.Wait()

		inst.mu.Lock()
		inst.exits = append(inst.exits, exitErr)
		inst.mu.Unlock()

		close(done)
	}()

	inst.mu.Lock()
	inst.cmd, inst.done = cmd, done
	inst.logs = append(inst.logs, logs)
	inst.mu.Unlock()

	deadline := time.Now().Add(binaryStartTimeout)

	if port == "0" {
		portErr := waitFor(ctx, deadline, done, func() bool {
			p, ok := listeningPortFromLogs(logs.String())
			if ok {
				port = p
			}

			return ok
		})
		if portErr != nil {
			return fmt.Errorf("wait for the port of the server binary: %w\n%s", portErr, logs.String())
		}

		inst.port = port
		inst.url = "https://127.0.0.1:" + port
	}

	err = waitFor(ctx, deadline, done, func() bool { return c.probe(ctx, inst) })
	if err != nil {
		return fmt.Errorf("wait for the readiness of the server binary: %w\n%s", err, logs.String())
	}

	inst.healthy.Store(true)

	return nil
}

// waitFor polls ok every binaryStartPoll until it is true. It fails when the
// process has exited, the deadline passes or ctx ends.
func waitFor(ctx context.Context, deadline time.Time, exited <-chan struct{}, ok func() bool) error {
	for time.Now().Before(deadline) {
		if ok() {
			return nil
		}

		select {
		case <-exited:
			return errProcessExited
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		case <-time.After(binaryStartPoll):
		}
	}

	return errWaitTimeout
}

// probe reports whether inst answers 200 on the readiness probe.
func (c *HACluster) probe(ctx context.Context, inst *haInstance) bool {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, inst.url+healthPath, http.NoBody)
	if err != nil {
		return false
	}

	resp, err := inst.transport.RoundTrip(req)
	if err != nil {
		return false
	}

	_, copyErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()

	return copyErr == nil && closeErr == nil && resp.StatusCode == http.StatusOK
}

// checkLoop probes every running instance each healthEvery and sets whether
// the proxy sends requests to it, until the cluster closes.
func (c *HACluster) checkLoop(ctx context.Context) {
	defer c.wg.Done()

	ticker := time.NewTicker(healthEvery)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
		}

		var probes sync.WaitGroup

		for i, inst := range c.instances {
			if !c.Alive(i) {
				continue
			}

			probes.Go(func() {
				// A probe that ends after the instance went down, or after
				// Kill, must not turn the instance healthy again.
				ok := c.probe(ctx, inst)
				inst.healthy.Store(ok && c.Alive(i))
			})
		}

		probes.Wait()
	}
}

// close ends every process, the check loop and the idle connections. It
// returns the errors of the kills.
func (c *HACluster) close() error {
	var errs []error

	c.closeOnce.Do(func() {
		close(c.stop)
		c.wg.Wait()

		for _, inst := range c.instances {
			inst.mu.Lock()
			cmd, done := inst.cmd, inst.done
			inst.cmd = nil
			inst.mu.Unlock()

			if cmd != nil && cmd.Process != nil {
				if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					errs = append(errs, err)
				}

				<-done
			}

			inst.transport.CloseIdleConnections()
		}
	})

	return errors.Join(errs...)
}

// pick returns the next healthy instance that is not in tried, in round-robin
// order, or -1.
func (c *HACluster) pick(tried []bool) int {
	n := len(c.instances)
	start := int((c.next.Add(1) - 1) % int64(n))

	for k := range n {
		i := (start + k) % n
		if !tried[i] && c.instances[i].healthy.Load() {
			return i
		}
	}

	return -1
}

// handler returns the proxy handler: it sends each request to the next healthy
// instance, with the retry rules of haTransport.
func (c *HACluster) handler() http.Handler {
	proxy := new(httputil.ReverseProxy)
	proxy.Rewrite = func(r *httputil.ProxyRequest) {
		// The instance is chosen in RoundTrip. The host stays the one of the
		// client, so absolute URLs that the instance reports point back
		// through the proxy.
		r.SetURL(&url.URL{Scheme: "https", Host: instanceHost})
		r.Out.Host = r.In.Host
		r.SetXForwarded()
	}
	proxy.Transport = haTransport{cluster: c}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		status := http.StatusBadGateway
		if errors.Is(err, errNoHealthyInstance) {
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "1")
		}

		http.Error(w, err.Error(), status)
	}

	return proxy
}

// haTransport sends each request to the next healthy instance.
//
// It sends a request again to another instance only when the first try fails
// before the instance answers, and only when that is safe: any method when
// the connection never opened, and a method that is idempotent by definition
// (GET, HEAD, OPTIONS, PUT, DELETE) after a failure on an open connection. A
// POST or a PATCH that failed on an open connection is not sent again: the
// instance may have run it, so the client sees the error, as it does behind a
// production load balancer.
type haTransport struct {
	cluster *HACluster
}

// RoundTrip implements http.RoundTripper.
func (t haTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := bufferBody(req)
	if err != nil {
		return nil, err
	}

	c := t.cluster
	tried := make([]bool, len(c.instances))

	var lastErr error

	// lastResp is the newest refusal of an instance below the minimum. The
	// client sees it when no other instance takes the request.
	var lastResp *http.Response

	for range c.instances {
		i := c.pick(tried)
		if i < 0 {
			break
		}

		tried[i] = true

		resp, again, tryErr := c.try(req, body, i)
		if !again {
			return resp, tryErr
		}

		lastErr = tryErr
		if resp != nil {
			lastResp = resp
		}
	}

	if lastResp != nil {
		return lastResp, nil
	}

	if lastErr == nil {
		lastErr = errNoHealthyInstance
	}

	return nil, lastErr
}

// try sends req to instance i. again is true when the proxy may send req to
// another instance: the instance failed in a way that is safe to repeat, or
// it refused the request because its binary is below the minimum of the
// schema. A refusal comes back as resp with errBelowMinimum.
func (c *HACluster) try(req *http.Request, body []byte, i int) (*http.Response, bool, error) {
	inst := c.instances[i]

	out := req.Clone(req.Context())
	out.URL.Scheme = "https"
	out.URL.Host = "127.0.0.1:" + inst.port
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }

	resp, sendErr := inst.transport.RoundTrip(out)
	if sendErr != nil {
		if req.Context().Err() != nil {
			return nil, false, fmt.Errorf("send to instance %d: %w", i, sendErr)
		}

		// A failed instance leaves the pool until a probe passes again.
		inst.healthy.Store(false)
		inst.transport.CloseIdleConnections()

		return nil, retryable(req.Method, sendErr), fmt.Errorf("send to instance %d: %w", i, sendErr)
	}

	refused, replay, refusalErr := belowMinimumRefusal(resp)
	if refusalErr != nil {
		return nil, false, fmt.Errorf("instance %d: %w", i, refusalErr)
	}

	if refused {
		// The instance refused before it wrote anything, so another instance
		// can take the request, as a production load balancer does on a 503.
		// The instance leaves the pool until a probe passes again.
		inst.healthy.Store(false)

		return replay, true, fmt.Errorf("instance %d: %w", i, errBelowMinimum)
	}

	noteBackend(req, "i"+strconv.Itoa(i))

	return resp, false, nil
}

// minimumRefusalText is the part of the answer of an instance below the
// minimum binary version that names the rule.
const minimumRefusalText = "the minimum binary version of the database schema"

// maxRefusalBody bounds the part of a 503 answer that the proxy reads.
const maxRefusalBody = 64 << 10

// belowMinimumRefusal reports whether resp is the refusal of an instance whose
// binary is below the minimum of the schema: status 503 and the text that
// names the rule. For an answer that is not a refusal, the body of resp stays
// readable. For a refusal it returns a copy of the answer that holds the
// whole body.
func belowMinimumRefusal(resp *http.Response) (bool, *http.Response, error) {
	if resp.StatusCode != http.StatusServiceUnavailable {
		return false, nil, nil
	}

	head, err := io.ReadAll(io.LimitReader(resp.Body, maxRefusalBody))
	if err != nil {
		return false, nil, fmt.Errorf("read the 503 answer: %w", err)
	}

	if !bytes.Contains(head, []byte(minimumRefusalText)) {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), resp.Body), resp.Body}

		return false, nil, nil
	}

	replay := new(http.Response)
	*replay = *resp
	replay.Body = io.NopCloser(bytes.NewReader(head))
	replay.ContentLength = int64(len(head))

	if err := resp.Body.Close(); err != nil {
		return false, nil, fmt.Errorf("close the refusal: %w", err)
	}

	return true, replay, nil
}

// bufferBody reads the body of req, so the proxy can send it again.
func bufferBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}

	body, readErr := io.ReadAll(io.LimitReader(req.Body, maxBufferedBody+1))
	closeErr := req.Body.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read the request body: %w", err)
	}

	if len(body) > maxBufferedBody {
		return nil, fmt.Errorf("%w: more than %d bytes", errBodyTooLarge, maxBufferedBody)
	}

	return body, nil
}

// retryable reports whether the proxy may send a request that failed with
// err to another instance.
func retryable(method string, err error) bool {
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "dial" {
		return true
	}

	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}
