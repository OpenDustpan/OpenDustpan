//go:build e2e

package ha_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/e2e/ha"
)

const (
	outageLongWindow   = 60 * time.Second
	outageWork         = 30
	outageReadyTimeout = 90 * time.Second
	outageMarkerWait   = 5 * time.Minute
	outagePoll         = 100 * time.Millisecond
	outageShortLead    = 2 * time.Second
	outageRetryAfter   = "5"
	outageFileMode     = 0o600
	outagePlugin       = "v1.1.0"
	outageSecretKey    = "outageSecret"
	outageSecretValue  = "outage-secret-value" // trufflehog:ignore fake value of a test config secret
)

var errOutageLockStays = errors.New("the workspace stays locked after a lock during the outage")

// outageSetup holds the real installation that the probe drives and the
// installed CLI of each version of the matrix.
type outageSetup struct {
	env     *e2e.Env
	cluster *e2e.HACluster
	probe   *ha.DatabaseOutageProbe
	matrix  []ha.CLIVersion
	pulumis []e2e.Install
	tofus   []e2e.Install
	clock   ha.OutageClock
}

// outageRound is one pair of installed CLI versions that one outage step
// runs. The index names the projects and the step of the round.
type outageRound struct {
	index        int
	pulumi, tofu e2e.Install
}

// rounds pairs the versions of the two matrices, so that each version runs in
// at least one round. The shorter matrix repeats its last version.
func (s *outageSetup) rounds() []outageRound {
	rounds := make([]outageRound, 0, max(len(s.pulumis), len(s.tofus)))

	for i := range max(len(s.pulumis), len(s.tofus)) {
		rounds = append(rounds, outageRound{
			index:  i,
			pulumi: s.pulumis[min(i, len(s.pulumis)-1)],
			tofu:   s.tofus[min(i, len(s.tofus)-1)],
		})
	}

	return rounds
}

// installOutageMatrix installs each version of the matrix of tool. A version
// that cannot be installed fails the test, and the failure names the version.
func installOutageMatrix(ctx context.Context, t *testing.T, fetcher *e2e.Fetcher, tool e2e.Tool) []e2e.Install {
	t.Helper()

	labels := tool.Versions()
	if len(labels) == 0 {
		t.Fatalf("the %s matrix has no version", tool.Name)
	}

	installs := make([]e2e.Install, 0, len(labels))

	for _, label := range labels {
		install, err := fetcher.Install(ctx, tool, label)
		if err != nil {
			t.Fatalf("install %s %s: %v", tool.Name, label, err)
		}

		installs = append(installs, install)
	}

	return installs
}

// probeProxy adapts the database control of the environment to the fixture.
type probeProxy struct {
	env *e2e.Env
}

func (p probeProxy) Stop(tb testing.TB) {
	tb.Helper()
	p.env.StopDatabase(tb)
}

func (p probeProxy) Start(tb testing.TB) {
	tb.Helper()
	p.env.StartDatabase(tb)
}

// probeInstance sends a request to one real instance behind the round-robin proxy.
type probeInstance struct {
	cluster *e2e.HACluster
	index   int
}

func (i probeInstance) Name() string {
	return fmt.Sprintf("i%d", i.index)
}

func (i probeInstance) Send(ctx context.Context, req ha.InstanceRequest) (ha.InstanceResponse, error) {
	resp, err := i.cluster.Send(ctx, i.index, e2e.InstanceRequest{
		Method: req.Method,
		Path:   req.Path,
		Header: req.Header,
		Body:   req.Body,
	})

	return ha.InstanceResponse{Status: resp.Status, Header: resp.Header, Body: resp.Body}, err
}

// newOutageSetup skips unless the suite has real instances, PostgreSQL and a
// CLI matrix. It wires the fixture to the real proxy and the real instances.
func newOutageSetup(t *testing.T) *outageSetup {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skipf("the outage probe needs Linux, not %s", runtime.GOOS)
	}

	instances, err := e2e.Instances()
	if err != nil {
		t.Fatalf("instances: %v", err)
	}

	if instances < 2 {
		t.Skip("the outage probe needs E2E_INSTANCES above 1")
	}

	if e2e.PostgresURL() == "" {
		t.Skip("the outage probe needs a PostgreSQL database")
	}

	ctx := t.Context()
	cache, err := e2e.CacheDir()
	if err != nil {
		t.Fatalf("CLI cache: %v", err)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cache}

	pulumis := installOutageMatrix(ctx, t, fetcher, e2e.Pulumi())
	tofus := installOutageMatrix(ctx, t, fetcher, e2e.OpenTofu())

	matrix := make([]ha.CLIVersion, 0, len(pulumis)+len(tofus))

	for _, install := range slices.Concat(pulumis, tofus) {
		matrix = append(matrix, ha.CLIVersion{Tool: install.Tool, Version: install.Version})
	}

	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, pulumis[0], e2e.StorePostgres)
	cluster := env.HA(t)

	targets := make([]ha.Instance, 0, cluster.Size())
	for i := range cluster.Size() {
		targets = append(targets, probeInstance{cluster: cluster, index: i})
	}

	probe, err := ha.NewDatabaseOutageProbe(probeProxy{env: env}, targets, matrix)
	if err != nil {
		t.Fatalf("new database outage probe: %v", err)
	}

	return &outageSetup{
		env:     env,
		cluster: cluster,
		probe:   probe,
		matrix:  matrix,
		pulumis: pulumis,
		tofus:   tofus,
		clock:   ha.RealOutageClock{},
	}
}

