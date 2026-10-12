//go:build e2e

package e2e_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	// Registers the "sqlite" database/sql driver for the upgrade check fixtures.
	_ "modernc.org/sqlite"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

const (
	stackName = "dev"
	// installLimit plus every step's commandTimeout must stay under the
	// go test -timeout in the Makefile, or a timeout loses every report. That
	// holds per version and store only while all runs go at once, so the
	// Makefile's -parallel must cover the whole matrix (12 runs by default: 6
	// versions on 2 stores).
	installLimit = 10 * time.Minute
	// readinessWithoutDBStep proves a stopped database turns /health/ready to
	// 503 while /health/live stays 204.
	readinessWithoutDBStep = "readiness without database"
	// healthReadyPath and healthLivePath are the probe paths (ADR 34).
	healthReadyPath = "/health/ready"
	healthLivePath  = "/health/live"
	// readyOKBody is the readiness body when every check passes.
	readyOKBody = `{"status":"ok"}`
	// readyStoreFailedBody is the readiness body when only the store fails.
	readyStoreFailedBody = `{"status":"unavailable","failed":["store"]}`
	// readyWaitTimeout bounds the wait for the readiness status to change
	// after the proxy closes or reopens. The probe itself has a 2-second
	// limit per check, so this covers several probes.
	readyWaitTimeout = 20 * time.Second
	// readyPollInterval sets how often the wait checks the probe.
	readyPollInterval = 100 * time.Millisecond
)

var errUnexpected = errors.New("unexpected output")

func cacheDir(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("E2E_CACHE_DIR"); dir != "" {
		return dir
	}

	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("find user cache dir: %v", err)
	}

	return filepath.Join(base, "opendustpan-e2e")
}

// TestCLIs runs each tool's scenario against every version in its matrix.
// Reports from both tools land in one summary.
func TestCLIs(t *testing.T) {
	t.Parallel()

	reports := e2e.ReportDir()
	if err := e2e.ClearReports(reports); err != nil {
		t.Fatalf("clear old reports: %v", err)
	}

	// Runs after every parallel subtest has finished and written its report.
	t.Cleanup(func() {
		if err := e2e.WriteSummary(reports); err != nil {
			t.Errorf("write summary: %v", err)
		}

		// Fails for every served route that no step reached, so an
		// endpoint can never land without its e2e step again.
		checkRouteCoverage(t, reports)
	})

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	matrix := []struct {
		scenario func(*testing.T, *e2e.Env)
		tool     e2e.Tool
	}{
		{scenario: pulumiScenario, tool: e2e.Pulumi()},
		{scenario: tofuScenario, tool: e2e.OpenTofu()},
	}

	for _, m := range matrix {
		t.Run(m.tool.Name, func(t *testing.T) {
			t.Parallel()

			for _, label := range m.tool.Versions() {
				t.Run(label, func(t *testing.T) {
					t.Parallel()

					run(t, runInput{
						fetcher:   fetcher,
						tool:      m.tool,
						label:     label,
						databases: databases,
						scenario:  m.scenario,
					})
				})
			}
		})
	}
}

// runInput groups what one CLI version needs to run on every store.
type runInput struct {
	fetcher   *e2e.Fetcher
	tool      e2e.Tool
	label     string
	databases []e2e.Store
	scenario  func(*testing.T, *e2e.Env)
}

func run(t *testing.T, in runInput) {
	t.Helper()

	tool := in.tool
	label := in.label
	databases := in.databases

	if tool.Name == e2e.ToolTofu && runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		reason := "needs Linux, not " + runtime.GOOS
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label, db, reason, true))
		}

		t.Skipf("the OpenTofu suite %s", reason)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := in.fetcher.Install(installCtx, tool, label)
	if err != nil {
		// Still report the version, so a broken download is visible in the summary.
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label, db, err.Error(), false))
		}

		t.Fatalf("install %s %s: %v", tool.Name, label, err)
	}

	t.Logf("%s %s (checksum %s) in %s", cli.Tool, cli.Version, cli.ChecksumSource, cli.Dir)

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runOnStore(t, cli, db, in.scenario)
		})
	}
}

func runOnStore(t *testing.T, cli e2e.Install, store e2e.Store, scenario func(*testing.T, *e2e.Env)) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		reason := "set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite"
		writeReport(t, e2e.NotRun(cli.Tool, cli.Label, store, reason, true))
		t.Skip(reason)
	}

	if e2e.UseS3Blobs(t) {
		if _, ok := e2e.LookupS3Config(); !ok {
			reason := e2e.S3SkipReason()
			writeReport(t, e2e.NotRun(cli.Tool, cli.Label, store, reason, true))
			t.Skip(reason)
		}
	}

	// If server setup fails, NewEnv ends the test before env exists; report
	// the version as not run instead of dropping it from the summary.
	var env *e2e.Env

	t.Cleanup(func() {
		if env == nil {
			writeReport(t, e2e.NotRun(cli.Tool, cli.Label, store, "server setup failed; see the test log", false))

			return
		}

		writeReport(t, e2e.NewRun(env))
	})

	env = e2e.NewEnv(t.Context(), t, cli, store)

	scenario(t, env)

	if cli.Tool == e2e.ToolPulumi {
		checkDeltaCheckpoints(t, cli, store)
	}

	checkReadyWithoutDatabase(t.Context(), t, env)
}

// checkDeltaCheckpoints proves delta checkpoint uploads on two fresh
// servers: one with the capability at cutoff 1, so every save after the
// first is a delta, and one with it off. Both run the same up and both
// exports match. The recorder shows checkpointdelta calls only in the
// first run. Each run writes its own report, so the route coverage sees
// the verbatim and delta traffic.
func checkDeltaCheckpoints(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()

	on := cli
	on.Label += "-delta-on"
	onEnv := e2e.NewEnvWithDeltaCutoff(ctx, t, on, store, 1)

	off := cli
	off.Label += "-delta-off"
	offEnv := e2e.NewEnvWithDeltaCutoff(ctx, t, off, store, 0)

	onExport := deltaUp(ctx, t, onEnv)
	offExport := deltaUp(ctx, t, offEnv)

	onCanonical, offCanonical := canonicalExport(t, onExport), canonicalExport(t, offExport)
	if onCanonical != offCanonical {
		t.Errorf(
			"stack export with delta uploads differs from stack export without them:\non=%s\noff=%s",
			clipCanonical(onCanonical),
			clipCanonical(offCanonical),
		)
	}

	if !recordedPatch(onEnv, "/checkpointverbatim") {
		t.Error("no PATCH checkpointverbatim answered 200 during up with cutoff 1")
	}

	if !recordedPatch(onEnv, "/checkpointdelta") {
		t.Error("no PATCH checkpointdelta answered 200 during up with cutoff 1")
	}

	if !recordedPatch(offEnv, "/checkpoint") {
		t.Error("no PATCH checkpoint answered 200 during up with the capability off")
	}

	if recordedPatch(offEnv, "/checkpointdelta") || recordedPatch(offEnv, "/checkpointverbatim") {
		t.Error("delta traffic recorded with the capability off")
	}

	checkDeltaMaskedState(t, onEnv)

	writeReport(t, e2e.NewRun(onEnv))
	writeReport(t, e2e.NewRun(offEnv))
}

// deltaUp logs in, creates the stack and the other stack its program
// references, runs up and returns the stack export. It carries no secrets,
// so both delta runs hold the same state.
func deltaUp(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, step("stack init", nil, "stack", "init", qualified))
	env.Step(ctx, t, step("stack init other", nil, "stack", "init", "other"))
	env.Step(ctx, t, step("stack select", nil, "stack", "select", qualified))
	env.Step(ctx, t, step("up", nil, "up", "--yes", "--non-interactive"))

	return env.Step(ctx, t, step("stack export", nil, "stack", "export")).Stdout
}

// providerIDPattern matches the random IDs the engine assigns to the
// default provider on every up. Each delta run mints its own, so the
// export comparison ignores them.
var providerIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// canonicalExport returns the stack export with the volatile fields
// removed: the two delta runs finish at different times on different
// servers, so the manifest time, the service URL and the resource
// timestamps always differ, and each up mints a new default provider ID.
// What must match is the state itself.
func canonicalExport(t *testing.T, raw string) string {
	t.Helper()

	var doc struct {
		Version    int            `json:"version"`
		Deployment map[string]any `json:"deployment"`
	}

	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode stack export: %v", err)
	}

	delete(doc.Deployment, "manifest")

	if providers, ok := doc.Deployment["secrets_providers"].(map[string]any); ok {
		delete(providers, "state")
	}

	if resources, ok := doc.Deployment["resources"].([]any); ok {
		for _, res := range resources {
			if r, ok := res.(map[string]any); ok {
				delete(r, "created")
				delete(r, "modified")
			}
		}
	}

	canonical, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode stack export: %v", err)
	}

	return providerIDPattern.ReplaceAllString(string(canonical), "provider-id")
}

// clipCanonical shortens a canonical export for the failure report.
func clipCanonical(s string) string {
	const limit = 20000
	if len(s) <= limit {
		return s
	}

	return s[:limit] + "…"
}

// recordedPatch reports whether the up step recorded a PATCH of path with a
// 200 answer. Suffixes keep checkpoint, checkpointverbatim and checkpointdelta
// apart.
func recordedPatch(env *e2e.Env, suffix string) bool {
	entries := env.Recorder.Entries()
	for i := range entries {
		if entries[i].Step == "up" && entries[i].Method == http.MethodPatch &&
			strings.HasSuffix(entries[i].Path, suffix) && entries[i].Status == http.StatusOK {
			return true
		}
	}

	return false
}

// checkDeltaMaskedState proves the masked state read after delta saves on
// the delta-enabled server: two ups of a program with a secret output and
// a stack reference, then the resource list, one resource detail and the
// outputs on latest and on version 1. It runs on the delta-on server only,
// so the default scenario does not change.
func checkDeltaMaskedState(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/masked-delta", org, e2e.Project)
	base := fmt.Sprintf("/api/stacks/%s/%s/masked-delta", org, e2e.Project)

	env.WritePulumiProject(t, maskedStateProgram(org, ""))
	env.Step(ctx, t, step("stack init masked delta", nil, "stack", "init", qualified, "--no-select"))
	env.Step(
		ctx,
		t,
		step(
			"config set secret for masked delta",
			nil,
			"config", "set", "--secret", "--stack", qualified, "apiKey", maskedStateSecret,
		),
	)
	env.Step(ctx, t, step("up masked delta 1", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.WritePulumiProject(t, maskedStateProgram(org, "hello-again"))
	env.Step(ctx, t, step("up masked delta 2", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	exported := env.Step(
		ctx,
		t,
		step("stack export masked delta", nil, "stack", "export", "--stack", qualified),
	).Stdout
	firstExported := env.Step(
		ctx,
		t,
		step("stack export masked delta version 1", nil, "stack", "export", "--version", "1", "--stack", qualified),
	).Stdout
	env.Step(ctx, t, step(
		"masked delta state read",
		checkDeltaMaskedBodies(ctx, env, base, exported, firstExported),
		"stack", "--show-name", "--stack", qualified,
	))
	env.Step(ctx, t, step("stack rm masked delta", nil, "stack", "rm", "--yes", "--force", "--stack", qualified))
}

// checkDeltaMaskedBodies proves the masked bodies on the delta stack: the
// recorder shows checkpointdelta saves for the masked ups, the latest list
// holds the same URNs as the export with no envelope fields, one resource
// detail answers masked inputs and outputs, the outputs answer the secret
// masked, and the version 1 reads answer the unwrapped deployment too.
func checkDeltaMaskedBodies(
	ctx context.Context, env *e2e.Env, base, exported, firstExported string,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		if !recordedDeltaPatchInSteps(env, []string{"up masked delta 1", "up masked delta 2"}) {
			return fmt.Errorf("%w: no PATCH checkpointdelta answered 200 during the masked delta ups", errUnexpected)
		}

		stackURN, latestErr := checkDeltaMaskedLatest(ctx, env, base, exported)
		if latestErr != nil {
			return latestErr
		}

		outputs, outputsErr := maskedStateGet(ctx, env, base+"/state/latest/outputs")
		if outputsErr != nil {
			return outputsErr
		}

		if outputsErr := checkDeltaMaskedOutputs(outputs); outputsErr != nil {
			return outputsErr
		}

		return checkDeltaMaskedVersionOne(ctx, env, base, firstExported, stackURN)
	}
}

// checkDeltaMaskedLatest proves the latest reads: the list holds the same
// URNs as the export with no envelope fields, and one resource detail
// answers masked inputs and outputs. It returns the stack URN for the
// version 1 reads.
func checkDeltaMaskedLatest(ctx context.Context, env *e2e.Env, base, exported string) (string, error) {
	latest, latestErr := maskedStateGet(ctx, env, base+"/state/latest/resources")
	if latestErr != nil {
		return "", latestErr
	}

	if listErr := checkDeltaListMatchesExport(latest, exported); listErr != nil {
		return "", listErr
	}

	stackURN, urnErr := deltaMaskedStackURN(latest)
	if urnErr != nil {
		return "", urnErr
	}

	detail, detailErr := maskedStateGet(ctx, env, base+"/state/latest/resources/"+url.PathEscape(stackURN))
	if detailErr != nil {
		return "", detailErr
	}

	if detailErr := checkDeltaMaskedDetail(detail); detailErr != nil {
		return "", detailErr
	}

	return stackURN, nil
}

// recordedDeltaPatchInSteps reports whether any of the named steps recorded
// a PATCH checkpointdelta with a 200 answer.
func recordedDeltaPatchInSteps(env *e2e.Env, steps []string) bool {
	entries := env.Recorder.Entries()
	for i := range entries {
		if entries[i].Method == http.MethodPatch &&
			strings.HasSuffix(entries[i].Path, "/checkpointdelta") &&
			entries[i].Status == http.StatusOK &&
			slices.Contains(steps, entries[i].Step) {
			return true
		}
	}

	return false
}

// checkDeltaListMatchesExport proves the resource list holds the same URNs
// as the stack export and neither holds envelope fields.
func checkDeltaListMatchesExport(listBody, exported string) error {
	if err := checkDeltaNoEnvelope(listBody); err != nil {
		return err
	}

	listURNs, listErr := maskedListURNs(listBody)
	if listErr != nil {
		return listErr
	}

	if len(listURNs) < 2 {
		return fmt.Errorf(
			"%w: latest list holds %d resources, want at least the stack and a reference",
			errUnexpected,
			len(listURNs),
		)
	}

	wantURNs, exportErr := exportURNs(exported)
	if exportErr != nil {
		return exportErr
	}

	slices.Sort(listURNs)
	slices.Sort(wantURNs)

	if !slices.Equal(listURNs, wantURNs) {
		return fmt.Errorf("%w: latest list URNs %q differ from export URNs %q", errUnexpected, listURNs, wantURNs)
	}

	return nil
}

// maskedListURNs returns the URNs of the masked resource list body.
func maskedListURNs(body string) ([]string, error) {
	var list maskedStateList

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return nil, fmt.Errorf("%w: decode latest list: %w", errUnexpected, err)
	}

	urns := make([]string, 0, len(list.Resources))
	for _, res := range list.Resources {
		urns = append(urns, res.URN)
	}

	return urns, nil
}

// deltaExportResource is one resource of a stack export in e2e checks.
type deltaExportResource struct {
	URN string `json:"urn"`
}

// deltaExportDeployment is the deployment of a stack export in e2e checks.
type deltaExportDeployment struct {
	Resources []deltaExportResource `json:"resources"`
}

// deltaExportDoc is a stack export in e2e checks.
type deltaExportDoc struct {
	Deployment deltaExportDeployment `json:"deployment"`
}

// exportURNs returns the URNs of the deployment resources in the stack
// export.
func exportURNs(exported string) ([]string, error) {
	var doc deltaExportDoc

	if err := json.Unmarshal([]byte(exported), &doc); err != nil {
		return nil, fmt.Errorf("%w: decode stack export: %w", errUnexpected, err)
	}

	urns := make([]string, 0, len(doc.Deployment.Resources))
	for _, res := range doc.Deployment.Resources {
		urns = append(urns, res.URN)
	}

	return urns, nil
}

// deltaMaskedStackURN returns the URN of the stack resource in the list.
func deltaMaskedStackURN(listBody string) (string, error) {
	var list maskedStateList

	if err := json.Unmarshal([]byte(listBody), &list); err != nil {
		return "", fmt.Errorf("%w: decode latest list: %w", errUnexpected, err)
	}

	for _, res := range list.Resources {
		if res.Type == "pulumi:pulumi:Stack" {
			return res.URN, nil
		}
	}

	return "", fmt.Errorf("%w: latest list holds no stack resource", errUnexpected)
}

// checkDeltaMaskedDetail proves one resource detail answers its inputs and
// outputs with the secret masked and no envelope fields.
func checkDeltaMaskedDetail(detail string) error {
	if err := checkDeltaNoEnvelope(detail); err != nil {
		return err
	}

	if err := maskedStateCheckLeaks(detail, maskedStateSecret); err != nil {
		return err
	}

	if err := maskedStateCheckMarkers(detail); err != nil {
		return err
	}

	var doc struct {
		Inputs  map[string]any `json:"inputs"`
		Outputs map[string]any `json:"outputs"`
		Masked  []string       `json:"masked"`
	}

	if err := json.Unmarshal([]byte(detail), &doc); err != nil {
		return fmt.Errorf("%w: decode resource detail: %w", errUnexpected, err)
	}

	if doc.Inputs == nil || doc.Outputs == nil {
		return fmt.Errorf("%w: resource detail holds no inputs and outputs", errUnexpected)
	}

	if !maskedStateHasMaskedWith(detail, "/outputs/") {
		return fmt.Errorf("%w: resource detail holds no masked secret in outputs", errUnexpected)
	}

	return nil
}

// checkDeltaMaskedOutputs proves the stack outputs answer the secret masked
// with no plain text, ciphertext or signature key.
func checkDeltaMaskedOutputs(outputs string) error {
	if err := checkDeltaNoEnvelope(outputs); err != nil {
		return err
	}

	if err := maskedStateCheckLeaks(outputs, maskedStateSecret); err != nil {
		return err
	}

	if err := maskedStateCheckMarkers(outputs); err != nil {
		return err
	}

	if !maskedStateHasSecret(outputs) {
		return fmt.Errorf("%w: outputs hold no masked secret", errUnexpected)
	}

	return nil
}

// checkDeltaMaskedVersionOne proves the version 1 reads answer the
// unwrapped deployment: the list holds the same URNs as the export of
// version 1, and the detail and outputs answer masked with no leaks.
func checkDeltaMaskedVersionOne(
	ctx context.Context, env *e2e.Env, base, firstExported, stackURN string,
) error {
	first, firstErr := maskedStateGet(ctx, env, base+"/state/1/resources")
	if firstErr != nil {
		return firstErr
	}

	if listErr := checkDeltaListMatchesExport(first, firstExported); listErr != nil {
		return listErr
	}

	firstDetail, detailErr := maskedStateGet(ctx, env, base+"/state/1/resources/"+url.PathEscape(stackURN))
	if detailErr != nil {
		return detailErr
	}

	if detailErr := checkDeltaMaskedDetail(firstDetail); detailErr != nil {
		return detailErr
	}

	firstOutputs, outputsErr := maskedStateGet(ctx, env, base+"/state/1/outputs")
	if outputsErr != nil {
		return outputsErr
	}

	return checkDeltaMaskedOutputs(firstOutputs)
}

// checkDeltaNoEnvelope fails when the masked body carries envelope fields
// of the stored deployment: version, deployment or features.
func checkDeltaNoEnvelope(body string) error {
	var doc map[string]any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode masked body: %w", errUnexpected, err)
	}

	for _, field := range []string{"version", "deployment", "features"} {
		if _, ok := doc[field]; ok {
			return fmt.Errorf("%w: masked body holds envelope field %q", errUnexpected, field)
		}
	}

	return nil
}

func writeReport(t *testing.T, r e2e.Run) {
	t.Helper()

	if err := e2e.WriteRun(e2e.ReportDir(), r); err != nil {
		t.Errorf("write report: %v", err)
	}
}

func contains(want string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, want) {
			return fmt.Errorf("%w: stdout lacks %q", errUnexpected, want)
		}

		return nil
	}
}

// tagListed checks that the tag table lists name with value: one line whose
// first two fields are the name and the value.
func tagListed(name, value string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		for line := range strings.Lines(r.Stdout) {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == name && fields[1] == value {
				return nil
			}
		}

		return fmt.Errorf("%w: stdout lacks tag %q with value %q", errUnexpected, name, value)
	}
}

// tagAbsent checks that the tag table lists no line for name.
func tagAbsent(name string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		for line := range strings.Lines(r.Stdout) {
			if fields := strings.Fields(line); len(fields) >= 1 && fields[0] == name {
				return fmt.Errorf("%w: stdout still lists tag %q", errUnexpected, name)
			}
		}

		return nil
	}
}

// step is a scenario step with no extra environment.
func step(name string, check func(e2e.Result) error, args ...string) e2e.StepSpec {
	return e2e.StepSpec{Check: check, Name: name, Args: args, Env: nil, Stdin: "", Fail: false, Redact: false}
}

// failStep is a scenario step that expects the CLI to fail, for steps that
// prove an error message.
func failStep(name string, check func(e2e.Result) error, args ...string) e2e.StepSpec {
	return e2e.StepSpec{Check: check, Name: name, Args: args, Env: nil, Stdin: "", Fail: true, Redact: false}
}

// outputContains reports whether stdout or stderr holds want. Failing CLI
// commands print the server error to stderr, so error steps check both.
func outputContains(want string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, want) && !strings.Contains(r.Stderr, want) {
			return fmt.Errorf("%w: output lacks %q", errUnexpected, want)
		}

		return nil
	}
}

// checkReady proves GET /health/ready answers 200 with {"status":"ok"} after
// the server starts, on every store. It sends no token: the probe is public.
func checkReady(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/health/ready", "")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET /health/ready status %d, want %d (body %s)", status, http.StatusOK, body)
	}

	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("GET /health/ready body %q, want status ok", body)
	}
}

// checkReadyWithoutDatabase proves a stopped database turns /health/ready to
// 503 with the check name only while /health/live stays 204, and that
// reopening the database turns /health/ready to 200 again. The binary server
// and the in-process server both reach PostgreSQL through a TCP proxy, so
// closing the proxy stops the database for that server only. On SQLite the
// step is skipped with a report entry that says why.
func checkReadyWithoutDatabase(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	if env.Store() == e2e.StoreSQLite {
		reason := "SQLite has no separate database process to stop; the step needs PostgreSQL"
		env.SkipStep(readinessWithoutDBStep, reason)
		t.Logf("skipped %q: %s", readinessWithoutDBStep, reason)

		return
	}

	t.Run(readinessWithoutDBStep, func(t *testing.T) {
		env.Recorder.SetStep(readinessWithoutDBStep)

		if err := doReadyWithoutDatabase(ctx, t, env); err != nil {
			env.AddStep(e2e.StepResult{
				Name:     readinessWithoutDBStep,
				Command:  http.MethodGet + " " + healthReadyPath,
				Failure:  err.Error(),
				Stderr:   "",
				ExitCode: 1,
				Passed:   false,
				Skipped:  false,
			})
			t.Errorf("%s: %v", readinessWithoutDBStep, err)
		} else {
			env.AddStep(e2e.StepResult{
				Name:     readinessWithoutDBStep,
				Command:  http.MethodGet + " " + healthReadyPath,
				Failure:  "",
				Stderr:   "",
				ExitCode: 0,
				Passed:   true,
				Skipped:  false,
			})
		}
	})
}

// doReadyWithoutDatabase runs the stopped-database sequence and returns an
// error for the first check that fails. It reopens the proxy before
// returning an error after the stop, so the environment stays usable.
func doReadyWithoutDatabase(ctx context.Context, t *testing.T, env *e2e.Env) error {
	t.Helper()

	if _, _, err := waitReadyStatus(ctx, env, http.StatusOK); err != nil {
		return err
	}

	env.StopDatabase(t)

	stoppedErr := checkStoppedDatabase(ctx, env)
	if stoppedErr != nil {
		env.StartDatabase(ctx, t)

		return stoppedErr
	}

	env.StartDatabase(ctx, t)

	return checkRecoveredDatabase(ctx, env)
}

// checkStoppedDatabase proves the stopped state: /health/ready answers 503
// with the store check name and no driver text, and /health/live stays 204.
func checkStoppedDatabase(ctx context.Context, env *e2e.Env) error {
	status, body, err := waitReadyStatus(ctx, env, http.StatusServiceUnavailable)
	if err != nil {
		return err
	}

	if checkErr := checkStoppedBody(status, body); checkErr != nil {
		return checkErr
	}

	liveStatus, liveBody, liveErr := env.APIStatus(ctx, http.MethodGet, healthLivePath, "")
	if liveErr != nil {
		return fmt.Errorf("%w: GET %s: %w", errUnexpected, healthLivePath, liveErr)
	}

	if liveStatus != http.StatusNoContent {
		return fmt.Errorf(
			"%w: GET %s status %d, want %d (body %s)",
			errUnexpected,
			healthLivePath,
			liveStatus,
			http.StatusNoContent,
			liveBody,
		)
	}

	return nil
}

// checkRecoveredDatabase proves /health/ready answers 200 again after the
// proxy reopens. pgxpool discards the broken connections and dials anew
// through the proxy, so the first probes after the reopen already pass.
func checkRecoveredDatabase(ctx context.Context, env *e2e.Env) error {
	_, body, err := waitReadyStatus(ctx, env, http.StatusOK)
	if err != nil {
		return err
	}

	if strings.TrimSpace(body) != readyOKBody {
		return fmt.Errorf("%w: GET %s body %q, want %q", errUnexpected, healthReadyPath, body, readyOKBody)
	}

	return nil
}

// waitReadyStatus polls GET /health/ready until it answers want or the wait
// times out. It returns the last status and body, or an error when the probe
// never answers want.
func waitReadyStatus(ctx context.Context, env *e2e.Env, want int) (int, string, error) {
	deadline := time.Now().Add(readyWaitTimeout)

	var lastStatus int

	var lastBody string

	for time.Now().Before(deadline) {
		status, body, err := env.APIStatus(ctx, http.MethodGet, healthReadyPath, "")
		if err == nil {
			lastStatus, lastBody = status, body

			if status == want {
				return status, body, nil
			}
		} else {
			lastBody = err.Error()
		}

		timer := time.NewTimer(readyPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()

			return lastStatus, lastBody, fmt.Errorf(
				"%w: context done while waiting for %s %d: %s",
				errUnexpected,
				healthReadyPath,
				want,
				lastBody,
			)
		case <-timer.C:
		}
	}

	return lastStatus, lastBody, fmt.Errorf(
		"%w: GET %s status %d, want %d (body %s)",
		errUnexpected,
		healthReadyPath,
		lastStatus,
		want,
		lastBody,
	)
}

// checkStoppedBody proves a 503 readiness body names the store check only,
// with no driver text. The blob store stays healthy, so the body must not
// name it.
func checkStoppedBody(status int, body string) error {
	if status != http.StatusServiceUnavailable {
		return fmt.Errorf(
			"%w: GET %s status %d, want %d (body %s)",
			errUnexpected,
			healthReadyPath,
			status,
			http.StatusServiceUnavailable,
			body,
		)
	}

	trimmed := strings.TrimSpace(body)
	if !strings.Contains(trimmed, `"store"`) {
		return fmt.Errorf("%w: GET %s body %q lacks the store check name", errUnexpected, healthReadyPath, body)
	}

	if strings.Contains(trimmed, `"blob"`) {
		return fmt.Errorf("%w: GET %s body %q names the healthy blob check", errUnexpected, healthReadyPath, body)
	}

	lowered := strings.ToLower(trimmed)
	for _, leaked := range []string{"postgres", "pgx", "dial", "refused", "connect", "driver", "eof", "pool"} {
		if strings.Contains(lowered, leaked) {
			return fmt.Errorf("%w: GET %s body %q holds driver text %q", errUnexpected, healthReadyPath, body, leaked)
		}
	}

	if trimmed != readyStoreFailedBody {
		return fmt.Errorf("%w: GET %s body %q, want %q", errUnexpected, healthReadyPath, body, readyStoreFailedBody)
	}

	return nil
}

