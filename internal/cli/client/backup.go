package client

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
	"net/url"
	"os"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
)

const (
	// AdminTokenEnvVar holds the access token the backup command sends.
	// It is read from the environment only, never from a flag. The value
	// is the name of the setting, not a credential.
	AdminTokenEnvVar = "DUSTPAN_ADMIN_TOKEN" //nolint:gosec // The value names the setting; it holds no credential.
	// BackupServerEnvVar sets the default --server value.
	BackupServerEnvVar = "DUSTPAN_SERVER"
	// backupPollInterval sets how often the command reads the backup row
	// while it runs.
	backupPollInterval = 200 * time.Millisecond
	// backupRequestTimeout bounds one backup API request.
	backupRequestTimeout = 30 * time.Second
	// MaxClientBodyBytes caps one installation API answer a client command
	// reads: 1 MiB, far above the small JSON documents the routes answer. A
	// hostile or broken server cannot grow the client without bound.
	MaxClientBodyBytes = 1 << 20
)

// Sentinel errors of the backup subcommand. Each error text names what
// failed and the one command that helps.
var (
	// ErrBackupNeedsServer reports a backup run without --server.
	ErrBackupNeedsServer = errors.New("backup needs --server")
	// ErrBackupFailed reports a backup that left running without success.
	ErrBackupFailed = errors.New("backup failed")
	// ErrBackupNoToken reports a backup run without an admin token.
	ErrBackupNoToken = errors.New("no admin token is set")
	// ErrBackupNoCA reports a CA bundle with no certificate.
	ErrBackupNoCA = errors.New("no certificate found")
	// ErrBackupTransport reports a backup HTTP transport of another type.
	ErrBackupTransport = errors.New("backup HTTP transport is not an *http.Transport")
	// ErrBackupStart reports a backup that never started.
	ErrBackupStart = errors.New("start backup failed")
	// ErrBackupRead reports a backup row that never arrived.
	ErrBackupRead = errors.New("read backup failed")
	// ErrResponseTooLarge reports a server answer above MaxClientBodyBytes.
	// The body may come from a hostile or broken server, so the client
	// refuses it instead of growing without bound.
	ErrResponseTooLarge = errors.New("server answered more data than the client reads")
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
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)
}

// backupStartResponse answers a started backup: its ID.
type backupStartResponse struct {
	ID string `json:"id"`
}

// BackupManifest holds the manifest fields the command prints: the SQL
// size of the backup.
type BackupManifest struct {
	SQLSize int64 `json:"sqlSize"`
}

// BackupStatusResponse answers one backup: its state and, when the backup
// succeeded, the manifest.
type BackupStatusResponse struct {
	ID       string          `json:"id"`
	State    string          `json:"state"`
	Error    string          `json:"error"`
	Manifest *BackupManifest `json:"manifest"`
}

// backupErrorResponse is the error body the installation API answers. Only
// the message is read; the code repeats the status.
type backupErrorResponse struct {
	Message string `json:"message"`
}

// BackupCommand runs `opendustpan backup`: it starts a backup, waits until
// it leaves running, and prints the ID, the state and the size with the
// key and plain-text warnings. A failed backup prints the same lines and
// returns an error, so the exit code is 1.
func BackupCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var f backupFlags

	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
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
			"%w: set --server (%s) to the server base URL and run opendustpan backup again",
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

	client, err := newBackupHTTPClient(f.caFile, "run opendustpan backup again", stderr)
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
			ErrBackupFailed,
			id,
			backupFailureText(doc),
			server,
		)
	}

	return nil
}

// BackupToken returns the access token from DUSTPAN_ADMIN_TOKEN, or from the
// file in DUSTPAN_ADMIN_TOKEN_FILE when the variable is empty: the first
// token, as the server signs each token in as a distinct administrator. No
// token is an error that names both settings and the one command that helps.
func BackupToken() (string, error) {
	tokens, err := LoadAdminTokensFrom(os.LookupEnv)
	if err != nil && !errors.Is(err, ErrAdminTokenMissing) {
		return "", err
	}

	for _, entry := range tokens {
		if token := strings.TrimSpace(entry); token != "" {
			return token, nil
		}
	}

	return "", fmt.Errorf(
		"%w: set %s or %s and run opendustpan backup again",
		ErrBackupNoToken,
		AdminTokenEnvVar,
		AdminTokenFileEnvVar,
	)
}

