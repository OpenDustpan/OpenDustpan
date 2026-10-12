package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// envPattern finds environment names in Go string literals: a DUSTPAN_
// setting the code reads, prints or sets.
var envPattern = regexp.MustCompile(`DUSTPAN_[A-Z0-9_]+`)

// TestEveryEnvVarHasADeclaration scans the Go sources for DUSTPAN_ names
// and proves each one has a setting declaration, so no setting is read
// without its flag, default and help in one place. Test-only names
// (DUSTPAN_TEST_*, DUSTPAN_BLOBTEST_*) are fixtures, not settings.
func TestEveryEnvVarHasADeclaration(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	found := map[string]bool{}

	for _, dir := range []string{"cmd", "internal", "pkg", "e2e"} {
		collectEnvNames(t, filepath.Join(root, dir), found)
	}

	declared := map[string]bool{}
	for _, env := range cli.DeclaredEnvs() {
		declared[env] = true
	}

	var missing []string

	for name := range found {
		if strings.HasPrefix(name, "DUSTPAN_TEST_") || strings.HasPrefix(name, "DUSTPAN_BLOBTEST_") {
			continue
		}

		if !declared[name] {
			missing = append(missing, name)
		}
	}

	slices.Sort(missing)

	if len(missing) > 0 {
		t.Fatalf("settings without a declaration: %s", strings.Join(missing, ", "))
	}
}

// collectEnvNames adds every DUSTPAN_ name in the string literals of the Go
// files under dir to found. Comments never add names: only literals reach
// the code that reads them.
func collectEnvNames(t *testing.T, dir string, found map[string]bool) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			collectEnvNames(t, path, found)

			continue
		}

		if strings.HasSuffix(entry.Name(), ".go") {
			collectFileEnvNames(t, path, found)
		}
	}
}

// collectFileEnvNames adds every DUSTPAN_ name in the string literals of
// the Go file at path to found.
func collectFileEnvNames(t *testing.T, path string, found map[string]bool) {
	t.Helper()

	node, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	ast.Inspect(node, func(n ast.Node) bool {
		literal, ok := n.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}

		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}

		for _, name := range envPattern.FindAllString(value, -1) {
			found[name] = true
		}

		return true
	})
}
