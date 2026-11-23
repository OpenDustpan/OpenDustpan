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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	namespacedomain "opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// runnerPoolsCommand is the command path of the runner pool list, as the
// errors name it.
const runnerPoolsCommand = "runner pools list"

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
	lines, err := validatePools(raw)
	if err != nil {
		return err
	}

	if strings.TrimSpace(f.output) == "json" {
		return writeLine(stdout, runnerPoolsCommand, "%s\n", strings.TrimSpace(string(raw)))
	}

	return printPools(stdout, lines)
}

// validatePools decodes the raw server answer and validates each pool. It
// answers the rows in print order.
func validatePools(raw []byte) ([]poolLine, error) {
	var doc poolListAnswer

	if err := decodeAnswer(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: %s: %w: run opendustpan %s again",
			ErrInventoryRequest, runnerPoolsCommand, err, runnerPoolsCommand)
	}

	// Defense in depth: a valid JSON value that is not the answer document
	// is an error, not an empty list. Check the shape before it prints.
	if doc.Pools == nil {
		return nil, fmt.Errorf("%w: %s: the server answer has no pools list: run opendustpan %s again",
			ErrInventoryRequest, runnerPoolsCommand, runnerPoolsCommand)
	}

	return checkPools(doc)
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
func checkPools(doc poolListAnswer) ([]poolLine, error) {
	lines := make([]poolLine, 0, len(doc.Pools))

	for _, p := range doc.Pools {
		line, err := checkPool(p)
		if err != nil {
			return nil, err
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
