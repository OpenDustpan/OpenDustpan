package client

// Audit chain subcommands: a client of GET /api/dustpan/v1/audit/chain, and the
// approve command that points to the console.
//
//	opendustpan audit-chain status --server https://dustpan.example [--ca-file ca.pem]
//	opendustpan audit-chain approve --server https://dustpan.example [--ca-file ca.pem]
//
// Status prints the stored state of the audit chain, the setting of the
// server that answers, and whether a change waits. Approve sends no request:
// the server approves a change of the audit chain only for a console session
// with a recent sign-in, and the CLI cannot give one, so the command prints
// where to approve and fails.
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
	// ErrAuditChainApproveInConsole reports the approve command: the CLI has
	// no fresh authentication, so it sends no request.
	ErrAuditChainApproveInConsole = errors.New("audit chain approval needs the console")
)

// auditChainApproveInConsole is the text that the approve command prints.
const auditChainApproveInConsole = "Approve the audit chain change in the console. " +
	"The CLI cannot give a fresh authentication."

// auditChainPath is the route of the audit chain commands.
const auditChainPath = "/api/dustpan/v1/audit/chain"

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
	answer, err := callAuditChain(ctx, args, stderr)
	if err != nil {
		return err
	}

	return writeAuditChain(stdout, answer)
}

// AuditChainApprove runs `opendustpan audit-chain approve`. It sends no
// request. The server approves a change of the audit chain only for a console
// session with a recent sign-in, and an access token never gives one, so the
// command prints the console path and fails.
func AuditChainApprove(_ context.Context, _ []string, _, stderr io.Writer) error {
	if _, err := fmt.Fprintln(stderr, auditChainApproveInConsole); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return ErrAuditChainApproveInConsole
}

// callAuditChain parses args, reads the audit chain route and answers the
// state that the server answers.
func callAuditChain(ctx context.Context, args []string, stderr io.Writer) (AuditChainAnswer, error) {
	var (
		f    auditChainFlags
		none AuditChainAnswer
	)

	fs := flag.NewFlagSet("audit-chain status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)

	if err := fs.Parse(args); err != nil {
		return none, fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return none, ErrUsage
	}

	rerun := "run opendustpan audit-chain status again"

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

	return sendAuditChain(ctx, client, auditChainCall{server: server, token: token, rerun: rerun})
}

// auditChainCall is the request of the audit chain status command.
type auditChainCall struct {
	server string
	token  string
	rerun  string
}

// sendAuditChain sends call and decodes the state. The body is capped, so a
// hostile or broken server cannot grow the client without bound.
func sendAuditChain(ctx context.Context, client *http.Client, call auditChainCall) (AuditChainAnswer, error) {
	var none AuditChainAnswer

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		backupEndpoint(call.server, auditChainPath),
		http.NoBody,
	)
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

	if resp.StatusCode != http.StatusOK {
		return none, fmt.Errorf("%w: %s", ErrAuditChainRequest, decodeErrorAnswer(resp))
	}

	var answer AuditChainAnswer

	if err := decodeCappedJSON(resp.Body, MaxClientBodyBytes, &answer); err != nil {
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
