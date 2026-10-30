//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// haOldBuild is the version that the old binaries of the rolling deploy
	// carry. It is a release version, because a dev build ignores the minimum.
	haOldBuild = "v1.0.0"
	// haNewBuild is the version of the newer build. The faked contract
	// migration raises the minimum of the schema to it.
	haNewBuild = "v1.1.0"
	// haContractVersion is the number of the faked contract migration. It is
	// above every migration that the binary embeds.
	haContractVersion = 9999
	// haContractName is the name of the faked contract migration.
	haContractName = "9999_e2e_contract.sql"
	// haRollingStack is the Pulumi stack of the rolling deploy loop.
	haRollingStack = "harolling"
	// haRollingSettle bounds the wait for the old instances to leave the pool
	// after the minimum went up.
	haRollingSettle = 30 * time.Second
	// haRollingMinRounds is the least number of rounds that each tool must
	// finish while the deploy runs.
	haRollingMinRounds = 2
)

// haRollingPulumiProgram is the project of the loop. It has no resource, so
// each `pulumi up` is one update that writes a checkpoint and a history item.
const haRollingPulumiProgram = `name: harolling
runtime: yaml
outputs:
  stack: ${pulumi.stack}
`

// haRollingTofuConfig is the configuration of the loop. Each round passes
// another value, so each `tofu apply` writes a new state version.
func haRollingTofuConfig(host string) string {
	return `terraform {
  cloud {
    hostname     = "` + host + `"
    organization = "` + e2e.Namespace + `"
    workspaces {
      name = "harolling"
    }
  }
}

variable "round" {
  type = string
}

resource "terraform_data" "round" {
  input = var.round
}
`
}

// haLoop runs a CLI command again and again in the background and records
// each failure, until stop is called.
type haLoop struct {
	stopped chan struct{}
	wg      sync.WaitGroup

	mu       sync.Mutex
	rounds   map[string]int
	failures []string
}

// newHaLoop returns a loop that has not started.
func newHaLoop() *haLoop {
	return &haLoop{
		stopped:  make(chan struct{}),
		wg:       sync.WaitGroup{},
		mu:       sync.Mutex{},
		rounds:   map[string]int{},
		failures: nil,
	}
}

// start runs round for tool until the loop stops. A round that has begun
// ends before the loop returns, so no command is cut short.
func (l *haLoop) start(tool string, round func(n int) (e2e.Result, error)) {
	l.wg.Go(func() {
		for n := 1; ; n++ {
			select {
			case <-l.stopped:
				return
			default:
			}

			res, err := round(n)

			l.mu.Lock()

			switch {
			case err != nil:
				l.failures = append(l.failures, fmt.Sprintf("%s round %d: %v", tool, n, err))
			case res.ExitCode != 0:
				l.failures = append(l.failures, fmt.Sprintf("%s round %d exits with %d:\n%s%s",
					tool, n, res.ExitCode, res.Stdout, res.Stderr))
			default:
				l.rounds[tool]++
			}

			l.mu.Unlock()
		}
	})
}

// stop ends the loop after the rounds in progress and returns the finished
// rounds of each tool and the failures.
func (l *haLoop) stop() (map[string]int, []string) {
	close(l.stopped)
	l.wg.Wait()

	l.mu.Lock()
	defer l.mu.Unlock()

	return maps.Clone(l.rounds), slices.Clone(l.failures)
}

// snapshot returns the finished rounds of each tool now.
func (l *haLoop) snapshot() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return maps.Clone(l.rounds)
}

// haRoll replaces instance i with a start of the binary bin, and waits until
// the proxy sends requests to every instance that runs.
func haRoll(ctx context.Context, t *testing.T, cluster *e2e.HACluster, i int, bin string) {
	t.Helper()

	if err := cluster.Stop(i); err != nil {
		t.Fatalf("stop instance %d: %v", i, err)
	}

	cluster.SetBinary(bin)

	if err := cluster.Start(ctx, i); err != nil {
		t.Fatalf("start instance %d with %s: %v", i, bin, err)
	}
}