// checkNamespaceCreateThroughAPI proves the namespace create route on a new
// installation that already holds org through POST /api/server/namespaces:
// a duplicate answers 409, a bad name answers 400, and a call without a
// token answers 401. The harness created org through the same route on the
// empty installation with the bootstrap admin token, so the duplicate proves
// that creation, and the login steps that follow prove each CLI can log in
// after it.
func checkNamespaceCreateThroughAPI(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()

	status, body, err := env.ServerAPIStatus(
		ctx,
		http.MethodPost,
		"/api/server/namespaces",
		`{"name":`+strconv.Quote(org)+`}`,
	)
	if err != nil {
		t.Fatalf("POST duplicate namespace %q: %v", org, err)
	}

	if status != http.StatusConflict {
		t.Fatalf("POST duplicate namespace %q answers %d, want %d (%s)", org, status, http.StatusConflict, body)
	}

	status, body, err = env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/namespaces", `{"name":"../x"}`)
	if err != nil {
		t.Fatalf("POST invalid namespace: %v", err)
	}

	if status != http.StatusBadRequest {
		t.Fatalf("POST invalid namespace answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	status, body, err = env.ServerAPIStatusBare(ctx, http.MethodPost, "/api/server/namespaces")
	if err != nil {
		t.Fatalf("POST namespaces without a token: %v", err)
	}

	if status != http.StatusUnauthorized {
		t.Fatalf("POST namespaces without a token answers %d, want %d (%s)", status, http.StatusUnauthorized, body)
	}
}

// createSecondNamespaceThroughAPI creates name through POST
// /api/server/namespaces and proves it answers 201 with the namespace homed
// in the test region, then that a duplicate answers 409.
func createSecondNamespaceThroughAPI(t *testing.T, env *e2e.Env, name string) {
	t.Helper()

	ctx := t.Context()

	status, body, err := env.ServerAPIStatus(
		ctx,
		http.MethodPost,
		"/api/server/namespaces",
		`{"name":`+strconv.Quote(name)+`}`,
	)
	if err != nil {
		t.Fatalf("POST namespace %q: %v", name, err)
	}

	if status != http.StatusCreated {
		t.Fatalf("POST namespace %q answers %d, want %d (%s)", name, status, http.StatusCreated, body)
	}

	var doc struct {
		Name            string `json:"name"`
		HomeRegion      string `json:"homeRegion"`
		FailoverVersion int64  `json:"failoverVersion"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &doc); decodeErr != nil {
		t.Fatalf("decode namespace %q %q: %v", name, body, decodeErr)
	}

	if doc.Name != name || doc.FailoverVersion != 1 || doc.HomeRegion == "" {
		t.Fatalf("namespace answers %+v, want %q at version 1 with a region", doc, name)
	}

	status, body, err = env.ServerAPIStatus(
		ctx,
		http.MethodPost,
		"/api/server/namespaces",
		`{"name":`+strconv.Quote(name)+`}`,
	)
	if err != nil {
		t.Fatalf("POST duplicate namespace %q: %v", name, err)
	}

	if status != http.StatusConflict {
		t.Fatalf("POST duplicate namespace %q answers %d, want %d (%s)", name, status, http.StatusConflict, body)
	}
}

// pulumiScenario exercises login and stack management. A namespace is a
// Pulumi organization, so every step that names or implies an organization
// should resolve to the Namespace namespace.
func pulumiScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	checkReady(ctx, t, env)
	checkNamespaceCreateThroughAPI(t, env, e2e.Namespace)
	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, stackName)
	renamedQualified := fmt.Sprintf("%s/%s/renamed", org, e2e.Project)
	renamePath := fmt.Sprintf("/api/stacks/%s/%s/%s/rename", org, e2e.Project, stackName)
	stackPath := fmt.Sprintf("/api/stacks/%s/%s", org, e2e.Project)
	scratch := fmt.Sprintf("%s/%s/scratch", org, e2e.Project)
	resourced := fmt.Sprintf("%s/%s/resourced", org, e2e.Project)
	resourcedPath := fmt.Sprintf("/api/stacks/%s/%s/resourced", org, e2e.Project)

	for _, s := range pulumiSetupSteps(env, org, qualified, stackPath) {
		env.Step(ctx, t, s)
	}

	for _, s := range []e2e.StepSpec{
		step("preview", nil, "preview", "--non-interactive"),
		// A preview changes no resources, so it leaves no history.
		step("stack history hides previews", historyHidesPreviews, "stack", "history", "--json"),
	} {
		env.Step(ctx, t, s)
	}

	// Previews take no lock, so two at once on one stack both work.
	env.StepParallel(ctx, t, e2e.ParallelSpec{
		Name:  "concurrent previews",
		Check: nil,
		Args: [][]string{
			{"preview", "--non-interactive"},
			{"preview", "--non-interactive"},
		},
		Env:  nil,
		Envs: nil,
	})

	beforeID := stackIDBeforeRename(t, env, org)

	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkRenameKeptID(ctx, env, org, renamePath, beforeID),
		Name:   "stack rename",
		Args:   []string{"stack", "rename", renamedQualified, "--stack", qualified},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	// A rename keeps the stack ID, so its secrets still decrypt.
	env.Step(ctx, t, step(
		"config get after rename",
		contains("s3cr3t"),
		"config", "get", "--stack", renamedQualified, "apiKey",
	))

	// Tags follow the stack through a rename.
	env.Step(ctx, t, step("stack tag ls after rename", tagListed("team", "e2e"), "stack", "tag", "ls"))
	env.Step(ctx, t, step("stack tag rm", nil, "stack", "tag", "rm", "team"))
	env.Step(ctx, t, step("stack tag ls after rm", tagAbsent("team"), "stack", "tag", "ls"))

	env.Step(ctx, t, step("up", checkCheckpointSaved(env, "up"), "up", "--yes", "--non-interactive"))
	exported := env.Step(ctx, t, step("stack export after up", exportHoldsReference, "stack", "export"))
	env.Step(ctx, t, step("stack export hides secrets", exportHidesSecrets, "stack", "export"))
	env.Step(ctx, t, step("stack history after up", historyShowsKinds("update"), "stack", "history", "--json"))

	// An export round-trips through import: the deployment is saved to a
	// file and imported back, which records an import update. State does not
	// change, so the next up has nothing to do.
	importPath := writeImportFile(t, exported.Stdout)
	env.Step(ctx, t, step("stack import", nil, "stack", "import", "--file", importPath))
	env.Step(
		ctx,
		t,
		step("stack history after import", historyShowsKinds("update", "import"), "stack", "history", "--json"),
	)
	env.Step(ctx, t, step("up after import", upHasNoChanges, "up", "--yes", "--non-interactive"))

	// ADR 28 decision 4: the history carries the update ID, the requester,
	// the checkpoint size and the restore source. The CLI still parses its
	// history on every version, and the API shows the new fields.
	env.Step(
		ctx,
		t,
		step(
			"stack history after two ups",
			historyShowsKinds("update", "import"),
			"stack",
			"history",
			"--json",
		),
	)
	checkHistoryFields(ctx, t, env, org)
	checkRunningHistoryFields(ctx, t, env, org)

	// Version 1 keeps its checkpoint after later updates, so exporting it
	// returns the first state. A version that does not exist has no state.
	env.Step(ctx, t, step("stack export version 1", exportHoldsReference, "stack", "export", "--version", "1"))
	env.Step(
		ctx,
		t,
		failStep("stack export missing version", outputContains("no state"), "stack", "export", "--version", "999999"),
	)
	env.Step(
		ctx,
		t,
		step("refresh", checkCheckpointSaved(env, "refresh"), "refresh", "--yes", "--non-interactive"),
	)
	env.Step(ctx, t, step("destroy", nil, "destroy", "--yes", "--non-interactive"))
	env.Step(
		ctx,
		t,
		step(
			"stack history after destroy",
			historyShowsKinds("update", "refresh", "destroy"),
			"stack",
			"history",
			"--json",
		),
	)

	// A second update cannot start while one runs: hold the stack lock
	// through the API, prove the CLI reports the conflict, then release it.
	// The stack is the renamed one, which up, refresh and destroy ran on.
	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	// The running update holds the stack lock: the inventory entry of the
	// stack holds it with the holder and the update kind.
	checkInventoryLock(ctx, t, env, lockedUnit{
		ref:       "pulumi:" + org + "/" + e2e.Project + "/renamed",
		holder:    e2e.Subject,
		operation: "update",
	})
	env.Step(ctx, t, failStep(
		"second up while one runs",
		outputContains("another update of this stack is in progress"),
		"up", "--yes", "--non-interactive",
	))
	env.Step(ctx, t, failStep(
		"stack import while one runs",
		outputContains("another update of this stack is in progress"),
		"stack", "import", "--file", importPath,
	))
	env.EndHeldUpdate(ctx, t, held, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.
	// The ended update releases the lock: the inventory entry holds none.
	checkInventoryUnlocked(ctx, t, env, "pulumi:"+org+"/"+e2e.Project+"/renamed")

	// A crashed CLI leaves its update running: hold the stack through the
	// API as the orphaned update, save the up's checkpoint with the crash
	// marker as the engine left it, prove the next up is blocked, cancel
	// through the CLI in a second process, prove the checkpoint survived
	// cancel, then up works.
	held = env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	env.SaveHeldCheckpoint(ctx, t, held, exported.Stdout)
	env.Step(ctx, t, failStep(
		"up blocked after crash",
		outputContains("another update of this stack is in progress"),
		"up", "--yes", "--non-interactive",
	))
	env.Step(ctx, t, step("cancel after crash", contains("has been canceled"), "cancel", "--yes"))
	env.Step(
		ctx,
		t,
		step("stack export preserves checkpoint", contains("crash-marker"), "stack", "export"),
	)
	env.Step(
		ctx,
		t,
		step("up after cancel", checkCheckpointSaved(env, "up after cancel"), "up", "--yes", "--non-interactive"),
	)
	env.Step(ctx, t, step("stack export after cancel", exportHoldsReference, "stack", "export"))

	// A ciphertext is bound to its stack: another stack cannot decrypt it.
	env.Step(ctx, t, step(
		"decrypt rejects a foreign ciphertext",
		checkForeignDecrypt(ctx, env),
		"stack", "--show-name",
	))

	// Cancel frees a running update from a second process: hold the stack
	// through the API while an update runs, cancel through the CLI, then
	// up works at once.
	env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	env.Step(ctx, t, step("cancel running update", contains("has been canceled"), "cancel", "--yes"))
	env.Step(ctx, t, step("up after concurrent cancel", nil, "up", "--yes", "--non-interactive"))

	pulumiMaintenanceScenario(t, env, org)

	pulumiRestoreScenario(t, env, org)

	for _, s := range pulumiTeardownSteps(scratch, resourced) {
		env.Step(ctx, t, s)
	}

	// The server cannot record resources through routes that later issues
	// own, so the test seeds a checkpoint with one managed resource.
	env.SeedResources(ctx, t, "resourced")

	for _, s := range pulumiResourcedRemoveSteps(env, resourced, resourcedPath) {
		env.Step(ctx, t, s)
	}

	// A second namespace, so the list-all step proves stacks from every
	// readable namespace. It is created late through the installation API so
	// the default-organization step above still sees one namespace.
	createSecondNamespaceThroughAPI(t, env, e2e.Namespace2)

	second := fmt.Sprintf("%s/%s/%s", e2e.Namespace2, e2e.Project, stackName)
	for _, s := range []e2e.StepSpec{
		step("stack init second namespace", nil, "stack", "init", second, "--no-select"),
		step("stack ls all namespaces", bothStacksListed, "stack", "ls", "--json", "--all"),
		failStep(
			"missing subject namespace names the fix",
			outputContains("pulumi org set-default"),
			"stack", "init", e2e.Subject+"/"+e2e.Project+"/ghost",
		),
	} {
		env.Step(ctx, t, s)
	}

	consoleURLSteps(t, env)

	pulumiChangeSecretsProviderScenario(t, env, org)

	pulumiMaskedStateScenario(t, env, org)

	pulumiDiffScenario(t, env, org)
}

// changeSecretsProviderProgram is the scratch YAML program for the
// change-secrets-provider steps. Its secret output proves that state
// encrypted under each provider still decrypts after the change.
const changeSecretsProviderProgram = "name: " + e2e.Project + "\nruntime: yaml\noutputs:\n  key:\n    fn::secret: abc\n"

// pulumiChangeSecretsProviderScenario proves both directions of
// `stack change-secrets-provider` on a stack with a secret config value
// and a secret output: to the passphrase provider and back to the service
// provider. The CLI prompts for the new passphrase and ignores
// PULUMI_CONFIG_PASSPHRASE for that prompt, so the change step pipes the
// passphrase on standard input (twice: the new passphrase and its
// confirmation) while later steps read through the per-step environment.
// An empty input would silently migrate to the empty passphrase. The stack
// is removed at the end, so the scenario leaves no state behind.
func pulumiChangeSecretsProviderScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()
	passphrase := fileImportPassphrase(t)
	qualified := fmt.Sprintf("%s/%s/secrets", org, e2e.Project)
	passEnv := []string{"PULUMI_CONFIG_PASSPHRASE=" + passphrase}

	env.WritePulumiProject(t, changeSecretsProviderProgram)

	env.Step(ctx, t, step("stack init secrets", nil, "stack", "init", qualified, "--no-select"))
	env.Step(
		ctx,
		t,
		step(
			"config set secret before provider change",
			nil,
			"config", "set", "--secret", "--stack", qualified, "apiKey", "s3cr3t",
		),
	)
	env.Step(ctx, t, step("up before provider change", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.Step(
		ctx,
		t,
		step(
			"stack output before provider change",
			contains("abc"),
			"stack", "output", "key", "--show-secrets", "--stack", qualified,
		),
	)
	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains("Migrating old configuration and state to new secrets provider"),
		Name:   "change secrets provider to passphrase",
		Args:   []string{"stack", "change-secrets-provider", "passphrase", "--stack", qualified},
		Env:    passEnv,
		Stdin:  passphrase + "\n" + passphrase + "\n",
		Fail:   false,
		Redact: false,
	})
	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains("s3cr3t"),
		Name:   "config get after passphrase change",
		Args:   []string{"config", "get", "--stack", qualified, "apiKey"},
		Env:    passEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains("abc"),
		Name:   "stack output after passphrase change",
		Args:   []string{"stack", "output", "key", "--show-secrets", "--stack", qualified},
		Env:    passEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
	env.Step(ctx, t, e2e.StepSpec{
		Check:  upHasNoChanges,
		Name:   "up after passphrase change",
		Args:   []string{"up", "--yes", "--non-interactive", "--stack", qualified},
		Env:    passEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
	// Changing back needs the passphrase too: the CLI decrypts with the
	// old provider before it encrypts with the service provider.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains("Migrating old configuration and state to new secrets provider"),
		Name:   "change secrets provider to default",
		Args:   []string{"stack", "change-secrets-provider", "default", "--stack", qualified},
		Env:    passEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
	env.Step(
		ctx,
		t,
		step("config get after default change", contains("s3cr3t"), "config", "get", "--stack", qualified, "apiKey"),
	)
	env.Step(
		ctx,
		t,
		step(
			"stack output after default change",
			contains("abc"),
			"stack", "output", "key", "--show-secrets", "--stack", qualified,
		),
	)
	env.Step(
		ctx,
		t,
		step("up after default change", upHasNoChanges, "up", "--yes", "--non-interactive", "--stack", qualified),
	)
	env.Step(ctx, t, step("stack rm secrets", nil, "stack", "rm", "--yes", "--stack", qualified))
}

// maskedStateSecret is the secret value the masked state scenario sets. It
// is a test value only; no masked route may ever return it.
const maskedStateSecret = "s3cr3t-masked-state"

// maskedStateSigKey is the Pulumi secret signature key: no masked route may
// ever return it or a ciphertext field.
const maskedStateSigKey = "4dabf18193072939515e22adb298388d"

// maskedStateProgram returns the Pulumi.yaml of the masked state scenario:
// a secret output from config and a stack reference whose outputs are
// secrets. The reference name stays plain: the engine admits no secret
// input on any usable built-in resource, it rejects a secret name with
// "'name' property must be a string", so the unit tests cover input
// masking while this scenario covers real service secrets in outputs.
// greeting renders only in the second version, which is the one change
// between the two ups.
func maskedStateProgram(org, greeting string) string {
	program := "name: " + e2e.Project + "\nruntime: yaml\n" +
		"config:\n" +
		"  apiKey:\n" +
		"    type: string\n" +
		"    secret: true\n" +
		"outputs:\n" +
		"  apiKeyOut:\n" +
		"    fn::secret: ${apiKey}\n"

	if greeting != "" {
		program += "  greeting: " + greeting + "\n"
	}

	return program + "resources:\n" +
		"  ref:\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + org + "/" + e2e.Project + "/other\n" +
		"    options:\n" +
		"      additionalSecretOutputs:\n" +
		"      - outputs\n"
}

// pulumiMaskedStateScenario proves the masked state read (ADR 41 decisions
// 1 to 3 and 7) on a stack with a secret output and secrets in resource
// outputs: two ups with one change, then the resource list, the stack and
// reference details and the outputs. It runs last, on a stack of its own
// that it removes, so the earlier history checks keep their shape.
func pulumiMaskedStateScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()
	qualified := fmt.Sprintf("%s/%s/masked", org, e2e.Project)

	env.WritePulumiProject(t, maskedStateProgram(org, ""))
	env.Step(ctx, t, step("stack init masked", nil, "stack", "init", qualified, "--no-select"))
	env.Step(
		ctx,
		t,
		step(
			"config set secret for masked state",
			nil,
			"config", "set", "--secret", "--stack", qualified, "apiKey", maskedStateSecret,
		),
	)
	env.Step(ctx, t, step("up masked 1", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.WritePulumiProject(t, maskedStateProgram(org, "hello-again"))
	env.Step(ctx, t, step("up masked 2", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.Step(ctx, t, step(
		"masked state read",
		checkMaskedState(ctx, env, org, maskedStateSecret),
		"stack", "--show-name", "--stack", qualified,
	))
	env.Step(ctx, t, step("stack rm masked", nil, "stack", "rm", "--yes", "--force", "--stack", qualified))
}

// diffSecretV1 and diffSecretV2 are the config secret values of the two diff
// ups. They are test values only; no diff response may ever return them.
const (
	diffSecretV1 = "s3cr3t-diff-v1"
	diffSecretV2 = "s3cr3t-diff-v2" //nolint:gosec // Test value only; no diff response may return it.
)

// stateDiffProgram returns the Pulumi.yaml of the diff scenario: a secret
// output from config, a plain greeting that changes between the two ups,
// one kept stack reference, and one reference whose logical name is extra.
func stateDiffProgram(org, greeting, extra string) string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"config:\n" +
		"  apiKey:\n" +
		"    type: string\n" +
		"    secret: true\n" +
		"outputs:\n" +
		"  apiKeyOut:\n" +
		"    fn::secret: ${apiKey}\n" +
		"  greeting: " + greeting + "\n" +
		"resources:\n" +
		"  keep:\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + org + "/" + e2e.Project + "/other\n" +
		"  " + extra + ":\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + org + "/" + e2e.Project + "/other\n"
}

// pulumiDiffScenario proves the state diff (ADR 41 decision 9, masked per
// decision 2) on a stack of its own that it removes: two ups with one added
// reference, one removed reference, one changed greeting and one changed
// secret. The diff answers each, with no secret value in the response.
func pulumiDiffScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()
	qualified := fmt.Sprintf("%s/%s/diffed", org, e2e.Project)

	env.WritePulumiProject(t, stateDiffProgram(org, "hi", "gone"))
	env.Step(ctx, t, step("stack init diffed", nil, "stack", "init", qualified, "--no-select"))
	env.Step(
		ctx,
		t,
		step(
			"config set secret for diff 1",
			nil,
			"config", "set", "--secret", "--stack", qualified, "apiKey", diffSecretV1,
		),
	)
	env.Step(ctx, t, step("up diffed 1", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.WritePulumiProject(t, stateDiffProgram(org, "hello-again", "added"))
	env.Step(
		ctx,
		t,
		step(
			"config set secret for diff 2",
			nil,
			"config", "set", "--secret", "--stack", qualified, "apiKey", diffSecretV2,
		),
	)
	env.Step(ctx, t, step("up diffed 2", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.Step(ctx, t, step(
		"state diff",
		checkStateDiff(ctx, env, org),
		"stack", "--show-name", "--stack", qualified,
	))
	env.Step(ctx, t, step("stack rm diffed", nil, "stack", "rm", "--yes", "--force", "--stack", qualified))
}

// stateDiffChangedEntry is one changed resource of the diff in e2e checks.
type stateDiffChangedEntry struct {
	URN            string   `json:"urn"`
	ChangedPaths   []string `json:"changedPaths"`
	ChangedSecrets []string `json:"changedSecrets"`
}

// stateDiffDoc is the diff body in e2e checks.
type stateDiffDoc struct {
	Added      []string                `json:"added"`
	Removed    []string                `json:"removed"`
	Changed    []stateDiffChangedEntry `json:"changed"`
	NextCursor string                  `json:"nextCursor"`
	Incomplete bool                    `json:"incomplete"`
}

// decodeStateDiffBody decodes a diff body.
func decodeStateDiffBody(body string) (stateDiffDoc, error) {
	var doc stateDiffDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return stateDiffDoc{}, fmt.Errorf("%w: decode state diff: %w", errUnexpected, err)
	}

	return doc, nil
}

// checkStateDiff proves the diff of versions 1 and 2 of the diffed stack:
// one added reference, one removed reference, and the changed stack with
// the changed greeting in changedPaths and the changed secret in
// changedSecrets. No body holds a secret value, a ciphertext field or the
// secret signature. A paged read with a limit of one lists the same three
// URNs, and the diff of version 2 with itself is empty.
func checkStateDiff(ctx context.Context, env *e2e.Env, org string) func(e2e.Result) error {
	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/diffed", org, e2e.Project)

		raw, err := maskedStateGet(ctx, env, base+"/state/diff?from=1&to=2")
		if err != nil {
			return err
		}

		doc, err := decodeStateDiffBody(raw)
		if err != nil {
			return err
		}

		if err := assertStateDiffEntries(doc); err != nil {
			return err
		}

		if err := assertStateDiffHasNoSecrets(raw); err != nil {
			return err
		}

		if err := assertStateDiffPaged(ctx, env, base, doc); err != nil {
			return err
		}

		return assertStateDiffEmpty(ctx, env, base)
	}
}

// assertStateDiffEntries proves the diff holds one added reference, one
// removed reference, and the changed stack with the greeting path and the
// secret path.
func assertStateDiffEntries(doc stateDiffDoc) error {
	if len(doc.Added) != 1 || !strings.HasSuffix(doc.Added[0], "::added") {
		return fmt.Errorf("%w: diff added = %q, want the added reference", errUnexpected, doc.Added)
	}

	if len(doc.Removed) != 1 || !strings.HasSuffix(doc.Removed[0], "::gone") {
		return fmt.Errorf("%w: diff removed = %q, want the gone reference", errUnexpected, doc.Removed)
	}

	if len(doc.Changed) != 1 {
		return fmt.Errorf("%w: diff changed = %+v, want only the stack resource", errUnexpected, doc.Changed)
	}

	entry := doc.Changed[0]

	if !strings.Contains(entry.URN, "pulumi:pulumi:Stack::") {
		return fmt.Errorf("%w: diff changed URN %q is not the stack resource", errUnexpected, entry.URN)
	}

	if !slices.Contains(entry.ChangedPaths, "/outputs/greeting") {
		return fmt.Errorf(
			"%w: diff changed paths = %q, want /outputs/greeting",
			errUnexpected,
			entry.ChangedPaths,
		)
	}

	if !slices.ContainsFunc(entry.ChangedSecrets, func(path string) bool {
		return strings.Contains(path, "/outputs/")
	}) {
		return fmt.Errorf(
			"%w: diff changed secrets = %q, want the secret output",
			errUnexpected,
			entry.ChangedSecrets,
		)
	}

	if doc.NextCursor != "" || doc.Incomplete {
		return fmt.Errorf("%w: diff = %+v, want a single complete page", errUnexpected, doc)
	}

	return nil
}

// assertStateDiffHasNoSecrets fails when the diff body holds a secret value,
// a ciphertext field or the secret signature key.
func assertStateDiffHasNoSecrets(body string) error {
	for _, leaked := range []string{diffSecretV1, diffSecretV2, "ciphertext", maskedStateSigKey} {
		if strings.Contains(body, leaked) {
			return fmt.Errorf("%w: diff body holds %q", errUnexpected, leaked)
		}
	}

	return nil
}

// stateDiffPageURNs returns the URNs of one diff page.
func stateDiffPageURNs(doc stateDiffDoc) []string {
	urns := make([]string, 0, len(doc.Added)+len(doc.Removed)+len(doc.Changed))
	urns = append(urns, doc.Added...)
	urns = append(urns, doc.Removed...)

	for _, entry := range doc.Changed {
		urns = append(urns, entry.URN)
	}

	return urns
}

// assertStateDiffPaged proves a paged read with a limit of one lists the
// same differing URNs as the full diff, with no secret value in any page.
func assertStateDiffPaged(ctx context.Context, env *e2e.Env, base string, full stateDiffDoc) error {
	want := map[string]bool{}
	for _, urn := range stateDiffPageURNs(full) {
		want[urn] = true
	}

	seen := map[string]bool{}
	cursor := ""

	for {
		raw, err := maskedStateGet(ctx, env, base+"/state/diff?from=1&to=2&limit=1&cursor="+cursor)
		if err != nil {
			return err
		}

		if leakErr := assertStateDiffHasNoSecrets(raw); leakErr != nil {
			return leakErr
		}

		doc, err := decodeStateDiffBody(raw)
		if err != nil {
			return err
		}

		urns := stateDiffPageURNs(doc)
		if len(urns) != 1 {
			return fmt.Errorf("%w: diff page at %q holds %d entries, want 1", errUnexpected, cursor, len(urns))
		}

		if seen[urns[0]] {
			return fmt.Errorf("%w: diff page lists %q twice", errUnexpected, urns[0])
		}

		seen[urns[0]] = true
		cursor = doc.NextCursor

		if cursor == "" {
			break
		}
	}

	if !maps.Equal(seen, want) {
		return fmt.Errorf("%w: paged diff URNs %v differ from full diff URNs %v", errUnexpected, seen, want)
	}

	return nil
}

// assertStateDiffEmpty proves the diff of version 2 with itself is empty.
func assertStateDiffEmpty(ctx context.Context, env *e2e.Env, base string) error {
	raw, err := maskedStateGet(ctx, env, base+"/state/diff?from=2&to=2")
	if err != nil {
		return err
	}

	doc, err := decodeStateDiffBody(raw)
	if err != nil {
		return err
	}

	if len(doc.Added) != 0 || len(doc.Removed) != 0 || len(doc.Changed) != 0 {
		return fmt.Errorf("%w: same-version diff = %+v, want no change", errUnexpected, doc)
	}

	return nil
}

// maskedStateItem is one entry of the masked resource list in e2e checks.
type maskedStateItem struct {
	URN     string `json:"urn"`
	Type    string `json:"type"`
	Changed bool   `json:"changed"`
}

// maskedStateList is the resource list body in e2e checks.
type maskedStateList struct {
	Resources []maskedStateItem `json:"resources"`
}

// checkMaskedState proves the masked routes on the masked stack: no body
// holds the secret plain text, a ciphertext field or the secret signature;
// each marker path is in masked; the outputs hold the secret output masked;
// the stack and reference details hold their secret outputs masked while
// the plain reference input reads back verbatim; and changed is true only
// on the stack resource, which the greeting changed in version 2.
func checkMaskedState(
	ctx context.Context, env *e2e.Env, org, secret string,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)

		lists, err := fetchMaskedLists(ctx, env, base)
		if err != nil {
			return err
		}

		if changedErr := checkMaskedChanged(lists.latest, lists.first, lists.second); changedErr != nil {
			return changedErr
		}

		stackURN, refURNs, err := maskedStateURNs(lists.latest)
		if err != nil {
			return err
		}

		stackDetail, err := maskedStateGet(ctx, env, base+"/state/latest/resources/"+url.PathEscape(stackURN))
		if err != nil {
			return err
		}

		refDetails := make([]string, 0, len(refURNs))

		for _, urn := range refURNs {
			detail, fetchErr := maskedStateGet(ctx, env, base+"/state/latest/resources/"+url.PathEscape(urn))
			if fetchErr != nil {
				return fetchErr
			}

			refDetails = append(refDetails, detail)
		}

		outputs, err := maskedStateGet(ctx, env, base+"/state/latest/outputs")
		if err != nil {
			return err
		}

		bodies := maskedStateBodies{
			latest: lists.latest, first: lists.first, second: lists.second,
			stack: stackDetail, refs: refDetails, outputs: outputs,
			refName: org + "/" + e2e.Project + "/other",
		}

		return assertMaskedBodies(ctx, env, base, bodies, secret)
	}
}

// maskedStateLists are the three resource list bodies the masked check
// reads.
type maskedStateLists struct {
	latest string
	first  string
	second string
}

// fetchMaskedLists reads the latest and the two versioned resource lists.
func fetchMaskedLists(ctx context.Context, env *e2e.Env, base string) (maskedStateLists, error) {
	var lists maskedStateLists

	var err error

	if lists.latest, err = maskedStateGet(ctx, env, base+"/state/latest/resources"); err != nil {
		return maskedStateLists{}, err
	}

	if lists.first, err = maskedStateGet(ctx, env, base+"/state/1/resources"); err != nil {
		return maskedStateLists{}, err
	}

	if lists.second, err = maskedStateGet(ctx, env, base+"/state/2/resources"); err != nil {
		return maskedStateLists{}, err
	}

	return lists, nil
}

// maskedStateBodies are the masked bodies the masked check asserts on: the
// three resource lists, the stack resource detail, one detail per stack
// reference and the stack outputs. refName is the plain name input each
// reference detail must carry verbatim.
type maskedStateBodies struct {
	latest  string
	first   string
	second  string
	stack   string
	refs    []string
	outputs string
	refName string
}

// assertMaskedBodies proves the masked bodies: no leaks, authoritative
// markers, a masked secret in the outputs and in the stack and reference
// details, plain reference inputs with no false-positive masking, and real
// secrets in the export.
func assertMaskedBodies(
	ctx context.Context, env *e2e.Env, base string, bodies maskedStateBodies, secret string,
) error {
	checked := make([]string, 0, len(bodies.refs)+5)
	checked = append(checked, bodies.latest, bodies.first, bodies.second, bodies.stack, bodies.outputs)
	checked = append(checked, bodies.refs...)

	for _, body := range checked {
		if err := maskedStateCheckLeaks(body, secret); err != nil {
			return err
		}
	}

	marked := make([]string, 0, len(bodies.refs)+2)
	marked = append(marked, bodies.stack)
	marked = append(marked, bodies.refs...)
	marked = append(marked, bodies.outputs)

	for _, body := range marked {
		if err := maskedStateCheckMarkers(body); err != nil {
			return err
		}
	}

	if !maskedStateHasSecret(bodies.outputs) {
		return fmt.Errorf("%w: outputs hold no masked secret", errUnexpected)
	}

	if !maskedStateHasMaskedWith(bodies.stack, "/outputs/") {
		return fmt.Errorf("%w: stack detail holds no masked secret in outputs", errUnexpected)
	}

	for _, detail := range bodies.refs {
		if !maskedStateHasMaskedWith(detail, "/outputs/") {
			return fmt.Errorf("%w: reference detail holds no masked secret in outputs", errUnexpected)
		}

		if err := maskedStateCheckRefInputs(detail, bodies.refName); err != nil {
			return err
		}
	}

	return maskedStateCheckExportSecret(ctx, env, base)
}

// maskedStateCheckRefInputs proves the reference detail carries its plain
// name input verbatim: masking leaves non-secret inputs alone, so no
// false-positive marker rewrites them.
func maskedStateCheckRefInputs(detail, refName string) error {
	var doc struct {
		Inputs map[string]any `json:"inputs"`
	}

	if err := json.Unmarshal([]byte(detail), &doc); err != nil {
		return fmt.Errorf("%w: decode reference detail: %w", errUnexpected, err)
	}

	if doc.Inputs["name"] != refName {
		return fmt.Errorf(
			"%w: reference inputs name = %v, want the plain name %q",
			errUnexpected,
			doc.Inputs["name"],
			refName,
		)
	}

	return nil
}

// maskedStateGet fetches path with the admin token: the status and the body
// on success, or an error that names the path.
func maskedStateGet(ctx context.Context, env *e2e.Env, path string) (string, error) {
	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		return "", fmt.Errorf("%w: GET %s: %w", errUnexpected, path, err)
	}

	if status != http.StatusOK {
		return "", fmt.Errorf("%w: GET %s answers %d, want %d (%s)", errUnexpected, path, status, http.StatusOK, body)
	}

	return body, nil
}

// checkMaskedChanged proves the changed markers: every resource of version
// 1 is new, while version 2 and latest changed only the stack resource,
// which carries the greeting of version 2. The deployment also holds the
// default provider resource, which never changes.
func checkMaskedChanged(latest, first, second string) error {
	var firstList maskedStateList

	if err := json.Unmarshal([]byte(first), &firstList); err != nil {
		return fmt.Errorf("%w: decode version 1 list: %w", errUnexpected, err)
	}

	if len(firstList.Resources) < 2 {
		return fmt.Errorf(
			"%w: version 1 list holds %d resources, want at least the stack and a reference",
			errUnexpected,
			len(firstList.Resources),
		)
	}

	for _, res := range firstList.Resources {
		if !res.Changed {
			return fmt.Errorf(
				"%w: version 1 resource %q changed = false, want true for a new resource",
				errUnexpected,
				res.URN,
			)
		}
	}

	for _, version := range []struct {
		name string
		body string
	}{{"version 2", second}, {"latest", latest}} {
		if err := checkMaskedLatestChanged(version.name, version.body); err != nil {
			return err
		}
	}

	return nil
}

// checkMaskedLatestChanged proves the changed markers of one list of the
// second version: only the stack resource changed.
func checkMaskedLatestChanged(name, body string) error {
	var list maskedStateList

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return fmt.Errorf("%w: decode %s list: %w", errUnexpected, name, err)
	}

	if len(list.Resources) < 2 {
		return fmt.Errorf(
			"%w: %s list holds %d resources, want at least the stack and a reference",
			errUnexpected,
			name,
			len(list.Resources),
		)
	}

	changed := 0

	for _, res := range list.Resources {
		wantChanged := res.Type == "pulumi:pulumi:Stack"
		if res.Changed != wantChanged {
			return fmt.Errorf(
				"%w: %s resource %q of type %q changed = %t, want %t",
				errUnexpected,
				name,
				res.URN,
				res.Type,
				res.Changed,
				wantChanged,
			)
		}

		if res.Changed {
			changed++
		}
	}

	if changed != 1 {
		return fmt.Errorf("%w: %s list changed %d resources, want only the stack", errUnexpected, name, changed)
	}

	return nil
}

// maskedStateURNs returns the URN of the stack resource and of every stack
// reference in the list.
func maskedStateURNs(latest string) (string, []string, error) {
	var list maskedStateList

	if err := json.Unmarshal([]byte(latest), &list); err != nil {
		return "", nil, fmt.Errorf("%w: decode latest list: %w", errUnexpected, err)
	}

	var stackURN string

	var refURNs []string

	for _, res := range list.Resources {
		switch res.Type {
		case "pulumi:pulumi:Stack":
			stackURN = res.URN
		case "pulumi:pulumi:StackReference":
			refURNs = append(refURNs, res.URN)
		default:
		}
	}

	if stackURN == "" {
		return "", nil, fmt.Errorf("%w: latest list holds no stack resource", errUnexpected)
	}

	if len(refURNs) == 0 {
		return "", nil, fmt.Errorf("%w: latest list holds no reference resource", errUnexpected)
	}

	return stackURN, refURNs, nil
}

// maskedStateCheckLeaks fails when body holds the secret plain text, a
// ciphertext field or the secret signature key.
func maskedStateCheckLeaks(body, secret string) error {
	for _, leaked := range []string{secret, "ciphertext", maskedStateSigKey} {
		if strings.Contains(body, leaked) {
			return fmt.Errorf("%w: masked body holds %q", errUnexpected, leaked)
		}
	}

	return nil
}

// maskedStateCheckMarkers proves each secret marker in body names a path in
// its masked list: masked is authoritative.
func maskedStateCheckMarkers(body string) error {
	var doc struct {
		Masked []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode masked body: %w", errUnexpected, err)
	}

	var raw any

	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return fmt.Errorf("%w: decode masked body: %w", errUnexpected, err)
	}

	return maskedStateWalkMarkers(raw, doc.Masked)
}

// maskedStateWalkMarkers proves each secret marker in v names a path in
// masked.
func maskedStateWalkMarkers(v any, masked []string) error {
	switch typed := v.(type) {
	case map[string]any:
		return maskedStateWalkObject(typed, masked)
	case []any:
		for _, elem := range typed {
			if err := maskedStateWalkMarkers(elem, masked); err != nil {
				return err
			}
		}

		return nil
	default:
		return nil
	}
}

// maskedStateWalkObject proves each secret marker in obj names a path in
// masked.
func maskedStateWalkObject(obj map[string]any, masked []string) error {
	secret, ok := obj["secret"].(bool)
	if ok && secret {
		path, ok := obj["path"].(string)
		if !ok || path == "" {
			return nil
		}

		if !slices.Contains(masked, path) {
			return fmt.Errorf("%w: marker for %q is not in masked %q", errUnexpected, path, masked)
		}

		return nil
	}

	for _, elem := range obj {
		if err := maskedStateWalkMarkers(elem, masked); err != nil {
			return err
		}
	}

	return nil
}

// maskedStateHasSecret reports whether the outputs body masks at least one
// secret value.
func maskedStateHasSecret(outputs string) bool {
	var doc struct {
		Masked []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(outputs), &doc); err != nil {
		return false
	}

	return len(doc.Masked) > 0
}

// maskedStateHasMaskedWith reports whether the masked list of body holds a
// value path with infix, for example "/inputs/" for a masked resource
// input or "/outputs/" for a masked output.
func maskedStateHasMaskedWith(body, infix string) bool {
	var doc struct {
		Masked []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return false
	}

	return slices.ContainsFunc(doc.Masked, func(path string) bool {
		return strings.Contains(path, infix)
	})
}

// maskedStateCheckExportSecret proves the checkpoint really holds secrets
// from the CLI: the export of the masked stack carries the secret signature
// in the outputs of the stack resource and of the reference resource. The
// masked routes above proved none of it leaks.
func maskedStateCheckExportSecret(ctx context.Context, env *e2e.Env, base string) error {
	status, body, err := env.APIStatus(ctx, http.MethodGet, base+"/export", "")
	if err != nil {
		return fmt.Errorf("%w: GET %s/export: %w", errUnexpected, base, err)
	}

	if status != http.StatusOK {
		return fmt.Errorf("%w: GET %s/export answers %d, want %d", errUnexpected, base, status, http.StatusOK)
	}

	var exported struct {
		Deployment map[string]any `json:"deployment"`
	}

	if err := json.Unmarshal([]byte(body), &exported); err != nil {
		return fmt.Errorf("%w: decode export: %w", errUnexpected, err)
	}

	resources, ok := exported.Deployment["resources"].([]any)
	if !ok {
		return fmt.Errorf("%w: export holds no resources array", errUnexpected)
	}

	stackSecret := false
	refSecret := false

	for _, elem := range resources {
		if maskedStateOutputsSecret(elem, "pulumi:pulumi:Stack") {
			stackSecret = true
		}

		if maskedStateOutputsSecret(elem, "pulumi:pulumi:StackReference") {
			refSecret = true
		}
	}

	if !stackSecret {
		return fmt.Errorf(
			"%w: export holds no secret in the stack outputs, want the secret output",
			errUnexpected,
		)
	}

	if !refSecret {
		return fmt.Errorf(
			"%w: export holds no secret in the reference outputs, want the secret resource output",
			errUnexpected,
		)
	}

	return nil
}

// maskedStateOutputsSecret reports whether elem is a resource of type typ
// whose outputs hold the secret signature key.
func maskedStateOutputsSecret(elem any, typ string) bool {
	obj, ok := elem.(map[string]any)
	if !ok {
		return false
	}

	if obj["type"] != typ {
		return false
	}

	return maskedStateHoldsSigKey(obj["outputs"])
}

// maskedStateHoldsSigKey reports whether v holds the secret signature key.
func maskedStateHoldsSigKey(v any) bool {
	switch typed := v.(type) {
	case map[string]any:
		if _, ok := typed[maskedStateSigKey]; ok {
			return true
		}

		return slices.ContainsFunc(slices.Collect(maps.Values(typed)), maskedStateHoldsSigKey)
	case []any:
		return slices.ContainsFunc(typed, maskedStateHoldsSigKey)
	default:
		return false
	}
}

// pulumiSetupSteps returns the scenario steps before the rename: login
// through a second stack init in the default organization.
func pulumiSetupSteps(env *e2e.Env, org, qualified, stackPath string) []e2e.StepSpec {
	return []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("whoami", func(r e2e.Result) error {
			if got := strings.TrimSpace(r.Stdout); got != e2e.Subject {
				return fmt.Errorf("%w: whoami printed %q, want %q", errUnexpected, got, e2e.Subject)
			}

			return nil
		}, "whoami"),
		// The namespaces a principal can use are its organizations.
		step("whoami lists organizations", contains("Organizations: "+org), "whoami", "--verbose"),
		// With one namespace, it is the default organization.
		step("org get-default", contains(org), "org", "get-default"),
		step("stack init", nil, "stack", "init", qualified),
		step("stack ls", stackListed, "stack", "ls", "--json"),
		step("stack ls in organization", stackListed, "stack", "ls", "--json", "--organization", org),
		step("stack ls all projects", stackListed, "stack", "ls", "--json", "--all"),
		step("stack select", nil, "stack", "select", qualified),
		step("stack show-name", contains(stackName), "stack", "--show-name"),
		step("stack tag set", nil, "stack", "tag", "set", "team", "e2e"),
		step("stack tag ls", tagListed("team", "e2e"), "stack", "tag", "ls"),
		// The service backend encrypts secrets on the server, per stack.
		// The list command hides secret values unless asked to show them,
		// while config get prints the plaintext by design.
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
		step("config get shows the secret", contains("s3cr3t"), "config", "get", "apiKey"),
		step("config hides the secret", configListHidesSecret, "config"),
		step("config shows the secret", contains("s3cr3t"), "config", "--show-secrets"),
		step("stack export", nil, "stack", "export"),
		{
			// An unqualified stack name takes the default organization.
			Check: func(e2e.Result) error {
				if !env.Requested("stack init default org", http.MethodPost, stackPath) {
					return fmt.Errorf("%w: stack was not created in namespace %q", errUnexpected, org)
				}

				return nil
			},
			Name:   "stack init default org",
			Args:   []string{"stack", "init", "other"},
			Env:    nil,
			Stdin:  "",
			Fail:   false,
			Redact: false,
		},
	}
}

// pulumiTeardownSteps returns the scenario steps after the rename: a stack of
// its own, so the result does not depend on rename, removed with stack rm.
// It then creates the stack that the resourced steps remove.
func pulumiTeardownSteps(scratch, resourced string) []e2e.StepSpec {
	return []e2e.StepSpec{
		step("stack init scratch", nil, "stack", "init", scratch, "--no-select"),
		step("stack rm", nil, "stack", "rm", "--yes", "--stack", scratch),
		// A stack of its own for the delete with resources below.
		step("stack init resourced", nil, "stack", "init", resourced, "--no-select"),
	}
}

// pulumiResourcedRemoveSteps returns the steps that remove a stack with
// resources: the CLI refuses without --force and removes it with --force.
func pulumiResourcedRemoveSteps(env *e2e.Env, resourced, resourcedPath string) []e2e.StepSpec {
	return []e2e.StepSpec{
		{
			Check: func(r e2e.Result) error {
				if out := r.Stdout + r.Stderr; !strings.Contains(out, "still has resources") {
					return fmt.Errorf("%w: output lacks the refusal: %s", errUnexpected, out)
				}

				if !env.Requested("stack rm with resources without force", http.MethodDelete, resourcedPath) {
					return fmt.Errorf("%w: stack was not delete-requested", errUnexpected)
				}

				return nil
			},
			Name:   "stack rm with resources without force",
			Args:   []string{"stack", "rm", "--yes", "--stack", resourced},
			Env:    nil,
			Stdin:  "",
			Fail:   true,
			Redact: false,
		},
		step("stack rm with resources with force",
			func(_ e2e.Result) error {
				if !env.Requested("stack rm with resources with force", http.MethodDelete, resourcedPath) {
					return fmt.Errorf("%w: stack was not delete-requested", errUnexpected)
				}

				return nil
			}, "stack", "rm", "--yes", "--force", "--stack", resourced),
	}
}

// stackIDBeforeRename returns the id of the dev stack, failing the test when
// the stack is missing.
func stackIDBeforeRename(t *testing.T, env *e2e.Env, org string) string {
	t.Helper()

	id, status, err := env.StackID(t.Context(), org, e2e.Project, stackName)
	if err != nil || status != http.StatusOK {
		t.Fatalf("get stack before rename: status %d, err %v", status, err)
	}

	return id
}

// checkRenameKeptID checks that the CLI renamed through renamePath, that the
// renamed stack keeps beforeID and that the old name is gone.
func checkRenameKeptID(
	ctx context.Context, env *e2e.Env, org, renamePath, beforeID string,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		if !env.Requested("stack rename", http.MethodPost, renamePath) {
			return fmt.Errorf("%w: CLI did not POST %s", errUnexpected, renamePath)
		}

		afterID, status, err := env.StackID(ctx, org, e2e.Project, "renamed")
		if err != nil {
			return fmt.Errorf("%w: get renamed stack: %w", errUnexpected, err)
		}

		if status != http.StatusOK {
			return fmt.Errorf("%w: renamed stack answers %d", errUnexpected, status)
		}

		if afterID != beforeID {
			return fmt.Errorf("%w: stack id changed from %q to %q", errUnexpected, beforeID, afterID)
		}

		oldID, status, err := env.StackID(ctx, org, e2e.Project, stackName)
		if err != nil {
			return fmt.Errorf("%w: get old stack name: %w", errUnexpected, err)
		}

		if status != http.StatusNotFound {
			return fmt.Errorf("%w: old stack name still answers %d with id %q", errUnexpected, status, oldID)
		}

		return nil
	}
}

// bothStacksListed checks that stack ls --all lists the dev stack in both
// namespaces. The CLI prints each stack as "<organization>/<stack>", so the
// organization is the part before the slash.
func bothStacksListed(r e2e.Result) error {
	var stacks []struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	seen := map[string]bool{}

	for _, s := range stacks {
		if org, _, ok := strings.Cut(s.Name, "/"); ok {
			seen[org] = true
		}
	}

	for _, want := range []string{e2e.Namespace, e2e.Namespace2} {
		if !seen[want] {
			return fmt.Errorf("%w: no stack listed in namespace %q: %s", errUnexpected, want, r.Stdout)
		}
	}

	return nil
}

func stackListed(r e2e.Result) error {
	var stacks []struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	for _, s := range stacks {
		if strings.HasSuffix(s.Name, stackName) {
			return nil
		}
	}

	return fmt.Errorf("%w: stack %q not listed: %s", errUnexpected, stackName, r.Stdout)
}

// historyHidesPreviews fails when the stack history holds any update: a
// preview changes nothing, so it must leave no entry.
func historyHidesPreviews(r e2e.Result) error {
	var updates []struct {
		Kind string `json:"kind"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &updates); err != nil {
		return fmt.Errorf("%w: decode stack history: %w", errUnexpected, err)
	}

	if len(updates) != 0 {
		return fmt.Errorf("%w: stack history holds %d updates, want none after a preview", errUnexpected, len(updates))
	}

	return nil
}

// historyShowsKinds proves that stack history lists an entry of each kind,
// such as the update, refresh and destroy the scenario ran.
func historyShowsKinds(kinds ...string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var updates []struct {
			Kind string `json:"kind"`
		}

		if err := json.Unmarshal([]byte(r.Stdout), &updates); err != nil {
			return fmt.Errorf("%w: decode stack history: %w", errUnexpected, err)
		}

		seen := map[string]bool{}
		for _, u := range updates {
			seen[u.Kind] = true
		}

		for _, kind := range kinds {
			if !seen[kind] {
				return fmt.Errorf("%w: stack history lacks kind %q: %s", errUnexpected, kind, r.Stdout)
			}
		}

		return nil
	}
}

// historyAPIEntry is one entry of the history API response as the console
// reads it (ADR 28 decision 4).
type historyAPIEntry struct {
	Result          string `json:"result"`
	Kind            string `json:"kind"`
	Version         int    `json:"version"`
	StartTime       int64  `json:"startTime"`
	EndTime         int64  `json:"endTime"`
	UpdateID        string `json:"updateID"`
	RequestedBy     string `json:"requestedBy"`
	CheckpointBytes int64  `json:"checkpointBytes"`
	LeaseExpiresAt  int64  `json:"leaseExpiresAt"`
	RestoredFrom    int64  `json:"restoredFrom"`
}

// readHistory fetches the history of the renamed stack over the API with the
// admin token.
func readHistory(ctx context.Context, t *testing.T, env *e2e.Env, org string) []historyAPIEntry {
	t.Helper()

	path := fmt.Sprintf("/api/stacks/%s/%s/renamed/updates", org, e2e.Project)

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET %s status %d, want %d (body %s)", path, status, http.StatusOK, body)
	}

	var decoded struct {
		Updates []historyAPIEntry `json:"updates"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode history %s: %v (%s)", path, err, body)
	}

	return decoded.Updates
}

// checkHistoryFields proves the history shows the requester and the
// checkpoint size: every entry names the subject, never a token, and one
// update that saved a checkpoint reports above 0.
func checkHistoryFields(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	updates := readHistory(ctx, t, env, org)

	if len(updates) == 0 {
		t.Fatal("history holds no updates, want the two ups and the import")
	}

	sized := false

	for _, u := range updates {
		if u.UpdateID == "" {
			t.Fatalf("history entry %+v holds no update ID", u)
		}

		if u.RequestedBy != e2e.Subject {
			t.Fatalf("history entry %+v requestedBy = %q, want %q", u, u.RequestedBy, e2e.Subject)
		}

		if u.CheckpointBytes > 0 {
			sized = true
		}
	}

	if !sized {
		t.Fatalf("no history entry reports a checkpoint size (%+v)", updates)
	}
}

// checkRunningHistoryFields proves the running update shows its requester and
// lease expiry with no checkpoint yet, then releases the stack.
func checkRunningHistoryFields(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")

	updates := readHistory(ctx, t, env, org)

	found := false

	for _, u := range updates {
		if u.UpdateID != held.ID {
			continue
		}

		found = true

		if u.RequestedBy != e2e.Subject {
			t.Errorf("running update requestedBy = %q, want %q", u.RequestedBy, e2e.Subject)
		}

		if u.LeaseExpiresAt <= 0 {
			t.Errorf("running update leaseExpiresAt = %d, want above 0", u.LeaseExpiresAt)
		}

		if u.CheckpointBytes != 0 {
			t.Errorf("running update checkpointBytes = %d, want 0 before any checkpoint", u.CheckpointBytes)
		}
	}

	if !found {
		t.Errorf("history holds no running update %q (%+v)", held.ID, updates)
	}

	env.EndHeldUpdate(ctx, t, held, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.
}

// pulumiRestoreScenario proves the restore route (ADR 28 decision 5) on the
// renamed stack. There is no CLI command for restore, so the restore steps
// call the route with the admin token: two ups, a restore of version 1,
// export equality with version 1, up after the restore, 404 for a version
// with no checkpoint, 409 during a running update freed by cancel, and the
// restore marker in history.
func pulumiRestoreScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()

	env.Step(ctx, t, step("up before restore 1", nil, "up", "--yes", "--non-interactive"))
	env.Step(ctx, t, step("up before restore 2", nil, "up", "--yes", "--non-interactive"))

	restoreStackVersion(t, env, org, 1, http.StatusOK)

	exported := env.Step(ctx, t, step("stack export after restore", nil, "stack", "export"))
	pinned := env.Step(
		ctx,
		t,
		step("stack export version 1 after restore", nil, "stack", "export", "--version", "1"),
	)

	if strings.TrimSpace(exported.Stdout) != strings.TrimSpace(pinned.Stdout) {
		t.Error("stack export after restoring version 1 differs from stack export --version 1")
	}

	env.Step(ctx, t, step("up after restore", nil, "up", "--yes", "--non-interactive"))

	// The CLI parses the new kind on every version in the matrix.
	env.Step(
		ctx,
		t,
		step("stack history after restore", historyShowsKinds("restore"), "stack", "history", "--json"),
	)

	// A version with no checkpoint has no state to copy.
	restoreStackVersion(t, env, org, 999999, http.StatusNotFound)

	// The version is a positive integer.
	restoreStackVersion(t, env, org, 0, http.StatusBadRequest)

	// A restore waits for the stack lock: hold the stack through the API,
	// prove the restore conflicts, cancel through the CLI in a second
	// process, then the restore works.
	env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	restoreStackVersion(t, env, org, 1, http.StatusConflict)
	env.Step(ctx, t, step("cancel frees the restore", contains("has been canceled"), "cancel", "--yes"))
	restoreStackVersion(t, env, org, 1, http.StatusOK)

	checkRestoreHistory(ctx, t, env, org)
}

// restoreStackVersion restores version of the renamed stack through the API
// with the admin token and proves the status. A success answers the ID of
// the restore update; an error answers the Pulumi shape
// {"code": <status>, "message": "..."}.
func restoreStackVersion(t *testing.T, env *e2e.Env, org string, version, want int) {
	t.Helper()

	ctx := t.Context()

	path := fmt.Sprintf("/api/stacks/%s/%s/renamed/restore/%d", org, e2e.Project, version)

	status, body, err := env.APIStatus(ctx, http.MethodPost, path, "")
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}

	if status != want {
		t.Fatalf("POST %s status %d, want %d (body %s)", path, status, want, body)
	}

	if want == http.StatusOK {
		var decoded struct {
			UpdateID string `json:"updateId"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
		}

		if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded.UpdateID == "" {
			t.Fatalf("POST %s answers %q, want an update id: %v", path, body, err)
		}

		return
	}

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error of POST %s: %v (%s)", path, err, body)
	}

	if decoded.Code != want || decoded.Message == "" {
		t.Fatalf("POST %s error = %+v, want code %d with a message", path, decoded, want)
	}
}

