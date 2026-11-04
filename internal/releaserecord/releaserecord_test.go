package releaserecord_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/releaserecord"
)

const baseline = "0168_baseline.sql"

// sumOf returns the SHA-256 of sql as lowercase hex.
func sumOf(sql string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(sql)))
}

// released returns a record of one tag that shipped the baseline of each
// dialect with the SQL "base".
func released(t *testing.T) releaserecord.Record {
	t.Helper()

	sum := sumOf("base")
	data := "# a comment\n\n" +
		"v0.1.0 sqlite " + baseline + " " + sum + "\n" +
		"v0.1.0 postgres " + baseline + " " + sum + "\n"

	rec, err := releaserecord.Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse() of the released fixture: %v", err)
	}

	if !rec.Released() {
		t.Fatal("the released fixture is not released")
	}

	return rec
}

// notReleased returns a record of comments only, as the tree holds it before
// the first tag.
func notReleased(t *testing.T) releaserecord.Record {
	t.Helper()

	rec, err := releaserecord.Parse([]byte("# comments only\n\n# no line\n"))
	if err != nil {
		t.Fatalf("Parse() of the not-released fixture: %v", err)
	}

	if rec.Released() {
		t.Fatal("the not-released fixture is released")
	}

	return rec
}

// TestCurrentParses proves that the record of the tree parses. A guard fails
// on a record that does not parse, so a bad edit cannot turn the checks off.
func TestCurrentParses(t *testing.T) {
	t.Parallel()

	if _, err := releaserecord.Current(); err != nil {
		t.Fatalf("Current() = %v", err)
	}
}

// TestParseRefuses sends hostile and malformed records directly to the parser.
func TestParseRefuses(t *testing.T) {
	t.Parallel()

	sum := sumOf("base")
	both := func(line string) string {
		return line + "\nv0.1.0 postgres " + baseline + " " + sum + "\n"
	}

	for name, data := range map[string]string{
		"three fields":        both("v0.1.0 sqlite " + baseline),
		"five fields":         both("v0.1.0 sqlite " + baseline + " " + sum + " extra"),
		"two spaces":          both("v0.1.0  sqlite " + baseline + " " + sum),
		"tab separator":       both("v0.1.0\tsqlite " + baseline + " " + sum),
		"leading space":       both(" v0.1.0 sqlite " + baseline + " " + sum),
		"trailing space":      both("v0.1.0 sqlite " + baseline + " " + sum + " "),
		"carriage return":     both("v0.1.0 sqlite " + baseline + " " + sum + "\r"),
		"tag without v":       both("0.1.0 sqlite " + baseline + " " + sum),
		"tag with suffix":     both("v0.1.0-rc1 sqlite " + baseline + " " + sum),
		"tag with zero":       both("v0.01.0 sqlite " + baseline + " " + sum),
		"unknown dialect":     both("v0.1.0 mysql " + baseline + " " + sum),
		"file with path":      both("v0.1.0 sqlite ../" + baseline + " " + sum),
		"file without number": both("v0.1.0 sqlite baseline.sql " + sum),
		"file not sql":        both("v0.1.0 sqlite 0168_baseline.txt " + sum),
		"short sum":           both("v0.1.0 sqlite " + baseline + " abc"),
		"upper-case sum":      both("v0.1.0 sqlite " + baseline + " " + strings.ToUpper(sum)),
		"line listed twice":   both("v0.1.0 sqlite " + baseline + " " + sum + "\nv0.1.0 sqlite " + baseline + " " + sum),
		"one file, two sums": both("v0.1.0 sqlite " + baseline + " " + sum +
			"\nv0.2.0 sqlite " + baseline + " " + sumOf("edited") + "\nv0.2.0 postgres " + baseline + " " + sum),
		"one dialect only":     "v0.1.0 sqlite " + baseline + " " + sum + "\n",
		"comment not at start": both("v0.1.0 sqlite " + baseline + " " + sum + " # note"),
	} {
		if _, err := releaserecord.Parse([]byte(data)); !errors.Is(err, releaserecord.ErrBadRecord) {
			t.Errorf("%s: Parse() = %v, want %v", name, err, releaserecord.ErrBadRecord)
		}
	}
}

