//go:build e2e

package ha_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
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

// outageSetup holds the real installation that the probe drives.
type outageSetup struct {
	env       *e2e.Env
	cluster   *e2e.HACluster
	probe     *ha.DatabaseOutageProbe
	matrix    []ha.CLIVersion
	pulumi    e2e.Install
	tofu      e2e.Install
	reportKey map[string]string
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

	pulumiTool, tofuTool := e2e.Pulumi(), e2e.OpenTofu()
	pulumiInstall, err := fetcher.Install(ctx, pulumiTool, pulumiTool.Single())
	if err != nil {
		t.Fatalf("install the Pulumi CLI: %v", err)
	}

	tofuInstall, err := fetcher.Install(ctx, tofuTool, tofuTool.Single())
	if err != nil {
		t.Fatalf("install the OpenTofu CLI: %v", err)
	}

	matrix := make([]ha.CLIVersion, 0, len(pulumiTool.Versions())+len(tofuTool.Versions()))

	for _, version := range pulumiTool.Versions() {
		matrix = append(matrix, ha.CLIVersion{Tool: pulumiTool.Name, Version: version})
	}

	for _, version := range tofuTool.Versions() {
		matrix = append(matrix, ha.CLIVersion{Tool: tofuTool.Name, Version: version})
	}

	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, pulumiInstall, e2e.StorePostgres)
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
		pulumi:  pulumiInstall,
		tofu:    tofuInstall,
		reportKey: map[string]string{
			pulumiTool.Name: ha.VersionLabel(pulumiTool.Name, pulumiInstall.Version),
			tofuTool.Name:   ha.VersionLabel(tofuTool.Name, tofuInstall.Version),
		},
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

	c.output(ctx, t, args...)
}

// output runs one command that must exit 0 and returns its standard output.
func (c outageCLI) output(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()

	res, err := c.env.RunAs(ctx, c.install, c.dir, nil, args...)
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

func newOutageProject(ctx context.Context, t *testing.T, s *outageSetup, name string) outageProject {
	t.Helper()

	pulumiDir, tofuDir, markers := t.TempDir(), t.TempDir(), t.TempDir()
	project := outageProject{
		pulumi:       outageCLI{env: s.env, install: s.pulumi, dir: pulumiDir},
		tofu:         outageCLI{env: s.env, install: s.tofu, dir: tofuDir},
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

	return project
}

// outageRuns holds the two CLI runs that startBoth starts during the outage:
// the Pulumi run and the OpenTofu run.
type outageRuns struct {
	up    *outageRun
	apply *outageRun
}

func (p outageProject) startBoth(ctx context.Context, t *testing.T) outageRuns {
	t.Helper()

	return outageRuns{
		up: p.pulumi.start(ctx, t, []string{"E2E_HA_PULUMI_STARTED=" + p.pulumiMarker},
			"up", "--yes", "--non-interactive", "--stack", p.stack),
		apply: p.tofu.start(ctx, t, []string{"E2E_HA_TOFU_STARTED=" + p.tofuMarker},
			"apply", "-input=false", "-auto-approve", "-no-color"),
	}
}

func waitOutageMarkers(ctx context.Context, t *testing.T, paths ...string) {
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
			case <-time.After(outagePoll):
			}
		}
	}
}

