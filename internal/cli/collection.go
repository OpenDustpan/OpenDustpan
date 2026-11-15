package cli

// Collection subcommands of the installation API:
//
//	opendustpan collection create --server URL [--ca-file FILE] <namespace>/<name>
//	opendustpan collection list --server URL [--ca-file FILE] <namespace>
//
// create is a client of POST /api/dustpan/v1/namespaces/{namespace}/collections
// and answers 201. list is a client of GET on the same route and answers 200.
// The commands print what the server did. The exit code is 0 on success and 1
// on any refusal.

import (
	"bytes"
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
	"path/filepath"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	namespacedomain "opendustpan.org/opendustpan/pkg/namespace"
)

// Shared request code of the collection and placement commands. Each command
// is a client of a route of the installation API under /api/dustpan/v1.
// The access token comes from DUSTPAN_ADMIN_TOKEN or from the file
// in DUSTPAN_ADMIN_TOKEN_FILE, never from a flag, as for namespace create.

const (
	// inventoryRequestTimeout bounds one request of the inventory commands.
	inventoryRequestTimeout = 30 * time.Second

	// inventoryAPIPrefix is the path prefix of the inventory routes that the
	// commands use.
	inventoryAPIPrefix = "/api/dustpan/v1"

	// maxInventoryIDLength bounds stable collection and placement rule IDs.
	maxInventoryIDLength = 64

	// inventoryMaxAnswerBytes caps one installation API answer that a command
	// reads.
	inventoryMaxAnswerBytes = 1 << 20

	// inventoryServerEnvVar sets the default --server value.
	inventoryServerEnvVar = "DUSTPAN_SERVER"

	// inventoryTokenFilePrivateBits are the permission bits a token file must
	// not set: group or other access of any kind.
	inventoryTokenFilePrivateBits = 0o077
)

// Sentinels of the inventory commands. Each error text names what failed and
// the one command that helps.
var (
	// ErrInventoryArguments reports a command run with a missing or invalid argument or flag.
	ErrInventoryArguments = errors.New("invalid arguments")
	// ErrInventoryNeedsServer reports a command run without --server.
	ErrInventoryNeedsServer = errors.New("needs --server")
	// ErrInventoryNoToken reports a command run without an admin token.
	ErrInventoryNoToken = errors.New("no admin token is set")
	// ErrInventoryRequest reports a request that did not get the answer that it expects.
	ErrInventoryRequest = errors.New("request failed")
	// ErrInventoryTooLarge reports a server answer above the cap of the read.
	ErrInventoryTooLarge = errors.New("server answered more data than the client reads")
	// errInventoryTokenFilePermissions reports a token file that group or other users can access.
	errInventoryTokenFilePermissions = errors.New("admin token file allows group or other access")
	// errInventoryNoCA reports a CA file that holds no certificate.
	errInventoryNoCA = errors.New("the file holds no certificate")
	// errInventoryTransport reports an HTTP transport that the client cannot clone.
	errInventoryTransport = errors.New("no HTTP transport")
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
	ByFlag("server").String(fs, &f.server)
	ByFlag("ca-file").String(fs, &f.caFile)
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
			inventoryServerEnvVar,
			command,
		)
	}

	if err := ByFlag("server").CheckURL(server); err != nil {
		return "", err
	}

	parsed, err := url.Parse(server)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", argumentError(command, "an HTTP or HTTPS server URL without a query or fragment", "--help")
	}

	return server, nil
}