// TestOffFollowsTheRecord proves that a check is off with a reason that names
// the record before the first tag, and on after it.
func TestOffFollowsTheRecord(t *testing.T) {
	t.Parallel()

	why := notReleased(t).Off("TestX")
	if !strings.HasPrefix(why, "TestX is off until the first tag: ") || !strings.Contains(why, releaserecord.Path) {
		t.Errorf("Off() before the first tag = %q, want a reason that names the check and %s", why, releaserecord.Path)
	}

	if why := released(t).Off("TestX"); why != "" {
		t.Errorf("Off() after the first tag = %q, want \"\"", why)
	}
}

// TestMigrationFilesFollowTheRecord proves both forms of the migration file
// check: the baseline alone before the first tag, and frozen shipped files
// with new numbered files after it.
func TestMigrationFilesFollowTheRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		released bool
		files    map[string]string
		problems int
	}{
		{"before: the baseline alone", false, map[string]string{baseline: "base"}, 0},
		{"before: an edited baseline", false, map[string]string{baseline: "edited"}, 0},
		{"before: a new numbered file", false, map[string]string{baseline: "base", "0169_next.sql": "x"}, 1},
		{"before: another baseline name", false, map[string]string{"0170_baseline.sql": "base"}, 1},
		{"before: no file", false, map[string]string{}, 1},
		{"after: the shipped baseline", true, map[string]string{baseline: "base"}, 0},
		{"after: a new numbered file", true, map[string]string{baseline: "base", "0169_next.sql": "x"}, 0},
		{"after: an edited baseline", true, map[string]string{baseline: "edited"}, 1},
		{"after: a removed baseline", true, map[string]string{"0169_next.sql": "x"}, 1},
		{"after: a new file below the baseline", true, map[string]string{baseline: "base", "0150_old.sql": "x"}, 1},
		{"after: a new file with the same number", true, map[string]string{baseline: "base", "0168_other.sql": "x"}, 1},
		{"after: a file without a number", true, map[string]string{baseline: "base", "next.sql": "x"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := notReleased(t)
			if tt.released {
				rec = released(t)
			}

			problems := rec.MigrationFiles("sqlite", baseline, tt.files)
			if len(problems) != tt.problems {
				t.Errorf("MigrationFiles() = %v, want %d problems", problems, tt.problems)
			}
		})
	}
}

// guard names a test function that a guard file holds, and the method of the
// record that the function must call.
type guard struct {
	file   string
	fn     string
	method string
}

// guards lists each check that the record turns on, relative to this
// directory.
func guards() []guard {
	return []guard{
		{"../../pkg/store/sqlite/frozen_test.go", "TestFrozenMigrations", "Off"},
		{"../../pkg/store/postgres/frozen_test.go", "TestFrozenMigrations", "Off"},
		{"../../pkg/store/sqlite/frozen_test.go", "TestMigrationFilesFollowTheRecord", "MigrationFiles"},
		{"../../pkg/store/postgres/frozen_test.go", "TestMigrationFilesFollowTheRecord", "MigrationFiles"},
		{"../../pkg/store/internal/baseline/roundtrip_test.go", "TestSQLiteBaselineRoundTrip", "Off"},
		{"../../pkg/store/internal/baseline/roundtrip_test.go", "TestPostgresBaselineRoundTrip", "Off"},
		{"../../internal/api/apidesc/version_test.go", "TestVersionFollowsTheDescription", "Off"},
		{"../../pkg/store/sqlite/frozen_test.go", "TestTagCopiesHaveTheirLines", "TagCopies"},
		{"../../pkg/store/postgres/frozen_test.go", "TestTagCopiesHaveTheirLines", "TagCopies"},
	}
}