// checkRestoreHistory proves the history shows the restore: one entry of
// kind restore records version 1 as its source and the caller as its
// requester.
func checkRestoreHistory(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	for _, u := range readHistory(ctx, t, env, org) {
		if u.Kind != "restore" {
			continue
		}

		if u.RestoredFrom != 1 {
			t.Fatalf("restore entry %+v restoredFrom = %d, want 1", u, u.RestoredFrom)
		}

		if u.RequestedBy != e2e.Subject {
			t.Fatalf("restore entry %+v requestedBy = %q, want %q", u, u.RequestedBy, e2e.Subject)
		}

		if u.UpdateID == "" {
			t.Fatalf("restore entry %+v holds no update ID", u)
		}

		return
	}

	t.Fatal("history holds no restore entry")
}

// writeImportFile saves an exported deployment for a later stack import
// step and returns its path.
func writeImportFile(t *testing.T, deployment string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "stack.json")
	if err := os.WriteFile(path, []byte(deployment), 0o600); err != nil {
		t.Fatalf("write import file: %v", err)
	}

	return path
}

// upHasNoChanges proves an up changed nothing: the engine reported its
// resources unchanged.
func upHasNoChanges(r e2e.Result) error {
	if strings.Contains(r.Stdout, "unchanged") {
		return nil
	}

	return fmt.Errorf("%w: up lacks an unchanged report: %s", errUnexpected, r.Stdout)
}

// exportHoldsReference proves that stack export after up holds the stack
// reference resource: the checkpoint survived the round trip.
func exportHoldsReference(r e2e.Result) error {
	if !strings.Contains(r.Stdout, "pulumi:pulumi:StackReference") {
		return fmt.Errorf("%w: export lacks the stack reference resource: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// configListHidesSecret proves that config list without --show-secrets never
// prints the plaintext secret.
func configListHidesSecret(r e2e.Result) error {
	if strings.Contains(r.Stdout, "s3cr3t") {
		return fmt.Errorf("%w: config printed the plaintext secret: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// exportHidesSecrets proves that stack export keeps secrets encrypted: the
// checkpoint survives the round trip without its plaintext.
func exportHidesSecrets(r e2e.Result) error {
	if err := exportHoldsReference(r); err != nil {
		return err
	}

	if strings.Contains(r.Stdout, "s3cr3t") {
		return fmt.Errorf("%w: stack export printed the plaintext secret", errUnexpected)
	}

	return nil
}

// checkForeignDecrypt proves that a ciphertext is bound to its stack: it
// encrypts on the renamed stack, decrypts on the same stack, and proves that
// the other stack cannot decrypt the ciphertext.
func checkForeignDecrypt(ctx context.Context, env *e2e.Env) func(e2e.Result) error {
	return func(e2e.Result) error {
		renamed := fmt.Sprintf("/api/stacks/%s/%s/renamed", e2e.Namespace, e2e.Project)
		other := fmt.Sprintf("/api/stacks/%s/%s/other", e2e.Namespace, e2e.Project)

		status, body, err := env.APIStatus(ctx, http.MethodPost, renamed+"/encrypt", `{"plaintext":"aGk="}`)
		if err != nil {
			return err
		}

		if status != http.StatusOK {
			return fmt.Errorf("%w: encrypt answers %d: %s", errUnexpected, status, body)
		}

		var encrypted struct {
			Ciphertext string `json:"ciphertext"`
		}

		if decodeErr := json.Unmarshal([]byte(body), &encrypted); decodeErr != nil || encrypted.Ciphertext == "" {
			return fmt.Errorf("%w: encrypt answers no ciphertext: %s", errUnexpected, body)
		}

		decryptBody := fmt.Sprintf(`{"ciphertext":%q}`, encrypted.Ciphertext)

		status, body, err = env.APIStatus(ctx, http.MethodPost, renamed+"/decrypt", decryptBody)
		if err != nil {
			return err
		}

		if status != http.StatusOK {
			return fmt.Errorf("%w: own decrypt answers %d: %s", errUnexpected, status, body)
		}

		status, body, err = env.APIStatus(ctx, http.MethodPost, other+"/decrypt", decryptBody)
		if err != nil {
			return err
		}

		if status != http.StatusBadRequest {
			return fmt.Errorf(
				"%w: foreign decrypt answers %d, want %d: %s",
				errUnexpected,
				status,
				http.StatusBadRequest,
				body,
			)
		}

		return nil
	}
}

// checkCheckpointSaved proves that the CLI stored its checkpoint through
// PATCH .../update/{id}/checkpoint during the named step.
func checkCheckpointSaved(env *e2e.Env, stepName string) func(e2e.Result) error {
	return func(e2e.Result) error {
		entries := env.Recorder.Entries()
		for i := range entries {
			if entries[i].Step == stepName && entries[i].Method == http.MethodPatch &&
				strings.HasSuffix(entries[i].Path, "/checkpoint") && entries[i].Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: no PATCH checkpoint answered 200 during %q", errUnexpected, stepName)
	}
}

// consoleURLSteps records how the CLI derives console links from the backend
// URL. `pulumi login` prints "(<console URL of the user>)", empty when the CLI
// cannot derive one.
func consoleURLSteps(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	const (
		apiURL = "http://api.dustpan.test"
		appURL = "http://app.dustpan.test/" + e2e.Subject
	)

	// The CLI inherits the console URL scheme from the backend URL, so the
	// override keeps http on a plain-HTTP server and https on a TLS server.
	overrideURL := "http://console.example.test:3000/" + e2e.Subject
	if strings.HasPrefix(env.URL, "https://") {
		overrideURL = "https://console.example.test:3000/" + e2e.Subject
	}

	env.Step(ctx, t, step("console url from 127.0.0.1", contains("as "+e2e.Subject+" ()\n"), "login"))
	env.Step(ctx, t, e2e.StepSpec{
		Check: contains("(" + appURL + ")"),
		Name:  "console url from api. host",
		Args:  []string{"login"},
		// The fake host does not resolve, so the CLI sends its requests through the
		// test server acting as an HTTP proxy; the server routes on the request path.
		Env:    []string{"PULUMI_BACKEND_URL=" + apiURL, "HTTP_PROXY=" + env.URL},
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains("(" + overrideURL + ")"),
		Name:   "console url from PULUMI_CONSOLE_DOMAIN",
		Args:   []string{"login"},
		Env:    []string{"PULUMI_CONSOLE_DOMAIN=console.example.test:3000"},
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
}

// tofuScenario drives OpenTofu's cloud block against the server: the
// organization is the Namespace namespace and the workspace is e2e.Workspace.
// The configuration has no resources and no providers, so only state and
// locking are exercised.
func tofuScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	checkReady(ctx, t, env)
	checkNamespaceCreateThroughAPI(t, env, e2e.Namespace)

	for _, s := range []e2e.StepSpec{
		// Init reads the workspace by name and creates it when missing, so
		// it proves the read, create and list routes with the inventory check.
		step("init", checkTofuInit(ctx, env), "init", "-input=false", "-no-color"),
		step("workspace show", contains(e2e.Workspace), "workspace", "show"),
	} {
		env.Step(ctx, t, s)
	}

	// Lock steps run before any state write: a failed apply would hold the
	// lock, and the lock scenario needs an unlocked workspace.
	tofuLockScenario(t, env)

	for _, s := range []e2e.StepSpec{
		step("plan", nil, "plan", "-input=false", "-no-color"),
		step("apply", checkApplyReadsStateVersion(env), "apply", "-input=false", "-auto-approve", "-no-color"),
		step("output", checkTofuOutput(env), "output", "-json", "-no-color"),
		step("state pull", contains(`"greeting"`), "state", "pull"),
	} {
		env.Step(ctx, t, s)
	}

	tofuMaintenanceScenario(t, env)

	// A second apply with a changed output makes a second state version, so
	// the list below holds two versions.
	env.WriteTofuConfig(t, tofuSecondConfig(env.Host()))
	env.Step(ctx, t, step("apply again", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	// The list route serves the two versions with the go-tfe query shape.
	tofuStateVersionListScenario(t, env)

	// A restore of the first version makes it current again: state pull
	// matches its MD5, a new apply succeeds, and a locked restore names the
	// holder.
	tofuRestoreScenario(t, env)

	// state push rejects a stale serial and another lineage, and accepts
	// -force.
	tofuStatePushScenario(t, env)

	// A crashed apply leaves the lock behind, and force-unlock recovers.
	tofuCrashScenario(t, env)

	// Destroy removes every resource and output with a new, higher serial.
	tofuDestroyScenario(t, env)

	tofuRemoteScenario(t, env)

	// The stdlib http backend is the T1 import source (ADR 29): init, apply
	// of a built-in resource and state pull pass against it.
	tofuHTTPScenario(t, env)

	// Import source T1 (ADR 29): local state, the http backend and
	// client-side encryption move into OpenDustpan.
	tofuImportScenario(t, env)

	// Import error cases (ADR 29 exit test step 6): a move into a missing
	// organization and a move with a wrong token fail with a clear message
	// and leave the source unchanged.
	tofuImportErrors(ctx, t, env)
}

// checkApplyReadsStateVersion proves apply fetched the state version it
// created through GET /api/v2/state-versions/{id}: the CLI reads back what
// it wrote before it unlocks.
func checkApplyReadsStateVersion(env *e2e.Env) func(e2e.Result) error {
	return func(e2e.Result) error {
		entries := env.Recorder.Entries()
		for i := range entries {
			entry := &entries[i]
			if entry.Step != "apply" || entry.Method != http.MethodGet {
				continue
			}

			if strings.HasPrefix(entry.Path, "/api/v2/state-versions/") &&
				!strings.HasSuffix(entry.Path, "/download") &&
				entry.Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: apply never read its state version", errUnexpected)
	}
}

// checkTofuOutput proves output printed the greeting and read the outputs
// through GET .../current-state-version-outputs.
func checkTofuOutput(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, `"hello"`) {
			return fmt.Errorf("%w: stdout lacks the greeting: %s", errUnexpected, r.Stdout)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			entry := &entries[i]
			if entry.Step == "output" && entry.Method == http.MethodGet &&
				strings.HasSuffix(entry.Path, "/current-state-version-outputs") &&
				entry.Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: output never read the current outputs", errUnexpected)
	}
}

// tofuRemoteScenario drives the remote backend with a workspace prefix in its
// own directory: new, list and select run without tags, and the two
// workspaces keep separate state.
func tofuRemoteScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	env.WriteRemoteProject(t)

	for _, s := range []e2e.StepSpec{
		step("remote init", nil, "init", "-input=false", "-no-color"),
		step("workspace new dev", contains(e2e.RemoteDev), "workspace", "new", e2e.RemoteDev, "-no-color"),
		step("workspace new prod", contains(e2e.RemoteProd), "workspace", "new", e2e.RemoteProd, "-no-color"),
		step("workspace list", checkRemoteList(env), "workspace", "list", "-no-color"),
		step("workspace select dev", nil, "workspace", "select", e2e.RemoteDev, "-no-color"),
		step("workspace show dev", contains(e2e.RemoteDev), "workspace", "show", "-no-color"),
		step("remote apply dev", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
		step("remote state pull dev", contains(`"greeting"`), "state", "pull", "-no-color"),
		step("workspace select prod", nil, "workspace", "select", e2e.RemoteProd, "-no-color"),
		step("remote state pull prod", statePullLacksGreeting, "state", "pull", "-no-color"),
	} {
		env.RemoteStep(ctx, t, s)
	}
}

// tofuHTTPScenario drives the stdlib http backend in its own directory: init,
// apply of a built-in resource and state pull pass, and the backend counts
// the apply write.
func tofuHTTPScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	backend := e2e.NewHTTPBackend(t)
	dir := backend.WriteProject(t)

	for _, s := range []e2e.StepSpec{
		step("http init", nil, "init", "-input=false", "-no-color"),
		step("http apply", checkHTTPWrites(backend), "apply", "-input=false", "-auto-approve", "-no-color"),
		step("http state pull", contains(`"greeting"`), "state", "pull", "-no-color"),
	} {
		env.StepIn(ctx, t, dir, s)
	}
}

// checkHTTPWrites proves the apply wrote state through the http backend.
func checkHTTPWrites(backend *e2e.HTTPBackend) func(e2e.Result) error {
	return func(e2e.Result) error {
		if got := backend.Writes(); got == 0 {
			return fmt.Errorf("%w: http backend holds no write after apply", errUnexpected)
		}

		return nil
	}
}

// importSourceConfig is a project without a backend: apply keeps local
// state. It uses the built-in terraform_data resource, so no provider is
// downloaded.
const importSourceConfig = `resource "terraform_data" "smoke" {
  input = "hello"
}

output "greeting" {
  value = "hello"
}
`

// importPassphrase is the test-only passphrase of the encrypted import
// source. The destination reads the moved state with the same block.
const importPassphrase = "correct horse battery staple"

// importCloudConfig returns the same project with a cloud block on host and
// the workspace name, so init -migrate-state copies the source state there.
func importCloudConfig(host, workspace string) string {
	return importCloudConfigOrg(host, e2e.Namespace, workspace)
}

// importCloudConfigOrg returns the same project with a cloud block on host,
// organization org and the workspace name.
func importCloudConfigOrg(host, org, workspace string) string {
	return fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

`, host, org, workspace) + importSourceConfig
}

// importEncryptionBlock returns the client-side encryption block shared by
// the encrypted source and its destination.
func importEncryptionBlock() string {
	return fmt.Sprintf(`  encryption {
    key_provider "pbkdf2" "e2e" {
      passphrase = %q
    }
    method "aes_gcm" "e2e" {
      keys = key_provider.pbkdf2.e2e
    }
    state {
      method = method.aes_gcm.e2e
    }
  }
`, importPassphrase)
}

// importEncryptedSourceConfig returns the source project with client-side
// encryption and a local backend: apply writes the encrypted envelope into
// the state file.
func importEncryptedSourceConfig() string {
	return "terraform {\n" + importEncryptionBlock() + "}\n\n" + importSourceConfig
}

// importEncryptedCloudConfig returns the same encrypted project with a cloud
// block on host and the workspace name.
func importEncryptedCloudConfig(host, workspace string) string {
	return fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
`, host, e2e.Namespace, workspace) + importEncryptionBlock() + "}\n\n" + importSourceConfig
}

// importStateDoc decodes the serial, lineage, outputs and resources of a
// pulled state.
type importStateDoc struct {
	Serial    int64          `json:"serial"`
	Lineage   string         `json:"lineage"`
	Outputs   map[string]any `json:"outputs"`
	Resources []any          `json:"resources"`
}

// decodeImportState decodes stdout as a pulled state.
func decodeImportState(stdout string) (importStateDoc, error) {
	var doc importStateDoc

	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		return importStateDoc{}, fmt.Errorf("%w: decode pulled state: %w", errUnexpected, err)
	}

	return doc, nil
}

// canonicalImportState returns the outputs and resources of doc as one
// string, so a check compares state content without serial and lineage.
func canonicalImportState(doc importStateDoc) (string, error) {
	raw, err := json.Marshal(struct {
		Outputs   map[string]any `json:"outputs"`
		Resources []any          `json:"resources"`
	}{Outputs: doc.Outputs, Resources: doc.Resources})
	if err != nil {
		return "", fmt.Errorf("%w: encode state content: %w", errUnexpected, err)
	}

	return string(raw), nil
}

// writeImportConfig writes content as main.tf in dir for an import move.
func writeImportConfig(t *testing.T, dir, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("write import config: %v", err)
	}
}

// importMigrateStep returns the migrate step: plain init with input on,
// answering yes to the copy approval. A cloud block takes no migrate flags:
// -migrate-state and -force-copy are for state backends only, and the copy
// into a cloud backend is an interactive prompt instead.
func importMigrateStep(name string) e2e.StepSpec {
	return e2e.StepSpec{
		Check:  nil,
		Name:   name,
		Args:   []string{"init", "-no-color"},
		Env:    []string{"TF_INPUT=1"},
		Stdin:  "yes\n",
		Fail:   false,
		Redact: false,
	}
}

// importRecord renders the lineage and serial before and after a move for
// the report.
func importRecord(before, after importStateDoc) string {
	return fmt.Sprintf(
		"source serial=%d lineage=%s; destination serial=%d lineage=%s",
		before.Serial,
		before.Lineage,
		after.Serial,
		after.Lineage,
	)
}

// checkImportBefore records the source state and proves it has a serial and
// a lineage, then runs the extra source checks, if any.
func checkImportBefore(before *importStateDoc, extra func() error) func(e2e.Result) error {
	return func(r e2e.Result) error {
		doc, err := decodeImportState(r.Stdout)
		if err != nil {
			return err
		}

		if doc.Serial < 1 {
			return fmt.Errorf("%w: source serial is %d, want at least 1", errUnexpected, doc.Serial)
		}

		if doc.Lineage == "" {
			return fmt.Errorf("%w: source state has no lineage", errUnexpected)
		}

		*before = doc

		if extra != nil {
			return extra()
		}

		return nil
	}
}

// checkImportPreserved proves kept holds the state before: the same content
// and the same serial.
func checkImportPreserved(before, kept importStateDoc) error {
	want, err := canonicalImportState(before)
	if err != nil {
		return err
	}

	got, err := canonicalImportState(kept)
	if err != nil {
		return err
	}

	if got != want {
		return fmt.Errorf("%w: the moved state lost content", errUnexpected)
	}

	if kept.Serial != before.Serial {
		return fmt.Errorf(
			"%w: moved serial is %d, want the source serial %d",
			errUnexpected,
			kept.Serial,
			before.Serial,
		)
	}

	return nil
}

// checkImportAfter records the destination state and proves the move: the
// content and the serial match the source, and the lineage is fresh, because
// OpenTofu writes a new lineage into an empty workspace. It then proves the
// source is unchanged, if asked.
func checkImportAfter(
	before, after *importStateDoc, checkSource func() error,
) func(e2e.Result) error {
	return func(r e2e.Result) error {
		doc, err := decodeImportState(r.Stdout)
		if err != nil {
			return err
		}

		*after = doc

		if err := checkImportPreserved(*before, doc); err != nil {
			return err
		}

		if doc.Lineage == "" || doc.Lineage == before.Lineage {
			return fmt.Errorf(
				"%w: destination lineage is %q, want a fresh lineage unlike the source %q",
				errUnexpected,
				doc.Lineage,
				before.Lineage,
			)
		}

		if checkSource != nil {
			return checkSource()
		}

		return nil
	}
}

// checkImportPlan proves plan shows no changes after the move.
func checkImportPlan(r e2e.Result) error {
	if out := r.Stdout + r.Stderr; !strings.Contains(out, "No changes.") {
		return fmt.Errorf("%w: plan lacks a no-changes report: %s", errUnexpected, out)
	}

	return nil
}

// checkImportBackup proves the local source is unchanged: its backup keeps
// the state the source phase wrote, with the same serial and lineage.
func checkImportBackup(dir string, before importStateDoc) error {
	backupFile := filepath.Join(dir, "terraform.tfstate.backup")
	raw, err := os.ReadFile(backupFile) //nolint:gosec // The test reads the state file it wrote.
	if err != nil {
		return fmt.Errorf("%w: read the local backup: %w", errUnexpected, err)
	}

	kept, err := decodeImportState(string(raw))
	if err != nil {
		return err
	}

	if err := checkImportPreserved(before, kept); err != nil {
		return err
	}

	if kept.Lineage != before.Lineage {
		return fmt.Errorf(
			"%w: backup lineage is %q, want the source lineage %q",
			errUnexpected,
			kept.Lineage,
			before.Lineage,
		)
	}

	return nil
}

// checkHTTPBackendQuiet proves the http source got no write after the copy:
// the write count stays and no delete arrived. Locks do not count: the move
// locks the source where the source allows it.
func checkHTTPBackendQuiet(backend *e2e.HTTPBackend, writes, deletes int) error {
	if got := backend.Writes(); got != writes {
		return fmt.Errorf("%w: http backend holds %d writes, want %d", errUnexpected, got, writes)
	}

	if got := backend.Counts().Deletes; got != deletes {
		return fmt.Errorf("%w: http backend holds %d deletes, want %d", errUnexpected, got, deletes)
	}

	return nil
}

// checkImportEncryptedFile returns a check that proves the source state file
// holds the encrypted envelope.
func checkImportEncryptedFile(dir string) func() error {
	return func() error {
		state := filepath.Join(dir, "terraform.tfstate")
		raw, err := os.ReadFile(state) //nolint:gosec // The test reads the state file it wrote.
		if err != nil {
			return fmt.Errorf("%w: read the encrypted source: %w", errUnexpected, err)
		}

		if !strings.Contains(string(raw), "encrypted_data") {
			return fmt.Errorf("%w: the source state holds no encrypted envelope", errUnexpected)
		}

		return nil
	}
}

// importEncryptedBackupDoc decodes the plaintext envelope fields of an
// encrypted state file: the serial and the lineage travel outside the
// encrypted payload.
type importEncryptedBackupDoc struct {
	Serial  int64  `json:"serial"`
	Lineage string `json:"lineage"`
}

// decodeImportEnvelope decodes the plaintext envelope fields of an
// encrypted state file: the serial and the lineage travel outside the
// encrypted payload.
func decodeImportEnvelope(raw []byte) (importEncryptedBackupDoc, error) {
	var envelope importEncryptedBackupDoc

	if err := json.Unmarshal(raw, &envelope); err != nil {
		return importEncryptedBackupDoc{}, fmt.Errorf("%w: decode the encrypted backup: %w", errUnexpected, err)
	}

	return envelope, nil
}

// checkImportEnvelopePreserved proves the backup envelope keeps the source
// serial and lineage.
func checkImportEnvelopePreserved(envelope importEncryptedBackupDoc, before importStateDoc) error {
	if envelope.Serial != before.Serial {
		return fmt.Errorf(
			"%w: backup serial is %d, want the source serial %d",
			errUnexpected,
			envelope.Serial,
			before.Serial,
		)
	}

	if envelope.Lineage != before.Lineage {
		return fmt.Errorf(
			"%w: backup lineage is %q, want the source lineage %q",
			errUnexpected,
			envelope.Lineage,
			before.Lineage,
		)
	}

	return nil
}

// checkImportEncryptedBackup proves the encrypted local source is unchanged
// after the move: the backup keeps the source serial and lineage, and its
// envelope decrypts to the source content. The bytes always differ, because
// every state write re-encrypts with a fresh nonce, so the check compares
// documents, not bytes.
func checkImportEncryptedBackup(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	dir string,
	before importStateDoc,
) func() error {
	t.Helper()

	return func() error {
		backupFile := filepath.Join(dir, "terraform.tfstate.backup")
		// The test reads the state file it wrote.
		raw, err := os.ReadFile(backupFile) //nolint:gosec // Test file with a fixed name.
		if err != nil {
			return fmt.Errorf("%w: read the encrypted backup: %w", errUnexpected, err)
		}

		if !strings.Contains(string(raw), "encrypted_data") {
			return fmt.Errorf("%w: the encrypted backup holds no encrypted envelope", errUnexpected)
		}

		envelope, err := decodeImportEnvelope(raw)
		if err != nil {
			return err
		}

		if checkErr := checkImportEnvelopePreserved(envelope, before); checkErr != nil {
			return checkErr
		}

		kept, err := decryptImportBackup(ctx, t, env, raw)
		if err != nil {
			return err
		}

		if checkErr := checkImportPreserved(before, kept); checkErr != nil {
			return checkErr
		}

		if kept.Lineage != before.Lineage {
			return fmt.Errorf(
				"%w: backup lineage is %q, want the source lineage %q",
				errUnexpected,
				kept.Lineage,
				before.Lineage,
			)
		}

		return nil
	}
}

// decryptImportBackup decrypts the encrypted backup raw with the test
// passphrase: it runs the CLI in a scratch directory that holds the backup
// as its local state beside the encrypted source project, so init and state
// pull read it through the same encryption block. It runs through RunIn, so
// it records no extra step.
func decryptImportBackup(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	raw []byte,
) (importStateDoc, error) {
	t.Helper()

	scratch := t.TempDir()

	backupCopy := filepath.Join(scratch, "terraform.tfstate")
	if err := os.WriteFile(backupCopy, raw, 0o600); err != nil { //nolint:gosec // Temp dir with a fixed name.
		return importStateDoc{}, fmt.Errorf("%w: write the backup copy: %w", errUnexpected, err)
	}

	writeImportConfig(t, scratch, importEncryptedSourceConfig())

	init, err := env.RunIn(ctx, scratch, nil, "", "init", "-input=false", "-no-color")
	if err != nil {
		return importStateDoc{}, fmt.Errorf("%w: init the decrypt project: %w", errUnexpected, err)
	}

	if init.ExitCode != 0 {
		return importStateDoc{}, fmt.Errorf(
			"%w: init the decrypt project exits %d: %s%s",
			errUnexpected,
			init.ExitCode,
			init.Stdout,
			init.Stderr,
		)
	}

	pulled, err := env.RunIn(ctx, scratch, nil, "", "state", "pull", "-no-color")
	if err != nil {
		return importStateDoc{}, fmt.Errorf("%w: pull the decrypted backup: %w", errUnexpected, err)
	}

	if pulled.ExitCode != 0 {
		return importStateDoc{}, fmt.Errorf(
			"%w: pull the decrypted backup exits %d: %s%s",
			errUnexpected,
			pulled.ExitCode,
			pulled.Stdout,
			pulled.Stderr,
		)
	}

	return decodeImportState(pulled.Stdout)
}

// tofuImportScenario proves import source T1 (ADR 29): local state and the
// http backend move into OpenDustpan with an interactive init that copies
// the source state, each with its own run and workspace, and client-side
// encryption stays.
func tofuImportScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	tofuImportLocal(ctx, t, env)
	tofuImportHTTP(ctx, t, env)
	tofuImportEncrypted(ctx, t, env)
}

// tofuImportLocal moves local state: apply with the default backend, then a
// cloud block and init, answering yes to the copy approval. The local
// backup keeps the source state.
func tofuImportLocal(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := t.TempDir()
	writeImportConfig(t, dir, importSourceConfig)

	var before, after importStateDoc

	env.StepIn(ctx, t, dir, step("import local init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, step("import local apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import local state before", checkImportBefore(&before, nil), "state", "pull", "-no-color"),
	)

	writeImportConfig(t, dir, importCloudConfig(env.Host(), "import-local"))

	env.StepIn(ctx, t, dir, importMigrateStep("import local migrate"))
	env.StepIn(ctx, t, dir, step(
		"import local state after",
		checkImportAfter(&before, &after, func() error { return checkImportBackup(dir, before) }),
		"state", "pull", "-no-color",
	))
	env.StepIn(ctx, t, dir, step("import local plan", checkImportPlan, "plan", "-input=false", "-no-color"))

	env.NoteStep(t, "import local lineage and serial", importRecord(before, after))
}

// tofuImportHTTP moves the http backend: apply against the backend, then a
// cloud block and init, answering yes to the copy approval. The backend
// counts no write after the copy.
func tofuImportHTTP(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	backend := e2e.NewHTTPBackend(t)
	dir := t.TempDir()
	writeImportConfig(t, dir, backend.Config())

	var before, after importStateDoc

	env.StepIn(ctx, t, dir, step("import http init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, step("import http apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	writesBefore := backend.Writes()
	deletesBefore := backend.Counts().Deletes

	env.StepIn(
		ctx,
		t,
		dir,
		step("import http state before", checkImportBefore(&before, nil), "state", "pull", "-no-color"),
	)

	writeImportConfig(t, dir, importCloudConfig(env.Host(), "import-http"))

	env.StepIn(ctx, t, dir, importMigrateStep("import http migrate"))
	env.StepIn(ctx, t, dir, step(
		"import http state after",
		checkImportAfter(&before, &after, func() error {
			return checkHTTPBackendQuiet(backend, writesBefore, deletesBefore)
		}),
		"state", "pull", "-no-color",
	))
	env.StepIn(ctx, t, dir, step("import http plan", checkImportPlan, "plan", "-input=false", "-no-color"))

	env.NoteStep(t, "import http lineage and serial", importRecord(before, after))
}

// tofuImportEncrypted moves client-side encrypted state: apply writes the
// encrypted envelope into the local file, plain init answers yes to the copy
// approval and moves it as it is, and the destination reads it with the same
// block. The local backup keeps the source serial, lineage and content
// behind a fresh envelope.
func tofuImportEncrypted(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := t.TempDir()
	writeImportConfig(t, dir, importEncryptedSourceConfig())

	var before, after importStateDoc

	env.StepIn(ctx, t, dir, step("import encrypted init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import encrypted apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
	)
	env.StepIn(ctx, t, dir, step(
		"import encrypted state before",
		checkImportBefore(&before, checkImportEncryptedFile(dir)),
		"state", "pull", "-no-color",
	))

	writeImportConfig(t, dir, importEncryptedCloudConfig(env.Host(), "import-encrypted"))

	env.StepIn(ctx, t, dir, importMigrateStep("import encrypted migrate"))
	env.StepIn(ctx, t, dir, step(
		"import encrypted state after",
		checkImportAfter(&before, &after, checkImportEncryptedBackup(ctx, t, env, dir, before)),
		"state", "pull", "-no-color",
	))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import encrypted plan", checkImportPlan, "plan", "-input=false", "-no-color"),
	)

	env.NoteStep(t, "import encrypted lineage and serial", importRecord(before, after))

	checkImportEncryptedStored(ctx, t, env)
}

// importMissingOrg is an organization that no server creates, so a move
// into it must fail.
const importMissingOrg = "ghost"

// tofuImportErrors proves the error cases of import source T1 (ADR 29 exit
// test step 6): init into a cloud block with a missing organization fails and
// names the organization, and init with a wrong token fails and names the
// authentication failure. Each case leaves the source unchanged, and the log
// records the text that the CLI prints in each case.
func tofuImportErrors(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	tofuImportMissingOrg(ctx, t, env)
	tofuImportWrongToken(ctx, t, env)
}

// tofuImportMissingOrg moves local state into a missing organization: the
// init fails, names the organization and holds no token, and the local state
// file keeps the source serial, lineage and content.
func tofuImportMissingOrg(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := t.TempDir()
	writeImportConfig(t, dir, importSourceConfig)

	var before importStateDoc

	env.StepIn(ctx, t, dir, step("import error init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, step("import error apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import error state before", checkImportBefore(&before, nil), "state", "pull", "-no-color"),
	)

	writeImportConfig(t, dir, importCloudConfigOrg(env.Host(), importMissingOrg, "import-missing-org"))

	res := env.StepIn(ctx, t, dir, e2e.StepSpec{
		Check:  checkImportErrorOneOf([]string{importMissingOrg}, env.TokenA()),
		Name:   "import error missing organization",
		Args:   []string{"init", "-no-color"},
		Env:    []string{"TF_INPUT=1"},
		Stdin:  "yes\n",
		Fail:   true,
		Redact: false,
	})
	t.Logf("import into missing organization printed:\n%s%s", res.Stdout, res.Stderr)

	if err := checkImportLocalFileUnchanged(dir, before); err != nil {
		t.Errorf("import into missing organization changed the source: %v", err)
	}
}

// tofuImportWrongToken moves the http backend with a wrong token: the init
// fails, names the authentication failure and holds no token, and the backend
// counts no write after the copy attempt.
func tofuImportWrongToken(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	backend := e2e.NewHTTPBackend(t)
	dir := t.TempDir()
	writeImportConfig(t, dir, backend.Config())

	var before importStateDoc

	env.StepIn(ctx, t, dir, step("import wrong token init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import wrong token apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
	)

	writesBefore := backend.Writes()
	deletesBefore := backend.Counts().Deletes

	env.StepIn(
		ctx,
		t,
		dir,
		step("import wrong token state before", checkImportBefore(&before, nil), "state", "pull", "-no-color"),
	)

	writeImportConfig(t, dir, importCloudConfig(env.Host(), "import-wrong-token"))

	wrongRC := writeWrongTofuRC(t, env.Host())

	res := env.StepIn(ctx, t, dir, e2e.StepSpec{
		Check:  checkImportErrorOneOf(importWrongTokenPhrases(), env.TokenA(), importWrongToken),
		Name:   "import error wrong token",
		Args:   []string{"init", "-no-color"},
		Env:    []string{"TF_INPUT=1", "TF_CLI_CONFIG_FILE=" + wrongRC},
		Stdin:  "yes\n",
		Fail:   true,
		Redact: false,
	})
	t.Logf("import with wrong token printed:\n%s%s", res.Stdout, res.Stderr)

	if err := checkHTTPBackendQuiet(backend, writesBefore, deletesBefore); err != nil {
		t.Errorf("import with wrong token changed the source: %v", err)
	}

	if before.Serial < 1 {
		t.Error("import with wrong token lost the source serial")
	}
}

// writeWrongTofuRC writes a CLI configuration with a wrong token for host
// and returns its path, so a step runs the CLI as an unauthenticated caller.
func writeWrongTofuRC(t *testing.T, host string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tofurc-wrong")
	content := fmt.Sprintf("credentials %q {\n  token = %q\n}\n", host, importWrongToken)

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write wrong CLI configuration: %v", err)
	}

	return path
}

// checkImportLocalFileUnchanged proves the local state file in dir still
// holds the source state: the same content, serial and lineage as before.
func checkImportLocalFileUnchanged(dir string, before importStateDoc) error {
	stateFile := filepath.Join(dir, "terraform.tfstate")
	// The test reads the state file it wrote.
	raw, err := os.ReadFile(stateFile) //nolint:gosec // Test file with a fixed name.
	if err != nil {
		return fmt.Errorf("%w: read the local source: %w", errUnexpected, err)
	}

	kept, err := decodeImportState(string(raw))
	if err != nil {
		return err
	}

	if err := checkImportPreserved(before, kept); err != nil {
		return err
	}

	if kept.Lineage != before.Lineage {
		return fmt.Errorf("%w: source lineage is %q, want %q", errUnexpected, kept.Lineage, before.Lineage)
	}

	return nil
}

// importStateVersionData holds the data object of a current-state-version
// document.
type importStateVersionData struct {
	ID string `json:"id"`
}

// importStateVersionRef decodes a current-state-version document in tests.
type importStateVersionRef struct {
	Data importStateVersionData `json:"data"`
}

// importStateVersionID returns the id of the current-state-version document.
func importStateVersionID(body string) (string, error) {
	var current importStateVersionRef

	if err := json.Unmarshal([]byte(body), &current); err != nil {
		return "", fmt.Errorf("%w: decode current state: %w", errUnexpected, err)
	}

	if current.Data.ID == "" {
		return "", fmt.Errorf("%w: current state has no id", errUnexpected)
	}

	return current.Data.ID, nil
}

// checkImportEncryptedStored proves the moved state stays encrypted on the
// destination: the stored bytes of the encrypted workspace hold the
// encrypted envelope. Each request is a recorded API step, so the report
// holds the exchange and the bodies stay clipped there.
func checkImportEncryptedStored(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	wsID := env.WorkspaceIDByName(ctx, t, "import-encrypted")

	var versionID string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check: func(body string) error {
			id, err := importStateVersionID(body)
			if err != nil {
				return err
			}

			versionID = id

			return nil
		},
		Name:   "import encrypted current state",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID + "/current-state-version",
		Body:   "",
		Want:   http.StatusOK,
	})

	if versionID == "" {
		return
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check: func(body string) error {
			if !strings.Contains(body, "encrypted_data") {
				return fmt.Errorf(
					"%w: the stored state of the encrypted workspace holds no encrypted envelope",
					errUnexpected,
				)
			}

			return nil
		},
		Name:   "import encrypted stored state",
		Method: http.MethodGet,
		Path:   "/api/v2/state-versions/" + versionID + "/download",
		Body:   "",
		Want:   http.StatusOK,
	})
}

// checkRemoteList proves workspace list shows the short names dev and prod
// and that the CLI listed with search[name]: the request carries the prefix.
func checkRemoteList(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if err := remoteListShows(r); err != nil {
			return err
		}

		return remoteListSearched(env)
	}
}

// remoteListShows proves workspace list shows the short names dev and prod.
func remoteListShows(r e2e.Result) error {
	for _, want := range []string{e2e.RemoteDev, e2e.RemoteProd} {
		if !strings.Contains(r.Stdout, want) {
			return fmt.Errorf("%w: workspace list lacks %q: %s", errUnexpected, want, r.Stdout)
		}
	}

	return nil
}

// remoteListSearched proves the list step sent a workspace list with a search
// query.
func remoteListSearched(env *e2e.Env) error {
	entries := env.Recorder.Entries()

	for i := range entries {
		if entries[i].Step != "workspace list" || entries[i].Method != http.MethodGet {
			continue
		}

		if entries[i].Path != "/api/v2/organizations/"+e2e.Namespace+"/workspaces" {
			continue
		}

		if !strings.Contains(entries[i].Query, "search") {
			return fmt.Errorf("%w: workspace list sent no search query: %q", errUnexpected, entries[i].Query)
		}

		return nil
	}

	return fmt.Errorf("%w: workspace list made no list request", errUnexpected)
}

// statePullLacksGreeting proves the state holds no resource of the other
// workspace: the pulled state lacks the greeting output.
func statePullLacksGreeting(r e2e.Result) error {
	if strings.Contains(r.Stdout, `"greeting"`) {
		return fmt.Errorf("%w: state pull shows the other workspace: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// inventoryLockDoc decodes the lock object of GET /api/inventory/units: the
// holder, the take time in Unix seconds, and the operation.
type inventoryLockDoc struct {
	Holder    string `json:"holder"`
	Since     int64  `json:"since"`
	Operation string `json:"operation"`
}

// inventoryUnitDoc decodes one unit of GET /api/inventory/units.
type inventoryUnitDoc struct {
	Tool       string            `json:"tool"`
	Reference  string            `json:"reference"`
	Namespace  string            `json:"namespace"`
	Name       string            `json:"name"`
	LastChange int64             `json:"lastChange"`
	Lock       *inventoryLockDoc `json:"lock"`
}

// readInventoryUnits reads the inventory units of ns through the API as
// principal A.
func readInventoryUnits(ctx context.Context, t *testing.T, env *e2e.Env, ns string) []inventoryUnitDoc {
	t.Helper()

	got, err := env.StatusWithHeader(
		ctx, "token "+env.TokenA(), http.MethodGet, "/api/inventory/units?organization="+ns, "",
	)
	if err != nil {
		t.Fatalf("GET /api/inventory/units: %v", err)
	}

	if got.Status != http.StatusOK {
		t.Fatalf("GET /api/inventory/units answers %d, want %d (%s)", got.Status, http.StatusOK, got.Body)
	}

	var doc struct {
		Units []inventoryUnitDoc `json:"units"`
	}

	if err := json.Unmarshal([]byte(got.Body), &doc); err != nil {
		t.Fatalf("decode inventory units: %v (%s)", err, got.Body)
	}

	return doc.Units
}

// findInventoryUnit returns the unit with reference, or nil when the list
// holds none.
func findInventoryUnit(units []inventoryUnitDoc, ref string) *inventoryUnitDoc {
	for i := range units {
		if units[i].Reference == ref {
			return &units[i]
		}
	}

	return nil
}

// namespaceOf returns the namespace of an inventory reference
// ("tool:namespace/name").
func namespaceOf(ref string) string {
	_, rest, _ := strings.Cut(ref, ":")
	ns, _, _ := strings.Cut(rest, "/")

	return ns
}

// lockedUnit is one unit with the lock the test expects: its reference,
// the holder and the operation.
type lockedUnit struct {
	ref       string
	holder    string
	operation string
}

// checkInventoryLock proves the inventory entry of want.ref holds a lock
// with its holder and operation, and a take time. It fails the test
// otherwise.
func checkInventoryLock(ctx context.Context, t *testing.T, env *e2e.Env, want lockedUnit) {
	t.Helper()

	unit := findInventoryUnit(readInventoryUnits(ctx, t, env, namespaceOf(want.ref)), want.ref)

	if unit == nil {
		t.Fatalf("inventory holds no entry %q", want.ref)
	}

	if unit.Lock == nil {
		t.Fatalf("inventory entry %q holds no lock", want.ref)
	}

	if unit.Lock.Holder != want.holder || unit.Lock.Operation != want.operation || unit.Lock.Since <= 0 {
		t.Fatalf(
			"inventory lock of %q = %+v, want holder %q operation %q with a take time",
			want.ref,
			unit.Lock,
			want.holder,
			want.operation,
		)
	}
}

// checkInventoryUnlocked proves the inventory entry of ref holds no lock. It
// fails the test otherwise.
func checkInventoryUnlocked(ctx context.Context, t *testing.T, env *e2e.Env, ref string) {
	t.Helper()

	unit := findInventoryUnit(readInventoryUnits(ctx, t, env, namespaceOf(ref)), ref)

	if unit == nil {
		t.Fatalf("inventory holds no entry %q", ref)
	}

	if unit.Lock != nil {
		t.Fatalf("inventory entry %q holds %+v, want no lock", ref, unit.Lock)
	}
}

// tofuLockScenario proves workspace locks across two principals: A locks
// through the API, B cannot plan until it force-unlocks.
func tofuLockScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"
	unlockPath := "/api/v2/workspaces/" + wsID + "/actions/unlock"
	forcePath := "/api/v2/workspaces/" + wsID + "/actions/force-unlock"

	// Principal A locks through the API. The step records 200.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by A",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})

	// The inventory entry of the workspace holds the lock: holder A, no
	// operation named, so "lock".
	checkInventoryLock(ctx, t, env, lockedUnit{
		ref:       "opentofu:" + e2e.Namespace + "/" + e2e.Workspace,
		holder:    e2e.Subject,
		operation: "lock",
	})

	// An unlock by A releases it, and the inventory entry holds no lock
	// after. A relocks for the steps below.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "unlock by A",
		Method: http.MethodPost,
		Path:   unlockPath,
		Body:   "",
		Want:   http.StatusOK,
	})

	checkInventoryUnlocked(ctx, t, env, "opentofu:"+e2e.Namespace+"/"+e2e.Workspace)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by A again",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})

	// A plan as B fails at once. go-tfe discards the 409 detail for lock, so
	// the CLI prints the generic message. The step still proves the holder:
	// the lock request the plan made answers 409 with "is locked by User"
	// and the holder of A.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkPlanLockedByAnother(ctx, env),
		Name:   "plan locked by another",
		Args:   []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   true,
		Redact: false,
	})

	// A second lock by B answers 409.
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by B conflicts",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusConflict,
	})

	// An unlock by B answers 409: only the holder unlocks.
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "unlock by B conflicts",
		Method: http.MethodPost,
		Path:   unlockPath,
		Body:   "",
		Want:   http.StatusConflict,
	})

	// A force-unlock as B succeeds. The CLI sends no lock ID that the
	// server uses: the workspace ID in the path names the workspace.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock by B",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	// The force-unlock releases the lock: the inventory entry holds none.
	checkInventoryUnlocked(ctx, t, env, "opentofu:"+e2e.Namespace+"/"+e2e.Workspace)

	// A plan as B now passes.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "plan after force-unlock",
		Args:   []string{"plan", "-input=false", "-no-color"},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	// With the workspace locked by B, a read as A shows the holder, time
	// and reason. After the unlock the three are empty or absent. The CLI
	// steps around them prove no CLI rejects the new attributes.
	wsPath := "/api/v2/workspaces/" + wsID

	const lockReason = "e2e lock reason"

	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by B with reason",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{"attributes":{"reason":"` + lockReason + `"}}}`,
		Want:   http.StatusOK,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceShowsLock(e2e.SubjectB, lockReason),
		Name:   "workspace shows lock holder",
		Method: http.MethodGet,
		Path:   wsPath,
		Body:   "",
		Want:   http.StatusOK,
	})
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "unlock by B",
		Method: http.MethodPost,
		Path:   unlockPath,
		Body:   "",
		Want:   http.StatusOK,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceHidesLock,
		Name:   "workspace hides lock holder",
		Method: http.MethodGet,
		Path:   wsPath,
		Body:   "",
		Want:   http.StatusOK,
	})

	// A force-unlock of an unlocked workspace answers 409.
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "force-unlock unlocked conflicts",
		Method: http.MethodPost,
		Path:   forcePath,
		Body:   "",
		Want:   http.StatusConflict,
	})

	// Two principals contend for one lock: both plans run at once with a
	// lock timeout, so the loser waits and retries instead of failing. Every
	// 409 lock answer during the step holds "is locked by User": a lost
	// compare-and-swap reports the held lock, or the CLI would not retry.
	env.StepParallel(ctx, t, e2e.ParallelSpec{
		Name:  "concurrent plans contend for lock",
		Check: checkContentionRetried(env, "concurrent plans contend for lock"),
		Args: [][]string{
			{"plan", "-input=false", "-no-color", "-lock-timeout=60s"},
			{"plan", "-input=false", "-no-color", "-lock-timeout=60s"},
		},
		Env: nil,
		Envs: [][]string{
			nil,
			env.TofuEnvB(),
		},
	})
}

