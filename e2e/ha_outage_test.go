//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// haOutageLength is how long PostgreSQL stays stopped in the long outage.
	haOutageLength = 60 * time.Second
	// haOutageReady bounds the wait for each instance to turn unready after the
	// stop, and ready after the start.
	haOutageReady = 90 * time.Second
	// haRetryAfter is the wait that an API names in Retry-After when the
	// database does not answer.
	haRetryAfter = "5"
	// haShortWork is how long the resource of the short outage waits, in
	// seconds.
	haShortWork = 8
	// haShortLead is how long before the end of the resource the short outage
	// starts, so that the writes at the end of the resource meet it.
	haShortLead = 2 * time.Second
	// haShortMargin is the time that the short outage stays below the smallest
	// retry window of the two commands.
	haShortMargin = 6 * time.Second
	// haShortMin and haShortMax bound the length of the short outage.
	haShortMin = 2 * time.Second
	haShortMax = 10 * time.Second
	// haConsistencyWait bounds the wait for the checks of the state after the
	// outage.
	haConsistencyWait = 30 * time.Second
)

// haOutagePulumiProgram returns the Pulumi project of an outage step: the
// program of the kill test under a name of its own, with a resource that waits
// sleep seconds.
func haOutagePulumiProgram(name, sleep string) string {
	return `name: ` + name + `
runtime: yaml
resources:
  wait:
    type: command:local:Command
    properties:
      create: touch "$E2E_HA_PULUMI_STARTED" && sleep ` + sleep + `
`
}

// haOutageTofuConfig returns the OpenTofu configuration of an outage step: the
// one of the kill test with a workspace of its own and a resource that waits
// sleep seconds.
func haOutageTofuConfig(host, workspace, sleep string) string {
	config := strings.ReplaceAll(haTofuConfig(host), `name = "hakill"`, `name = "`+workspace+`"`)

	return strings.ReplaceAll(config, "sleep "+haWork, "sleep "+sleep)
}

// haRetryWindow is how long each command kept retrying before it failed, as
// the long outage measured it. A zero value means that the command did not
// fail in the outage.
type haRetryWindow struct {
	pulumi time.Duration
	tofu   time.Duration
}

// smallest returns the smallest window of a command that failed, or 0 when
// none failed.
func (w haRetryWindow) smallest() time.Duration {
	switch {
	case w.pulumi > 0 && w.tofu > 0:
		return min(w.pulumi, w.tofu)
	case w.pulumi > 0:
		return w.pulumi
	default:
		return w.tofu
	}
}

// haOutageProject is the pair of CLI projects of one outage step.
type haOutageProject struct {
	pulumi, tofu             haCLI
	stack, workspace         string
	pulumiMarker, tofuMarker string
}

// haNewOutageProject writes the projects of an outage step and prepares the
// stack and the workspace.
func haNewOutageProject(
	ctx context.Context, t *testing.T, env *e2e.Env, pair backupPair, name string, sleep string,
) haOutageProject {
	t.Helper()

	pulumiDir, tofuDir, markers := t.TempDir(), t.TempDir(), t.TempDir()

	p := haOutageProject{
		pulumi:       haCLI{env: env, install: pair.pulumi, dir: pulumiDir},
		tofu:         haCLI{env: env, install: pair.tofu, dir: tofuDir},
		stack:        e2e.Namespace + "/" + name + "/dev",
		workspace:    name,
		pulumiMarker: filepath.Join(markers, "pulumi-started"),
		tofuMarker:   filepath.Join(markers, "tofu-started"),
	}

	haWriteFile(t, filepath.Join(pulumiDir, "Pulumi.yaml"), haOutagePulumiProgram(name, sleep))
	haWriteFile(t, filepath.Join(tofuDir, "main.tf"), haOutageTofuConfig(env.Host(), name, sleep))

	p.pulumi.run(ctx, t, "plugin", "install", "resource", "command", haCommandPlugin)
	p.pulumi.run(ctx, t, "stack", "init", p.stack)
	p.tofu.run(ctx, t, "init", "-input=false", "-no-color")

	return p
}