// outageCLI runs one installed CLI in its own project directory.
type outageCLI struct {
	env     *e2e.Env
	install e2e.Install
	dir     string
}

// outageRun is one CLI command that runs while the database stops.
type outageRun struct {
	done chan struct{}
	res  e2e.Result
	err  error
}

func (c outageCLI) start(ctx context.Context, t *testing.T, extra []string, args ...string) *outageRun {
	t.Helper()

	run := &outageRun{
		done: make(chan struct{}),
		res:  e2e.Result{Command: "", Stdout: "", Stderr: "", Duration: 0, ExitCode: 0},
		err:  nil,
	}

	go func() {
		defer close(run.done)
		run.res, run.err = c.env.RunAs(ctx, c.install, c.dir, extra, args...)
	}()

	t.Cleanup(func() { <-run.done })

	return run
}

func (c outageCLI) run(ctx context.Context, t *testing.T, args ...string) {
	t.Helper()

	c.outputWith(ctx, t, nil, args...)
}

// runWith runs one command that must exit 0, with extra environment variables.
func (c outageCLI) runWith(ctx context.Context, t *testing.T, extra []string, args ...string) {
	t.Helper()

	c.outputWith(ctx, t, extra, args...)
}

// output runs one command that must exit 0 and returns its standard output.
func (c outageCLI) output(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()

	return c.outputWith(ctx, t, nil, args...)
}

// outputWith runs one command that must exit 0, with extra environment
// variables, and returns its standard output.
func (c outageCLI) outputWith(ctx context.Context, t *testing.T, extra []string, args ...string) string {
	t.Helper()

	res, err := c.env.RunAs(ctx, c.install, c.dir, extra, args...)
	if err != nil {
		t.Fatalf("%s %s: %v", c.install.Tool, strings.Join(args, " "), err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("%s %s exits %d, want 0:\n%s%s",
			c.install.Tool, strings.Join(args, " "), res.ExitCode, res.Stdout, res.Stderr)
	}

	return res.Stdout
}

// outageProject is one pair of CLI projects with resources that wait before
// their first state write.
type outageProject struct {
	pulumi, tofu             outageCLI
	stack, workspace         string
	pulumiMarker, tofuMarker string
}

func newOutageProject(
	ctx context.Context, t *testing.T, s *outageSetup, round *outageRound, name string,
) outageProject {
	t.Helper()

	pulumiDir, tofuDir, markers := t.TempDir(), t.TempDir(), t.TempDir()
	project := outageProject{
		pulumi:       outageCLI{env: s.env, install: round.pulumi, dir: pulumiDir},
		tofu:         outageCLI{env: s.env, install: round.tofu, dir: tofuDir},
		stack:        e2e.Namespace + "/" + name + "/dev",
		workspace:    name,
		pulumiMarker: filepath.Join(markers, "pulumi-started"),
		tofuMarker:   filepath.Join(markers, "tofu-started"),
	}

	pulumiProgram := `name: ` + name + `
runtime: yaml
resources:
  wait:
    type: command:local:Command
    properties:
      create: touch "$E2E_HA_PULUMI_STARTED" && sleep ` + strconv.Itoa(outageWork) + `
`

	tofuConfig := `terraform {
  cloud {
    hostname     = ` + strconv.Quote(s.env.Host()) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = "` + name + `"
    }
  }
}

