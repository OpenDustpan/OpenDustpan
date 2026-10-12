package main

// Restore subcommand of the installation API (ADR 38, ADR 39): a client of
// POST /api/server/restores and GET /api/server/restores/{id} (ADR 17).
//
//	opendustpan restore --server https://dustpan.example --backup bkp-aaa [--check-only] [--force] [--ca-file ca.pem]
//
// The command starts a restore, waits until it leaves checking and applying,
// and prints the ID, the state and the backup taken before it. A check-only
// restore runs the same checks and applies nothing. With --force the server
// cancels active updates and runs first. The access token comes from
// DUSTPAN_ADMIN_TOKEN or from the file in DUSTPAN_ADMIN_TOKEN_FILE, never
// from a flag. When the setting holds several tokens, the command uses the
// first one. A TLS server is trusted through the system store with an
// optional --ca-file bundle. The exit code is 0 on a succeeded restore and 1
// on a failed restore or an error. The server reads its own configured
// backup target and its own live stores: the command takes no --from,
// --database, --blob-dir or --blob-store.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Sentinel errors of the restore subcommand. Each error text names what
// failed and the one command that helps.
var (
	// errRestoreNeedsServer reports a restore run without --server.
	errRestoreNeedsServer = errors.New("restore needs --server")
	// errRestoreNeedsBackup reports a restore run without --backup.
	errRestoreNeedsBackup = errors.New("restore needs --backup")
	// errRestoreFailed reports a restore that left checking and applying
	// without success.
	errRestoreFailed = errors.New("restore failed")
	// errRestoreStart reports a restore that never started.
	errRestoreStart = errors.New("start restore failed")
	// errRestoreRead reports a restore row that never arrived.
	errRestoreRead = errors.New("read restore failed")
)

// restoreRerunHelp names the one command that helps after a restore error:
// the same restore command against the same server.
const restoreRerunHelp = "run opendustpan restore --server %s --backup %s again"

// maxRestoreStatusBytes bounds a restore status read: the row is small, but
// GET can embed the full manifest, so the client never reads unbounded
// server bytes.
const maxRestoreStatusBytes = 8 << 20

// restoreFlags holds the settings of the restore subcommand.
type restoreFlags struct {
	server    string
	caFile    string
	backup    string
	checkOnly bool
	force     bool
}

func (f *restoreFlags) register(fs *flag.FlagSet) {
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
	fs.StringVar(&f.backup, "backup", "", "backup ID to restore, for example bkp-aaa")
	fs.BoolVar(&f.checkOnly, "check-only", false, "run the restore checks and apply nothing")
	fs.BoolVar(&f.force, "force", false, "cancel active updates and runs first, then restore")
}

// restoreStartRequest asks the server for one restore: the backup ID from
// its configured target, whether to check only, and whether to cancel active
// work first.
type restoreStartRequest struct {
	BackupID  string `json:"backupID"`
	CheckOnly bool   `json:"checkOnly,omitempty"`
	Force     bool   `json:"force,omitempty"`
}

// restoreStartResponse answers a started restore: its ID. The backup taken
// before the apply appears in the polled status row, not here.
type restoreStartResponse struct {
	ID string `json:"id"`
}

// restoreManifest holds the manifest fields the command prints: the SQL
// size of the restored backup.
type restoreManifest struct {
	SQLSize int64 `json:"sqlSize"`
}

// restoreStatusResponse answers one restore: its state and, when the restore
// succeeded, the manifest.
type restoreStatusResponse struct {
	ID          string           `json:"id"`
	State       string           `json:"state"`
	BackupID    string           `json:"backupID"`
	PreBackupID string           `json:"preBackupID"`
	Error       string           `json:"error"`
	Manifest    *restoreManifest `json:"manifest"`
}