// haFakeContract writes the row of a contract migration to the schema: it
// raises the minimum binary version to haNewBuild, as the migration of a newer
// release does. The real migrations of the binary set no minimum above the
// binary itself, and the check that a contract migration does not run while
// an older instance row exists belongs to the migration runner, so the test
// writes the row itself. The row lives in this test only: the binary ships no
// such migration. The cleanup removes the row.
func haFakeContract(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	conn := haConnect(ctx, t, env)

	_, err := conn.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, min_binary_version) VALUES ($1, $2, $3)`,
		haContractVersion, haContractName, haNewBuild)
	if err != nil {
		t.Fatalf("write the faked contract migration: %v", err)
	}

	t.Cleanup(func() {
		_, err := conn.Exec(context.WithoutCancel(ctx),
			`DELETE FROM schema_migrations WHERE version = $1`, haContractVersion)
		if err != nil {
			t.Errorf("remove the faked contract migration: %v", err)
		}
	})
}

// haRollingDeploy replaces each instance with a binary of a newer build, one
// at a time, while a loop of `tofu apply` and `pulumi up` runs. No command may
// fail. After the first new instance raised the minimum, each old instance
// must leave the pool, answer 503 on a write with the text that names both
// versions, and stay out of the pool until it is replaced.
func haRollingDeploy(t *testing.T, env *e2e.Env, pair backupPair) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)

	original := cluster.Binary()
	oldBin := e2e.BuildBinaryWithVersion(ctx, t, t.TempDir(), haOldBuild)
	newBin := e2e.BuildBinaryWithVersion(ctx, t, t.TempDir(), haNewBuild)

	pulumiDir, tofuDir := t.TempDir(), t.TempDir()

	haWriteFile(t, filepath.Join(pulumiDir, "Pulumi.yaml"), haRollingPulumiProgram)
	haWriteFile(t, filepath.Join(tofuDir, "main.tf"), haRollingTofuConfig(env.Host()))

	pulumi := haCLI{env: env, install: pair.pulumi, dir: pulumiDir}
	tofu := haCLI{env: env, install: pair.tofu, dir: tofuDir}
	stack := e2e.Namespace + "/" + haRollingStack + "/dev"

	pulumi.run(ctx, t, "stack", "init", stack)
	tofu.run(ctx, t, "init", "-input=false", "-no-color")

	// Every instance runs the old build first. A binary of a release version
	// checks the minimum; the dev build of the suite does not.
	for i := range cluster.Size() {
		haRoll(ctx, t, cluster, i, oldBin)
	}

	if err := cluster.WaitHealthy(ctx); err != nil {
		t.Fatalf("%v", err)
	}

	// The binaries of the step live in its temporary directories, which go
	// away with the step. A later step starts instances with the binary of
	// the suite, so the cleanup puts it back. A running instance keeps its
	// process, so the pool stays on the newer build until a step restarts it.
	// The cleanup also starts an instance that a failed step left down.
	t.Cleanup(func() {
		cluster.SetBinary(newBin)

		for i := range cluster.Size() {
			if cluster.Alive(i) {
				continue
			}

			if err := cluster.Start(context.WithoutCancel(ctx), i); err != nil {
				t.Logf("start instance %d again: %v", i, err)
			}
		}

		cluster.SetBinary(original)
	})

	env.Recorder.SetStep("5 rolling deploy")

	loop := newHaLoop()
	loopDone := false

	t.Cleanup(func() {
		if !loopDone {
			loop.stop()
		}
	})

	haStartLoop(ctx, loop, pulumi, tofu, stack)

	// The first instance gets the newer build, and the schema minimum goes up.
	haRoll(ctx, t, cluster, 0, newBin)
	haFakeContract(ctx, t, env)

	haCheckOldInstances(ctx, t, env, cluster, []int{1, 2})

	for _, i := range []int{1, 2} {
		haRoll(ctx, t, cluster, i, newBin)
	}

	if err := cluster.WaitHealthy(ctx); err != nil {
		t.Fatalf("%v", err)
	}

	// The loop runs on the full pool of newer instances for a moment, so the
	// last rounds do not end on a half-replaced pool.
	before := loop.snapshot()

	haWaitRounds(ctx, t, loop, before)

	loopDone = true

	haCheckLoop(t, loop)
}

// haStartLoop starts the two commands of the loop: `pulumi up` and `tofu
// apply`, each in its own goroutine.
func haStartLoop(ctx context.Context, loop *haLoop, pulumi, tofu haCLI, stack string) {
	loop.start(e2e.ToolPulumi, func(int) (e2e.Result, error) {
		return pulumi.env.RunAs(ctx, pulumi.install, pulumi.dir, nil,
			"up", "--yes", "--non-interactive", "--stack", stack)
	})
	loop.start(e2e.ToolTofu, func(n int) (e2e.Result, error) {
		return tofu.env.RunAs(ctx, tofu.install, tofu.dir, nil,
			"apply", "-input=false", "-auto-approve", "-no-color", "-var", fmt.Sprintf("round=%d", n))
	})
}

