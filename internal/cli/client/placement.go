package client

// Placement rule subcommands of the installation API:
//
//	opendustpan placement add --server URL [--ca-file FILE] --tool TOOL \
//	  --project PATTERN --stack PATTERN --collection NAME [--position N] <namespace>
//	opendustpan placement list --server URL [--ca-file FILE] <namespace>
//	opendustpan placement delete --server URL [--ca-file FILE] <namespace> <rule-id>
//
// The flags of add map one to one to the fields of the rule body: --tool,
// --project, --stack, --collection and --position. Position is sent only when
// the flag is set, so the server keeps its default place for the rule. The
// commands are clients of the placement-rules route of the namespace. The exit
// code is 0 on success and 1 on any refusal.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	namespacedomain "opendustpan.org/opendustpan/pkg/namespace"
)

// placementDeleteOperands is the number of operands of placement delete: the
// namespace and the rule ID.
const placementDeleteOperands = 2

// placementRuleBody is the body of a rule create. Position is nil when the
// flag is not set, and then the field is left out of the body.
type placementRuleBody struct {
	Tool       string `json:"tool"`
	Project    string `json:"project"`
	Stack      string `json:"stack"`
	Collection string `json:"collection"`
	Position   *int64 `json:"position,omitempty"`
}

// placementRuleEntry is one rule as the installation API answers it.
type placementRuleEntry struct {
	ID         string `json:"id"`
	Namespace  string `json:"namespace"`
	Position   int    `json:"position"`
	Tool       string `json:"tool"`
	Project    string `json:"project"`
	Stack      string `json:"stack"`
	Collection string `json:"collection"`
}

// placementListAnswer is the answered document of a rule list.
type placementListAnswer struct {
	Rules []placementRuleEntry `json:"rules"`
}

// placementAddFlags holds the flags of placement add. Each one maps to the
// field of the rule body with the same meaning.
type placementAddFlags struct {
	tool       string
	project    string
	stack      string
	collection string
	position   int64
}

func (p *placementAddFlags) register(fs *flag.FlagSet) {
	cli.ByFlag("tool").String(fs, &p.tool)
	cli.ByFlag("project").String(fs, &p.project)
	cli.ByFlag("stack").String(fs, &p.stack)
	cli.ByFlag("collection").String(fs, &p.collection)
	cli.ByFlag("position").Int64(fs, &p.position)
}

// body returns the rule body of the flags. It names each required flag that
// is empty. Position is set only when the flag was given on the command line.
func (p *placementAddFlags) body(fs *flag.FlagSet, command, usage string) (placementRuleBody, error) {
	required := []struct{ name, value string }{
		{"--tool", p.tool},
		{"--project", p.project},
		{"--stack", p.stack},
		{"--collection", p.collection},
	}

	for _, req := range required {
		if strings.TrimSpace(req.value) == "" {
			return placementRuleBody{}, argumentError(command, req.name, usage)
		}
	}

	if err := p.check(); err != nil {
		return placementRuleBody{}, argumentError(command, err.Error(), usage)
	}

	out := placementRuleBody{
		Tool:       p.tool,
		Project:    p.project,
		Stack:      p.stack,
		Collection: p.collection,
		Position:   nil,
	}

	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "position" {
			position := p.position
			out.Position = &position
		}
	})

	if out.Position != nil && *out.Position < 1 {
		return placementRuleBody{}, argumentError(command, "--position of 1 or more", usage)
	}

	return out, nil
}

func (p *placementAddFlags) check() error {
	if p.tool != collection.ToolPulumi && p.tool != collection.ToolOpenTofu {
		return collection.ErrInvalidPlacementTool
	}
	if _, err := collection.ParsePattern(p.project); err != nil {
		return fmt.Errorf("project: %w", err)
	}
	if _, err := collection.ParsePattern(p.stack); err != nil {
		return fmt.Errorf("stack: %w", err)
	}
	if !validCollectionID(p.collection) {
		if _, err := collection.ParseName(p.collection); err != nil {
			return fmt.Errorf("collection: %w", err)
		}
	}
	return nil
}

// namespaceOperand checks the one namespace argument of a placement command.
// It names the command and its usage when the argument is missing or holds a
// slash.
func namespaceOperand(command string, fs *flag.FlagSet, usage string) (string, error) {
	if fs.NArg() != 1 {
		return "", argumentError(command, "one argument <namespace>", usage)
	}

	if _, err := namespacedomain.ParseName(fs.Arg(0)); err != nil {
		return "", argumentError(command, "a valid <namespace>", usage)
	}

	return fs.Arg(0), nil
}

// PlacementAdd runs `opendustpan placement add` and prints the created rule.
func PlacementAdd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	const (
		command = "placement add"
		usage   = "--server <url> --tool <tool> --project <pattern> --stack <pattern> " +
			"--collection <name> [--position <n>] <namespace>"
	)

	var (
		f  serverFlags
		pf placementAddFlags
	)

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)
	pf.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	namespace, err := namespaceOperand(command, fs, usage)
	if err != nil {
		return err
	}

	body, err := pf.body(fs, command, usage)
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

	var rule placementRuleEntry

	conn := inventoryConn{server: server, caFile: f.caFile, token: token}
	body.Collection, err = resolvePlacementCollection(ctx, conn, namespace, body.Collection, stderr)
	if err != nil {
		return err
	}

	err = inventoryDo(ctx, conn, inventoryCall{
		command: command,
		method:  http.MethodPost,
		path:    inventoryPath("namespaces", namespace, "placement-rules"),
		body:    body,
		want:    http.StatusCreated,
		out:     &rule,
	}, stderr)
	if err != nil {
		return err
	}

	if err := rule.checkAnswer(command); err != nil {
		return err
	}

	return writeLine(stdout, command,
		"created placement rule %s in namespace %s: %s project %s stack %s to collection %s, position %d\n",
		rule.ID, namespace, rule.Tool, rule.Project, rule.Stack, rule.Collection, rule.Position,
	)
}

