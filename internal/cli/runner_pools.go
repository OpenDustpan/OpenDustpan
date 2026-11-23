package cli

// Runner pool subcommands of the installation API:
//
//	opendustpan runner pools list --namespace <ns> [--output json]
//
// list is a client of GET /api/dustpan/v1/namespaces/{namespace}/runner-pools
// and answers 200. The command prints one line for each pool, with the
// columns name, id, purpose, concurrency and isolation separated by one tab,
// sorted by name. --output json prints the server answer unchanged. The exit
// code is 0 on success and 1 on any refusal.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	namespacedomain "opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// runnerPoolsCommand is the command path of the runner pool list, as the
// errors name it.
const runnerPoolsCommand = "runner pools list"

var (
	errAnswerDuplicateKey = errors.New("the server answer has a duplicate key")
	errAnswerKeyNotText   = errors.New("the server answer has a key that is not text")
	errAnswerUnknownKey   = errors.New("the server answer has an unknown top-level key")
)

// poolEntry is one pool as the management API answers it. The command reads
// only the fields that it prints, so a new field of the answer changes
// nothing here.
type poolEntry struct {
	ID          string   `json:"id"`
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Purpose     string   `json:"purpose"`
	Concurrency int      `json:"concurrency"`
	Isolation   []string `json:"isolation"`
}

// poolListAnswer is the answered document of a pool list.
type poolListAnswer struct {
	Pools []poolEntry `json:"pools"`
}

// poolLine is one printed row of a pool: the columns in print order.
type poolLine struct {
	name        string
	id          string
	purpose     string
	concurrency int
	isolation   string
}

// RunnerPoolsListCommand answers the runner pools list row of the command
// table with run: a client of
// GET /api/dustpan/v1/namespaces/{namespace}/runner-pools.
func RunnerPoolsListCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandRunner, commandPools, commandList},
		Synopsis: "List the runner pools of a namespace on the server.",
		Settings: SettingsFor("server", "ca-file", "namespace", "output"),
		Run:      run,
	}
}

// runnerPoolsFlags holds the parsed flags of the runner pools list command.
type runnerPoolsFlags struct {
	server string
	caFile string
	ns     string
	output string
}

// parseRunnerPoolsFlags parses args and validates the flags. It answers the
// server URL, the namespace, the output format and the CA file.
func parseRunnerPoolsFlags(args []string, stderr io.Writer) (runnerPoolsFlags, string, error) {
	fs := flag.NewFlagSet(runnerPoolsCommand, flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f runnerPoolsFlags

	ByFlag("server").String(fs, &f.server)
	ByFlag("ca-file").String(fs, &f.caFile)
	ByFlag("namespace").String(fs, &f.ns)
	ByFlag("output").String(fs, &f.output)

	if err := fs.Parse(args); err != nil {
		return f, "", fmt.Errorf("parse flags: %w", err)
	}

	usage := "--namespace <namespace> [--output json]"

	if fs.NArg() != 0 {
		return f, "", argumentError(runnerPoolsCommand, "no argument", usage)
	}

	namespace := strings.TrimSpace(f.ns)
	if namespace == "" {
		return f, "", argumentError(runnerPoolsCommand, "--namespace <namespace>", usage)
	}

	if _, err := namespacedomain.ParseName(namespace); err != nil {
		return f, "", argumentError(runnerPoolsCommand, "a valid --namespace", usage)
	}

	format := strings.TrimSpace(f.output)
	if format != "" && format != "json" {
		return f, "", argumentError(runnerPoolsCommand, "--output json", usage)
	}

	return f, namespace, nil
}

// RunnerPoolsList runs `opendustpan runner pools list --namespace <ns>` and
// prints one line for each pool of the namespace, sorted by name.
func RunnerPoolsList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f, namespace, err := parseRunnerPoolsFlags(args, stderr)
	if err != nil {
		return err
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return fmt.Errorf(
			"%w: %s needs --server: set --server (%s) to the server base URL and run opendustpan %s again",
			ErrInventoryNeedsServer, runnerPoolsCommand, inventoryServerEnvVar, runnerPoolsCommand,
		)
	}

	if checkErr := ByFlag("server").CheckURL(server); checkErr != nil {
		return checkErr
	}

	token, err := inventoryToken(runnerPoolsCommand)
	if err != nil {
		return err
	}

	// Defense in depth: keep the whole answer, so that --output json
	// prints exactly what the server sent. The read cap still bounds it.
	var raw json.RawMessage

	err = inventoryDo(ctx, inventoryConn{server: server, caFile: f.caFile, token: token}, inventoryCall{
		command: runnerPoolsCommand,
		method:  http.MethodGet,
		path:    inventoryPath("namespaces", namespace, "runner-pools"),
		body:    nil,
		want:    http.StatusOK,
		out:     &raw,
	})
	if err != nil {
		return err
	}

	// The answer is checked in both output formats before anything prints.
	lines, err := validatePools(raw, namespace)
	if err != nil {
		return err
	}

	// Defense in depth: --output json prints the raw bytes, not the decoded
	// fields that checkPool validates. This check stays so that a hidden rune
	// in a field that the command does not decode, or between two tokens,
	// cannot reach the terminal. It does not rely on the field checks or on
	// the server.
	err = checkRawText(raw)
	if err != nil {
		return err
	}

	if strings.TrimSpace(f.output) == "json" {
		return writeLine(stdout, runnerPoolsCommand, "%s\n", strings.TrimSpace(string(raw)))
	}

	return printPools(stdout, lines)
}