resource "terraform_data" "wait" {
  provisioner "local-exec" {
    command = "touch \"$E2E_HA_TOFU_STARTED\" && sleep ` + strconv.Itoa(outageWork) + `"
  }
}
`

	if err := os.WriteFile(filepath.Join(pulumiDir, "Pulumi.yaml"), []byte(pulumiProgram), outageFileMode); err != nil {
		t.Fatalf("write the Pulumi project: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tofuDir, "main.tf"), []byte(tofuConfig), outageFileMode); err != nil {
		t.Fatalf("write the OpenTofu project: %v", err)
	}

	project.pulumi.run(ctx, t, "plugin", "install", "resource", "command", outagePlugin)
	project.pulumi.run(ctx, t, "stack", "init", project.stack)
	project.tofu.run(ctx, t, "init", "-input=false", "-no-color")
	project.pulumi.run(ctx, t, "config", "set", "--secret", "--stack", project.stack,
		name+":"+outageSecretKey, outageSecretValue)

	// One apply before the outage gives the workspace a state version, so that
	// the apply during the outage reads the current state and then writes a
	// new state version. The marker of this apply is removed, so that the
	// outage waits for the marker of the next apply.
	project.tofu.runWith(ctx, t, []string{"E2E_HA_TOFU_STARTED=" + project.tofuMarker},
		"apply", "-input=false", "-auto-approve", "-no-color")

	if err := os.Remove(project.tofuMarker); err != nil {
		t.Fatalf("remove the OpenTofu marker of the first apply: %v", err)
	}

	return project
}

// outageRuns holds the two CLI runs that startBoth starts during the outage:
// the Pulumi run and the OpenTofu run.
type outageRuns struct {
	up    *outageRun
	apply *outageRun
}

// startBoth starts both CLIs. The apply replaces the wait resource, so that
// its command runs again although the workspace already has it in its state.
func (p outageProject) startBoth(ctx context.Context, t *testing.T) outageRuns {
	t.Helper()

	return outageRuns{
		up:    p.pulumi.start(ctx, t, p.pulumiEnv(), p.upArgs()...),
		apply: p.tofu.start(ctx, t, p.tofuEnv(), outageApplyArgs()...),
	}
}

// pulumiEnv and tofuEnv set the marker that the wait resource of each CLI
// touches when it starts.
func (p outageProject) pulumiEnv() []string {
	return []string{"E2E_HA_PULUMI_STARTED=" + p.pulumiMarker}
}

func (p outageProject) tofuEnv() []string {
	return []string{"E2E_HA_TOFU_STARTED=" + p.tofuMarker}
}

// upArgs and outageApplyArgs are the commands that startBoth runs during the
// outage.
func (p outageProject) upArgs() []string {
	return []string{"up", "--yes", "--non-interactive", "--stack", p.stack}
}

func outageApplyArgs() []string {
	return []string{"apply", "-input=false", "-auto-approve", "-no-color", "-replace=terraform_data.wait"}
}

func waitOutageMarkers(ctx context.Context, t *testing.T, clock ha.OutageClock, paths ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, outageMarkerWait)
	defer cancel()

	for _, path := range paths {
		for {
			if _, err := os.Stat(path); err == nil {
				break
			}

			select {
			case <-ctx.Done():
				t.Fatalf("wait for %s: %v", path, ctx.Err())
			case <-clock.After(outagePoll):
			}
		}
	}
}

// waitOutageHealthy waits until the proxy sends requests to each instance
// that runs, before the first proxied request of the round. A failure names
// each instance that is not healthy.
func waitOutageHealthy(ctx context.Context, t *testing.T, s *outageSetup, round *outageRound) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, outageReadyTimeout)
	defer cancel()

	if err := s.cluster.WaitHealthy(ctx); err != nil {
		var unhealthy []string

		for i := range s.cluster.Size() {
			if s.cluster.Alive(i) && !s.cluster.Healthy(i) {
				unhealthy = append(unhealthy, probeInstance{cluster: s.cluster, index: i}.Name())
			}
		}

		t.Fatalf("round %d: instances %s are not healthy after %s: %v",
			round.index, strings.Join(unhealthy, ", "), outageReadyTimeout, err)
	}
}

// outageWriteRequest writes a state version of the workspace with id through
// the proxy with an access token.
func outageWriteRequest(env *e2e.Env, id string) ha.InstanceRequest {
	return ha.InstanceRequest{
		Method: http.MethodPost,
		Path:   "/api/v2/workspaces/" + id + "/state-versions",
		Header: http.Header{
			"Authorization": {"Bearer " + env.TokenA()},
			"Content-Type":  {"application/vnd.api+json"},
		},
		Body: `{"data":{"type":"state-versions","attributes":{"serial":1,"md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`,
	}
}

// outageVersionDoc is one entry of the state version list of the TFE API.
type outageVersionDoc struct {
	ID string `json:"id"`
}

// outageWorkspaceLock is the lock state in the attributes of a TFE workspace
// document.
type outageWorkspaceLock struct {
	Locked bool `json:"locked"`
}

// outageWorkspaceDoc is the workspace document of the TFE API.
type outageWorkspaceDoc struct {
	Attributes outageWorkspaceLock `json:"attributes"`
}

func outageVersions(ctx context.Context, t *testing.T, env *e2e.Env, workspace string) int {
	t.Helper()

	path := "/api/v2/state-versions?filter[organization][name]=" + e2e.Namespace +
		"&filter[workspace][name]=" + workspace

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, path, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list state versions of %s: %d, %v (%s)", workspace, status, err, body)
	}

	var doc struct {
		Data []outageVersionDoc `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode state versions of %s: %v (%s)", workspace, err, body)
	}

	return len(doc.Data)
}

// outageLockRequest locks the workspace with id through the proxy with an
// access token.
func outageLockRequest(env *e2e.Env, id string) ha.InstanceRequest {
	return ha.InstanceRequest{
		Method: http.MethodPost,
		Path:   "/api/v2/workspaces/" + id + "/actions/lock",
		Header: http.Header{
			"Authorization": {"Bearer " + env.TokenA()},
			"Content-Type":  {"application/vnd.api+json"},
		},
		Body: `{"reason":"database outage probe"}`,
	}
}

func checkOutageUnlocked(ctx context.Context, t *testing.T, env *e2e.Env, workspace string) {
	t.Helper()

	if outageLocked(ctx, t, env, workspace) {
		t.Errorf("workspace %s stays locked after the outage", workspace)
	}
}

func outageLocked(ctx context.Context, t *testing.T, env *e2e.Env, workspace string) bool {
	t.Helper()

	id := env.WorkspaceIDByName(ctx, t, workspace)

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+id, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read workspace %s: %d, %v (%s)", workspace, status, err, body)
	}

	var doc struct {
		Data outageWorkspaceDoc `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode workspace %s: %v (%s)", workspace, err, body)
	}

	return doc.Data.Attributes.Locked
}