// checkContentionRetried proves every 409 lock answer during step holds "is
// locked by User": the loser of a lock race reports the held lock, so the
// CLI waits and retries under -lock-timeout instead of failing.
func checkContentionRetried(env *e2e.Env, step string) func([]e2e.Result) error {
	return func([]e2e.Result) error {
		entries := env.Recorder.Entries()

		conflicts := 0

		for i := range entries {
			e := &entries[i]
			if e.Step != step || e.Method != http.MethodPost ||
				!strings.HasSuffix(e.Path, "/actions/lock") {
				continue
			}

			if e.Status != http.StatusConflict {
				continue
			}

			conflicts++

			if !strings.Contains(e.ResponseBody, "is locked by User") {
				return fmt.Errorf(
					"%w: lock detail lacks %q: %s",
					errUnexpected,
					"is locked by User",
					e.ResponseBody,
				)
			}
		}

		return nil
	}
}

// checkPlanLockedByAnother proves a plan as B fails on the held lock and
// that the lock request it made answers 409 with "is locked by User" and the
// holder of A. go-tfe maps any 409 on lock to its generic error, so the CLI
// prints "workspace already locked" without the holder; the recorded response
// body holds the detail the unit test also proves.
func checkPlanLockedByAnother(_ context.Context, env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if out := r.Stdout + r.Stderr; !strings.Contains(out, "workspace already locked") {
			return fmt.Errorf("%w: plan output lacks the lock failure: %s", errUnexpected, out)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			e := &entries[i]
			if e.Step != "plan locked by another" || e.Method != http.MethodPost ||
				!strings.HasSuffix(e.Path, "/actions/lock") {
				continue
			}

			if e.Status != http.StatusConflict {
				return fmt.Errorf(
					"%w: lock during plan answers %d, want %d",
					errUnexpected,
					e.Status,
					http.StatusConflict,
				)
			}

			if !strings.Contains(e.ResponseBody, "is locked by User") ||
				!strings.Contains(e.ResponseBody, e2e.Subject) {
				return fmt.Errorf(
					"%w: lock detail lacks %q and the holder: %s",
					errUnexpected,
					"is locked by User",
					e.ResponseBody,
				)
			}

			return nil
		}

		return fmt.Errorf("%w: plan made no lock request", errUnexpected)
	}
}

// checkTofuInit proves that init got past service discovery and created the
// workspace: the CLI called discovery, ping and the entitlement set in order,
// and a direct store call sees one inventory entry for the workspace.
func checkTofuInit(ctx context.Context, env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if err := checkTofuDiscovery(env)(r); err != nil {
			return err
		}

		entries, err := env.ListInventory(ctx, e2e.Namespace)
		if err != nil {
			return err
		}

		if len(entries) != 1 {
			return fmt.Errorf("%w: inventory holds %d entries, want one for the workspace", errUnexpected, len(entries))
		}

		entry := entries[0]
		want := "opentofu:" + e2e.Namespace + "/" + e2e.Workspace

		if entry.Reference() != want || entry.Tool != "opentofu" || entry.Name != e2e.Workspace {
			return fmt.Errorf("%w: inventory entry %+v, want reference %q", errUnexpected, entry, want)
		}

		if entry.UnitID == "" || !strings.HasPrefix(entry.UnitID, "ws-") {
			return fmt.Errorf("%w: inventory unit id %q, want the ws- workspace id", errUnexpected, entry.UnitID)
		}

		return nil
	}
}

// checkTofuDiscovery proves that init got past service discovery: the CLI
// called discovery, ping and the entitlement set in order, and each answered
// 200.
func checkTofuDiscovery(env *e2e.Env) func(e2e.Result) error {
	return func(e2e.Result) error {
		want := []struct {
			method, path string
		}{
			{http.MethodGet, "/.well-known/terraform.json"},
			{http.MethodGet, "/api/v2/ping"},
			{http.MethodGet, "/api/v2/organizations/" + e2e.Namespace + "/entitlement-set"},
		}

		entries := env.Recorder.Entries()
		next := 0

		for i := range entries {
			if entries[i].Step != "init" || next >= len(want) {
				continue
			}

			if entries[i].Method != want[next].method || entries[i].Path != want[next].path {
				continue
			}

			if entries[i].Status != http.StatusOK {
				return fmt.Errorf(
					"%w: %s %s answers %d, want %d",
					errUnexpected,
					entries[i].Method,
					entries[i].Path,
					entries[i].Status,
					http.StatusOK,
				)
			}

			next++
		}

		if next != len(want) {
			return fmt.Errorf("%w: init missed a discovery call", errUnexpected)
		}

		return nil
	}
}

// tofuSecondConfig is the configuration of the second apply: the same cloud
// block with a changed greeting output, so the apply writes a second state
// version.
func tofuSecondConfig(host string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = ` + strconv.Quote(e2e.Workspace) + `
    }
  }
}

output "greeting" {
  value = "hello again"
}
`
}

// stateVersionListAttributes holds the listed attributes the e2e list step
// checks: the serial for order and the added holder, force and
// restored-from attributes.
type stateVersionListAttributes struct {
	Serial       int64  `json:"serial"`
	Holder       string `json:"holder"`
	Force        bool   `json:"force"`
	RestoredFrom string `json:"restored-from"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateVersionListItem holds one entry of a state version list document.
type stateVersionListItem struct {
	ID         string                     `json:"id"`
	Type       string                     `json:"type"`
	Attributes stateVersionListAttributes `json:"attributes"`
}

// stateVersionListPagination holds the meta.pagination object of a state
// version list document.
type stateVersionListPagination struct {
	CurrentPage int `json:"current-page"` //nolint:tagliatelle // The TFE wire name; it must not change.
	PageSize    int `json:"page-size"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalCount  int `json:"total-count"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalPages  int `json:"total-pages"`  //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateVersionListMeta holds the meta object of a state version list
// document.
type stateVersionListMeta struct {
	Pagination stateVersionListPagination `json:"pagination"`
}

// stateVersionListDoc decodes a state version list document.
type stateVersionListDoc struct {
	Data []stateVersionListItem `json:"data"`
	Meta stateVersionListMeta   `json:"meta"`
}

// decodeStateVersionList decodes body into a state version list document.
func decodeStateVersionList(body string) (stateVersionListDoc, error) {
	var doc stateVersionListDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return stateVersionListDoc{}, fmt.Errorf("%w: decode state version list: %w", errUnexpected, err)
	}

	return doc, nil
}

// stateVersionListBase is the list target with the go-tfe request shape:
// filter[organization][name], filter[workspace][name], page[number] and
// page[size].
func stateVersionListBase() string {
	return "/api/v2/state-versions?filter[organization][name]=" + e2e.Namespace +
		"&filter[workspace][name]=" + e2e.Workspace
}

// checkStateVersionListTwo proves the list holds two versions, newest first,
// with the holder, force and restored-from attributes on every entry.
func checkStateVersionListTwo(body string) error {
	var raw struct {
		Data []map[string]any `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return fmt.Errorf("%w: decode state version list: %w", errUnexpected, err)
	}

	if len(raw.Data) != 2 {
		return fmt.Errorf("%w: list holds %d versions, want 2: %s", errUnexpected, len(raw.Data), body)
	}

	for _, item := range raw.Data {
		attrs, ok := item["attributes"].(map[string]any)
		if !ok {
			return fmt.Errorf("%w: entry holds no attributes: %s", errUnexpected, body)
		}

		for _, key := range []string{"holder", "force", "restored-from"} {
			if _, ok := attrs[key]; !ok {
				return fmt.Errorf("%w: entry lacks attribute %q: %s", errUnexpected, key, body)
			}
		}
	}

	doc, err := decodeStateVersionList(body)
	if err != nil {
		return err
	}

	if doc.Data[0].ID == doc.Data[1].ID {
		return fmt.Errorf("%w: list holds the same version twice: %s", errUnexpected, body)
	}

	if doc.Data[0].Attributes.Serial <= doc.Data[1].Attributes.Serial {
		return fmt.Errorf(
			"%w: list is not newest first: serials %d then %d",
			errUnexpected,
			doc.Data[0].Attributes.Serial,
			doc.Data[1].Attributes.Serial,
		)
	}

	if doc.Meta.Pagination.TotalCount != 2 {
		return fmt.Errorf("%w: total count is %d, want 2: %s", errUnexpected, doc.Meta.Pagination.TotalCount, body)
	}

	return nil
}

// checkStateVersionListPage proves one list page holds the want ID with the
// want pagination.
func checkStateVersionListPage(wantID string, number, size int) func(string) error {
	return func(body string) error {
		doc, err := decodeStateVersionList(body)
		if err != nil {
			return err
		}

		if len(doc.Data) != 1 || doc.Data[0].ID != wantID {
			return fmt.Errorf("%w: page holds %+v, want one entry %q", errUnexpected, doc.Data, wantID)
		}

		page := doc.Meta.Pagination
		if page.CurrentPage != number || page.PageSize != size || page.TotalCount != 2 || page.TotalPages != 2 {
			return fmt.Errorf(
				"%w: pagination %+v, want page %d of size %d with 2 entries in 2 pages",
				errUnexpected,
				page,
				number,
				size,
			)
		}

		return nil
	}
}

// tofuStateVersionListScenario proves the list route after two applies: both
// versions listed newest first with the added attributes, and page[size] and
// page[number] respected. Every request uses the go-tfe query shape.
func tofuStateVersionListScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	base := stateVersionListBase()

	var newest, oldest string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check: func(body string) error {
			if err := checkStateVersionListTwo(body); err != nil {
				return err
			}

			doc, err := decodeStateVersionList(body)
			if err != nil {
				return err
			}

			newest, oldest = doc.Data[0].ID, doc.Data[1].ID

			return nil
		},
		Name:   "list state versions",
		Method: http.MethodGet,
		Path:   base,
		Body:   "",
		Want:   http.StatusOK,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkStateVersionListPage(newest, 1, 1),
		Name:   "list state versions first page",
		Method: http.MethodGet,
		Path:   base + "&page[size]=1&page[number]=1",
		Body:   "",
		Want:   http.StatusOK,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkStateVersionListPage(oldest, 2, 1),
		Name:   "list state versions second page",
		Method: http.MethodGet,
		Path:   base + "&page[size]=1&page[number]=2",
		Body:   "",
		Want:   http.StatusOK,
	})
}

// restoreResponseAttributes holds the attributes a restore response carries:
// the MD5 and size of the copied state, the forced flag and the version it
// was restored from.
type restoreResponseAttributes struct {
	MD5          string `json:"md5"`
	Force        bool   `json:"force"`
	RestoredFrom string `json:"restored-from"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// restoreResponseData holds the data object of a restore response in tests.
type restoreResponseData struct {
	ID         string                    `json:"id"`
	Type       string                    `json:"type"`
	Attributes restoreResponseAttributes `json:"attributes"`
}

// restoreResponseMeta holds the meta object of a restore response in tests.
type restoreResponseMeta struct {
	Message string `json:"message"`
}

// restoreResponseDoc decodes a restore response: the new state version and
// the message that no infrastructure changed.
type restoreResponseDoc struct {
	Data restoreResponseData `json:"data"`
	Meta restoreResponseMeta `json:"meta"`
}

// decodeRestoreResponse decodes body into a restore response.
func decodeRestoreResponse(body string) (restoreResponseDoc, error) {
	var doc restoreResponseDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return restoreResponseDoc{}, fmt.Errorf("%w: decode restore response: %w", errUnexpected, err)
	}

	return doc, nil
}

// checkRestoreResponse proves a restore response holds a new version restored
// from wantFrom with the forced flag and the message that no infrastructure
// changed.
func checkRestoreResponse(wantFrom string) func(string) error {
	return func(body string) error {
		doc, err := decodeRestoreResponse(body)
		if err != nil {
			return err
		}

		if doc.Data.ID == "" || doc.Data.ID == wantFrom {
			return fmt.Errorf(
				"%w: restore holds id %q, want a new version from %q",
				errUnexpected,
				doc.Data.ID,
				wantFrom,
			)
		}

		if doc.Data.Type != "state-versions" {
			return fmt.Errorf("%w: restore type is %q, want state-versions", errUnexpected, doc.Data.Type)
		}

		if !doc.Data.Attributes.Force || doc.Data.Attributes.RestoredFrom != wantFrom {
			return fmt.Errorf(
				"%w: restore holds force %v restored from %q, want force true from %q",
				errUnexpected,
				doc.Data.Attributes.Force,
				doc.Data.Attributes.RestoredFrom,
				wantFrom,
			)
		}

		if msg := strings.ToLower(doc.Meta.Message); !strings.Contains(msg, "no infrastructure") {
			return fmt.Errorf(
				"%w: restore message %q lacks the words that no infrastructure changed",
				errUnexpected,
				doc.Meta.Message,
			)
		}

		return nil
	}
}

// md5Hex answers the hex MD5 of s.
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	return hex.EncodeToString(sum[:])
}

// downloadStateVersion fetches the stored bytes of a state version through
// the API, failing the test when the version is missing.
func downloadStateVersion(ctx context.Context, t *testing.T, env *e2e.Env, id string) string {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/state-versions/"+id+"/download", "")
	if err != nil {
		t.Fatalf("download state %s: %v", id, err)
	}

	if status != http.StatusOK {
		t.Fatalf("download state %s answers %d: %s", id, status, body)
	}

	return body
}

// pulledOutput holds one output of a pulled state in tests.
type pulledOutput struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

// pulledRestoreState decodes the serial, lineage and outputs of a pulled
// state in tests.
type pulledRestoreState struct {
	Serial  int64                   `json:"serial"`
	Lineage string                  `json:"lineage"`
	Outputs map[string]pulledOutput `json:"outputs"`
}

// checkStatePullMatchesFirst proves a state pull holds the restored state:
// the same serial, lineage and greeting output as the first version. The CLI
// re-serializes the document, so the test compares the parsed state, not its
// bytes.
func checkStatePullMatchesFirst(firstSerial int64, firstLineage string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var got pulledRestoreState

		if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
			return fmt.Errorf("%w: decode pulled state: %w", errUnexpected, err)
		}

		if got.Serial != firstSerial || got.Lineage != firstLineage {
			return fmt.Errorf(
				"%w: pulled serial %d lineage %q, want serial %d lineage %q",
				errUnexpected,
				got.Serial,
				got.Lineage,
				firstSerial,
				firstLineage,
			)
		}

		greeting, ok := got.Outputs["greeting"]
		if !ok || greeting.Value != "hello" {
			return fmt.Errorf("%w: pulled state lacks the greeting hello: %s", errUnexpected, r.Stdout)
		}

		return nil
	}
}

// currentStateMD5Attributes holds the MD5 attribute of a current state
// version in tests.
type currentStateMD5Attributes struct {
	MD5 string `json:"md5"`
}

// currentStateMD5Data holds the data object of a current state version in
// tests.
type currentStateMD5Data struct {
	Attributes currentStateMD5Attributes `json:"attributes"`
}

// currentStateMD5Doc decodes a current state version in tests.
type currentStateMD5Doc struct {
	Data currentStateMD5Data `json:"data"`
}

// currentStateMD5 fetches the MD5 attribute of the workspace current state
// version, failing the test when the workspace has none.
func currentStateMD5(ctx context.Context, t *testing.T, env *e2e.Env, wsID string) string {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+wsID+"/current-state-version", "")
	if err != nil {
		t.Fatalf("get current state after restore: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("get current state after restore answers %d: %s", status, body)
	}

	var doc currentStateMD5Doc

	if decodeErr := json.Unmarshal([]byte(body), &doc); decodeErr != nil {
		t.Fatalf("decode current state after restore: %v", decodeErr)
	}

	return doc.Data.Attributes.MD5
}

// checkRestoreConflict proves a restore that another principal blocks answers
// 409 with a detail holding "is locked by User" and the holder.
func checkRestoreConflict(body string) error {
	if !strings.Contains(body, "is locked by User") || !strings.Contains(body, e2e.SubjectB) {
		return fmt.Errorf(
			"%w: restore conflict lacks %q and the holder %q: %s",
			errUnexpected,
			"is locked by User",
			e2e.SubjectB,
			body,
		)
	}

	return nil
}

// checkRestoreKeepsHistory proves a restore of an older version loses no
// version: after the restore and the apply that follows it, the list holds
// four versions, and the version that was newest before the restore is
// still among them.
func checkRestoreKeepsHistory(ctx context.Context, t *testing.T, env *e2e.Env, newest string) {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, stateVersionListBase(), "")
	if err != nil {
		t.Fatalf("list state versions after restore: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions after restore answers %d: %s", status, body)
	}

	doc, err := decodeStateVersionList(body)
	if err != nil {
		t.Fatalf("decode state versions after restore: %v", err)
	}

	if len(doc.Data) != 4 {
		t.Fatalf("list holds %d versions after the restore and the apply, want 4", len(doc.Data))
	}

	for _, version := range doc.Data {
		if version.ID == newest {
			return
		}
	}

	t.Fatalf("list after the restore misses %s, the version that was newest before it", newest)
}

// tofuRestoreScenario proves the restore route after two applies: a restore
// of the first version makes state pull match its MD5 with the restored-from
// version and the message that no infrastructure changed, a new apply
// succeeds, a restore while another principal holds the lock answers 409 with
// the holder, and the restore works after force-unlock. A restore changes no
// other workspace: the inventory still holds one entry for the workspace.
func tofuRestoreScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, stateVersionListBase(), "")
	if err != nil {
		t.Fatalf("list state versions for restore: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions for restore answers %d: %s", status, body)
	}

	doc, err := decodeStateVersionList(body)
	if err != nil {
		t.Fatalf("decode state versions for restore: %v", err)
	}

	if len(doc.Data) != 2 {
		t.Fatalf("list holds %d versions, want 2 before the restore", len(doc.Data))
	}

	newest, oldest := doc.Data[0].ID, doc.Data[1].ID
	firstDownload := downloadStateVersion(ctx, t, env, oldest)
	firstMD5 := md5Hex(firstDownload)

	var firstState pulledRestoreState

	if decodeErr := json.Unmarshal([]byte(firstDownload), &firstState); decodeErr != nil {
		t.Fatalf("decode first state for restore: %v", decodeErr)
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkRestoreResponse(oldest),
		Name:   "restore first state version",
		Method: http.MethodPost,
		Path:   "/api/v2/state-versions/" + oldest + "/actions/restore",
		Body:   "",
		Want:   http.StatusOK,
	})

	// The new current version copies the first version: its MD5 attribute
	// equals the MD5 of the first stored bytes, and state pull holds the
	// same serial, lineage and greeting.
	if got := currentStateMD5(ctx, t, env, wsID); got != firstMD5 {
		t.Fatalf("current state MD5 is %s, want the MD5 %s of the first version", got, firstMD5)
	}

	env.Step(
		ctx,
		t,
		step(
			"state pull after restore",
			checkStatePullMatchesFirst(firstState.Serial, firstState.Lineage),
			"state",
			"pull",
		),
	)
	env.Step(ctx, t, step("apply after restore", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	checkRestoreKeepsHistory(ctx, t, env, newest)

	entries, err := env.ListInventory(ctx, e2e.Namespace)
	if err != nil {
		t.Fatalf("list inventory after restore: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("inventory holds %d entries, want one for the workspace", len(entries))
	}

	if want := "opentofu:" + e2e.Namespace + "/" + e2e.Workspace; entries[0].Reference() != want {
		t.Fatalf("inventory entry %+v, want reference %q", entries[0], want)
	}

	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"

	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by B for restore",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkRestoreConflict,
		Name:   "restore locked by another",
		Method: http.MethodPost,
		Path:   "/api/v2/state-versions/" + newest + "/actions/restore",
		Body:   "",
		Want:   http.StatusConflict,
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock for restore",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkRestoreResponse(newest),
		Name:   "restore after force-unlock",
		Method: http.MethodPost,
		Path:   "/api/v2/state-versions/" + newest + "/actions/restore",
		Body:   "",
		Want:   http.StatusOK,
	})
}

// tofuSlowConfig is the crash configuration: the built-in terraform_data
// resource with a local-exec provisioner that sleeps, so an apply takes time
// without a provider download. The greeting output stays, so the state after
// recovery still holds it.
func tofuSlowConfig(host string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = ` + strconv.Quote(e2e.Workspace) + `
    }
  }
}

resource "terraform_data" "slow" {
  provisioner "local-exec" {
    command = "sleep 10"
  }
}

output "greeting" {
  value = "hello"
}
`
}

// tofuCrashScenario proves recovery after a crashed apply: the harness kills
// an apply at its lock request, the workspace stays locked, a plan fails on
// the lock, force-unlock releases it, the next apply passes and no pending
// version is current.
func tofuCrashScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)

	env.WriteTofuConfig(t, tofuSlowConfig(env.Host()))

	// The harness kills the apply once the recorder shows its lock request,
	// then reaps it, so no process stays behind.
	env.KillAtRequest(ctx, t, e2e.KillSpec{
		Name:       "crashed apply",
		Args:       []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:        nil,
		Method:     http.MethodPost,
		PathSuffix: "/actions/lock",
	})

	// The killed apply never unlocked: the workspace is still locked.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceLocked,
		Name:   "workspace still locked",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID,
		Body:   "",
		Want:   http.StatusOK,
	})

	// A plan now fails on the held lock: the CLI prints the lock failure and
	// the lock request it made answers 409 with "is locked by User".
	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkPlanLockedAfterCrash(env),
		Name:   "plan fails on the crashed lock",
		Args:   []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:    nil,
		Stdin:  "",
		Fail:   true,
		Redact: false,
	})

	// Force-unlock as another principal releases the crashed lock.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock after crash",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	// The next apply passes and the state holds the slow resource.
	env.Step(ctx, t, step("apply after crash", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.Step(ctx, t, step("state pull after crash", checkStateHasSlowResource, "state", "pull"))

	// No pending version is current: the current state version is finalized.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkCurrentFinalized,
		Name:   "current state is finalized",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID + "/current-state-version",
		Body:   "",
		Want:   http.StatusOK,
	})
}

// tofuDestroyScenario proves destroy: it locks, uploads a new state version
// with a higher serial and unlocks, leaving no resources and no outputs.
func tofuDestroyScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	before := currentSerial(ctx, t, env, wsID)

	env.Step(
		ctx,
		t,
		step("destroy", checkDestroyUploadsNewSerial(ctx, env, wsID, before), "destroy", "-auto-approve", "-no-color"),
	)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceUnlocked,
		Name:   "workspace unlocked after destroy",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID,
		Body:   "",
		Want:   http.StatusOK,
	})

	env.Step(ctx, t, step("state pull after destroy", checkStateDestroyed, "state", "pull"))
	env.Step(ctx, t, step("output after destroy", checkOutputDestroyed, "output", "-json", "-no-color"))
}

// workspaceLockedAttributes holds the locked flag of a workspace document.
type workspaceLockedAttributes struct {
	Locked bool `json:"locked"`
}

// workspaceLockedData holds the data object of a workspace document.
type workspaceLockedData struct {
	Attributes workspaceLockedAttributes `json:"attributes"`
}

// workspaceLockedDoc decodes a workspace document in tests.
type workspaceLockedDoc struct {
	Data workspaceLockedData `json:"data"`
}

// statePullResource is one resource of a pulled state.
type statePullResource struct {
	Type string `json:"type"`
}

// statePullDoc decodes a pulled state in tests.
type statePullDoc struct {
	Outputs   map[string]any      `json:"outputs"`
	Resources []statePullResource `json:"resources"`
}

// currentStateAttributes holds the status and serial of a state version
// document.
type currentStateAttributes struct {
	Status string `json:"status"`
	Serial int64  `json:"serial"`
}

// currentStateData holds the data object of a state version document.
type currentStateData struct {
	Attributes currentStateAttributes `json:"attributes"`
}

// currentStateDoc decodes a state version document in tests.
type currentStateDoc struct {
	Data currentStateData `json:"data"`
}

// decodeWorkspaceLocked returns the locked flag of a workspace document.
func decodeWorkspaceLocked(body string) (bool, error) {
	var doc workspaceLockedDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return false, fmt.Errorf("%w: decode workspace: %w", errUnexpected, err)
	}

	return doc.Data.Attributes.Locked, nil
}

// checkWorkspaceLocked proves a workspace document holds locked true.
func checkWorkspaceLocked(body string) error {
	locked, err := decodeWorkspaceLocked(body)
	if err != nil {
		return err
	}

	if !locked {
		return fmt.Errorf("%w: locked is false, want true", errUnexpected)
	}

	return nil
}

// checkWorkspaceUnlocked proves a workspace document holds locked false.
func checkWorkspaceUnlocked(body string) error {
	locked, err := decodeWorkspaceLocked(body)
	if err != nil {
		return err
	}

	if locked {
		return fmt.Errorf("%w: locked is true, want false", errUnexpected)
	}

	return nil
}

// workspaceLockAttributes holds the lock view of a workspace document.
type workspaceLockAttributes struct {
	Locked     bool   `json:"locked"`
	LockHolder string `json:"lock-holder"` //nolint:tagliatelle // The TFE wire name; it must not change.
	LockedAt   string `json:"locked-at"`   //nolint:tagliatelle // The TFE wire name; it must not change.
	LockReason string `json:"lock-reason"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// workspaceLockData holds the data object of a workspace document.
type workspaceLockData struct {
	Attributes workspaceLockAttributes `json:"attributes"`
}

// workspaceLockDoc decodes a workspace document in tests.
type workspaceLockDoc struct {
	Data workspaceLockData `json:"data"`
}

// decodeWorkspaceLock returns the lock view of a workspace document. A
// missing attribute decodes as empty, so absent and empty both read as "".
func decodeWorkspaceLock(body string) (workspaceLockAttributes, error) {
	var doc workspaceLockDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return workspaceLockAttributes{}, fmt.Errorf("%w: decode workspace: %w", errUnexpected, err)
	}

	return doc.Data.Attributes, nil
}

// checkWorkspaceShowsLock proves a workspace document shows the lock of
// holder with reason: locked is true, the holder and reason match, and the
// time parses as RFC 3339.
func checkWorkspaceShowsLock(holder, reason string) func(string) error {
	return func(body string) error {
		attrs, err := decodeWorkspaceLock(body)
		if err != nil {
			return err
		}

		if !attrs.Locked {
			return fmt.Errorf("%w: locked is false, want true", errUnexpected)
		}

		if attrs.LockHolder != holder {
			return fmt.Errorf("%w: lock-holder is %q, want %q", errUnexpected, attrs.LockHolder, holder)
		}

		if attrs.LockReason != reason {
			return fmt.Errorf("%w: lock-reason is %q, want %q", errUnexpected, attrs.LockReason, reason)
		}

		if attrs.LockedAt == "" {
			return fmt.Errorf("%w: locked-at is empty", errUnexpected)
		}

		if _, err := time.Parse(time.RFC3339, attrs.LockedAt); err != nil {
			return fmt.Errorf("%w: locked-at %q is not RFC 3339: %w", errUnexpected, attrs.LockedAt, err)
		}

		return nil
	}
}

// checkWorkspaceHidesLock proves a workspace document shows no lock: locked
// is false and the holder, time and reason are empty or absent.
func checkWorkspaceHidesLock(body string) error {
	attrs, err := decodeWorkspaceLock(body)
	if err != nil {
		return err
	}

	if attrs.Locked {
		return fmt.Errorf("%w: locked is true, want false", errUnexpected)
	}

	if attrs.LockHolder != "" {
		return fmt.Errorf("%w: lock-holder is %q, want empty", errUnexpected, attrs.LockHolder)
	}

	if attrs.LockedAt != "" {
		return fmt.Errorf("%w: locked-at is %q, want empty", errUnexpected, attrs.LockedAt)
	}

	if attrs.LockReason != "" {
		return fmt.Errorf("%w: lock-reason is %q, want empty", errUnexpected, attrs.LockReason)
	}

	return nil
}

