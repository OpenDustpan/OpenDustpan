//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/e2e"
)

// haReportMode is the mode of a file in the report directory.
const haReportMode = 0o600

// haReportDirMode is the mode of the report directory.
const haReportDirMode = 0o750

// TestHighAvailability runs the high availability exit test: several server
// binaries of one installation on one PostgreSQL database and one blob store,
// behind the round-robin TLS proxy of the suite. It runs for each pair of CLI
// versions of the matrix, on PostgreSQL only, and it needs E2E_INSTANCES above
// 1 (make e2e-ha). With one instance it skips.
//
// The steps run in order on one cluster per pair: alternation, a kill during
// work, a kill during a backup, a kill during a restore, a rolling deploy, the
// job schedules, a database outage and a missing worker. The step for a
// missing worker runs last, because it restarts every instance
// with only the api component.
func TestHighAvailability(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// Both CLIs must trust the test server's certificate, and Go
		// honors SSL_CERT_FILE only on Linux.
		t.Skipf("the high availability suite needs Linux, not %s", runtime.GOOS)
	}

	instances, err := e2e.Instances()
	if err != nil {
		t.Fatalf("instances: %v", err)
	}

	if instances < 2 {
		t.Skip("needs E2E_INSTANCES above 1: run make e2e-ha")
	}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	fetcher := sharedFetcher(t)

	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersions := e2e.OpenTofu().Versions()

	if len(pulumiVersions) == 0 || len(tofuVersions) == 0 {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pairs := pairBackupCLIs(
		installBackupCLIs(installCtx, t, fetcher, e2e.Pulumi(), pulumiVersions),
		installBackupCLIs(installCtx, t, fetcher, e2e.OpenTofu(), tofuVersions),
	)

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			for _, p := range pairs {
				t.Run(p.name, func(t *testing.T) {
					t.Parallel()
					runHighAvailability(t, p, db)
				})
			}
		})
	}
}

// runHighAvailability runs the steps of the exit test for one pair of CLI
// versions on one cluster.
func runHighAvailability(t *testing.T, pair backupPair, store e2e.Store) {
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
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, pair.tofu, store)
	cluster := env.HA(t)

	t.Logf("pulumi %s with tofu %s on %d instances", pair.pulumi.Version, pair.tofu.Version, cluster.Size())

	t.Run("1 alternation", func(t *testing.T) { haAlternation(t, env, pair) })
	t.Run("2 kill during work", func(t *testing.T) { haKillDuringWork(t, env, pair) })
	t.Run("3 kill during backup", func(t *testing.T) { haKillDuringBackup(t, env) })

	t.Run("4 restart during restore", func(t *testing.T) { haRestartDuringRestore(t, env) })
	t.Run("5 rolling deploy", func(t *testing.T) {
		haRollingDeploy(t, env, pair)
		t.Run("mixed-version matrix", haMixedVersionMatrix)
	})

	t.Run("6 schedules", func(t *testing.T) { haSchedules(t, env) })

	var window haRetryWindow

	t.Run("7a database outage", func(t *testing.T) { window = haLongOutage(t, env, pair) })
	t.Run("7b short database outage", func(t *testing.T) { haShortOutage(t, env, pair, window) })
	t.Run("8 no worker", func(t *testing.T) { haNoWorker(t, env) })
}

// haToolOf names the tool that sent a request, by its user agent, or "" for a
// request that neither CLI sent.
func haToolOf(userAgent string) string {
	ua := strings.ToLower(userAgent)

	switch {
	case strings.Contains(ua, "pulumi"):
		return e2e.ToolPulumi
	case strings.Contains(ua, "tofu"), strings.Contains(ua, "terraform"):
		return e2e.ToolTofu
	default:
		return ""
	}
}

// haServedBy counts the requests of each tool per instance, from the record
// of the proxy: tool, then instance name ("i0", "i1", ...), then requests.
func haServedBy(entries []e2e.Entry) map[string]map[string]int {
	out := map[string]map[string]int{e2e.ToolPulumi: {}, e2e.ToolTofu: {}}

	for i := range entries {
		tool := haToolOf(entries[i].UserAgent)
		if tool == "" {
			continue
		}

		out[tool][entries[i].Backend]++
	}

	return out
}

// haAlternation runs the full OpenTofu scenario and the full Pulumi scenario
// through the proxy, and checks the record of the proxy: every instance served
// requests of each tool, and no request went around the proxy.
func haAlternation(t *testing.T, env *e2e.Env, pair backupPair) {
	t.Helper()

	cluster := env.HA(t)

	tofuScenario(t, env)

	env.SetCLI(pair.pulumi)

	pulumiScenario(t, env)

	served := haServedBy(env.Recorder.Entries())

	for _, tool := range []string{e2e.ToolPulumi, e2e.ToolTofu} {
		for i := range cluster.Size() {
			name := fmt.Sprintf("i%d", i)
			if served[tool][name] == 0 {
				t.Errorf("instance %d served no %s request; the proxy record is %v", i, tool, served[tool])
			}
		}
	}

	// A request with no instance is one that the proxy answered itself,
	// because no instance was ready: a test that stops the whole installation
	// to write to its database does that on purpose.
	entries := env.Recorder.Entries()

	for i := range entries {
		entry := &entries[i]
		if entry.Backend != "" {
			continue
		}

		if entry.Status != http.StatusBadGateway && entry.Status != http.StatusServiceUnavailable {
			t.Errorf("request %s %s (step %q) has no instance in the proxy record and status %d",
				entry.Method, entry.Path, entry.Step, entry.Status)
		}

		t.Logf("the proxy answered %s %s (step %q) with %d: no instance was ready",
			entry.Method, entry.Path, entry.Step, entry.Status)
	}

	t.Logf("requests per instance: %v", served)
	haWriteRecord(t, pair, served)
}

// haWriteRecord writes the requests per tool and instance of the alternation
// step next to the other reports, so a run shows what each instance served.
func haWriteRecord(t *testing.T, pair backupPair, served map[string]map[string]int) {
	t.Helper()

	dir := e2e.ReportDir()

	if err := os.MkdirAll(dir, haReportDirMode); err != nil {
		t.Errorf("create the report directory: %v", err)

		return
	}

	raw, err := json.MarshalIndent(served, "", "  ")
	if err != nil {
		t.Errorf("encode the proxy record: %v", err)

		return
	}

	name := filepath.Join(dir, "ha-alternation-"+pair.name+".json")
	if err := os.WriteFile(name, raw, haReportMode); err != nil {
		t.Errorf("write the proxy record: %v", err)
	}
}

// haConnect opens a connection to the database that the cluster serves.
func haConnect(ctx context.Context, t *testing.T, env *e2e.Env) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(ctx, env.DatabaseURL(t))
	if err != nil {
		t.Fatalf("connect to the database: %v", err)
	}

	t.Cleanup(func() {
		if err := conn.Close(context.WithoutCancel(ctx)); err != nil {
			t.Logf("close the database connection: %v", err)
		}
	})

	return conn
}