func checkOutageRun(t *testing.T, what string, run *outageRun) {
	t.Helper()

	if run.err != nil {
		t.Fatalf("%s: %v", what, run.err)
	}

	if run.res.ExitCode == 0 {
		return
	}

	out := run.res.Stdout + run.res.Stderr
	if !strings.Contains(out, "503") && !strings.Contains(strings.ToLower(out), "service unavailable") {
		t.Errorf("%s exits %d with no 503 message:\n%s", what, run.res.ExitCode, out)
	}
}

// cancelOutageUpdateDuringOutage runs pulumi cancel while the database is
// down. The command must fail, and the failure must keep a 503 message.
func cancelOutageUpdateDuringOutage(ctx context.Context, t *testing.T, project outageProject) {
	t.Helper()

	res, err := project.pulumi.env.RunAs(ctx, project.pulumi.install, project.pulumi.dir, nil,
		"cancel", "--yes", "--stack", project.stack)
	if err != nil {
		t.Fatalf("pulumi cancel during the outage: %v", err)
	}

	if res.ExitCode == 0 {
		t.Errorf("pulumi cancel exits 0 while the database is down, want a failure:\n%s%s", res.Stdout, res.Stderr)

		return
	}

	out := res.Stdout + res.Stderr
	if !strings.Contains(out, "503") && !strings.Contains(strings.ToLower(out), "service unavailable") {
		t.Errorf("pulumi cancel exits %d with no 503 message:\n%s", res.ExitCode, out)
	}
}

func cancelOutageUpdate(ctx context.Context, t *testing.T, s *outageSetup, project outageProject) {
	t.Helper()

	if !outageUpdateActive(ctx, t, s, project) {
		return
	}

	project.pulumi.run(ctx, t, "cancel", "--yes", "--stack", project.stack)
}

// outageUpdateActive reports whether the stack of project has an active
// update: the update lock of the stack.
func outageUpdateActive(ctx context.Context, t *testing.T, s *outageSetup, project outageProject) bool {
	t.Helper()

	status, body, err := s.env.APIStatus(ctx, http.MethodGet, "/api/stacks/"+project.stack, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read stack %s: %d, %v (%s)", project.stack, status, err, body)
	}

	return strings.Contains(body, `"activeUpdate":"`) && !strings.Contains(body, `"activeUpdate":""`)
}

// outageHistory returns the number of updates in the history of the stack of
// project.
func outageHistory(ctx context.Context, t *testing.T, project outageProject) int {
	t.Helper()

	out := project.pulumi.output(ctx, t, "stack", "history", "--json", "--stack", project.stack)

	var updates []json.RawMessage
	if err := json.Unmarshal([]byte(out), &updates); err != nil {
		t.Fatalf("decode the history of stack %s: %v (%s)", project.stack, err, out)
	}

	return len(updates)
}

// outageDeployment is the part of a Pulumi stack export that shows that the
// state can be read.
type outageDeployment struct {
	Version    int             `json:"version"`
	Deployment json.RawMessage `json:"deployment"`
}

// checkOutageExport fails when the state of the stack of project cannot be
// exported and decoded.
func checkOutageExport(ctx context.Context, t *testing.T, project outageProject) {
	t.Helper()

	out := project.pulumi.output(ctx, t, "stack", "export", "--stack", project.stack)

	var doc outageDeployment
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Errorf("the state of stack %s is not valid JSON after the failed pulumi up: %v", project.stack, err)

		return
	}

	if doc.Version == 0 || len(doc.Deployment) == 0 {
		t.Errorf("the state of stack %s has no deployment after the failed pulumi up:\n%s", project.stack, out)
	}
}

