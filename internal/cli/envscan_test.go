package cli_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// modulePath is the module path of this repository. The scan maps an import
// path under it to a directory under the root of the scan.
const modulePath = "opendustpan.org/opendustpan"

// envReaderNames answers the functions that read one environment variable by
// name, for each package that the scan watches by import path.
func envReaderNames() map[string][]string {
	return map[string][]string{
		"os":      {"Getenv", "LookupEnv"},
		"syscall": {"Getenv"},
	}
}

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
		"SSL_CERT_FILE":                  "the path of a root certificate file; it holds no secret",
		"SSL_CERT_DIR":                   "the paths of root certificate directories; they hold no secret",
		"LocalAppData":                   "the base of the user data directory; it holds no secret",
		"XDG_DATA_HOME":                  "the base of the user data directory; it holds no secret",
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

// valueUse is the allowance of one file that uses an env reader as a value.
type valueUse struct {
	// reason says why the file passes the reader and what the callee reads.
	reason string
	// vars are the variables that the callees of the file read through the
	// reader. The test classifies each of them like a direct read, so that a
	// secret in the list must be a sensitive setting or an external secret.
	vars []string
}

// sinkVars are the variables of the audit and log sinks that the start reads
// through a lookup function.
func sinkVars() []string {
	return []string{
		"DUSTPAN_AUDIT_WEBHOOK_SECRET", "DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE",
		"DUSTPAN_AUDIT_OTLP_HEADERS", "DUSTPAN_AUDIT_OTLP_HEADERS_FILE",
		"DUSTPAN_LOG_WEBHOOK_SECRET", "DUSTPAN_LOG_WEBHOOK_SECRET_FILE",
		"DUSTPAN_LOG_OTLP_HEADERS", "DUSTPAN_LOG_OTLP_HEADERS_FILE",
	}
}

// envReaderValuesAllowed names each file that uses os.Getenv or os.LookupEnv
// as a value, without a call: the code passes the function to a callee that
// calls it later. The scan cannot see the names that the callee reads, so
// the entry of each file lists them, and the callees read nothing else.
func envReaderValuesAllowed() map[string]valueUse {
	return map[string]valueUse{
		"cmd/opendustpan/dev.go": {
			reason: "the dev command passes the lookup to its process input, the sink reader and the root certificate finder",
			vars: append([]string{
				"DUSTPAN_DEV_ADMIN_TOKEN", "DUSTPAN_DATA_DIR", "SSL_CERT_FILE", "SSL_CERT_DIR",
			}, sinkVars()...),
		},
		"cmd/opendustpan/devdir.go": {
			reason: "the dev data directory finder reads the base of the user data directory",
			vars:   []string{"LocalAppData", "XDG_DATA_HOME"},
		},
		"cmd/opendustpan/host.go": {
			reason: "the host command reads its mode and its recovery switch through the lookup",
			vars:   cli.DeclaredEnvs(),
		},
		"cmd/opendustpan/main.go": {
			reason: "serve passes the lookup to the setting layers, the start checks, the admin token loader, " +
				"the sinks, the sign-in options, the S3 credential loader and the key loader",
			vars: append([]string{
				"DUSTPAN_DEV", "DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE",
				"DUSTPAN_KEY_ENCRYPTION_KEY", "DUSTPAN_KEY_ENCRYPTION_KEY_FILE",
				"DUSTPAN_OIDC_CLIENT_SECRET", "DUSTPAN_OIDC_CLIENT_SECRET_FILE",
				"DUSTPAN_LDAP_BIND_PASSWORD", "DUSTPAN_LDAP_BIND_PASSWORD_FILE",
				"DUSTPAN_AUDIT_SIGNING_KEY_FILE", "DUSTPAN_AUDIT_VERIFY_KEYS_FILE",
				"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
			}, append(sinkVars(), cli.DeclaredEnvs()...)...),
		},
		"internal/cli/client/backup.go": {
			reason: "the backup client loads the admin tokens through the lookup",
			vars:   []string{"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE"},
		},
		"internal/cli/client/namespace.go": {
			reason: "the namespace client loads the admin tokens through the lookup",
			vars:   []string{"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE"},
		},
		"internal/cli/client/restore.go": {
			reason: "the restore client loads the admin tokens through the lookup",
			vars:   []string{"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE"},
		},
		"internal/cli/client/doctor.go": {
			reason: "doctor resolves the settings and reads the key variable through the lookup",
			vars:   append([]string{"DUSTPAN_KEY_ENCRYPTION_KEY"}, cli.DeclaredEnvs()...),
		},
	}
}

// envRead is one read of a variable name in the source.
type envRead struct {
	name string
	site string
}

