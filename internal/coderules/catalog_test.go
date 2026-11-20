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
// under pkg/ and internal/ whose comment holds codeRuleMark. The catalog names
// a sentinel by the name of its package, so it fails the test when two
// directories with the same package name mark the same name: one catalog
// entry would match both, and a missing entry of the other would not show.
func markedSentinels(t *testing.T) []string {
	t.Helper()

	var out []string

	dirOf := map[string]string{}

	fset := token.NewFileSet()

	repo, err := os.OpenRoot("../..")
	if err != nil {
		t.Fatalf("open the repository root: %v", err)
	}

	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("close the repository root: %v", err)
		}
	})

	repoFS := repo.FS()

	for _, root := range []string{"pkg", "internal"} {
		err := fs.WalkDir(repoFS, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walk %s: %w", path, err)
			}

			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			names, err := markedInFile(repoFS, fset, path)
			checkOneDir(t, dirOf, names, filepath.Dir(path))

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

// checkOneDir fails the test for a name of names that dirOf holds with a
// directory other than dir, and records dir for each name.
func checkOneDir(t *testing.T, dirOf map[string]string, names []string, dir string) {
	t.Helper()

	for _, name := range names {
		if other, seen := dirOf[name]; seen && other != dir {
			t.Errorf("%s is marked in %s and in %s: the catalog cannot tell them apart; rename one", name, other, dir)
		}

		dirOf[name] = dir
	}
}

// markedInFile returns the marked declarations of one Go file.
func markedInFile(repoFS fs.FS, fset *token.FileSet, path string) ([]string, error) {
	// The read goes through os.Root, so a path cannot leave the repository.
	src, err := fs.ReadFile(repoFS, path)
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
			if !ok || !strings.Contains(specDoc(gen, vs).Text(), codeRuleMark) {
				continue
			}

			for _, name := range vs.Names {
				out = append(out, file.Name.Name+"."+name.Name)
			}
		}
	}

	return out
}

// specDoc returns the comment of one value declaration. The parser puts the
// comment of a declaration with no parentheses, for example a one-line
// `var X = ...`, on the GenDecl and not on its ValueSpec. The comment of a
// block stays out: it is not the comment of each value in the block.
func specDoc(gen *ast.GenDecl, vs *ast.ValueSpec) *ast.CommentGroup {
	if vs.Doc != nil {
		return vs.Doc
	}

	if !gen.Lparen.IsValid() {
		return gen.Doc
	}

	return nil
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

// TestMarkedFindsEachForm proves that the scan finds a marked sentinel in a
// var block and in a one-line var declaration, and does not give the comment
// of a block to each value in it.
func TestMarkedFindsEachForm(t *testing.T) {
	t.Parallel()

	const src = `package demo

import "errors"

// ErrOneLine is a sentinel.
//
// Code rule: the catalog lists it.
var ErrOneLine = errors.New("one line")

var (
	// ErrInBlock is a sentinel.
	//
	// Code rule: the catalog lists it.
	ErrInBlock = errors.New("in block")
)

// Code rule: this comment is on the block, not on a value.
var (
	ErrUnmarked = errors.New("unmarked")
)
`

	file, err := parser.ParseFile(token.NewFileSet(), "demo.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := marked(file)
	want := []string{"demo.ErrOneLine", "demo.ErrInBlock"}

	if !slices.Equal(got, want) {
		t.Fatalf("marked = %v, want %v", got, want)
	}
}
