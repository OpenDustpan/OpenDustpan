package client

// Audit chain subcommands: clients of GET and POST /api/server/audit/chain.
//
//	opendustpan audit-chain status --server https://dustpan.example [--ca-file ca.pem]
//	opendustpan audit-chain approve --server https://dustpan.example [--ca-file ca.pem]
//
// Status prints the stored state of the audit chain, the setting of the
// server that answers, and whether a change waits. Approve approves the
// change to the setting of the server that answers, and the server
// migrates the audit log in the background.
//
// This file is client code: it must not import a store adapter, a blob
// adapter, backup-target code or key-encryption key code.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
)

// Sentinel errors of the audit chain subcommands.
var (
	// ErrAuditChainNeedsServer reports an audit chain command without
	// --server.
	ErrAuditChainNeedsServer = errors.New("audit-chain needs --server")
	// ErrAuditChainRequest reports a failed audit chain request.
	ErrAuditChainRequest = errors.New("audit chain request failed")
)

// auditChainPath is the route of the audit chain commands.
const auditChainPath = "/api/server/audit/chain"

// AuditChainAnswer is the state of the audit chain that the server answers.
type AuditChainAnswer struct {
	State     string `json:"state"`
	Setting   string `json:"setting"`
	ChangedAt string `json:"changedAt"`
	ChangedBy string `json:"changedBy"`
	Pending   bool   `json:"pending"`
	Migrating bool   `json:"migrating"`
}

// auditChainFlags holds the settings of the audit chain subcommands. The
// token comes from DUSTPAN_ADMIN_TOKEN, never from a flag.
type auditChainFlags struct {
	server string
	caFile string
}

// AuditChainStatus runs `opendustpan audit-chain status --server <url>`: it
// prints the state of the audit chain.
func AuditChainStatus(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	answer, err := callAuditChain(ctx, args, stderr, http.MethodGet, "status")
	if err != nil {
		return err
	}

	return writeAuditChain(stdout, answer)
}

// AuditChainApprove runs `opendustpan audit-chain approve --server <url>`:
// it approves the change to the setting of the server, and prints the state.
func AuditChainApprove(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	answer, err := callAuditChain(ctx, args, stderr, http.MethodPost, "approve")
	if err != nil {
		return err
	}

	const approved = "approved the change of the audit chain to %s; the migration runs\n"
	if _, err := fmt.Fprintf(stdout, approved, answer.Setting); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return writeAuditChain(stdout, answer)
}

// callAuditChain parses args, sends one request with method to the audit
// chain route and answers the state that the server answers.
func callAuditChain(
	ctx context.Context, args []string, stderr io.Writer, method, sub string,
) (AuditChainAnswer, error) {
	var (
		f    auditChainFlags
		none AuditChainAnswer
	)

	fs := flag.NewFlagSet("audit-chain "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)

	if err := fs.Parse(args); err != nil {
		return none, fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return none, ErrUsage
	}

	rerun := "run opendustpan audit-chain " + sub + " again"

	server := strings.TrimSpace(f.server)
	if server == "" {
		return none, fmt.Errorf("%w: set --server (%s) to the server base URL and %s",
			ErrAuditChainNeedsServer, BackupServerEnvVar, rerun)
	}

	if err := cli.ByFlag("server").CheckURL(server); err != nil {
		return none, err
	}

	token, err := BackupToken()
	if err != nil {
		return none, err
	}

	client, err := newBackupHTTPClient(f.caFile, rerun, stderr)
	if err != nil {
		return none, err
	}

	return sendAuditChain(ctx, client, auditChainCall{server: server, token: token, method: method, rerun: rerun})
}

// auditChainCall is one request of an audit chain command.
type auditChainCall struct {
	server string
	token  string
	method string
	rerun  string
}

// sendAuditChain sends call and decodes the state. The body is capped, so a
// hostile or broken server cannot grow the client without bound.
func sendAuditChain(ctx context.Context, client *http.Client, call auditChainCall) (AuditChainAnswer, error) {
	var none AuditChainAnswer

	req, err := http.NewRequestWithContext(ctx, call.method, backupEndpoint(call.server, auditChainPath), http.NoBody)
	if err != nil {
		return none, fmt.Errorf("%w: %w: check --server and %s", ErrAuditChainRequest, err, call.rerun)
	}

	backupAuth(req, call.token)

	resp, err := client.Do(req)
	if err != nil {
		return none, fmt.Errorf("%w: %w: check --server and %s", ErrAuditChainRequest, err, call.rerun)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return none, fmt.Errorf("%w: %s", ErrAuditChainRequest, decodeBackupError(resp))
	}

	var answer AuditChainAnswer

	if err := decodeCappedJSON(resp.Body, &answer); err != nil {
		return none, fmt.Errorf("%w: %w: check the server log and %s", ErrAuditChainRequest, err, call.rerun)
	}

	return answer, nil
}

// writeAuditChain prints the state of the audit chain.
func writeAuditChain(stdout io.Writer, answer AuditChainAnswer) error {
	var waits string

	switch {
	case answer.Migrating:
		waits = "the migration runs"
	case answer.Pending:
		waits = "a change waits for approval"
	default:
		waits = "no change waits"
	}

	_, err := fmt.Fprintf(stdout, "audit chain: stored state %s, setting of this server %s, %s\n",
		answer.State, answer.Setting, waits)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	if answer.ChangedBy != "" {
		if _, err := fmt.Fprintf(stdout, "last change: %s by %s\n", answer.ChangedAt, answer.ChangedBy); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}
