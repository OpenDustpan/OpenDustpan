package problem_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// apiDir is the directory internal/api, seen from this package.
const apiDir = ".."

// okStatuses returns the net/http status constants that a success answer may
// use in a WriteHeader call. Any other http.Status constant is an error status.
func okStatuses() []string {
	return []string{
		"StatusOK",
		"StatusCreated",
		"StatusAccepted",
		"StatusNoContent",
		"StatusPartialContent",
		"StatusMultiStatus",
		"StatusNotModified",
		"StatusSeeOther",
		"StatusFound",
		"StatusMovedPermanently",
		"StatusTemporaryRedirect",
		"StatusPermanentRedirect",
	}
}

// ownContractDirs returns the adapters that keep their own wire contract. The
// gate does not check them. Each value is the reason. The list only shrinks.
func ownContractDirs() map[string]string {
	return map[string]string{
		"pulumi":    "the Pulumi Cloud-compatible API keeps the error shape that the Pulumi CLI expects",
		"tfe":       "the TFE-compatible API keeps the JSON:API error documents that OpenTofu expects",
		"tofuhttp":  "the OpenTofu HTTP backend keeps the plain text errors and the 409 and 423 answers that OpenTofu expects",
		"runnerapi": "the runner protocol has its own codes and its own versions",
	}
}

// ownBodies returns, for each gated directory, the struct types with a JSON
// member code, errorCode or errors that are not written through problem.Writer.
// Each value is the reason. The list only shrinks.
func ownBodies() map[string]map[string]string {
	return map[string]map[string]string{
		"problem": {
			"Body": "the one error body of the tool-neutral APIs",
		},
		"server": {
			"errorResponse":        "an own error writer of the installation API that has not moved to problem.Writer yet",
			"codedResponse":        "an own error writer of the installation API that has not moved to problem.Writer yet",
			"enterRefusedResponse": "an own error writer of the installation API that has not moved to problem.Writer yet",
			"devCodeRequest":       "the request body of the dev sign-in; code is the sign-in code",
			"getRestoreResponse":   "a 200 row; errorCode repeats the stored catalog code of a restore",
			"restoreSummaryJSON":   "a 200 row; errorCode repeats the stored catalog code of a restore",
			"restoreWarningJSON":   "a warning in the report of a restore that succeeded; it is no error answer",
		},
		"settings": {
			"bulkError": "the error of one item inside a 200 bulk answer",
		},
	}
}

// writerViolations parses the non-test Go files of dir and returns the sorted
// violations of the writer rules, each as "<file>: <rule>".
//
// Defense in depth: the error code gate in internal/errcode checks that an
// error answer carries a catalog code. This gate checks who writes it. Neither
// one relies on the other.
func writerViolations(dir string) ([]string, error) {
	bodies, err := errcodetest.BodyTypes(dir)
	if err != nil {
		return nil, fmt.Errorf("read body types of %s: %w", dir, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", dir, err)
	}

	owned := ownBodies()[filepath.Base(dir)]

	var out []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		fileOut, err := fileViolations(filepath.Join(dir, name), bodies, owned)
		if err != nil {
			return nil, err
		}

		out = append(out, fileOut...)
	}

	slices.Sort(out)

	return out, nil
}

// fileViolations returns the violations of the Go file at path. bodies are the
// error body types of its directory, and owned are the body types that have a
// reason in ownBodies.
func fileViolations(path string, bodies []string, owned map[string]string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	name := filepath.Base(path)

	var out []string

	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			out = append(out, callViolations(name, call)...)
		}

		if ts, ok := n.(*ast.TypeSpec); ok {
			_, isOwnedType := owned[ts.Name.Name]
			if slices.Contains(bodies, ts.Name.Name) && !isOwnedType {
				out = append(out, fmt.Sprintf("%s: error body type %s", name, ts.Name.Name))
			}
		}

		return true
	})

	return out, nil
}

