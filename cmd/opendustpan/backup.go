package main

// Backup subcommand of the installation API (ADR 30 decision 10): a client
// of POST /api/server/backups and GET /api/server/backups/{id} (ADR 17).
//
//	opendustpan backup --server https://dustpan.example [--ca-file ca.pem]
//
// The command starts a backup, waits until it leaves running, and prints
// the ID, the state and the SQL size. The access token comes from
// DUSTPAN_ADMIN_TOKEN or from the file in DUSTPAN_ADMIN_TOKEN_FILE, never
// from a flag. When the setting holds several tokens, the command uses the
// first one. A TLS
// server is trusted through the system store with an optional --ca-file
// bundle. The exit code is 0 on a succeeded backup and 1 on a failed
// backup or an error.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// adminTokenEnvVar holds the access token the backup command sends.
	// It is read from the environment only, never from a flag. The value
	// is the name of the setting, not a credential.
	adminTokenEnvVar = "DUSTPAN_ADMIN_TOKEN" //nolint:gosec // The value names the setting; it holds no credential.
	// backupServerEnvVar sets the default --server value.
	backupServerEnvVar = "DUSTPAN_SERVER"
	// backupPollInterval sets how often the command reads the backup row
	// while it runs.
	backupPollInterval = 200 * time.Millisecond
	// backupRequestTimeout bounds one backup API request.
	backupRequestTimeout = 30 * time.Second
)

// Sentinel errors of the backup subcommand. Each error text names what
// failed and the one command that helps.
var (
	// errBackupNeedsServer reports a backup run without --server.
	errBackupNeedsServer = errors.New("backup needs --server")
	// errBackupFailed reports a backup that left running without success.
	errBackupFailed = errors.New("backup failed")
	// errBackupNoToken reports a backup run without an admin token.
	errBackupNoToken = errors.New("no admin token is set")
	// errBackupNoCA reports a CA bundle with no certificate.
	errBackupNoCA = errors.New("no certificate found")
	// errBackupTransport reports a backup HTTP transport of another type.
	errBackupTransport = errors.New("backup HTTP transport is not an *http.Transport")
	// errBackupStart reports a backup that never started.
	errBackupStart = errors.New("start backup failed")
	// errBackupRead reports a backup row that never arrived.
	errBackupRead = errors.New("read backup failed")
)

// rerunHelp names the one command that helps after a backup error: the
// same backup command against the same server.
const rerunHelp = "run opendustpan backup --server %s again"

// backupFlags holds the settings of the backup subcommand.
type backupFlags struct {
	server string
	caFile string
}

func (f *backupFlags) register(fs *flag.FlagSet) {
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

// backupStartResponse answers a started backup: its ID.
type backupStartResponse struct {
	ID string `json:"id"`
}

// backupManifest holds the manifest fields the command prints: the SQL
// size of the backup.
type backupManifest struct {
	SQLSize int64 `json:"sqlSize"`
}

// backupStatusResponse answers one backup: its state and, when the backup
// succeeded, the manifest.
type backupStatusResponse struct {
	ID       string          `json:"id"`
	State    string          `json:"state"`
	Error    string          `json:"error"`
	Manifest *backupManifest `json:"manifest"`
}

// backupErrorResponse is the error body the installation API answers. Only
// the message is read; the code repeats the status.
type backupErrorResponse struct {
	Message string `json:"message"`
}

// backupCommand runs `opendustpan backup`: it starts a backup, waits until
// it leaves running, and prints the ID, the state and the size with the
// key and plain-text warnings. A failed backup prints the same lines and
// returns an error, so the exit code is 1.
func backupCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var f backupFlags

	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
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
			"%w: set --server (%s) to the server base URL and run opendustpan backup again",
			errBackupNeedsServer,
			backupServerEnvVar,
		)
	}

	token, err := backupToken()
	if err != nil {
		return err
	}

	client, err := newBackupHTTPClient(f.caFile, "run opendustpan backup again")
	if err != nil {
		return err
	}

	id, err := startBackup(ctx, client, server, token)
	if err != nil {
		return err
	}

	doc, err := waitBackup(ctx, client, server, token, id)
	if err != nil {
		return err
	}

	if err := writeBackupReport(stdout, doc); err != nil {
		return err
	}

	if doc.State != "succeeded" {
		return fmt.Errorf(
			"%w %s: %s: "+rerunHelp,
			errBackupFailed,
			id,
			backupFailureText(doc),
			server,
		)
	}

	return nil
}

// backupToken returns the access token from DUSTPAN_ADMIN_TOKEN, or from the
// file in DUSTPAN_ADMIN_TOKEN_FILE when the variable is empty: the first
// token, as the server signs each token in as a distinct administrator. No
// token is an error that names both settings and the one command that helps.
func backupToken() (string, error) {
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
		"%w: set %s or %s and run opendustpan backup again",
		errBackupNoToken,
		adminTokenEnvVar,
		adminTokenFileEnvVar,
	)
}

