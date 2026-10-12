package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestClientCommandsImportNoStores proves no non-serve command file imports a
// store, a blob adapter or target, or the key-encryption-key code: only the
// serve wiring opens the data (ADR 39), so a future client command cannot
// reintroduce the offline restore path that ADR 39 deleted. It reads the
// package sources with go/parser, so it needs no build tags and runs with the
// unit tests.
func TestClientCommandsImportNoStores(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read command directory: %v", err)
	}

	for _, entry := range entries {
		checkCommandFile(t, entry)
	}
}

// checkCommandFile proves one directory entry imports no offline dependency.
// Directories, non-Go files, and the serve wiring in main.go are exempt: the
// serve command opens the live stores.
func checkCommandFile(t *testing.T, entry os.DirEntry) {
	t.Helper()

	name := entry.Name()

	if entry.IsDir() || !strings.HasSuffix(name, ".go") || name == "main.go" || name == "main_test.go" {
		return
	}

	path := filepath.Join(".", name)

	raw, err := os.ReadFile(path) //nolint:gosec // The test reads its own package sources.
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), name, raw, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}

	checkCommandImports(t, name, file)
}

// checkCommandImports proves no import of file names a store, a blob
// adapter, or another offline dependency. pkg/store/schema stays allowed:
// it is the neutral migration list both dialects and the clients share,
// with no driver in it (ADR 39 decision 4).
func checkCommandImports(t *testing.T, name string, file *ast.File) {
	t.Helper()

	prefixes := []string{
		"opendustpan.org/opendustpan/pkg/store/",
		"opendustpan.org/opendustpan/pkg/blob/",
		"opendustpan.org/opendustpan/pkg/secret",
	}

	const schemaImport = "opendustpan.org/opendustpan/pkg/store/schema"

	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("decode import in %s: %v", name, err)
		}

		if importPath == schemaImport {
			continue
		}

		for _, prefix := range prefixes {
			if strings.HasPrefix(importPath, prefix) {
				t.Errorf(
					"%s imports %q: only the serve wiring in main.go may open a store",
					name,
					importPath,
				)
			}
		}
	}
}
