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

// sensitiveWords find a secret, a credential or a URL in the text of a
// declaration.
var sensitiveWords = regexp.MustCompile(`(?i)\burl\b|://|token|secret|password|passphrase|credential`)

// TestSensitiveSettingsAreMarked proves every setting that holds a secret, a
// credential or a URL is marked sensitive, so no log line shows its value
// (ADR 63 decision 6). A string declaration holds one when its flag, its
// variable, its help or its reference names one. A variable with no flag
// holds a secret unless its name ends in _FILE: a value that no flag takes is
// a secret by design. A path (a name that ends in -file or _FILE), a bool and
// an int hold no secret.
func TestSensitiveSettingsAreMarked(t *testing.T) {
	t.Parallel()

	for _, s := range cli.Settings {
		if s.Kind != cli.KindString {
			continue
		}

		isPath := strings.HasSuffix(s.Flag, "-file") || strings.HasSuffix(s.Env, "_FILE")

		holds := !isPath && sensitiveWords.MatchString(s.Flag+" "+s.Env+" "+s.Help+" "+s.Reference)

		if s.Flag == "" && !isPath {
			holds = true
		}

		if holds && !s.Sensitive {
			t.Errorf("setting %s holds a secret, a credential or a URL and has no sensitive mark", s.Name())
		}
	}
}

// TestSecretsHaveNoFlag proves a secret never comes from a flag (ADR 63
// decision 4): a sensitive setting with no URL in its declaration has no
// flag and no file key. The database URL and the other URLs keep the flags
// that they had before ADR 63.
func TestSecretsHaveNoFlag(t *testing.T) {
	t.Parallel()

	url := regexp.MustCompile(`(?i)\burl\b|://`)

	for _, s := range cli.Settings {
		if !s.Sensitive || url.MatchString(s.Help+" "+s.Reference) {
			continue
		}

		if s.Flag != "" || s.File != "" {
			t.Errorf("secret %s has a flag or a file key", s.Name())
		}
	}
}

// TestFileKeysAreUnique proves no two declarations share a file key, and
// that each serve flag has one.
func TestFileKeysAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, s := range cli.Settings {
		if s.File == "" {
			continue
		}

		if seen[s.File] {
			t.Errorf("file key %s is declared twice", s.File)
		}

		seen[s.File] = true
	}

	for _, s := range cli.ServeFlags() {
		if s.File == "" && s.Flag != "config" {
			t.Errorf("serve flag --%s has no file key", s.Flag)
		}
	}
}