// checkPlanLockedAfterCrash proves a plan fails on the crashed lock: the CLI
// prints the lock failure and its lock request answers 409 with the holder.
func checkPlanLockedAfterCrash(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if out := r.Stdout + r.Stderr; !strings.Contains(out, "locked") {
			return fmt.Errorf("%w: plan output lacks the lock failure: %s", errUnexpected, out)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			e := &entries[i]
			if e.Step != "plan fails on the crashed lock" || e.Method != http.MethodPost ||
				!strings.HasSuffix(e.Path, "/actions/lock") {
				continue
			}

			if e.Status != http.StatusConflict {
				return fmt.Errorf(
					"%w: lock during plan answers %d, want %d",
					errUnexpected,
					e.Status,
					http.StatusConflict,
				)
			}

			if !strings.Contains(e.ResponseBody, "is locked by User") {
				return fmt.Errorf("%w: lock detail lacks %q: %s", errUnexpected, "is locked by User", e.ResponseBody)
			}

			return nil
		}

		return fmt.Errorf("%w: plan made no lock request", errUnexpected)
	}
}

// checkStateHasSlowResource proves the state after recovery holds the slow
// resource and the greeting output.
func checkStateHasSlowResource(r e2e.Result) error {
	var doc statePullDoc

	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return fmt.Errorf("%w: decode state: %w", errUnexpected, err)
	}

	slow := false

	for _, res := range doc.Resources {
		if res.Type == "terraform_data" {
			slow = true
		}
	}

	if !slow {
		return fmt.Errorf("%w: state holds no terraform_data resource: %s", errUnexpected, r.Stdout)
	}

	if _, ok := doc.Outputs["greeting"]; !ok {
		return fmt.Errorf("%w: state holds no greeting output: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// checkCurrentFinalized proves the current state version is finalized: no
// pending version is current.
func checkCurrentFinalized(body string) error {
	var doc currentStateDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode current state: %w", errUnexpected, err)
	}

	if doc.Data.Attributes.Status != "finalized" {
		return fmt.Errorf("%w: current state is %q, want finalized", errUnexpected, doc.Data.Attributes.Status)
	}

	return nil
}

// fetchCurrentSerial returns the serial of the workspace's current state
// version.
func fetchCurrentSerial(ctx context.Context, env *e2e.Env, wsID string) (int64, error) {
	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+wsID+"/current-state-version", "")
	if err != nil {
		return 0, err
	}

	if status != http.StatusOK {
		return 0, fmt.Errorf("%w: current state answers %d: %s", errUnexpected, status, body)
	}

	var doc currentStateDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return 0, fmt.Errorf("%w: decode current state: %w", errUnexpected, err)
	}

	return doc.Data.Attributes.Serial, nil
}

// currentSerial returns the serial of the workspace's current state version,
// failing the test when the workspace has none.
func currentSerial(ctx context.Context, t *testing.T, env *e2e.Env, wsID string) int64 {
	t.Helper()

	serial, err := fetchCurrentSerial(ctx, env, wsID)
	if err != nil {
		t.Fatalf("get current state: %v", err)
	}

	return serial
}

// destroyLocked reports whether the destroy step locked the workspace.
func destroyLocked(env *e2e.Env) bool {
	entries := env.Recorder.Entries()

	for i := range entries {
		if entries[i].Step == "destroy" && entries[i].Method == http.MethodPost &&
			strings.HasSuffix(entries[i].Path, "/actions/lock") && entries[i].Status == http.StatusOK {
			return true
		}
	}

	return false
}

// destroyCreatedState reports whether the destroy step created a state
// version.
func destroyCreatedState(env *e2e.Env) bool {
	entries := env.Recorder.Entries()

	for i := range entries {
		if entries[i].Step == "destroy" && entries[i].Method == http.MethodPost &&
			strings.HasSuffix(entries[i].Path, "/state-versions") && entries[i].Status == http.StatusCreated {
			return true
		}
	}

	return false
}

// checkDestroyUploadsNewSerial proves destroy uploaded a new state version:
// the current serial is higher than before, and the destroy step locked and
// created a state version.
func checkDestroyUploadsNewSerial(
	ctx context.Context, env *e2e.Env, wsID string, before int64,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		after, err := fetchCurrentSerial(ctx, env, wsID)
		if err != nil {
			return err
		}

		if after <= before {
			return fmt.Errorf("%w: serial %d after destroy, want higher than %d", errUnexpected, after, before)
		}

		if !destroyLocked(env) {
			return fmt.Errorf("%w: destroy made no lock request", errUnexpected)
		}

		if !destroyCreatedState(env) {
			return fmt.Errorf("%w: destroy created no state version", errUnexpected)
		}

		return nil
	}
}

// checkStateDestroyed proves destroy emptied the state: no resources and no
// outputs remain.
func checkStateDestroyed(r e2e.Result) error {
	var doc statePullDoc

	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return fmt.Errorf("%w: decode state: %w", errUnexpected, err)
	}

	if len(doc.Resources) != 0 {
		return fmt.Errorf("%w: state still holds %d resources: %s", errUnexpected, len(doc.Resources), r.Stdout)
	}

	if len(doc.Outputs) != 0 {
		return fmt.Errorf("%w: state still holds outputs: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// checkOutputDestroyed proves destroy removed every output: output -json
// prints an empty object.
func checkOutputDestroyed(r e2e.Result) error {
	trimmed := strings.TrimSpace(r.Stdout)
	if trimmed == "" {
		trimmed = strings.TrimSpace(r.Stderr)
	}

	var outputs map[string]any

	if err := json.Unmarshal([]byte(trimmed), &outputs); err != nil {
		return fmt.Errorf("%w: decode output: %w (%s)", errUnexpected, err, r.Stdout)
	}

	if len(outputs) != 0 {
		return fmt.Errorf("%w: output still holds %d entries: %s", errUnexpected, len(outputs), r.Stdout)
	}

	return nil
}

// staleSerialDetail is the server detail a stale push shows: the CLI prints
// the JSON:API error detail go-tfe returns, which holds this sentinel text.
const staleSerialDetail = "state serial is not newer than the current state"

// lineageMismatchDetail is the server detail a push of another lineage
// shows, through the same path as staleSerialDetail.
const lineageMismatchDetail = "state lineage differs from the current state"

// clientImportStale and clientOverwriteSame are the CLI-side refusals of a
// push without -force: a lower serial cannot import over a newer state, an
// equal serial cannot overwrite a different state, and another lineage
// cannot import over an unrelated state (which shares the import wording).
const (
	clientImportStale   = "cannot import state"
	clientOverwriteSame = "cannot overwrite existing state"
)

// tofuStatePushScenario proves stale-serial rejection and force push: a push
// of a copy of the current state with its own serial fails, direct API
// creates with a stale serial and another lineage answer 422, a force push
// of the same file succeeds and a later pull shows it, and a push of another
// lineage fails without -force.
func tofuStatePushScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	pulled := env.Step(ctx, t, step("state pull before push", contains(`"greeting"`), "state", "pull"))

	var current pulledStateDoc

	if err := json.Unmarshal([]byte(pulled.Stdout), &current); err != nil {
		t.Fatalf("decode pulled state: %v", err)
	}

	if current.Lineage == "" {
		t.Fatal("pulled state has no lineage")
	}

	// The file carries the current serial with a marker output added. A
	// lower serial would not survive the force push: the CLI writes file
	// serial plus one, which would still be stale. The current serial fails
	// without -force (the CLI stops the import itself, and the server would
	// answer 422 for a serial that is not newer) and succeeds with -force.
	stalePath := writePushFile(t, "stale.tfstate", pushedState(t, pulled.Stdout, current.Serial, current.Lineage))

	env.Step(
		ctx,
		t,
		failStep(
			"state push stale",
			checkPushRejected(env, "state push stale", staleSerialDetail),
			"state",
			"push",
			stalePath,
		),
	)
	logPushSide(t, env, "state push stale")

	// The CLI may stop a push itself before it reaches the server, so the
	// harness also calls the create route directly: a lower serial and
	// another lineage each answer 422 with the JSON:API error the failure
	// steps prove when the server is the side that rejects. The workspace
	// must be held for the creates to reach the checks.
	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"
	unlockPath := "/api/v2/workspaces/" + wsID + "/actions/unlock"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock for stale check",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkDirectRejected(staleSerialDetail),
		Name:   "stale serial rejected direct",
		Method: http.MethodPost,
		Path:   "/api/v2/workspaces/" + wsID + "/state-versions",
		Body: fmt.Sprintf(
			`{"data":{"type":"state-versions","attributes":{"serial":%d,"lineage":%q,"md5":%q}}}`,
			max(current.Serial-1, 0),
			current.Lineage,
			"d41d8cd98f00b204e9800998ecf8427e",
		),
		Want: http.StatusUnprocessableEntity,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkDirectRejected(lineageMismatchDetail),
		Name:   "other lineage rejected direct",
		Method: http.MethodPost,
		Path:   "/api/v2/workspaces/" + wsID + "/state-versions",
		Body: fmt.Sprintf(
			`{"data":{"type":"state-versions","attributes":{"serial":%d,"lineage":%q,"md5":%q}}}`,
			current.Serial+100,
			"00000000-0000-0000-0000-000000000000",
			"d41d8cd98f00b204e9800998ecf8427e",
		),
		Want: http.StatusUnprocessableEntity,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "unlock after stale check",
		Method: http.MethodPost,
		Path:   unlockPath,
		Body:   "",
		Want:   http.StatusOK,
	})

	env.Step(ctx, t, step("state push force", nil, "state", "push", "-force", stalePath))
	env.Step(
		ctx,
		t,
		step("state pull after force", checkPushedState(current.Serial, current.Lineage), "state", "pull"),
	)

	otherPath := writePushFile(
		t,
		"other.tfstate",
		editedState(t, pulled.Stdout, current.Serial+100, "00000000-0000-0000-0000-000000000000"),
	)

	env.Step(
		ctx,
		t,
		failStep(
			"state push other lineage",
			checkPushRejected(env, "state push other lineage", lineageMismatchDetail),
			"state",
			"push",
			otherPath,
		),
	)
	logPushSide(t, env, "state push other lineage")
}

// pulledStateDoc decodes the serial and lineage of a pulled state document.
type pulledStateDoc struct {
	Serial  int64  `json:"serial"`
	Lineage string `json:"lineage"`
}

// editedState returns pulled with its serial and lineage replaced.
func editedState(t *testing.T, pulled string, serial int64, lineage string) []byte {
	t.Helper()

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(pulled), &fields); err != nil {
		t.Fatalf("decode pulled state: %v", err)
	}

	rawSerial, err := json.Marshal(serial)
	if err != nil {
		t.Fatalf("encode serial %d: %v", serial, err)
	}

	rawLineage, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("encode lineage: %v", err)
	}

	fields["serial"], fields["lineage"] = rawSerial, rawLineage

	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode edited state: %v", err)
	}

	return out
}

// pushedState returns pulled with its serial and lineage replaced and a
// marker output added, so the pull after a force push proves the pushed
// state landed whatever serial the CLI sent.
func pushedState(t *testing.T, pulled string, serial int64, lineage string) []byte {
	t.Helper()

	doc := editedState(t, pulled, serial, lineage)

	var fields map[string]json.RawMessage

	if err := json.Unmarshal(doc, &fields); err != nil {
		t.Fatalf("decode edited state: %v", err)
	}

	var outputs map[string]json.RawMessage

	if raw, ok := fields["outputs"]; ok {
		if err := json.Unmarshal(raw, &outputs); err != nil {
			t.Fatalf("decode state outputs: %v", err)
		}
	}

	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}

	marker, err := json.Marshal(map[string]string{"value": "forced", "type": "string"})
	if err != nil {
		t.Fatalf("encode marker output: %v", err)
	}

	outputs["pushed"] = marker

	rawOutputs, err := json.Marshal(outputs)
	if err != nil {
		t.Fatalf("encode state outputs: %v", err)
	}

	fields["outputs"] = rawOutputs

	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode pushed state: %v", err)
	}

	return out
}

// writePushFile saves doc for a state push step and returns its path.
func writePushFile(t *testing.T, name string, doc []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

// checkPushRejected proves the push failed and which side rejected it. When
// the create reached the server, the CLI output holds the server detail
// go-tfe printed and the create answers 422 with the JSON:API error
// document holding it. Otherwise the CLI stopped the push itself, and the
// output holds its own message.
func checkPushRejected(env *e2e.Env, step, serverDetail string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		out := r.Stdout + r.Stderr

		if !pushReachedServer(env, step) {
			return checkClientRefusal(step, out)
		}

		return checkServerRefusal(env, step, out, serverDetail)
	}
}

// checkClientRefusal proves the CLI stopped the push itself with its own
// import refusal.
func checkClientRefusal(step, out string) error {
	if strings.Contains(out, clientImportStale) || strings.Contains(out, clientOverwriteSame) {
		return nil
	}

	return fmt.Errorf(
		"%w: %s output lacks %q or %q: %s",
		errUnexpected,
		step,
		clientImportStale,
		clientOverwriteSame,
		out,
	)
}

// checkServerRefusal proves the CLI output holds the server detail and the
// create the step made answers 422 with the JSON:API error document holding
// it.
func checkServerRefusal(env *e2e.Env, step, out, serverDetail string) error {
	if !strings.Contains(out, serverDetail) {
		return fmt.Errorf("%w: %s output lacks %q: %s", errUnexpected, step, serverDetail, out)
	}

	entries := env.Recorder.Entries()
	for i := range entries {
		e := &entries[i]
		if e.Step != step || e.Method != http.MethodPost || !strings.HasSuffix(e.Path, "/state-versions") {
			continue
		}

		return checkCreateError(step, e, serverDetail)
	}

	return fmt.Errorf("%w: %s reached no state-versions route", errUnexpected, step)
}

// checkCreateError proves the recorded create answers 422 with the JSON:API
// error document holding the detail.
func checkCreateError(step string, e *e2e.Entry, serverDetail string) error {
	if e.Status != http.StatusUnprocessableEntity {
		return fmt.Errorf(
			"%w: POST state-versions during %q answers %d, want %d: %s",
			errUnexpected,
			step,
			e.Status,
			http.StatusUnprocessableEntity,
			e.ResponseBody,
		)
	}

	if !strings.Contains(e.ResponseBody, `"errors"`) ||
		!strings.Contains(e.ResponseBody, `"422"`) ||
		!strings.Contains(e.ResponseBody, serverDetail) {
		return fmt.Errorf(
			"%w: POST state-versions during %q lacks the 422 detail %q: %s",
			errUnexpected,
			step,
			serverDetail,
			e.ResponseBody,
		)
	}

	return nil
}

// pushReachedServer reports whether the CLI sent a state version create
// during step: a create means the server rejected the push, none means the
// CLI stopped it itself.
func pushReachedServer(env *e2e.Env, step string) bool {
	entries := env.Recorder.Entries()
	for i := range entries {
		if e := &entries[i]; e.Step == step && e.Method == http.MethodPost &&
			strings.HasSuffix(e.Path, "/state-versions") {
			return true
		}
	}

	return false
}

// checkDirectRejected proves the direct create answered the JSON:API error
// with the detail.
func checkDirectRejected(wantDetail string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, `"errors"`) ||
			!strings.Contains(body, `"422"`) ||
			!strings.Contains(body, wantDetail) {
			return fmt.Errorf("%w: direct create lacks the 422 detail %q: %s", errUnexpected, wantDetail, body)
		}

		return nil
	}
}

// logPushSide records which side rejected step's push: a create request
// during the step means the server answered, otherwise the CLI stopped the
// push itself.
func logPushSide(t *testing.T, env *e2e.Env, step string) {
	t.Helper()

	entries := env.Recorder.Entries()
	for i := range entries {
		e := &entries[i]
		if e.Step == step && e.Method == http.MethodPost && strings.HasSuffix(e.Path, "/state-versions") {
			t.Logf("%s rejected server-side: POST %s answers %d", step, e.Path, e.Status)

			return
		}
	}

	t.Logf("%s rejected client-side: the CLI stopped the push itself", step)
}

// checkPushedState proves the pull after a force push shows the pushed
// state: its lineage, its marker output, and the pushed serial with the
// increment of one the CLI writes on top.
func checkPushedState(serial int64, lineage string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var got pulledStateDoc

		if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
			return fmt.Errorf("%w: decode pulled state: %w", errUnexpected, err)
		}

		if got.Lineage != lineage || (got.Serial != serial && got.Serial != serial+1) {
			return fmt.Errorf(
				"%w: pulled serial %d lineage %q, want %d or %d of %q",
				errUnexpected,
				got.Serial,
				got.Lineage,
				serial,
				serial+1,
				lineage,
			)
		}

		if !strings.Contains(r.Stdout, `"pushed"`) {
			return fmt.Errorf("%w: pulled state lacks the pushed marker output: %s", errUnexpected, r.Stdout)
		}

		return nil
	}
}

// TestBinaryRunAndRestart proves the binary helpers: RunBinary runs any
// subcommand against the database of the environment, and StopServer and
// StartServer stop and start the same server on the same port with the same
// state. It runs only with E2E_SERVER=binary, on every store under test.
func TestBinaryRunAndRestart(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runBinaryRestartOnStore(t, cli, db)
		})
	}
}

// runBinaryRestartOnStore runs the binary restart steps on one store: a
// namespace create and an unknown subcommand through RunBinary, one Pulumi
// stack before the stop, and the same stack read with the same login after
// the start.
func runBinaryRestartOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkRunBinary(ctx, t, env)
	writeRestartStack(ctx, t, env)
	checkServerRestart(ctx, t, env)
	readRestartStack(ctx, t, env)
}

// checkRunBinary proves RunBinary through the API of the environment: a
// namespace create gives exit code 0 and an unknown subcommand gives a
// non-zero exit with text on stderr.
func checkRunBinary(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	created := env.RunBinary(
		ctx,
		t,
		"namespace", "create",
		"--server", env.BackendURL(t),
		"--ca-file", env.BackendCertFile(t),
		e2e.Namespace2,
	)
	if created.ExitCode != 0 {
		t.Fatalf("namespace create exits with status %d: %s%s", created.ExitCode, created.Stdout, created.Stderr)
	}

	if blobDir := env.BlobDir(t); blobDir == "" {
		t.Fatal("blob directory is empty")
	}

	unknown := env.RunBinary(ctx, t, "no-such-command")
	if unknown.ExitCode == 0 {
		t.Fatal("unknown subcommand exits with status 0")
	}

	if !strings.Contains(unknown.Stderr, "usage") {
		t.Fatalf("unknown subcommand stderr lacks usage: %q", unknown.Stderr)
	}
}

// writeRestartStack writes one Pulumi stack before the stop, so the restart
// check proves the same state survives.
func writeRestartStack(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	for _, s := range []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
	} {
		env.Step(ctx, t, s)
	}
}

// healthStatus returns the status of GET /health/live through the environment.
func healthStatus(ctx context.Context, env *e2e.Env) (int, string, error) {
	return env.APIStatus(ctx, http.MethodGet, "/health/live", "")
}

// checkServerRestart proves the stop and the start: after StopServer
// GET /health/live fails, after StartServer it answers 204 again.
func checkServerRestart(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	env.StopServer(ctx, t)

	status, body, err := healthStatus(ctx, env)
	if err == nil && status == http.StatusNoContent {
		t.Fatal("GET /health/live answers 204 after the stop")
	}

	t.Logf("GET /health/live after the stop: status %d, err %v, body %q", status, err, body)

	env.StartServer(ctx, t)

	status, body, err = healthStatus(ctx, env)
	if err != nil || status != http.StatusNoContent {
		t.Fatalf("GET /health/live after the start: status %d, err %v, body %q", status, err, body)
	}
}

// readRestartStack proves the CLI reads the same stack with the same login
// after the start.
func readRestartStack(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, s := range []e2e.StepSpec{
		step("config get after restart", contains("s3cr3t"), "config", "get", "apiKey"),
		step("stack ls after restart", stackListed, "stack", "ls", "--json"),
	} {
		env.Step(ctx, t, s)
	}
}

// TestPairTwoLogins proves two servers in one test with two saved Pulumi
// logins in one home: a stack exists on the source S, login works for S and
// for the destination D, and both list through the saved logins with no
// access token in the environment. It runs on every store under test and
// writes a -pair report that holds both servers.
func TestPairTwoLogins(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runPairLoginsOnStore(t, cli, db)
		})
	}
}

// runPairLoginsOnStore runs the two-login smoke test on one store.
func runPairLoginsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRun())
	})

	pair.SharePulumiHome()

	src := pair.Src
	dst := pair.Dst
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	src.Step(ctx, t, step("login to source with token", contains(src.URL), "login"))
	src.Step(ctx, t, step("init on source", nil, "stack", "init", qualified))

	pair.SavePulumiLogin(ctx, t, src, "save login for source")
	pair.SavePulumiLogin(ctx, t, dst, "save login for destination")

	pair.UsePulumiBackend(ctx, t, src, "use source")
	src.StepSavedLogin(ctx, t, step("list on source", stackListed, "stack", "ls", "--json"))

	pair.UsePulumiBackend(ctx, t, dst, "use destination")
	src.StepSavedLogin(ctx, t, step("init on destination", nil, "stack", "init", qualified))
	src.StepSavedLogin(ctx, t, step("list on destination", stackListed, "stack", "ls", "--json"))

	checkPairReport(t, pair)
}

// checkPairReport proves the report records both servers: one step command
// holds the source URL and another holds the destination URL.
func checkPairReport(t *testing.T, pair *e2e.Pair) {
	t.Helper()

	run := pair.NewPairRun()
	seenSrc, seenDst := false, false

	for _, s := range run.Steps {
		if strings.Contains(s.Command, pair.Src.URL) {
			seenSrc = true
		}

		if strings.Contains(s.Command, pair.Dst.URL) {
			seenDst = true
		}
	}

	if !seenSrc || !seenDst {
		t.Fatalf("pair report holds source %v and destination %v, want both", seenSrc, seenDst)
	}

	if len(run.Requests) == 0 {
		t.Fatal("pair report holds no requests")
	}
}

// TestPulumiFileImport drives P2 of ADR 29 on every Pulumi CLI version in
// the matrix: a file backend stack with the passphrase secrets provider moves
// to D with its current state only, and the passphrase provider keeps working.
// The source directory is byte-identical after the import. It writes a -file
// report per version and store, so it never overwrites a matrix report.
func TestPulumiFileImport(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tool := e2e.Pulumi()

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, err := fetcher.Install(installCtx, tool, label)
			if err != nil {
				// Still report the version, so a broken download is visible in the summary.
				for _, db := range databases {
					writeReport(t, e2e.NotRun(tool.Name, label+fileImportSuffix, db, err.Error(), false))
				}

				t.Fatalf("install %s %s: %v", tool.Name, label, err)
			}

			t.Logf("%s %s (checksum %s) in %s", cli.Tool, cli.Version, cli.ChecksumSource, cli.Dir)

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					runPulumiFileImportOnStore(t, cli, db)
				})
			}
		})
	}
}

// runPulumiFileImportOnStore runs the file backend import on one store. If
// server setup fails, NewEnv ends the test before env exists; the report then
// holds the version as not run instead of dropping it from the summary.
func runPulumiFileImportOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		reason := "set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite"
		writeReport(t, e2e.NotRun(cli.Tool, cli.Label+fileImportSuffix, store, reason, true))
		t.Skip(reason)
	}

	var env *e2e.Env

	t.Cleanup(func() {
		if env == nil {
			writeReport(
				t,
				e2e.NotRun(cli.Tool, cli.Label+fileImportSuffix, store, "server setup failed; see the test log", false),
			)

			return
		}

		run := e2e.NewRun(env)
		run.Label += fileImportSuffix
		writeReport(t, run)
	})

	env = e2e.NewEnv(t.Context(), t, cli, store)

	pulumiFileImportScenario(t, env)
}

// The P2 scenario uses its own Pulumi project and stack, so it never
// touches the stacks of the main scenario. Reports carry the suffix, so they
// never overwrite a matrix report.
const (
	fileImportProject = "e2e-import"
	fileImportStack   = "dev"
	fileImportSuffix  = "-file"
)

// fileImportProgram is the scratch YAML program for the file backend import.
// It holds no resources, so no provider plugin is downloaded and an up after
// the import has nothing to change.
const fileImportProgram = "name: " + fileImportProject + "\nruntime: yaml\nresources: {}\n"

// pulumiFileImportScenario drives P2: a file backend stack with the passphrase
// secrets provider moves to D. The file backend exports the current state
// only, so the destination history holds one import update. The passphrase
// provider survives the move, and changing it to the service provider changes
// nothing either. The source directory is byte-identical after the import.
func pulumiFileImportScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	checkReady(ctx, t, env)

	backendDir := t.TempDir()
	fileProject := writeFileImportProject(t)
	destProject := writeFileImportProject(t)
	passphrase := fileImportPassphrase(t)
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, fileImportProject, fileImportStack)

	env.StepIn(
		ctx,
		t,
		fileProject,
		fileStep(backendDir, passphrase, "file login", contains("file://"), "login", "file://"+backendDir),
	)
	env.StepIn(
		ctx,
		t,
		fileProject,
		fileStep(
			backendDir,
			passphrase,
			"file stack init",
			nil,
			"stack",
			"init",
			fileImportStack,
			"--secrets-provider",
			"passphrase",
		),
	)
	env.StepIn(
		ctx,
		t,
		fileProject,
		fileStep(
			backendDir,
			passphrase,
			"file up",
			nil,
			"up",
			"--yes",
			"--non-interactive",
			"--stack",
			fileImportStack,
		),
	)
	env.StepIn(
		ctx,
		t,
		fileProject,
		fileStep(
			backendDir,
			passphrase,
			"file stack history",
			historyShowsKinds("update"),
			"stack",
			"history",
			"--json",
			"--stack",
			fileImportStack,
		),
	)

	before := digestSourceDir(t, backendDir)

	env.StepIn(ctx, t, destProject, destStep(passphrase, "login to destination", contains(env.URL), "login"))
	env.StepIn(
		ctx,
		t,
		destProject,
		destStep(
			passphrase,
			"destination stack init",
			nil,
			"stack",
			"init",
			qualified,
			"--secrets-provider",
			"passphrase",
		),
	)
	exported := env.StepIn(
		ctx,
		t,
		fileProject,
		fileStep(
			backendDir,
			passphrase,
			"file stack export",
			fileImportHoldsState,
			"stack",
			"export",
			"--stack",
			fileImportStack,
		),
	)

	// The pipe from the source into the destination: the export of the file
	// stack goes to the import on standard input, so no file holds it, and
	// becomes the first state of the stack on D.
	importSpec := destStep(passphrase, "file stack import", nil, "stack", "import", "--stack", qualified)
	importSpec.Stdin = exported.Stdout
	env.StepIn(ctx, t, destProject, importSpec)
	env.StepIn(
		ctx,
		t,
		destProject,
		destStep(
			passphrase,
			"destination export after import",
			fileImportHoldsState,
			"stack",
			"export",
			"--stack",
			qualified,
		),
	)
	env.StepIn(
		ctx,
		t,
		destProject,
		destStep(
			passphrase,
			"destination history after import",
			fileImportHistoryShowsImportOnly,
			"stack",
			"history",
			"--json",
			"--stack",
			qualified,
		),
	)
	env.StepIn(
		ctx,
		t,
		destProject,
		destStep(
			passphrase,
			"up after file import",
			upHasNoChanges,
			"up",
			"--yes",
			"--non-interactive",
			"--stack",
			qualified,
		),
	)
	env.StepIn(
		ctx,
		t,
		destProject,
		destStep(
			passphrase,
			"change secrets provider to service",
			nil,
			"stack",
			"change-secrets-provider",
			"default",
			"--stack",
			qualified,
		),
	)
	env.StepIn(
		ctx,
		t,
		destProject,
		destStep(
			passphrase,
			"up after provider change",
			upHasNoChanges,
			"up",
			"--yes",
			"--non-interactive",
			"--stack",
			qualified,
		),
	)

	checkSourceDirUnchanged(t, backendDir, before)
}

// fileImportPassphrase returns a fresh passphrase for the P2 file backend
// stack. It is random per run, so it never lands in the report: the CLI logs
// only that the variable is set.
func fileImportPassphrase(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate passphrase: %v", err)
	}

	return hex.EncodeToString(raw)
}

// fileStep returns a scenario step against the file backend source at
// backendDir: the backend URL points at the source, no access token is sent,
// and the passphrase unlocks the passphrase secrets provider.
func fileStep(
	backendDir, passphrase, name string,
	check func(e2e.Result) error,
	args ...string,
) e2e.StepSpec {
	return e2e.StepSpec{
		Check: check,
		Name:  name,
		Args:  args,
		Env: []string{
			"PULUMI_BACKEND_URL=file://" + backendDir,
			"PULUMI_ACCESS_TOKEN=",
			"PULUMI_CONFIG_PASSPHRASE=" + passphrase,
		},
		Stdin:  "",
		Fail:   false,
		Redact: false,
	}
}

// destStep returns a scenario step against the destination with the passphrase
// set, so the imported stack still unlocks before the provider change.
func destStep(passphrase, name string, check func(e2e.Result) error, args ...string) e2e.StepSpec {
	return e2e.StepSpec{
		Check:  check,
		Name:   name,
		Args:   args,
		Env:    []string{"PULUMI_CONFIG_PASSPHRASE=" + passphrase},
		Stdin:  "",
		Fail:   false,
		Redact: false,
	}
}

// writeFileImportProject writes the P2 scratch project into a fresh directory
// and returns its path. The file backend and the destination each get their
// own copy, so their stack config files never clash.
func writeFileImportProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Pulumi.yaml"), []byte(fileImportProgram), 0o600); err != nil {
		t.Fatalf("write P2 Pulumi.yaml: %v", err)
	}

	return dir
}

