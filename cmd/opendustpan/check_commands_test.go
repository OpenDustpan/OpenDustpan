package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// checkBearer is the Bearer credential the fake check servers expect.
const checkBearer = "test-token"

// checkServerVersion is the server version the fake upgrade check answers.
const checkServerVersion = "9.9.9"

// checkFakeServer answers one installation API route from a scripted body
// and status. It checks the Bearer credential on every request and records
// the requests it saw, so a test proves the command wrote nothing.
type checkFakeServer struct {
	server   *httptest.Server
	requests atomic.Int64
	status   int
	body     string
}

// newCheckFakeServer starts the fake server for a GET of path, answering
// status with body and expecting checkBearer as the Bearer credential.
func newCheckFakeServer(t *testing.T, path string, status int, body string) *checkFakeServer {
	t.Helper()

	fake := &checkFakeServer{server: nil, requests: atomic.Int64{}, status: status, body: body}

	mux := http.NewServeMux()
	mux.HandleFunc(http.MethodGet+" "+path, func(w http.ResponseWriter, r *http.Request) {
		fake.requests.Add(1)

		if r.Header.Get("Authorization") != "Bearer "+checkBearer {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)

			if err := json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "unauthenticated"}); err != nil {
				t.Errorf("write 401 body: %v", err)
			}

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fake.status)

		if _, err := w.Write([]byte(fake.body)); err != nil {
			t.Errorf("write check body: %v", err)
		}
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// upgradeCheckBody encodes the upgrade check answer the fake server sends:
// checkServerVersion with the schema version of the applied names.
func upgradeCheckBody(t *testing.T, applied []string) string {
	t.Helper()

	return upgradeCheckBodyWithMinimum(t, applied, "")
}

// upgradeCheckBodyWithMinimum is upgradeCheckBody with the minimum binary
// version of the schema.
func upgradeCheckBodyWithMinimum(t *testing.T, applied []string, minimum string) string {
	t.Helper()

	versions := make([]int64, 0, len(applied))

	for _, name := range applied {
		version, err := schema.ParseMigrationVersion(name)
		if err != nil {
			t.Fatalf("parse applied migration %q: %v", name, err)
		}

		versions = append(versions, version)
	}

	body, err := json.Marshal(upgradeCheckAnswer{
		ServerVersion:           checkServerVersion,
		SchemaVersion:           schema.DatabaseVersion(versions),
		MinimumBinaryVersion:    minimum,
		AppliedMigrations:       applied,
		KeyEncryptionKey:        nil,
		RunningJournaledUpdates: 0,
	})
	if err != nil {
		t.Fatalf("encode upgrade check answer: %v", err)
	}

	return string(body)
}

// runUpgradeCheckCommand runs `upgrade check` against server with
// checkBearer in DUSTPAN_ADMIN_TOKEN and returns its stdout and error. A nil
// error means exit code 0, a non-nil error exit code 1.
func runUpgradeCheckCommand(t *testing.T, server string) (string, error) {
	t.Helper()
	t.Setenv(adminTokenEnvVar, checkBearer)

	var stdout, stderr bytes.Buffer

	err := run(t.Context(), []string{"upgrade", "check", "--server", server}, &stdout, &stderr)

	return stdout.String(), err
}

// wantUpgradeReport holds the expected report of one upgrade check run: the
// server version, the database and binary schema versions, whether pending
// migrations are listed, and the verdict.
type wantUpgradeReport struct {
	serverVersion string
	dbVersion     int64
	binVersion    int64
	pending       bool
	verdict       string
}

// checkUpgradeReport proves that out names the binary version, the server
// version, the database and binary schema versions, the pending state and
// the verdict.
func checkUpgradeReport(t *testing.T, out string, want wantUpgradeReport) {
	t.Helper()

	for _, line := range []string{
		"binary version: " + version,
		"server version: " + want.serverVersion,
		"database schema version: " + strconv.FormatInt(want.dbVersion, 10),
		"binary schema version: " + strconv.FormatInt(want.binVersion, 10),
		"verdict: " + want.verdict,
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s", line, out)
		}
	}

	if want.pending {
		if !strings.Contains(out, "pending migrations:") || !strings.Contains(out, ".sql") {
			t.Fatalf("upgrade check output lists no pending migration:\n%s", out)
		}

		for _, line := range []string{
			"Back up the database before the upgrade.",
			"No downgrade path exists.",
		} {
			if !strings.Contains(out, line) {
				t.Fatalf("upgrade check output lacks %q:\n%s", line, out)
			}
		}
	} else if !strings.Contains(out, "pending migrations: none") {
		t.Fatalf("upgrade check output lacks `pending migrations: none`:\n%s", out)
	}
}