// outageWritesAt returns the earliest time of the first state write of the
// CLI that touched marker: the marker time plus the wait of its resource. A
// refused write before it, such as pulumi cancel, is not a retry of the run.
func outageWritesAt(t *testing.T, marker string) time.Time {
	t.Helper()

	info, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("read the time of %s: %v", marker, err)
	}

	return info.ModTime().Add(outageWork * time.Second)
}

// longOutageStep names the recorder step of the long outage of round.
func longOutageStep(round *outageRound) string {
	return fmt.Sprintf("long database outage %d", round.index)
}

// logOutageRefused logs each distinct method, path, status and user agent of
// the 503 answers in step, so that a failed match shows what the CLIs sent.
func logOutageRefused(t *testing.T, step string, entries []e2e.Entry) {
	t.Helper()

	seen := map[string]bool{}

	for i := range entries {
		entry := &entries[i]
		if entry.Step != step || entry.Status != http.StatusServiceUnavailable {
			continue
		}

		line := fmt.Sprintf("%s %s %d %q", entry.Method, entry.Path, entry.Status, entry.UserAgent)
		if seen[line] {
			continue
		}

		seen[line] = true
		t.Logf("503 in %s: %s", step, line)
	}
}

func recordOutageRetries(
	t *testing.T, s *outageSetup, round *outageRound, report *ha.RetryReport, writesAt map[string]time.Time,
) {
	t.Helper()

	step := longOutageStep(round)
	reportKey := map[string]string{
		e2e.ToolPulumi: ha.VersionLabel(round.pulumi.Tool, round.pulumi.Version),
		e2e.ToolTofu:   ha.VersionLabel(round.tofu.Tool, round.tofu.Version),
	}
	entries := s.env.Recorder.Entries()

	logOutageRefused(t, step, entries)

	for i := range entries {
		entry := &entries[i]

		if entry.Step != step || entry.Status != http.StatusServiceUnavailable ||
			entry.Method == http.MethodGet {
			continue
		}

		// The report measures only the write kinds. Another refused write,
		// such as a lease renewal, is in the log of logOutageRefused only.
		if _, found := ha.WriteKindOf(entry.Method, entry.Path); !found {
			continue
		}

		tool := strings.ToLower(entry.UserAgent)
		var name string

		switch {
		case strings.Contains(tool, e2e.ToolPulumi):
			name = e2e.ToolPulumi
		case strings.Contains(tool, e2e.ToolTofu), strings.Contains(tool, "terraform"):
			name = e2e.ToolTofu
		default:
			continue
		}

		version, found := reportKey[name]
		if !found || entry.At.Before(writesAt[name]) {
			continue
		}

		attempt := ha.WriteAttempt{
			Version: version,
			Method:  entry.Method,
			Path:    entry.Path,
			Status:  entry.Status,
			Round:   round.index,
			At:      entry.At,
		}

		if err := report.Add(attempt); err != nil {
			t.Fatalf("record a retry of %s: %v", version, err)
		}
	}
}

func checkOutageNoChanges(ctx context.Context, t *testing.T, s *outageSetup, project outageProject) {
	t.Helper()

	project.pulumi.run(ctx, t, "preview", "--expect-no-changes", "--non-interactive", "--stack", project.stack)
	project.tofu.run(ctx, t, "plan", "-detailed-exitcode", "-input=false", "-no-color")
	checkOutageUnlocked(ctx, t, s.env, project.workspace)

	got := strings.TrimSpace(project.pulumi.output(ctx, t, "config", "get", "--stack", project.stack,
		project.workspace+":"+outageSecretKey))
	if got != outageSecretValue {
		t.Errorf("the config secret of stack %s changed during the outage", project.stack)
	}
}

// outageInstanceIDs returns the process IDs that each instance logged at start.
func outageInstanceIDs(cluster *e2e.HACluster) [][]string {
	ids := make([][]string, 0, cluster.Size())
	for i := range cluster.Size() {
		ids = append(ids, cluster.IDs(i))
	}

	return ids
}

func checkOutageNoRestart(t *testing.T, cluster *e2e.HACluster, before [][]string) {
	t.Helper()

	after := outageInstanceIDs(cluster)
	if !slices.EqualFunc(before, after, slices.Equal[[]string]) {
		t.Errorf("an instance restarted during the outage: IDs were %v, now %v", before, after)
	}
}