// validatePools decodes the raw server answer and validates each pool of the
// namespace. It answers the rows in print order.
func validatePools(raw []byte, namespace string) ([]poolLine, error) {
	var doc poolListAnswer

	if err := decodeAnswer(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: %s: %w: run opendustpan %s again",
			ErrInventoryRequest, runnerPoolsCommand, err, runnerPoolsCommand)
	}

	// Defense in depth: json.Unmarshal keeps the last value of a duplicate
	// key and matches a key without regard to case, but --output json prints
	// the raw answer with each value. This check stays so that the printed
	// answer cannot hold a pools list or a pool field that the field checks
	// did not see. It does not rely on the server.
	if err := checkUniqueKeys(raw); err != nil {
		return nil, fmt.Errorf("%w: %s: %w: run opendustpan %s again",
			ErrInventoryRequest, runnerPoolsCommand, err, runnerPoolsCommand)
	}

	// Defense in depth: a valid JSON value that is not the answer document
	// is an error, not an empty list. Check the shape before it prints.
	if doc.Pools == nil {
		return nil, fmt.Errorf("%w: %s: the server answer has no pools list: run opendustpan %s again",
			ErrInventoryRequest, runnerPoolsCommand, runnerPoolsCommand)
	}

	return checkPools(doc, namespace)
}

// checkRawText refuses a raw server answer that is not valid UTF-8 or that
// holds a hidden rune other than a line feed or a tab.
func checkRawText(raw []byte) error {
	hidden := func(r rune) bool { return ident.IsHiddenRuneExcept(r, '\n', '\t') }

	if !utf8.Valid(raw) || bytes.ContainsFunc(raw, hidden) {
		return fmt.Errorf("%w: %s: the server answer has invalid UTF-8 or a hidden character: run opendustpan %s again",
			ErrInventoryRequest, runnerPoolsCommand, runnerPoolsCommand)
	}

	return nil
}

// answerTopKey is the only key of the top-level answer object, in its exact
// case.
const answerTopKey = "pools"

// keyFrame is one open object or array of the answer during the key check.
type keyFrame struct {
	object  bool
	wantKey bool
	// keys maps the case-folded form of each key of the object to the key.
	keys map[string]string
}

// checkUniqueKeys refuses an answer that holds one key two times in one
// object, at any depth. It compares the keys with strings.EqualFold, because
// encoding/json matches a field name without regard to case. It also refuses
// a top-level key other than "pools" in its exact case. A top-level value
// that is not an object has no keys: the shape check refuses it.
func checkUniqueKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))

	var checker keyChecker

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("decode server answer: %w", err)
		}

		if err := checker.step(tok); err != nil {
			return err
		}
	}
}

// keyChecker follows the tokens of the answer and checks each key. The stack
// is a slice, not recursion, so a deep answer cannot use up the goroutine
// stack.
type keyChecker struct {
	stack []*keyFrame
}

// step checks one token of the answer.
func (c *keyChecker) step(tok json.Token) error {
	if len(c.stack) > 0 {
		if top := c.stack[len(c.stack)-1]; top.object && top.wantKey {
			return c.key(top, tok)
		}
	}

	switch tok {
	case json.Delim('{'):
		c.stack = append(c.stack, &keyFrame{object: true, wantKey: true, keys: make(map[string]string)})
	case json.Delim('['):
		c.stack = append(c.stack, &keyFrame{object: false, wantKey: false, keys: nil})
	case json.Delim(']'), json.Delim('}'):
		c.closeFrame()
	default:
		c.valueDone()
	}

	return nil
}

// key checks tok in the place of a key of the object frame top. It refuses a
// key that matches an earlier key of the object with strings.EqualFold, and a
// key of the top-level object other than answerTopKey.
func (c *keyChecker) key(top *keyFrame, tok json.Token) error {
	if tok == json.Delim('}') {
		c.closeFrame()

		return nil
	}

	// Defense in depth: the decoder returns only string keys; this check stays
	// so that a later change cannot skip it.
	key, ok := tok.(string)
	if !ok {
		return errAnswerKeyNotText
	}

	folded := foldKey(key)

	if earlier, found := top.keys[folded]; found && strings.EqualFold(earlier, key) {
		return errAnswerDuplicateKey
	}

	if len(c.stack) == 1 && key != answerTopKey {
		return errAnswerUnknownKey
	}

	top.keys[folded] = key
	top.wantKey = false

	return nil
}