// envScan is the result of a scan.
type envScan struct {
	// reads are the reads with a resolved name.
	reads []envRead
	// unresolved are the sites of a call whose name argument the scan cannot
	// resolve to a string constant. The scan fails closed: it never guesses
	// that such an argument is not a name.
	unresolved []string
	// values are the sites that use an env reader as a value, without a call.
	values []string
}

// scanFile is one parsed file with the facts that the scan needs.
type scanFile struct {
	file *ast.File
	// dir is the directory of the file, relative to the root of the scan.
	dir string
	// imports maps the local name of each import to the directory that holds
	// the package, for the imports under the module path.
	imports map[string]string
	// readers maps the local name of an import of os or syscall to the
	// functions of that package that read a variable.
	readers map[string][]string
	// dotImports are the sites of a dot import of os or syscall.
	dotImports []token.Pos
}

// TestEveryEnvReadIsDeclaredOrAllowed is the guard against a missed secret
// variable. It parses the code of cmd, internal and pkg, finds each read of an
// environment variable by a literal name or a constant name, and requires the
// name to be a declared Setting, an external secret that the server clears,
// or an entry of readsAnyEnvAllowed. The scan fails closed: a name that it
// cannot resolve, and a use of an env reader as a value, fail too unless the
// file is on a reasoned allowlist. The server cannot clear a variable that
// nobody lists.
func TestEveryEnvReadIsDeclaredOrAllowed(t *testing.T) {
	t.Parallel()

	scan := scanEnvReads(t, "../..", "cmd", "internal", "pkg")

	if len(scan.reads) < 5 {
		t.Fatalf("the scan found %d reads, want at least 5: the scan is broken", len(scan.reads))
	}

	for _, read := range scan.reads {
		if !classified(read.name) {
			t.Errorf("%s reads %s: %s", read.site, read.name, classifyHelp)
		}
	}

	checkSites(t, scan.unresolved, dynamicEnvReadsAllowed(),
		"reads a variable with a name the scan cannot resolve: use a literal or a constant")

	reasons := map[string]string{}

	for file, use := range envReaderValuesAllowed() {
		reasons[file] = use.reason

		for _, name := range use.vars {
			if !classified(name) {
				t.Errorf("%s passes a reader that reads %s: %s", file, name, classifyHelp)
			}
		}
	}

	checkSites(
		t,
		scan.values,
		reasons,
		"uses an env reader as a value: call it with a literal name, or list the file in envReaderValuesAllowed with the variables it reads",
	)
}

// classifyHelp is the advice for a variable that nobody classified.
const classifyHelp = "declare it as a setting, add it to ExternalSecretEnvs or to readsAnyEnvAllowed"

// classified reports whether the variable is a declared setting, an external
// secret that the server clears, or an entry of readsAnyEnvAllowed.
func classified(name string) bool {
	if slices.Contains(cli.DeclaredEnvs(), name) || slices.Contains(cli.ExternalSecretEnvs(), name) {
		return true
	}

	reason, ok := readsAnyEnvAllowed()[name]

	return ok && reason != ""
}

// checkSites requires each site to be in a file of allowed with a reason, and
// each allowed file to have a site: a stale entry fails.
func checkSites(t *testing.T, sites []string, allowed map[string]string, problem string) {
	t.Helper()

	used := map[string]bool{}

	for _, site := range sites {
		file, _, _ := strings.Cut(site, ":")
		if reason, ok := allowed[file]; ok && reason != "" {
			used[file] = true

			continue
		}

		t.Errorf("%s %s", site, problem)
	}

	for file := range allowed {
		if !used[file] {
			t.Errorf("%s is on an allowlist but has no matching site: remove the entry", file)
		}
	}
}

// fixtureCases are the hostile cases of the scan. Each file of
// testdata/envscan is one case, with the result that the scan must give.
func fixtureCases() map[string]fixtureWant {
	return map[string]fixtureWant{
		"concat.go":             wantUnresolved(),
		"call.go":               wantUnresolved(),
		"sprintf.go":            wantUnresolved(),
		"conversion.go":         wantUnresolved(),
		"paren.go":              wantUnresolved(),
		"variable.go":           wantValue(),
		"passed.go":             wantValue(),
		"dotimport.go":          wantValue(),
		"aliased_read.go":       wantReads("ALIASED_SECRET"),
		"aliased_dynamic.go":    wantUnresolved(),
		"syscall_read.go":       wantReads("SYSCALL_SECRET"),
		"syscall_dynamic.go":    wantUnresolved(),
		"plain_literal.go":      wantReads("PLAIN_SECRET"),
		"plain_const.go":        wantReads("CONST_SECRET"),
		"c/dir_keyed.go":        wantReads("DIR_A_SECRET", "DIR_B_SECRET"),
		"a/const.go":            wantReads(),
		"b/const.go":            wantReads(),
		"unrelated_receiver.go": wantReads(),
	}
}

