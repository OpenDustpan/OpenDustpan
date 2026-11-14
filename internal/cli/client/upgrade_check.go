package client

// Upgrade check subcommand: a client of GET /api/dustpan/v1/upgrade-check
// (ADR 39 decision 3).
//
//	opendustpan upgrade check --server https://dustpan.example [--ca-file ca.pem]
//
// The command asks the server for its version, the schema version of its
// database and the names of the applied migrations, compares them with the
// migrations in its own binary and prints the binary version, the server
// version, the schema version of the database, the schema version the binary
// needs, the names of the pending migrations and one verdict. It writes
// nothing: only the server opens the database (ADR 39 decision 1). The exit
// code is 0 for `up to date` and `upgrade possible` and 1 for `cannot start`
// and for errors.
//
// This file is client code: it must not import a store adapter, a blob
// adapter, backup-target code or key-encryption key code (ADR 39 decision 4).
// The depguard rule for internal/cli/client and the gate test in
// cmd/opendustpan fail the build when it does. The shared migration list
// lives in pkg/store/schema, which both dialects and this client import.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// ErrUpgradeVersionMismatch reports a server answer whose schema version the
// applied migration names do not support: the answer is inconsistent, so the
// command trusts neither number.
var ErrUpgradeVersionMismatch = errors.New(
	"server answered a schema version its applied migrations do not match",
)

// Upgrade verdicts of the upgrade check subcommand.
const (
	// VerdictUpToDate reports a database with no pending and no unknown migrations.
	VerdictUpToDate = "up to date"
	// VerdictUpgradePossible reports a database with pending migrations and no unknown versions.
	VerdictUpgradePossible = "upgrade possible"
	// VerdictCannotStart reports a database whose schema minimum binary
	// version is above the version of the binary.
	VerdictCannotStart = "cannot start"
)

// UpgradeCheck runs `opendustpan upgrade check`: it asks the server for its
// version, the schema version of its database, the names of the applied
// migrations and the count of running journaled updates, compares the
// migrations with the migrations in its own binary and prints the binary
// version, the server version, the schema version of the database, the
// schema version the binary needs, the names of the pending migrations, the
// count of running journaled updates and one verdict. It writes nothing. The
// exit code is 0 for `up to date` and `upgrade possible` and 1 for `cannot
// start` and for errors.
// binaryVersion is the stamped version of the binary: the report prints it,
// so it names the same version as --version.
func UpgradeCheck(
	ctx context.Context, args []string, stdout, stderr io.Writer, binaryVersion string,
) error {
	var f upgradeCheckFlags

	fs := flag.NewFlagSet("upgrade check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return ErrUsage
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return fmt.Errorf(
			"%w: set --server (%s) to the server base URL and run opendustpan upgrade check again",
			ErrUpgradeCheckNeedsServer,
			BackupServerEnvVar,
		)
	}

	if err := cli.ByFlag("server").CheckURL(server); err != nil {
		return err
	}

	token, err := BackupToken()
	if err != nil {
		return err
	}

	client, err := newBackupHTTPClient(f.caFile, "run opendustpan upgrade check again", stderr)
	if err != nil {
		return err
	}

	answer, err := readUpgradeCheck(ctx, client, server, token)
	if err != nil {
		return err
	}

	inspection, err := compareUpgradeCheck(answer)
	if err != nil {
		return err
	}

	warning := keyWarningFor(answer)

	report := newUpgradeReport(stdout, answer, binaryVersion, inspection, warning)

	if err := writeUpgradeReport(report); err != nil {
		return err
	}

	if baselineErr := baselineOf(answer); baselineErr != nil {
		return baselineErr
	}

	return startRefusal(inspection, binaryVersion)
}

