//go:build e2e

package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// haWork is how long the resource of the kill test waits, in seconds.
	haWork = "30"
	// haCommandPlugin is the version of the Pulumi command provider that
	// runs the waiting resource. Pulumi downloads it once for each test.
	haCommandPlugin = "v1.1.0"
	// haMarkerWait bounds the wait for both commands to reach their resource.
	haMarkerWait = 5 * time.Minute
	// haMarkerPoll sets how often the test looks for the marker files.
	haMarkerPoll = 100 * time.Millisecond
	// haLease is the lease of a durable operation. The server fixes it.
	haLease = 30 * time.Second
	// haBackupGrace is the time after the lease within which a killed backup
	// must end.
	haBackupGrace = 60 * time.Second
	// haBackupMBEnv names the variable that sets the megabytes of padded
	// checkpoint data that the backup step writes before it starts the backup.
	haBackupMBEnv = "E2E_HA_BACKUP_MB"
	// haBackupMBDefault is the size of the exit test. A pull request profile
	// sets a smaller value.
	haBackupMBDefault = 500
	// haPadBytes is the size of the padding in one checkpoint.
	haPadBytes = 16 << 20
	// haOperationEnd bounds the wait for the operation of a finished backup to
	// end.
	haOperationEnd = 30 * time.Second
	// haPollEvery sets how often the backup step polls.
	haPollEvery = 50 * time.Millisecond
	// haFileMode is the mode of the project files.
	haFileMode = 0o600
)

// haPulumiProgram is the project of the kill test. Its one resource runs a
// shell command from the command provider: it writes the marker file that
// the test waits for, then sleeps. The config secret is read back after the
// kill, so the secrets provider is proven too.
const haPulumiProgram = `name: hakill
runtime: yaml
resources:
  wait:
    type: command:local:Command
    properties:
      create: touch "$E2E_HA_PULUMI_STARTED" && sleep ` + haWork + `
`

// haTofuConfig is the configuration of the kill test. The built-in
// terraform_data resource runs a provisioner that writes the marker file that
// the test waits for, then sleeps, so no provider is downloaded.
func haTofuConfig(host string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = "hakill"
    }
  }
}

resource "terraform_data" "wait" {
  provisioner "local-exec" {
    command = "touch \"$E2E_HA_TOFU_STARTED\" && sleep ` + haWork + `"
  }
}

