package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// loopbackSeams are the names of the seam that opens the loopback interface to
// a connection to a Git host. Only tests may use them.
var loopbackSeams = map[string]bool{"LoopbackForTests": true, "WithGitHostLoopbackForTests": true}

// TestNoSettingReachesTheGitHostLoopbackSeam proves that no flag, environment
// variable or configuration file key declares a way to open the loopback
// interface to a Git host. The settings table is the only source of those
// three, so a new one must show up here.
func TestNoSettingReachesTheGitHostLoopbackSeam(t *testing.T) {
	t.Parallel()

	for _, s := range cli.Settings {
		for _, name := range []string{s.Flag, s.Env, s.File} {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "loopback") || strings.Contains(lower, "githost-local") {
				t.Errorf("the setting %q names a loopback seam", name)
			}
		}
	}
}

// TestProductionCodeNeverNamesTheGitHostLoopbackSeam parses each file of the
// binary and of the settings package that is not a test, and fails on a name
// of the seam. Code in these directories is the only code that turns a flag, a
// variable or a key into an option of the server.
func TestProductionCodeNeverNamesTheGitHostLoopbackSeam(t *testing.T) {
	t.Parallel()

	var files []string

	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "cli")} {
		found, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}

		files = append(files, found...)
	}

	checked := 0

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		checked++

		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		fset := token.NewFileSet()

		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && loopbackSeams[id.Name] {
				t.Errorf("%s names the test seam %s", fset.Position(id.Pos()), id.Name)
			}

			return true
		})
	}

	if checked < 10 {
		t.Fatalf("scanned %d files, want the production files of both directories", checked)
	}
}

// TestTheLoopbackSeamScanSeesTheSeam is the positive control of the scan: a
// source that names the seam must fail the same walk.
func TestTheLoopbackSeamScanSeesTheSeam(t *testing.T) {
	t.Parallel()

	src := "package x\nvar _ = dustpan.WithGitHostLoopbackForTests()\n"

	file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	found := false

	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && loopbackSeams[id.Name] {
			found = true
		}

		return true
	})

	if !found {
		t.Fatal("the scan did not find the seam in a source that names it")
	}
}