// startRefusal returns why a binary of version binaryVersion cannot start on
// the database of inspection, or nil when it can: the schema minimum is above
// binaryVersion, or the schema and the binary have diverged.
func startRefusal(inspection schema.Inspection, binaryVersion string) error {
	if err := schema.CheckMinimum(inspection.Minimum, binaryVersion); err != nil {
		return err
	}

	if len(inspection.Unknown) > 0 && len(inspection.Pending) > 0 {
		return fmt.Errorf(
			"%w: the database records migrations %v that this binary does not know, and lacks %d of its migrations",
			schema.ErrSchemaDiverged, inspection.Unknown, len(inspection.Pending),
		)
	}

	return nil
}

// upgradeCheckFlags holds the settings of the upgrade check subcommand: the
// server to ask and the CA bundle to trust it. The token comes from
// DUSTPAN_ADMIN_TOKEN, never from a flag.
type upgradeCheckFlags struct {
	server string
	caFile string
}

func (f *upgradeCheckFlags) register(fs *flag.FlagSet) {
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)
}

// UpgradeCheckAnswer is the server answer the upgrade check compares: the
// server version, the schema version of its database, the minimum binary
// version of its schema ("" when none), the names of the
// applied migrations ordered by version and the count of running journaled
// updates. KeyEncryptionKey is nil when the server is older and does not
// answer the field.
type UpgradeCheckAnswer struct {
	ServerVersion           string   `json:"version"`
	SchemaVersion           int64    `json:"schemaVersion"`
	MinimumBinaryVersion    string   `json:"minimumBinaryVersion"`
	AppliedMigrations       []string `json:"appliedMigrations"`
	KeyEncryptionKey        *bool    `json:"keyEncryptionKey"`
	RunningJournaledUpdates int64    `json:"runningJournaledUpdates"`
}

// KeyWarning is the upgrade report line for a server that runs without a
// key-encryption key, or that is too old to say. This release needs a key
// to encrypt OpenTofu state, so the operator sets one before the upgrade.
const KeyWarning = "warning: this release needs a key-encryption key: " +
	"set DUSTPAN_KEY_ENCRYPTION_KEY or --key-encryption-key-file " +
	"(DUSTPAN_KEY_ENCRYPTION_KEY_FILE). To make a key file, run: " +
	"opendustpan key generate --out <path>"

// DevBinaryWarning is the upgrade report line for a binary with no release
// version. A dev build counts as newer than every release, so the minimum
// binary version of the schema never stops it.
const DevBinaryWarning = "warning: this binary is a dev build. " +
	"It ignores the minimum binary version of the database schema."

// DevServerWarning is the upgrade report line for a server that runs a build
// with no release version, so that a dev build on a production schema shows.
const DevServerWarning = "warning: the server runs a dev build. " +
	"It ignores the minimum binary version of the database schema."

// keyWarningFor returns KeyWarning when the server answer shows no
// key-encryption key (the field is missing on an older server, or false),
// else "".
func keyWarningFor(answer UpgradeCheckAnswer) string {
	if answer.KeyEncryptionKey == nil || !*answer.KeyEncryptionKey {
		return KeyWarning
	}

	return ""
}

