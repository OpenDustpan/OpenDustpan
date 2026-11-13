//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// configSecretKey is the config key the move carries from S to D.
	configSecretKey = "apiKey"
	// configSecretValue is the test-only secret the move carries. Every step
	// that sees it is redacted, so it never reaches the log or the report.
	configSecretValue = "s3cr3t-config-move" //nolint:gosec // Test-only secret; steps redact it from logs and reports.
)

// TestPulumiConfigSecrets proves P1 config secrets move (ADR 29 decision 3,
// exit test step 2): under the login of S set a secret config value, read it
// with config --show-secrets --json, under the login of D in a fresh project
// directory with the same project set each value with config set --secret
// with the value from stdin, prove the value on D decrypts to the same text,
// the stack file on D holds a secure value and no plain text file stays on
// disk. It runs on every Pulumi version in the matrix and writes a -pair
// report. The secret value is never in a command argument or in the report.
func TestPulumiConfigSecrets(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tool := e2e.Pulumi()

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			runConfigSecretsLabel(t, fetcher, tool, label, databases)
		})
	}
}

// runConfigSecretsLabel installs one Pulumi CLI and runs the config move on
// every store under test.
func runConfigSecretsLabel(
	t *testing.T, fetcher *e2e.Fetcher, tool e2e.Tool, label string, databases []e2e.Store,
) {
	t.Helper()

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := fetcher.Install(installCtx, tool, label)
	if err != nil {
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label+"-pair", db, err.Error(), false))
		}

		t.Fatalf("install %s %s: %v", tool.Name, label, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runConfigSecretsOnStore(t, cli, db)
		})
	}
}

// configSecretsDoc decodes config --show-secrets --json: each key holds its
// value and whether it is secret. Keys carry the project prefix, for example
// "e2e-project:apiKey".
type configSecretsDoc map[string]struct {
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// runConfigSecretsOnStore moves one secret config value from S to D on one
// store: set it on S, read it back as JSON, set each value on D from stdin,
// prove D decrypts it, its stack file holds a secure value and no plain text
// file stays on disk.
func runConfigSecretsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRun())
	})

	src, dst := pair.Src, pair.Dst
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	src.Step(ctx, t, step("login to source", contains(src.URL), "login"))
	src.Step(ctx, t, step("init on source", nil, "stack", "init", qualified))

	src.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "config set secret on source",
		Args:   []string{"config", "set", "--secret", configSecretKey},
		Env:    nil,
		Stdin:  configSecretValue + "\n",
		Fail:   false,
		Redact: true,
	})

	moved := map[string]string{}

	src.Step(ctx, t, e2e.StepSpec{
		Check:  checkConfigSecretsShown(moved),
		Name:   "config show secrets on source",
		Args:   []string{"config", "--show-secrets", "--json"},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: true,
	})

	if len(moved) == 0 {
		t.Fatal("source shows no config values, want the secret just set")
	}

	// The guide leaves the source stack file in place: config lives in
	// Pulumi.<stack>.yaml, not on the server, so the source file still holds
	// the ciphertext of S after the move.
	src.NoteStep(t, "source stack file stays", "the guide leaves the source Pulumi."+stackName+
		".yaml in place with the ciphertext of the source; the move never deletes it")

	dst.Step(ctx, t, step("login to destination", contains(dst.URL), "login"))
	dst.Step(ctx, t, step("init on destination", nil, "stack", "init", qualified))

	// The destination project directory is fresh with the same project: it
	// holds Pulumi.yaml but no stack file yet. Each value moves with config
	// set --secret with the value from stdin, never as an argument.
	for key, value := range moved {
		dst.Step(ctx, t, e2e.StepSpec{
			Check:  nil,
			Name:   "config set " + key + " on destination",
			Args:   []string{"config", "set", "--secret", key},
			Env:    nil,
			Stdin:  value + "\n",
			Fail:   false,
			Redact: true,
		})
	}

	for key, value := range moved {
		dst.Step(ctx, t, e2e.StepSpec{
			Check:  checkConfigGetMatches(value),
			Name:   "config get " + key + " on destination",
			Args:   []string{"config", "get", key},
			Env:    nil,
			Stdin:  "",
			Fail:   false,
			Redact: true,
		})
	}

	checkDestinationStackFile(t, dst, moved)
	checkNoPlaintextOnDisk(t, src, dst, moved)

	checkConfigSecretsRedacted(t, pair, moved)
}