// newBackupHTTPClient returns the API client of the backup and restore
// commands: the system store with the PEM bundle in caFile added when set.
// An empty caFile trusts the system store alone. rerun names the one command
// that helps, for example "run opendustpan backup again". The client prints
// one warning on stderr when the server says that the access token expires
// soon (ADR 53, decision 11).
func newBackupHTTPClient(caFile, rerun string, stderr io.Writer) (*http.Client, error) {
	if strings.TrimSpace(caFile) == "" {
		return warnOnExpiry(&http.Client{Timeout: backupRequestTimeout}, stderr), nil
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
			ErrBackupNoCA,
			rerun,
		)
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf(
			"%w: check --ca-file and %s",
			ErrBackupTransport,
			rerun,
		)
	}

	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}

	return warnOnExpiry(&http.Client{Timeout: backupRequestTimeout, Transport: transport}, stderr), nil
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

// decodeCappedJSON decodes one installation API answer from r, reading at
// most MaxClientBodyBytes. A larger body is an error wrapping
// ErrResponseTooLarge, never a silent truncation.
func decodeCappedJSON(r io.Reader, doc any) error {
	raw, err := io.ReadAll(io.LimitReader(r, MaxClientBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read server answer: %w", err)
	}

	if len(raw) > MaxClientBodyBytes {
		return fmt.Errorf(
			"%w: over %d bytes: check the server log",
			ErrResponseTooLarge,
			MaxClientBodyBytes,
		)
	}

	if err := json.Unmarshal(raw, doc); err != nil {
		return fmt.Errorf("decode server answer: %w", err)
	}

	return nil
}

// decodeBackupError reads an error body of the installation API: the
// message with its code, or the status alone when the body has no JSON
// error. The body is capped at MaxClientBodyBytes: an oversized body answers
// the status alone.
func decodeBackupError(resp *http.Response) string {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxClientBodyBytes+1))
	if err != nil {
		return resp.Status
	}

	if len(raw) > MaxClientBodyBytes {
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
	endpoint := backupEndpoint(server, "/api/server/backups")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
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
			ErrBackupStart,
			decodeBackupError(resp),
			server,
		)
	}

	var doc backupStartResponse

	if err := decodeCappedJSON(resp.Body, &doc); err != nil || strings.TrimSpace(doc.ID) == "" {
		return "", fmt.Errorf(
			"%w: the server answered no backup ID: check the server log and "+rerunHelp,
			ErrBackupStart,
			server,
		)
	}

	return doc.ID, nil
}

// readBackup reads one backup row.
func readBackup(
	ctx context.Context, client *http.Client, server, token, id string,
) (BackupStatusResponse, error) {
	endpoint := backupEndpoint(server, "/api/server/backups/"+url.PathEscape(id))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return BackupStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+rerunHelp,
			ErrBackupRead,
			id,
			err,
			server,
		)
	}

	backupAuth(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return BackupStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+rerunHelp,
			ErrBackupRead,
			id,
			err,
			server,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK {
		return BackupStatusResponse{}, fmt.Errorf(
			"%w %s: %s: "+rerunHelp,
			ErrBackupRead,
			id,
			decodeBackupError(resp),
			server,
		)
	}

	var doc BackupStatusResponse

	if err := decodeCappedJSON(resp.Body, &doc); err != nil {
		return BackupStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+rerunHelp,
			ErrBackupRead,
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
) (BackupStatusResponse, error) {
	timer := time.NewTimer(backupPollInterval)
	defer timer.Stop()

	for {
		doc, err := readBackup(ctx, client, server, token, id)
		if err != nil {
			return BackupStatusResponse{}, err
		}

		if doc.State != "running" {
			return doc, nil
		}

		select {
		case <-ctx.Done():
			return BackupStatusResponse{}, fmt.Errorf(
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
func backupFailureText(doc BackupStatusResponse) string {
	if strings.TrimSpace(doc.Error) != "" {
		return strings.TrimSpace(doc.Error)
	}

	return "the server reported no error text"
}

// backupSizeText answers the SQL size of a succeeded backup row, or an
// empty string when the manifest is missing.
func backupSizeText(doc BackupStatusResponse) string {
	if doc.Manifest == nil {
		return ""
	}

	return fmt.Sprintf(" (SQL %d bytes)", doc.Manifest.SQLSize)
}

// writeBackupReport prints the ID, the state and, when the manifest is
// present, the size of the backup. It then prints that a backup cannot
// decrypt secrets without the key-encryption key and that a backup holds
// sealed OpenTofu state, so the operator protects the target and keeps the
// key apart from it (ADR 41 decision 10).
func writeBackupReport(stdout io.Writer, doc BackupStatusResponse) error {
	if _, err := fmt.Fprintf(stdout, "backup %s %s%s\n", doc.ID, doc.State, backupSizeText(doc)); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	warnings := []string{
		"A backup cannot decrypt secrets without " + KeyEnvVar + ".",
		"A backup holds OpenTofu state that the server encrypted: protect the target.",
	}

	for _, line := range warnings {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}
