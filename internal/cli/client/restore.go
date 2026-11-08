package client

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
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/backup"
)

// Sentinel errors of the restore subcommand. Each error text names what
// failed and the one command that helps.
var (
	// ErrRestoreNeedsServer reports a restore run without --server.
	ErrRestoreNeedsServer = errors.New("restore needs --server")
	// ErrRestoreNeedsBackup reports a restore run without --backup.
	ErrRestoreNeedsBackup = errors.New("restore needs --backup")
	// ErrRestoreFailed reports a restore that left checking and applying
	// without success.
	ErrRestoreFailed = errors.New("restore failed")
	// ErrRestoreStart reports a restore that never started.
	ErrRestoreStart = errors.New("start restore failed")
	// ErrRestoreRead reports a restore row that never arrived.
	ErrRestoreRead = errors.New("read restore failed")
	// ErrRestoreMissing reports a restore row the server does not hold.
	// waitRestore uses it to tell a transient swap gap from other read
	// failures: after one successful read, a missing row retries.
	ErrRestoreMissing = errors.New("restore not found")
)

// restoreRerunHelp names the one command that helps after a restore error:
// the same restore command against the same server.
const restoreRerunHelp = "run opendustpan restore --server %s --backup %s again"

// restoreActiveWorkText is the start of the error text that the server
// stores for a restore that active work stopped from entering maintenance
// mode.
const restoreActiveWorkText = "cannot enter maintenance mode while work is active"

// restoreForceHelp names the two ways past active work: end the work, or
// run the same restore with --force, which cancels it.
const restoreForceHelp = "end that work, or run opendustpan restore --server %s --backup %s --force to cancel it"

// restoreCorruptHelp names the one command that helps after a restore of a
// backup with corrupt data: the check of the backup, which names each missing
// or bad object. A second run of the same restore fails in the same way.
const restoreCorruptHelp = "run opendustpan backup check --server %s --backup %s to find each missing or bad object, " +
	"or restore another backup"

// restoreResumeHelp says what to do when the command cannot read a restore
// that started: the server resumes the restore on its own, for example after
// the database comes back, so a second run is not needed. The operator reads
// the state of the restore instead.
const restoreResumeHelp = "the server resumes restore %s on its own and it needs no second run: " +
	"read its state with GET %s/api/server/restores/%s"

// restoreDamagedHelp names the one command that helps after a restore that
// stopped at live blobs whose bytes do not match their records or do not
// open: the server text names the keys, and the operator puts their good
// bytes back first.
const restoreDamagedHelp = "put the good bytes back at the keys above from a copy of the blob store, then " +
	restoreRerunHelp

// restoreMissingHelp names the one command that helps after a restore that
// stopped at more missing live blobs than its bound allows.
const restoreMissingHelp = "run opendustpan store check --server %s --target blobs to check the blob store " +
	"setting of the server, then " + restoreRerunHelp

// restoreBreakGlassHelp names the one action that helps after a restore that
// stopped because the restored data would hold fewer working break-glass
// accounts than the live data, and fewer than 2.
const restoreBreakGlassHelp = "restore a backup that holds at least 2 working break-glass accounts"

// restoreFlags holds the settings of the restore subcommand.
type restoreFlags struct {
	server    string
	caFile    string
	backup    string
	checkOnly bool
	force     bool
}

func (f *restoreFlags) register(fs *flag.FlagSet) {
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)
	cli.ByFlag("backup").String(fs, &f.backup)
	cli.ByFlag("check-only").Bool(fs, &f.checkOnly)
	cli.ByFlag("force").Bool(fs, &f.force)
}

// RestoreStartRequest asks the server for one restore: the backup ID from
// its configured target, whether to check only, and whether to cancel active
// work first.
type RestoreStartRequest struct {
	BackupID  string `json:"backupID"`
	CheckOnly bool   `json:"checkOnly,omitempty"`
	Force     bool   `json:"force,omitempty"`
}

// restoreStartResponse answers a started restore: its ID. The backup taken
// before the apply appears in the polled status row, not here.
type restoreStartResponse struct {
	ID       string           `json:"id"`
	Warnings []RestoreWarning `json:"warnings"`
}

// RestoreWarning is a warning of a restore: a stable code and the text for
// the operator. The restore goes on after a warning.
type RestoreWarning struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// RestoreDevData states the dev data of a restore: the removal of a restore
// that ran, or, for a check-only restore, the removal that it would make.
type RestoreDevData struct {
	Principals int64            `json:"principals"`
	Classes    map[string]int64 `json:"classes"`
}