// checkConfigSecretsShown records every value of config --show-secrets --json
// in moved under its short key and proves the secret just set is there. The
// failure names no value, so the secret never reaches the report.
func checkConfigSecretsShown(moved map[string]string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var doc configSecretsDoc

		if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
			return fmt.Errorf("%w: decode config secrets: %w", errUnexpected, err)
		}

		for full, entry := range doc {
			key := full
			if _, short, ok := strings.Cut(full, ":"); ok {
				key = short
			}

			moved[key] = entry.Value
		}

		if _, ok := moved[configSecretKey]; !ok {
			return fmt.Errorf("%w: source shows no value for the secret key", errUnexpected)
		}

		return nil
	}
}

// checkConfigGetMatches proves config get printed value without naming it in
// the failure, so the secret never reaches the report.
func checkConfigGetMatches(value string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if got := strings.TrimSpace(r.Stdout); got != value {
			return fmt.Errorf("%w: config get differs", errUnexpected)
		}

		return nil
	}
}

// checkDestinationStackFile proves the stack file on D holds a secure value
// and no plain text of any moved value.
func checkDestinationStackFile(t *testing.T, dst *e2e.Env, moved map[string]string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dst.ProjectDir(), "Pulumi.*.yaml"))
	if err != nil {
		t.Fatalf("list stack files: %v", err)
	}

	var stackFile string

	for _, name := range matches {
		if filepath.Base(name) != "Pulumi.yaml" {
			stackFile = name

			break
		}
	}

	if stackFile == "" {
		t.Fatalf("destination holds no stack file in %s", dst.ProjectDir())
	}

	raw, err := os.ReadFile(stackFile) //nolint:gosec // The test reads the stack file it wrote.
	if err != nil {
		t.Fatalf("read the destination stack file: %v", err)
	}

	if !strings.Contains(string(raw), "secure:") {
		t.Fatalf("destination stack file %s holds no secure value", stackFile)
	}

	for _, value := range moved {
		if strings.Contains(string(raw), value) {
			t.Fatal("destination stack file holds the plain text secret")
		}
	}
}

// checkNoPlaintextOnDisk proves no file in either project directory holds the
// plain text of any moved value: the JSON of --show-secrets stayed in memory
// and the move wrote ciphertext only.
func checkNoPlaintextOnDisk(t *testing.T, src, dst *e2e.Env, moved map[string]string) {
	t.Helper()

	values := movedValues(moved)

	for _, dir := range []string{src.ProjectDir(), dst.ProjectDir()} {
		checkDirHoldsNoSecret(t, dir, values)
	}
}

// movedValues returns the non-empty moved values.
func movedValues(moved map[string]string) []string {
	values := make([]string, 0, len(moved))

	for _, value := range moved {
		if value != "" {
			values = append(values, value)
		}
	}

	return values
}

// checkDirHoldsNoSecret proves no file directly in dir holds any value.
func checkDirHoldsNoSecret(t *testing.T, dir string, values []string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			checkFileHoldsNoSecret(t, dir, entry.Name(), values)
		}
	}
}

// checkFileHoldsNoSecret proves one file holds none of the values.
func checkFileHoldsNoSecret(t *testing.T, dir, name string, values []string) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // The test reads files it wrote.
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	if holdsAny(string(raw), values) {
		t.Fatalf("plain text stays on disk in %s", name)
	}
}

// holdsAny reports whether s holds any value.
func holdsAny(s string, values []string) bool {
	for _, value := range values {
		if strings.Contains(s, value) {
			return true
		}
	}

	return false
}

// checkConfigSecretsRedacted proves the secret value is never in a command
// argument or in the report: no step command, failure or stored output and no
// recorded request or response body holds any moved value.
func checkConfigSecretsRedacted(t *testing.T, pair *e2e.Pair, moved map[string]string) {
	t.Helper()

	run := pair.NewPairRun()
	values := movedValues(moved)

	if stepsHoldSecret(run.Steps, values) || requestsHoldSecret(run.Requests, values) {
		t.Fatal("the report holds the plain text secret")
	}
}

// stepsHoldSecret reports whether any step command, failure or stored output
// holds any value.
func stepsHoldSecret(steps []e2e.StepResult, values []string) bool {
	for i := range steps {
		if holdsAny(steps[i].Command, values) || holdsAny(steps[i].Failure, values) ||
			holdsAny(steps[i].Stderr, values) {
			return true
		}
	}

	return false
}

// requestsHoldSecret reports whether any recorded request or response body
// holds any value.
func requestsHoldSecret(requests []e2e.Entry, values []string) bool {
	for i := range requests {
		if holdsAny(requests[i].RequestBody, values) || holdsAny(requests[i].ResponseBody, values) {
			return true
		}
	}

	return false
}
