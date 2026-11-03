package releaserecord_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
		"three fields":         both("v0.1.0 sqlite " + baseline),
		"five fields":          both("v0.1.0 sqlite " + baseline + " " + sum + " extra"),
		"two spaces":           both("v0.1.0  sqlite " + baseline + " " + sum),
		"tab separator":        both("v0.1.0\tsqlite " + baseline + " " + sum),
		"leading space":        both(" v0.1.0 sqlite " + baseline + " " + sum),
		"trailing space":       both("v0.1.0 sqlite " + baseline + " " + sum + " "),
		"carriage return":      both("v0.1.0 sqlite " + baseline + " " + sum + "\r"),
		"tag without v":        both("0.1.0 sqlite " + baseline + " " + sum),
		"tag with suffix":      both("v0.1.0-rc1 sqlite " + baseline + " " + sum),
		"tag with zero":        both("v0.01.0 sqlite " + baseline + " " + sum),
		"unknown dialect":      both("v0.1.0 mysql " + baseline + " " + sum),
		"file with path":       both("v0.1.0 sqlite ../" + baseline + " " + sum),
		"file without number":  both("v0.1.0 sqlite baseline.sql " + sum),
		"file not sql":         both("v0.1.0 sqlite 0168_baseline.txt " + sum),
		"short sum":            both("v0.1.0 sqlite " + baseline + " abc"),
		"upper-case sum":       both("v0.1.0 sqlite " + baseline + " " + strings.ToUpper(sum)),
		"file listed twice":    both("v0.1.0 sqlite " + baseline + " " + sum + "\nv0.2.0 sqlite " + baseline + " " + sum),
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
	}
}

// TestEachGuardAsksTheRecord proves that each guard asks the record, and that
// none skips with no condition. A guard that ignores the record fails here.
func TestEachGuardAsksTheRecord(t *testing.T) {
	t.Parallel()

	for _, g := range guards() {
		t.Run(g.fn+" in "+filepath.Base(filepath.Dir(g.file)), func(t *testing.T) {
			t.Parallel()

			body := funcBody(t, g.file, g.fn)

			if !callsMethod(body, g.method) {
				t.Errorf("%s in %s does not call the %s method of the release record", g.fn, g.file, g.method)
			}

			for _, stmt := range body.List {
				if isSkip(stmt) {
					t.Errorf("%s in %s skips with no condition: skip only on the reason of the release record", g.fn, g.file)
				}
			}

			if g.method == "Off" {
				checkFirstSkip(t, g, body)
			}
		})
	}
}

// checkFirstSkip fails t unless the first if statement of body that skips
// gets the reason from Off in its init and tests that reason in its condition.
func checkFirstSkip(t *testing.T, g guard, body *ast.BlockStmt) {
	t.Helper()

	for _, stmt := range body.List {
		ifStmt, ok := stmt.(*ast.IfStmt)
		if !ok || !slices.ContainsFunc(ifStmt.Body.List, isSkip) {
			continue
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

		return
	}

	t.Errorf("%s in %s has no if statement that skips on the reason from Off", g.fn, g.file)
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

	script, err := filepath.Abs("../../scripts/regenerate-baseline.sh")
	if err != nil {
		t.Fatal(err)
	}

	sum := sumOf("base")
	shippedLines := "v0.1.0 sqlite " + baseline + " " + sum + "\nv0.1.0 postgres " + baseline + " " + sum + "\n"

	tests := []struct {
		name   string
		record *string
		tag    bool
		want   string
	}{
		{"not released", ptr("# comments only\n"), false, "set DUSTPAN_TEST_POSTGRES_URL"},
		{"released", ptr("# a comment\n" + shippedLines), false, "lists a shipped migration"},
		{"no record", nil, false, "is missing"},
		{"a tag without record lines", ptr("# comments only\n"), true, "has a release tag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := newRepository(t, tt.record, tt.tag)

			cmd := exec.CommandContext(t.Context(), "bash", script)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1"}

			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the script passed, want a refusal:\n%s", out)
			}

			if !strings.Contains(string(out), tt.want) {
				t.Errorf("the script said:\n%s\nwant %q", out, tt.want)
			}
		})
	}
}

func ptr(s string) *string {
	return &s
}

// newRepository makes a Git repository with the record, when record is not
// nil, and a release tag, when tag is true.
func newRepository(t *testing.T, record *string, tag bool) string {
	t.Helper()

	dir := t.TempDir()

	git(t, dir, "init", "--quiet")

	if record != nil {
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}

		defer root.Close()

		if err := root.MkdirAll("internal/releaserecord", 0o700); err != nil {
			t.Fatal(err)
		}

		if err := root.WriteFile(releaserecord.Path, []byte(*record), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if tag {
		git(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false",
			"commit", "--quiet", "--allow-empty", "-m", "first")
		git(t, dir, "-c", "tag.gpgsign=false", "tag", "v0.1.0")
	}

	return dir
}

// git runs git in dir with no system or user configuration.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1"}

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