// RestoreManifest holds the manifest fields the command prints: the SQL
// size of the restored backup.
type RestoreManifest struct {
	SQLSize int64 `json:"sqlSize"`
}

// RestoreStatusResponse answers one restore: its state and, when the restore
// succeeded, the manifest.
type RestoreStatusResponse struct {
	ID          string           `json:"id"`
	State       string           `json:"state"`
	BackupID    string           `json:"backupID"`
	PreBackupID string           `json:"preBackupID"`
	Error       string           `json:"error"`
	ErrorCode   string           `json:"errorCode"`
	Manifest    *RestoreManifest `json:"manifest"`
	Warnings    []RestoreWarning `json:"warnings"`
	DevData     *RestoreDevData  `json:"devData"`
}

// devBackupKind is the value of installationKind in the manifest of a backup
// of a dev installation.
const devBackupKind = "dev"

// stateSucceeded is the state of a backup or a restore row that ended well.
const stateSucceeded = "succeeded"

// devBackupNotice is what the command prints before it starts a restore of a
// backup of a dev installation. The server decides: it refuses the restore
// unless it started with the setting that allows it.
const devBackupNotice = "warning: this backup is from a dev installation. " +
	"A production installation refuses it unless the server started with " +
	"`--unsafe-allow-restoring-dev-installation-backups-into-production`. " +
	"A restore that the server allows removes the dev users and their data. We do not recommend this restore."