// fixtureWant is the expected result of one fixture file.
type fixtureWant struct {
	unresolved bool
	value      bool
	reads      []string
}

func wantUnresolved() fixtureWant { return fixtureWant{unresolved: true, value: false, reads: nil} }

func wantValue() fixtureWant { return fixtureWant{unresolved: false, value: true, reads: nil} }

func wantReads(names ...string) fixtureWant {
	return fixtureWant{unresolved: false, value: false, reads: names}
}

// fixtureGot answers what the scan found in one file.
func fixtureGot(scan envScan, file string) fixtureWant {
	var reads []string

	for _, read := range scan.reads {
		if name, _, _ := strings.Cut(read.site, ":"); name == file {
			reads = append(reads, read.name)
		}
	}

	return fixtureWant{
		unresolved: countSites(scan.unresolved, file) > 0,
		value:      countSites(scan.values, file) > 0,
		reads:      reads,
	}
}

// TestEnvScanHostileFixtures runs the scan on files that hold the hostile
// cases: a name that is not a literal, an env reader as a value, an aliased
// import, syscall, and constants with the same name in two directories. Each
// file must give exactly its result, so a hole in the scan fails here.
func TestEnvScanHostileFixtures(t *testing.T) {
	t.Parallel()

	scan := scanEnvReads(t, "../..", "internal/cli/testdata/envscan")
	cases := fixtureCases()

	const prefix = "internal/cli/testdata/envscan/"

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := fixtureGot(scan, prefix+name)

			slices.Sort(got.reads)
			slices.Sort(want.reads)

			if got.unresolved != want.unresolved || got.value != want.value || !slices.Equal(got.reads, want.reads) {
				t.Errorf("scan = %+v, want %+v", got, want)
			}
		})
	}

	// Each file of the fixture directory must be a case: a new file without a
	// row would be a case that nobody checks.
	for _, file := range fixtureFiles(t, "testdata/envscan") {
		if _, ok := cases[file]; !ok {
			t.Errorf("fixture %s has no row in fixtureCases", file)
		}
	}
}

// countSites answers how many sites are in the file that site names.
func countSites(sites []string, file string) int {
	count := 0

	for _, site := range sites {
		if name, _, _ := strings.Cut(site, ":"); name == file {
			count++
		}
	}

	return count
}

// fixtureFiles answers the Go files under dir, relative to dir.
func fixtureFiles(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(dir, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !entry.IsDir() && strings.HasSuffix(p, ".go") {
			rel, relErr := filepath.Rel(dir, p)
			if relErr != nil {
				return fmt.Errorf("rel %s: %w", p, relErr)
			}

			out = append(out, filepath.ToSlash(rel))
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}

	return out
}

// scanEnvReads parses each non-test Go file under the subdirectories of root,
// and answers the reads of environment variables in them.
func scanEnvReads(t *testing.T, root string, subdirs ...string) envScan {
	t.Helper()

	fset := token.NewFileSet()

	var files []*scanFile

	for _, sub := range subdirs {
		found, err := parseDir(fset, root, sub)
		if err != nil {
			t.Fatalf("parse %s: %v", sub, err)
		}

		files = append(files, found...)
	}

	resolveImports(files)

	consts := stringConsts(files)

	var out envScan

	for _, file := range files {
		scanFileReads(fset, file, consts, &out)
	}

	return out
}

// parseDir parses each non-test Go file under root/sub. It reads through a
// file system that is rooted at root, so that no path leaves the tree.
func parseDir(fset *token.FileSet, root, sub string) ([]*scanFile, error) {
	var files []*scanFile

	tree := os.DirFS(root)

	err := fs.WalkDir(tree, filepath.ToSlash(sub), func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// A testdata directory holds fixtures, not code of the server. The scan
		// of the fixtures starts below it.
		if entry.IsDir() && entry.Name() == "testdata" {
			return fs.SkipDir
		}

		if entry.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		src, readErr := fs.ReadFile(tree, p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", p, readErr)
		}

		// The file name in the position is the path relative to the root, so
		// that each site is the same in every working directory.
		file, parseErr := parser.ParseFile(fset, p, src, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", p, parseErr)
		}

		files = append(files, newScanFile(file, path.Dir(p)))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", sub, err)
	}

	return files, nil
}

// newScanFile answers a scan file with empty import tables.
func newScanFile(file *ast.File, dir string) *scanFile {
	return &scanFile{
		file:       file,
		dir:        dir,
		imports:    map[string]string{},
		readers:    map[string][]string{},
		dotImports: nil,
	}
}

// resolveImports fills the import maps and the reader maps of the files. The
// local name of an import without an alias is the package name that the
// scan saw in the directory of the import, or the last element of the path.
func resolveImports(files []*scanFile) {
	pkgNames := map[string]string{}

	for _, f := range files {
		pkgNames[f.dir] = f.file.Name.Name
	}

	for _, f := range files {
		for _, spec := range f.file.Imports {
			bindImport(f, spec, pkgNames)
		}
	}
}

// bindImport records one import of a file: a watched package as a reader
// table, a package of the module as a directory.
func bindImport(f *scanFile, spec *ast.ImportSpec, pkgNames map[string]string) {
	importPath, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		return
	}

	dir, inModule := strings.CutPrefix(importPath, modulePath+"/")
	local := path.Base(importPath)

	if name, known := pkgNames[dir]; inModule && known {
		local = name
	}

	if spec.Name != nil {
		local = spec.Name.Name
	}

	if names, watched := envReaderNames()[importPath]; watched {
		if local == "." {
			f.dotImports = append(f.dotImports, spec.Pos())
		} else {
			f.readers[local] = names
		}

		return
	}

	if inModule && local != "_" && local != "." {
		f.imports[local] = dir
	}
}