// checkOutageStop stops the database, probes the server parts that the test
// needs and restores the database, before any CLI run. A missing part fails
// the test with no CLI run, and the failure names the part. A proxy that cannot
// stop the database, or any other error, fails the test.
func checkOutageStop(
	ctx context.Context, t *testing.T, s *outageSetup, project outageProject, write ha.InstanceRequest,
) {
	t.Helper()

	restored := false

	t.Cleanup(func() {
		if !restored {
			s.probe.Restore(context.WithoutCancel(ctx), t)
		}
	})

	lock := outageLockRequest(s.env, s.env.WorkspaceIDByName(ctx, t, project.workspace))
	err := s.probe.ProbeCapabilities(ctx, t, write, lock, outageRetryAfter)

	s.probe.Restore(ctx, t)
	restored = true

	if err == nil && outageLocked(ctx, t, s.env, project.workspace) {
		err = ha.MissingPart(ha.PartAtomicStateLocks,
			fmt.Errorf("workspace %s: %w", project.workspace, errOutageLockStays))
	}

	if part, missing := ha.MissingPartOf(err); missing {
		t.Fatalf("the outage probe needs the server part %q: %v", part, err)
	}

	if err != nil {
		t.Fatalf("probe the server parts: %v", err)
	}
}

// runLongOutage runs the long outage once for each round, so that each
// version of the matrix retries its writes. A version with no retry window
// fails the test, and the failure names the version.
func runLongOutage(t *testing.T, s *outageSetup) *ha.RetryReport {
	t.Helper()

	report := ha.NewRetryReport(s.matrix)

	rounds := s.rounds()
	for i := range rounds {
		runLongOutageRound(t, s, &rounds[i], report)
	}

	if unmeasured := report.Unmeasured(); len(unmeasured) > 0 {
		t.Fatalf("no retry window measured for %s: each version of the matrix must retry a refused write",
			strings.Join(unmeasured, ", "))
	}

	t.Logf("retry report: %s", report)

	if _, err := s.probe.ShortWindow(report); err != nil {
		t.Fatalf("short window from the retry report: %v", err)
	}

	return report
}

func runLongOutageRound(t *testing.T, s *outageSetup, round *outageRound, report *ha.RetryReport) {
	t.Helper()

	ctx := t.Context()
	waitOutageHealthy(ctx, t, s, round)
	project := newOutageProject(ctx, t, s, round, fmt.Sprintf("haoutage%d", round.index))
	before := outageVersions(ctx, t, s.env, project.workspace)
	idsBefore := outageInstanceIDs(s.cluster)

	t.Logf("long outage with pulumi %s and tofu %s", round.pulumi.Version, round.tofu.Version)
	s.env.Recorder.SetStep(longOutageStep(round))

	write := outageWriteRequest(s.env, s.env.WorkspaceIDByName(ctx, t, project.workspace))
	checkOutageStop(ctx, t, s, project, write)

	runs := project.startBoth(ctx, t)
	waitOutageMarkers(ctx, t, s.clock, project.pulumiMarker, project.tofuMarker)

	writesAt := map[string]time.Time{
		e2e.ToolPulumi: outageWritesAt(t, project.pulumiMarker),
		e2e.ToolTofu:   outageWritesAt(t, project.tofuMarker),
	}
	restored := false

	t.Cleanup(func() {
		if !restored {
			s.probe.Restore(context.WithoutCancel(ctx), t)
		}
	})

	started := s.clock.Now()
	s.probe.Stop(ctx, t, write, outageRetryAfter)

	if err := s.probe.WaitReady(ctx, http.StatusServiceUnavailable); err != nil {
		t.Fatalf("instances did not turn unready: %v", err)
	}

	if err := s.probe.CheckWrites(ctx, write, outageRetryAfter); err != nil {
		t.Fatalf("writes during the outage: %v", err)
	}

	cancelOutageUpdateDuringOutage(ctx, t, project)

	if remaining := outageLongWindow - s.clock.Now().Sub(started); remaining > 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("outage wait ended: %v", ctx.Err())
		case <-s.clock.After(remaining):
		}
	}

	s.probe.Restore(ctx, t)
	restored = true

	t.Logf("PostgreSQL stopped for %s", s.clock.Now().Sub(started).Round(time.Second))

	checkOutageNoRestart(t, s.cluster, idsBefore)

	<-runs.up.done
	<-runs.apply.done

	checkOutageRun(t, "pulumi up", runs.up)
	checkOutageRun(t, "tofu apply", runs.apply)
	recordOutageRetries(t, s, round, report, writesAt)

	// A failed apply must leave no state version. A successful apply commits one.
	if runs.apply.res.ExitCode != 0 {
		if got := outageVersions(ctx, t, s.env, project.workspace); got != before {
			t.Errorf("%d state versions after the failed apply, want %d", got, before)
		}
	}

	checkOutageUnlocked(ctx, t, s.env, project.workspace)
	cancelOutageUpdate(ctx, t, s, project)

	project.pulumi.runWith(ctx, t, []string{"E2E_HA_PULUMI_STARTED=" + project.pulumiMarker},
		"up", "--yes", "--non-interactive", "--stack", project.stack)
	project.tofu.runWith(ctx, t, []string{"E2E_HA_TOFU_STARTED=" + project.tofuMarker},
		"apply", "-input=false", "-auto-approve", "-no-color")
	checkOutageNoChanges(ctx, t, s, project)
}