// startBoth starts `pulumi up` and `tofu apply` in the background.
func (p haOutageProject) startBoth(ctx context.Context, t *testing.T) (up, apply *haRun) {
	t.Helper()

	up = p.pulumi.start(ctx, t, []string{"E2E_HA_PULUMI_STARTED=" + p.pulumiMarker},
		"up", "--yes", "--non-interactive", "--stack", p.stack)
	apply = p.tofu.start(ctx, t, []string{"E2E_HA_TOFU_STARTED=" + p.tofuMarker},
		"apply", "-input=false", "-auto-approve", "-no-color")

	return up, apply
}

// refusedWatch notes when the proxy first answered 503 to each CLI in a step.
type refusedWatch struct {
	mu    sync.Mutex
	first map[string]time.Time
}

// watchRefused polls the record of the proxy until the test ends and notes the
// first 503 that each CLI got in step.
func watchRefused(ctx context.Context, env *e2e.Env, step string) *refusedWatch {
	w := &refusedWatch{mu: sync.Mutex{}, first: map[string]time.Time{}}

	go func() {
		for ctx.Err() == nil {
			for _, entry := range env.Recorder.Entries() {
				if entry.Step != step || entry.Status != http.StatusServiceUnavailable {
					continue
				}

				tool := haToolOf(entry.UserAgent)

				w.mu.Lock()
				if _, seen := w.first[tool]; !seen {
					w.first[tool] = time.Now()
				}
				w.mu.Unlock()
			}

			select {
			case <-ctx.Done():
			case <-time.After(haPollEvery * 2):
			}
		}
	}()

	return w
}

// firstRefused returns when the CLI tool first got a 503, and false when it
// got none.
func (w *refusedWatch) firstRefused(tool string) (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	at, ok := w.first[tool]

	return at, ok
}

// haLongOutage is step 7a. It stops PostgreSQL for 60 s while `tofu apply` and
// `pulumi up` run. During the outage every instance must answer 503 on
// /health/ready, and 503 with Retry-After, not 500, on API writes. After the
// database returns, the instances turn ready with no restart. A command that
// fails in the outage must fail with a 503 message, and must leave no
// half-written state version and no lock. A run of the same command again
// must succeed, and the next preview and plan must show no changes. It
// returns how long each command kept retrying.
func haLongOutage(t *testing.T, env *e2e.Env, pair backupPair) haRetryWindow {
	t.Helper()

	const step = "7a database outage"

	ctx := t.Context()
	cluster := env.HA(t)
	project := haNewOutageProject(ctx, t, env, pair, "haoutage", haWork)

	env.Recorder.SetStep(step)

	watch := watchRefused(ctx, env, step)
	up, apply := project.startBoth(ctx, t)

	haWaitForFiles(ctx, t, project.pulumiMarker, project.tofuMarker)

	// The proxy of the test stops PostgreSQL for this environment only. The
	// shared PostgreSQL server keeps running. The cleanup starts it again when
	// the step fails in the outage.
	t.Cleanup(func() { env.StartDatabase(t) })

	outageStart := time.Now()

	env.StopDatabase(t)
	t.Logf("PostgreSQL stopped for %s", haOutageLength)

	haWaitReadiness(ctx, t, cluster, http.StatusServiceUnavailable)

	for i := range cluster.Size() {
		haCheckWritesRefused(ctx, t, env, cluster, i)
	}

	if remaining := haOutageLength - time.Since(outageStart); remaining > 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("the outage wait ended: %v", ctx.Err())
		case <-time.After(remaining):
		}
	}

	env.StartDatabase(t)
	t.Logf("PostgreSQL started after %s", time.Since(outageStart).Round(time.Second))

	// No instance restarts: the same processes turn ready.
	haWaitReadiness(ctx, t, cluster, http.StatusOK)

	<-up.done
	<-apply.done

	var window haRetryWindow

	window.pulumi = haOutageResult(t, "pulumi up", up, watch, e2e.ToolPulumi)
	window.tofu = haOutageResult(t, "tofu apply", apply, watch, e2e.ToolTofu)

	t.Logf("retry window measured through the proxy: pulumi %s, tofu %s (0 means the command did not fail)",
		window.pulumi.Round(time.Second), window.tofu.Round(time.Second))

	haCheckConsistent(ctx, t, env, project)

	// The same commands run again and succeed. Then nothing is left to change.
	project.pulumi.run(ctx, t, "up", "--yes", "--non-interactive", "--stack", project.stack)
	project.tofu.run(ctx, t, "apply", "-input=false", "-auto-approve", "-no-color")
	project.pulumi.run(ctx, t, "preview", "--expect-no-changes", "--non-interactive", "--stack", project.stack)
	project.tofu.run(ctx, t, "plan", "-detailed-exitcode", "-input=false", "-no-color")

	return window
}