// scanFileReads adds the reads of one file to out.
func scanFileReads(fset *token.FileSet, f *scanFile, consts map[string]constExpr, out *envScan) {
	site := func(pos token.Pos) string { return fset.Position(pos).String() }

	for _, pos := range f.dotImports {
		out.values = append(out.values, site(pos))
	}

	// The calls that the scan handled; a reader selector that is not here is
	// a use as a value.
	called := map[*ast.SelectorExpr]bool{}

	ast.Inspect(f.file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			if sel := readerSelector(f, n.Fun); sel != nil {
				called[sel] = true

				addCall(f, n, consts, out, site(n.Pos()))
			}
		case *ast.SelectorExpr:
			if !called[n] && readerSelector(f, n) != nil {
				out.values = append(out.values, site(n.Pos()))
			}
		default:
			// Another node is not a read.
		}

		return true
	})
}

// addCall adds one call of an env reader to out: a read when the name
// resolves, else an unresolved site.
func addCall(f *scanFile, call *ast.CallExpr, consts map[string]constExpr, out *envScan, site string) {
	if len(call.Args) > 0 {
		if name, resolved := resolveName(f, call.Args[0], consts, 0); resolved {
			out.reads = append(out.reads, envRead{name: name, site: site})

			return
		}
	}

	out.unresolved = append(out.unresolved, site)
}

// readerSelector answers the selector of an env reader in fun, after the
// parentheses, or nil when fun is not an env reader of os or syscall.
func readerSelector(f *scanFile, fun ast.Expr) *ast.SelectorExpr {
	for {
		paren, ok := fun.(*ast.ParenExpr)
		if !ok {
			break
		}

		fun = paren.X
	}

	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	if names, ok := f.readers[pkg.Name]; ok && slices.Contains(names, sel.Sel.Name) {
		return sel
	}

	return nil
}

// constExpr is the expression of a constant, with the file that holds it.
type constExpr struct {
	expr ast.Expr
	file *scanFile
}

// maxConstDepth stops a loop of constants that name each other.
const maxConstDepth = 8

// resolveName answers the string that expr names: a string literal, or a
// constant of the same directory or of another directory of the module. A
// constant that names another constant resolves through it. Anything else is
// not resolved.
func resolveName(f *scanFile, expr ast.Expr, consts map[string]constExpr, depth int) (string, bool) {
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
		key = f.dir + "." + e.Name
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}

		dir, ok := f.imports[x.Name]
		if !ok {
			return "", false
		}

		key = dir + "." + e.Sel.Name
	default:
		return "", false
	}

	target, ok := consts[key]
	if !ok {
		return "", false
	}

	return resolveName(target.file, target.expr, consts, depth+1)
}

// stringConsts answers each package-level constant, keyed by the directory and
// the constant name. The package name is not a key: two directories can hold a
// package with the same name.
func stringConsts(files []*scanFile) map[string]constExpr {
	out := map[string]constExpr{}

	for _, f := range files {
		for _, decl := range f.file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.CONST {
				addConsts(out, f, gen)
			}
		}
	}

	return out
}

// addConsts adds the constants of one declaration to out.
func addConsts(out map[string]constExpr, f *scanFile, gen *ast.GenDecl) {
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok || len(value.Names) != len(value.Values) {
			continue
		}

		for i, name := range value.Names {
			out[f.dir+"."+name.Name] = constExpr{expr: value.Values[i], file: f}
		}
	}
}