// TestDatabaseOutageLongOutage proves that a 60 s stop during both CLIs ends
// with 503 on readiness and writes, a cancel and recovery, and no changes.
func TestDatabaseOutageLongOutage(t *testing.T) {
	t.Parallel()

	// Each test makes its own environment, with its own database proxy.
	s := newOutageSetup(t)
	runLongOutage(t, s)
}

// TestDatabaseOutageShortOutage measures the retry window of each write kind in
// the long step and stops PostgreSQL for half of the smallest window above
// 0 s in the same run, once for each round of the matrix. A command whose
// write kinds all retry must exit 0. A command with a write kind that is not
// retried must fail clean and succeed on the next run.
func TestDatabaseOutageShortOutage(t *testing.T) {
	t.Parallel()

	// Each test makes its own environment, with its own database proxy.
	s := newOutageSetup(t)
	report := runLongOutage(t, s)

	window, err := s.probe.ShortWindow(report)
	if err != nil {
		t.Fatalf("short window: %v", err)
	}

	rounds := s.rounds()
	for i := range rounds {
		runShortOutageRound(t, s, &rounds[i], window, report)
	}
}

// watchOutageReady polls GET /health/ready on each instance at the same time,
// from returned on. The returned function waits for the polls and gives, for
// each instance, the time from returned to its first 200, or -1 when the
// instance did not answer 200 in outageReadyTimeout.
func watchOutageReady(ctx context.Context, s *outageSetup, returned time.Time) func() []time.Duration {
	ctx, cancel := context.WithTimeout(ctx, outageReadyTimeout)
	instances := s.probe.Instances()
	after := make([]time.Duration, len(instances))

	var wg sync.WaitGroup

	for i, instance := range instances {
		after[i] = -1

		wg.Go(func() {
			for {
				resp, err := instance.Send(ctx, ha.InstanceRequest{
					Method: http.MethodGet,
					Path:   "/health/ready",
					Header: nil,
					Body:   "",
				})
				if err == nil && resp.Status == http.StatusOK {
					after[i] = s.clock.Now().Sub(returned)

					return
				}

				select {
				case <-ctx.Done():
					return
				case <-s.clock.After(outagePoll):
				}
			}
		})
	}

	return func() []time.Duration {
		wg.Wait()
		cancel()

		return after
	}
}

// logOutageRecovery logs, for each instance, the seconds from the database
// return to the first 200 on GET /health/ready and to the first proxied
// request of step that the instance answered with a status other than 503.
func logOutageRecovery(t *testing.T, s *outageSetup, step string, returned time.Time, readyAfter []time.Duration) {
	t.Helper()

	entries := s.env.Recorder.Entries()

	for i, instance := range s.probe.Instances() {
		ready := "no 200 in " + outageReadyTimeout.String()
		if readyAfter[i] >= 0 {
			ready = fmt.Sprintf("%.1f s", readyAfter[i].Seconds())
		}

		proxied := "no proxied request after the database return"

		for j := range entries {
			entry := &entries[j]
			if entry.Step != step || entry.Backend != instance.Name() || entry.At.Before(returned) ||
				entry.Status == http.StatusServiceUnavailable {
				continue
			}

			proxied = fmt.Sprintf("%.1f s (%s %s %d)",
				entry.At.Sub(returned).Seconds(), entry.Method, entry.Path, entry.Status)

			break
		}

		t.Logf("%s: instance %s: first GET /health/ready 200 after the database return: %s; "+
			"first proxied request that is not a 503: %s", step, instance.Name(), ready, proxied)
	}
}

func runShortOutageRound(
	t *testing.T, s *outageSetup, round *outageRound, window time.Duration, report *ha.RetryReport,
) {
	t.Helper()

	ctx := t.Context()
	waitOutageHealthy(ctx, t, s, round)
	project := newOutageProject(ctx, t, s, round, fmt.Sprintf("haquick%d", round.index))
	before := outageVersions(ctx, t, s.env, project.workspace)

	t.Logf("short outage with pulumi %s and tofu %s", round.pulumi.Version, round.tofu.Version)
	step := fmt.Sprintf("short database outage %d", round.index)
	s.env.Recorder.SetStep(step)

	write := outageWriteRequest(s.env, s.env.WorkspaceIDByName(ctx, t, project.workspace))
	checkOutageStop(ctx, t, s, project, write)

	runs := project.startBoth(ctx, t)
	waitOutageMarkers(ctx, t, s.clock, project.pulumiMarker, project.tofuMarker)

	select {
	case <-ctx.Done():
		t.Fatalf("wait for the short outage ended: %v", ctx.Err())
	case <-s.clock.After(outageWork*time.Second - outageShortLead):
	}

	restored := false

	t.Cleanup(func() {
		if !restored {
			s.probe.Restore(context.WithoutCancel(ctx), t)
		}
	})

	s.probe.Stop(ctx, t, write, outageRetryAfter)

	select {
	case <-ctx.Done():
		t.Fatalf("short outage wait ended: %v", ctx.Err())
	case <-s.clock.After(window):
	}

	returned := s.clock.Now()
	readyAfter := watchOutageReady(ctx, s, returned)

	s.probe.Restore(ctx, t)
	restored = true

	t.Logf("PostgreSQL stopped for %s (smallest retry window %s)", window, report.Shortest().Round(time.Second))

	<-runs.up.done
	<-runs.apply.done

	logOutageRecovery(t, s, step, returned, readyAfter())

	checkShortOutageUp(ctx, t, s, project, shortOutageCommand{
		run:        runs.up,
		notRetried: report.NotRetried(ha.VersionLabel(round.pulumi.Tool, round.pulumi.Version)),
		before:     0,
	})
	checkShortOutageApply(ctx, t, s, project, shortOutageCommand{
		run:        runs.apply,
		notRetried: report.NotRetried(ha.VersionLabel(round.tofu.Tool, round.tofu.Version)),
		before:     before,
	})

	checkOutageNoChanges(ctx, t, s, project)
}

