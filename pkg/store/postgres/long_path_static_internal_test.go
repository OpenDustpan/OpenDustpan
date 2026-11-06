package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// pathForm is the allowed form of a function that uses the pool with no
// guarded wrapper.
type pathForm int

// The allowed forms.
const (
	// formPoolOwner opens, pings or closes the pool. It runs no statement of
	// a request.
	formPoolOwner pathForm = iota + 1
	// formGuarded is a guarded wrapper itself: it bounds each round trip.
	formGuarded
	// formSetLocal runs in a transaction and sets both server bounds with
	// SET LOCAL through setLocalBounds.
	formSetLocal
	// formHelper is the long-path helper: it marks the connection, so the
	// pool resets it at its release.
	formHelper
	// formSession keeps a connection of the helper for a session and changes
	// no session value.
	formSession
	// formCheck runs the fixed write check of the pool guard on a connection.
	formCheck
)

// longPathAllowlist names each function that uses the pool with no guarded
// wrapper, and its allowed form. A new function that uses the pool fails the
// test until it is in this list with a form that the test can check.
func longPathAllowlist() map[string]pathForm {
	return map[string]pathForm{
		"Open":            formPoolOwner,
		"CloseContext":    formPoolOwner,
		"Ping":            formPoolOwner,
		"migrate":         formSetLocal,
		"inLongTx":        formSetLocal,
		"longConn":        formHelper,
		"unmarkForHijack": formHelper,
		"afterRelease":    formHelper,
		"TryLockSweep":    formSession,
		"idleReadOnly":    formCheck,
		"checkWritable":   formCheck,
		"reset":           formCheck,
		"Begin":           formGuarded,
		"BeginTx":         formGuarded,
	}
}

// poolCalls are the calls that take a connection from the pool or from its
// wrapper.
func poolCalls() []string { return []string{"Acquire", "AcquireAllIdle", "Hijack", "Conn"} }

// sessionSet matches a statement that changes a session value: a SET with no
// LOCAL, which names one setting and holds no list and no WHERE, and
// set_config with false as its last argument. The SET part of an UPDATE
// (a list of columns, or a WHERE) does not match.
var sessionSet = regexp.MustCompile(
	`(?i)(^\s*SET\s+(SESSION\s+)?[a-z_.]+\s*(=|\bTO\b)[^,]*$)|(set_config\([^)]*,\s*false\s*\))`,
)

// updateClause matches the WHERE of an UPDATE, which a session SET never has.
var updateClause = regexp.MustCompile(`(?i)\bWHERE\b`)

// isSessionSet reports whether stmt changes a session value.
func isSessionSet(stmt string) bool {
	return sessionSet.MatchString(stmt) && !updateClause.MatchString(stmt)
}

// pathUse is what the check found in one function.
type pathUse struct {
	usesPool    bool
	setsLocal   bool
	usesHelper  bool
	sessionSets []string
}

// TestLongPathsNameTheirBound finds each function of the package that uses
// the pool with no guarded wrapper, with a static check of the source: each
// direct use of a field named pool, and each call of Acquire,
// AcquireAllIdle, Hijack or Conn. Each such function must be in the
// allowlist, and its allowed form must hold: a set-local path calls
// setLocalBounds, which sets both statement_timeout and
// idle_in_transaction_session_timeout; a session path gets its connection
// from the helper. No function may run a session SET or set_config(...,
// false), because the value would stay on a connection that goes back to the
// pool.
func TestLongPathsNameTheirBound(t *testing.T) {
	t.Parallel()

	uses := scanPackage(t)
	allow := longPathAllowlist()

	for name, use := range uses {
		for _, stmt := range use.sessionSets {
			t.Errorf("%s runs a session SET %q: use SET LOCAL in a transaction, or set_config with true", name, stmt)
		}

		if !use.usesPool && !use.usesHelper {
			continue
		}

		form, ok := allow[name]
		if !ok {
			t.Errorf("%s uses the pool with no guarded wrapper: run it through inLongTx with its bound, "+
				"or add it to the allowlist with its form", name)

			continue
		}

		checkForm(t, name, form, use)
	}

	for name := range allow {
		if !uses[name].usesPool && !uses[name].usesHelper {
			t.Errorf("allowlist entry %s uses no pool: remove it", name)
		}
	}
}