// RestoreCommand runs `opendustpan restore`: it starts a restore, waits until
// it leaves checking and applying, and prints the ID, the state and the
// backup taken before it. A failed restore prints the same lines and returns
// an error, so the exit code is 1.
func RestoreCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
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

	client, err := newBackupHTTPClient(f.caFile, fmt.Sprintf(restoreRerunHelp, server, id), stderr)
	if err != nil {
		return err
	}

	// Read the manifest first, so the warning comes before the restore
	// starts. A backup that cannot be read here gets its own error from the
	// restore, so this read never stops the command.
	kindRead := restoreReadParams{client: client, server: server, token: token, restoreID: "", backupID: id}
	warnDevBackup(ctx, kindRead, stderr)

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

	doc, err := waitRestore(ctx, restoreReadParams{
		client:    client,
		server:    server,
		token:     token,
		restoreID: started.ID,
		backupID:  id,
	})
	if err != nil {
		return err
	}

	if err := writeRestoreReport(stdout, doc, f.checkOnly); err != nil {
		return err
	}

	if err := writeRestoreWarnings(stdout, started.Warnings, doc); err != nil {
		return err
	}

	if doc.State != stateSucceeded {
		return fmt.Errorf(
			"%w %s: %s: %s",
			ErrRestoreFailed,
			started.ID,
			restoreFailureText(doc),
			restoreFailureHelp(doc, f, server, id),
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
		return restoreRun{}, ErrUsage
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return restoreRun{}, fmt.Errorf(
			"%w: set --server (%s) to the server base URL and run opendustpan restore again",
			ErrRestoreNeedsServer,
			BackupServerEnvVar,
		)
	}

	if err := cli.ByFlag("server").CheckURL(server); err != nil {
		return restoreRun{}, err
	}

	id := strings.TrimSpace(f.backup)
	if id == "" {
		return restoreRun{}, fmt.Errorf(
			"%w: set --backup to the backup ID and run opendustpan restore --server %s again",
			ErrRestoreNeedsBackup,
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
		"%w: set %s or %s and "+restoreRerunHelp,
		ErrBackupNoToken,
		AdminTokenEnvVar,
		AdminTokenFileEnvVar,
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
// runs gives the message of the server, which names the running restore.
func startRestore(ctx context.Context, params startRestoreParams) (restoreStartResponse, error) {
	server := params.server
	id := params.backupID

	endpoint := backupEndpoint(server, "/api/server/restores")

	data, err := json.Marshal(RestoreStartRequest{
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
		hint := fmt.Sprintf("check the server log and "+restoreRerunHelp, server, id)

		return restoreStartResponse{}, fmt.Errorf(
			"%w: %s",
			ErrRestoreStart,
			answerText(resp.StatusCode, decodeErrorAnswer(resp), hint),
		)
	}

	return readRestoreStart(resp.Body, server, id)
}

// readRestoreStart reads the answer of a started restore: the restore ID.
func readRestoreStart(body io.Reader, server, id string) (restoreStartResponse, error) {
	var doc restoreStartResponse

	if err := decodeCappedJSON(body, MaxClientBodyBytes, &doc); err != nil {
		return restoreStartResponse{}, fmt.Errorf(
			"%w: %w: check the server log and "+restoreRerunHelp,
			ErrRestoreStart,
			err,
			server,
			id,
		)
	}

	if strings.TrimSpace(doc.ID) == "" {
		return restoreStartResponse{}, fmt.Errorf(
			"%w: the server answered no restore ID: check the server log and "+restoreRerunHelp,
			ErrRestoreStart,
			server,
			id,
		)
	}

	return doc, nil
}

// restoreReadParams carries one restore read: the client, the server, the
// token, the polled restore ID, and the requested backup ID. The hint names
// the backup, so an operator who follows the error reruns a command that can
// work; the restore ID stays only in the failure prefix.
type restoreReadParams struct {
	client    *http.Client
	server    string
	token     string
	restoreID string
	backupID  string
}

// readRestore reads one restore row.
func readRestore(ctx context.Context, params restoreReadParams) (RestoreStatusResponse, error) {
	endpoint := backupEndpoint(params.server, "/api/server/restores/"+url.PathEscape(params.restoreID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return RestoreStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+restoreResumeHelp,
			ErrRestoreRead,
			params.restoreID,
			err,
			params.restoreID,
			params.server,
			params.restoreID,
		)
	}

	backupAuth(req, params.token)

	resp, err := params.client.Do(req)
	if err != nil {
		return RestoreStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+restoreResumeHelp,
			ErrRestoreRead,
			params.restoreID,
			err,
			params.restoreID,
			params.server,
			params.restoreID,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK {
		return RestoreStatusResponse{}, restoreStatusError(resp, params)
	}

	var doc RestoreStatusResponse

	if err := decodeCappedJSON(resp.Body, MaxManifestAnswerBytes, &doc); err != nil {
		return RestoreStatusResponse{}, fmt.Errorf(
			"%w %s: %w: "+restoreResumeHelp,
			ErrRestoreRead,
			params.restoreID,
			err,
			params.restoreID,
			params.server,
			params.restoreID,
		)
	}

	return doc, nil
}

// restoreStatusError answers the read error for a non-200 status. A missing
// row wraps ErrRestoreMissing so the wait tells a transient swap gap from
// other failures; any other status is a plain read failure.
func restoreStatusError(resp *http.Response, params restoreReadParams) error {
	hint := fmt.Sprintf(restoreResumeHelp, params.restoreID, params.server, params.restoreID)
	text := answerText(resp.StatusCode, decodeErrorAnswer(resp), hint)

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w %s: %w: %s", ErrRestoreRead, params.restoreID, ErrRestoreMissing, text)
	}

	return fmt.Errorf("%w %s: %s", ErrRestoreRead, params.restoreID, text)
}

// waitRestore polls one restore until it leaves checking and applying and
// answers its last row. A missing row after one successful read waits one
// poll interval and retries until the context ends: the swap hides the row
// between the apply and its recreate. A missing row before any successful
// read fails at once, so a wrong ID never polls forever. A canceled context
// stops the wait with the context error.
func waitRestore(ctx context.Context, params restoreReadParams) (RestoreStatusResponse, error) {
	timer := time.NewTimer(backupPollInterval)
	defer timer.Stop()

	sawRow := false

	for {
		doc, err := readRestore(ctx, params)
		if err != nil {
			if !sawRow || !errors.Is(err, ErrRestoreMissing) {
				return RestoreStatusResponse{}, err
			}

			if waitErr := waitRestoreMissing(ctx, timer, params); waitErr != nil {
				return RestoreStatusResponse{}, waitErr
			}

			continue
		}

		sawRow = true

		if doc.State != "checking" && doc.State != "applying" {
			return doc, nil
		}

		select {
		case <-ctx.Done():
			return RestoreStatusResponse{}, fmt.Errorf(
				"wait for restore %s: %w: "+restoreResumeHelp,
				params.restoreID,
				ctx.Err(),
				params.restoreID,
				params.server,
				params.restoreID,
			)
		case <-timer.C:
			timer.Reset(backupPollInterval)
		}
	}
}

// waitRestoreMissing waits one poll interval after a transient missing row.
// A canceled context stops the wait with the context error.
func waitRestoreMissing(ctx context.Context, timer *time.Timer, params restoreReadParams) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf(
			"wait for restore %s: %w: "+restoreResumeHelp,
			params.restoreID,
			ctx.Err(),
			params.restoreID,
			params.server,
			params.restoreID,
		)
	case <-timer.C:
		timer.Reset(backupPollInterval)

		return nil
	}
}

// restoreFailureText answers the error text of a failed restore row, or a
// fallback when the row holds none.
func restoreFailureText(doc RestoreStatusResponse) string {
	if strings.TrimSpace(doc.Error) != "" {
		return strings.TrimSpace(doc.Error)
	}

	return "the server reported no error text"
}

// restoreFailureHelp answers the one command that helps after a failed
// restore. A restore without --force that active work stopped gets the
// --force command. A restore that damaged or missing live blobs stopped gets
// the repair or the check of the blob store first. A restore of a backup with
// corrupt data gets the backup check command. A restore that would leave too
// few working break-glass accounts gets the request to restore another
// backup. Every other failure gets the same restore command again.
func restoreFailureHelp(doc RestoreStatusResponse, f restoreFlags, server, id string) string {
	if !f.force && strings.HasPrefix(strings.TrimSpace(doc.Error), restoreActiveWorkText) {
		return fmt.Sprintf(restoreForceHelp, server, id)
	}

	// The code of the refusal chooses the help, not the text: the text
	// holds keys that the code does not depend on.
	switch backup.RestoreErrorCode(doc.ErrorCode) {
	case backup.CodeLiveBlobsDamaged:
		return fmt.Sprintf(restoreDamagedHelp, server, id)
	case backup.CodeLiveBlobsMissing:
		return fmt.Sprintf(restoreMissingHelp, server, server, id)
	case backup.CodeBlobsNotBacked:
		return fmt.Sprintf(restoreCorruptHelp, server, id)
	case backup.CodePreBackupForeign:
		return fmt.Sprintf(restoreRerunHelp, server, id)
	case backup.CodeNoWorkingBreakGlass:
		return restoreBreakGlassHelp
	}

	if strings.HasSuffix(strings.TrimSpace(doc.Error), backup.ErrCorrupt.Error()) {
		return fmt.Sprintf(restoreCorruptHelp, server, id)
	}

	return fmt.Sprintf(restoreRerunHelp, server, id)
}

// writeRestoreReport prints the ID, the state, the backup taken before it
// and, when the manifest is present, the SQL size. A check-only restore that
// succeeded (a failed one ends with its error line, not with this line)
// prints that it changed nothing, so the operator knows the live data is
// untouched.
//
//nolint:revive // The check-only flag only selects the last line; the report stays one function.
func writeRestoreReport(stdout io.Writer, doc RestoreStatusResponse, checkOnly bool) error {
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

	if checkOnly && doc.State == stateSucceeded {
		if _, err := fmt.Fprintln(stdout, "check only: no data changed"); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}

// backupKindResponse holds the one field that the notice reads from the
// answer of GET /api/server/backups/{id}.
type backupKindResponse struct {
	Manifest *backupKindManifest `json:"manifest"`
}

// backupKindManifest holds the kind of the manifest of a backup.
type backupKindManifest struct {
	InstallationKind string `json:"installationKind"`
}

// warnDevBackup prints the notice about a dev backup when the manifest of the
// backup says dev. A failed read prints nothing: the restore reports it.
func warnDevBackup(ctx context.Context, params restoreReadParams, stderr io.Writer) {
	endpoint := backupEndpoint(params.server, "/api/server/backups/"+url.PathEscape(params.backupID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return
	}

	backupAuth(req, params.token)

	resp, err := params.client.Do(req)
	if err != nil {
		return
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var doc backupKindResponse
	if err := decodeCappedJSON(resp.Body, MaxManifestAnswerBytes, &doc); err != nil {
		return
	}

	if doc.Manifest != nil && doc.Manifest.InstallationKind == devBackupKind {
		_, _ = fmt.Fprintln(stderr, devBackupNotice) //nolint:errcheck // A failed notice never stops the restore.
	}
}

// writeRestoreWarnings prints the warnings that the server answered, once
// each, and the dev data that the restore removed or would remove.
func writeRestoreWarnings(stdout io.Writer, started []RestoreWarning, doc RestoreStatusResponse) error {
	printed := map[string]bool{}

	for _, w := range slices.Concat(started, doc.Warnings) {
		if printed[w.Code] {
			continue
		}

		printed[w.Code] = true

		if _, err := fmt.Fprintf(stdout, "warning: %s\n", w.Text); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	if doc.DevData == nil {
		return nil
	}

	classes := make([]string, 0, len(doc.DevData.Classes))
	for class, n := range doc.DevData.Classes {
		classes = append(classes, fmt.Sprintf("%s %d", class, n))
	}

	slices.Sort(classes)

	_, err := fmt.Fprintf(stdout, "dev data: %d principals; %s\n", doc.DevData.Principals, strings.Join(classes, ", "))
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}
