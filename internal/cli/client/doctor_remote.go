package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
)

// backupMaxAge is the age at which the last backup counts as old: a daily
// backup, with one hour of slack.
const backupMaxAge = 25 * time.Hour

// capabilitiesDocument is the part of the capabilities document that doctor
// reads.
type capabilitiesDocument struct {
	InstallationKind string `json:"installationKind"`
}

// backupListDocument is the part of the list of backups that doctor reads,
// newest first.
type backupListDocument struct {
	Backups []backupRecord `json:"backups"`
}

// backupRecord is one row of the list of backups.
type backupRecord struct {
	State   string `json:"state"`
	EndedAt string `json:"endedAt"`
}

// remoteChecks asks the running server at server through the REST API with
// the token of the client commands. A missing token or a server that does not
// answer fails the check of the kind, and the other two checks say that they
// could not run.
func remoteChecks(
	ctx context.Context, d doctorSettings, server, binaryVersion string, stderr io.Writer,
) []checkResult {
	const rerun = "run opendustpan doctor again"

	if err := cli.ByFlag("server").CheckURL(server); err != nil {
		return []checkResult{{statusFail, nameServerKind, err.Error()}}
	}

	client, err := newBackupHTTPClient(d.value("ca-file"), rerun, stderr)
	if err != nil {
		return []checkResult{{statusFail, nameServerKind, err.Error()}}
	}

	token, tokenErr := BackupToken()
	if tokenErr != nil {
		const skipped = "not checked: the token of the client commands is missing"

		return []checkResult{
			{statusFail, nameServerKind, tokenErr.Error()},
			{statusWarn, nameServerUpgrade, skipped},
			{statusWarn, nameServerBackup, skipped},
		}
	}

	return []checkResult{
		serverKindCheck(ctx, client, server, token),
		serverUpgradeCheck(ctx, client, server, token, binaryVersion),
		serverBackupCheck(ctx, client, server, token),
	}
}

// serverKindCheck sends the token and checks that the capabilities document
// reports the installation kind production. A missing field fails the check.
func serverKindCheck(ctx context.Context, client *http.Client, server, token string) checkResult {
	var doc capabilitiesDocument

	if err := getJSON(ctx, client, backupEndpoint(server, "/api/dustpan/v1/capabilities"), token, &doc); err != nil {
		return checkResult{statusFail, nameServerKind, err.Error()}
	}

	switch doc.InstallationKind {
	case "production":
		return checkResult{statusPass, nameServerKind, "the server reports the installation kind production"}
	case "":
		return checkResult{
			statusFail, nameServerKind,
			"the capabilities document has no installationKind: check that the token can read the caller",
		}
	default:
		return checkResult{
			statusFail, nameServerKind,
			fmt.Sprintf("the server reports the installation kind %s, not production", doc.InstallationKind),
		}
	}
}

// serverUpgradeCheck warns when the upgrade check of the server gives the
// verdict cannot start for this binary.
func serverUpgradeCheck(
	ctx context.Context, client *http.Client, server, token, binaryVersion string,
) checkResult {
	answer, err := readUpgradeCheck(ctx, client, server, token)
	if err != nil {
		return checkResult{statusWarn, nameServerUpgrade, err.Error()}
	}

	inspection, err := compareUpgradeCheck(answer)
	if err != nil {
		return checkResult{statusWarn, nameServerUpgrade, err.Error()}
	}

	if err := startRefusal(inspection, binaryVersion); err != nil {
		return checkResult{statusWarn, nameServerUpgrade, "cannot start: " + err.Error()}
	}

	return checkResult{statusPass, nameServerUpgrade, "this binary can start on the database of the server"}
}

// serverBackupCheck warns when the server has no backup that ended in the last
// 25 hours.
func serverBackupCheck(ctx context.Context, client *http.Client, server, token string) checkResult {
	var doc backupListDocument

	if err := getJSON(ctx, client, backupEndpoint(server, "/api/server/backups"), token, &doc); err != nil {
		return checkResult{statusWarn, nameServerBackup, err.Error()}
	}

	for _, record := range doc.Backups {
		if record.State != "succeeded" {
			continue
		}

		ended, err := time.Parse(time.RFC3339Nano, record.EndedAt)
		if err != nil {
			return checkResult{statusWarn, nameServerBackup, "the last backup has no valid end time"}
		}

		age := time.Since(ended)
		if age >= backupMaxAge {
			return checkResult{
				statusWarn, nameServerBackup,
				fmt.Sprintf("the last backup ended %s ago: run opendustpan backup", age.Round(time.Minute)),
			}
		}

		return checkResult{
			statusPass,
			nameServerBackup,
			fmt.Sprintf("the last backup ended %s ago", age.Round(time.Minute)),
		}
	}

	return checkResult{statusWarn, nameServerBackup, "the server has no successful backup: run opendustpan backup"}
}

// getJSON reads one JSON answer of the installation API with the token. The
// body is capped.
func getJSON(ctx context.Context, client *http.Client, endpoint, token string, doc any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("ask the server: %w: check --server", err)
	}

	backupAuth(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ask the server: %w: check --server", err)
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxClientBodyBytes+1))
	closeErr := resp.Body.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read the answer of the server: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s", ErrBackupRead, answerText(resp.StatusCode, resp.Status, "check the server log"))
	}

	if int64(len(raw)) > MaxClientBodyBytes {
		return fmt.Errorf("%w: over %d bytes", ErrResponseTooLarge, MaxClientBodyBytes)
	}

	if err := json.Unmarshal(raw, doc); err != nil {
		return fmt.Errorf("decode the answer of the server: %w", err)
	}

	return nil
}
