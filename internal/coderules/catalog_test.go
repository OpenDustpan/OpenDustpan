package coderules_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	catalogPath = "../../docs/reference/code-rules.md"
	// codeRuleMark is the text that a sentinel error carries in its comment
	// when the catalog must list it.
	codeRuleMark = "Code rule:"
)

var catalogMarker = regexp.MustCompile(`<!-- code-rule: ([A-Za-z0-9_]+\.[A-Za-z0-9_]+) -->`)

// markedSentinels returns "package.Name" for each package-level declaration
// under pkg/ and internal/ whose comment holds codeRuleMark.
func markedSentinels(t *testing.T) []string {
	t.Helper()

	var out []string

	fset := token.NewFileSet()

	for _, root := range []string{"../../pkg", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walk %s: %w", path, err)
			}

			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			names, err := markedInFile(fset, path)
			out = append(out, names...)

			return err
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	slices.Sort(out)

	return out
}

// markedInFile returns the marked declarations of one Go file.
func markedInFile(fset *token.FileSet, path string) ([]string, error) {
	src, err := os.ReadFile(path) //nolint:gosec // path comes from the walk of the repository tree
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if !strings.Contains(string(src), codeRuleMark) {
		return nil, nil
	}

	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return marked(file), nil
}

func marked(file *ast.File) []string {
	var out []string

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || vs.Doc == nil || !strings.Contains(vs.Doc.Text(), codeRuleMark) {
				continue
			}

			for _, name := range vs.Names {
				out = append(out, file.Name.Name+"."+name.Name)
			}
		}
	}

	return out
}

func catalogEntries(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}

	matches := catalogMarker.FindAllStringSubmatch(string(body), -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}

	slices.Sort(out)

	return out
}

// TestCatalogMatchesCode fails when a sentinel marked as a code rule has no
// entry in the catalog, and when an entry names no marked sentinel.
func TestCatalogMatchesCode(t *testing.T) {
	t.Parallel()

	code := slices.Compact(markedSentinels(t))
	docs := catalogEntries(t)

	if len(code) == 0 {
		t.Fatal("no sentinel carries the mark " + codeRuleMark)
	}

	for _, name := range code {
		if !slices.Contains(docs, name) {
			t.Errorf("%s is marked as a code rule but the catalog has no entry: add <!-- code-rule: %s -->", name, name)
		}
	}

	for _, name := range docs {
		if !slices.Contains(code, name) {
			t.Errorf("docs/reference/code-rules.md lists %s but no sentinel carries the mark %q", name, codeRuleMark)
		}
	}
}
