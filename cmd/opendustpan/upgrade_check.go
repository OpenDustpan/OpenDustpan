package main

// Upgrade check subcommand: a client of GET /api/server/upgrade-check
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
// client_gate_test.go and the depguard client rule fail the build when it
// does. The shared migration list lives in pkg/store/schema, which both
// dialects and this client import.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/store/schema"
)

// errUpgradeVersionMismatch reports a server answer whose schema version the
// applied migration names do not support: the answer is inconsistent, so the
// command trusts neither number.
var errUpgradeVersionMismatch = errors.New(
	"server answered a schema version its applied migrations do not match",
)

// Upgrade verdicts of the upgrade check subcommand.
const (
	// verdictUpToDate reports a database with no pending and no unknown migrations.
	verdictUpToDate = "up to date"
	// verdictUpgradePossible reports a database with pending migrations and no unknown versions.
	verdictUpgradePossible = "upgrade possible"
	// verdictCannotStart reports a database with a version the binary does not embed.
	verdictCannotStart = "cannot start"
)

// upgradeCheck runs `opendustpan upgrade check`: it asks the server for its
// version, the schema version of its database and the names of the applied
// migrations, compares them with the migrations in its own binary and prints
// the binary version, the server version, the schema version of the database,
// the schema version the binary needs, the names of the pending migrations
// and one verdict. It writes nothing. The exit code is 0 for `up to date`
// and `upgrade possible` and 1 for `cannot start` and for errors.
func upgradeCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var f upgradeCheckFlags

	fs := flag.NewFlagSet("upgrade check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return errUsage
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return fmt.Errorf(
			"%w: set --server (%s) to the server base URL and run opendustpan upgrade check again",
			errBackupNeedsServer,
			backupServerEnvVar,
		)
	}

	token, err := backupToken()
	if err != nil {
		return err
	}

	client, err := newBackupHTTPClient(f.caFile, "run opendustpan upgrade check again")
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

	if err := writeUpgradeReport(stdout, answer.ServerVersion, inspection, keyWarningFor(answer)); err != nil {
		return err
	}

	if len(inspection.Unknown) > 0 {
		return withBinaryVersion(&schema.NewerError{
			DatabaseVersion: inspection.DatabaseVersion,
			BinaryVersion:   inspection.BinaryVersion,
		})
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
	fs.StringVar(
		&f.server,
		"server",
		env(backupServerEnvVar, ""),
		"base URL of the server, for example https://dustpan.example (DUSTPAN_SERVER)",
	)
	fs.StringVar(
		&f.caFile,
		"ca-file",
		"",
		"PEM bundle of extra CA certificates to trust with the system store",
	)
}

// upgradeCheckAnswer is the server answer the upgrade check compares: the
// server version, the schema version of its database and the names of the
// applied migrations ordered by version. KeyEncryptionKey is nil when the
// server is older than ADR 41 and does not answer the field.
type upgradeCheckAnswer struct {
	ServerVersion     string   `json:"version"`
	SchemaVersion     int64    `json:"schemaVersion"`
	AppliedMigrations []string `json:"appliedMigrations"`
	KeyEncryptionKey  *bool    `json:"keyEncryptionKey"`
}

// keyWarning is the upgrade report line for a server that runs without a
// key-encryption key, or that is too old to say. This release needs a key
// (ADR 41 decision 12), so the operator sets one before the upgrade.
const keyWarning = "warning: this release needs a key-encryption key (ADR 41): " +
	"set DUSTPAN_KEY_ENCRYPTION_KEY or --key-encryption-key-file " +
	"(DUSTPAN_KEY_ENCRYPTION_KEY_FILE), or start serve once with " +
	"--generate-key-encryption-key (DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true)"

// keyWarningFor returns keyWarning when the server answer shows no
// key-encryption key (the field is missing on an older server, or false),
// else "".
func keyWarningFor(answer upgradeCheckAnswer) string {
	if answer.KeyEncryptionKey == nil || !*answer.KeyEncryptionKey {
		return keyWarning
	}

	return ""
}

// readUpgradeCheck asks the server for its version, schema version and
// applied migrations. It writes nothing. The body is capped, so a hostile or
// broken server cannot grow the client without bound.
func readUpgradeCheck(
	ctx context.Context, client *http.Client, server, token string,
) (upgradeCheckAnswer, error) {
	var none upgradeCheckAnswer

	endpoint := backupEndpoint(server, "/api/server/upgrade-check")

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
		return none, fmt.Errorf(
			"%w: %s: check the server log and run opendustpan upgrade check again",
			errBackupRead,
			decodeBackupError(resp),
		)
	}

	var answer upgradeCheckAnswer

	if err := decodeCappedJSON(resp.Body, &answer); err != nil {
		return none, fmt.Errorf(
			"%w: %w: check the server log and run opendustpan upgrade check again",
			errBackupRead,
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
func compareUpgradeCheck(answer upgradeCheckAnswer) (schema.Inspection, error) {
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
			errUpgradeVersionMismatch,
			answer.SchemaVersion,
			databaseVersion,
		)
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
	}, nil
}

// upgradeVerdict returns the one verdict for inspection: `cannot start` when
// the database records a version the binary does not embed, `upgrade
// possible` when migrations are pending, else `up to date`.
func upgradeVerdict(inspection schema.Inspection) string {
	switch {
	case len(inspection.Unknown) > 0:
		return verdictCannotStart
	case len(inspection.Pending) > 0:
		return verdictUpgradePossible
	default:
		return verdictUpToDate
	}
}

// writeUpgradeReport writes the binary version, the server version, the
// schema version of the database, the schema version the binary needs, the
// pending migration names, the key warning when warning is not empty, and
// the verdict. When migrations are pending it tells the operator to back up the
// database first and that no downgrade path exists.
func writeUpgradeReport(
	stdout io.Writer, serverVersion string, inspection schema.Inspection, warning string,
) error {
	lines := []string{
		"binary version: " + version,
		"server version: " + serverVersion,
		fmt.Sprintf("database schema version: %d", inspection.DatabaseVersion),
		fmt.Sprintf("binary schema version: %d", inspection.BinaryVersion),
	}

	if len(inspection.Pending) == 0 {
		lines = append(lines, "pending migrations: none")
	} else {
		lines = append(lines, "pending migrations:")
		for _, name := range inspection.Pending {
			lines = append(lines, "  "+name)
		}

		lines = append(
			lines,
			"Back up the database before the upgrade. No downgrade path exists.",
		)
	}

	if warning != "" {
		lines = append(lines, warning)
	}

	lines = append(lines, "verdict: "+upgradeVerdict(inspection))

	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}