// checkForm checks that the use of name holds its allowed form.
func checkForm(t *testing.T, name string, form pathForm, use pathUse) {
	t.Helper()

	switch form {
	case formSetLocal:
		if !use.setsLocal {
			t.Errorf("%s must set both server bounds with setLocalBounds", name)
		}
	case formSession:
		if !use.usesHelper {
			t.Errorf("%s must get its connection from longConn", name)
		}
	case formPoolOwner, formGuarded, formHelper, formCheck:
	default:
		t.Errorf("%s has an unknown form %d", name, form)
	}
}

// scanPackage parses each source file of the package, without the tests, and
// returns what each function does with the pool.
func scanPackage(t *testing.T) map[string]pathUse {
	t.Helper()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	out := map[string]pathUse{}

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		parsed, parseErr := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			use := out[fn.Name.Name]
			scanBody(fn.Body, &use)
			out[fn.Name.Name] = use
		}
	}

	return out
}

// scanBody records the uses of the pool in body.
func scanBody(body *ast.BlockStmt, use *pathUse) {
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			if n.Sel.Name == "pool" && ownsPool(n.X) {
				use.usesPool = true
			}
		case *ast.CallExpr:
			scanCall(n, use)
		case *ast.BasicLit:
			if n.Kind == token.STRING && isSessionSet(strings.Trim(n.Value, "`\"")) {
				use.sessionSets = append(use.sessionSets, n.Value)
			}
		default:
		}

		return true
	})
}

// ownsPool reports whether x is a value whose field pool is the connection
// pool: the store (receiver s, or x.s), the pool guard and the guarded
// starter (receiver g). Other types have a field pool too, for example a
// runner pool, so the check keeps to these names.
func ownsPool(x ast.Expr) bool {
	switch v := x.(type) {
	case *ast.Ident:
		return v.Name == "s" || v.Name == "g"
	case *ast.SelectorExpr:
		return v.Sel.Name == "s"
	default:
		return false
	}
}

// scanCall records a call of the pool, of the helper or of setLocalBounds.
func scanCall(call *ast.CallExpr, use *pathUse) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if slices.Contains(poolCalls(), fun.Sel.Name) {
			use.usesPool = true
		}

		if fun.Sel.Name == "longConn" {
			use.usesHelper = true
		}
	case *ast.Ident:
		if fun.Name == "setLocalBounds" {
			use.setsLocal = true
		}
	default:
	}
}

// TestSessionSetPattern proves the pattern of a session SET: it matches a SET
// with no LOCAL and set_config with false, and not SET LOCAL, set_config with
// true, or the SET of an UPDATE.
func TestSessionSetPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		stmt string
		want bool
	}{
		{"SET statement_timeout = 0", true},
		{"set search_path TO public", true},
		{"SET SESSION idle_in_transaction_session_timeout = 0", true},
		{"SELECT set_config('statement_timeout', '0', false)", true},
		{"SET LOCAL statement_timeout = 0", false},
		{"SELECT set_config('lock_timeout', $1, true)", false},
		{"UPDATE workspaces SET revision = revision + 1", false},
		{"SET lock_id = '', lock_holder = '' WHERE lock_id <> ''", false},
		{"SET lock_holder = NULL WHERE tool = $1", false},
	}

	for _, tc := range tests {
		t.Run(tc.stmt, func(t *testing.T) {
			t.Parallel()

			if got := isSessionSet(tc.stmt); got != tc.want {
				t.Fatalf("match = %t, want %t", got, tc.want)
			}
		})
	}
}