// TestEachGuardAsksTheRecord proves that each guard asks the record. A guard
// that calls Off must start with t.Parallel() and then the if statement that
// skips on the reason from Off. After it, each skip and each return must be
// inside an if statement. A guard that calls MigrationFiles has no skip and
// no return. So a guard that ignores the record, skips with no condition,
// or returns early with no condition fails here. A skip inside a helper
// function is out of the reach of this test.
func TestEachGuardAsksTheRecord(t *testing.T) {
	t.Parallel()

	for _, g := range guards() {
		t.Run(g.fn+" in "+filepath.Base(filepath.Dir(g.file)), func(t *testing.T) {
			t.Parallel()

			body := funcBody(t, g.file, g.fn)

			if !callsMethod(body, g.method) {
				t.Errorf("%s in %s does not call the %s method of the release record", g.fn, g.file, g.method)
			}

			if g.method != "Off" {
				for _, stop := range stops(body.List) {
					t.Errorf("%s in %s: a skip or a return at %d; this guard has none", g.fn, g.file, stop)
				}

				return
			}

			checkOffGuard(t, g, body)
		})
	}
}

// checkOffGuard fails t unless body starts with t.Parallel() and the if
// statement that skips on the reason from Off, and each later skip or return
// is inside an if statement.
func checkOffGuard(t *testing.T, g guard, body *ast.BlockStmt) {
	t.Helper()

	if len(body.List) < 2 || !isCall(body.List[0], "Parallel") {
		t.Errorf("%s in %s does not start with t.Parallel()", g.fn, g.file)

		return
	}

	ifStmt, ok := body.List[1].(*ast.IfStmt)
	if !ok || !slices.ContainsFunc(ifStmt.Body.List, isSkip) {
		t.Errorf("%s in %s: the statement after t.Parallel() is not the if statement that skips", g.fn, g.file)

		return
	}

	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 1 || !callsMethod(assign, "Off") {
		t.Errorf("%s in %s: the first skip does not take its reason from Off", g.fn, g.file)

		return
	}

	reason, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || !testsReason(ifStmt.Cond, reason.Name) {
		t.Errorf("%s in %s: the first skip does not test the reason from Off", g.fn, g.file)
	}

	checkTailStops(t, g, body.List[2:])
}

// checkTailStops fails t for each skip or return in rest that is not inside
// an if statement.
func checkTailStops(t *testing.T, g guard, rest []ast.Stmt) {
	t.Helper()

	guarded := ifBodies(rest)

	for _, stop := range stops(rest) {
		if !slices.ContainsFunc(guarded, func(r [2]token.Pos) bool { return r[0] <= stop && stop < r[1] }) {
			t.Errorf("%s in %s: a skip or a return at %d is not inside an if statement", g.fn, g.file, stop)
		}
	}
}

// stops returns the position of each skip call and each return statement in
// stmts, outside function literals.
func stops(stmts []ast.Stmt) []token.Pos {
	var out []token.Pos

	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.ReturnStmt:
				out = append(out, n.Pos())
			case *ast.ExprStmt:
				if isSkip(n) {
					out = append(out, n.Pos())
				}
			default:
				// Other nodes hold no skip or return of their own.
			}

			return true
		})
	}

	return out
}

// ifBodies returns the range of the body and the else part of each if
// statement in stmts.
func ifBodies(stmts []ast.Stmt) [][2]token.Pos {
	var out [][2]token.Pos

	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if ifStmt, ok := n.(*ast.IfStmt); ok {
				out = append(out, [2]token.Pos{ifStmt.Body.Pos(), ifStmt.Body.End()})

				if ifStmt.Else != nil {
					out = append(out, [2]token.Pos{ifStmt.Else.Pos(), ifStmt.Else.End()})
				}
			}

			return true
		})
	}

	return out
}

// isCall reports whether stmt is a call of a method named method.
func isCall(stmt ast.Stmt, method string) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}

	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)

	return ok && sel.Sel.Name == method
}

// testsReason reports whether cond is `name != ""`, or a chain of && whose
// first operand is `name != ""`. So the skip needs the reason from Off.
func testsReason(cond ast.Expr, name string) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}

	if bin.Op == token.LAND {
		return testsReason(bin.X, name)
	}

	id, okX := bin.X.(*ast.Ident)
	lit, okY := bin.Y.(*ast.BasicLit)

	return bin.Op == token.NEQ && okX && id.Name == name && okY && lit.Value == `""`
}

