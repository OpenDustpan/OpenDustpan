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

// envBulkNames answers the functions that read many variables, or expand
// names inside a text, for each package that the scan watches by import path.
// The name of a variable is not an argument of them, so the scan cannot
// classify what they read. It reports each call and each use as a value of
// them as an unresolved site.
func envBulkNames() map[string][]string {
	return map[string][]string{
		"os":      {"Environ", "Expand", "ExpandEnv"},
		"syscall": {"Environ"},
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

// dynamicEnvReadsAllowed names each function that reads a variable with a name
// that the scan cannot resolve, or that reads or expands the whole
// environment, with the reason and the number of sites. The key is the file and
// the function. The name comes from the caller there, and the scan checks
// the literal at the caller.
func dynamicEnvReadsAllowed() map[string]siteAllowance {
	const passes = "the test process passes the variables that its caller lists"

	return map[string]siteAllowance{
		"internal/cli/setting.go#Env": {
			"the layered lookup of the settings; its name comes from the Settings table",
			1,
		},
		"internal/cli/setting.go#EnvBool": {
			"the layered lookup of the settings; its name comes from the Settings table",
			1,
		},
		"internal/cli/setting.go#EnvInt": {
			"the layered lookup of the settings; its name comes from the Settings table",
			1,
		},
		"internal/clientgen/conformance/process.go#Environ":           {passes, 1},
		"internal/clientgen/conformance/goclient/goclient.go#Environ": {passes, 1},
		"internal/clientgen/conformance/tsclient/tsclient.go#Environ": {passes, 1},
		"internal/testchild/testchild.go#Run": {
			"the test child process inherits the environment of the test on purpose; no server code uses it", 1,
		},
	}
}

// valueUse is the allowance of one file that uses an env reader as a value.
type valueUse struct {
	// sites is the number of places in the function that use a reader as a
	// value. A new place fails the scan until a person raises the number and
	// lists the variables that the new callee reads.
	sites int
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

// envReaderValuesAllowed names each function that uses os.Getenv or
// os.LookupEnv as a value, without a call: the code passes the function to a
// callee that calls it later. The scan cannot see the names that the callee
// reads, so the entry of each function lists them with the number of places,
// and the callees read nothing else. The key is the file and the function.
func envReaderValuesAllowed() map[string]valueUse {
	return map[string]valueUse{
		"cmd/opendustpan/dev.go#osDevProcess": {
			sites:  1,
			reason: "the dev command passes the lookup to its process input, the sink reader and the root certificate finder",
			vars: append([]string{
				"DUSTPAN_DEV_ADMIN_TOKEN", "DUSTPAN_DATA_DIR", "SSL_CERT_FILE", "SSL_CERT_DIR",
			}, sinkVars()...),
		},
		"cmd/opendustpan/devdir.go#osDevDirInput": {
			sites:  1,
			reason: "the dev data directory finder reads the base of the user data directory",
			vars:   []string{"LocalAppData", "XDG_DATA_HOME"},
		},
		"cmd/opendustpan/host.go#hostCreateTempAdmin": {
			sites:  1,
			reason: "the host command reads its mode and its recovery switch through the lookup",
			vars:   cli.DeclaredEnvs(),
		},
		"cmd/opendustpan/main.go#serveFlags.beforeServe": {
			sites:  1,
			reason: "the serve hook asks which sensitive variables are set, and logs their names, before it clears them",
			vars:   append(cli.SensitiveEnvs(), cli.ExternalSecretEnvs()...),
		},
		"cmd/opendustpan/main.go#loadKeyEncryptionKey": {
			sites:  1,
			reason: "the key loader reads the key variable and its file variable",
			vars:   []string{"DUSTPAN_KEY_ENCRYPTION_KEY", "DUSTPAN_KEY_ENCRYPTION_KEY_FILE"},
		},
		"cmd/opendustpan/main.go#openS3Store": {
			sites:  1,
			reason: "the S3 credential loader reads the AWS credential variables",
			vars:   []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"},
		},
		"cmd/opendustpan/main.go#serveFlags.appLogFor": {
			sites:  1,
			reason: "the log sink reader reads the secrets of the log sinks",
			vars:   sinkVars(),
		},
		"cmd/opendustpan/main.go#serveFlags.run": {
			sites:  1,
			reason: "the admin token loader reads the token variables through the layered lookup of the settings",
			vars: append([]string{
				"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE",
				"DUSTPAN_AUDIT_SIGNING_KEY_FILE", "DUSTPAN_AUDIT_VERIFY_KEYS_FILE",
			}, cli.DeclaredEnvs()...),
		},
		"cmd/opendustpan/main.go#serveFlags.serveWithStores": {
			sites:  1,
			reason: "the audit sink reader reads the secrets of the audit sinks",
			vars:   sinkVars(),
		},
		"cmd/opendustpan/main.go#serveFlags.serverOptions": {
			sites:  1,
			reason: "the sign-in options read the OIDC and LDAP secrets",
			vars: []string{
				"DUSTPAN_OIDC_CLIENT_SECRET", "DUSTPAN_OIDC_CLIENT_SECRET_FILE",
				"DUSTPAN_LDAP_BIND_PASSWORD", "DUSTPAN_LDAP_BIND_PASSWORD_FILE",
			},
		},
		"cmd/opendustpan/main.go#serveWith": {
			sites:  4,
			reason: "serve passes the lookup to the dev check, the setting snapshot, the setting layers and the start checks",
			vars:   append([]string{"DUSTPAN_DEV"}, cli.DeclaredEnvs()...),
		},
		"internal/cli/client/backup.go#BackupToken": {
			sites:  1,
			reason: "the backup client loads the admin tokens through the lookup",
			vars:   []string{"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE"},
		},
		"internal/cli/client/namespace.go#namespaceToken": {
			sites:  1,
			reason: "the namespace client loads the admin tokens through the lookup",
			vars:   []string{"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE"},
		},
		"internal/cli/client/restore.go#restoreToken": {
			sites:  1,
			reason: "the restore client loads the admin tokens through the lookup",
			vars:   []string{"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE"},
		},
		"internal/cli/client/doctor.go#readDoctorSettings": {
			sites:  2,
			reason: "doctor resolves the settings through the lookup",
			vars:   cli.DeclaredEnvs(),
		},
	}
}

// scanSite is one place in the source that the scan reports.
type scanSite struct {
	// file is the path of the file, relative to the root of the scan.
	file string
	// fn is the top-level function that holds the site, as "Name" or
	// "Type.Name". It is empty for a site outside a function.
	fn string
	// pos is the position as "file:line:column".
	pos string
}

// key answers the allowlist key of the site: the file and its function. The
// allowlists name a function, so a new site in an allowed file fails unless
// the function has an entry too.
func (s scanSite) key() string { return s.file + "#" + s.fn }

// envRead is one read of a variable name in the source.
type envRead struct {
	name string
	site scanSite
}

// envScan is the result of a scan.
type envScan struct {
	// reads are the reads with a resolved name.
	reads []envRead
	// unresolved are the sites of a call whose name argument the scan cannot
	// resolve to a string constant, and of each call or use of a function that
	// reads many variables. The scan fails closed: it never guesses that such
	// an argument is not a name.
	unresolved []scanSite
	// values are the sites that use an env reader as a value, without a call.
	values []scanSite
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
	// bulk maps the local name of an import of os or syscall to the functions
	// of that package that read many variables.
	bulk map[string][]string
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
			t.Errorf("%s reads %s: %s", read.site.pos, read.name, classifyHelp)
		}
	}

	checkSites(t, scan.unresolved, dynamicEnvReadsAllowed(),
		"reads a variable with a name the scan cannot resolve: use a literal or a constant")

	reasons := map[string]siteAllowance{}

	for key, use := range envReaderValuesAllowed() {
		reasons[key] = siteAllowance{reason: use.reason, sites: use.sites}

		for _, name := range use.vars {
			if !classified(name) {
				t.Errorf("%s passes a reader that reads %s: %s", key, name, classifyHelp)
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

// siteAllowance is the allowance of one function of one file: the reason, and
// the number of sites that the allowance covers. A new site in the same
// function raises the count, so it fails until a person raises the allowance.
type siteAllowance struct {
	reason string
	sites  int
}

// siteFindings answers one finding for each problem with the sites. Each site
// must be in a function of allowed, with a reason and with the same number of
// sites as the allowance. An allowance without a site is stale. It answers
// the findings and does not fail a test, so that a test can feed it a stale
// entry and an unknown site and check the answer.
func siteFindings(sites []scanSite, allowed map[string]siteAllowance, problem string) []string {
	byKey := map[string][]scanSite{}

	for _, site := range sites {
		byKey[site.key()] = append(byKey[site.key()], site)
	}

	var findings []string

	for key, found := range byKey {
		allowance, ok := allowed[key]

		switch {
		case !ok || allowance.reason == "":
			for _, site := range found {
				findings = append(findings, site.pos+" "+problem)
			}
		case len(found) != allowance.sites:
			findings = append(findings, fmt.Sprintf(
				"%s has %d sites, the allowance lists %d: %s", key, len(found), allowance.sites, problem,
			))
		default:
			// The sites match the allowance.
		}
	}

	for key := range allowed {
		if len(byKey[key]) == 0 {
			findings = append(
				findings,
				key+" is on an allowlist but has no matching site: remove the entry",
			)
		}
	}

	slices.Sort(findings)

	return findings
}

// checkSites fails the test for each finding of siteFindings.
func checkSites(t *testing.T, sites []scanSite, allowed map[string]siteAllowance, problem string) {
	t.Helper()

	for _, finding := range siteFindings(sites, allowed, problem) {
		t.Error(finding)
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
		"shadow.go":             wantUnresolved(),
		"shadow_param.go":       wantUnresolved(),
		"shadow_const.go":       wantUnresolved(),
		"c/shadow_import.go":    wantUnresolved(),
		"environ.go":            wantUnresolved(),
		"expand_env.go":         wantUnresolved(),
		"expand.go":             wantUnresolved(),
		"syscall_environ.go":    wantUnresolved(),
		"bulk_value.go":         wantUnresolved(),
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
		if name, _, _ := strings.Cut(read.site.pos, ":"); name == file {
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
func countSites(sites []scanSite, file string) int {
	count := 0

	for _, site := range sites {
		if site.file == file {
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
		bulk:       map[string][]string{},
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

	names, watched := envReaderNames()[importPath]
	bulk, watchedBulk := envBulkNames()[importPath]

	if watched || watchedBulk {
		if local == "." {
			f.dotImports = append(f.dotImports, spec.Pos())
		} else {
			f.readers[local] = names
			f.bulk[local] = bulk
		}

		return
	}

	if inModule && local != "_" && local != "." {
		f.imports[local] = dir
	}
}

// scanFileReads adds the reads of one file to out.
func scanFileReads(fset *token.FileSet, f *scanFile, consts map[string]constExpr, out *envScan) {
	siteIn := func(fn string) func(pos token.Pos) scanSite {
		return func(pos token.Pos) scanSite {
			position := fset.Position(pos)

			return scanSite{file: position.Filename, fn: fn, pos: position.String()}
		}
	}

	for _, pos := range f.dotImports {
		out.values = append(out.values, siteIn("")(pos))
	}

	// The calls that the scan handled; a watched selector that is not here is
	// a use as a value.
	called := map[*ast.SelectorExpr]bool{}

	for _, decl := range f.file.Decls {
		site := siteIn(declName(decl))

		ast.Inspect(decl, func(node ast.Node) bool {
			scanNode(&scanState{file: f, consts: consts, out: out, called: called}, node, site)

			return true
		})
	}
}

// scanState is what the scan of the nodes of one file shares.
type scanState struct {
	file   *scanFile
	consts map[string]constExpr
	out    *envScan
	// called holds the selectors that a call handled; a watched selector that
	// is not here is a use as a value.
	called map[*ast.SelectorExpr]bool
}

// scanNode adds the site of one node to out, when the node is a call of a
// watched function or a use of one as a value.
func scanNode(st *scanState, node ast.Node, site func(token.Pos) scanSite) {
	f, consts, out, called := st.file, st.consts, st.out, st.called

	switch n := node.(type) {
	case *ast.CallExpr:
		if sel := watchedSelector(n.Fun, f.readers); sel != nil {
			called[sel] = true

			addCall(f, n, consts, out, site(n.Pos()))
		} else if sel := watchedSelector(n.Fun, f.bulk); sel != nil {
			called[sel] = true

			out.unresolved = append(out.unresolved, site(n.Pos()))
		}
	case *ast.SelectorExpr:
		if called[n] {
			return
		}

		if watchedSelector(n, f.readers) != nil {
			out.values = append(out.values, site(n.Pos()))
		} else if watchedSelector(n, f.bulk) != nil {
			out.unresolved = append(out.unresolved, site(n.Pos()))
		}
	default:
		// Another node is not a read.
	}
}

// declName answers the name of a top-level function declaration as "Name" or
// "Type.Name". It answers an empty string for another declaration.
func declName(decl ast.Decl) string {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok {
		return ""
	}

	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}

	typ := fn.Recv.List[0].Type

	for {
		switch t := typ.(type) {
		case *ast.StarExpr:
			typ = t.X

			continue
		case *ast.IndexExpr:
			typ = t.X

			continue
		case *ast.Ident:
			return t.Name + "." + fn.Name.Name
		default:
		}

		return fn.Name.Name
	}
}

// addCall adds one call of an env reader to out: a read when the name
// resolves, else an unresolved site.
func addCall(f *scanFile, call *ast.CallExpr, consts map[string]constExpr, out *envScan, site scanSite) {
	if len(call.Args) > 0 {
		if name, resolved := resolveName(f, call.Args[0], consts, 0); resolved {
			out.reads = append(out.reads, envRead{name: name, site: site})

			return
		}
	}

	out.unresolved = append(out.unresolved, site)
}

// watchedSelector answers the selector of a watched function in fun, after
// the parentheses, or nil when fun is not a function of table. The table maps
// the local name of an import to its watched functions.
func watchedSelector(fun ast.Expr, table map[string][]string) *ast.SelectorExpr {
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

	// Defense in depth: a local name that shadows the import is not the
	// package. This check does not rely on the import table alone.
	if pkg.Obj != nil {
		return nil
	}

	if names, ok := table[pkg.Name]; ok && slices.Contains(names, sel.Sel.Name) {
		return sel
	}

	return nil
}

// fileConst reports whether obj is a constant that the file declares at file
// scope, not inside a function.
func (f *scanFile) fileConst(id *ast.Ident) bool {
	if id.Obj == nil || id.Obj.Kind != ast.Con {
		return false
	}

	spec, ok := id.Obj.Decl.(*ast.ValueSpec)
	if !ok {
		return false
	}

	for _, decl := range f.file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if ok && gen.Tok == token.CONST && slices.Contains(gen.Specs, ast.Spec(spec)) {
			return true
		}
	}

	return false
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

	var (
		key string
		ok  bool
	)

	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}

		value, err := strconv.Unquote(e.Value)

		return value, err == nil
	case *ast.Ident:
		key, ok = f.identKey(e)
	case *ast.SelectorExpr:
		key, ok = f.selectorKey(e)
	default:
		return "", false
	}

	target, found := consts[key]
	if !ok || !found {
		return "", false
	}

	return resolveName(target.file, target.expr, consts, depth+1)
}

// identKey answers the constant key of a name. A name that the parser bound to
// a local variable, a parameter or a local constant is not the package
// constant of the same name. The name resolves only when it is unbound (the
// constant is in another file of the package) or bound to a constant of the
// file scope.
func (f *scanFile) identKey(id *ast.Ident) (string, bool) {
	if id.Obj != nil && !f.fileConst(id) {
		return "", false
	}

	return f.dir + "." + id.Name, true
}

// selectorKey answers the constant key of pkg.Name, where pkg is an import
// of the module. A local name that shadows the import is not the package.
func (f *scanFile) selectorKey(sel *ast.SelectorExpr) (string, bool) {
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Obj != nil {
		return "", false
	}

	dir, ok := f.imports[x.Name]
	if !ok {
		return "", false
	}

	return dir + "." + sel.Sel.Name, true
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

// TestSiteFindingsFailsClosed feeds siteFindings the cases that the allowlists
// must reject: a site with no allowance, a new site in an allowed file but in
// another function, a new site in an allowed function, an allowance that
// lost its site, and an allowance without a reason. Each case must give a
// finding, so a no-op siteFindings fails here.
func TestSiteFindingsFailsClosed(t *testing.T) {
	t.Parallel()

	site := func(file, fn string) scanSite {
		return scanSite{file: file, fn: fn, pos: file + ":1:1"}
	}

	allowed := map[string]siteAllowance{
		"main.go#known": {reason: "a reason", sites: 1},
	}

	cases := map[string]struct {
		sites   []scanSite
		allowed map[string]siteAllowance
		want    string
	}{
		"allowed": {[]scanSite{site("main.go", "known")}, allowed, ""},
		"unknown site": {
			[]scanSite{site("other.go", "f")},
			map[string]siteAllowance{},
			"other.go:1:1 problem",
		},
		"new function in known file": {
			[]scanSite{site("main.go", "known"), site("main.go", "probe")},
			allowed,
			"main.go:1:1 problem",
		},
		"new site in known function": {
			[]scanSite{site("main.go", "known"), site("main.go", "known")},
			allowed,
			"main.go#known has 2 sites",
		},
		"stale entry": {nil, allowed, "main.go#known is on an allowlist but has no matching site"},
		"missing reason": {
			[]scanSite{site("main.go", "known")},
			map[string]siteAllowance{"main.go#known": {reason: "", sites: 1}},
			"main.go:1:1 problem",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			findings := siteFindings(tc.sites, tc.allowed, "problem")

			switch {
			case tc.want == "" && len(findings) != 0:
				t.Errorf("findings = %q, want none", findings)
			case tc.want != "" && !slices.ContainsFunc(findings, func(f string) bool { return strings.Contains(f, tc.want) }):
				t.Errorf("findings = %q, want one with %q", findings, tc.want)
			default:
				// The findings match the case.
			}
		})
	}
}