// readAdminTokenFile reads the tokens in the file at path. Like the
// key-encryption key file, it refuses a file that group or other users can
// access.
func readAdminTokenFile(path string) ([]string, error) {
	// Defense in depth: the operator names the path. The root of its
	// directory keeps the read inside that directory, and the stat below
	// refuses a file that group or other users can access.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: open file: %w", "DUSTPAN_ADMIN_TOKEN_FILE", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: close file: %w", "DUSTPAN_ADMIN_TOKEN_FILE", closeErr))
		}
	}()

	info, err := root.Stat(filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("%s: stat file: %w", "DUSTPAN_ADMIN_TOKEN_FILE", err)
	}

	if perm := info.Mode().Perm(); perm&inventoryTokenFilePrivateBits != 0 {
		return nil, fmt.Errorf(
			"%s: %w: %q (mode %04o): run chmod 600 on the file",
			"DUSTPAN_ADMIN_TOKEN_FILE",
			errInventoryTokenFilePermissions,
			path,
			perm,
		)
	}

	raw, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("%s: read file: %w", "DUSTPAN_ADMIN_TOKEN_FILE", err)
	}

	var tokens []string

	for line := range strings.SplitSeq(string(raw), "\n") {
		for entry := range strings.SplitSeq(line, ",") {
			if token := strings.TrimSpace(entry); token != "" {
				tokens = append(tokens, token)
			}
		}
	}

	if len(tokens) == 0 {
		return nil, fmt.Errorf(
			"%s: %w: the file %q holds no token",
			"DUSTPAN_ADMIN_TOKEN_FILE",
			ErrInventoryNoToken,
			path,
		)
	}

	return tokens, nil
}

// loadAdminTokens reads the admin tokens from the process environment, as
// namespace create does: the variable holds comma-separated tokens; the file
// holds tokens separated by commas or new lines.
func loadAdminTokens() ([]string, error) {
	if raw := os.Getenv("DUSTPAN_ADMIN_TOKEN"); strings.TrimSpace(raw) != "" {
		return strings.Split(raw, ","), nil
	}

	if path := strings.TrimSpace(os.Getenv("DUSTPAN_ADMIN_TOKEN_FILE")); path != "" {
		return readAdminTokenFile(path)
	}

	return nil, fmt.Errorf(
		"%w: set %s or %s",
		ErrInventoryNoToken,
		"DUSTPAN_ADMIN_TOKEN",
		"DUSTPAN_ADMIN_TOKEN_FILE",
	)
}

// inventoryToken returns the access token of the command from
// DUSTPAN_ADMIN_TOKEN, or from the file in DUSTPAN_ADMIN_TOKEN_FILE when the
// variable is empty: the first token, as namespace create does.
func inventoryToken(command string) (string, error) {
	tokens, err := loadAdminTokens()
	if err != nil && !errors.Is(err, ErrInventoryNoToken) {
		return "", err
	}

	for _, entry := range tokens {
		if token := strings.TrimSpace(entry); token != "" {
			if strings.ContainsAny(token, "\r\n") {
				return "", argumentError(command, "an admin token without line breaks", "--help")
			}
			return token, nil
		}
	}

	return "", fmt.Errorf(
		"%w: set %s or %s and run opendustpan %s again",
		ErrInventoryNoToken,
		"DUSTPAN_ADMIN_TOKEN",
		"DUSTPAN_ADMIN_TOKEN_FILE",
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

// inventoryHTTPClient answers a client for one command run: with the trust
// pool from caFile when it is set, the system pool otherwise. rerun names the
// step that helps after a refusal.
func inventoryHTTPClient(caFile, rerun string) (*http.Client, error) {
	if strings.TrimSpace(caFile) == "" {
		return &http.Client{Timeout: inventoryRequestTimeout}, nil
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
			errInventoryNoCA,
			rerun,
		)
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: check --ca-file and %s", errInventoryTransport, rerun)
	}

	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}

	return &http.Client{Timeout: inventoryRequestTimeout, Transport: transport}, nil
}