output "greeting" {
  value = "hello"
}
`
}

// haCLI is one CLI of the environment with the directory that it runs in.
type haCLI struct {
	env     *e2e.Env
	install e2e.Install
	dir     string
}

// haRun is one CLI command that runs in the background.
type haRun struct {
	done chan struct{}
	// ended is the time the command ended; valid after done closes.
	ended time.Time
	res   e2e.Result
	err   error
}

// start runs the command in the background. The test waits for it with
// haRun.wait. extra holds NAME=value pairs for this command only.
func (c haCLI) start(ctx context.Context, t *testing.T, extra []string, args ...string) *haRun {
	t.Helper()

	run := new(haRun)
	run.done = make(chan struct{})

	go func() {
		defer close(run.done)

		run.res, run.err = c.env.RunAs(ctx, c.install, c.dir, extra, args...)
		run.ended = time.Now()
	}()

	// A test that ends early cancels its context, which ends the command.
	t.Cleanup(func() { <-run.done })

	return run
}

// run runs the command to its end and fails the test unless it exits with 0.
// It returns the result.
func (c haCLI) run(ctx context.Context, t *testing.T, args ...string) e2e.Result {
	t.Helper()

	return c.runWith(ctx, t, nil, args...)
}

// runWith is run with NAME=value pairs for this command only.
func (c haCLI) runWith(ctx context.Context, t *testing.T, extra []string, args ...string) e2e.Result {
	t.Helper()

	res, err := c.env.RunAs(ctx, c.install, c.dir, extra, args...)
	if err != nil {
		t.Fatalf("%s %s: %v", c.install.Tool, strings.Join(args, " "), err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("%s %s exits with status %d, want 0:\n%s%s",
			c.install.Tool, strings.Join(args, " "), res.ExitCode, res.Stdout, res.Stderr)
	}

	return res
}

// wait waits for the command and fails the test unless it exits with 0.
func (r *haRun) wait(t *testing.T, what string) {
	t.Helper()

	<-r.done

	if r.err != nil {
		t.Fatalf("%s: %v", what, r.err)
	}

	if r.res.ExitCode != 0 {
		t.Fatalf("%s exits with status %d, want 0:\n%s%s", what, r.res.ExitCode, r.res.Stdout, r.res.Stderr)
	}

	t.Logf("%s took %s", what, r.res.Duration.Round(time.Millisecond))
}

// haWaitForFiles waits until every path exists.
func haWaitForFiles(ctx context.Context, t *testing.T, paths ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haMarkerWait)
	defer cancel()

	for _, path := range paths {
		for {
			if _, err := os.Stat(path); err == nil {
				break
			}

			select {
			case <-ctx.Done():
				t.Fatalf("the file %s did not appear: %v", path, ctx.Err())
			case <-time.After(haMarkerPoll):
			}
		}
	}
}

// haLastBackend returns the instance that served the last request of tool,
// from the record of the proxy, or 0 when the record holds none.
func haLastBackend(env *e2e.Env, tool string) int {
	entries := env.Recorder.Entries()

	for i := range slices.Backward(entries) {
		if haToolOf(entries[i].UserAgent) != tool {
			continue
		}

		if n, ok := strings.CutPrefix(entries[i].Backend, "i"); ok {
			if index, err := strconv.Atoi(n); err == nil {
				return index
			}
		}
	}

	return 0
}

// haWriteFile writes a project file of the kill test.
func haWriteFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), haFileMode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// haKillDuringWork starts `pulumi up` and `tofu apply`, each with a resource
// that waits 30 s. It sends SIGKILL to the instance that served the last
// request of the Pulumi CLI while both run. Both commands must end with exit
// 0. The next `pulumi preview` and `tofu plan` must show no changes, and the
// config secret must decrypt.
func haKillDuringWork(t *testing.T, env *e2e.Env, pair backupPair) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)
	stack := e2e.Namespace + "/hakill/dev"

	pulumiDir, tofuDir := t.TempDir(), t.TempDir()
	markers := t.TempDir()
	pulumiMarker := filepath.Join(markers, "pulumi-started")
	tofuMarker := filepath.Join(markers, "tofu-started")

	haWriteFile(t, filepath.Join(pulumiDir, "Pulumi.yaml"), haPulumiProgram)
	haWriteFile(t, filepath.Join(tofuDir, "main.tf"), haTofuConfig(env.Host()))

	pulumi := haCLI{env: env, install: pair.pulumi, dir: pulumiDir}
	tofu := haCLI{env: env, install: pair.tofu, dir: tofuDir}

	pulumi.run(ctx, t, "plugin", "install", "resource", "command", haCommandPlugin)
	pulumi.run(ctx, t, "stack", "init", stack)
	pulumi.run(ctx, t, "config", "set", "--secret", "--stack", stack, "apiKey", "s3cr3t")
	tofu.run(ctx, t, "init", "-input=false", "-no-color")

	env.Recorder.SetStep("2 kill during work")

	up := pulumi.start(ctx, t, []string{"E2E_HA_PULUMI_STARTED=" + pulumiMarker},
		"up", "--yes", "--non-interactive", "--stack", stack)
	apply := tofu.start(ctx, t, []string{"E2E_HA_TOFU_STARTED=" + tofuMarker},
		"apply", "-input=false", "-auto-approve", "-no-color")

	// Both commands are in their resource, so each holds its lock and its
	// lease, and neither sends a request until the wait ends.
	haWaitForFiles(ctx, t, pulumiMarker, tofuMarker)

	victim := haLastBackend(env, e2e.ToolPulumi)

	t.Logf("SIGKILL to instance %d while both commands wait", victim)
	haRestartOnCleanup(ctx, t, cluster, victim)

	if err := cluster.Kill(victim); err != nil {
		t.Fatalf("kill instance %d: %v", victim, err)
	}

	up.wait(t, "pulumi up")
	apply.wait(t, "tofu apply")

	preview := pulumi.run(ctx, t, "preview", "--expect-no-changes", "--non-interactive", "--stack", stack)
	t.Logf("pulumi preview: %s", strings.TrimSpace(preview.Stdout))

	secret := pulumi.run(ctx, t, "config", "get", "--stack", stack, "apiKey")
	if strings.TrimSpace(secret.Stdout) != "s3cr3t" {
		t.Fatalf("the config secret reads %q after the kill, want s3cr3t", strings.TrimSpace(secret.Stdout))
	}

	plan := tofu.run(ctx, t, "plan", "-detailed-exitcode", "-input=false", "-no-color")
	t.Logf("tofu plan: %s", strings.TrimSpace(plan.Stdout))

	// The instance joins the pool again, so the next steps run on all of them.
	if err := cluster.Start(ctx, victim); err != nil {
		t.Fatalf("start instance %d again: %v", victim, err)
	}

	if err := cluster.WaitHealthy(ctx); err != nil {
		t.Fatalf("%v", err)
	}
}

// haRestartOnCleanup starts instance i again when the step ends with the
// instance down, so a failed step does not leave the pool short for the steps
// after it.
func haRestartOnCleanup(ctx context.Context, t *testing.T, cluster *e2e.HACluster, i int) {
	t.Helper()

	t.Cleanup(func() {
		if cluster.Alive(i) {
			return
		}

		if err := cluster.Start(context.WithoutCancel(ctx), i); err != nil {
			t.Logf("start instance %d again: %v", i, err)
		}
	})
}

// haBackupMB returns the megabytes of padding for the backup step.
func haBackupMB(t *testing.T) int {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(haBackupMBEnv))
	if raw == "" {
		return haBackupMBDefault
	}

	mb, err := strconv.Atoi(raw)
	if err != nil || mb < 1 {
		t.Fatalf("%s = %q, want a whole number of megabytes above 0", haBackupMBEnv, raw)
	}

	return mb
}

// haPad returns n deterministic high-entropy letters for pad number index.
func haPad(index, n int) string {
	letters := make([]byte, n)
	offset := 0

	var counter uint64

	for offset < n {
		sum := sha256.Sum256([]byte("ha-pad-v1:" + strconv.Itoa(index) + ":" + strconv.FormatUint(counter, 10)))
		counter++

		for _, b := range sum {
			if offset >= n {
				break
			}

			letters[offset] = 'a' + b%26
			offset++
		}
	}

	return string(letters)
}

// haSeedBlobs writes about mb megabytes of checkpoint data: one stack for each
// padded checkpoint, each with its own blob.
func haSeedBlobs(ctx context.Context, t *testing.T, env *e2e.Env, mb int) int {
	t.Helper()

	count := (mb<<20 + haPadBytes - 1) / haPadBytes

	for i := range count {
		name := fmt.Sprintf("hapad%d", i)

		status, body, err := env.APIStatus(ctx, http.MethodPost,
			"/api/stacks/"+e2e.Namespace+"/"+e2e.Project, fmt.Sprintf(`{"stackName":%q}`, name))
		if err != nil || status != http.StatusOK {
			t.Fatalf("create stack %s answers %d: %v (%s)", name, status, err, body)
		}

		held := env.StartHeldUpdate(ctx, t, e2e.Namespace, e2e.Project, name)
		env.SaveHeldPaddedCheckpoint(ctx, t, held, `{"version":3,"deployment":{}}`,
			map[string]string{"e2ePad": haPad(i, haPadBytes)})
		env.EndHeldUpdate(ctx, t, held, "succeeded")
	}

	return count
}

// haBackupOperation is the durable operation of one backup.
type haBackupOperation struct {
	id       string
	state    string
	owner    string
	expiries int64
}

// haReadBackupOperation reads the newest backup operation that the database
// holds.
func haReadBackupOperation(ctx context.Context, t *testing.T, conn *pgx.Conn) (haBackupOperation, bool) {
	t.Helper()

	var op haBackupOperation

	rows, err := conn.Query(ctx,
		`SELECT id, state, COALESCE(claim_owner, ''), expiries FROM durable_operations `+
			`WHERE kind = 'backup' ORDER BY created_at_ms DESC LIMIT 1`)
	if err != nil {
		t.Fatalf("read the backup operation: %v", err)
	}

	defer rows.Close()

	if !rows.Next() {
		return op, false
	}

	if err := rows.Scan(&op.id, &op.state, &op.owner, &op.expiries); err != nil {
		t.Fatalf("scan the backup operation: %v", err)
	}

	return op, true
}

// haWaitBackupClaim waits until a worker holds the claim of the backup that
// started after the call, and returns the operation.
func haWaitBackupClaim(ctx context.Context, t *testing.T, conn *pgx.Conn) haBackupOperation {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haLease+haBackupGrace)
	defer cancel()

	for {
		if op, ok := haReadBackupOperation(ctx, t, conn); ok && op.state == "running" && op.owner != "" {
			return op
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no worker claimed the backup: %v", ctx.Err())
		case <-time.After(haPollEvery):
		}
	}
}

// haStartBackup starts a backup through the installation API and returns its
// ID.
func haStartBackup(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("POST backups answers %d: %v (%s)", status, err, body)
	}

	var started struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil || started.ID == "" {
		t.Fatalf("POST backups answers %q, want an id: %v", body, err)
	}

	return started.ID
}

// haKillDuringBackup starts a backup, finds the instance that holds its claim
// and sends it SIGKILL. The backup must end succeeded within the lease period
// plus 60 s. `backup check` must report it complete, and the operation must
// show one expiry.
func haKillDuringBackup(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)

	mb := haBackupMB(t)
	seeded := time.Now()
	stacks := haSeedBlobs(ctx, t, env, mb)
	t.Logf("wrote %d checkpoints of %d MiB in %s", stacks, haPadBytes>>20, time.Since(seeded).Round(time.Millisecond))

	env.Recorder.SetStep("3 kill during backup")

	id := haStartBackup(ctx, t, env)
	conn := haConnect(ctx, t, env)
	op := haWaitBackupClaim(ctx, t, conn)

	victim := cluster.IndexOfID(op.owner)
	if victim < 0 {
		t.Fatalf("the claim owner %q is no instance of the cluster", op.owner)
	}

	t.Logf("backup %s, operation %s: instance %d holds the claim", id, op.id, victim)
	haRestartOnCleanup(ctx, t, cluster, victim)

	killed := time.Now()
	if err := cluster.Kill(victim); err != nil {
		t.Fatalf("kill instance %d: %v", victim, err)
	}

	haWaitBackupSucceeded(ctx, t, env, id, killed.Add(haLease+haBackupGrace))
	t.Logf("backup %s ended succeeded %s after the kill", id, time.Since(killed).Round(time.Millisecond))

	after := haWaitBackupOperationEnd(ctx, t, conn, op.id)
	if after.state != "succeeded" || after.expiries != 1 {
		t.Fatalf("the backup operation reads state %q with %d expiries, want succeeded with 1; "+
			"if the backup ended before the kill, raise %s", after.state, after.expiries, haBackupMBEnv)
	}

	check := env.RunBinary(ctx, t, "backup", "check",
		"--server", env.URL, "--ca-file", env.CABundle(), "--backup", id)
	if check.ExitCode != 0 || !strings.Contains(check.Stdout, "backup "+id+" is complete") {
		t.Fatalf("backup check exits with status %d:\n%s%s", check.ExitCode, check.Stdout, check.Stderr)
	}

	if err := cluster.Start(ctx, victim); err != nil {
		t.Fatalf("start instance %d again: %v", victim, err)
	}

	if err := cluster.WaitHealthy(ctx); err != nil {
		t.Fatalf("%v", err)
	}
}

// haWaitBackupOperationEnd waits until the durable operation id has ended: the
// backup row turns succeeded just before the operation does.
func haWaitBackupOperationEnd(ctx context.Context, t *testing.T, conn *pgx.Conn, id string) haBackupOperation {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haOperationEnd)
	defer cancel()

	for {
		op, ok := haReadBackupOperation(ctx, t, conn)
		if !ok || op.id != id {
			t.Fatalf("the newest backup operation is %+v, want operation %s", op, id)
		}

		if op.state != "running" && op.state != "pending" {
			return op
		}

		select {
		case <-ctx.Done():
			t.Fatalf("the backup operation %s stays %s: %v", id, op.state, ctx.Err())
		case <-time.After(haPollEvery):
		}
	}
}

// haWaitBackupSucceeded polls the backup until it is succeeded, and fails the
// test when it fails or when the deadline passes.
func haWaitBackupSucceeded(ctx context.Context, t *testing.T, env *e2e.Env, id string, deadline time.Time) {
	t.Helper()

	for time.Now().Before(deadline) {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/backups/"+id, "")
		if err == nil && status == http.StatusOK {
			var doc backupDoc

			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatalf("decode backup %s %q: %v", id, body, err)
			}

			switch doc.State {
			case "succeeded":
				return
			case "running":
			default:
				t.Fatalf("backup %s reads %q with the error %q, want succeeded", id, doc.State, doc.Error)
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("wait for backup %s: %v", id, ctx.Err())
		case <-time.After(haPollEvery):
		}
	}

	t.Fatalf("backup %s did not end succeeded within the lease period (%s) and %s of the kill",
		id, haLease, haBackupGrace)
}