// callViolations returns the violations of one call expression in file.
func callViolations(file string, call *ast.CallExpr) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" &&
		(sel.Sel.Name == "Error" || sel.Sel.Name == "NotFound") {
		return []string{file + ": http.Error"}
	}

	if sel.Sel.Name == "WriteHeader" && len(call.Args) == 1 {
		arg, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok {
			return nil
		}

		if pkg, ok := arg.X.(*ast.Ident); ok && pkg.Name == "http" &&
			!slices.Contains(okStatuses(), arg.Sel.Name) {
			return []string{file + ": error status in WriteHeader"}
		}
	}

	return nil
}

// staleRows returns a message for each row of contract and bodies that no
// longer matches the source. dirs are the source directories, and bodyTypes
// returns the error body types of a directory. The result is sorted.
func staleRows(
	dirs []string,
	contract map[string]string,
	bodies map[string]map[string]string,
	bodyTypes func(string) ([]string, error),
) []string {
	var out []string

	for key := range contract {
		if !slices.Contains(dirs, key) {
			out = append(out, fmt.Sprintf("ownContractDirs row %q is not a source directory; remove the row", key))
		}
	}

	for dir, types := range bodies {
		if !slices.Contains(dirs, dir) {
			out = append(out, fmt.Sprintf("ownBodies row %q is not a source directory; remove the row", dir))

			continue
		}

		have, err := bodyTypes(dir)
		if err != nil {
			out = append(out, fmt.Sprintf("ownBodies row %q: read body types: %v", dir, err))

			continue
		}

		for name := range types {
			if !slices.Contains(have, name) {
				out = append(out, fmt.Sprintf(
					"ownBodies row %q lists %q, which is no longer an error body type; remove it",
					dir, name,
				))
			}
		}
	}

	slices.Sort(out)

	return out
}

// TestToolNeutralAdaptersUseProblemWriter fails when an adapter under
// internal/api writes an error body without problem.Writer, or when a row of
// the exception lists no longer matches the source.
func TestToolNeutralAdaptersUseProblemWriter(t *testing.T) {
	t.Parallel()

	dirs, err := errcodetest.SourceDirs(apiDir)
	if err != nil {
		t.Fatalf("list source directories: %v", err)
	}

	contract := ownContractDirs()

	for _, d := range dirs {
		first, _, _ := strings.Cut(filepath.ToSlash(d), "/")
		if _, ok := contract[d]; ok {
			continue
		}

		if _, ok := contract[first]; ok {
			continue
		}

		t.Run(d, func(t *testing.T) {
			t.Parallel()

			violations, err := writerViolations(filepath.Join(apiDir, d))
			if err != nil {
				t.Fatalf("check %s: %v", d, err)
			}

			for _, v := range violations {
				t.Errorf("%s: write the error through problem.Writer, or add the type to ownBodies with a reason", v)
			}
		})
	}

	t.Run("stale rows", func(t *testing.T) {
		t.Parallel()

		bodyTypes := func(d string) ([]string, error) {
			return errcodetest.BodyTypes(filepath.Join(apiDir, d))
		}

		for _, msg := range staleRows(dirs, contract, ownBodies(), bodyTypes) {
			t.Error(msg)
		}
	})
}

// checkFixture writes the files of one fixture package and compares the
// violations of its directory with want.
func checkFixture(t *testing.T, files map[string]string, want []string) {
	t.Helper()

	// The directory is named a, which has no entry in ownBodies.
	dir := filepath.Join(t.TempDir(), "a")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}

	for name, src := range files {
		body := "package a\n\n" + strings.ReplaceAll(src, "'", "`") + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	got, err := writerViolations(dir)
	if err != nil {
		t.Fatalf("writerViolations: %v", err)
	}

	if !slices.Equal(got, want) {
		t.Errorf("writerViolations = %q, want %q", got, want)
	}
}