// embeddedNames returns the shared embedded migration names with the newest
// version.
func embeddedNames(t *testing.T) ([]string, int64) {
	t.Helper()

	names, newest := schema.EmbeddedMigrationNames()

	if len(names) == 0 || newest <= 0 {
		t.Fatal("shared embedded migrations hold no name, want the binary list")
	}

	return names, newest
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckFresh(t *testing.T) {
	names, binVersion := embeddedNames(t)
	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check",
		http.StatusOK, upgradeCheckBody(t, nil))

	out, err := runUpgradeCheckCommand(t, fake.server.URL)
	if err != nil {
		t.Fatalf("upgrade check on a fresh database: %v", err)
	}

	checkUpgradeReport(t, out, wantUpgradeReport{
		serverVersion: checkServerVersion,
		dbVersion:     0,
		binVersion:    binVersion,
		pending:       true,
		verdict:       verdictUpgradePossible,
	})

	if !strings.Contains(out, names[0]) {
		t.Fatalf("upgrade check output lacks pending migration %q:\n%s", names[0], out)
	}

	if got := fake.requests.Load(); got != 1 {
		t.Fatalf("server saw %d requests, want exactly one read that writes nothing", got)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckBeforeBaseline(t *testing.T) {
	_, binVersion := embeddedNames(t)

	const oldVersion = 76

	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check",
		http.StatusOK, upgradeCheckBody(t, []string{"0075_sign_in_freshness.sql", "0076_sign_in_providers.sql"}))

	out, err := runUpgradeCheckCommand(t, fake.server.URL)
	if err == nil || !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		t.Fatalf("upgrade check on a database from before the baseline: err = %v, want ErrSchemaBeforeBaseline", err)
	}

	checkUpgradeReport(t, out, wantUpgradeReport{
		serverVersion: checkServerVersion,
		dbVersion:     oldVersion,
		binVersion:    binVersion,
		pending:       true,
		verdict:       verdictCannotStart,
	})

	for _, want := range []string{strconv.FormatInt(oldVersion, 10), strconv.FormatInt(schema.BaselineVersion, 10)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("upgrade check error %q does not name %q", err, want)
		}
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckCurrent(t *testing.T) {
	names, binVersion := embeddedNames(t)
	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check",
		http.StatusOK, upgradeCheckBody(t, names))

	out, err := runUpgradeCheckCommand(t, fake.server.URL)
	if err != nil {
		t.Fatalf("upgrade check on an up-to-date database: %v", err)
	}

	checkUpgradeReport(t, out, wantUpgradeReport{
		serverVersion: checkServerVersion,
		dbVersion:     binVersion,
		binVersion:    binVersion,
		pending:       false,
		verdict:       verdictUpToDate,
	})
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckNewerAtMinimum(t *testing.T) {
	names, binVersion := embeddedNames(t)
	setBinaryVersion(t, "v0.2.0")

	newer := append(slices.Clone(names), fmt.Sprintf("%04d_future.sql", binVersion+1))
	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check",
		http.StatusOK, upgradeCheckBodyWithMinimum(t, newer, "v0.2.0"))

	out, err := runUpgradeCheckCommand(t, fake.server.URL)
	if err != nil {
		t.Fatalf("upgrade check on a newer database at the minimum: %v", err)
	}

	checkUpgradeReport(t, out, wantUpgradeReport{
		serverVersion: checkServerVersion,
		dbVersion:     binVersion + 1,
		binVersion:    binVersion,
		pending:       false,
		verdict:       verdictUpToDate,
	})

	for _, line := range []string{
		"schema minimum binary version: v0.2.0",
		"Migrations in the database that this binary does not know: 1.",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s", line, out)
		}
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckBelowMinimum(t *testing.T) {
	names, binVersion := embeddedNames(t)
	setBinaryVersion(t, "v0.1.0")

	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check",
		http.StatusOK, upgradeCheckBodyWithMinimum(t, names, "v0.2.0"))

	out, err := runUpgradeCheckCommand(t, fake.server.URL)
	if err == nil || !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("upgrade check below the schema minimum: err = %v, want ErrBinaryBelowMinimum", err)
	}

	checkUpgradeReport(t, out, wantUpgradeReport{
		serverVersion: checkServerVersion,
		dbVersion:     binVersion,
		binVersion:    binVersion,
		pending:       false,
		verdict:       verdictCannotStart,
	})

	for _, want := range []string{"v0.1.0", "v0.2.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("upgrade check error %q does not name %q", err, want)
		}
	}

	for _, warning := range []string{devBinaryWarning, devServerWarning} {
		if strings.Contains(out, warning) {
			t.Fatalf("upgrade check output of release builds holds %q:\n%s", warning, out)
		}
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckDevBuildIgnoresMinimum(t *testing.T) {
	names, binVersion := embeddedNames(t)

	body, err := json.Marshal(upgradeCheckAnswer{
		ServerVersion: "dev", SchemaVersion: binVersion, MinimumBinaryVersion: "v9.0.0", AppliedMigrations: names,
		KeyEncryptionKey: nil, RunningJournaledUpdates: 0,
	})
	if err != nil {
		t.Fatalf("encode upgrade check answer: %v", err)
	}

	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check", http.StatusOK, string(body))

	out, err := runUpgradeCheckCommand(t, fake.server.URL)
	if err != nil {
		t.Fatalf("upgrade check of a dev build: %v", err)
	}

	checkUpgradeReport(t, out, wantUpgradeReport{
		serverVersion: "dev",
		dbVersion:     binVersion,
		binVersion:    binVersion,
		pending:       false,
		verdict:       verdictUpToDate,
	})

	for _, warning := range []string{devBinaryWarning, devServerWarning} {
		if !strings.Contains(out, warning) {
			t.Fatalf("upgrade check output of dev builds lacks %q:\n%s", warning, out)
		}
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckVersionMismatch(t *testing.T) {
	body, err := json.Marshal(upgradeCheckAnswer{
		ServerVersion:           checkServerVersion,
		SchemaVersion:           99,
		MinimumBinaryVersion:    "",
		AppliedMigrations:       nil,
		KeyEncryptionKey:        nil,
		RunningJournaledUpdates: 0,
	})
	if err != nil {
		t.Fatalf("encode mismatched answer: %v", err)
	}

	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check",
		http.StatusOK, string(body))

	_, err = runUpgradeCheckCommand(t, fake.server.URL)
	if err == nil || !errors.Is(err, errUpgradeVersionMismatch) {
		t.Fatalf("upgrade check on a mismatched answer: err = %v, want errUpgradeVersionMismatch", err)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckOversizedBody(t *testing.T) {
	fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check",
		http.StatusOK, strings.Repeat("x", maxClientBodyBytes+10))

	_, err := runUpgradeCheckCommand(t, fake.server.URL)
	if err == nil || !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("upgrade check on an oversized body: err = %v, want errResponseTooLarge", err)
	}
}

func TestUpgradeCheckUsage(t *testing.T) {
	t.Parallel()

	if !strings.Contains(errUsage.Error(), "upgrade check") {
		t.Fatalf("usage = %q, want it to name upgrade check", errUsage.Error())
	}

	cases := []struct {
		name string
		args []string
		want error
	}{
		{"upgrade without check", []string{"upgrade"}, errUsage},
		{"upgrade check with a name", []string{"upgrade", "check", "--server", "http://x", "extra"}, errUsage},
		{
			"upgrade check without a server",
			[]string{"upgrade", "check"},
			errUpgradeCheckNeedsServer,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			if err := run(t.Context(), tc.args, &stdout, &stderr); !errors.Is(err, tc.want) {
				t.Fatalf("run %v: err = %v, want %v", tc.args, err, tc.want)
			}
		})
	}
}

// runBackupCheckCommand runs `backup check` for id against server with
// checkBearer in DUSTPAN_ADMIN_TOKEN and returns its stdout and error. A nil
// error means exit code 0, a non-nil error exit code 1.
func runBackupCheckCommand(t *testing.T, server, id string) (string, error) {
	t.Helper()
	t.Setenv(adminTokenEnvVar, checkBearer)

	var stdout, stderr bytes.Buffer

	err := run(t.Context(), []string{"backup", "check", "--server", server, "--backup", id}, &stdout, &stderr)

	return stdout.String(), err
}

// backupCheckBody encodes the backup check answer the fake server sends.
func backupCheckBody(t *testing.T, id string, complete bool, checkErr string) string {
	t.Helper()

	body, err := json.Marshal(checkBackupAnswer{ID: id, Complete: complete, Error: checkErr})
	if err != nil {
		t.Fatalf("encode backup check answer: %v", err)
	}

	return string(body)
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCheckComplete(t *testing.T) {
	const id = "bkp-test00000000000001"

	fake := newCheckFakeServer(t, "/api/server/backups/"+id+"/check",
		http.StatusOK, backupCheckBody(t, id, true, ""))

	out, err := runBackupCheckCommand(t, fake.server.URL, id)
	if err != nil {
		t.Fatalf("backup check of a complete backup: %v", err)
	}

	if !strings.Contains(out, "backup "+id+" is complete") {
		t.Fatalf("backup check output lacks the verdict:\n%s", out)
	}

	if got := fake.requests.Load(); got != 1 {
		t.Fatalf("server saw %d requests, want exactly one read that writes nothing", got)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCheckCorrupt(t *testing.T) {
	const id = "bkp-test00000000000001"
	const missing = "blobs/tofu/ws1/sv1/a1"

	fake := newCheckFakeServer(t, "/api/server/backups/"+id+"/check",
		http.StatusOK, backupCheckBody(t, id, false, "object "+missing+" is missing"))

	_, err := runBackupCheckCommand(t, fake.server.URL, id)
	if err == nil {
		t.Fatal("backup check of a corrupt backup succeeded, want an error")
	}

	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("backup check after the delete: err = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("backup check error = %q, want it to name %q", err, missing)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCheckUnknown(t *testing.T) {
	const id = "bkp-missing000000000001"

	fake := newCheckFakeServer(t, "/api/server/backups/"+id+"/check",
		http.StatusNotFound, `{"code":404,"message":"backup not found"}`)

	_, err := runBackupCheckCommand(t, fake.server.URL, id)
	if err == nil || !errors.Is(err, errBackupRead) {
		t.Fatalf("backup check of an unknown backup: err = %v, want errBackupRead", err)
	}

	if !strings.Contains(err.Error(), "backup not found") {
		t.Fatalf("backup check error = %q, want it to name backup not found", err)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCheckOversizedBody(t *testing.T) {
	const id = "bkp-test00000000000001"

	fake := newCheckFakeServer(t, "/api/server/backups/"+id+"/check",
		http.StatusOK, strings.Repeat("x", maxClientBodyBytes+10))

	_, err := runBackupCheckCommand(t, fake.server.URL, id)
	if err == nil || !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("backup check on an oversized body: err = %v, want errResponseTooLarge", err)
	}
}

func TestBackupCheckUsage(t *testing.T) {
	t.Parallel()

	if !strings.Contains(errUsage.Error(), "backup check") {
		t.Fatalf("usage = %q, want it to name backup check", errUsage.Error())
	}

	cases := []struct {
		name string
		args []string
		want error
	}{
		{"unknown backup subcommand", []string{"backup", "no-such"}, errUsage},
		{"check without an id", []string{"backup", "check", "--server", "http://x"}, errBackupID},
		{
			"check with an invalid id",
			[]string{"backup", "check", "--server", "http://x", "--backup", "no"},
			backup.ErrInvalidID,
		},
		{
			"check without a server",
			[]string{"backup", "check", "--backup", "bkp-test00000000000001"},
			errBackupCheckNeedsServer,
		},
		{
			"check with a positional argument",
			[]string{"backup", "check", "--server", "http://x", "--backup", "bkp-test00000000000001", "extra"},
			errUsage,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			if err := run(t.Context(), tc.args, &stdout, &stderr); !errors.Is(err, tc.want) {
				t.Fatalf("run %v: err = %v, want %v", tc.args, err, tc.want)
			}
		})
	}
}

// clientSourceFiles holds the client command files that must stay clients of
// the REST API: they take --server and never open the database, a blob adapter,
// a backup target or the key-encryption key (ADR 39 decision 4). The serve
// wiring in main.go may import those packages; these files must not. The
// paths are relative to cmd/opendustpan.
//
//nolint:gochecknoglobals // The gate test reads one fixed file list; passing it as a parameter adds nothing.
var clientSourceFiles = []string{
	"../../internal/cli/client/backup.go",
	"../../internal/cli/client/backup_check.go",
	"../../internal/cli/client/namespace.go",
	"../../internal/cli/client/restore.go",
	"../../internal/cli/client/upgrade_check.go",
	"../../internal/cli/client/health.go",
	"../../internal/cli/client/admintoken.go",
	"../../internal/cli/client/client.go",
}

// clientBannedImports holds the import prefixes no client source file may
// use. pkg/store/schema stays allowed: it is the neutral migration list both
// dialects and the clients share, with no driver in it.
//
//nolint:gochecknoglobals // The gate test reads one fixed prefix list; passing it as a parameter adds nothing.
var clientBannedImports = []string{
	"opendustpan.org/opendustpan/pkg/store/sqlite",
	"opendustpan.org/opendustpan/pkg/store/postgres",
	"opendustpan.org/opendustpan/pkg/store/internal",
	"opendustpan.org/opendustpan/pkg/blob/fs",
	"opendustpan.org/opendustpan/pkg/blob/s3",
	"opendustpan.org/opendustpan/pkg/secret",
}

// TestClientCommandsImportNoStore proves the client commands take --server
// and never reach the database: no client source file imports a store
// adapter, a blob adapter, backup-target code or key-encryption key code
// (ADR 39 decision 4). Only the serve wiring may import them.
func TestClientCommandsImportNoStore(t *testing.T) {
	t.Parallel()

	for _, file := range clientSourceFiles {
		checkClientFileImports(t, file)
	}
}

// checkClientFileImports proves one client source file imports no store
// adapter, blob adapter, backup-target code or key-encryption key code.
func checkClientFileImports(t *testing.T, file string) {
	t.Helper()

	node, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	for _, spec := range node.Imports {
		path := strings.Trim(spec.Path.Value, `"`)

		for _, banned := range clientBannedImports {
			if path == banned || strings.HasPrefix(path, banned+"/") {
				t.Errorf("client file %s imports %s, want only the REST client", file, path)
			}
		}

		if strings.HasPrefix(path, "opendustpan.org/opendustpan/pkg/store/") &&
			path != "opendustpan.org/opendustpan/pkg/store/schema" {
			t.Errorf("client file %s imports %s, want only pkg/store/schema", file, path)
		}
	}
}

// TestClientCommandsOpenNoStore proves no command other than serve opens a
// store: no client source file calls a store, blob adapter, target or key
// constructor. The test fails when a non-serve command opens a store, as ADR
// 39 decision 4 orders.
func TestClientCommandsOpenNoStore(t *testing.T) {
	t.Parallel()

	banned := map[string]bool{
		"sqlite":   true,
		"postgres": true,
		"blobfs":   true,
		"blobs3":   true,
		"secret":   true,
	}

	for _, file := range clientSourceFiles {
		node, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}

		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			id, ok := sel.X.(*ast.Ident)
			if !ok || !banned[id.Name] {
				return true
			}

			t.Errorf("client file %s uses %s.%s, want the REST client only", file, id.Name, sel.Sel.Name)

			return true
		})
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestUpgradeCheckKeyWarning(t *testing.T) {
	names, binVersion := embeddedNames(t)

	keyed := true
	unkeyed := false

	for _, tc := range []struct {
		name string
		key  *bool
		want bool
	}{
		{name: "server older than ADR 41", key: nil, want: true},
		{name: "server without a key", key: &unkeyed, want: true},
		{name: "server with a key", key: &keyed, want: false},
	} {
		body, err := json.Marshal(upgradeCheckAnswer{
			ServerVersion:           checkServerVersion,
			SchemaVersion:           binVersion,
			MinimumBinaryVersion:    "",
			AppliedMigrations:       names,
			KeyEncryptionKey:        tc.key,
			RunningJournaledUpdates: 0,
		})
		if err != nil {
			t.Fatalf("%s: encode upgrade check answer: %v", tc.name, err)
		}

		fake := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check", http.StatusOK, string(body))

		out, runErr := runUpgradeCheckCommand(t, fake.server.URL)
		if runErr != nil {
			t.Fatalf("%s: upgrade check: %v", tc.name, runErr)
		}

		if got := strings.Contains(out, keyWarning); got != tc.want {
			t.Fatalf("%s: output holds the key warning = %t, want %t:\n%s", tc.name, got, tc.want, out)
		}

		if !tc.want {
			continue
		}

		for _, name := range []string{
			"opendustpan key generate --out",
			"--key-encryption-key-file",
			keyFileEnvVar,
			keyEnvVar,
		} {
			if !strings.Contains(out, name) {
				t.Fatalf("%s: key warning lacks %q:\n%s", tc.name, name, out)
			}
		}
	}
}

// TestCheckCommandsNameThemselvesWithoutServer proves that upgrade check and
// backup check, run without --server, name their own command first.
func TestCheckCommandsNameThemselvesWithoutServer(t *testing.T) {
	t.Setenv(backupServerEnvVar, "")

	for _, tc := range []struct {
		want string
		args []string
	}{
		{args: []string{"upgrade", "check"}, want: "upgrade check needs --server: "},
		{args: []string{"backup", "check", "--backup", "bkp-test00000000000001"}, want: "backup check needs --server: "},
	} {
		var stdout, stderr bytes.Buffer

		err := run(t.Context(), tc.args, &stdout, &stderr)
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("run %v: err = %v, want a text that starts with %q", tc.args, err, tc.want)
		}
	}
}
