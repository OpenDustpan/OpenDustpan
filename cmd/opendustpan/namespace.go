package main

// Namespace create subcommand of the installation API (ADR 39): a client of
// POST /api/server/namespaces (ADR 17).
//
//	opendustpan namespace create --server https://dustpan.example [--ca-file ca.pem] <name>
//
// The command creates a namespace homed in the region of the server. The
// bootstrap admin token calls it on an empty installation for the first
// namespace. The access token comes from DUSTPAN_ADMIN_TOKEN or from the file
// in DUSTPAN_ADMIN_TOKEN_FILE, never from a flag. When the setting holds
// several tokens, the command uses the first one. A TLS server is trusted
// through the system store with an optional --ca-file bundle. The exit code
// is 0 on success and 1 on any refusal.

import (
	"bytes"
	"context"
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

// Sentinel errors of the namespace create subcommand. Each error text names
// what failed and the one command that helps.
var (
	// errNamespaceNeedsServer reports a namespace create run without --server.
	errNamespaceNeedsServer = errors.New("namespace create needs --server")
	// errNamespaceNoToken reports a namespace create run without an admin token.
	errNamespaceNoToken = errors.New("no admin token is set")
	// errNamespaceCreate reports a namespace that never arrived.
	errNamespaceCreate = errors.New("create namespace failed")
)

// namespaceRequestTimeout bounds one namespace API request.
const namespaceRequestTimeout = 30 * time.Second

// namespaceFlags holds the settings of the namespace create subcommand.
type namespaceFlags struct {
	server string
	caFile string
}

func (f *namespaceFlags) register(fs *flag.FlagSet) {
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

// namespaceCreateRequest is the create body: the name of the namespace.
type namespaceCreateRequest struct {
	Name string `json:"name"`
}

// namespaceCreateResponse answers a created namespace: its name and home
// region with the failover version. The creation time is not printed.
type namespaceCreateResponse struct {
	Name            string `json:"name"`
	HomeRegion      string `json:"homeRegion"`
	FailoverVersion int64  `json:"failoverVersion"`
}

// namespaceErrorResponse is the error body the installation API answers. Only
// the message is read; the code repeats the status.
type namespaceErrorResponse struct {
	Message string `json:"message"`
}

// createNamespace runs `opendustpan namespace create --server <url> <name>`:
// it creates a namespace through the installation API and prints it. It opens
// no database and no store: only the server opens the data (ADR 39).
func createNamespace(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var f namespaceFlags

	fs := flag.NewFlagSet("namespace create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 1 {
		return errUsage
	}

	name := strings.TrimSpace(fs.Arg(0))
	if name == "" {
		return errUsage
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return fmt.Errorf(
			"%w: set --server (%s) to the server base URL and run opendustpan namespace create again",
			errNamespaceNeedsServer,
			backupServerEnvVar,
		)
	}

	token, err := namespaceToken()
	if err != nil {
		return err
	}

	client, err := newBackupHTTPClient(f.caFile)
	if err != nil {
		return err
	}

	doc, err := postNamespace(ctx, client, server, token, name)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(stdout, "created namespace %s in region %s\n", doc.Name, doc.HomeRegion); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// namespaceToken returns the access token from DUSTPAN_ADMIN_TOKEN, or from
// the file in DUSTPAN_ADMIN_TOKEN_FILE when the variable is empty: the first
// token, as the server signs each token in as a distinct administrator. No
// token is an error that names both settings and the one command that helps.
func namespaceToken() (string, error) {
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
		"%w: set %s or %s and run opendustpan namespace create again",
		errNamespaceNoToken,
		adminTokenEnvVar,
		adminTokenFileEnvVar,
	)
}

// postNamespace creates name through POST /api/server/namespaces and answers
// the created namespace.
func postNamespace(
	ctx context.Context, client *http.Client, server, token, name string,
) (namespaceCreateResponse, error) {
	raw, err := json.Marshal(namespaceCreateRequest{Name: name})
	if err != nil {
		return namespaceCreateResponse{}, fmt.Errorf(
			"%w %q: %w: check the name and run opendustpan namespace create --server %s again",
			errNamespaceCreate,
			name,
			err,
			server,
		)
	}

	ctx, cancel := context.WithTimeout(ctx, namespaceRequestTimeout)
	defer cancel()

	req, err := buildNamespaceRequest(ctx, server, token, raw)
	if err != nil {
		return namespaceCreateResponse{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return namespaceCreateResponse{}, fmt.Errorf(
			"%w %q: %w: check --server and run opendustpan namespace create --server %s again",
			errNamespaceCreate,
			name,
			err,
			server,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	return decodeNamespaceResponse(resp, server, name)
}

// buildNamespaceRequest builds the namespace create request for ctx.
func buildNamespaceRequest(
	ctx context.Context, server, token string, raw []byte,
) (*http.Request, error) {
	url := strings.TrimSuffix(server, "/") + "/api/server/namespaces"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf(
			"create namespace: %w: check --server and run opendustpan namespace create --server %s again",
			err,
			server,
		)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

// decodeNamespaceResponse reads a namespace create response: 201 with the
// namespace, else the server error with the one command that helps.
func decodeNamespaceResponse(
	resp *http.Response, server, name string,
) (namespaceCreateResponse, error) {
	if resp.StatusCode != http.StatusCreated {
		return namespaceCreateResponse{}, fmt.Errorf(
			"%w %q: %s: check the server log and run opendustpan namespace create --server %s again",
			errNamespaceCreate,
			name,
			decodeNamespaceError(resp),
			server,
		)
	}

	var doc namespaceCreateResponse

	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return namespaceCreateResponse{}, fmt.Errorf(
			"%w %q: %w: check the server log and run opendustpan namespace create --server %s again",
			errNamespaceCreate,
			name,
			err,
			server,
		)
	}

	if strings.TrimSpace(doc.Name) == "" || strings.TrimSpace(doc.HomeRegion) == "" {
		return namespaceCreateResponse{}, fmt.Errorf(
			"%w %q: the server answered no namespace: check the server log and run opendustpan namespace create --server %s again",
			errNamespaceCreate,
			name,
			server,
		)
	}

	return doc, nil
}

// decodeNamespaceError reads an error body of the installation API: the
// message with its code, or the status alone when the body has no JSON error.
func decodeNamespaceError(resp *http.Response) string {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.Status
	}

	var doc namespaceErrorResponse

	if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil || doc.Message == "" {
		if text := strings.TrimSpace(string(raw)); text != "" {
			return resp.Status + ": " + text
		}

		return resp.Status
	}

	return resp.Status + ": " + doc.Message
}