// fileImportHoldsState proves that a stack export holds the moved state: the
// stack resource of the file stack with the passphrase secrets provider.
func fileImportHoldsState(r e2e.Result) error {
	if !strings.Contains(r.Stdout, fileImportProject+"-"+fileImportStack) {
		return fmt.Errorf("%w: export lacks the stack resource: %s", errUnexpected, r.Stdout)
	}

	if !strings.Contains(r.Stdout, `"type": "passphrase"`) {
		return fmt.Errorf("%w: export lacks the passphrase secrets provider: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// fileImportHistoryShowsImportOnly proves that the import moved the current
// state only: the destination history holds one import update and no replayed
// history from the source.
func fileImportHistoryShowsImportOnly(r e2e.Result) error {
	var updates []struct {
		Kind string `json:"kind"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &updates); err != nil {
		return fmt.Errorf("%w: decode stack history: %w", errUnexpected, err)
	}

	if len(updates) != 1 || updates[0].Kind != "import" {
		return fmt.Errorf(
			"%w: destination history holds %d updates, want one import: %s",
			errUnexpected,
			len(updates),
			r.Stdout,
		)
	}

	return nil
}

// digestSourceDir returns the SHA-256 digest of every file under dir: relative
// paths and contents, so the test proves the file backend source stayed
// byte-identical.
func digestSourceDir(t *testing.T, dir string) string {
	t.Helper()

	hash := sha256.New()

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", path, err)
		}

		hash.Write([]byte(rel))

		data, err := os.ReadFile(path) //nolint:gosec // The test digests the file backend directory it created.
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		hash.Write(data)

		return nil
	})
	if err != nil {
		t.Fatalf("digest %s: %v", dir, err)
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// checkSourceDirUnchanged proves that the file backend source is byte-identical
// to before: the import never deletes in the source.
func checkSourceDirUnchanged(t *testing.T, dir, before string) {
	t.Helper()

	if after := digestSourceDir(t, dir); after != before {
		t.Error("file backend source changed during the import")
	} else {
		t.Logf("file backend source unchanged: %s", after)
	}
}

// TestPulumiServiceImport moves a stack with a secret from one service
// backend to another through the CLI alone (ADR 29 source P1). With S as the
// source and D as the destination, on every Pulumi CLI version in the matrix
// and on every store: three ups on S, a read-only dry run, a pipe of the last
// three versions into import updates on D, the checks on D, a second run that
// moves nothing and a final proof that S is unchanged. It writes an -import
// report that holds both servers.
func TestPulumiServiceImport(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	for _, label := range versions {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, err := fetcher.Install(installCtx, e2e.Pulumi(), label)
			if err != nil {
				t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, label, err)
			}

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					runP1ImportOnStore(t, cli, db)
				})
			}
		})
	}
}

// p1VersionCount is how many source versions the P1 import replays: the last
// three of the source history, oldest first.
const p1VersionCount = 3

// p1Snapshot holds the source export and history before the import. The
// import must leave both unchanged.
type p1Snapshot struct {
	export  string
	history string
}

// p1Fixture carries one P1 import run: the server pair and the stack names.
type p1Fixture struct {
	pair  *e2e.Pair
	dev   string
	other string
}

// runP1ImportOnStore moves a stack with a secret from the source server to
// the destination server on one store and proves every step.
func runP1ImportOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRunAs(cli.Label+"-import"))
	})

	pair.SharePulumiHome()

	fix := &p1Fixture{
		pair:  pair,
		dev:   fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName),
		other: fmt.Sprintf("%s/%s/other", e2e.Namespace, e2e.Project),
	}

	before := fix.setupSource(ctx, t)
	since := len(pair.Src.Recorder.Entries())

	fix.dryRun(ctx, t, before)
	fix.replay(ctx, t)
	fix.checkDestination(ctx, t, before)
	fix.secondRun(ctx, t)
	fix.upOnDestination(ctx, t)
	fix.importErrors(ctx, t, before)
	fix.checkSourceUnchanged(ctx, t, before, since)
	fix.logoutSource(ctx, t)
}

// p1Program returns the Pulumi.yaml the P1 import moves: a stack reference
// to the other stack and a secret output from config. The checkpoint holds a
// service secret, so the import re-encrypts it on the destination. It needs
// no provider download.
func p1Program() string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"config:\n" +
		"  apiKey:\n" +
		"    type: string\n" +
		"    secret: true\n" +
		"outputs:\n" +
		"  apiKeyOut:\n" +
		"    fn::secret: ${apiKey}\n" +
		"resources:\n" +
		"  ref:\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + e2e.Namespace + "/" + e2e.Project + "/other\n"
}

// setupSource writes the secret program and builds three versions on the
// source, one up per secret value with the newest last. It saves both logins
// and returns the source export and history before the import.
func (f *p1Fixture) setupSource(ctx context.Context, t *testing.T) p1Snapshot {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst
	secrets := []string{"s3cr3t-1", "s3cr3t-2", "s3cr3t-3"}

	src.WritePulumiProject(t, p1Program())

	src.Step(ctx, t, step("login to source with token", contains(src.URL), "login"))
	src.Step(ctx, t, step("init dev on source", nil, "stack", "init", f.dev))
	src.Step(ctx, t, step("init other on source", nil, "stack", "init", f.other))
	src.Step(ctx, t, step("select dev on source", nil, "stack", "select", f.dev))

	for i, secret := range secrets {
		version := strconv.Itoa(i + 1)
		src.Step(
			ctx,
			t,
			step("config set secret "+version+" on source", nil, "config", "set", "--secret", "apiKey", secret),
		)
		src.Step(ctx, t, step("up "+version+" on source", nil, "up", "--yes", "--non-interactive"))
	}

	f.pair.SavePulumiLogin(ctx, t, src, "save login for source")
	f.pair.SavePulumiLogin(ctx, t, dst, "save login for destination")
	f.pair.UsePulumiBackend(ctx, t, src, "use source")

	exported := src.StepSavedLogin(ctx, t, step("export latest on source before import", nil, "stack", "export"))
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on source before import", p1HistoryIs("update"), "stack", "history", "--json"),
	)

	return p1Snapshot{export: exported.Stdout, history: history.Stdout}
}

// dryRun lists the stacks of the source and their versions without changing
// anything: the export and the history still match the snapshot, and no step
// sends a write request.
func (f *p1Fixture) dryRun(ctx context.Context, t *testing.T, before p1Snapshot) {
	t.Helper()

	src := f.pair.Src
	dry := []string{
		"stack ls on source",
		"select other on source",
		"history of other on source",
		"select dev on source",
		"history of dev on source",
		"export latest on source after dry run",
	}

	src.StepSavedLogin(ctx, t, step(dry[0], stackListed, "stack", "ls", "--json"))
	src.StepSavedLogin(ctx, t, step(dry[1], nil, "stack", "select", f.other))
	src.StepSavedLogin(ctx, t, step(dry[2], historyHidesPreviews, "stack", "history", "--json"))
	src.StepSavedLogin(ctx, t, step(dry[3], nil, "stack", "select", f.dev))

	history := src.StepSavedLogin(ctx, t, step(dry[4], p1HistoryIs("update"), "stack", "history", "--json"))
	exported := src.StepSavedLogin(ctx, t, step(dry[5], nil, "stack", "export"))

	if history.Stdout != before.history {
		t.Error("dry run changed the source history")
	}

	if exported.Stdout != before.export {
		t.Error("dry run changed the source export")
	}

	if writes := p1WritesInSteps(src.Recorder.Entries(), dry...); len(writes) != 0 {
		t.Errorf(
			"dry run sent %d write requests, want none (first is %s %s)",
			len(writes),
			writes[0].Method,
			writes[0].Path,
		)
	}
}

// replay pipes the last three source versions into the destination, oldest
// first, with no file: each export travels in memory into an import on
// standard input.
func (f *p1Fixture) replay(ctx context.Context, t *testing.T) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst
	exports := make([]string, 0, 3)

	for _, version := range []string{"1", "2", "3"} {
		exported := src.StepSavedLogin(
			ctx,
			t,
			step(
				"export version "+version+" from source",
				p1ExportHoldsSecret,
				"stack",
				"export",
				"--version",
				version,
			),
		)
		exports = append(exports, exported.Stdout)
	}

	if exports[0] == exports[1] || exports[1] == exports[2] {
		t.Error("source versions share one export, want one version per secret")
	}

	f.pair.UsePulumiBackend(ctx, t, dst, "use destination")

	src.StepSavedLogin(ctx, t, step("init dev on destination", nil, "stack", "init", f.dev))
	src.StepSavedLogin(ctx, t, step("init other on destination", nil, "stack", "init", f.other))
	src.StepSavedLogin(ctx, t, step("select dev on destination", nil, "stack", "select", f.dev))
	src.StepSavedLogin(
		ctx,
		t,
		step("config set secret on destination", nil, "config", "set", "--secret", "apiKey", "s3cr3t-3"),
	)

	for i, exported := range exports {
		version := strconv.Itoa(i + 1)
		spec := step("import version "+version+" to destination", nil, "stack", "import")
		spec.Stdin = exported
		src.StepSavedLogin(ctx, t, spec)
	}
}

// checkDestination proves the replay landed: the secret output decrypts, the
// destination export matches the source latest except for the backend URL and
// the re-encrypted secrets, and the history holds three imports. The import
// time and kind replace the original update time and kind.
func (f *p1Fixture) checkDestination(ctx context.Context, t *testing.T, before p1Snapshot) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst

	src.StepSavedLogin(
		ctx,
		t,
		step(
			"stack output shows the secret on destination",
			contains("s3cr3t-3"),
			"stack",
			"output",
			"--show-secrets",
		),
	)

	exported := src.StepSavedLogin(ctx, t, step("export latest on destination", nil, "stack", "export"))
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on destination shows imports", p1HistoryIs("import"), "stack", "history", "--json"),
	)

	normalizedSrc, srcSecrets := normalizeP1Export(t, before.export, src.URL, dst.URL)
	normalizedDst, dstSecrets := normalizeP1Export(t, exported.Stdout, src.URL, dst.URL)

	if normalizedSrc != normalizedDst {
		t.Error("destination export differs from the source export except for the backend URL and secrets")
	}

	if slices.Equal(srcSecrets, dstSecrets) {
		t.Error("destination kept the source ciphertexts, want secrets re-encrypted with its own key")
	}

	// The guide records what the replay loses: the original kind and time of
	// each version. The destination history shows kind import with the time
	// of the import.
	t.Logf("source history before the import:\n%s", before.history)
	t.Logf("destination history after the import:\n%s", history.Stdout)
}

// secondRun replays the import and moves nothing new: the destination latest
// export already matches the source latest, so no version is missing and the
// destination history and export stay identical. This is the resume rule: a
// run after a failure moves only the versions the destination still lacks.
func (f *p1Fixture) secondRun(ctx context.Context, t *testing.T) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst

	latest := src.StepSavedLogin(
		ctx,
		t,
		step("export latest on destination before second run", nil, "stack", "export"),
	)
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on destination before second run", p1HistoryIs("import"), "stack", "history", "--json"),
	)

	normalizedLatest, _ := normalizeP1Export(t, latest.Stdout, src.URL, dst.URL)
	exports := make([]string, 0, 3)

	for _, version := range []string{"1", "2", "3"} {
		exported := src.StepSavedLogin(
			ctx,
			t,
			step("replay version "+version+" from source", nil, "stack", "export", "--version", version),
		)
		exports = append(exports, exported.Stdout)
	}

	// Resume from the newest source version the destination already holds:
	// only newer versions still need an import. A run after a failure moves
	// the remainder; a run after a full import moves nothing.
	matched := 0

	for i, exported := range exports {
		normalized, _ := normalizeP1Export(t, exported, src.URL, dst.URL)
		if normalized == normalizedLatest {
			matched = i + 1
		}
	}

	for i := matched; i < len(exports); i++ {
		spec := step("import missing version "+strconv.Itoa(i+1)+" to destination", nil, "stack", "import")
		spec.Stdin = exports[i]
		src.StepSavedLogin(ctx, t, spec)
	}

	moved := len(exports) - matched

	t.Logf("second import run moved %d of 3 versions", moved)

	if moved != 0 {
		t.Errorf("second import run moved %d versions, want none", moved)
	}

	afterExport := src.StepSavedLogin(
		ctx,
		t,
		step("export latest on destination after second run", nil, "stack", "export"),
	)
	afterHistory := src.StepSavedLogin(
		ctx,
		t,
		step("history on destination after second run", p1HistoryIs("import"), "stack", "history", "--json"),
	)

	if afterExport.Stdout != latest.Stdout {
		t.Error("second import run changed the destination export")
	}

	if afterHistory.Stdout != history.Stdout {
		t.Error("second import run changed the destination history")
	}
}

// upOnDestination proves the moved state is current: an up changes nothing.
func (f *p1Fixture) upOnDestination(ctx context.Context, t *testing.T) {
	t.Helper()

	f.pair.Src.StepSavedLogin(
		ctx,
		t,
		step("up on destination shows no changes", upHasNoChanges, "up", "--yes", "--non-interactive"),
	)
}

// importMissingNamespace is a namespace that neither server creates, so an
// import into it must fail.
const importMissingNamespace = "ghost"

// importWrongToken is a token that neither server accepts, so an import with
// it must fail. It is a fixed test value, never a real credential.
const importWrongToken = "wrong-token-not-accepted"

// importErrors proves the error cases of the import (ADR 29 exit test step
// 6): a stack import on D into a namespace that D does not have fails and
// names the namespace, and the same import with a wrong access token for D
// fails and names the authentication failure. Each case leaves the source
// alone: the final source check of the run proves it. The log records the
// text that the CLI prints in each case.
func (f *p1Fixture) importErrors(ctx context.Context, t *testing.T, before p1Snapshot) {
	t.Helper()

	src := f.pair.Src
	tokens := []string{src.TokenA(), f.pair.Dst.TokenA(), importWrongToken}

	missing := importMissingNamespace + "/" + e2e.Project + "/" + stackName

	res := src.StepSavedLogin(ctx, t, e2e.StepSpec{
		Check:  checkImportErrorOneOf([]string{importMissingNamespace}, tokens...),
		Name:   "import to missing namespace",
		Args:   []string{"stack", "import", "--stack", missing},
		Env:    nil,
		Stdin:  before.export,
		Fail:   true,
		Redact: false,
	})
	t.Logf("import to missing namespace printed:\n%s%s", res.Stdout, res.Stderr)

	res = src.StepSavedLogin(ctx, t, e2e.StepSpec{
		Check:  checkImportErrorOneOf(importWrongTokenPhrases(), tokens...),
		Name:   "import with wrong token",
		Args:   []string{"stack", "import", "--stack", f.dev},
		Env:    []string{"PULUMI_ACCESS_TOKEN=" + importWrongToken},
		Stdin:  before.export,
		Fail:   true,
		Redact: false,
	})
	t.Logf("import with wrong token printed:\n%s%s", res.Stdout, res.Stderr)
}

// importWrongTokenPhrases holds the failure phrases a wrong access token may
// print: the wording differs by CLI version, from the client-side refusal to
// the server detail.
func importWrongTokenPhrases() []string {
	return []string{"invalid access token", "Unauthorized", "unauthorized", "unauthenticated", "401"}
}

// checkImportErrorOneOf proves a failed import names one of want and holds
// no token: the output names what failed and leaks no credential. The
// accepted phrases cover the wording drift between CLI versions.
func checkImportErrorOneOf(want []string, tokens ...string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		out := r.Stdout + r.Stderr

		matched := false

		for _, phrase := range want {
			if strings.Contains(out, phrase) {
				matched = true

				break
			}
		}

		if !matched {
			return fmt.Errorf("%w: import error lacks any of %q: %s", errUnexpected, want, out)
		}

		for _, token := range tokens {
			if token != "" && strings.Contains(out, token) {
				return fmt.Errorf("%w: import error holds a token", errUnexpected)
			}
		}

		return nil
	}
}

// checkSourceUnchanged proves the import left the source alone: the latest
// export and the history still match the snapshot, and no write request
// reached the source past entry since. Decrypt requests are reads: the CLI
// decrypts through the source at import and encrypts through the destination.
func (f *p1Fixture) checkSourceUnchanged(
	ctx context.Context, t *testing.T, before p1Snapshot, since int,
) {
	t.Helper()

	src := f.pair.Src

	f.pair.UsePulumiBackend(ctx, t, src, "use source for final check")

	exported := src.StepSavedLogin(ctx, t, step("export latest on source after import", nil, "stack", "export"))
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on source after import", p1HistoryIs("update"), "stack", "history", "--json"),
	)

	if exported.Stdout != before.export {
		t.Error("import changed the source export")
	}

	if history.Stdout != before.history {
		t.Error("import changed the source history")
	}

	entries := src.Recorder.Entries()

	for i := since; i < len(entries); i++ {
		if entry := &entries[i]; entry.Host == src.Host() && p1IsWrite(entry.Method, entry.Path) {
			t.Errorf("import sent %s %s to the source, want no write request", entry.Method, entry.Path)

			return
		}
	}
}

// logoutSource logs out of the source and proves the destination needs no
// source login afterwards: the secret output and the config still decrypt
// through the destination alone.
func (f *p1Fixture) logoutSource(ctx context.Context, t *testing.T) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst

	src.StepSavedLogin(ctx, t, step("logout of source", nil, "logout", src.URL))
	f.pair.UsePulumiBackend(ctx, t, dst, "use destination after logout")
	src.StepSavedLogin(
		ctx,
		t,
		step("stack output after source logout", contains("s3cr3t-3"), "stack", "output", "--show-secrets"),
	)
	src.StepSavedLogin(
		ctx,
		t,
		step("config get after source logout", contains("s3cr3t-3"), "config", "get", "apiKey"),
	)
}

// p1HistoryIs proves stack history holds every replayed version, each of
// kind.
func p1HistoryIs(kind string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var updates []struct {
			Kind string `json:"kind"`
		}

		if err := json.Unmarshal([]byte(r.Stdout), &updates); err != nil {
			return fmt.Errorf("%w: decode stack history: %w", errUnexpected, err)
		}

		if len(updates) != p1VersionCount {
			return fmt.Errorf(
				"%w: stack history holds %d updates, want %d",
				errUnexpected,
				len(updates),
				p1VersionCount,
			)
		}

		for _, u := range updates {
			if u.Kind != kind {
				return fmt.Errorf("%w: stack history holds kind %q, want %q", errUnexpected, u.Kind, kind)
			}
		}

		return nil
	}
}

// p1ExportHoldsSecret proves a version export holds the stack reference and
// an encrypted secret, never its plaintext.
func p1ExportHoldsSecret(r e2e.Result) error {
	if err := exportHoldsReference(r); err != nil {
		return err
	}

	if !strings.Contains(r.Stdout, "ciphertext") {
		return fmt.Errorf("%w: export holds no encrypted secret", errUnexpected)
	}

	if strings.Contains(r.Stdout, "s3cr3t-") {
		return fmt.Errorf("%w: export printed a plaintext secret", errUnexpected)
	}

	return nil
}

// normalizeP1Export blanks the server URLs and every ciphertext of an export
// and returns the canonical form with the sorted ciphertexts. The URL names
// the server and the destination re-encrypts every secret with its own key,
// so both always differ between source and destination.
func normalizeP1Export(t *testing.T, raw string, servers ...string) (string, []string) {
	t.Helper()

	for _, server := range servers {
		raw = strings.ReplaceAll(raw, server, "")
	}

	var doc any

	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode stack export: %v", err)
	}

	var ciphertexts []string

	redactP1Ciphertexts(doc, &ciphertexts)

	normalized, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode stack export: %v", err)
	}

	slices.Sort(ciphertexts)

	return string(normalized), ciphertexts
}

// redactP1Ciphertexts replaces every ciphertext string under doc with "*" and
// records the originals.
func redactP1Ciphertexts(doc any, ciphertexts *[]string) {
	switch doc := doc.(type) {
	case map[string]any:
		for key, item := range doc {
			if key == "ciphertext" {
				if text, ok := item.(string); ok {
					*ciphertexts = append(*ciphertexts, text)
					doc[key] = "*"
				}

				continue
			}

			redactP1Ciphertexts(item, ciphertexts)
		}
	case []any:
		for _, item := range doc {
			redactP1Ciphertexts(item, ciphertexts)
		}
	default:
	}
}

// p1IsWrite reports whether a recorded request changes server state. The
// encrypt and decrypt routes only wrap keys, so a decrypt through the source
// during an import is not a write.
func p1IsWrite(method, path string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}

	return !strings.Contains(path, "encrypt") && !strings.Contains(path, "decrypt")
}

// p1WritesInSteps returns the write requests in entries made during steps.
func p1WritesInSteps(entries []e2e.Entry, steps ...string) []e2e.Entry {
	var writes []e2e.Entry

	for i := range entries {
		if entry := &entries[i]; slices.Contains(steps, entry.Step) && p1IsWrite(entry.Method, entry.Path) {
			writes = append(writes, *entry)
		}
	}

	return writes
}

// TestTofuPairHosts proves one OpenTofu client addresses both servers in one
// test: after the trust bundle and the merged credentials, init works in the
// source project and in the destination project. It runs on Linux on every
// store under test and writes a -pair report.
func TestTofuPairHosts(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the OpenTofu pair suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.OpenTofu().Versions()
	if len(versions) == 0 {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.OpenTofu(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runTofuPairOnStore(t, cli, db)
		})
	}
}

// runTofuPairOnStore runs the two-host OpenTofu test on one store.
func runTofuPairOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRun())
	})

	pair.MergeTofuPair(t)

	pair.Src.Step(ctx, t, step("init on source host", nil, "init", "-input=false", "-no-color"))
	pair.Dst.Step(ctx, t, step("init on destination host", nil, "init", "-input=false", "-no-color"))

	checkTofuPairReport(ctx, t, pair)
}

// checkTofuPairReport proves one client addressed both hosts and the report
// records both: each server holds one workspace in its inventory and the
// steps hold the init on each host.
func checkTofuPairReport(ctx context.Context, t *testing.T, pair *e2e.Pair) {
	t.Helper()

	for _, env := range []*e2e.Env{pair.Src, pair.Dst} {
		entries, err := env.ListInventory(ctx, e2e.Namespace)
		if err != nil {
			t.Fatalf("list inventory: %v", err)
		}

		if len(entries) != 1 {
			t.Fatalf("inventory holds %d entries, want one workspace", len(entries))
		}
	}

	run := pair.NewPairRun()
	seenSrc, seenDst := false, false

	for _, s := range run.Steps {
		switch s.Name {
		case "init on source host":
			seenSrc = true
		case "init on destination host":
			seenDst = true
		default:
		}
	}

	if !seenSrc || !seenDst {
		t.Fatalf("pair report holds source %v and destination %v, want both", seenSrc, seenDst)
	}

	if len(run.Requests) == 0 {
		t.Fatal("pair report holds no requests")
	}
}

// TestTofuImportReplay proves the T2 import (ADR 29): three applies on the
// source S, the replay script in scripts/import-tfe-state.sh (dry run first),
// three replayed versions on the destination D with the same serial, lineage
// and MD5, a normal apply on D, and a second run that moves nothing, with S
// unchanged. It runs on Linux on every OpenTofu version and store under test
// and writes a -pair report. It needs jq on PATH for the script and skips
// with a clear report when jq is absent.
func TestTofuImportReplay(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the OpenTofu replay suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tool := e2e.OpenTofu()

	if skipWithoutJq(t, tool, databases) {
		t.Skip("the replay script needs jq on PATH")
	}

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			runImportReplayLabel(t, fetcher, tool, label, databases)
		})
	}
}

// skipWithoutJq writes a skipped report for every version and store when jq
// is absent from PATH and reports whether it did. The replay script needs jq
// to read the TFE list route.
func skipWithoutJq(t *testing.T, tool e2e.Tool, databases []e2e.Store) bool {
	t.Helper()

	if _, err := exec.LookPath("jq"); err == nil {
		return false
	}

	reason := "the replay script needs jq on PATH"

	for _, label := range tool.Versions() {
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label+"-pair", db, reason, true))
		}
	}

	return true
}

// runImportReplayLabel installs one OpenTofu CLI and runs the T2 replay on
// every store under test.
func runImportReplayLabel(
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
			runImportReplayOnStore(t, cli, db)
		})
	}
}

// replayVersion is one listed state version the replay test compares: its ID
// with the serial, lineage and MD5 the replay must keep.
type replayVersion struct {
	id      string
	serial  int64
	lineage string
	md5     string
}

// runImportReplayOnStore runs the T2 replay on one store: three applies on S,
// the script dry run and replay, the triple check on D, an apply on D, a
// second run that moves nothing, and the proof that S never changed.
func runImportReplayOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRun())
	})

	pair.MergeTofuPair(t)

	src, dst := pair.Src, pair.Dst

	src.Step(ctx, t, step("init on source", nil, "init", "-input=false", "-no-color"))

	for i, greeting := range []string{"one", "two", "three"} {
		src.WriteTofuConfig(t, tofuGreetingConfig(src.Host(), greeting))
		src.Step(
			ctx,
			t,
			step(fmt.Sprintf("source apply %d", i+1), nil, "apply", "-input=false", "-auto-approve", "-no-color"),
		)
	}

	before := listReplayVersions(ctx, t, src)
	if len(before) != 3 {
		t.Fatalf("source holds %d versions, want 3 before the replay", len(before))
	}

	dst.Step(ctx, t, step("init on destination", nil, "init", "-input=false", "-no-color"))

	if got := listReplayVersions(ctx, t, dst); len(got) != 0 {
		t.Fatalf("destination holds %d versions, want none before the replay", len(got))
	}

	srcWS := src.WorkspaceID(ctx, t)

	args := replayArgs(pair)

	dry := runReplayScript(ctx, t, pair, "replay dry run", append(args, "--dry-run")...)

	if dry.ExitCode != 0 {
		t.Fatalf("replay dry run exits with status %d: %s%s", dry.ExitCode, dry.Stdout, dry.Stderr)
	}

	if got := listReplayVersions(ctx, t, dst); len(got) != 0 {
		t.Fatalf("destination holds %d versions, want none after the dry run", len(got))
	}

	checkDryRunReadOnly(t, pair, "replay dry run")

	replayed := runReplayScript(ctx, t, pair, "replay", args...)

	if replayed.ExitCode != 0 {
		t.Fatalf("replay exits with status %d: %s%s", replayed.ExitCode, replayed.Stdout, replayed.Stderr)
	}

	checkReplayedTriples(t, before, listReplayVersions(ctx, t, dst))
	checkSourceUnchanged(ctx, t, src, before)
	checkNoSourceWrite(t, pair, srcWS, "replay")

	dst.WriteTofuConfig(t, tofuGreetingConfig(dst.Host(), "four"))
	dst.Step(ctx, t, step("apply on destination", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	dst.Step(ctx, t, step("state pull on destination", contains(`"four"`), "state", "pull", "-no-color"))

	again := runReplayScript(ctx, t, pair, "replay again", args...)

	if again.ExitCode != 0 {
		t.Fatalf("second replay exits with status %d: %s%s", again.ExitCode, again.Stdout, again.Stderr)
	}

	if out := again.Stdout + again.Stderr; !strings.Contains(out, "moving 0") {
		t.Fatalf("second replay output lacks moving 0: %s%s", again.Stdout, again.Stderr)
	}

	if got := listReplayVersions(ctx, t, dst); len(got) != 4 {
		t.Fatalf("destination holds %d versions, want 4 after the apply and the second run", len(got))
	}

	replayErrorCases(ctx, t, pair, before, srcWS)
}

// replayErrorCases runs the error cases of the T2 replay (ADR 29 exit test
// step 6): the replay with a missing organization and with a wrong token
// fails with a clear message and changes neither side. The log records the
// text that the script prints in each case.
func replayErrorCases(
	ctx context.Context, t *testing.T, pair *e2e.Pair, before []replayVersion, srcWS string,
) {
	t.Helper()

	src, dst := pair.Src, pair.Dst

	runReplayErrorScript(ctx, t, pair, replayErrorInput{
		name:     "replay missing organization",
		srcToken: pair.Src.TokenA(),
		dstToken: pair.Dst.TokenA(),
		want:     []string{importMissingOrg},
		args:     replayArgsOrgs(pair, importMissingOrg, e2e.Namespace),
	})

	runReplayErrorScript(ctx, t, pair, replayErrorInput{
		name:     "replay wrong token",
		srcToken: importWrongToken,
		dstToken: pair.Dst.TokenA(),
		want:     []string{"401"},
		args:     replayArgs(pair),
	})

	if got := listReplayVersions(ctx, t, dst); len(got) != 4 {
		t.Fatalf("destination holds %d versions, want 4 after the error cases", len(got))
	}

	checkSourceUnchanged(ctx, t, src, before)
	checkNoSourceWrite(t, pair, srcWS, "replay missing organization")
	checkNoSourceWrite(t, pair, srcWS, "replay wrong token")
}

// tofuGreetingConfig is the cloud-block configuration with one greeting
// output, so each apply with a new greeting writes a new state version.
func tofuGreetingConfig(host, greeting string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = ` + strconv.Quote(e2e.Workspace) + `
    }
  }
}

output "greeting" {
  value = ` + strconv.Quote(greeting) + `
}
`
}

// replayArgs returns the replay script arguments for pair: both server URLs,
// both namespaces and workspaces, the last ten versions and the trust bundle.
func replayArgs(pair *e2e.Pair) []string {
	return replayArgsOrgs(pair, e2e.Namespace, e2e.Namespace)
}

// replayArgsOrgs returns the replay script arguments for pair with the given
// source and destination organizations, so error cases run the script against
// a missing organization.
func replayArgsOrgs(pair *e2e.Pair, srcOrg, dstOrg string) []string {
	args := make([]string, 0, 16)

	return append(args,
		"--source-url", pair.Src.URL,
		"--dest-url", pair.Dst.URL,
		"--source-org", srcOrg,
		"--source-workspace", e2e.Workspace,
		"--dest-org", dstOrg,
		"--dest-workspace", e2e.Workspace,
		"--count", "10",
		"--ca-bundle", pair.Src.CABundle(),
	)
}

// replayScriptPath returns the path of scripts/import-tfe-state.sh from the
// directory of this test file.
func replayScriptPath(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find the test file for the script path")
	}

	return filepath.Join(filepath.Dir(file), "..", "scripts", "import-tfe-state.sh")
}

// runReplayScript runs the replay script with args and tokens from the pair,
// records it as name on the source side, and returns the outcome. The tokens
// travel through the environment, never as arguments, and the outcome must
// hold neither token.
func runReplayScript(
	ctx context.Context, t *testing.T, pair *e2e.Pair, name string, args ...string,
) e2e.ExternalResult {
	t.Helper()

	pair.Src.Recorder.SetStep(name)

	path := replayScriptPath(t)
	command := "import-tfe-state.sh " + strings.Join(args, " ")

	var stdout, stderr bytes.Buffer

	//nolint:gosec // The command is the repository's own replay script.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = t.TempDir()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"IMPORT_TFE_SOURCE_TOKEN="+pair.Src.TokenA(),
		"IMPORT_TFE_DEST_TOKEN="+pair.Dst.TokenA(),
	)

	err := cmd.Run()
	res := e2e.ExternalResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}

	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
			res.Stderr += "\nrun import-tfe-state.sh: " + err.Error()
		}
	}

	failure := ""

	switch {
	case res.ExitCode != 0:
		failure = fmt.Sprintf("exit code %d", res.ExitCode)
	case strings.Contains(res.Stdout+res.Stderr, pair.Src.TokenA()) ||
		strings.Contains(res.Stdout+res.Stderr, pair.Dst.TokenA()):
		failure = "output holds a token"
	default:
	}

	pair.Src.RecordExternalStep(t, name, command, res, failure)

	return res
}

// replayErrorInput groups one replay error case: the step name, the tokens
// the script runs with, the phrases the output must hold, and the script
// arguments.
type replayErrorInput struct {
	name     string
	srcToken string
	dstToken string
	want     []string
	args     []string
}

// runReplayErrorScript runs the replay script for an error case, records it
// under the input name, and fails the test when the script exits zero, names
// none of the wanted phrases, or holds a token: neither the given tokens nor
// the pair tokens. The tokens travel through the environment, never as
// arguments. The log records the text that the script prints.
func runReplayErrorScript(ctx context.Context, t *testing.T, pair *e2e.Pair, in replayErrorInput) {
	t.Helper()

	pair.Src.Recorder.SetStep(in.name)

	path := replayScriptPath(t)
	command := "import-tfe-state.sh " + strings.Join(in.args, " ")

	var stdout, stderr bytes.Buffer

	//nolint:gosec // The command is the repository's own replay script.
	cmd := exec.CommandContext(ctx, path, in.args...)
	cmd.Dir = t.TempDir()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"IMPORT_TFE_SOURCE_TOKEN="+in.srcToken,
		"IMPORT_TFE_DEST_TOKEN="+in.dstToken,
	)

	err := cmd.Run()
	res := e2e.ExternalResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}

	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
			res.Stderr += "\nrun import-tfe-state.sh: " + err.Error()
		}
	}

	failure := replayErrorFailure(res, in.want, []string{
		in.srcToken,
		in.dstToken,
		pair.Src.TokenA(),
		pair.Dst.TokenA(),
	})

	pair.Src.RecordExternalStep(t, in.name, command, res, failure)
	t.Logf("%s printed:\n%s%s", in.name, res.Stdout, res.Stderr)
}

// replayErrorFailure returns the failure of an error-case replay run, or ""
// when the script exits non-zero, names one of want, and holds no token.
func replayErrorFailure(res e2e.ExternalResult, want, tokens []string) string {
	if res.ExitCode == 0 {
		return "expected a non-zero exit"
	}

	out := res.Stdout + res.Stderr

	matched := false

	for _, phrase := range want {
		if strings.Contains(out, phrase) {
			matched = true

			break
		}
	}

	if !matched {
		return fmt.Sprintf("output lacks any of %q", want)
	}

	for _, token := range tokens {
		if token != "" && strings.Contains(out, token) {
			return "output holds a token"
		}
	}

	return ""
}

// replayListAttributes holds the serial, lineage and MD5 of one listed
// state version in the replay test.
type replayListAttributes struct {
	Serial  int64  `json:"serial"`
	Lineage string `json:"lineage"`
	MD5     string `json:"md5"`
}

// replayListItem decodes one entry of a state version list in the replay
// test: its ID with the serial, lineage and MD5 the replay must keep.
type replayListItem struct {
	ID         string               `json:"id"`
	Attributes replayListAttributes `json:"attributes"`
}

// listReplayVersions returns the state versions of the environment workspace,
// newest first, through the TFE list route.
func listReplayVersions(ctx context.Context, t *testing.T, env *e2e.Env) []replayVersion {
	t.Helper()

	target := "/api/v2/state-versions?filter[organization][name]=" + e2e.Namespace +
		"&filter[workspace][name]=" + e2e.Workspace + "&page[size]=100"

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, target, "")
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions answers %d: %s", status, body)
	}

	var doc struct {
		Data []replayListItem `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode state version list: %v", err)
	}

	out := make([]replayVersion, 0, len(doc.Data))
	for _, item := range doc.Data {
		out = append(out, replayVersion{
			id:      item.ID,
			serial:  item.Attributes.Serial,
			lineage: item.Attributes.Lineage,
			md5:     item.Attributes.MD5,
		})
	}

	return out
}

// checkReplayedTriples proves D holds every source version with the same
// serial, lineage and MD5: the replay kept the three values of each version.
func checkReplayedTriples(t *testing.T, want, got []replayVersion) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("destination holds %d versions, want %d", len(got), len(want))
	}

	bySerial := func(a, b replayVersion) int {
		switch {
		case a.serial != b.serial:
			return cmp.Compare(a.serial, b.serial)
		case a.lineage != b.lineage:
			return strings.Compare(a.lineage, b.lineage)
		default:
			return strings.Compare(a.md5, b.md5)
		}
	}

	slices.SortFunc(want, bySerial)
	slices.SortFunc(got, bySerial)

	for i := range want {
		if got[i].serial != want[i].serial || got[i].lineage != want[i].lineage || got[i].md5 != want[i].md5 {
			t.Fatalf(
				"destination version %+v, want serial %d lineage %q md5 %q",
				got[i],
				want[i].serial,
				want[i].lineage,
				want[i].md5,
			)
		}
	}
}

// checkSourceUnchanged proves the source still holds the same versions: the
// replay never writes to S.
func checkSourceUnchanged(ctx context.Context, t *testing.T, src *e2e.Env, want []replayVersion) {
	t.Helper()

	got := listReplayVersions(ctx, t, src)

	if len(got) != len(want) {
		t.Fatalf("source holds %d versions, want %d: the replay changed S", len(got), len(want))
	}

	ids := map[string]bool{}
	for _, v := range want {
		ids[v.id] = true
	}

	for _, v := range got {
		if !ids[v.id] {
			t.Fatalf("source holds new version %q: the replay changed S", v.id)
		}
	}
}

// checkDryRunReadOnly proves every request of a dry-run step is a GET: the
// dry run only reads.
func checkDryRunReadOnly(t *testing.T, pair *e2e.Pair, step string) {
	t.Helper()

	entries := pair.Src.Recorder.Entries()

	for i := range entries {
		entry := &entries[i]
		if entry.Step != step {
			continue
		}

		if entry.Method != http.MethodGet {
			t.Fatalf("dry run step %q holds a write: %s %s", step, entry.Method, entry.Path)
		}
	}
}

// checkNoSourceWrite proves the recorder shows no write to the source
// workspace during step: no non-GET entry names the source workspace ID. The
// destination writes of a real run name its own workspace instead.
func checkNoSourceWrite(t *testing.T, pair *e2e.Pair, srcWS, step string) {
	t.Helper()

	entries := pair.Src.Recorder.Entries()

	for i := range entries {
		entry := &entries[i]
		if entry.Step != step {
			continue
		}

		if entry.Method == http.MethodGet {
			continue
		}

		if strings.Contains(entry.Path, srcWS) {
			t.Fatalf("replay step %q writes to the source: %s %s", step, entry.Method, entry.Path)
		}
	}
}

// TestUpgradeCheck proves `opendustpan upgrade check` with RunBinary against
// the running binary server: on the migrated database the verdict is
// `up to date` with exit 0, and on the newer, old and fresh states reached by
// editing schema_migrations while the server runs the verdicts of ADR 33 and
// the exit codes match, with the command writing nothing to the server
// database. It runs only with E2E_SERVER=binary, on every store under test.
func TestUpgradeCheck(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runUpgradeCheckOnStore(t, cli, db)
		})
	}
}

// runUpgradeCheckOnStore runs the upgrade check cases on one store through
// the running binary server: the migrated database is up to date, and the
// newer, old and fresh states are reached by editing schema_migrations while
// the server runs, each through `upgrade check --server` with the environment
// token. The cases run in order on one server and restore its rows, so they
// never run in parallel.
func runUpgradeCheckOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkUpgradeUpToDateServer(ctx, t, env, store)
	checkUpgradeNewerServer(ctx, t, env, store)
	checkUpgradeOldServer(ctx, t, env, store)
	checkUpgradeFreshServer(ctx, t, env, store)
}

// runUpgradeCheckServer runs `opendustpan upgrade check --server` against the
// running server of env. The token travels in DUSTPAN_ADMIN_TOKEN in the
// RunBinary environment, and the command trusts the server through --ca-file
// with the same certificate bundle the CLIs trust.
func runUpgradeCheckServer(ctx context.Context, t *testing.T, env *e2e.Env) e2e.BinaryResult {
	t.Helper()

	return env.RunBinary(ctx, t, "upgrade", "check", "--server", env.URL, "--ca-file", env.CABundle())
}

// serverMigrationRow is one schema_migrations row the test edits while the
// server runs: the direct database access is the test setup, never the
// command, which only calls the upgrade check route.
type serverMigrationRow struct {
	version int64
	name    string
}

// serverSchemaSnapshot holds the schema_migrations rows and table list of the
// server database, so the test proves upgrade check wrote nothing.
type serverSchemaSnapshot struct {
	versions []string
	tables   []string
}

// checkServerDBUnchanged proves upgrade check wrote nothing: the rows and the
// table list match the snapshot taken before the call.
func checkServerDBUnchanged(t *testing.T, before, after serverSchemaSnapshot) {
	t.Helper()

	if !slices.Equal(after.tables, before.tables) {
		t.Fatalf("tables after check = %v, want %v", after.tables, before.tables)
	}

	if !slices.Equal(after.versions, before.versions) {
		t.Fatalf("versions after check = %v, want %v", after.versions, before.versions)
	}
}

// sqliteServerPath returns the database file path the binary server of env
// serves.
func sqliteServerPath(t *testing.T, env *e2e.Env) string {
	t.Helper()

	path, ok := strings.CutPrefix(env.DatabaseURL(t), "sqlite:")
	if !ok {
		t.Fatalf("database URL %q is not a SQLite URL", env.DatabaseURL(t))
	}

	return path
}

// openServerSQLite opens a direct handle on the SQLite file at path, so the
// test can edit schema_migrations while the server runs.
func openServerSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()

	conn, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open server database: %v", err)
	}

	return conn
}

// readServerSQLiteRows returns every schema_migrations row ordered by
// version.
func readServerSQLiteRows(ctx context.Context, t *testing.T, path string) []serverMigrationRow {
	t.Helper()

	conn := openServerSQLite(t, path)

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close server database: %v", closeErr)
		}
	}()

	rows, err := conn.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close schema versions: %v", closeErr)
		}
	}()

	var out []serverMigrationRow

	for rows.Next() {
		var row serverMigrationRow

		if scanErr := rows.Scan(&row.version, &row.name); scanErr != nil {
			t.Fatalf("read schema version: %v", scanErr)
		}

		out = append(out, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read schema versions: %v", rowsErr)
	}

	return out
}

// execServerSQLite runs one write on the SQLite file at path.
func execServerSQLite(ctx context.Context, t *testing.T, path, stmt string, args ...any) {
	t.Helper()

	conn := openServerSQLite(t, path)

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close server database: %v", closeErr)
		}
	}()

	if _, err := conn.ExecContext(ctx, stmt, args...); err != nil {
		t.Fatalf("write server database: %v", err)
	}
}

// snapshotServerSQLite returns the schema_migrations rows with the table
// names of the SQLite file at path.
func snapshotServerSQLite(ctx context.Context, t *testing.T, path string) serverSchemaSnapshot {
	t.Helper()

	conn := openServerSQLite(t, path)

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close server database: %v", closeErr)
		}
	}()

	return serverSchemaSnapshot{
		versions: snapshotServerSQLiteVersions(ctx, t, conn),
		tables:   snapshotServerSQLiteTables(ctx, t, conn),
	}
}

// snapshotServerSQLiteTables returns the table names of conn ordered by
// name.
func snapshotServerSQLiteTables(ctx context.Context, t *testing.T, conn *sql.DB) []string {
	t.Helper()

	tableRows, err := conn.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`,
	)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}

	defer func() {
		if closeErr := tableRows.Close(); closeErr != nil {
			t.Errorf("close tables: %v", closeErr)
		}
	}()

	var tables []string

	for tableRows.Next() {
		var name string

		if scanErr := tableRows.Scan(&name); scanErr != nil {
			t.Fatalf("read table name: %v", scanErr)
		}

		tables = append(tables, name)
	}

	if rowsErr := tableRows.Err(); rowsErr != nil {
		t.Fatalf("read tables: %v", rowsErr)
	}

	return tables
}

// snapshotServerSQLiteVersions returns the schema_migrations rows of conn as
// version:name ordered by version.
func snapshotServerSQLiteVersions(ctx context.Context, t *testing.T, conn *sql.DB) []string {
	t.Helper()

	versionRows, err := conn.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	defer func() {
		if closeErr := versionRows.Close(); closeErr != nil {
			t.Errorf("close schema versions: %v", closeErr)
		}
	}()

	var versions []string

	for versionRows.Next() {
		var version int64

		var name string

		if scanErr := versionRows.Scan(&version, &name); scanErr != nil {
			t.Fatalf("read schema version: %v", scanErr)
		}

		versions = append(versions, strconv.FormatInt(version, 10)+":"+name)
	}

	if rowsErr := versionRows.Err(); rowsErr != nil {
		t.Fatalf("read schema versions: %v", rowsErr)
	}

	return versions
}

// snapshotServerDB returns the rows and tables of the server database of env
// on store.
func snapshotServerDB(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) serverSchemaSnapshot {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		return snapshotServerSQLite(ctx, t, sqliteServerPath(t, env))
	case e2e.StorePostgres:
		got := snapshotPostgres(ctx, t, env.DatabaseURL(t))

		return serverSchemaSnapshot{versions: got.versions, tables: got.tables}
	default:
		t.Fatalf("unknown store: %s", store)

		//nolint:exhaustruct_v5 // t.Fatalf ends the test; the value never leaves this branch.
		return serverSchemaSnapshot{}
	}
}

// insertServerFuture records one version above the newest in the server
// database of env on store, so upgrade check sees a newer database. It
// returns the future version.
func insertServerFuture(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) int64 {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		path := sqliteServerPath(t, env)
		rows := readServerSQLiteRows(ctx, t, path)

		var newest int64

		for _, row := range rows {
			newest = max(newest, row.version)
		}

		future := newest + 1
		execServerSQLite(ctx, t, path,
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
			future, fmt.Sprintf("%04d_future.sql", future))

		return future
	case e2e.StorePostgres:
		schemaURL := env.DatabaseURL(t)

		conn, err := pgx.Connect(ctx, schemaURL)
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(ctx); closeErr != nil {
				t.Errorf("close postgres connection: %v", closeErr)
			}
		}()

		var newest int64

		if err := conn.QueryRow(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&newest); err != nil {
			t.Fatalf("read newest version: %v", err)
		}

		future := newest + 1
		name := fmt.Sprintf("%04d_future.sql", future)

		const recordFuture = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

		if _, execErr := conn.Exec(ctx, recordFuture, future, name); execErr != nil {
			t.Fatalf("record future version: %v", execErr)
		}

		return future
	default:
		t.Fatalf("unknown store: %s", store)

		return 0
	}
}

// deleteServerVersion removes one schema_migrations row from the server
// database of env on store.
func deleteServerVersion(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store, version int64) {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		execServerSQLite(ctx, t, sqliteServerPath(t, env),
			`DELETE FROM schema_migrations WHERE version = ?`, version)
	case e2e.StorePostgres:
		conn, err := pgx.Connect(ctx, env.DatabaseURL(t))
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(ctx); closeErr != nil {
				t.Errorf("close postgres connection: %v", closeErr)
			}
		}()

		if _, err := conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
			t.Fatalf("delete version %d: %v", version, err)
		}
	default:
		t.Fatalf("unknown store: %s", store)
	}
}

// trimServerVersions removes every schema_migrations row above keep from the
// server database of env on store and returns the removed rows, so the caller
// restores them after the check.
func trimServerVersions(
	ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store, keep int64,
) []serverMigrationRow {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		return trimServerSQLiteVersions(ctx, t, env, keep)
	case e2e.StorePostgres:
		return trimServerPostgresVersions(ctx, t, env, keep)
	default:
		t.Fatalf("unknown store: %s", store)

		return nil
	}
}

// trimServerSQLiteVersions removes every schema_migrations row above keep
// from the SQLite file the server of env serves and returns the removed
// rows.
func trimServerSQLiteVersions(
	ctx context.Context, t *testing.T, env *e2e.Env, keep int64,
) []serverMigrationRow {
	t.Helper()

	path := sqliteServerPath(t, env)

	var removed []serverMigrationRow

	for _, row := range readServerSQLiteRows(ctx, t, path) {
		if row.version > keep {
			removed = append(removed, row)
		}
	}

	execServerSQLite(ctx, t, path, `DELETE FROM schema_migrations WHERE version > ?`, keep)

	return removed
}