// funcBody parses file and returns the body of the function fn.
func funcBody(t *testing.T, file, fn string) *ast.BlockStmt {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	for _, decl := range parsed.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == fn && f.Body != nil {
			return f.Body
		}
	}

	t.Fatalf("%s holds no function %s", file, fn)

	return nil
}

// callsMethod reports whether node holds a call of a method named method.
func callsMethod(node ast.Node, method string) bool {
	found := false

	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
				found = true
			}
		}

		return !found
	})

	return found
}

// isSkip reports whether stmt is a call of Skip, Skipf or SkipNow.
func isSkip(stmt ast.Stmt) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}

	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)

	return ok && strings.HasPrefix(sel.Sel.Name, "Skip")
}

// TestRegenerateBaselineFollowsTheRecord runs scripts/regenerate-baseline.sh
// in a new repository with each state of the record. It refuses a collapse
// after the first tag before it needs a database. Before the first tag it
// passes the record and stops at the missing database.
func TestRegenerateBaselineFollowsTheRecord(t *testing.T) {
	t.Parallel()

	script := readScript(t)

	sum := sumOf("base")
	shippedLines := "v0.1.0 sqlite " + baseline + " " + sum + "\nv0.1.0 postgres " + baseline + " " + sum + "\n"

	tests := []struct {
		name   string
		record *string
		setup  func(t *testing.T, dir string)
		want   string
	}{
		{"not released", new("# comments only\n"), nil, "set DUSTPAN_TEST_POSTGRES_URL"},
		{"released", new("# a comment\n" + shippedLines), nil, "lists a shipped migration"},
		{"no record", nil, nil, "is missing"},
		{"a tag without record lines", new("# comments only\n"), addTag, "has a release tag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := newRepository(t, tt.record)
			if tt.setup != nil {
				tt.setup(t, dir)
			}

			// The script goes in on standard input, so that the command
			// line holds no variable.
			cmd := exec.CommandContext(t.Context(), "bash", "-s")
			cmd.Stdin = strings.NewReader(script)

			out, err := run(cmd, dir)
			if err == nil {
				t.Fatalf("the script passed, want a refusal:\n%s", out)
			}

			if !strings.Contains(out, tt.want) {
				t.Errorf("the script said:\n%s\nwant %q", out, tt.want)
			}
		})
	}
}

// readScript returns the text of scripts/regenerate-baseline.sh.
func readScript(t *testing.T) string {
	t.Helper()

	return readScriptFile(t, "regenerate-baseline.sh")
}

