package client

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
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/collection"
	namespacedomain "opendustpan.org/opendustpan/pkg/namespace"
)

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
	}, stderr)
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
	}, stderr)
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