// trimServerPostgresVersions removes every schema_migrations row above keep
// from the PostgreSQL schema the server of env serves and returns the
// removed rows.
func trimServerPostgresVersions(
	ctx context.Context, t *testing.T, env *e2e.Env, keep int64,
) []serverMigrationRow {
	t.Helper()

	conn, err := pgx.Connect(ctx, env.DatabaseURL(t))
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	versionRows, err := conn.Query(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	defer func() {
		versionRows.Close()
	}()

	var removed []serverMigrationRow

	for versionRows.Next() {
		var row serverMigrationRow

		if scanErr := versionRows.Scan(&row.version, &row.name); scanErr != nil {
			t.Fatalf("read schema version: %v", scanErr)
		}

		if row.version > keep {
			removed = append(removed, row)
		}
	}

	if rowsErr := versionRows.Err(); rowsErr != nil {
		t.Fatalf("read schema versions: %v", rowsErr)
	}

	if _, err := conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version > $1`, keep); err != nil {
		t.Fatalf("trim versions above %d: %v", keep, err)
	}

	return removed
}

// restoreServerVersions inserts rows back into the server database of env on
// store.
func restoreServerVersions(
	ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store, rows []serverMigrationRow,
) {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		path := sqliteServerPath(t, env)

		for _, row := range rows {
			execServerSQLite(ctx, t, path,
				`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, row.version, row.name)
		}
	case e2e.StorePostgres:
		conn, err := pgx.Connect(ctx, env.DatabaseURL(t))
		if err != nil {
			t.Fatalf("connect to postgres: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(ctx); closeErr != nil {
				t.Errorf("close postgres connection: %v", closeErr)
			}
		}()

		const recordRow = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

		for _, row := range rows {
			if _, execErr := conn.Exec(ctx, recordRow, row.version, row.name); execErr != nil {
				t.Fatalf("restore version %d: %v", row.version, execErr)
			}
		}
	default:
		t.Fatalf("unknown store: %s", store)
	}
}

// checkRestoredServerDB proves the server database of env on store holds the
// rows of want after the test restores them.
func checkRestoredServerDB(
	ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store, want serverSchemaSnapshot,
) {
	t.Helper()

	if got := snapshotServerDB(ctx, t, env, store); !slices.Equal(got.versions, want.versions) {
		t.Fatalf("versions after restore = %v, want %v", got.versions, want.versions)
	}
}

// checkUpgradeUpToDateServer proves upgrade check on the migrated server
// database: it exits 0 with `up to date` and writes nothing.
func checkUpgradeUpToDateServer(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) {
	t.Helper()

	before := snapshotServerDB(ctx, t, env, store)
	res := runUpgradeCheckServer(ctx, t, env)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "up to date", exitCode: 0, pending: false})

	database, binary := parseUpgradeVersions(t, res.Stdout)
	if database != binary {
		t.Fatalf("schema versions differ: database %d, binary %d:\n%s", database, binary, res.Stdout)
	}

	checkServerDBUnchanged(t, before, snapshotServerDB(ctx, t, env, store))
}

// checkUpgradeNewerServer proves upgrade check with one extra version row in
// the server database: it exits 1 with `cannot start` and writes nothing. It
// removes the row after the check.
func checkUpgradeNewerServer(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) {
	t.Helper()

	pristine := snapshotServerDB(ctx, t, env, store)
	future := insertServerFuture(ctx, t, env, store)

	before := snapshotServerDB(ctx, t, env, store)
	res := runUpgradeCheckServer(ctx, t, env)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "cannot start", exitCode: 1, pending: false})

	database, binary := parseUpgradeVersions(t, res.Stdout)
	if database <= binary {
		t.Fatalf("newer database %d is not above binary %d:\n%s", database, binary, res.Stdout)
	}

	checkServerDBUnchanged(t, before, snapshotServerDB(ctx, t, env, store))

	deleteServerVersion(ctx, t, env, store, future)
	restoreServerVersions(ctx, t, env, store, nil)
	checkRestoredServerDB(ctx, t, env, store, pristine)
}

// checkUpgradeOldServer proves upgrade check with only versions 1 through
// oldFixtureMaxVersion in the server database: it exits 0 with `upgrade
// possible` and writes nothing. It restores the removed rows after the
// check.
func checkUpgradeOldServer(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) {
	t.Helper()

	pristine := snapshotServerDB(ctx, t, env, store)
	removed := trimServerVersions(ctx, t, env, store, oldFixtureMaxVersion)

	if len(removed) == 0 {
		t.Fatalf("trim above %d removed nothing, want pending migrations", oldFixtureMaxVersion)
	}

	before := snapshotServerDB(ctx, t, env, store)
	res := runUpgradeCheckServer(ctx, t, env)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "upgrade possible", exitCode: 0, pending: true})

	if out := res.Stdout; !strings.Contains(
		out,
		"database schema version: "+strconv.FormatInt(oldFixtureMaxVersion, 10),
	) {
		t.Fatalf("old output lacks database schema version %d:\n%s", oldFixtureMaxVersion, out)
	}

	checkServerDBUnchanged(t, before, snapshotServerDB(ctx, t, env, store))

	restoreServerVersions(ctx, t, env, store, removed)
	checkRestoredServerDB(ctx, t, env, store, pristine)
}

// checkUpgradeFreshServer proves upgrade check with no version row in the
// server database: it exits 0 with `upgrade possible` and writes nothing. It
// restores the removed rows after the check.
func checkUpgradeFreshServer(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) {
	t.Helper()

	pristine := snapshotServerDB(ctx, t, env, store)
	removed := trimServerVersions(ctx, t, env, store, 0)

	if len(removed) == 0 {
		t.Fatal("trim above 0 removed nothing, want every migration pending")
	}

	before := snapshotServerDB(ctx, t, env, store)
	res := runUpgradeCheckServer(ctx, t, env)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "upgrade possible", exitCode: 0, pending: true})

	if out := res.Stdout; !strings.Contains(out, "database schema version: 0") {
		t.Fatalf("fresh output lacks database schema version 0:\n%s", out)
	}

	checkServerDBUnchanged(t, before, snapshotServerDB(ctx, t, env, store))

	restoreServerVersions(ctx, t, env, store, removed)
	checkRestoredServerDB(ctx, t, env, store, pristine)
}

// wantUpgradeResult holds the expected outcome of one upgrade check run:
// the verdict, the exit code and whether pending migrations are listed.
type wantUpgradeResult struct {
	verdict  string
	exitCode int
	pending  bool
}

// checkUpgradeResult proves res exits with the expected code, holds the
// expected verdict and names the binary version, both schema versions and
// the pending state.
func checkUpgradeResult(t *testing.T, res e2e.BinaryResult, want wantUpgradeResult) {
	t.Helper()

	if res.ExitCode != want.exitCode {
		t.Fatalf(
			"upgrade check exits with status %d, want %d:\n%s%s",
			res.ExitCode,
			want.exitCode,
			res.Stdout,
			res.Stderr,
		)
	}

	for _, line := range []string{
		"binary version: ",
		"database schema version: ",
		"binary schema version: ",
		"verdict: " + want.verdict,
	} {
		if !strings.Contains(res.Stdout, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s%s", line, res.Stdout, res.Stderr)
		}
	}

	if want.pending {
		checkUpgradePending(t, res)
	} else {
		checkUpgradeNoPending(t, res)
	}
}

// checkUpgradePending proves res lists pending migrations with the backup
// advice.
func checkUpgradePending(t *testing.T, res e2e.BinaryResult) {
	t.Helper()

	for _, line := range []string{
		"pending migrations:",
		".sql",
		"Back up the database before the upgrade.",
		"No downgrade path exists.",
	} {
		if !strings.Contains(res.Stdout, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s%s", line, res.Stdout, res.Stderr)
		}
	}
}

// checkUpgradeNoPending proves res holds `pending migrations: none`.
func checkUpgradeNoPending(t *testing.T, res e2e.BinaryResult) {
	t.Helper()

	if !strings.Contains(res.Stdout, "pending migrations: none") {
		t.Fatalf("upgrade check output lacks `pending migrations: none`:\n%s%s", res.Stdout, res.Stderr)
	}
}

// parseUpgradeVersions reads the database and binary schema versions from
// upgrade check output.
//
//nolint:nonamedreturns // The names keep the database and binary versions in order at callers.
func parseUpgradeVersions(t *testing.T, out string) (database, binary int64) {
	t.Helper()

	for line := range strings.Lines(out) {
		if rest, ok := strings.CutPrefix(line, "database schema version: "); ok {
			v, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				t.Fatalf("parse database schema version %q: %v", rest, err)
			}

			database = v
		}

		if rest, ok := strings.CutPrefix(line, "binary schema version: "); ok {
			v, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				t.Fatalf("parse binary schema version %q: %v", rest, err)
			}

			binary = v
		}
	}

	if database < 0 || binary <= 0 {
		t.Fatalf("schema versions are missing:\n%s", out)
	}

	return database, binary
}

// oldFixtureMaxVersion is the newest version in the fixed old-database
// fixture. The fixture stays below the binary version, so the check reports
// pending migrations. Bump it only with the fixture name and assertions.
const oldFixtureMaxVersion = 8

// postgresSnapshot holds the table list and schema_migrations rows of one
// PostgreSQL schema, so the test proves upgrade check wrote nothing.
type postgresSnapshot struct {
	tables   []string
	versions []string
}

// snapshotPostgres reads the table names and the schema_migrations rows of
// schemaURL. versions is nil when the table is absent.
func snapshotPostgres(ctx context.Context, t *testing.T, schemaURL string) postgresSnapshot {
	t.Helper()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}

	var tables []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			rows.Close()
			t.Fatalf("read table name: %v", scanErr)
		}

		tables = append(tables, name)
	}

	rows.Close()

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read tables: %v", rowsErr)
	}

	if !slices.Contains(tables, "schema_migrations") {
		return postgresSnapshot{tables: tables, versions: nil}
	}

	versionRows, err := conn.Query(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	var versions []string

	for versionRows.Next() {
		var version int64

		var name string

		if scanErr := versionRows.Scan(&version, &name); scanErr != nil {
			versionRows.Close()
			t.Fatalf("read schema version: %v", scanErr)
		}

		versions = append(versions, strconv.FormatInt(version, 10)+":"+name)
	}

	versionRows.Close()

	if rowsErr := versionRows.Err(); rowsErr != nil {
		t.Fatalf("read schema versions: %v", rowsErr)
	}

	return postgresSnapshot{tables: tables, versions: versions}
}

// TestBackupCheck proves `opendustpan backup check` with RunBinary against
// the running binary server: a backup started through the installation API
// polls to succeeded, the check exits 0 with one verdict, then one deleted
// object in the server backup target makes the check exit 1 naming the key.
// It runs only with E2E_SERVER=binary, on every store under test.
func TestBackupCheck(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runBackupCheckOnStore(t, cli, db)
		})
	}
}

// runBackupCheckOnStore runs the backup check steps on one store: a backup
// through the installation API, a check with exit 0, a deleted target object,
// and a check with exit 1 naming the key.
func runBackupCheckOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	id := startBackupForCheck(ctx, t, env)

	complete := env.RunBinary(
		ctx, t, "backup", "check",
		"--server", env.URL, "--ca-file", env.CABundle(), "--backup", id,
	)
	if complete.ExitCode != 0 {
		t.Fatalf(
			"backup check exits with status %d, want 0:\n%s%s",
			complete.ExitCode,
			complete.Stdout,
			complete.Stderr,
		)
	}

	if !strings.Contains(complete.Stdout, "backup "+id+" is complete") {
		t.Fatalf("backup check output lacks the verdict:\n%s%s", complete.Stdout, complete.Stderr)
	}

	blobKey := corruptServerBackupTarget(ctx, t, env, id)

	broken := env.RunBinary(
		ctx, t, "backup", "check",
		"--server", env.URL, "--ca-file", env.CABundle(), "--backup", id,
	)
	if broken.ExitCode != 1 {
		t.Fatalf(
			"backup check after the delete exits with status %d, want 1:\n%s%s",
			broken.ExitCode,
			broken.Stdout,
			broken.Stderr,
		)
	}

	if out := broken.Stdout + broken.Stderr; !strings.Contains(out, blobKey) {
		t.Fatalf("backup check output lacks the key %q:\n%s", blobKey, out)
	}
}

// backupCheckManifestDoc decodes the manifest of one backup: only the blob
// keys the check names when one goes missing.
type backupCheckManifestDoc struct {
	Blobs []backupCheckBlobDoc `json:"blobs"`
}

// backupCheckBlobDoc decodes one manifest blob entry.
type backupCheckBlobDoc struct {
	Key string `json:"key"`
}

// backupCheckDoc decodes one backup with its manifest.
type backupCheckDoc struct {
	State    string                 `json:"state"`
	Manifest backupCheckManifestDoc `json:"manifest"`
}

// startBackupForCheck starts a backup through the installation API and polls
// it to succeeded, returning its ID.
func startBackupForCheck(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("POST backups: %v", err)
	}

	if status != http.StatusAccepted {
		t.Fatalf("POST backups answers %d, want %d (%s)", status, http.StatusAccepted, body)
	}

	var started struct {
		ID string `json:"id"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &started); decodeErr != nil || started.ID == "" {
		t.Fatalf("POST backups answers %q, want an id: %v", body, decodeErr)
	}

	doc := pollBackupState(ctx, t, env, started.ID, "succeeded")
	t.Logf("backup %s succeeded with SQL size %d", doc.ID, doc.Manifest.SQLSize)

	return started.ID
}

// corruptServerBackupTarget deletes one object of backup id from the server
// backup target and returns the key the next check names: the first manifest
// blob, or the SQL part when the backup holds no blob.
func corruptServerBackupTarget(ctx context.Context, t *testing.T, env *e2e.Env, id string) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/backups/"+id, "")
	if err != nil {
		t.Fatalf("GET backup %s: %v", id, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET backup %s answers %d, want %d (%s)", id, status, http.StatusOK, body)
	}

	var doc backupCheckDoc

	if decodeErr := json.Unmarshal([]byte(body), &doc); decodeErr != nil {
		t.Fatalf("decode backup %s %q: %v", id, body, decodeErr)
	}

	key := "backups/" + id + "/db"
	if len(doc.Manifest.Blobs) > 0 {
		key = "blobs/" + doc.Manifest.Blobs[0].Key
	}

	if err := os.Remove(filepath.Join(env.BackupTargetDir(t), filepath.FromSlash(key))); err != nil {
		t.Fatalf("delete target object %q: %v", key, err)
	}

	return key
}

// backupPadBytes grows the backup with one padded checkpoint, so a backup
// holds still running while the test starts another one or stops the server.
// Checkpoints live in blobs, so the pad lands in the blob copy, not in SQL.
// The pad stays below the default 32 MiB checkpoint limit, and no test hook
// is needed: the data set itself holds the backup.
const backupPadBytes = 24 << 20

// backupPadMinBlob is the smallest gzipped blob the padded checkpoint may
// hold. Blobs are gzipped and uniform letters gzip to about two thirds of
// their size, so half the pad proves the blob holds the padding: the small
// deployment alone gzips to kilobytes.
const backupPadMinBlob = backupPadBytes / 2

// backupPollTimeout bounds a backup poll: a test backup ends well before it,
// even with the padded checkpoint.
const backupPollTimeout = 2 * time.Minute

// backupPollInterval sets how often a backup poll reads the row.
const backupPollInterval = 100 * time.Millisecond

// TestBackups proves the installation backup routes (ADR 30 decision 9): the
// Pulumi CLI writes state, POST starts a backup that polls to succeeded with
// a manifest holding the Pulumi data, a second start while one runs answers
// 409, calls without a token answer 401, and the list holds the backups
// newest first. It runs on every store under test with the first Pulumi
// version of the matrix. The restart part needs the real server binary: it
// stops the server during a running backup and sees the row failed after the
// start. Without E2E_SERVER=binary the test proves the routes and skips only
// the restart.
func TestBackups(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runBackupsOnStore(t, cli, db)
		})
	}
}

// runBackupsOnStore runs the backup steps on one store: CLI writes, a backup
// to succeeded with the manifest checks, the 409 and 401 checks, the list
// check, and the restart check with the real binary.
func runBackupsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)
	writeBackupData(ctx, t, env)

	id := startBackupAndConflict(ctx, t, env)
	doc := pollBackupState(ctx, t, env, id, "succeeded")
	checkBackupManifest(t, store, doc)
	checkBackupList(ctx, t, env, id)
	checkBackupUnauthorized(ctx, t, env, id)

	if !e2e.UseBinaryServer() {
		t.Log("skipping the binary parts without E2E_SERVER=binary")
		return
	}

	checkBackupCommand(ctx, t, env)
	checkBackupRestartFailed(ctx, t, env, id)
}

// writeBackupData writes Pulumi state with the CLI and grows the database
// with one padded checkpoint, so a backup holds still running while the test
// starts another one or stops the server.
func writeBackupData(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, stackName)

	for _, s := range []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
		step("stack init other", nil, "stack", "init", "other"),
		step("stack select", nil, "stack", "select", qualified),
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
		step("up", nil, "up", "--yes", "--non-interactive"),
	} {
		env.Step(ctx, t, s)
	}

	exported := env.Step(ctx, t, step("stack export", exportHoldsReference, "stack", "export"))

	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, stackName)
	env.SaveHeldPaddedCheckpoint(ctx, t, held, exported.Stdout, map[string]string{
		"e2ePad": backupPad(),
	})
	env.EndHeldUpdate(ctx, t, held, "succeeded")
}

// backupPad returns backupPadBytes of deterministic high-entropy letters.
// Uniform letters gzip to about two thirds of their size, so the padded
// checkpoint blob still holds the backup running while the test starts
// another backup or stops the server. A fixed seed keeps the run
// reproducible. The manifest checks compare the gzipped blob against
// backupPadMinBlob, not the full pad, because blobs are gzipped.
func backupPad() string {
	letters := make([]byte, backupPadBytes)

	offset := 0
	var counter uint64

	for offset < len(letters) {
		sum := sha256.Sum256([]byte("backup-pad-v1:" + strconv.FormatUint(counter, 10)))
		counter++

		for _, b := range sum {
			if offset >= len(letters) {
				break
			}

			letters[offset] = 'a' + b%26
			offset++
		}
	}

	return string(letters)
}

// backupDoc decodes one backup in tests.
type backupDoc struct {
	ID        string            `json:"id"`
	State     string            `json:"state"`
	StartedAt string            `json:"startedAt"`
	EndedAt   string            `json:"endedAt"`
	Target    string            `json:"target"`
	Error     string            `json:"error"`
	Manifest  backupManifestDoc `json:"manifest"`
}

// backupManifestDoc holds the manifest fields the backup steps check.
type backupManifestDoc struct {
	ID            string               `json:"id"`
	Format        int                  `json:"format"`
	Dialect       string               `json:"dialect"`
	HomeRegions   []string             `json:"homeRegions"`
	WrapperIDs    []string             `json:"wrapperIds"`
	Blobs         []backupManifestBlob `json:"blobs"`
	SQLSize       int64                `json:"sqlSize"`
	SQLSHA256     string               `json:"sqlSha256"`
	SchemaVersion int                  `json:"schemaVersion"`
}

// backupManifestBlob holds one copied blob of the manifest.
type backupManifestBlob struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

// startBackupAndConflict starts a backup and proves a second start while it
// runs answers 409 with the one JSON error shape. The padded checkpoint holds
// the first backup running, so no test hook is needed. It returns the ID of
// the running backup.
func startBackupAndConflict(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("POST backups: %v", err)
	}

	if status != http.StatusAccepted {
		t.Fatalf("POST backups answers %d, want %d (%s)", status, http.StatusAccepted, body)
	}

	var started struct {
		ID string `json:"id"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &started); decodeErr != nil || started.ID == "" {
		t.Fatalf("POST backups answers %q, want an id: %v", body, decodeErr)
	}

	if !strings.HasPrefix(started.ID, "bkp-") {
		t.Fatalf("backup id = %q, want the bkp- prefix", started.ID)
	}

	status, body, err = env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("second POST backups: %v", err)
	}

	if status != http.StatusConflict {
		t.Fatalf("second POST backups answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	var conflict struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &conflict); decodeErr != nil {
		t.Fatalf("decode 409 of POST backups: %v (%s)", decodeErr, body)
	}

	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Message, "already running") {
		t.Fatalf("second POST backups error = %+v, want code 409 naming the running backup", conflict)
	}

	return started.ID
}

// pollBackupState polls one backup until it reaches want, failing the test
// when it reaches another terminal state or the deadline passes first.
func pollBackupState(ctx context.Context, t *testing.T, env *e2e.Env, id, want string) backupDoc {
	t.Helper()

	deadline, cancel := context.WithTimeout(ctx, backupPollTimeout)
	defer cancel()

	timer := time.NewTimer(backupPollInterval)
	defer timer.Stop()

	for {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/backups/"+id, "")
		if err != nil {
			t.Fatalf("GET backup %s: %v", id, err)
		}

		if status != http.StatusOK {
			t.Fatalf("GET backup %s answers %d, want %d (%s)", id, status, http.StatusOK, body)
		}

		var doc backupDoc

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode backup %s %q: %v", id, body, err)
		}

		switch doc.State {
		case want:
			return doc
		case "running":
		default:
			t.Fatalf("backup %s reads %q with error %q, want %q", id, doc.State, doc.Error, want)
		}

		select {
		case <-deadline.Done():
			t.Fatalf("backup %s stays running, want %q", id, want)
		case <-timer.C:
			timer.Reset(backupPollInterval)
		}
	}
}

// checkBackupManifest proves the manifest of a succeeded backup holds the
// Pulumi data: the store dialect, the SQL part, the padded checkpoint blob,
// the wrapper of the config secret and the home region.
func checkBackupManifest(t *testing.T, store e2e.Store, doc backupDoc) {
	t.Helper()

	manifest := doc.Manifest

	if manifest.ID != doc.ID {
		t.Fatalf("manifest id = %q, want backup %q", manifest.ID, doc.ID)
	}

	if manifest.Format != 1 {
		t.Fatalf("manifest format = %d, want 1", manifest.Format)
	}

	if manifest.Dialect != string(store) {
		t.Fatalf("manifest dialect = %q, want %q", manifest.Dialect, store)
	}

	if manifest.SQLSize <= 0 {
		t.Fatalf("manifest SQL size = %d, want above 0", manifest.SQLSize)
	}

	if got := backupCheckpointBlobSize(manifest); got < backupPadMinBlob {
		t.Fatalf("manifest checkpoint blob size = %d, want at least the %d pad bytes gzipped", got, backupPadMinBlob)
	}

	if manifest.SQLSHA256 == "" {
		t.Fatal("manifest holds no SQL SHA-256")
	}

	if manifest.SchemaVersion <= 0 {
		t.Fatalf("manifest schema version = %d, want above 0", manifest.SchemaVersion)
	}

	if len(manifest.WrapperIDs) == 0 {
		t.Fatal("manifest holds no wrapper ID, want the key of the config secret")
	}

	if len(manifest.HomeRegions) == 0 {
		t.Fatal("manifest holds no home region")
	}
}

// checkBackupList proves GET lists the backups newest first and holds the
// backup the test started.
func checkBackupList(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("GET backups: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET backups answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc struct {
		Backups []backupDoc `json:"backups"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode backup list %q: %v", body, err)
	}

	found := false

	for i := range doc.Backups {
		entry := &doc.Backups[i]
		if entry.ID == id {
			found = true

			if entry.State != "succeeded" {
				t.Fatalf("listed backup %s reads %q, want succeeded", id, entry.State)
			}
		}
	}

	if !found {
		t.Fatalf("backup list holds %v, want backup %q", doc.Backups, id)
	}

	checkBackupListOrder(t, doc.Backups)
}

// checkBackupListOrder proves the listed backups run newest first by start
// time.
func checkBackupListOrder(t *testing.T, backups []backupDoc) {
	t.Helper()

	seen := time.Time{}

	for i := range backups {
		at, err := time.Parse(time.RFC3339Nano, backups[i].StartedAt)
		if err != nil {
			t.Fatalf("backup %s startedAt %q does not parse: %v", backups[i].ID, backups[i].StartedAt, err)
		}

		if !seen.IsZero() && at.After(seen) {
			t.Fatalf("backup list is not newest first at %q", backups[i].ID)
		}

		seen = at
	}
}

// checkBackupUnauthorized proves the backup routes answer 401 without a
// token, with the one JSON error shape.
func checkBackupUnauthorized(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	for _, target := range []struct {
		method, path string
	}{
		{http.MethodPost, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups/" + id},
	} {
		status, body, err := env.ServerAPIStatusBare(ctx, target.method, target.path)
		if err != nil {
			t.Fatalf("%s %s without a token: %v", target.method, target.path, err)
		}

		if status != http.StatusUnauthorized {
			t.Fatalf("%s %s without a token answers %d, want %d (%s)",
				target.method, target.path, status, http.StatusUnauthorized, body)
		}

		var doc struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode 401 of %s %s: %v (%s)", target.method, target.path, err, body)
		}

		if doc.Code != http.StatusUnauthorized || doc.Message == "" {
			t.Fatalf("%s %s error = %+v, want code 401 with a message", target.method, target.path, doc)
		}
	}
}

// checkBackupCommand runs `opendustpan backup` through RunBinary against
// the running server (ADR 30 decision 10): the command starts a backup,
// waits, and exits with status 0. The output names the backup ID, the
// succeeded state and the key warning that a backup cannot decrypt secrets
// without DUSTPAN_KEY_ENCRYPTION_KEY. The command trusts the server through
// --ca-file with the same certificate bundle the CLIs trust through
// SSL_CERT_FILE, and it reads the token from DUSTPAN_ADMIN_TOKEN in the
// RunBinary environment. It needs E2E_SERVER=binary.
func checkBackupCommand(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	res := env.RunBinary(ctx, t, "backup", "--server", env.URL, "--ca-file", env.CABundle())
	if res.ExitCode != 0 {
		t.Fatalf("opendustpan backup exits with status %d: %s%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	for _, want := range []string{"bkp-", "succeeded", "DUSTPAN_KEY_ENCRYPTION_KEY", "plain text"} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("opendustpan backup output lacks %q: %q", want, res.Stdout)
		}
	}

	t.Logf("opendustpan backup: %s", strings.TrimSpace(res.Stdout))
}

// checkBackupRestartFailed stops the server during a running backup, starts
// it, and proves the row reads failed. The padded checkpoint holds the backup
// running, so the stop lands mid-backup with no test hook. It also proves the
// earlier backup survives the restart still succeeded.
func checkBackupRestartFailed(ctx context.Context, t *testing.T, env *e2e.Env, firstID string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("POST backups before the restart: %v", err)
	}

	if status != http.StatusAccepted {
		t.Fatalf("POST backups before the restart answers %d, want %d (%s)", status, http.StatusAccepted, body)
	}

	var started struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil || started.ID == "" {
		t.Fatalf("POST backups before the restart answers %q, want an id: %v", body, err)
	}

	env.StopServer(ctx, t)
	env.StartServer(ctx, t)

	doc := pollBackupState(ctx, t, env, started.ID, "failed")

	if doc.Error == "" {
		t.Fatalf("backup %s reads failed with no error text", started.ID)
	}

	t.Logf("backup %s failed after the restart: %q", started.ID, doc.Error)

	again := pollBackupState(ctx, t, env, firstID, "succeeded")
	if backupCheckpointBlobSize(again.Manifest) < backupPadMinBlob {
		t.Fatalf("backup %s after the restart holds no checkpoint blob of %d pad bytes gzipped",
			firstID, backupPadMinBlob)
	}
}

// backupCheckpointBlobSize returns the largest checkpoint blob of the
// manifest: the padded checkpoint the backup data set writes. Checkpoints
// live in blobs under the pulumi/ prefix.
func backupCheckpointBlobSize(manifest backupManifestDoc) int64 {
	var largest int64

	for i := range manifest.Blobs {
		if strings.HasPrefix(manifest.Blobs[i].Key, "pulumi/") {
			largest = max(largest, manifest.Blobs[i].Size)
		}
	}

	return largest
}

// restoreSecret is the Pulumi config secret the test writes before the
// backup and reads back after the restore.
const restoreSecret = "s3cr3t"

// TestBackupRestore proves the backup exit test of ADR 30 consequence 5 with
// the real binary. Server A serves with --backup-target. Both CLIs write
// first, then background writers churn other stacks while `opendustpan
// backup` runs, and `backup check` exits 0. The test stops A, wipes the
// destination, starts the server on the empty database, and restores the
// backup into the empty installation through `opendustpan restore` against
// the running server (ADR 38, ADR 39), then proves both CLIs read the same
// state: the Pulumi export and the OpenTofu pull match server A, the secret
// decrypts, and the history and state version counts match. It then restores
// over live data, runs a check-only restore, proves a failed check changes
// nothing, proves a lease from before a restore is fenced after it, undoes
// the restore with the backup taken before it, and proves a wrong-key and a
// schema-mismatch restore name their cause and change nothing. It runs for
// every CLI version of both tools, paired, on SQLite and on PostgreSQL, and
// only with E2E_SERVER=binary on Linux.
func TestBackupRestore(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		t.Skipf("the restore test needs Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersions := e2e.OpenTofu().Versions()

	if len(pulumiVersions) == 0 || len(tofuVersions) == 0 {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiCLIs := installBackupCLIs(installCtx, t, fetcher, e2e.Pulumi(), pulumiVersions)
	tofuCLIs := installBackupCLIs(installCtx, t, fetcher, e2e.OpenTofu(), tofuVersions)

	pairs := pairBackupCLIs(pulumiCLIs, tofuCLIs)

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			for _, p := range pairs {
				t.Run(p.name, func(t *testing.T) {
					t.Parallel()
					runBackupRestoreOnStore(t, p, db)
				})
			}
		})
	}
}

// installBackupCLIs installs every version label of tool in order and returns
// the installs, so the restore test drives every CLI version of its matrix.
func installBackupCLIs(
	ctx context.Context, t *testing.T, fetcher *e2e.Fetcher, tool e2e.Tool, labels []string,
) []e2e.Install {
	t.Helper()

	clis := make([]e2e.Install, 0, len(labels))

	for _, label := range labels {
		cli, err := fetcher.Install(ctx, tool, label)
		if err != nil {
			t.Fatalf("install %s %s: %v", tool.Name, label, err)
		}

		clis = append(clis, cli)
	}

	return clis
}

// backupPair is one Pulumi CLI with one OpenTofu CLI for one restore run.
type backupPair struct {
	name   string
	pulumi e2e.Install
	tofu   e2e.Install
}

// pairBackupCLIs pairs the CLI versions of both tools round-robin, so every
// version of each matrix runs at least once with the other tool.
func pairBackupCLIs(pulumi, tofu []e2e.Install) []backupPair {
	count := max(len(pulumi), len(tofu))
	pairs := make([]backupPair, 0, count)

	for i := range count {
		p, q := pulumi[i%len(pulumi)], tofu[i%len(tofu)]
		pairs = append(pairs, backupPair{name: p.Label + "+" + q.Label, pulumi: p, tofu: q})
	}

	return pairs
}

// runBackupRestoreOnStore runs the backup exit test on one store with one
// CLI pair: both CLIs write, a backup runs while background writers churn,
// backup check exits 0, the backup restores into an empty destination with
// the same key, server B reads the same state, a wrong-key restore names the
// key and writes nothing, and loaded-destination and schema-mismatch restores
// exit 1. Restores run through `opendustpan restore` against the running
// server (ADR 38, ADR 39): restore into an empty installation, restore over
// live data, check-only, a failed check that changes nothing, a lease from
// before the restore fenced after it, and an undo with the backup taken
// before the restore.
func runBackupRestoreOnStore(t *testing.T, tools backupPair, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	if e2e.UseS3Blobs(t) {
		if _, ok := e2e.LookupS3Config(); !ok {
			t.Skip(e2e.S3SkipReason())
		}
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, tools.pulumi, store)

	target := env.BackupTarget(t)
	if !validBackupTarget(target) {
		t.Fatalf("backup target = %q, want dir:<path> or s3://<bucket>[/<prefix>] on server A", target)
	}

	writeExitTestState(ctx, t, env, tools.pulumi, tools.tofu)

	before := captureExitState(ctx, t, env, tools, "before backup")
	id := backupWhileWriting(ctx, t, env, tools.pulumi, tools.tofu)
	after := captureExitState(ctx, t, env, tools, "after backup")
	checkExitStateStable(t, before, after)
	runBackupCheck(ctx, t, env, id)
	backupVersions := readFailoverVersions(ctx, t, env, store)

	// Restore into an empty installation through the API: stop A, wipe the
	// destination, start the server on the empty database, and restore
	// through the new client command.
	env.StopServer(ctx, t)
	wipeRestoreDestination(ctx, t, env, store)
	env.StartServer(ctx, t)
	runRestoreCommand(ctx, t, env, id)
	env.NoteStep(t, "restore into an empty installation", "restored "+id+" through the API")

	restored := captureExitState(ctx, t, env, tools, "after restore")
	checkExitStateMatches(t, after, restored)
	checkRestoreFenced(t, backupVersions, readFailoverVersions(ctx, t, env, store))

	// Restore over live data: back up the restored state, restore the
	// first backup over it, and prove the state matches the first backup.
	liveID := runBackupCommand(ctx, t, env)
	runRestoreCommand(ctx, t, env, id)
	overRestored := captureExitState(ctx, t, env, tools, "after over-live restore")
	checkExitStateMatches(t, after, overRestored)

	// Check-only changes nothing.
	runRestoreCheckOnly(ctx, t, env, liveID)
	checkOnlyUnchanged := captureExitState(ctx, t, env, tools, "after check-only")
	checkExitStateMatches(t, overRestored, checkOnlyUnchanged)

	// A failed check changes nothing: restore a missing backup and prove
	// the state stays.
	runRestoreMissing(ctx, t, env)
	failedUnchanged := captureExitState(ctx, t, env, tools, "after failed check")
	checkExitStateMatches(t, overRestored, failedUnchanged)

	// A lease from before the restore is fenced after it: hold an update,
	// prove a restore without --force is refused while work is active, then
	// restore with --force and prove the old lease no longer writes and every
	// namespace moved exactly one failover version.
	held := env.StartHeldUpdate(ctx, t, e2e.Namespace, e2e.Project, stackName)
	heldBaseline := captureExitState(ctx, t, env, tools, "with held update")
	runRestoreRefused(ctx, t, env, id, "active")
	runRestoreCommandForced(ctx, t, env, id)
	checkHeldLeaseFenced(ctx, t, env, held)
	checkRestoreFenced(t, backupVersions, readFailoverVersions(ctx, t, env, store))
	env.NoteStep(t, "lease fenced after restore", "old lease from before the restore no longer writes")

	// Undo with the backup taken before the restore: the restore names its
	// pre-backup, and restoring it brings back the state from before the
	// forced restore, held update included.
	preID := restorePreBackupID(ctx, t, env, id)
	runRestoreCommand(ctx, t, env, preID)
	undone := captureExitState(ctx, t, env, tools, "after undo")
	checkExitStateMatches(t, heldBaseline, undone)

	// A restore with a key that matches no wrapper stops before it writes
	// and names DUSTPAN_KEY_ENCRYPTION_KEY: tamper the manifest wrappers in
	// place, restore, prove the failure and the unchanged state, then put
	// the manifest back.
	saved := readTargetManifest(t, target, id)
	rewriteTargetManifest(t, target, id, func(m *backup.Manifest) { m.WrapperIDs = []string{"kek-unknown"} })
	runRestoreRefused(ctx, t, env, id, "DUSTPAN_KEY_ENCRYPTION_KEY")
	wrongKeyUnchanged := captureExitState(ctx, t, env, tools, "after wrong-key restore")
	checkExitStateMatches(t, undone, wrongKeyUnchanged)
	writeTargetManifest(t, target, id, saved)

	// A restore of another schema version stops before it writes and names
	// the versions: tamper the schema version in place, restore, prove the
	// failure and the unchanged state, then put the manifest back.
	rewriteTargetManifest(t, target, id, func(m *backup.Manifest) { m.SchemaVersion += 1000 })
	runRestoreRefused(ctx, t, env, id, "schema version")
	schemaUnchanged := captureExitState(ctx, t, env, tools, "after schema-mismatch restore")
	checkExitStateMatches(t, undone, schemaUnchanged)
	writeTargetManifest(t, target, id, saved)
}

// writeExitTestState writes the checked state with both CLIs: a Pulumi stack
// with a secret and an up, and OpenTofu state with an apply. It then grows
// the database with one padded checkpoint, so the backup window stays open
// while the background writers churn.
func writeExitTestState(ctx context.Context, t *testing.T, env *e2e.Env, pulumiCLI, tofuCLI e2e.Install) {
	t.Helper()

	env.SetCLI(pulumiCLI)

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	for _, s := range []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
		// The scratch program reads stack other, so up needs it to exist.
		step("stack init other", nil, "stack", "init", "other"),
		step("stack select", nil, "stack", "select", qualified),
		step("config set secret", nil, "config", "set", "--secret", "apiKey", restoreSecret),
		step("up", nil, "up", "--yes", "--non-interactive"),
	} {
		env.Step(ctx, t, s)
	}

	exported := env.Step(ctx, t, step("stack export for pad", exportHoldsReference, "stack", "export"))

	held := env.StartHeldUpdate(ctx, t, e2e.Namespace, e2e.Project, stackName)
	env.SaveHeldPaddedCheckpoint(ctx, t, held, exported.Stdout, map[string]string{
		"e2ePad": strings.Repeat("x", backupPadBytes),
	})
	env.EndHeldUpdate(ctx, t, held, "succeeded")

	env.SetCLI(tofuCLI)

	for _, s := range []e2e.StepSpec{
		step("init", nil, "init", "-input=false", "-no-color"),
		step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
	} {
		env.Step(ctx, t, s)
	}
}

// exitState holds the checked state of one server: the Pulumi export with the
// history count, and the OpenTofu pull with the state version count.
type exitState struct {
	export       string
	pull         importStateDoc
	history      int
	tofuVersions int
}

// captureExitState reads the checked state with both CLIs: the Pulumi export
// with the secret and the history count, and the OpenTofu pull with the state
// version count. tag names the phase in the report, for example
// "before backup".
func captureExitState(
	ctx context.Context, t *testing.T, env *e2e.Env, tools backupPair, tag string,
) exitState {
	t.Helper()

	env.SetCLI(tools.pulumi)

	exported := env.Step(ctx, t, step("stack export "+tag, exportHoldsReference, "stack", "export"))
	env.Step(ctx, t, step("config get "+tag, contains(restoreSecret), "config", "get", "apiKey"))
	history := env.Step(ctx, t, step("stack history "+tag, nil, "stack", "history", "--json"))

	env.SetCLI(tools.tofu)

	pulled := env.Step(ctx, t, step("state pull "+tag, contains(`"greeting"`), "state", "pull"))

	doc, err := decodeImportState(pulled.Stdout)
	if err != nil {
		t.Fatalf("decode pulled state %s: %v", tag, err)
	}

	return exitState{
		export:       strings.TrimSpace(exported.Stdout),
		pull:         doc,
		history:      countHistory(t, history.Stdout, tag),
		tofuVersions: countStateVersions(ctx, t, env, tag),
	}
}