// readUpgradeCheck asks the server for its version, schema version and
// applied migrations. It writes nothing. The body is capped, so a hostile or
// broken server cannot grow the client without bound.
func readUpgradeCheck(
	ctx context.Context, client *http.Client, server, token string,
) (UpgradeCheckAnswer, error) {
	var none UpgradeCheckAnswer

	endpoint := backupEndpoint(server, "/api/dustpan/v1/upgrade-check")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return none, fmt.Errorf(
			"read upgrade check: %w: check --server and run opendustpan upgrade check again",
			err,
		)
	}

	backupAuth(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return none, fmt.Errorf(
			"read upgrade check: %w: check --server and run opendustpan upgrade check again",
			err,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK {
		const hint = "check the server log and run opendustpan upgrade check again"

		return none, fmt.Errorf("%w: %s", ErrBackupRead, answerText(resp.StatusCode, decodeErrorAnswer(resp), hint))
	}

	var answer UpgradeCheckAnswer

	if err := decodeCappedJSON(resp.Body, MaxClientBodyBytes, &answer); err != nil {
		return none, fmt.Errorf(
			"%w: %w: check the server log and run opendustpan upgrade check again",
			ErrBackupRead,
			err,
		)
	}

	return answer, nil
}

// compareUpgradeCheck compares the server answer with the migrations the
// binary embeds and returns the inspection the report prints. Pending holds
// the embedded names the database does not record, ordered by version.
// Unknown holds the recorded versions the binary does not embed, ordered.
// The database version is derived from the applied names, never trusted from
// the wire: a server that answers inconsistent version and names fails here.
func compareUpgradeCheck(answer UpgradeCheckAnswer) (schema.Inspection, error) {
	embedded, binaryVersion := schema.EmbeddedMigrationNames()

	appliedVersions := make([]int64, 0, len(answer.AppliedMigrations))
	applied := make(map[int64]bool, len(answer.AppliedMigrations))

	for _, name := range answer.AppliedMigrations {
		version, err := schema.ParseMigrationVersion(name)
		if err != nil {
			return schema.Inspection{}, fmt.Errorf("server applied migration %q: %w", name, err)
		}

		appliedVersions = append(appliedVersions, version)
		applied[version] = true
	}

	databaseVersion := schema.DatabaseVersion(appliedVersions)
	if answer.SchemaVersion != databaseVersion {
		return schema.Inspection{}, fmt.Errorf(
			"%w: answered %d, applied names record %d: check the server log and run opendustpan upgrade check again",
			ErrUpgradeVersionMismatch,
			answer.SchemaVersion,
			databaseVersion,
		)
	}

	if minimum := answer.MinimumBinaryVersion; minimum != "" {
		if err := schema.ValidMinimum(minimum); err != nil {
			return schema.Inspection{}, fmt.Errorf(
				"server minimum binary version: %w: check the server log and run opendustpan upgrade check again", err,
			)
		}
	}

	embeddedSet := make(map[int64]bool, len(embedded))

	var pending []string

	for _, name := range embedded {
		version, err := schema.ParseMigrationVersion(name)
		if err != nil {
			return schema.Inspection{}, fmt.Errorf("embedded migration %q: %w", name, err)
		}

		embeddedSet[version] = true

		if !applied[version] {
			pending = append(pending, name)
		}
	}

	var unknown []int64

	for _, version := range appliedVersions {
		if !embeddedSet[version] {
			unknown = append(unknown, version)
		}
	}

	return schema.Inspection{
		DatabaseVersion: databaseVersion,
		BinaryVersion:   binaryVersion,
		Pending:         pending,
		Unknown:         unknown,
		Minimum:         answer.MinimumBinaryVersion,
	}, nil
}

// baselineOf returns the refusal for a server whose database records a
// migration from before the baseline, or nil. compareUpgradeCheck already
// refused a name without a version, so a name that does not parse here is
// skipped.
func baselineOf(answer UpgradeCheckAnswer) error {
	versions := make([]int64, 0, len(answer.AppliedMigrations))

	for _, name := range answer.AppliedMigrations {
		if version, err := schema.ParseMigrationVersion(name); err == nil {
			versions = append(versions, version)
		}
	}

	if err := schema.CheckBaseline(versions); err != nil {
		return fmt.Errorf("server database: %w", err)
	}

	return nil
}

// upgradeVerdict returns the one verdict for inspection and a binary of
// version binaryVersion: `cannot start` when the schema minimum binary
// version is above binaryVersion, `upgrade possible` when migrations are
// pending, else `up to date`. A database that records migrations the binary
// does not embed is `up to date` when the binary is at or above the minimum:
// the binary starts on it and applies nothing. A database that also lacks
// migrations of the binary has diverged from it: `cannot start`.
func upgradeVerdict(inspection schema.Inspection, binaryVersion string) string {
	switch {
	case schema.CheckMinimum(inspection.Minimum, binaryVersion) != nil:
		return VerdictCannotStart
	case len(inspection.Unknown) > 0 && len(inspection.Pending) > 0:
		return VerdictCannotStart
	case len(inspection.Unknown) > 0:
		return VerdictUpToDate
	case len(inspection.Pending) > 0:
		return VerdictUpgradePossible
	default:
		return VerdictUpToDate
	}
}

// upgradeReport holds the inputs of the upgrade report: where to write it,
// the server and binary versions, the migration inspection, the key warning
// (empty means none) and the count of running journaled updates.
type upgradeReport struct {
	stdout        io.Writer
	serverVersion string
	binaryVersion string
	inspection    schema.Inspection
	warning       string
	journaled     int64
}

// newUpgradeReport gathers the inputs of the upgrade report: where to
// write it, the server answer, the version of the binary, the migration
// inspection and the key warning (empty means none).
func newUpgradeReport(
	stdout io.Writer,
	answer UpgradeCheckAnswer,
	binaryVersion string,
	inspection schema.Inspection,
	warning string,
) upgradeReport {
	return upgradeReport{
		stdout:        stdout,
		serverVersion: answer.ServerVersion,
		binaryVersion: binaryVersion,
		inspection:    inspection,
		warning:       warning,
		journaled:     answer.RunningJournaledUpdates,
	}
}

// writeUpgradeReport writes the binary version, the server version, the
// schema version of the database, the schema version the binary needs, the
// pending migration names, the count of running journaled updates, the key
// warning when warning is not empty, and the verdict. When migrations are
// pending it tells the operator to back up the database first and that no
// downgrade path exists. When journaled updates run it tells the operator to
// let them finish or cancel them before a downgrade: a downgrade to a binary
// without the journal route answers their journal batches with 404, which
// the Pulumi CLI does not retry.
func writeUpgradeReport(report upgradeReport) error {
	lines := []string{
		"binary version: " + report.binaryVersion,
		"server version: " + report.serverVersion,
		fmt.Sprintf("database schema version: %d", report.inspection.DatabaseVersion),
		fmt.Sprintf("binary schema version: %d", report.inspection.BinaryVersion),
		"schema minimum binary version: " + minimumText(report.inspection.Minimum),
	}

	if n := len(report.inspection.Unknown); n > 0 {
		lines = append(lines, fmt.Sprintf(
			"Migrations in the database that this binary does not know: %d. "+
				"A binary at or above the schema minimum starts on it and applies nothing.", n,
		))
	}

	if len(report.inspection.Pending) == 0 {
		lines = append(lines, "pending migrations: none")
	} else {
		lines = append(lines, "pending migrations:")
		for _, name := range report.inspection.Pending {
			lines = append(lines, "  "+name)
		}

		lines = append(
			lines,
			"Back up the database before the upgrade. No downgrade path exists.",
		)
	}

	lines = append(lines, fmt.Sprintf("running journaled updates: %d", report.journaled))

	if report.journaled > 0 {
		lines = append(
			lines,
			"Let running journaled updates finish or cancel them before a downgrade.",
		)
	}

	if report.warning != "" {
		lines = append(lines, report.warning)
	}

	if schema.IsDevBuild(report.binaryVersion) {
		lines = append(lines, DevBinaryWarning)
	}

	if schema.IsDevBuild(report.serverVersion) {
		lines = append(lines, DevServerWarning)
	}

	lines = append(lines, "verdict: "+upgradeVerdict(report.inspection, report.binaryVersion))

	for _, line := range lines {
		if _, err := fmt.Fprintln(report.stdout, line); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}

// minimumText returns minimum, or "none" when the schema records none.
func minimumText(minimum string) string {
	if minimum == "" {
		return "none"
	}

	return minimum
}
