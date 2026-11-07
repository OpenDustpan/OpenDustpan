package client

// Placement rule subcommands of the installation API (ADR 17):
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
	"os"
	"strings"
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
	Position   *int   `json:"position,omitempty"`
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
	position   int
}

func (p *placementAddFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&p.tool, "tool", "", "the tool of the rule, for example pulumi")
	fs.StringVar(&p.project, "project", "", "the project pattern of the rule, for example *")
	fs.StringVar(&p.stack, "stack", "", "the stack pattern of the rule, for example *")
	fs.StringVar(&p.collection, "collection", "", "the collection that the rule places units in")
	fs.IntVar(&p.position, "position", 0, "the place of the rule, from 1; the first matching rule wins")
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

	return out, nil
}

// namespaceOperand checks the one namespace argument of a placement command.
// It names the command and its usage when the argument is missing or holds a
// slash.
func namespaceOperand(command string, fs *flag.FlagSet, usage string) (string, error) {
	if fs.NArg() != 1 || fs.Arg(0) == "" || strings.Contains(fs.Arg(0), "/") {
		return "", argumentError(command, "one argument <namespace>", usage)
	}

	return fs.Arg(0), nil
}

// PlacementAdd runs `opendustpan placement add` and prints the created rule.
func PlacementAdd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runPlacementAdd(ctx, args, os.LookupEnv, stdout, stderr)
}

// placementAdd is PlacementAdd with the environment lookup of the admin token
// as an argument.
func runPlacementAdd(ctx context.Context, args []string, lookup envLookup, stdout, stderr io.Writer) error {
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

	token, err := inventoryToken(command, lookup)
	if err != nil {
		return err
	}

	var rule placementRuleEntry

	err = inventoryDo(ctx, inventoryConn{server: server, caFile: f.caFile, token: token}, inventoryCall{
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

	return writeLine(stdout, command,
		"created placement rule %s in namespace %s: %s project %s stack %s to collection %s, position %d\n",
		rule.ID, namespace, rule.Tool, rule.Project, rule.Stack, rule.Collection, rule.Position,
	)
}

// PlacementList runs `opendustpan placement list` and prints one line for each
// rule in list order: position, ID, tool, project, stack and collection.
func PlacementList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runPlacementList(ctx, args, os.LookupEnv, stdout, stderr)
}

// placementList is PlacementList with the environment lookup of the admin
// token as an argument.
func runPlacementList(ctx context.Context, args []string, lookup envLookup, stdout, stderr io.Writer) error {
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

	token, err := inventoryToken(command, lookup)
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
	return runPlacementDelete(ctx, args, os.LookupEnv, stdout, stderr)
}

// placementDelete is PlacementDelete with the environment lookup of the admin
// token as an argument.
func runPlacementDelete(ctx context.Context, args []string, lookup envLookup, stdout, stderr io.Writer) error {
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
	if namespace == "" || ruleID == "" || strings.Contains(namespace, "/") || strings.Contains(ruleID, "/") {
		return argumentError(command, "<namespace> and <rule-id> without slashes", usage)
	}

	server, err := f.checkServer(command)
	if err != nil {
		return err
	}

	token, err := inventoryToken(command, lookup)
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