func (r placementRuleEntry) checkAnswer(command string) error {
	// Defense in depth: flag checks do not validate a server answer.
	// Check each field before it becomes terminal output or a rule ID to delete.
	if !strings.HasPrefix(r.ID, "plr-") || !ident.Valid(r.ID, maxInventoryIDLength) ||
		r.Position < 1 || !validCollectionID(r.Collection) {
		return fmt.Errorf("%w: invalid placement rule in the server answer: run opendustpan %s",
			ErrInventoryRequest, command)
	}
	flags := placementAddFlags{
		tool: r.Tool, project: r.Project, stack: r.Stack, collection: r.Collection, position: 0,
	}
	if err := flags.check(); err != nil {
		return fmt.Errorf("%w: %w: run opendustpan %s", ErrInventoryRequest, err, command)
	}
	return nil
}

func resolvePlacementCollection(
	ctx context.Context, conn inventoryConn, ns, name string, stderr io.Writer,
) (string, error) {
	var doc collectionListAnswer
	err := inventoryDo(ctx, conn, inventoryCall{
		command: "placement add", method: http.MethodGet,
		path: inventoryPath("namespaces", ns, "collections"), body: nil, want: http.StatusOK, out: &doc,
	}, stderr)
	if err != nil {
		return "", err
	}
	// An exact ID takes priority over a name that has the same text.
	for _, c := range doc.Collections {
		if c.ID == name && validCollectionID(c.ID) {
			return c.ID, nil
		}
	}
	for _, c := range doc.Collections {
		if !strings.EqualFold(c.Name, name) {
			continue
		}
		// Defense in depth: command flags cannot validate a server answer.
		// A missing or invalid ID must never become a name-based rule.
		if !validCollectionID(c.ID) {
			return "", fmt.Errorf("%w: no stable collection ID in the server answer: run opendustpan collection list",
				ErrInventoryRequest)
		}
		return c.ID, nil
	}
	return "", fmt.Errorf("%w: collection does not exist: run opendustpan collection list --server %s %s",
		ErrInventoryArguments, conn.server, ns)
}

// PlacementList runs `opendustpan placement list` and prints one line for each
// rule in list order: position, ID, tool, project, stack and collection.
func PlacementList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	const (
		command = "placement list"
		usage   = "--server <url> <namespace>"
	)

	var f serverFlags

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	namespace, err := namespaceOperand(command, fs, usage)
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

	var doc placementListAnswer

	err = inventoryDo(ctx, inventoryConn{server: server, caFile: f.caFile, token: token}, inventoryCall{
		command: command,
		method:  http.MethodGet,
		path:    inventoryPath("namespaces", namespace, "placement-rules"),
		body:    nil,
		want:    http.StatusOK,
		out:     &doc,
	}, stderr)
	if err != nil {
		return err
	}

	for _, r := range doc.Rules {
		if err := r.checkAnswer(command); err != nil {
			return err
		}
	}
	for _, r := range doc.Rules {
		line := fmt.Sprintf("%d\t%s\t%s\t%s\t%s\t%s\n",
			r.Position, r.ID, r.Tool, r.Project, r.Stack, r.Collection)

		if err := writeLine(stdout, command, "%s", line); err != nil {
			return err
		}
	}

	return nil
}

// PlacementDelete runs `opendustpan placement delete <namespace> <rule-id>`. It
// prints the deleted rule. The units that the rule placed keep their
// collections.
func PlacementDelete(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	const (
		command = "placement delete"
		usage   = "--server <url> <namespace> <rule-id>"
	)

	var f serverFlags

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != placementDeleteOperands {
		return argumentError(command, "two arguments <namespace> <rule-id>", usage)
	}

	namespace, ruleID := fs.Arg(0), fs.Arg(1)
	if !ident.Valid(namespace, namespacedomain.MaxNameLength) || !ident.Valid(ruleID, maxInventoryIDLength) {
		return argumentError(command, "a valid <namespace> and <rule-id>", usage)
	}

	server, err := f.checkServer(command)
	if err != nil {
		return err
	}

	token, err := inventoryToken(command)
	if err != nil {
		return err
	}

	err = inventoryDo(ctx, inventoryConn{server: server, caFile: f.caFile, token: token}, inventoryCall{
		command: command,
		method:  http.MethodDelete,
		path:    inventoryPath("namespaces", namespace, "placement-rules", ruleID),
		body:    nil,
		want:    http.StatusNoContent,
		out:     nil,
	}, stderr)
	if err != nil {
		return err
	}

	return writeLine(stdout, command, "deleted placement rule %s in namespace %s\n", ruleID, namespace)
}