// restoreCommand runs `opendustpan restore`: it starts a restore, waits until
// it leaves checking and applying, and prints the ID, the state and the
// backup taken before it. A failed restore prints the same lines and returns
// an error, so the exit code is 1.
func restoreCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	run, err := parseRestoreArgs(args, stderr)
	if err != nil {
		return err
	}

	server := run.server
	id := run.backupID
	f := run.flags

	token, err := restoreToken(server, id)
	if err != nil {
		return err
	}

	client, err := newBackupHTTPClient(f.caFile, fmt.Sprintf(restoreRerunHelp, server, id))
	if err != nil {
		return err
	}

	started, err := startRestore(ctx, startRestoreParams{
		client:    client,
		server:    server,
		token:     token,
		backupID:  id,
		checkOnly: f.checkOnly,
		force:     f.force,
	})
	if err != nil {
		return err
	}

	doc, err := waitRestore(ctx, client, server, token, started.ID)
	if err != nil {
		return err
	}

	if err := writeRestoreReport(stdout, doc, f.checkOnly); err != nil {
		return err
	}

	if doc.State != "succeeded" {
		return fmt.Errorf(
			"%w %s: %s: "+restoreRerunHelp,
			errRestoreFailed,
			started.ID,
			restoreFailureText(doc),
			server,
			id,
		)
	}

	return nil
}

// restoreRun carries one parsed restore invocation: the flags, the server,
// and the backup ID.
type restoreRun struct {
	flags    restoreFlags
	server   string
	backupID string
}

// parseRestoreArgs parses the restore flags and returns the server, the
// backup ID, and the flags. It fails on unknown flags, positional arguments,
// and a missing --server or --backup.
func parseRestoreArgs(args []string, stderr io.Writer) (restoreRun, error) {
	var f restoreFlags

	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return restoreRun{}, fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return restoreRun{}, errUsage
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return restoreRun{}, fmt.Errorf(
			"%w: set --server (%s) to the server base URL and run opendustpan restore again",
			errRestoreNeedsServer,
			backupServerEnvVar,
		)
	}

	id := strings.TrimSpace(f.backup)
	if id == "" {
		return restoreRun{}, fmt.Errorf(
			"%w: set --backup to the backup ID and run opendustpan restore --server %s again",
			errRestoreNeedsBackup,
			server,
		)
	}

	return restoreRun{flags: f, server: server, backupID: id}, nil
}

// restoreToken returns the access token from DUSTPAN_ADMIN_TOKEN, or from
// the file in DUSTPAN_ADMIN_TOKEN_FILE when the variable is empty: the first
// token, as the server signs each token in as a distinct administrator. No
// token is an error that names both settings and the one command that helps.
func restoreToken(server, id string) (string, error) {
	tokens, err := loadAdminTokensFrom(os.LookupEnv)
	if err != nil && !errors.Is(err, errAdminTokenMissing) {
		return "", err
	}

	for _, entry := range tokens {
		if token := strings.TrimSpace(entry); token != "" {
			return token, nil
		}
	}

	return "", fmt.Errorf(
		"%w: set %s or %s and "+restoreRerunHelp,
		errBackupNoToken,
		adminTokenEnvVar,
		adminTokenFileEnvVar,
		server,
		id,
	)
}

// startRestoreParams carries one restore start: the client, the server, the
// token, the backup ID, and the check-only and force flags.
type startRestoreParams struct {
	client    *http.Client
	server    string
	token     string
	backupID  string
	checkOnly bool
	force     bool
}