// outageWriteRequest writes a state version through the proxy with an access token.
func outageWriteRequest(env *e2e.Env) ha.InstanceRequest {
	return ha.InstanceRequest{
		Method: http.MethodPost,
		Path:   "/api/v2/workspaces/ws-outage/state-versions",
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

func checkOutageUnlocked(ctx context.Context, t *testing.T, env *e2e.Env, workspace string) {
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

	if doc.Data.Attributes.Locked {
		t.Errorf("workspace %s stays locked after the outage", workspace)
	}
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

	status, body, err := s.env.APIStatus(ctx, http.MethodGet, "/api/stacks/"+project.stack, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read stack %s: %d, %v (%s)", project.stack, status, err, body)
	}

	if !strings.Contains(body, `"activeUpdate":"`) || strings.Contains(body, `"activeUpdate":""`) {
		return
	}

	project.pulumi.run(ctx, t, "cancel", "--yes", "--stack", project.stack)
}

func recordOutageRetries(t *testing.T, s *outageSetup, step string, report *ha.RetryReport, writesAt time.Time) {
	t.Helper()

	entries := s.env.Recorder.Entries()

	for i := range entries {
		entry := &entries[i]

		if entry.Step != step || entry.Status != http.StatusServiceUnavailable ||
			entry.Method == http.MethodGet || entry.At.Before(writesAt) {
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

		version, found := s.reportKey[name]
		if !found {
			continue
		}

		attempt := ha.WriteAttempt{
			Version: version,
			Method:  entry.Method,
			Path:    entry.Path,
			Status:  entry.Status,
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

// checkOutageStop stops the database, checks that a write cannot reach it and
// restores it, before any CLI run. A proxy that cannot stop the database ends
// the step with no CLI run.
func checkOutageStop(ctx context.Context, t *testing.T, s *outageSetup, write ha.InstanceRequest) {
	t.Helper()

	restored := false

	t.Cleanup(func() {
		if !restored {
			s.probe.Restore(context.WithoutCancel(ctx), t)
		}
	})

	s.probe.Stop(ctx, t, write, outageRetryAfter)
	s.probe.Restore(ctx, t)
	restored = true
}

func runLongOutage(t *testing.T, s *outageSetup) *ha.RetryReport {
	t.Helper()

	ctx := t.Context()
	project := newOutageProject(ctx, t, s, "haoutage")
	report := ha.NewRetryReport(s.matrix)
	before := outageVersions(ctx, t, s.env, project.workspace)
	idsBefore := outageInstanceIDs(s.cluster)

	s.env.Recorder.SetStep("long database outage")

	write := outageWriteRequest(s.env)
	checkOutageStop(ctx, t, s, write)

	runs := project.startBoth(ctx, t)
	waitOutageMarkers(ctx, t, project.pulumiMarker, project.tofuMarker)

	writesAt := time.Now().Add(outageWork * time.Second)
	restored := false

	t.Cleanup(func() {
		if !restored {
			s.probe.Restore(context.WithoutCancel(ctx), t)
		}
	})

	started := time.Now()
	s.probe.Stop(ctx, t, write, outageRetryAfter)

	if err := s.probe.WaitReady(ctx, http.StatusServiceUnavailable); err != nil {
		t.Fatalf("instances did not turn unready: %v", err)
	}

	if err := s.probe.CheckWrites(ctx, write, outageRetryAfter); err != nil {
		t.Fatalf("writes during the outage: %v", err)
	}

	cancelOutageUpdateDuringOutage(ctx, t, project)

	if remaining := outageLongWindow - time.Since(started); remaining > 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("outage wait ended: %v", ctx.Err())
		case <-time.After(remaining):
		}
	}

	s.probe.Restore(ctx, t)
	restored = true

	t.Logf("PostgreSQL stopped for %s", time.Since(started).Round(time.Second))

	checkOutageNoRestart(t, s.cluster, idsBefore)

	<-runs.up.done
	<-runs.apply.done

	checkOutageRun(t, "pulumi up", runs.up)
	checkOutageRun(t, "tofu apply", runs.apply)
	recordOutageRetries(t, s, "long database outage", report, writesAt)

	// A failed apply must leave no state version. A successful apply commits one.
	if runs.apply.res.ExitCode != 0 {
		if got := outageVersions(ctx, t, s.env, project.workspace); got != before {
			t.Errorf("%d state versions after the failed apply, want %d", got, before)
		}
	}

	checkOutageUnlocked(ctx, t, s.env, project.workspace)
	cancelOutageUpdate(ctx, t, s, project)

	project.pulumi.run(ctx, t, "up", "--yes", "--non-interactive", "--stack", project.stack)
	project.tofu.run(ctx, t, "apply", "-input=false", "-auto-approve", "-no-color")
	checkOutageNoChanges(ctx, t, s, project)

	t.Logf("retry report: %s", report)

	if _, err := s.probe.ShortWindow(report); err != nil {
		t.Fatalf("short window from the retry report: %v", err)
	}

	return report
}

// TestDatabaseOutageLongOutage proves that a 60 s stop during both CLIs ends
// with 503 on readiness and writes, a cancel and recovery, and no changes.
func TestDatabaseOutageLongOutage(t *testing.T) {
	t.Parallel()

	// Each test makes its own environment, with its own database proxy.
	s := newOutageSetup(t)
	runLongOutage(t, s)
}

// TestDatabaseOutageShortOutage measures the retry window in the long step and
// stops PostgreSQL for half of the smallest window in the same run.
func TestDatabaseOutageShortOutage(t *testing.T) {
	t.Parallel()

	// Each test makes its own environment, with its own database proxy.
	s := newOutageSetup(t)
	report := runLongOutage(t, s)

	window, err := s.probe.ShortWindow(report)
	if err != nil {
		t.Fatalf("short window: %v", err)
	}

	ctx := t.Context()
	project := newOutageProject(ctx, t, s, "haquick")
	before := outageVersions(ctx, t, s.env, project.workspace)

	s.env.Recorder.SetStep("short database outage")

	write := outageWriteRequest(s.env)
	checkOutageStop(ctx, t, s, write)

	runs := project.startBoth(ctx, t)
	waitOutageMarkers(ctx, t, project.pulumiMarker, project.tofuMarker)

	select {
	case <-ctx.Done():
		t.Fatalf("wait for the short outage ended: %v", ctx.Err())
	case <-time.After(outageWork*time.Second - outageShortLead):
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
	case <-time.After(window):
	}

	s.probe.Restore(ctx, t)
	restored = true

	t.Logf("PostgreSQL stopped for %s (smallest retry window %s)", window, report.Shortest().Round(time.Second))

	<-runs.up.done
	<-runs.apply.done

	for what, run := range map[string]*outageRun{"pulumi up": runs.up, "tofu apply": runs.apply} {
		if run.err != nil {
			t.Fatalf("%s: %v", what, run.err)
		}

		if run.res.ExitCode != 0 {
			t.Errorf("%s exits %d, want 0:\n%s%s", what, run.res.ExitCode, run.res.Stdout, run.res.Stderr)
		}
	}

	if got := outageVersions(ctx, t, s.env, project.workspace); got <= before {
		t.Errorf("%d state versions after the short outage, want above %d", got, before)
	}

	checkOutageNoChanges(ctx, t, s, project)
}