// TestWriterViolations checks the writer rules on fixture packages in a
// temporary directory. Each fixture file is written with the package clause
// "package a" before its source. A backtick in a fixture is written as a single
// quote, because a Go raw string cannot hold a backtick.
func TestWriterViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "clean",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }`,
			},
			want: nil,
		},
		{
			name: "http.Error",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { http.Error(w, "x", 400) }`,
			},
			want: []string{"a.go: http.Error"},
		},
		{
			name: "http.NotFound",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }`,
			},
			want: []string{"a.go: http.Error"},
		},
		{
			name: "WriteHeader 400",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest) }`,
			},
			want: []string{"a.go: error status in WriteHeader"},
		},
		{
			name: "WriteHeader 500",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }`,
			},
			want: []string{"a.go: error status in WriteHeader"},
		},
		{
			name: "WriteHeader variable",
			files: map[string]string{
				"a.go": `import "net/http"; func f(w http.ResponseWriter, s int) { w.WriteHeader(s) }`,
			},
			want: nil,
		},
		{
			name: "own error body",
			files: map[string]string{
				"a.go": `type errBody struct { Code int 'json:"code"'; Message string 'json:"message"' }`,
			},
			want: []string{"a.go: error body type errBody"},
		},
		{
			name: "own body with errorCode",
			files: map[string]string{
				"a.go": `type e2 struct { ErrorCode string 'json:"errorCode"' }`,
			},
			want: []string{"a.go: error body type e2"},
		},
		{
			name: "test file ignored",
			files: map[string]string{
				"a.go":      `import "net/http"; func f(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }`,
				"a_test.go": `import "net/http"; func f(w http.ResponseWriter) { http.Error(w, "x", 400) }`,
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkFixture(t, tt.files, tt.want)
		})
	}
}

// errRead is the read failure of the fixture in TestStaleRows.
var errRead = errors.New("read failed")

// TestStaleRows checks that staleRows reports each row that no longer matches
// the source, and nothing for rows that match.
func TestStaleRows(t *testing.T) {
	t.Parallel()

	types := map[string][]string{
		"server": {"errorResponse"},
	}

	tests := []struct {
		name     string
		dirs     []string
		contract map[string]string
		bodies   map[string]map[string]string
		readErr  error
		want     int
	}{
		{
			name:     "matching rows",
			dirs:     []string{"server", "pulumi"},
			contract: map[string]string{"pulumi": "reason"},
			bodies:   map[string]map[string]string{"server": {"errorResponse": "reason"}},
			readErr:  nil,
			want:     0,
		},
		{
			name:     "contract key not in dirs",
			dirs:     []string{"server"},
			contract: map[string]string{"pulumi": "reason"},
			bodies:   nil,
			readErr:  nil,
			want:     1,
		},
		{
			name:     "body type not returned",
			dirs:     []string{"server"},
			contract: nil,
			bodies:   map[string]map[string]string{"server": {"gone": "reason"}},
			readErr:  nil,
			want:     1,
		},
		{
			name:     "body types cannot be read",
			dirs:     []string{"server"},
			contract: nil,
			bodies:   map[string]map[string]string{"server": {"errorResponse": "reason"}},
			readErr:  errRead,
			want:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bodyTypes := func(d string) ([]string, error) {
				return types[d], tt.readErr
			}

			got := staleRows(tt.dirs, tt.contract, tt.bodies, bodyTypes)
			if len(got) != tt.want {
				t.Errorf("staleRows = %q, want %d message(s)", got, tt.want)
			}
		})
	}
}

// faultLogExceptions returns the files that may write an error-level log line
// without internal/api/faultlog. Each key is a path under internal/api and
// each value is the reason. The list only shrinks.
func faultLogExceptions() map[string]string {
	return map[string]string{
		"apidesc/request.go": "a developer message about a handler that reads an undeclared name; it is no failed request and has no context",
	}
}

// faultCalls returns the functions of faultlog that write a failure log line.
func faultCalls() []string {
	return []string{"LogInternal", "LogFault", "LogServer"}
}

// faultLogViolations parses the non-test Go files of dir and returns the
// sorted violations of the failure log rules, each as "<file>: <rule>":
//
//   - an error-level log call (ErrorContext, Error, Log, LogAttrs), a reference
//     to slog.LevelError, slog.NewRecord or slog.Handler, a conversion
//     slog.Level(x), or a call of Handler, outside internal/api/faultlog;
//   - a call of faultlog.LogInternal or faultlog.LogFault whose message is
//     not a string literal;
//   - any call in the arguments of a faultlog call, except faultlog.Cause,
//     because a call can return an error text.
//
// Defense in depth: the writer rule above checks who writes the body. These
// rules check who writes the log line, and the helper takes no free
// attribute. Neither one relies on the other.
func faultLogViolations(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", dir, err)
	}

	rel, relErr := filepath.Rel(apiDir, dir)
	if relErr != nil {
		rel = dir
	}

	var out []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		if _, ok := faultLogExceptions()[filepath.ToSlash(filepath.Join(rel, name))]; ok {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			for _, rule := range nodeFaultRules(n) {
				out = append(out, name+": "+rule)
			}

			return true
		})
	}

	slices.Sort(out)

	return out, nil
}

// nodeFaultRules returns the failure log rules that n breaks.
func nodeFaultRules(n ast.Node) []string {
	switch x := n.(type) {
	case *ast.SelectorExpr:
		return selectorFaultRules(x)
	case *ast.CallExpr:
		return callFaultRules(x)
	default:
		return nil
	}
}

// selectorFaultRules returns the failure log rules that sel breaks.
func selectorFaultRules(sel *ast.SelectorExpr) []string {
	// A method value, as in f := l.ErrorContext, is a bypass too.
	if slices.Contains([]string{"ErrorContext", "LogAttrs"}, sel.Sel.Name) {
		return []string{"error-level log call without faultlog"}
	}

	if identName(sel.X) == "slog" && slices.Contains([]string{"LevelError", "NewRecord", "Handler"}, sel.Sel.Name) {
		return []string{"slog." + sel.Sel.Name + " outside faultlog"}
	}

	return nil
}

// identName returns the name of e when e is an identifier, and "" otherwise.
func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}

	return ""
}

// callFaultRules returns the failure log rules that call breaks.
func callFaultRules(call *ast.CallExpr) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	recv := identName(sel.X)
	name := sel.Sel.Name
	args := len(call.Args)

	switch {
	case recv == "faultlog" && slices.Contains(faultCalls(), name):
		return faultCallRules(name, call)
	case recv == "slog" && name == "Level":
		return []string{"slog.Level conversion outside faultlog"}
	case name == "Handler" && args == 0:
		return []string{"Handler() outside faultlog"}
	default:
		return logCallRules(recv, name, args)
	}
}

// logCallRules returns the rule that a call of Log or Error breaks. A call of
// Log has a context and a level, and a call of Error has a message. An error
// text has no arguments, and http.Error and errors.* are not log calls.
func logCallRules(recv, name string, args int) []string {
	isLog := name == "Log" && args > 1
	isError := name == "Error" && args > 0 && recv != "http" && recv != "errors"

	if isLog || isError {
		return []string{"error-level log call without faultlog"}
	}

	return nil
}

// faultCallRules returns the rules that a call of a faultlog function breaks.
func faultCallRules(name string, call *ast.CallExpr) []string {
	// The context and the logger are not logged. Every other argument is.
	const firstLogged = 2

	if len(call.Args) <= firstLogged {
		return []string{"faultlog call with too few arguments"}
	}

	var out []string

	// Every faultlog call has a message at the same place, and it is a literal.
	if !isStringLiteral(call.Args[firstLogged]) {
		out = append(out, "faultlog message is not a string literal")
	}

	// The op of a fault names a call site, so it is a literal too.
	const opIndex = 4
	if name == "LogFault" && len(call.Args) > opIndex && !isStringLiteral(call.Args[opIndex]) {
		out = append(out, "faultlog op is not a string literal")
	}

	for _, arg := range call.Args[firstLogged:] {
		out = append(out, callsInArgument(arg)...)
	}

	return out
}

// isStringLiteral returns true when e is a string literal.
func isStringLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)

	return ok && lit.Kind == token.STRING
}

// callsInArgument returns one rule for each call in arg, except the calls of
// faultlog.Cause, because a call can return an error text.
func callsInArgument(arg ast.Expr) []string {
	var out []string

	ast.Inspect(arg, func(n ast.Node) bool {
		inner, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		if fn, isSel := inner.Fun.(*ast.SelectorExpr); isSel && identName(fn.X) == "faultlog" &&
			fn.Sel.Name == "Cause" {
			// The cause class is from a closed list: do not look inside.
			return false
		}

		out = append(out, "call in an argument of faultlog")

		return true
	})

	return out
}

// TestAdaptersLogFailuresWithFaultlog fails when an adapter under
// internal/api writes an error-level log line without internal/api/faultlog,
// the adapters with their own wire contract included, or when a row of the
// exception list no longer matches the source.
func TestAdaptersLogFailuresWithFaultlog(t *testing.T) {
	t.Parallel()

	dirs, err := errcodetest.SourceDirs(apiDir)
	if err != nil {
		t.Fatalf("list source directories: %v", err)
	}

	for _, d := range dirs {
		if d == "faultlog" {
			continue
		}

		t.Run(d, func(t *testing.T) {
			t.Parallel()

			violations, err := faultLogViolations(filepath.Join(apiDir, d))
			if err != nil {
				t.Fatalf("check %s: %v", d, err)
			}

			for _, v := range violations {
				t.Errorf("%s: log the failure with faultlog.LogInternal or faultlog.LogFault", v)
			}
		})
	}

	t.Run("stale rows", func(t *testing.T) {
		t.Parallel()

		for key := range faultLogExceptions() {
			if _, err := os.Stat(filepath.Join(apiDir, filepath.FromSlash(key))); err != nil {
				t.Errorf("faultLogExceptions row %q is not a source file; remove the row", key)
			}
		}
	})
}

// TestFaultLogViolations checks the failure log rules on fixture files, one
// for each way to bypass the rule that a reviewer named.
func TestFaultLogViolations(t *testing.T) {
	t.Parallel()

	const head = `import ("context"; "log/slog"; "errors"; "opendustpan.org/opendustpan/internal/api/faultlog"); ` +
		`var _ = errors.New; var _ = faultlog.CauseNetwork; `

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "faultlog call is clean",
			src: `func f(c context.Context, l *slog.Logger, err error) { ` +
				`faultlog.LogInternal(c, l, "x", faultlog.Cause(err), "server.internal") }`,
			want: nil,
		},
		{
			name: "warn level is not checked",
			src:  `func f(c context.Context, l *slog.Logger, err error) { l.WarnContext(c, "x", slog.String("error", err.Error())) }`,
			want: nil,
		},
		{
			name: "own status attribute",
			src:  `func f(c context.Context, l *slog.Logger) { l.ErrorContext(c, "x", slog.Int("status", 500)) }`,
			want: []string{"a.go: error-level log call without faultlog"},
		},
		{
			name: "status key in a constant",
			src: `const key = "status"; func f(c context.Context, l *slog.Logger) { ` +
				`l.ErrorContext(c, "x", slog.Int(key, 500)) }`,
			want: []string{"a.go: error-level log call without faultlog"},
		},
		{
			name: "Log with LevelError",
			src:  `func f(c context.Context, l *slog.Logger) { l.Log(c, slog.LevelError, "x") }`,
			want: []string{"a.go: error-level log call without faultlog", "a.go: slog.LevelError outside faultlog"},
		},
		{
			name: "Log with a level from a variable",
			src:  `func f(c context.Context, l *slog.Logger, lvl slog.Level) { l.Log(c, lvl, "x") }`,
			want: []string{"a.go: error-level log call without faultlog"},
		},
		{
			name: "ErrorContext with an error text in the message",
			src:  `func f(c context.Context, l *slog.Logger, err error) { l.ErrorContext(c, err.Error()) }`,
			want: []string{"a.go: error-level log call without faultlog"},
		},
		{
			name: "method value",
			src: `func f(c context.Context, l *slog.Logger) { g := l.ErrorContext; ` +
				`g(c, "x") }`,
			want: []string{"a.go: error-level log call without faultlog"},
		},
		{
			name: "Error without context",
			src:  `func f(l *slog.Logger) { l.Error("x") }`,
			want: []string{"a.go: error-level log call without faultlog"},
		},
		{
			name: "record written to the handler",
			src: `func f(c context.Context, l *slog.Logger, r slog.Record) { ` +
				`_ = l.Handler().Handle(c, r) }`,
			want: []string{"a.go: Handler() outside faultlog"},
		},
		{
			name: "level from a number",
			src:  `func f(c context.Context, l *slog.Logger) { l.Log(c, slog.Level(8), "x") }`,
			want: []string{
				"a.go: error-level log call without faultlog",
				"a.go: slog.Level conversion outside faultlog",
			},
		},
		{
			name: "new record",
			src:  `func f() slog.Record { return slog.NewRecord(time.Time{}, 0, "x", 0) }`,
			want: []string{"a.go: slog.NewRecord outside faultlog"},
		},
		{
			name: "error text in a faultlog argument",
			src: `func f(c context.Context, l *slog.Logger, err error) { ` +
				`faultlog.LogInternal(c, l, "x", faultlog.CauseNetwork, err.Error()) }`,
			want: []string{"a.go: call in an argument of faultlog"},
		},
		{
			name: "formatted error in a faultlog argument",
			src: `func f(c context.Context, l *slog.Logger, err error) { ` +
				`faultlog.LogFault(c, l, "x", faultlog.CauseNetwork, fmt.Sprint(err)) }`,
			want: []string{"a.go: call in an argument of faultlog", "a.go: faultlog op is not a string literal"},
		},
		{
			name: "LogServer with an error text in the message",
			src: `func f(c context.Context, l *slog.Logger, err error) { ` +
				`msg := "x: " + err.Error(); faultlog.LogServer(c, l, msg, faultlog.Fields{Status: 500}) }`,
			want: []string{"a.go: faultlog message is not a string literal"},
		},
		{
			name: "LogServer with a call in the message",
			src: `func f(c context.Context, l *slog.Logger, err error) { ` +
				`faultlog.LogServer(c, l, err.Error(), faultlog.Fields{Status: 500}) }`,
			want: []string{"a.go: call in an argument of faultlog", "a.go: faultlog message is not a string literal"},
		},
		{
			name: "LogServer with an error text in a field",
			src: `func f(c context.Context, l *slog.Logger, err error) { ` +
				`faultlog.LogServer(c, l, "x", faultlog.Fields{Status: 500, Op: err.Error()}) }`,
			want: []string{"a.go: call in an argument of faultlog"},
		},
		{
			name: "LogFault op from a variable",
			src: `func f(c context.Context, l *slog.Logger, op string) { ` +
				`faultlog.LogFault(c, l, "x", faultlog.CauseNetwork, op) }`,
			want: []string{"a.go: faultlog op is not a string literal"},
		},
		{
			name: "message that is not a literal",
			src: `func f(c context.Context, l *slog.Logger, m string) { ` +
				`faultlog.LogInternal(c, l, m, faultlog.CauseNetwork, "") }`,
			want: []string{"a.go: faultlog message is not a string literal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			body := []byte("package a\n\n" + head + tt.src + "\n")

			err := os.WriteFile(filepath.Join(dir, "a.go"), body, 0o600)
			if err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			got, err := faultLogViolations(dir)
			if err != nil {
				t.Fatalf("faultLogViolations: %v", err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("faultLogViolations = %q, want %q", got, tt.want)
			}
		})
	}
}
