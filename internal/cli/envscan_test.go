package cli_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// envReaderNames are the functions of package os that read one environment
// variable by name. A read through a lookup function that the code passes
// around is not in the scan; the variables of such a read (the S3 credentials)
// are in cli.ExternalSecretEnvs, and the serve-level test covers them.
func envReaderNames() []string { return []string{"Getenv", "LookupEnv"} }

// readsAnyEnvAllowed names each variable that the code reads and that is
// neither a Setting nor an external secret, with the reason. A variable that
// holds a secret does not belong here: declare it as a sensitive Setting, or
// add it to cli.ExternalSecretEnvs so that the server clears it.
func readsAnyEnvAllowed() map[string]string {
	return map[string]string{
		"PATH":                           "the search path of the operating system; it holds no secret",
		"USER":                           "the operating system user name; it holds no secret",
		"DUSTPAN_DEV":                    "the marker that dev sets for its own process; it holds no secret",
		"DUSTPAN_TEST_POSTGRES_TEMPLATE": "the name of a test database template; test helper, it holds no secret",
		"DUSTPAN_TEST_POSTGRES_URL":      "the address of a test database; test helper, never set for serve",
		"DUSTPAN_HELPER_SOCKET":          "the path of the launcher socket; it holds no secret",
	}
}

// dynamicEnvReadsAllowed names each file that reads a variable with a name that
// the scan cannot resolve, with the reason. The name comes from the caller
// there, and the scan checks the literal at the caller.
func dynamicEnvReadsAllowed() map[string]string {
	return map[string]string{
		"internal/cli/setting.go":                             "the layered lookup of the settings; its names come from the Settings table",
		"internal/clientgen/conformance/process.go":           "the test process passes the variables that its caller lists",
		"internal/clientgen/conformance/goclient/goclient.go": "the test process passes the variables that its caller lists",
		"internal/clientgen/conformance/tsclient/tsclient.go": "the test process passes the variables that its caller lists",
	}
}

// envRead is one read of a variable name in the source.
type envRead struct {
	name string
	site string
}

// TestEveryEnvReadIsDeclaredOrAllowed is the guard against a missed secret
// variable. It parses the code of cmd, internal and pkg, finds each read of an
// environment variable by a literal name or a constant name, and requires the
// name to be a declared Setting, an external secret that the server clears,
// or an entry of readsAnyEnvAllowed. A name that the test cannot resolve fails
// too: the server cannot clear a variable that nobody lists.
func TestEveryEnvReadIsDeclaredOrAllowed(t *testing.T) {
	t.Parallel()

	reads, unresolved := scanEnvReads(t, "../..")

	if len(reads) < 5 {
		t.Fatalf("the scan found %d reads, want at least 5: the scan is broken", len(reads))
	}

	declared := cli.DeclaredEnvs()
	external := cli.ExternalSecretEnvs()

	for _, read := range reads {
		if slices.Contains(declared, read.name) || slices.Contains(external, read.name) {
			continue
		}

		if reason, ok := readsAnyEnvAllowed()[read.name]; ok && reason != "" {
			continue
		}

		t.Errorf(
			"%s reads %s: declare it as a setting, add it to ExternalSecretEnvs or to readsAnyEnvAllowed",
			read.site,
			read.name,
		)
	}

	for _, site := range unresolved {
		file, _, _ := strings.Cut(strings.TrimPrefix(site, "../../"), ":")
		if reason, ok := dynamicEnvReadsAllowed()[file]; ok && reason != "" {
			continue
		}

		t.Errorf("%s reads a variable with a name the scan cannot resolve: use a literal or a constant", site)
	}
}

// scanEnvReads parses each non-test Go file under the cmd, internal and pkg
// directories of root. It answers the reads with a resolved name, and the
// sites of the reads with an unresolved name.
func scanEnvReads(t *testing.T, root string) ([]envRead, []string) {
	t.Helper()

	fset := token.NewFileSet()

	var files []*ast.File

	for _, dir := range []string{"cmd", "internal", "pkg"} {
		found, err := parseDir(fset, filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}

		files = append(files, found...)
	}

	consts := stringConsts(files)

	var (
		reads      []envRead
		unresolved []string
	)

	for _, file := range files {
		fileReads, fileUnresolved := readsOf(fset, file, consts)
		reads = append(reads, fileReads...)
		unresolved = append(unresolved, fileUnresolved...)
	}

	return reads, unresolved
}

// parseDir parses each non-test Go file under dir.
func parseDir(fset *token.FileSet, dir string) ([]*ast.File, error) {
	var files []*ast.File

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}

		files = append(files, file)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}

	return files, nil
}

// readsOf answers the reads of one file: the reads with a resolved name, and
// the sites of the reads with an unresolved name.
func readsOf(fset *token.FileSet, file *ast.File, consts map[string]constExpr) ([]envRead, []string) {
	var (
		reads      []envRead
		unresolved []string
	)

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 || !isEnvReader(call.Fun) {
			return true
		}

		site := fset.Position(call.Pos()).String()

		name, resolved := resolveName(call.Args[0], file.Name.Name, consts, 0)

		switch {
		case resolved:
			reads = append(reads, envRead{name: name, site: site})
		case looksLikeName(call.Args[0]):
			unresolved = append(unresolved, site)
		default:
			// A call with another first argument is not a read by name.
		}

		return true
	})

	return reads, unresolved
}

// isEnvReader reports whether fun is os.Getenv or os.LookupEnv.
func isEnvReader(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)

	return ok && pkg.Name == "os" && slices.Contains(envReaderNames(), sel.Sel.Name)
}

// looksLikeName reports whether the argument can be a variable name that the
// scan failed to resolve: an identifier or a selector. A call or a literal of
// another kind is not a name.
func looksLikeName(arg ast.Expr) bool {
	switch arg.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return true
	default:
		return false
	}
}

// constExpr is the expression of a constant, with the package that holds it.
type constExpr struct {
	expr ast.Expr
	pkg  string
}

// maxConstDepth stops a loop of constants that name each other.
const maxConstDepth = 8

// resolveName answers the string that expr names: a string literal, or a
// constant of the same package or of another package, by package name. A
// constant that names another constant resolves through it.
func resolveName(expr ast.Expr, pkg string, consts map[string]constExpr, depth int) (string, bool) {
	if depth > maxConstDepth {
		return "", false
	}

	var key string

	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}

		value, err := strconv.Unquote(e.Value)

		return value, err == nil
	case *ast.Ident:
		key = pkg + "." + e.Name
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}

		key = x.Name + "." + e.Sel.Name
	default:
		return "", false
	}

	target, ok := consts[key]
	if !ok {
		return "", false
	}

	return resolveName(target.expr, target.pkg, consts, depth+1)
}

// stringConsts answers each package-level constant, keyed by the package name
// and the constant name.
func stringConsts(files []*ast.File) map[string]constExpr {
	out := map[string]constExpr{}

	for _, file := range files {
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.CONST {
				addConsts(out, file.Name.Name, gen)
			}
		}
	}

	return out
}

// addConsts adds the constants of one declaration to out.
func addConsts(out map[string]constExpr, pkg string, gen *ast.GenDecl) {
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok || len(value.Names) != len(value.Values) {
			continue
		}

		for i, name := range value.Names {
			out[pkg+"."+name.Name] = constExpr{expr: value.Values[i], pkg: pkg}
		}
	}
}