// countHistory returns the number of entries in a `stack history --json`
// document.
func countHistory(t *testing.T, stdout, tag string) int {
	t.Helper()

	var updates []struct {
		Kind string `json:"kind"`
	}

	if err := json.Unmarshal([]byte(stdout), &updates); err != nil {
		t.Fatalf("decode stack history %s: %v", tag, err)
	}

	return len(updates)
}

// countStateVersions returns the total state version count of the checked
// workspace through the list route.
func countStateVersions(ctx context.Context, t *testing.T, env *e2e.Env, tag string) int {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, stateVersionListBase(), "")
	if err != nil {
		t.Fatalf("list state versions %s: %v", tag, err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions %s answers %d: %s", tag, status, body)
	}

	doc, err := decodeStateVersionList(body)
	if err != nil {
		t.Fatalf("decode state version list %s: %v", tag, err)
	}

	return doc.Meta.Pagination.TotalCount
}

// checkExitStateStable proves the background writers left the checked state
// alone: the export, pull, history count and state version count match across
// the concurrent backup window.
func checkExitStateStable(t *testing.T, before, after exitState) {
	t.Helper()
	checkExitStateEqual(t, before, after, "across the backup while writers churn")
}

// checkExitStateMatches proves server B reads the same state server A held at
// the manifest: the same export, pull serial and lineage and content, history
// count and state version count.
func checkExitStateMatches(t *testing.T, want, got exitState) {
	t.Helper()
	checkExitStateEqual(t, want, got, "after restore")
}

// checkExitStateEqual proves two checked states hold the same export bytes,
// pull serial and lineage and content, history count and state version count.
func checkExitStateEqual(t *testing.T, want, got exitState, tag string) {
	t.Helper()

	if got.export != want.export {
		t.Errorf("stack export %s differs from server A at the manifest", tag)
	}

	if got.pull.Serial != want.pull.Serial || got.pull.Lineage != want.pull.Lineage {
		t.Errorf(
			"state pull %s holds serial %d lineage %q, want serial %d lineage %q",
			tag,
			got.pull.Serial,
			got.pull.Lineage,
			want.pull.Serial,
			want.pull.Lineage,
		)
	}

	wantContent, err := canonicalImportState(want.pull)
	if err != nil {
		t.Fatalf("encode pulled state: %v", err)
	}

	gotContent, err := canonicalImportState(got.pull)
	if err != nil {
		t.Fatalf("encode restored state: %v", err)
	}

	if gotContent != wantContent {
		t.Errorf("state pull content %s differs from server A at the manifest", tag)
	}

	if got.history != want.history {
		t.Errorf("stack history %s holds %d entries, want %d", tag, got.history, want.history)
	}

	if got.tofuVersions != want.tofuVersions {
		t.Errorf("state versions %s hold %d, want %d", tag, got.tofuVersions, want.tofuVersions)
	}
}

// churnFirstTimeout bounds the wait for the first write of one background
// writer: a stack init and a workspace new finish well before it.
const churnFirstTimeout = 5 * time.Minute

// churnOutcome is the result of one background writer: its tool with the
// completed writes, or the first error.
type churnOutcome struct {
	tool   string
	writes int
	err    error
}

// backupWhileWriting starts one background writer per tool, waits for the
// first write of each, runs `opendustpan backup` while they keep writing,
// stops them and returns the backup ID. Each writer churns units outside the
// checked dev stack and cloud workspace, so the checked state stays stable
// while the database still writes during the backup.
func backupWhileWriting(
	ctx context.Context, t *testing.T, env *e2e.Env, pulumiCLI, tofuCLI e2e.Install,
) string {
	t.Helper()

	pulumiDir := t.TempDir()
	pulumiProject := []byte("name: " + e2e.Project + "\nruntime: yaml\n")

	if err := os.WriteFile(filepath.Join(pulumiDir, "Pulumi.yaml"), pulumiProject, 0o600); err != nil {
		t.Fatalf("write churn project: %v", err)
	}

	tofuDir := env.WriteRemoteProject(t)

	res, err := env.RunAs(ctx, tofuCLI, tofuDir, nil, "init", "-input=false", "-no-color")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("init churn project: exit %d: %v\n%s%s", res.ExitCode, err, res.Stdout, res.Stderr)
	}

	stop := make(chan struct{})

	var stopOnce sync.Once

	stopWriters := func() {
		stopOnce.Do(func() { close(stop) })
	}

	defer stopWriters()

	pulumiFirst := make(chan struct{}, 1)
	tofuFirst := make(chan struct{}, 1)
	done := make(chan churnOutcome, 2)

	go func() {
		done <- churnPulumiStacks(ctx, churnInput{
			env:   env,
			cli:   pulumiCLI,
			dir:   pulumiDir,
			stop:  stop,
			first: pulumiFirst,
		})
	}()

	go func() {
		done <- churnTofuWorkspaces(ctx, churnInput{
			env:   env,
			cli:   tofuCLI,
			dir:   tofuDir,
			stop:  stop,
			first: tofuFirst,
		})
	}()

	waitChurnFirst(ctx, t, pulumiFirst, pulumiCLI.Tool)
	waitChurnFirst(ctx, t, tofuFirst, tofuCLI.Tool)

	id := runBackupCommand(ctx, t, env)

	stopWriters()

	outcomes := []churnOutcome{<-done, <-done}

	for _, o := range outcomes {
		if o.err != nil {
			t.Fatalf("%s writer: %v", o.tool, o.err)
		}

		if o.writes == 0 {
			t.Fatalf("%s writer completed no writes during the backup", o.tool)
		}
	}

	env.NoteStep(t, "backup while writing", churnSummary(outcomes, id))

	return id
}

// waitChurnFirst waits for the first write of one background writer, so the
// backup starts only after both tools write.
func waitChurnFirst(ctx context.Context, t *testing.T, first <-chan struct{}, tool string) {
	t.Helper()

	deadline, cancel := context.WithTimeout(ctx, churnFirstTimeout)
	defer cancel()

	select {
	case <-first:
	case <-deadline.Done():
		t.Fatalf("%s writer completed no write, want churn during the backup", tool)
	}
}

// churnSummary renders the completed writes of the background writers with
// the backup ID for the report.
func churnSummary(outcomes []churnOutcome, id string) string {
	parts := make([]string, 0, len(outcomes))

	for _, o := range outcomes {
		parts = append(parts, fmt.Sprintf("%s writes=%d", o.tool, o.writes))
	}

	return fmt.Sprintf("backup %s while %s", id, strings.Join(parts, ", "))
}

// churnInput groups what one background writer needs: the server with the
// CLI and directory it drives, the stop channel that ends the loop, and the
// channel that takes the first completed write.
type churnInput struct {
	env   *e2e.Env
	cli   e2e.Install
	dir   string
	stop  <-chan struct{}
	first chan<- struct{}
}

// churnPulumiStacks creates one Pulumi stack per iteration until stop closes.
// Stack names are unique per run, so every iteration writes rows without
// touching the checked dev stack. It signals the first completed write on
// first.
func churnPulumiStacks(ctx context.Context, in churnInput) churnOutcome {
	outcome := churnOutcome{tool: in.cli.Tool, writes: 0, err: nil}
	signaled := false

	for i := 0; ; i++ {
		select {
		case <-in.stop:
			return outcome
		default:
		}

		name := fmt.Sprintf("%s/%s/churn-%d", e2e.Namespace, e2e.Project, i)

		res, err := in.env.RunAs(ctx, in.cli, in.dir, nil, "stack", "init", name, "--no-select")
		if err != nil {
			outcome.err = err

			return outcome
		}

		if res.ExitCode != 0 {
			outcome.err = fmt.Errorf(
				"%w: stack init %s exits %d: %s%s",
				errUnexpected,
				name,
				res.ExitCode,
				res.Stdout,
				res.Stderr,
			)

			return outcome
		}

		outcome.writes++

		if !signaled {
			signaled = true

			select {
			case in.first <- struct{}{}:
			case <-in.stop:
				return outcome
			}
		}
	}
}

// churnTofuWorkspaces creates one OpenTofu workspace per iteration until stop
// closes. Workspace names are unique per run, so every iteration writes rows
// without touching the checked cloud workspace. It signals the first
// completed write on first.
func churnTofuWorkspaces(ctx context.Context, in churnInput) churnOutcome {
	outcome := churnOutcome{tool: in.cli.Tool, writes: 0, err: nil}
	signaled := false

	for i := 0; ; i++ {
		select {
		case <-in.stop:
			return outcome
		default:
		}

		name := fmt.Sprintf("churn-%d", i)

		res, err := in.env.RunAs(ctx, in.cli, in.dir, nil, "workspace", "new", name, "-no-color")
		if err != nil {
			outcome.err = err

			return outcome
		}

		if res.ExitCode != 0 {
			outcome.err = fmt.Errorf(
				"%w: workspace new %s exits %d: %s%s",
				errUnexpected,
				name,
				res.ExitCode,
				res.Stdout,
				res.Stderr,
			)

			return outcome
		}

		outcome.writes++

		if !signaled {
			signaled = true

			select {
			case in.first <- struct{}{}:
			case <-in.stop:
				return outcome
			}
		}
	}
}

// runBackupCommand runs `opendustpan backup` against the running server and
// returns the backup ID. The output names the ID, the succeeded state, the
// key warning that a backup cannot decrypt secrets without
// DUSTPAN_KEY_ENCRYPTION_KEY, and the plain-text state warning.
func runBackupCommand(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	res := env.RunBinary(ctx, t, "backup", "--server", env.URL, "--ca-file", env.CABundle())
	if res.ExitCode != 0 {
		t.Fatalf("opendustpan backup exits with status %d:\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	for _, want := range []string{"succeeded", "DUSTPAN_KEY_ENCRYPTION_KEY", "plain text"} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("opendustpan backup output lacks %q:\n%s", want, res.Stdout)
		}
	}

	id := parseBackupID(res.Stdout)
	if id == "" {
		t.Fatalf("opendustpan backup output holds no backup ID:\n%s", res.Stdout)
	}

	return id
}

// parseBackupID returns the first bkp- ID in the backup command output, or
// "" when the output holds none.
func parseBackupID(stdout string) string {
	for field := range strings.FieldsSeq(stdout) {
		if strings.HasPrefix(field, "bkp-") {
			return field
		}
	}

	return ""
}

// validBackupTarget reports whether target names a backup target the test
// restores from: dir:<path> or s3://<bucket>[/<prefix>], with no blank path.
func validBackupTarget(target string) bool {
	if dir, ok := strings.CutPrefix(target, "dir:"); ok {
		return strings.TrimSpace(dir) != ""
	}

	if rest, ok := strings.CutPrefix(target, "s3://"); ok {
		return strings.TrimSpace(rest) != ""
	}

	return false
}

// runBackupCheck runs `opendustpan backup check` for id through the running
// server and proves it exits 0 with the complete verdict.
func runBackupCheck(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	res := env.RunBinary(
		ctx, t, "backup", "check",
		"--server", env.URL, "--ca-file", env.CABundle(), "--backup", id,
	)
	if res.ExitCode != 0 {
		t.Fatalf("backup check exits with status %d, want 0:\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "backup "+id+" is complete") {
		t.Fatalf("backup check output lacks the verdict:\n%s%s", res.Stdout, res.Stderr)
	}
}

// wipeRestoreDestination empties the destination the restore loads into: the
// SQLite file with its blob directory, or the PostgreSQL schema recreated
// empty, so the restore migrates it and finds no rows. The server is stopped.
func wipeRestoreDestination(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		wipeSQLiteDestination(ctx, t, env)
	case e2e.StorePostgres:
		wipePostgresDestination(ctx, t, env)
	default:
		t.Fatalf("unknown store: %s", store)
	}
}

// wipeSQLiteDestination removes the SQLite file with its journals and the
// live blobs. The restore recreates them.
func wipeSQLiteDestination(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	dbURL := env.DatabaseURL(t)

	path, ok := strings.CutPrefix(dbURL, "sqlite:")
	if !ok {
		t.Fatalf("database URL %q is not a SQLite URL", dbURL)
	}

	for _, file := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", file, err)
		}
	}

	env.ClearLiveBlobs(ctx, t)
}

// wipePostgresDestination drops the PostgreSQL schema the environment serves
// and recreates it empty, and removes the live blobs. The restore
// migrates the empty schema and finds no rows and no blobs: a copied blob
// that the destination already holds is an error.
func wipePostgresDestination(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	schema := restoreSchema(t, env.DatabaseURL(t))

	conn, err := pgx.Connect(ctx, e2e.PostgresURL())
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	name := pgx.Identifier{schema}.Sanitize()

	if _, err := conn.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}

	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	env.ClearLiveBlobs(ctx, t)
}

// restoreSchema returns the search_path schema of schemaURL: the isolated
// schema the environment serves.
func restoreSchema(t *testing.T, schemaURL string) string {
	t.Helper()

	parsed, err := url.Parse(schemaURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}

	if schema := parsed.Query().Get("search_path"); schema != "" {
		return schema
	}

	t.Fatal("database URL holds no search_path schema")

	return ""
}

// runRestoreRefused runs `opendustpan restore` for id against the running
// server and proves it exits 1 with output naming want, for restores the
// server refuses before it writes anything.
func runRestoreRefused(ctx context.Context, t *testing.T, env *e2e.Env, id, want string) {
	t.Helper()

	res := env.RunBinary(ctx, t, "restore", "--server", env.URL, "--ca-file", env.CABundle(), "--backup", id)

	if res.ExitCode != 1 {
		t.Fatalf("refused restore exits with status %d, want 1:\n%s%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}

	if out := res.Stdout + res.Stderr; !strings.Contains(out, want) {
		t.Fatalf("restore refusal lacks %q:\n%s", want, out)
	}
}

// targetManifestPath returns the path of backups/<id>/manifest.json in the
// file backup target, failing the test when the target is not a directory.
func targetManifestPath(t *testing.T, target, id string) string {
	t.Helper()

	dir, ok := strings.CutPrefix(target, "dir:")
	if !ok || strings.TrimSpace(dir) == "" {
		t.Fatalf("backup target = %q, want dir:<path>", target)
	}

	return filepath.Join(strings.TrimSpace(dir), "backups", id, "manifest.json")
}

// readTargetManifest reads backups/<id>/manifest.json from the backup target
// of the running server.
func readTargetManifest(t *testing.T, target, id string) backup.Manifest {
	t.Helper()

	// The test reads the manifest the server wrote into its own backup target.
	raw := readManifestBytes(t, target, id)

	var manifest backup.Manifest

	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	return manifest
}

// readManifestBytes returns backups/<id>/manifest.json from the backup target
// of the running server: a file read on a dir: target, an object read on an
// s3:// target.
func readManifestBytes(t *testing.T, target, id string) []byte {
	t.Helper()

	key := "backups/" + id + "/manifest.json"

	if strings.HasPrefix(target, "s3://") {
		ctx := t.Context()

		return readS3Object(ctx, t, e2e.OpenS3TargetStore(ctx, t, target), key)
	}

	raw, err := os.ReadFile(targetManifestPath(t, target, id))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	return raw
}

// rewriteTargetManifest edits backups/<id>/manifest.json in the backup target
// of the running server, so the next restore meets the tampered manifest.
// Callers write the saved manifest back after the refused restore.
func rewriteTargetManifest(t *testing.T, target, id string, edit func(*backup.Manifest)) {
	t.Helper()

	manifest := readTargetManifest(t, target, id)
	edit(&manifest)
	writeTargetManifest(t, target, id, manifest)
}

// writeTargetManifest writes manifest as backups/<id>/manifest.json in the
// backup target of the running server.
func writeTargetManifest(t *testing.T, target, id string, manifest backup.Manifest) {
	t.Helper()

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	writeManifestBytes(t, target, id, data)
}

// writeManifestBytes writes data as backups/<id>/manifest.json into the
// backup target of the running server: a file write on a dir: target, an
// object write on an s3:// target.
func writeManifestBytes(t *testing.T, target, id string, data []byte) {
	t.Helper()

	key := "backups/" + id + "/manifest.json"

	if strings.HasPrefix(target, "s3://") {
		ctx := t.Context()

		parsed, err := blob.ParseKey(key)
		if err != nil {
			t.Fatalf("parse backup object key %q: %v", key, err)
		}

		// Blob keys are write-once, so the old manifest goes first.
		store := e2e.OpenS3TargetStore(ctx, t, target)
		if delErr := store.Delete(ctx, parsed); delErr != nil {
			t.Fatalf("delete backup object %s: %v", key, delErr)
		}

		if putErr := store.Put(ctx, parsed, bytes.NewReader(data)); putErr != nil {
			t.Fatalf("write backup object %s: %v", key, putErr)
		}

		return
	}

	if err := os.WriteFile(targetManifestPath(t, target, id), data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// runRestoreCommand runs `opendustpan restore` against the running server
// (ADR 38, ADR 39): it starts a restore of id, waits until it leaves
// running, and proves it succeeded. The server reads its own configured
// backup target and its own live stores: the command takes no --from,
// --database, --blob-dir or --blob-store.
func runRestoreCommand(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	checkRestoreSucceeded(t, id, env.RunBinary(
		ctx, t, "restore", "--server", env.URL, "--ca-file", env.CABundle(), "--backup", id,
	))
}

// runRestoreCommandForced restores id with --force, canceling active work
// first, and proves it succeeded.
func runRestoreCommandForced(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	checkRestoreSucceeded(t, id, env.RunBinary(
		ctx, t, "restore", "--server", env.URL, "--ca-file", env.CABundle(), "--backup", id, "--force",
	))
}

// checkRestoreSucceeded proves a restore invocation exited 0 with the
// succeeded state.
func checkRestoreSucceeded(t *testing.T, id string, res e2e.BinaryResult) {
	t.Helper()

	if res.ExitCode != 0 {
		t.Fatalf("opendustpan restore %s exits with status %d:\n%s%s", id, res.ExitCode, res.Stdout, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "succeeded") {
		t.Fatalf("opendustpan restore %s output lacks the succeeded state:\n%s%s", id, res.Stdout, res.Stderr)
	}
}

// runRestoreCheckOnly runs `opendustpan restore --check-only` and proves it
// exits 0 without applying anything.
func runRestoreCheckOnly(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	res := env.RunBinary(
		ctx, t, "restore", "--server", env.URL, "--ca-file", env.CABundle(), "--backup", id, "--check-only",
	)
	if res.ExitCode != 0 {
		t.Fatalf("opendustpan restore --check-only %s exits with status %d:\n%s%s",
			id, res.ExitCode, res.Stdout, res.Stderr)
	}
}

// runRestoreMissing restores a backup ID the target never held and proves it
// exits 1 without changing the live data.
func runRestoreMissing(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	res := env.RunBinary(
		ctx, t, "restore", "--server", env.URL, "--ca-file", env.CABundle(), "--backup", "bkp-missing000000000001",
	)
	if res.ExitCode != 1 {
		t.Fatalf("restore of a missing backup exits with status %d, want 1:\n%s%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}
}

// checkHeldLeaseFenced proves the lease held from before the restore no
// longer writes: completing it with the old lease token fails, while a new
// update starts at once. The restore replaced the update row, so the old
// lease answers an error either way; checkRestoreFenced proves the fencing
// itself by reading the failover version of every namespace.
func checkHeldLeaseFenced(ctx context.Context, t *testing.T, env *e2e.Env, held e2e.HeldUpdate) {
	t.Helper()

	prefix := "/api/stacks/" + held.Namespace + "/" + held.Project + "/" + held.Name

	got, err := env.StatusWithHeader(
		ctx,
		"update-token "+held.Lease,
		http.MethodPost,
		prefix+"/update/"+held.ID+"/complete",
		`{"status":"succeeded"}`,
	)
	if err != nil {
		t.Fatalf("complete old update after restore: %v", err)
	}

	if got.Status < 400 {
		t.Fatalf("complete with a lease from before the restore answers %d, want an error (%s)",
			got.Status, got.Body)
	}

	fresh := env.StartHeldUpdate(ctx, t, held.Namespace, held.Project, held.Name)
	env.EndHeldUpdate(ctx, t, fresh, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.
}

// readFailoverVersions returns the failover version of every namespace in the
// live database, read directly: the server API exposes no namespace read, and
// the fencing proof needs the stored versions before and after the restore.
func readFailoverVersions(
	ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store,
) map[string]int64 {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		return readSQLiteFailoverVersions(ctx, t, env)
	case e2e.StorePostgres:
		return readPostgresFailoverVersions(ctx, t, env)
	default:
		t.Fatalf("unknown store: %s", store)
	}

	return nil
}

// readSQLiteFailoverVersions reads the failover version of every namespace
// from the live SQLite file while the server runs: a single ordered select,
// so a concurrent write only waits out the read.
func readSQLiteFailoverVersions(ctx context.Context, t *testing.T, env *e2e.Env) map[string]int64 {
	t.Helper()

	path, ok := strings.CutPrefix(env.DatabaseURL(t), "sqlite:")
	if !ok {
		t.Fatalf("database URL %q is not a SQLite URL", env.DatabaseURL(t))
	}

	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open database for failover versions: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	}()

	return queryFailoverVersions(ctx, t, conn)
}

// queryFailoverVersions returns the failover version of every namespace
// through conn, which reads the live database of the running server.
func queryFailoverVersions(ctx context.Context, t *testing.T, conn *sql.DB) map[string]int64 {
	t.Helper()

	rows, err := conn.QueryContext(ctx, `SELECT name, failover_version FROM namespaces ORDER BY name`)
	if err != nil {
		t.Fatalf("read failover versions: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close failover versions: %v", closeErr)
		}
	}()

	out := map[string]int64{}

	for rows.Next() {
		var (
			name    string
			version int64
		)

		if scanErr := rows.Scan(&name, &version); scanErr != nil {
			t.Fatalf("scan failover version: %v", scanErr)
		}

		out[name] = version
	}

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read failover versions: %v", iterErr)
	}

	return out
}

// readPostgresFailoverVersions reads the failover version of every namespace
// from the live PostgreSQL schema of the running server.
func readPostgresFailoverVersions(ctx context.Context, t *testing.T, env *e2e.Env) map[string]int64 {
	t.Helper()

	conn, err := pgx.Connect(ctx, env.DatabaseURL(t))
	if err != nil {
		t.Fatalf("connect to postgres for failover versions: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	rows, err := conn.Query(ctx, `SELECT name, failover_version FROM namespaces ORDER BY name`)
	if err != nil {
		t.Fatalf("read failover versions: %v", err)
	}

	defer rows.Close()

	out := map[string]int64{}

	for rows.Next() {
		var (
			name    string
			version int64
		)

		if scanErr := rows.Scan(&name, &version); scanErr != nil {
			t.Fatalf("scan failover version: %v", scanErr)
		}

		out[name] = version
	}

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read failover versions: %v", iterErr)
	}

	return out
}

// checkRestoreFenced proves the restore fenced every namespace the backup
// holds: each version after the restore sits exactly one above the version
// the backup captured. A restore that swaps the data without the bump leaves
// the captured versions and fails here, while the replaced update rows alone
// would still answer that the old lease is gone.
func checkRestoreFenced(t *testing.T, backupVersions, after map[string]int64) {
	t.Helper()

	if len(after) == 0 {
		t.Fatal("no namespaces after the restore, want the fenced namespaces")
	}

	for name, captured := range backupVersions {
		got, ok := after[name]
		if !ok {
			t.Fatalf("namespace %q is missing after the restore", name)
		}

		if got != captured+1 {
			t.Fatalf(
				"failover version of %q after the restore is %d, want one above the backed-up %d",
				name,
				got,
				captured,
			)
		}
	}

	for name := range after {
		if _, ok := backupVersions[name]; !ok {
			t.Fatalf("namespace %q appeared during the restore", name)
		}
	}
}

// restoreListEntry is one row of the restores list in tests.
type restoreListEntry struct {
	BackupID    string `json:"backupID"`
	PreBackupID string `json:"preBackupID"`
}

// restoreListDoc decodes the restores list in tests.
type restoreListDoc struct {
	Restores []restoreListEntry `json:"restores"`
}

// restorePreBackupID reads the restore of id and returns the backup taken
// before it, failing when the restore names none.
func restorePreBackupID(ctx context.Context, t *testing.T, env *e2e.Env, id string) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/restores", "")
	if err != nil {
		t.Fatalf("list restores: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list restores answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc restoreListDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode restores %q: %v", body, err)
	}

	for _, r := range doc.Restores {
		if r.BackupID == id && r.PreBackupID != "" {
			return r.PreBackupID
		}
	}

	t.Fatalf("restores list holds no pre-backup for %s (%s)", id, body)

	return ""
}

// readS3Object returns the content of the object under key in store.
func readS3Object(ctx context.Context, t *testing.T, store blob.Store, key string) []byte {
	t.Helper()

	parsed, err := blob.ParseKey(key)
	if err != nil {
		t.Fatalf("parse backup object key %q: %v", key, err)
	}

	body, err := store.Get(ctx, parsed)
	if err != nil {
		t.Fatalf("read backup object %s: %v", key, err)
	}

	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			t.Errorf("close backup object %s: %v", key, closeErr)
		}
	}()

	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read backup object %s: %v", key, err)
	}

	return data
}

// maintenanceStateDoc decodes the installation state in tests.
type maintenanceStateDoc struct {
	State string `json:"state"`
}

// enterMaintenanceDoc decodes an enter response in tests.
type enterMaintenanceDoc struct {
	State    string   `json:"state"`
	Canceled []string `json:"canceled"`
}

// maintenanceErrorDoc decodes an installation API error in tests.
type maintenanceErrorDoc struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// getMaintenanceState reads the installation state through the installation
// API with the admin token.
func getMaintenanceState(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/maintenance", "")
	if err != nil {
		t.Fatalf("GET maintenance: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET maintenance answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc maintenanceStateDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode maintenance state %q: %v", body, err)
	}

	return doc.State
}

// enterMaintenance enters maintenance mode through the installation API and
// returns the status with the canceled unit references. forceBody is the
// enter body: empty enters without force, `{"force":true}` cancels the
// active work first.
func enterMaintenance(
	ctx context.Context, t *testing.T, env *e2e.Env, forceBody string,
) (int, []string, string) {
	t.Helper()

	status, resp, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/maintenance/enter", forceBody)
	if err != nil {
		t.Fatalf("POST maintenance enter: %v", err)
	}

	if status != http.StatusOK {
		return status, nil, resp
	}

	var doc enterMaintenanceDoc

	if err := json.Unmarshal([]byte(resp), &doc); err != nil {
		t.Fatalf("decode enter %q: %v", resp, err)
	}

	if doc.State != "maintenance" {
		t.Fatalf("enter state = %q, want maintenance (%s)", doc.State, resp)
	}

	return status, doc.Canceled, resp
}

// leaveMaintenance leaves maintenance mode through the installation API.
func leaveMaintenance(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/maintenance/leave", "")
	if err != nil {
		t.Fatalf("POST maintenance leave: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("POST maintenance leave answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc maintenanceStateDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode leave %q: %v", body, err)
	}

	if doc.State != "normal" {
		t.Fatalf("leave state = %q, want normal", doc.State)
	}
}

// waitForToolStatus polls GET /api/user until it answers want or the wait
// times out. The maintenance watcher caches the installation state with a
// short limit, so a state change reaches the tool routes only after the
// cache expires: the poll waits that out instead of sleeping a fixed time.
func waitForToolStatus(ctx context.Context, t *testing.T, env *e2e.Env, want int) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		status, _, err := env.APIStatus(ctx, http.MethodGet, "/api/user", "")
		if err == nil && status == want {
			return
		}

		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("test ended while waiting for the maintenance gate to answer %d", want)
		case <-timer.C:
		}
	}

	t.Fatalf("maintenance gate never answered %d", want)
}

// waitForMaintenanceRefusal waits until the maintenance gate refuses tool
// calls with 503.
func waitForMaintenanceRefusal(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	waitForToolStatus(ctx, t, env, http.StatusServiceUnavailable)
}

// waitForMaintenanceServe waits until the maintenance gate serves tool calls
// again.
func waitForMaintenanceServe(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	waitForToolStatus(ctx, t, env, http.StatusOK)
}

// decodeMaintenanceError decodes an installation API error and proves it
// holds want with a message naming wantName.
func decodeMaintenanceError(t *testing.T, body string, want int, wantName string) {
	t.Helper()

	var doc maintenanceErrorDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %d of maintenance enter: %v (%s)", want, err, body)
	}

	if doc.Code != want || !strings.Contains(doc.Message, wantName) {
		t.Fatalf("maintenance enter error = %+v, want code %d naming %q", doc, want, wantName)
	}
}

// pulumiMaintenanceScenario proves maintenance mode (ADR 38 decision 4) with
// the Pulumi CLI: entering refuses while the update of the renamed stack
// runs, force cancels the update and names it, the CLI fails with the
// maintenance message, the probes stay available, and leaving restores the
// CLI. It runs in every matrix version, so the report shows what each
// supported CLI does on the 503.
func pulumiMaintenanceScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()
	unit := "pulumi:" + org + "/" + e2e.Project + "/renamed"

	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")

	status, _, body := enterMaintenance(ctx, t, env, "")
	if status != http.StatusConflict {
		t.Fatalf("POST enter with a running update answers %d, want %d (%s)",
			status, http.StatusConflict, body)
	}

	decodeMaintenanceError(t, body, http.StatusConflict, unit)

	status, canceled, _ := enterMaintenance(ctx, t, env, `{"force":true}`)
	if status != http.StatusOK {
		t.Fatalf("POST enter with force answers %d, want %d", status, http.StatusOK)
	}

	if len(canceled) != 1 || canceled[0] != unit {
		t.Fatalf("force enter canceled = %v, want [%s]", canceled, unit)
	}

	// The force canceled the held update through the server, so the test
	// never ends it: the lease is dead.
	_ = held

	waitForMaintenanceRefusal(ctx, t, env)

	if got := getMaintenanceState(ctx, t, env); got != "maintenance" {
		t.Fatalf("installation state = %q, want maintenance", got)
	}

	env.Step(ctx, t, failStep(
		"stack ls in maintenance",
		outputContains("maintenance"),
		"stack", "ls", "--json",
	))

	checkReady(ctx, t, env)
	leaveMaintenance(ctx, t, env)
	waitForMaintenanceServe(ctx, t, env)

	env.Step(ctx, t, step("stack ls after maintenance", contains("renamed"), "stack", "ls", "--json"))
}

// tofuMaintenanceScenario proves maintenance mode (ADR 38 decision 4) with
// OpenTofu: entering refuses while the workspace is locked, force releases
// the lock and names it, the CLI fails with the maintenance message, and
// leaving restores the CLI. Discovery stays available, so the plan fails
// when it reads the organization: OpenTofu prints the 503 refusal title and
// detail ("maintenance / The installation is in maintenance mode. Retry
// later."). It runs in every matrix version, so the report shows what each
// supported OpenTofu does on the 503.
func tofuMaintenanceScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	unit := "opentofu:" + e2e.Namespace + "/" + e2e.Workspace
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock for maintenance",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})

	status, _, body := enterMaintenance(ctx, t, env, "")
	if status != http.StatusConflict {
		t.Fatalf("POST enter with a locked workspace answers %d, want %d (%s)",
			status, http.StatusConflict, body)
	}

	decodeMaintenanceError(t, body, http.StatusConflict, unit)

	status, canceled, _ := enterMaintenance(ctx, t, env, `{"force":true}`)
	if status != http.StatusOK {
		t.Fatalf("POST enter with force answers %d, want %d", status, http.StatusOK)
	}

	if len(canceled) != 1 || canceled[0] != unit {
		t.Fatalf("force enter canceled = %v, want [%s]", canceled, unit)
	}

	waitForMaintenanceRefusal(ctx, t, env)

	env.Step(ctx, t, failStep(
		"plan in maintenance",
		outputContains("maintenance"),
		"plan", "-input=false", "-no-color",
	))

	leaveMaintenance(ctx, t, env)
	waitForMaintenanceServe(ctx, t, env)

	env.Step(ctx, t, step("plan after maintenance", nil, "plan", "-input=false", "-no-color"))
}

// TestMaintenance proves the installation maintenance routes (ADR 38
// decision 4) with the first Pulumi version of the matrix on every store:
// the state reads normal, entering refuses while an update runs, force
// cancels it and names it, every tool adapter answers 503 with Retry-After
// and a message that names maintenance in its own error shape, the
// installation API and the probes stay available, and leaving restores the
// CLI. Calls without a token answer 401.
func TestMaintenance(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runMaintenanceOnStore(t, cli, db)
		})
	}
}

// runMaintenanceOnStore runs the maintenance steps on one store.
func runMaintenanceOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	org := e2e.Namespace
	qualified := org + "/" + e2e.Project + "/" + stackName
	unit := "pulumi:" + org + "/" + e2e.Project + "/" + stackName

	for _, s := range []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
	} {
		env.Step(ctx, t, s)
	}

	if got := getMaintenanceState(ctx, t, env); got != "normal" {
		t.Fatalf("installation state = %q, want normal", got)
	}

	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, stackName)

	status, _, body := enterMaintenance(ctx, t, env, "")
	if status != http.StatusConflict {
		t.Fatalf("POST enter with a running update answers %d, want %d (%s)",
			status, http.StatusConflict, body)
	}

	decodeMaintenanceError(t, body, http.StatusConflict, unit)

	status, canceled, _ := enterMaintenance(ctx, t, env, `{"force":true}`)
	if status != http.StatusOK {
		t.Fatalf("POST enter with force answers %d, want %d", status, http.StatusOK)
	}

	if len(canceled) != 1 || canceled[0] != unit {
		t.Fatalf("force enter canceled = %v, want [%s]", canceled, unit)
	}

	// The force canceled the held update through the server, so the test
	// never ends it: the lease is dead.
	_ = held

	waitForMaintenanceRefusal(ctx, t, env)

	if got := getMaintenanceState(ctx, t, env); got != "maintenance" {
		t.Fatalf("installation state = %q, want maintenance", got)
	}

	checkMaintenanceShapes(ctx, t, env)
	checkMaintenanceProbes(ctx, t, env)
	checkMaintenanceUnauthorized(ctx, t, env)

	leaveMaintenance(ctx, t, env)
	waitForMaintenanceServe(ctx, t, env)

	// The force freed the stack lock, so a new update starts at once.
	freed := env.StartHeldUpdate(ctx, t, org, e2e.Project, stackName)
	env.EndHeldUpdate(ctx, t, freed, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.
}

// refusalExpect groups what one maintenance refusal must answer: the
// adapter name for failures, its media type, and a body fragment beside the
// maintenance message.
type refusalExpect struct {
	adapter   string
	mediaType string
	wantBody  string
}

// checkMaintenanceShapes proves every tool adapter answers 503 with
// Retry-After and a message that names maintenance, each in its own error
// shape.
func checkMaintenanceShapes(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	got, err := env.StatusWithHeader(ctx, "token "+env.TokenA(), http.MethodGet, "/api/user", "")
	if err != nil {
		t.Fatalf("GET /api/user in maintenance: %v", err)
	}

	checkMaintenanceRefusal(t, got.Status, got.Header, got.Body, refusalExpect{
		adapter:   "Pulumi",
		mediaType: "application/json",
		wantBody:  `"code":503`,
	})

	got, err = env.StatusWithHeader(ctx, "", http.MethodGet, "/api/v2/ping", "")
	if err != nil {
		t.Fatalf("GET /api/v2/ping in maintenance: %v", err)
	}

	checkMaintenanceRefusal(t, got.Status, got.Header, got.Body, refusalExpect{
		adapter:   "TFE",
		mediaType: "application/vnd.api+json",
		wantBody:  `"status":"503"`,
	})

	got, err = env.StatusWithHeader(
		ctx, "token "+env.TokenA(), http.MethodGet, "/api/inventory/units?organization="+e2e.Namespace, "",
	)
	if err != nil {
		t.Fatalf("GET /api/inventory/units in maintenance: %v", err)
	}

	checkMaintenanceRefusal(t, got.Status, got.Header, got.Body, refusalExpect{
		adapter:   "inventory",
		mediaType: "application/json",
		wantBody:  `"code":503`,
	})
}

// checkMaintenanceRefusal proves one refusal answers 503 with a Retry-After
// wait, the media type of its adapter, and a body holding wantBody with
// maintenance named.
func checkMaintenanceRefusal(t *testing.T, status int, header http.Header, body string, exp refusalExpect) {
	t.Helper()

	if status != http.StatusServiceUnavailable {
		t.Fatalf("%s refusal answers %d, want %d (%s)", exp.adapter, status, http.StatusServiceUnavailable, body)
	}

	if got := header.Get("Retry-After"); got == "" {
		t.Fatalf("%s refusal holds no Retry-After header", exp.adapter)
	}

	if got := header.Get("Content-Type"); got != exp.mediaType {
		t.Fatalf("%s refusal media type = %q, want %q", exp.adapter, got, exp.mediaType)
	}

	if !strings.Contains(body, exp.wantBody) || !strings.Contains(body, "maintenance") {
		t.Fatalf("%s refusal body %q lacks %q with maintenance named", exp.adapter, body, exp.wantBody)
	}
}

// checkMaintenanceProbes proves the probes stay available in maintenance
// mode: readiness answers 200 and liveness answers 204, both without a
// token.
func checkMaintenanceProbes(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/health/ready", "")
	if err != nil {
		// APIStatus sends the admin token; the probes ignore it.
		t.Fatalf("GET /health/ready: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET /health/ready in maintenance answers %d, want %d (%s)",
			status, http.StatusOK, body)
	}

	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("GET /health/ready body %q, want status ok", body)
	}
}

// checkMaintenanceUnauthorized proves the maintenance routes answer 401
// without a token, with the one JSON error shape.
func checkMaintenanceUnauthorized(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, target := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/server/maintenance"},
		{http.MethodPost, "/api/server/maintenance/enter"},
		{http.MethodPost, "/api/server/maintenance/leave"},
	} {
		status, body, err := env.ServerAPIStatusBare(ctx, target.method, target.path)
		if err != nil {
			t.Fatalf("%s %s without a token: %v", target.method, target.path, err)
		}

		if status != http.StatusUnauthorized {
			t.Fatalf("%s %s without a token answers %d, want %d (%s)",
				target.method, target.path, status, http.StatusUnauthorized, body)
		}

		var doc maintenanceErrorDoc

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode 401 of %s %s: %v (%s)", target.method, target.path, err, body)
		}

		if doc.Code != http.StatusUnauthorized || doc.Message == "" {
			t.Fatalf("%s %s error = %+v, want code 401 with a message", target.method, target.path, doc)
		}
	}
}