// haOutageResult checks one command after the long outage. A command that
// failed must have failed with a 503 message. It returns how long the command
// retried before it failed, or 0 when it did not fail.
func haOutageResult(t *testing.T, what string, run *haRun, watch *refusedWatch, tool string) time.Duration {
	t.Helper()

	if run.err != nil {
		t.Fatalf("%s: %v", what, run.err)
	}

	if run.res.ExitCode == 0 {
		t.Logf("%s completed in %s: it retried through the outage", what, run.res.Duration.Round(time.Second))

		return 0
	}

	out := run.res.Stdout + run.res.Stderr
	if !strings.Contains(out, "503") && !strings.Contains(strings.ToLower(out), "service unavailable") {
		t.Errorf("%s failed with status %d and no 503 message:\n%s", what, run.res.ExitCode, out)
	}

	first, ok := watch.firstRefused(tool)
	if !ok {
		t.Errorf("%s failed, but the proxy answered it no 503", what)

		return 0
	}

	return run.ended.Sub(first)
}

// haCheckConsistent checks the state after the long outage: no state version
// of the workspace is pending, no lock holds the workspace, and the stack has
// no running update.
func haCheckConsistent(ctx context.Context, t *testing.T, env *e2e.Env, project haOutageProject) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haConsistencyWait)
	defer cancel()

	conn := haConnect(ctx, t, env)

	var pending int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM state_versions sv JOIN workspaces w ON w.id = sv.workspace_id `+
			`WHERE w.name = $1 AND sv.status = 'pending'`, project.workspace).Scan(&pending); err != nil {
		t.Fatalf("count pending state versions: %v", err)
	}

	if pending != 0 {
		t.Errorf("%d state versions of %s stay pending after the outage, want 0", pending, project.workspace)
	}

	var lockID string
	if err := conn.QueryRow(ctx, `SELECT lock_id FROM workspaces WHERE name = $1`, project.workspace).Scan(&lockID); err != nil {
		t.Fatalf("read the lock of %s: %v", project.workspace, err)
	}

	if lockID != "" {
		t.Errorf("workspace %s stays locked (lock %s) after the outage, want unlocked", project.workspace, lockID)
	}

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/api/stacks/"+project.stack, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read stack %s answers %d: %v (%s)", project.stack, status, err, body)
	}

	if strings.Contains(body, `"activeUpdate":"`) && !strings.Contains(body, `"activeUpdate":""`) {
		t.Errorf("stack %s keeps a running update after the outage: %s", project.stack, body)
	}
}

// haShortOutage is step 7b. It stops PostgreSQL for less than the smallest
// retry window that step 7a measured, while `tofu apply` and `pulumi up` run.
// Both commands must complete with exit 0, and the next preview and plan must
// show no changes.
func haShortOutage(t *testing.T, env *e2e.Env, pair backupPair, window haRetryWindow) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)
	sleep := strconv.Itoa(haShortWork)
	project := haNewOutageProject(ctx, t, env, pair, "haquick", sleep)

	length := haShortOutageLength(window)

	env.Recorder.SetStep("7b short database outage")

	up, apply := project.startBoth(ctx, t)

	haWaitForFiles(ctx, t, project.pulumiMarker, project.tofuMarker)

	t.Cleanup(func() { env.StartDatabase(t) })

	// The writes come at the end of the resource, so the outage starts just
	// before it.
	select {
	case <-ctx.Done():
		t.Fatalf("the wait for the outage ended: %v", ctx.Err())
	case <-time.After(haShortWork*time.Second - haShortLead):
	}

	env.StopDatabase(t)
	t.Logf("PostgreSQL stopped for %s (smallest measured retry window %s)", length, window.smallest().Round(time.Second))

	select {
	case <-ctx.Done():
		t.Fatalf("the outage wait ended: %v", ctx.Err())
	case <-time.After(length):
	}

	env.StartDatabase(t)

	haWaitReadiness(ctx, t, cluster, http.StatusOK)

	up.wait(t, "pulumi up")
	apply.wait(t, "tofu apply")

	project.pulumi.run(ctx, t, "preview", "--expect-no-changes", "--non-interactive", "--stack", project.stack)
	project.tofu.run(ctx, t, "plan", "-detailed-exitcode", "-input=false", "-no-color")
}