// startRestore starts a restore and answers its ID. A second start while one
// runs names the running restore and the one command that helps.
func startRestore(ctx context.Context, params startRestoreParams) (restoreStartResponse, error) {
	server := params.server
	id := params.backupID

	endpoint := backupEndpoint(server, "/api/server/restores")

	data, err := json.Marshal(restoreStartRequest{
		BackupID:  params.backupID,
		CheckOnly: params.checkOnly,
		Force:     params.force,
	})
	if err != nil {
		return restoreStartResponse{}, fmt.Errorf(
			"start restore: %w: check --backup and "+restoreRerunHelp,
			err,
			server,
			id,
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return restoreStartResponse{}, fmt.Errorf(
			"start restore: %w: check --server and "+restoreRerunHelp,
			err,
			server,
			id,
		)
	}

	req.Header.Set("Content-Type", "application/json")

	backupAuth(req, params.token)

	resp, err := params.client.Do(req)
	if err != nil {
		return restoreStartResponse{}, fmt.Errorf(
			"start restore: %w: check --server and "+restoreRerunHelp,
			err,
			server,
			id,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion where it matters.
	}()

	if resp.StatusCode != http.StatusAccepted {
		return restoreStartResponse{}, fmt.Errorf(
			"%w: %s: check the server log and "+restoreRerunHelp,
			errRestoreStart,
			decodeBackupError(resp),
			server,
			id,
		)
	}

	var doc restoreStartResponse

	limited := io.LimitReader(resp.Body, maxRestoreStatusBytes)

	if err := json.NewDecoder(limited).Decode(&doc); err != nil || strings.TrimSpace(doc.ID) == "" {
		return restoreStartResponse{}, fmt.Errorf(
			"%w: the server answered no restore ID: check the server log and "+restoreRerunHelp,
			errRestoreStart,
			server,
			id,
		)
	}

	return doc, nil
}

// readRestore reads one restore row.
func readRestore(
	ctx context.Context, client *http.Client, server, token, id string,
) (restoreStatusResponse, error) {
	endpoint := backupEndpoint(server, "/api/server/restores/"+url.PathEscape(id))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return restoreStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+restoreRerunHelp,
			errRestoreRead,
			id,
			err,
			server,
			id,
		)
	}

	backupAuth(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return restoreStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+restoreRerunHelp,
			errRestoreRead,
			id,
			err,
			server,
			id,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK {
		return restoreStatusResponse{}, fmt.Errorf(
			"%w %s: %s: "+restoreRerunHelp,
			errRestoreRead,
			id,
			decodeBackupError(resp),
			server,
			id,
		)
	}

	var doc restoreStatusResponse

	limited := io.LimitReader(resp.Body, maxRestoreStatusBytes)

	if err := json.NewDecoder(limited).Decode(&doc); err != nil {
		return restoreStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+restoreRerunHelp,
			errRestoreRead,
			id,
			err,
			server,
			id,
		)
	}

	return doc, nil
}

// waitRestore polls one restore until it leaves checking and applying and
// answers its last row. A canceled context stops the wait with the context
// error.
func waitRestore(
	ctx context.Context, client *http.Client, server, token, id string,
) (restoreStatusResponse, error) {
	timer := time.NewTimer(backupPollInterval)
	defer timer.Stop()

	for {
		doc, err := readRestore(ctx, client, server, token, id)
		if err != nil {
			return restoreStatusResponse{}, err
		}

		if doc.State != "checking" && doc.State != "applying" {
			return doc, nil
		}

		select {
		case <-ctx.Done():
			return restoreStatusResponse{}, fmt.Errorf(
				"wait for restore %s: %w: "+restoreRerunHelp,
				id,
				ctx.Err(),
				server,
				doc.BackupID,
			)
		case <-timer.C:
			timer.Reset(backupPollInterval)
		}
	}
}

// restoreFailureText answers the error text of a failed restore row, or a
// fallback when the row holds none.
func restoreFailureText(doc restoreStatusResponse) string {
	if strings.TrimSpace(doc.Error) != "" {
		return strings.TrimSpace(doc.Error)
	}

	return "the server reported no error text"
}

// writeRestoreReport prints the ID, the state, the backup taken before it
// and, when the manifest is present, the SQL size. A check-only restore
// prints that it changed nothing, so the operator knows the live data is
// untouched.
//
//nolint:revive // The check-only flag only selects the last line; the report stays one function.
func writeRestoreReport(stdout io.Writer, doc restoreStatusResponse, checkOnly bool) error {
	size := ""
	if doc.Manifest != nil {
		size = fmt.Sprintf(" (SQL %d bytes)", doc.Manifest.SQLSize)
	}

	if _, err := fmt.Fprintf(stdout, "restore %s %s%s\n", doc.ID, doc.State, size); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	if strings.TrimSpace(doc.PreBackupID) != "" {
		if _, err := fmt.Fprintf(stdout, "backup before restore: %s\n", doc.PreBackupID); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	if checkOnly {
		if _, err := fmt.Fprintln(stdout, "check only: no data changed"); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}