// inventoryDo sends call over conn and checks the status of the answer. A
// status other than want answers the server error, with the status and the
// message of the server.
func inventoryDo(ctx context.Context, conn inventoryConn, call inventoryCall) error {
	client, err := inventoryHTTPClient(conn.caFile, "run opendustpan "+call.command+" again")
	if err != nil {
		return err
	}

	// Defense in depth: URL checks do not contain a redirect. Do not send
	// the admin token to another route or host through a redirect.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
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
	// Each call owns one connection. Do not leave an idle connection after
	// the command returns.
	req.Close = true

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
			inventoryAnswerText(resp.StatusCode, decodeInventoryError(resp), hint),
		)
	}

	if call.out == nil {
		return nil
	}

	if err := decodeCappedInventoryJSON(resp.Body, inventoryMaxAnswerBytes, call.out); err != nil {
		return fmt.Errorf(
			"%w: %s: %w: the server answered, so check the server log",
			ErrInventoryRequest,
			call.command,
			err,
		)
	}

	return nil
}

// decodeInventoryError keeps the stable error code with the server message.
func decodeInventoryError(resp *http.Response) string {
	// Defense in depth: a server can send controls in the status text,
	// independently of its JSON error fields.
	status := cleanInventoryText(resp.Status)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, inventoryMaxAnswerBytes+1))
	if err != nil || len(raw) > inventoryMaxAnswerBytes {
		return status
	}

	var doc struct {
		Message string `json:"message"`
		Code    string `json:"errorCode"`
	}
	if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil || doc.Message == "" {
		if text := strings.TrimSpace(string(raw)); text != "" {
			return status + ": " + cleanInventoryText(text)
		}
		return status
	}
	if doc.Code == "" {
		return status + ": " + cleanInventoryText(doc.Message)
	}
	return status + ": " + cleanInventoryText(doc.Code) + ": " + cleanInventoryText(doc.Message)
}

// inventoryAnswerText returns the text of a server answer with an unexpected
// status: message, which holds the status and the message of the server once,
// and then hint, the step that can help. A 4xx answer has its cause in the
// request, so the text of a 4xx answer has no hint.
func inventoryAnswerText(status int, message, hint string) string {
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		return message
	}

	return message + ": " + hint
}

// decodeCappedInventoryJSON decodes one installation API answer from r,
// reading at most limit bytes. A larger body is an error, never a silent
// truncation.
func decodeCappedInventoryJSON(r io.Reader, limit int64, doc any) error {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return fmt.Errorf("read server answer: %w", err)
	}

	if int64(len(raw)) > limit {
		return fmt.Errorf("%w: over %d bytes", ErrInventoryTooLarge, limit)
	}

	if err := json.Unmarshal(raw, doc); err != nil {
		return fmt.Errorf("decode server answer: %w", err)
	}

	return nil
}

func cleanInventoryText(value string) string {
	return strings.Map(func(r rune) rune {
		if ident.IsHiddenRune(r) {
			return -1
		}
		return r
	}, value)
}

func validCollectionID(id string) bool {
	return strings.HasPrefix(id, "col-") && len(id) > len("col-") && ident.Valid(id, maxInventoryIDLength)
}

// inventoryPath returns the route below inventoryAPIPrefix for the segments,
// with each segment escaped as a path segment.
func inventoryPath(segments ...string) string {
	var b strings.Builder

	b.WriteString(inventoryAPIPrefix)

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

// collectionEntry is one collection as the installation API answers it.
type collectionEntry struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// collectionCreateBody is the body of a collection create: the name.
type collectionCreateBody struct {
	Name string `json:"name"`
}

// collectionListAnswer is the answered document of a collection list.
type collectionListAnswer struct {
	Collections []collectionEntry `json:"collections"`
}

// collectionOperand is the <namespace>/<name> operand of a collection create.
type collectionOperand struct {
	namespace string
	name      string
}

// parseCollectionOperand reads the operand <namespace>/<name>. Each part must
// be non-empty, and the name must not hold a slash.
func parseCollectionOperand(operand string) (collectionOperand, error) {
	const usage = "--server <url> <namespace>/<name>"

	namespace, name, ok := strings.Cut(operand, "/")
	if !ok || namespace == "" || name == "" || strings.Contains(name, "/") {
		return collectionOperand{}, argumentError(
			"collection create",
			fmt.Sprintf("<namespace>/<name> as its argument, got %q", operand),
			usage,
		)
	}

	if err := checkCollectionNames(namespace, name); err != nil {
		return collectionOperand{}, argumentError("collection create", err.Error(), usage)
	}

	return collectionOperand{namespace: namespace, name: name}, nil
}

func checkCollectionNames(ns, name string) error {
	if _, err := namespacedomain.ParseName(ns); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	if _, err := collection.ParseName(name); err != nil {
		return fmt.Errorf("collection: %w", err)
	}
	return nil
}

// CollectionCreateCommand answers the collection create row of the command
// table with run: a client of POST /api/dustpan/v1/namespaces/{namespace}/collections.
func CollectionCreateCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandCollection, commandCreate},
		Synopsis: "Create a collection in a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}