// haShortOutageLength returns the length of the short outage: the smallest
// measured retry window less a margin, between the bounds. When no command
// failed in the long outage, it returns the upper bound.
func haShortOutageLength(window haRetryWindow) time.Duration {
	smallest := window.smallest()
	if smallest == 0 {
		return haShortMax
	}

	return min(max(smallest-haShortMargin, haShortMin), haShortMax)
}

// haWaitReadiness waits until every instance answers want on /health/ready.
func haWaitReadiness(ctx context.Context, t *testing.T, cluster *e2e.HACluster, want int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haOutageReady)
	defer cancel()

	for i := range cluster.Size() {
		for {
			resp, err := cluster.Send(ctx, i, http.MethodGet, "/health/ready", nil, "")
			if err == nil && resp.Status == want {
				break
			}

			select {
			case <-ctx.Done():
				t.Fatalf("instance %d: /health/ready did not answer %d (last: %+v, %v)", i, want, resp, err)
			case <-time.After(haPollEvery * 4):
			}
		}
	}
}

// haCheckWritesRefused sends API writes of each API to instance i and checks
// that each answers 503 with Retry-After, never 500.
func haCheckWritesRefused(ctx context.Context, t *testing.T, env *e2e.Env, cluster *e2e.HACluster, i int) {
	t.Helper()

	pulumiHeader := http.Header{"Authorization": {"token " + env.TokenA()}}
	jsonAPIHeader := http.Header{
		"Authorization": {"Bearer " + env.TokenA()},
		"Content-Type":  {"application/vnd.api+json"},
	}
	bearerHeader := http.Header{
		"Authorization": {"Bearer " + env.TokenA()},
		"Content-Type":  {"application/json"},
	}

	writes := []struct {
		name   string
		method string
		path   string
		header http.Header
		body   string
	}{
		{
			"pulumi create stack", http.MethodPost,
			"/api/stacks/" + e2e.Namespace + "/" + e2e.Project, pulumiHeader, `{"stackName":"outage` + strconv.Itoa(i) + `"}`,
		},
		{
			"tfe create workspace", http.MethodPost,
			"/api/v2/organizations/" + e2e.Namespace + "/workspaces", jsonAPIHeader,
			`{"data":{"type":"workspaces","attributes":{"name":"outage` + strconv.Itoa(i) + `"}}}`,
		},
		{
			"tfe create state version", http.MethodPost,
			"/api/v2/workspaces/ws-outage/state-versions", jsonAPIHeader,
			`{"data":{"type":"state-versions","attributes":{"serial":1,"md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`,
		},
		{
			"installation create namespace", http.MethodPost,
			"/api/server/namespaces", bearerHeader, `{"name":"outage` + strconv.Itoa(i) + `"}`,
		},
	}

	for _, w := range writes {
		resp, err := cluster.Send(ctx, i, w.method, w.path, w.header, w.body)
		if err != nil {
			t.Errorf("instance %d: %s: %v", i, w.name, err)

			continue
		}

		if resp.Status != http.StatusServiceUnavailable {
			t.Errorf("instance %d: %s answers %d, want 503: %s", i, w.name, resp.Status, resp.Body)

			continue
		}

		if got := resp.Header.Get("Retry-After"); got != haRetryAfter {
			t.Errorf("instance %d: %s: Retry-After = %q, want %s", i, w.name, got, haRetryAfter)
		}
	}
}