// readScriptFile returns the text of the script name in scripts/.
func readScriptFile(t *testing.T, name string) string {
	t.Helper()

	root, err := os.OpenRoot("../../scripts")
	if err != nil {
		t.Fatal(err)
	}

	defer closeRoot(t, root)

	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// newRepository makes a Git repository that holds the record, when record is
// not nil.
func newRepository(t *testing.T, record *string) string {
	t.Helper()

	dir := t.TempDir()

	if out, err := run(exec.CommandContext(t.Context(), "git", "init", "--quiet"), dir); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	if record == nil {
		return dir
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	defer closeRoot(t, root)

	if err := root.MkdirAll("internal/releaserecord", 0o700); err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(releaserecord.Path, []byte(*record), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

// addTag commits nothing and tags the commit v0.1.0.
func addTag(t *testing.T, dir string) {
	t.Helper()

	commit := exec.CommandContext(t.Context(), "git",
		"-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false",
		"commit", "--quiet", "--allow-empty", "-m", "first")
	if out, err := run(commit, dir); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	tag := exec.CommandContext(t.Context(), "git", "-c", "tag.gpgsign=false", "tag", "v0.1.0")
	if out, err := run(tag, dir); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}
}

// run runs cmd in dir with no system or user Git configuration and no other
// variable than PATH and extraEnv, and returns its combined output.
func run(cmd *exec.Cmd, dir string, extraEnv ...string) (string, error) {
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1"}, extraEnv...)

	out, err := cmd.CombinedOutput()

	return string(out), err
}

// closeRoot closes root and fails t on an error.
func closeRoot(t *testing.T, root *os.Root) {
	t.Helper()

	if err := root.Close(); err != nil {
		t.Error(err)
	}
}

// twoTags returns a record that the documented step writes at v0.1.0 and
// again at v0.2.0: each tag lists every file at the tag, so the baseline
// appears under both tags.
func twoTags(t *testing.T) releaserecord.Record {
	t.Helper()

	var data strings.Builder

	for _, dialect := range releaserecord.Dialects() {
		data.WriteString("v0.1.0 " + dialect + " " + baseline + " " + sumOf("base") + "\n")
	}

	for _, dialect := range releaserecord.Dialects() {
		data.WriteString("v0.2.0 " + dialect + " " + baseline + " " + sumOf("base") + "\n")
		data.WriteString("v0.2.0 " + dialect + " 0169_next.sql " + sumOf("next") + "\n")
	}

	rec, err := releaserecord.Parse([]byte(data.String()))
	if err != nil {
		t.Fatalf("Parse() of a record of two tags: %v", err)
	}

	return rec
}

// TestTwoTagsFollowTheRecord proves that the documented step works at a
// second tag, and that the files of both tags stay frozen.
func TestTwoTagsFollowTheRecord(t *testing.T) {
	t.Parallel()

	rec := twoTags(t)

	tests := []struct {
		name     string
		files    map[string]string
		problems int
	}{
		{"the files of both tags", map[string]string{baseline: "base", "0169_next.sql": "next"}, 0},
		{"a new file after the second tag", map[string]string{
			baseline: "base", "0169_next.sql": "next", "0170_more.sql": "more",
		}, 0},
		{"an edited file of the second tag", map[string]string{baseline: "base", "0169_next.sql": "edited"}, 1},
		{"a removed file of the second tag", map[string]string{baseline: "base"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			problems := rec.MigrationFiles("postgres", baseline, tt.files)
			if len(problems) != tt.problems {
				t.Errorf("MigrationFiles() = %v, want %d problems", problems, tt.problems)
			}
		})
	}
}

// TestTagCopiesFollowTheRecord sends tag copies directly to the check that
// each copy has its line in the record.
func TestTagCopiesFollowTheRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rec      releaserecord.Record
		tag      string
		files    map[string]string
		problems int
	}{
		{"the copies of the first tag", twoTags(t), "v0.1.0", map[string]string{baseline: "base"}, 0},
		{
			"the copies of the second tag", twoTags(t), "v0.2.0",
			map[string]string{baseline: "base", "0169_next.sql": "next"},
			0,
		},
		{"the lines of the tag are removed", notReleased(t), "v0.1.0", map[string]string{baseline: "base"}, 1},
		{"a tag that the record does not list", twoTags(t), "v0.3.0", map[string]string{baseline: "base"}, 1},
		{"an edited copy", twoTags(t), "v0.1.0", map[string]string{baseline: "edited"}, 1},
		{
			"a copy that the tag did not ship", twoTags(t), "v0.1.0",
			map[string]string{baseline: "base", "0169_next.sql": "next"},
			1,
		},
		{"a listed file with no copy", twoTags(t), "v0.2.0", map[string]string{baseline: "base"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			problems := tt.rec.TagCopies("sqlite", tt.tag, tt.files)
			if len(problems) != tt.problems {
				t.Errorf("TagCopies() = %v, want %d problems", problems, tt.problems)
			}
		})
	}
}

// TestCheckReleaseRecordRefuses runs scripts/check-release-record.sh in a new
// repository with each state of the record and the migration files. It
// passes only when the lines of the version match the files of each dialect.
func TestCheckReleaseRecordRefuses(t *testing.T) {
	t.Parallel()

	script := readScriptFile(t, "check-release-record.sh")
	lines := func(version, sqliteSum string) string {
		return version + " sqlite " + baseline + " " + sqliteSum + "\n" +
			version + " postgres " + baseline + " " + sumOf("base") + "\n"
	}

	next := map[string]string{"pkg/store/sqlite/migrations/0169_next.sql": "next"}

	tests := []struct {
		name    string
		version string
		record  *string
		extra   map[string]string
		want    string
		pass    bool
	}{
		{
			"the lines match",
			"v0.1.0",
			new("# comment\n" + lines("v0.1.0", sumOf("base"))),
			nil,
			"lists the migrations",
			true,
		},
		{"an empty record", "v0.1.0", new("# comments only\n"), nil, "has no sqlite line for v0.1.0", false},
		{
			"the lines of another version",
			"v0.2.0",
			new(lines("v0.1.0", sumOf("base"))),
			nil,
			"has no sqlite line",
			false,
		},
		{"another SHA-256", "v0.1.0", new(lines("v0.1.0", sumOf("edited"))), nil, "differ from", false},
		{"a file with no line", "v0.1.0", new(lines("v0.1.0", sumOf("base"))), next, "differ from", false},
		{"no record", "v0.1.0", nil, nil, "is missing", false},
		{
			"a CR at the end of a line",
			"v0.1.0",
			new(strings.ReplaceAll(lines("v0.1.0", sumOf("base")), "\n", "\r\n")),
			nil,
			"has a CR or NUL",
			false,
		},
		{
			"a CR inside a line",
			"v0.1.0",
			new(strings.Replace(lines("v0.1.0", sumOf("base")), "sqlite ", "sqlite\r", 1)),
			nil,
			"has a CR or NUL",
			false,
		},
		{
			"a NUL in a comment",
			"v0.1.0",
			new("# comment\x00\n" + lines("v0.1.0", sumOf("base"))),
			nil,
			"has a CR or NUL",
			false,
		},
		{
			"a NUL inside a line",
			"v0.1.0",
			new(strings.Replace(lines("v0.1.0", sumOf("base")), baseline, "0168_base\x00line.sql", 1)),
			nil,
			"has a CR or NUL",
			false,
		},
		{"a version with a suffix", "v0.1.0-rc1", new(lines("v0.1.0", sumOf("base"))), nil, "is not vMAJOR", false},
		{
			"a version with a newline",
			"v0.1.0\nv0.2.0",
			new(lines("v0.1.0", sumOf("base"))),
			nil,
			"is not vMAJOR",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := newRepository(t, tt.record)
			writeMigrations(t, dir, tt.extra)

			// The script and its argument go in on standard input, so that
			// the command line holds no variable.
			cmd := exec.CommandContext(t.Context(), "bash", "-s")
			cmd.Stdin = strings.NewReader(`set -- "$RELEASE_VERSION"` + "\n" + script)

			out, err := run(cmd, dir, "RELEASE_VERSION="+tt.version)
			if (err == nil) != tt.pass {
				t.Fatalf("the script returned %v, want pass = %v:\n%s", err, tt.pass, out)
			}

			if !strings.Contains(out, tt.want) {
				t.Errorf("the script said:\n%s\nwant %q", out, tt.want)
			}
		})
	}
}

// writeMigrations writes the baseline "base" of each dialect in dir, and the
// files of extra, a map from path to SQL.
func writeMigrations(t *testing.T, dir string, extra map[string]string) {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	defer closeRoot(t, root)

	files := map[string]string{}
	for _, dialect := range releaserecord.Dialects() {
		files["pkg/store/"+dialect+"/migrations/"+baseline] = "base"
	}

	maps.Copy(files, extra)

	for name, sql := range files {
		if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := root.WriteFile(name, []byte(sql), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestReleaseBuildChecksTheRecordFirst proves that scripts/release-build.sh
// runs the record check before it builds anything.
func TestReleaseBuildChecksTheRecordFirst(t *testing.T) {
	t.Parallel()

	script := readScriptFile(t, "release-build.sh")

	check := strings.Index(script, `"$root/scripts/check-release-record.sh" "$version"`)
	build := strings.Index(script, "go build")

	if check < 0 || build < 0 || check > build {
		t.Errorf("release-build.sh runs the record check at %d and go build at %d: "+
			"run check-release-record.sh before the build", check, build)
	}

	for line := range strings.SplitSeq(script[:max(check, 0)], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "exit 0") {
			t.Errorf("release-build.sh can exit before the record check: %q", line)
		}
	}
}
