package client

// Backup check subcommand: a client of GET /api/server/backups/{id}/check
// (ADR 39 decision 3).
//
//	opendustpan backup check --server https://dustpan.example --backup <id> [--ca-file ca.pem]
//
// The command asks the server to check the backup in its configured target
// and prints one verdict. It opens no database and reads no target or key:
// the server reads the target (ADR 39 decision 1). It exits 0 when the
// backup is complete, else it returns the first missing or short object by
// key with exit 1.
//
// This file is client code: it must not import a store adapter, a blob
// adapter, backup-target code or key-encryption key code (ADR 39 decision 4).
// The depguard rule for internal/cli/client and the gate test in
// cmd/opendustpan fail the build when it does.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/backup"
)

// ErrBackupID reports a backup check without a backup ID. It names the
// flag, so the operator knows what to set.
var ErrBackupID = errors.New("backup id is missing: set --backup <id>")

// backupCheckFlags holds the settings of the backup check subcommand: the
// server to ask and the backup ID to check. The token comes from
// DUSTPAN_ADMIN_TOKEN, never from a flag. The target is a server setting,
// so the command sends no target.
type backupCheckFlags struct {
	server string
	caFile string
	id     string
}

func (f *backupCheckFlags) register(fs *flag.FlagSet) {
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)
	cli.ByFlag("backup").String(fs, &f.id)
}

// CheckBackupAnswer is the server answer the backup check reads: whether the
// configured target holds every object the manifest names with the recorded
// size, with the first missing or short object in Error.
type CheckBackupAnswer struct {
	ID       string `json:"id"`
	Complete bool   `json:"complete"`
	Error    string `json:"error"`
}

// BackupCheck runs `opendustpan backup check --server <url> --backup <id>`:
// it asks the server to check the backup in its configured target and prints
// one verdict. It opens no database and reads no target or key. It exits 0
// when the backup is complete, else it returns the first missing or short
// object by key with exit 1.
func BackupCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var f backupCheckFlags

	fs := flag.NewFlagSet("backup check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return ErrUsage
	}

	id := strings.TrimSpace(f.id)
	if id == "" {
		return ErrBackupID
	}

	if _, err := backup.ParseID(id); err != nil {
		return fmt.Errorf("%w: set --backup to a backup ID like bkp-aaa: %w", ErrBackupID, err)
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return fmt.Errorf(
			"%w: set --server (%s) to the server base URL and run opendustpan backup check again",
			ErrBackupNeedsServer,
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

	client, err := newBackupHTTPClient(f.caFile, "run opendustpan backup check again", stderr)
	if err != nil {
		return err
	}

	answer, err := readBackupCheck(ctx, client, server, token, id)
	if err != nil {
		return err
	}

	if !answer.Complete {
		return fmt.Errorf("%w: %s", backup.ErrCorrupt, answer.Error)
	}

	if _, err := fmt.Fprintf(stdout, "backup %s is complete\n", id); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// readBackupCheck asks the server to check the backup in its configured
// target and answers the result. The server reads the target, not the
// command. The ID travels escaped, and the body is capped, so a hostile or
// broken server cannot grow the client without bound.
func readBackupCheck(
	ctx context.Context, client *http.Client, server, token, id string,
) (CheckBackupAnswer, error) {
	var none CheckBackupAnswer

	endpoint := backupEndpoint(server, "/api/server/backups/"+url.PathEscape(id)+"/check")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return none, fmt.Errorf(
			"%w %s: %w: check --server and run opendustpan backup check again",
			ErrBackupRead,
			id,
			err,
		)
	}

	backupAuth(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return none, fmt.Errorf(
			"%w %s: %w: check --server and run opendustpan backup check again",
			ErrBackupRead,
			id,
			err,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK {
		return none, fmt.Errorf(
			"%w %s: %s: check the server log and run opendustpan backup check again",
			ErrBackupRead,
			id,
			decodeBackupError(resp),
		)
	}

	var answer CheckBackupAnswer

	if err := decodeCappedJSON(resp.Body, &answer); err != nil {
		return none, fmt.Errorf(
			"%w %s: %w: check the server log and run opendustpan backup check again",
			ErrBackupRead,
			id,
			err,
		)
	}

	return answer, nil
}