// newBackupHTTPClient returns the API client of the backup and restore
// commands: the system store with the PEM bundle in caFile added when set.
// An empty caFile trusts the system store alone. rerun names the one command
// that helps, for example "run opendustpan backup again".
func newBackupHTTPClient(caFile, rerun string) (*http.Client, error) {
	if strings.TrimSpace(caFile) == "" {
		return &http.Client{Timeout: backupRequestTimeout}, nil
	}

	pemData, err := os.ReadFile(strings.TrimSpace(caFile))
	if err != nil {
		return nil, fmt.Errorf(
			"read CA file %q: %w: check --ca-file and %s",
			caFile,
			err,
			rerun,
		)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf(
			"read CA file %q: %w: check --ca-file and %s",
			caFile,
			errBackupNoCA,
			rerun,
		)
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf(
			"%w: check --ca-file and %s",
			errBackupTransport,
			rerun,
		)
	}

	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}

	return &http.Client{Timeout: backupRequestTimeout, Transport: transport}, nil
}

// backupEndpoint joins the server base URL with path, without a double
// slash.
func backupEndpoint(server, path string) string {
	return strings.TrimSuffix(server, "/") + path
}

// backupAuth sets the Bearer credential of the installation API.
func backupAuth(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
}

// decodeBackupError reads an error body of the installation API: the
// message with its code, or the status alone when the body has no JSON
// error.
func decodeBackupError(resp *http.Response) string {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.Status
	}

	var doc backupErrorResponse

	if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil || doc.Message == "" {
		if text := strings.TrimSpace(string(raw)); text != "" {
			return fmt.Sprintf("%s: %s", resp.Status, text)
		}

		return resp.Status
	}

	return fmt.Sprintf("%s: %s", resp.Status, doc.Message)
}

// startBackup starts a backup and answers its ID. A second start while one
// runs names the running backup and the one command that helps.
func startBackup(ctx context.Context, client *http.Client, server, token string) (string, error) {
	url := backupEndpoint(server, "/api/server/backups")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf(
			"start backup: %w: check --server and "+rerunHelp,
			err,
			server,
		)
	}

	backupAuth(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf(
			"start backup: %w: check --server and "+rerunHelp,
			err,
			server,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion where it matters.
	}()

	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf(
			"%w: %s: check the server log and "+rerunHelp,
			errBackupStart,
			decodeBackupError(resp),
			server,
		)
	}

	var doc backupStartResponse

	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil || strings.TrimSpace(doc.ID) == "" {
		return "", fmt.Errorf(
			"%w: the server answered no backup ID: check the server log and "+rerunHelp,
			errBackupStart,
			server,
		)
	}

	return doc.ID, nil
}

// readBackup reads one backup row.
func readBackup(
	ctx context.Context, client *http.Client, server, token, id string,
) (backupStatusResponse, error) {
	url := backupEndpoint(server, "/api/server/backups/"+id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return backupStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+rerunHelp,
			errBackupRead,
			id,
			err,
			server,
		)
	}

	backupAuth(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return backupStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+rerunHelp,
			errBackupRead,
			id,
			err,
			server,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK {
		return backupStatusResponse{}, fmt.Errorf(
			"%w %s: %s: "+rerunHelp,
			errBackupRead,
			id,
			decodeBackupError(resp),
			server,
		)
	}

	var doc backupStatusResponse

	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return backupStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+rerunHelp,
			errBackupRead,
			id,
			err,
			server,
		)
	}

	return doc, nil
}

// waitBackup polls one backup until it leaves running and answers its
// last row. A canceled context stops the wait with the context error.
func waitBackup(
	ctx context.Context, client *http.Client, server, token, id string,
) (backupStatusResponse, error) {
	timer := time.NewTimer(backupPollInterval)
	defer timer.Stop()

	for {
		doc, err := readBackup(ctx, client, server, token, id)
		if err != nil {
			return backupStatusResponse{}, err
		}

		if doc.State != "running" {
			return doc, nil
		}

		select {
		case <-ctx.Done():
			return backupStatusResponse{}, fmt.Errorf(
				"wait for backup %s: %w: "+rerunHelp,
				id,
				ctx.Err(),
				server,
			)
		case <-timer.C:
			timer.Reset(backupPollInterval)
		}
	}
}

// backupFailureText answers the error text of a failed backup row, or a
// fallback when the row holds none.
func backupFailureText(doc backupStatusResponse) string {
	if strings.TrimSpace(doc.Error) != "" {
		return strings.TrimSpace(doc.Error)
	}

	return "the server reported no error text"
}

// backupSizeText answers the SQL size of a succeeded backup row, or an
// empty string when the manifest is missing.
func backupSizeText(doc backupStatusResponse) string {
	if doc.Manifest == nil {
		return ""
	}

	return fmt.Sprintf(" (SQL %d bytes)", doc.Manifest.SQLSize)
}

// writeBackupReport prints the ID, the state and, when the manifest is
// present, the size of the backup. It then prints that a backup cannot
// decrypt secrets without the key-encryption key and that a backup holds
// OpenTofu state in plain text, so the operator protects the target
// (ADR 30 decision 7).
func writeBackupReport(stdout io.Writer, doc backupStatusResponse) error {
	if _, err := fmt.Fprintf(stdout, "backup %s %s%s\n", doc.ID, doc.State, backupSizeText(doc)); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	warnings := []string{
		"A backup cannot decrypt secrets without " + keyEnvVar + ".",
		"A backup holds OpenTofu state in plain text: protect the target.",
	}

	for _, line := range warnings {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}
