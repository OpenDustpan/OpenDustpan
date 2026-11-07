package client

// Shared request code of the collection and placement commands. Each command
// is a client of one route of the installation API under /api/dustpan/v1
// (ADR 17). The access token comes from DUSTPAN_ADMIN_TOKEN or from the file
// in DUSTPAN_ADMIN_TOKEN_FILE, never from a flag, as for namespace create.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
)

// inventoryRequestTimeout bounds one request of the inventory commands.
const inventoryRequestTimeout = 30 * time.Second

// apiPrefix is the path prefix of the inventory routes that the commands use.
const apiPrefix = "/api/dustpan/v1"

// Sentinel errors of the inventory commands. Each error text names what
// failed and the one command that helps.
var (
	// ErrInventoryArguments reports a command run with a missing or invalid argument or flag.
	ErrInventoryArguments = errors.New("invalid arguments")
	// ErrInventoryNeedsServer reports a command run without --server.
	ErrInventoryNeedsServer = errors.New("needs --server")
	// ErrInventoryNoToken reports a command run without an admin token.
	ErrInventoryNoToken = errors.New("no admin token is set")
	// ErrInventoryRequest reports a request that did not get the answer that it expects.
	ErrInventoryRequest = errors.New("request failed")
)

// argumentError names what a command needs and the one command that helps.
// usage is the flags and operands of the command, without its name.
func argumentError(command, need, usage string) error {
	return fmt.Errorf("%w: %s needs %s: run opendustpan %s %s again",
		ErrInventoryArguments, command, need, command, usage)
}

// serverFlags holds the --server and --ca-file settings of the inventory
// commands.
type serverFlags struct {
	server string
	caFile string
}

func (f *serverFlags) register(fs *flag.FlagSet) {
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)
}

// checkServer returns the trimmed server URL of the command. An empty value
// and a value that is not a URL are errors that name the command.
func (f *serverFlags) checkServer(command string) (string, error) {
	server := strings.TrimSpace(f.server)
	if server == "" {
		return "", fmt.Errorf(
			"%w: %s needs --server: set --server (%s) to the server base URL and run opendustpan %s again",
			ErrInventoryNeedsServer,
			command,
			BackupServerEnvVar,
			command,
		)
	}

	if err := cli.ByFlag("server").CheckURL(server); err != nil {
		return "", err
	}

	return server, nil
}

// envLookup reads one environment variable, as os.LookupEnv does. The commands
// take it as an argument so that the tests need no change to the process
// environment.
type envLookup func(key string) (string, bool)

// inventoryToken returns the access token of the command from
// DUSTPAN_ADMIN_TOKEN, or from the file in DUSTPAN_ADMIN_TOKEN_FILE when the
// variable is empty: the first token, as namespace create does.
func inventoryToken(command string, lookup envLookup) (string, error) {
	tokens, err := LoadAdminTokensFrom(lookup)
	if err != nil && !errors.Is(err, ErrAdminTokenMissing) {
		return "", err
	}

	for _, entry := range tokens {
		if token := strings.TrimSpace(entry); token != "" {
			return token, nil
		}
	}

	return "", fmt.Errorf(
		"%w: set %s or %s and run opendustpan %s again",
		ErrInventoryNoToken,
		AdminTokenEnvVar,
		AdminTokenFileEnvVar,
		command,
	)
}

// inventoryConn is the server and the credentials of one command run.
type inventoryConn struct {
	server string
	caFile string
	token  string
}

// inventoryCall is one request of an inventory command.
type inventoryCall struct {
	// command is the command path, for example "collection create". It names
	// the command in each error.
	command string
	method  string
	// path is the route after the server base URL, for example
	// /api/dustpan/v1/namespaces/acme/collections.
	path string
	// body is the value that is encoded as the JSON body. Nil sends no body.
	body any
	// want is the status of a successful answer.
	want int
	// out receives the decoded answer. Nil skips the decoding.
	out any
}

// inventoryDo sends call over conn and checks the status of the answer. A
// status other than want answers the server error, with the status and the
// message of the server.
func inventoryDo(ctx context.Context, conn inventoryConn, call inventoryCall, stderr io.Writer) error {
	client, err := newBackupHTTPClient(conn.caFile, "run opendustpan "+call.command+" again", stderr)
	if err != nil {
		return err
	}

	var reader io.Reader

	if call.body != nil {
		raw, marshalErr := json.Marshal(call.body)
		if marshalErr != nil {
			return fmt.Errorf("%w: %s: encode the request: %w", ErrInventoryRequest, call.command, marshalErr)
		}

		reader = bytes.NewReader(raw)
	}

	ctx, cancel := context.WithTimeout(ctx, inventoryRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, call.method, strings.TrimSuffix(conn.server, "/")+call.path, reader)
	if err != nil {
		return fmt.Errorf(
			"%w: %s: %w: check --server and run opendustpan %s again",
			ErrInventoryRequest,
			call.command,
			err,
			call.command,
		)
	}

	req.Header.Set("Authorization", "Bearer "+conn.token)

	if call.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf(
			"%w: %s: %w: check --server and run opendustpan %s again",
			ErrInventoryRequest,
			call.command,
			err,
			call.command,
		)
	}

	return readInventory(resp, call)
}

// readInventory reads the answer of call and closes its body. It checks the
// status, decodes the body into call.out, and reports a close error when
// nothing else failed.
func readInventory(resp *http.Response, call inventoryCall) error {
	err := checkInventoryAnswer(resp, call)

	if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("%w: %s: close the answer: %w", ErrInventoryRequest, call.command, closeErr)
	}

	return err
}

// checkInventoryAnswer checks the status of resp against call.want and decodes
// the body into call.out.
func checkInventoryAnswer(resp *http.Response, call inventoryCall) error {
	if resp.StatusCode != call.want {
		hint := "check the server log and run opendustpan " + call.command + " again"

		return fmt.Errorf(
			"%w: %s: %s",
			ErrInventoryRequest,
			call.command,
			answerText(resp.StatusCode, decodeErrorAnswer(resp), hint),
		)
	}

	if call.out == nil {
		return nil
	}

	if err := decodeCappedJSON(resp.Body, MaxClientBodyBytes, call.out); err != nil {
		return fmt.Errorf(
			"%w: %s: %w: the server answered, so check the server log",
			ErrInventoryRequest,
			call.command,
			err,
		)
	}

	return nil
}

// inventoryPath returns the route below apiPrefix for the segments, with each
// segment escaped as a path segment.
func inventoryPath(segments ...string) string {
	var b strings.Builder

	b.WriteString(apiPrefix)

	for _, seg := range segments {
		b.WriteString("/")
		b.WriteString(url.PathEscape(seg))
	}

	return b.String()
}

// writeLine prints one line of a command report and returns the write error.
func writeLine(w io.Writer, command, format string, args ...any) error {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return fmt.Errorf("%s: write output: %w", command, err)
	}

	return nil
}