// shortOutageCommand is one CLI run of the short outage: the run, the write
// kinds that its version does not retry, and the state versions of the
// workspace before the run (OpenTofu only).
type shortOutageCommand struct {
	run        *outageRun
	notRetried []ha.WriteKind
	before     int
}

// kindNames joins the names of write kinds for a message.
func kindNames(kinds []ha.WriteKind) string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}

	return strings.Join(names, ", ")
}

// checkShortOutageUp checks the pulumi up of the short outage. When each
// write kind of the version retries, the command must exit 0. When the
// version does not retry a write kind (a window of 0 s), the command must
// fail clean: a non-zero exit, a state that exports, no active update, and a
// next run of the same command that exits 0 with exactly one new update.
func checkShortOutageUp(
	ctx context.Context, t *testing.T, s *outageSetup, project outageProject, cmd shortOutageCommand,
) {
	t.Helper()

	run, notRetried := cmd.run, cmd.notRetried

	if run.err != nil {
		t.Fatalf("pulumi up: %v", run.err)
	}

	if len(notRetried) == 0 {
		if run.res.ExitCode != 0 {
			t.Errorf("pulumi up exits %d, want 0:\n%s%s", run.res.ExitCode, run.res.Stdout, run.res.Stderr)
		}

		return
	}

	if run.res.ExitCode == 0 {
		t.Errorf("pulumi up exits 0, want a failure: the version does not retry the write kinds %s",
			kindNames(notRetried))
	}

	checkOutageExport(ctx, t, project)

	if outageUpdateActive(ctx, t, s, project) {
		t.Errorf("stack %s has an active update after the failed pulumi up", project.stack)
		cancelOutageUpdate(ctx, t, s, project)
	}

	after := outageHistory(ctx, t, project)

	project.pulumi.runWith(ctx, t, project.pulumiEnv(), project.upArgs()...)

	if got, want := outageHistory(ctx, t, project), after+1; got != want {
		t.Errorf("%d updates of stack %s after the next pulumi up, want %d (%d after the failed run, plus 1)",
			got, project.stack, want, after)
	}
}

// checkShortOutageApply checks the tofu apply of the short outage. When the
// version retries the state version write, the command must exit 0 with
// exactly one new state version. When it does not retry it (a window of
// 0 s), the command must fail clean: a non-zero exit, no new state version,
// no lock, and a next run of the same command that exits 0 with exactly one
// new state version.
func checkShortOutageApply(
	ctx context.Context, t *testing.T, s *outageSetup, project outageProject, cmd shortOutageCommand,
) {
	t.Helper()

	run, notRetried, before := cmd.run, cmd.notRetried, cmd.before

	if run.err != nil {
		t.Fatalf("tofu apply: %v", run.err)
	}

	if len(notRetried) > 0 {
		if run.res.ExitCode == 0 {
			t.Errorf("tofu apply exits 0, want a failure: the version does not retry the write kinds %s",
				kindNames(notRetried))
		}

		if got := outageVersions(ctx, t, s.env, project.workspace); got != before {
			t.Errorf("%d state versions after the failed apply, want %d", got, before)
		}

		checkOutageUnlocked(ctx, t, s.env, project.workspace)

		project.tofu.runWith(ctx, t, project.tofuEnv(), outageApplyArgs()...)
	} else if run.res.ExitCode != 0 {
		t.Errorf("tofu apply exits %d, want 0:\n%s%s", run.res.ExitCode, run.res.Stdout, run.res.Stderr)
	}

	if got, want := outageVersions(ctx, t, s.env, project.workspace), before+1; got != want {
		t.Errorf("%d state versions after the short outage, want %d (%d before the apply, plus 1)",
			got, want, before)
	}
}
