//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// haOutageLength is how long PostgreSQL stays stopped.
	haOutageLength = 60 * time.Second
	// haOutageReady bounds the wait for each instance to turn unready after the
	// stop, and ready after the start.
	haOutageReady = 90 * time.Second
	// haRetryAfter is the wait that an API names in Retry-After when the
	// database does not answer.
	haRetryAfter = "5"
)

// haOutagePulumiProgram is the project of the outage test: the program of the
// kill test under a name of its own, so its resource runs again.
const haOutagePulumiProgram = `name: haoutage
runtime: yaml
resources:
  wait:
    type: command:local:Command
    properties:
      create: touch "$E2E_HA_PULUMI_STARTED" && sleep ` + haWork + `
`

// haOutageTofuConfig is the configuration of the outage test: the one of the
// kill test with a workspace of its own.
func haOutageTofuConfig(host string) string {
	return strings.ReplaceAll(haTofuConfig(host), `name = "hakill"`, `name = "haoutage"`)
}

// haDatabaseOutage stops PostgreSQL for 60 s while `tofu apply` and `pulumi
// up` run. During the outage every instance must answer 503 on
// /health/ready, and 503 with Retry-After, not 500, on API writes. After the
// database returns, the instances turn ready with no restart and both
// commands complete.
func haDatabaseOutage(t *testing.T, env *e2e.Env, pair backupPair) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)
	stack := e2e.Namespace + "/haoutage/dev"

	pulumiDir, tofuDir := t.TempDir(), t.TempDir()
	markers := t.TempDir()
	pulumiMarker := filepath.Join(markers, "pulumi-started")
	tofuMarker := filepath.Join(markers, "tofu-started")

	haWriteFile(t, filepath.Join(pulumiDir, "Pulumi.yaml"), haOutagePulumiProgram)
	haWriteFile(t, filepath.Join(tofuDir, "main.tf"), haOutageTofuConfig(env.Host()))

	pulumi := haCLI{env: env, install: pair.pulumi, dir: pulumiDir}
	tofu := haCLI{env: env, install: pair.tofu, dir: tofuDir}

	pulumi.run(ctx, t, "plugin", "install", "resource", "command", haCommandPlugin)
	pulumi.run(ctx, t, "stack", "init", stack)
	tofu.run(ctx, t, "init", "-input=false", "-no-color")

	env.Recorder.SetStep("7 database outage")

	up := pulumi.start(ctx, t, []string{"E2E_HA_PULUMI_STARTED=" + pulumiMarker},
		"up", "--yes", "--non-interactive", "--stack", stack)
	apply := tofu.start(ctx, t, []string{"E2E_HA_TOFU_STARTED=" + tofuMarker},
		"apply", "-input=false", "-auto-approve", "-no-color")

	haWaitForFiles(ctx, t, pulumiMarker, tofuMarker)

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

	// Wait for both commands first, so that a failure of one still shows what
	// the other did.
	<-up.done
	<-apply.done

	haLogRetries(t, env)
	up.wait(t, "pulumi up")
	apply.wait(t, "tofu apply")

	pulumi.run(ctx, t, "preview", "--expect-no-changes", "--non-interactive", "--stack", stack)
	tofu.run(ctx, t, "plan", "-detailed-exitcode", "-input=false", "-no-color")
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

// haLogRetries logs, for each CLI, how many requests of the step the proxy
// answered with 503, so a command that gave up shows how long it retried.
func haLogRetries(t *testing.T, env *e2e.Env) {
	t.Helper()

	refused := map[string]int{}

	for _, entry := range env.Recorder.Entries() {
		if entry.Step == "7 database outage" && entry.Status == http.StatusServiceUnavailable {
			refused[haToolOf(entry.UserAgent)]++
		}
	}

	t.Logf("requests answered 503 during the step: pulumi %d, tofu %d", refused[e2e.ToolPulumi], refused[e2e.ToolTofu])
}