// closeFrame removes the innermost frame and marks it as a complete value of
// its parent.
func (c *keyChecker) closeFrame() {
	if len(c.stack) == 0 {
		return
	}

	c.stack = c.stack[:len(c.stack)-1]
	c.valueDone()
}

// valueDone marks the end of one value. In an object, the next token is a
// key or the end of the object.
func (c *keyChecker) valueDone() {
	if len(c.stack) > 0 && c.stack[len(c.stack)-1].object {
		c.stack[len(c.stack)-1].wantKey = true
	}
}

// foldKey answers one form for all the keys that strings.EqualFold matches:
// each rune becomes the smallest rune of its simple case-folding orbit.
func foldKey(key string) string {
	var b strings.Builder

	for _, r := range key {
		smallest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			smallest = min(smallest, f)
		}

		b.WriteRune(smallest)
	}

	return b.String()
}

// printPools prints one line per pool.
func printPools(stdout io.Writer, lines []poolLine) error {
	for _, line := range lines {
		writeErr := writeLine(stdout, runnerPoolsCommand, "%s\t%s\t%s\t%d\t%s\n",
			line.name, line.id, line.purpose, line.concurrency, line.isolation)
		if writeErr != nil {
			return writeErr
		}
	}

	return nil
}

// decodeAnswer decodes the raw answer of the server into doc.
func decodeAnswer(raw []byte, doc *poolListAnswer) error {
	if err := json.Unmarshal(raw, doc); err != nil {
		return fmt.Errorf("decode server answer: %w", err)
	}

	return nil
}

// checkPools checks each pool of the answer before the command prints one
// line, and answers the rows in print order: sorted by name. An unknown value
// is an error, not partial output.
func checkPools(doc poolListAnswer, namespace string) ([]poolLine, error) {
	lines := make([]poolLine, 0, len(doc.Pools))

	for _, p := range doc.Pools {
		line, err := checkPool(p)
		if err != nil {
			return nil, err
		}

		// Defense in depth: the request path names the namespace, and the
		// server reads only that namespace. This check stays so that a pool
		// of another namespace in the answer cannot print as a pool of
		// --namespace. It does not rely on the server.
		if p.Namespace != namespace {
			return nil, fmt.Errorf("%w: runner pool of another namespace in the server answer: run opendustpan %s",
				ErrInventoryRequest, runnerPoolsCommand)
		}

		lines = append(lines, line)
	}

	slices.SortStableFunc(lines, func(a, b poolLine) int { return strings.Compare(a.name, b.name) })

	return lines, nil
}

// checkPool validates one pool of the answer and answers its printed row.
func checkPool(p poolEntry) (poolLine, error) {
	// Defense in depth: flag checks do not validate a server answer.
	// Check each field before it becomes terminal output.
	if !strings.HasPrefix(p.ID, runner.PoolIDPrefix) || len(p.ID) == len(runner.PoolIDPrefix) ||
		!ident.Valid(p.ID, maxInventoryIDLength) {
		return poolLine{}, fmt.Errorf("%w: invalid runner pool in the server answer: run opendustpan %s",
			ErrInventoryRequest, runnerPoolsCommand)
	}

	if !runner.ValidName(p.Name) {
		return poolLine{}, fmt.Errorf("%w: invalid runner pool name in the server answer: run opendustpan %s",
			ErrInventoryRequest, runnerPoolsCommand)
	}

	purpose := runner.Purpose(p.Purpose)
	if !purpose.Valid() {
		return poolLine{}, fmt.Errorf("%w: unknown runner pool purpose in the server answer: run opendustpan %s",
			ErrInventoryRequest, runnerPoolsCommand)
	}

	if p.Concurrency < 1 || p.Concurrency > runner.MaxConcurrency {
		return poolLine{}, fmt.Errorf("%w: invalid runner pool concurrency in the server answer: run opendustpan %s",
			ErrInventoryRequest, runnerPoolsCommand)
	}

	isolation := make([]string, 0, len(p.Isolation))
	for _, kind := range p.Isolation {
		if !runner.Isolation(kind).Valid() {
			return poolLine{}, fmt.Errorf(
				"%w: unknown runner pool isolation in the server answer: run opendustpan %s",
				ErrInventoryRequest, runnerPoolsCommand,
			)
		}

		isolation = append(isolation, kind)
	}

	return poolLine{
		name:        p.Name,
		id:          p.ID,
		purpose:     p.Purpose,
		concurrency: p.Concurrency,
		isolation:   strings.Join(isolation, ","),
	}, nil
}