// haCheckLoop ends the loop and fails the test for each command that failed
// and for a tool that finished too few rounds.
func haCheckLoop(t *testing.T, loop *haLoop) {
	t.Helper()

	rounds, failures := loop.stop()

	t.Logf("rounds finished during the rolling deploy: %v", rounds)

	for _, failure := range failures {
		t.Errorf("a command of the loop failed during the rolling deploy: %s", failure)
	}

	for _, tool := range []string{e2e.ToolPulumi, e2e.ToolTofu} {
		if rounds[tool] < haRollingMinRounds {
			t.Errorf("%s finished %d rounds, want at least %d", tool, rounds[tool], haRollingMinRounds)
		}
	}
}

// haWaitRounds waits until each tool has finished one more round than before.
func haWaitRounds(ctx context.Context, t *testing.T, loop *haLoop, before map[string]int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haRollingSettle*2)
	defer cancel()

	for {
		now := loop.snapshot()
		if now[e2e.ToolPulumi] > before[e2e.ToolPulumi] && now[e2e.ToolTofu] > before[e2e.ToolTofu] {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("a round did not finish on the newer instances: %v", ctx.Err())
		case <-time.After(haPollEvery):
		}
	}
}

// haCheckOldInstances checks the old instances after the minimum went up:
// each leaves the pool, answers 503 to a write with the text that names both
// versions, and answers 503 on the readiness probe. The newer instance still
// takes the same write.
func haCheckOldInstances(ctx context.Context, t *testing.T, env *e2e.Env, cluster *e2e.HACluster, old []int) {
	t.Helper()

	for _, i := range old {
		haWaitOutOfPool(ctx, t, cluster, i)
		haCheckRefusal(ctx, t, env, i)
	}

	path := "/api/stacks/" + e2e.Namespace + "/" + e2e.Project

	status, body, err := env.InstanceRequest(ctx, 0, http.MethodPost, path, `{"stackName":"rolling-new"}`)
	if err != nil {
		t.Fatalf("write to the newer instance: %v", err)
	}

	if status != http.StatusOK {
		t.Errorf("write to the newer instance answers %d, want 200:\n%s", status, body)
	}
}

// haWaitOutOfPool waits until the proxy no longer sends requests to instance i.
func haWaitOutOfPool(ctx context.Context, t *testing.T, cluster *e2e.HACluster, i int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haRollingSettle)
	defer cancel()

	for cluster.Healthy(i) {
		select {
		case <-ctx.Done():
			t.Fatalf("old instance %d is still in the pool %s after the minimum went up", i, haRollingSettle)
		case <-time.After(haPollEvery):
		}
	}
}

// haCheckRefusal checks that old instance i refuses a write with 503 and the
// text that names both versions, and answers 503 on the readiness probe.
func haCheckRefusal(ctx context.Context, t *testing.T, env *e2e.Env, i int) {
	t.Helper()

	path := "/api/stacks/" + e2e.Namespace + "/" + e2e.Project
	payload := fmt.Sprintf(`{"stackName":"rolling-old-%d"}`, i)

	status, body, err := env.InstanceRequest(ctx, i, http.MethodPost, path, payload)
	if err != nil {
		t.Fatalf("write to old instance %d: %v", i, err)
	}

	if status != http.StatusServiceUnavailable {
		t.Errorf("write to old instance %d answers %d, want 503:\n%s", i, status, body)
	}

	for _, want := range []string{haOldBuild, haNewBuild, "minimum binary version of the database schema"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal of old instance %d lacks %q:\n%s", i, want, body)
		}
	}

	status, body, err = env.InstanceRequest(ctx, i, http.MethodGet, "/health/ready", "")
	if err != nil {
		t.Fatalf("probe old instance %d: %v", i, err)
	}

	if status != http.StatusServiceUnavailable {
		t.Errorf("readiness of old instance %d answers %d, want 503:\n%s", i, status, body)
	}
}

// haMixedVersionMatrix names the part of the step that needs a release tag.
// The mixed-version matrix runs the binary of each of the last 2 tags next to
// the current one (make mixed-version-test). With no tag it has nothing to
// run, and the skip says so.
func haMixedVersionMatrix(t *testing.T) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "tag", "--merged", "HEAD", "--list", "v*")
	cmd.Dir = e2e.GitTopLevel(t.Context(), t)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list the v* tags: %v", err)
	}

	if strings.TrimSpace(string(out)) == "" {
		t.Skip("no v* tag is reachable from HEAD, so make mixed-version-test has no release binary to run; " +
			"it runs over the last 2 tags once a tag exists")
	}

	t.Skip("make mixed-version-test runs the last 2 tags against the current binary")
}