// CollectionListCommand answers the collection list row of the command table
// with run: a client of GET /api/dustpan/v1/namespaces/{namespace}/collections.
func CollectionListCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandCollection, commandList},
		Synopsis: "List the collections of a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}

// CollectionCreate runs `opendustpan collection create --server <url> <namespace>/<name>`
// and prints the created collection.
func CollectionCreate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	const (
		command = "collection create"
		usage   = "--server <url> <namespace>/<name>"
	)

	var f serverFlags

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 1 {
		return argumentError(command, "one argument <namespace>/<name>", usage)
	}

	server, err := f.checkServer(command)
	if err != nil {
		return err
	}

	operand, err := parseCollectionOperand(fs.Arg(0))
	if err != nil {
		return err
	}

	token, err := inventoryToken(command)
	if err != nil {
		return err
	}

	var created collectionEntry

	err = inventoryDo(ctx, inventoryConn{server: server, caFile: f.caFile, token: token}, inventoryCall{
		command: command,
		method:  http.MethodPost,
		path:    inventoryPath("namespaces", operand.namespace, "collections"),
		body:    collectionCreateBody{Name: operand.name},
		want:    http.StatusCreated,
		out:     &created,
	})
	if err != nil {
		return err
	}

	if !validCollectionID(created.ID) {
		return fmt.Errorf("%w: no stable collection ID in the server answer: run opendustpan collection list",
			ErrInventoryRequest)
	}
	return writeLine(stdout, command, "created collection %s/%s\t%s\n", operand.namespace, operand.name, created.ID)
}

// CollectionList runs `opendustpan collection list --server <url> <namespace>`
// and prints the ID and <namespace>/<name> of each collection.
func CollectionList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	const (
		command = "collection list"
		usage   = "--server <url> <namespace>"
	)

	var f serverFlags

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	ns, err := namespaceOperand(command, fs, usage)
	if err != nil {
		return err
	}

	server, err := f.checkServer(command)
	if err != nil {
		return err
	}

	token, err := inventoryToken(command)
	if err != nil {
		return err
	}

	var doc collectionListAnswer

	err = inventoryDo(ctx, inventoryConn{server: server, caFile: f.caFile, token: token}, inventoryCall{
		command: command,
		method:  http.MethodGet,
		path:    inventoryPath("namespaces", ns, "collections"),
		body:    nil,
		want:    http.StatusOK,
		out:     &doc,
	})
	if err != nil {
		return err
	}

	for _, c := range doc.Collections {
		if !validCollectionID(c.ID) {
			return fmt.Errorf("%w: no stable collection ID in the server answer: run opendustpan collection list",
				ErrInventoryRequest)
		}
		if err := checkCollectionNames(ns, c.Name); err != nil {
			return fmt.Errorf("%w: %w: run opendustpan collection list", ErrInventoryRequest, err)
		}
	}
	for _, c := range doc.Collections {
		if err := writeLine(stdout, command, "%s\t%s/%s\n", c.ID, ns, c.Name); err != nil {
			return err
		}
	}

	return nil
}
