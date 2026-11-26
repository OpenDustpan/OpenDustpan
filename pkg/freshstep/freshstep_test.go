package freshstep_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/freshstep"
)

// freshCallees are the names of the freshness checks. A call of one of them
// is a call site that the test knows. sessionauth.Fresh counts only with the
// package name sessionauth.
func freshCallees() []string {
	return []string{
		"Fresh",
		"RequireFresh",
		"requireFresh",
		"checkTokenFreshness",
		"checkSignInFreshness",
		"requireAlwaysFresh",
	}
}

// freshSite is one function of one file that calls one freshness check.
// fn is the function name, with its receiver type for a method, for example
// "Handler.fresh".
type freshSite struct {
	file, fn, callee string
}

func (s freshSite) String() string { return s.file + ": " + s.fn + " calls " + s.callee }

// freshCallSites lists each function that may call a freshness check, and
// the check that it calls. A new call, in a new function or a new file, fails
// the test: call a check that asks a Guard, or add the site here with a
// reason. A site that is not in the code any more also fails the test.
func freshCallSites() []freshSite {
	return []freshSite{
		// The checks that a Guard wraps: the Guard asks the policy first.
		{"pkg/dustpan/dustpan.go", "tokenFreshCheck", "Fresh"},
		{"internal/trustrulesvc/service.go", "Service.checkFresh", "Fresh"},
		{"internal/api/issuer/freshness.go", "Handler.freshCheck", "Fresh"},
		{"internal/api/profile/freshness.go", "Handler.freshCheck", "Fresh"},
		{"internal/api/pulumi/freshness.go", "Handler.freshCheck", "Fresh"},
		{"internal/api/tfe/freshness.go", "Handler.freshCheck", "Fresh"},

		// The proof of a person, which the setting does not turn off: the
		// first administrator, a passkey change, a local-user reset and a
		// password change with no current password.
		{"internal/api/server/firstadmin.go", "Handler.checkRedeemFreshness", "Fresh"},
		{"internal/api/server/freshness.go", "Handler.checkSignInFreshness", "Fresh"},
		{"internal/api/server/freshness.go", "Handler.requireAlwaysFresh", "checkSignInFreshness"},
		{"internal/api/server/freshness.go", "Handler.freshAlways", "requireAlwaysFresh"},
		{"internal/api/server/passkeys.go", "Handler.passkeyActor", "requireAlwaysFresh"},
		{"internal/api/server/localusers.go", "Handler.changeOwnPassword", "checkSignInFreshness"},

		// A gated check (see gatedFreshChecks) that calls the age check after
		// the guard.
		{"internal/api/server/freshness.go", "Handler.checkTokenFreshness", "checkSignInFreshness"},

		// The callers of a gated check.
		{"pkg/accesstoken/service.go", "Service.Create", "requireFresh"},
		{"pkg/accesstoken/service.go", "Service.Rotate", "requireFresh"},
		{"pkg/profile/service.go", "Service.authorizeReveal", "requireFresh"},
		{"internal/trustrulesvc/service.go", "Service.Create", "requireFresh"},
		{"internal/trustrulesvc/service.go", "Service.Delete", "requireFresh"},
		{"internal/trustrulesvc/service_bundle.go", "Service.requireWriter", "requireFresh"},
		{"internal/api/server/freshness.go", "Handler.fresh", "requireFresh"},
		{"internal/api/server/settings.go", "Handler.requireFreshStep", "requireFresh"},
		{"internal/api/server/tokens.go", "createToken", "requireFresh"},
		{"internal/api/server/tokens.go", "createPrincipalToken", "requireFresh"},
		{"internal/api/server/tokens.go", "rotateToken", "requireFresh"},
		{"internal/api/server/tokens.go", "freshFirst", "requireFresh"},
		{"internal/api/server/tokenpolicy.go", "changeTokenPolicy", "requireFresh"},
		{"internal/api/server/sessionpolicy.go", "Handler.setSessionPolicy", "checkTokenFreshness"},
		{"internal/api/server/providers.go", "Handler.createProvider", "checkTokenFreshness"},
		{"internal/api/server/providers.go", "Handler.changeProvider", "checkTokenFreshness"},
		{"internal/api/server/providers.go", "Handler.deleteSignInProvider", "checkTokenFreshness"},
		{"internal/api/server/githost_app.go", "Handler.registerGitHostApp", "checkTokenFreshness"},
		{"internal/api/server/githost_app.go", "Handler.endGitHostRotation", "checkTokenFreshness"},
		{"internal/api/server/githost_app.go", "Handler.removeGitHostApp", "checkTokenFreshness"},
		{"internal/api/server/githost_app.go", "Handler.assign", "checkTokenFreshness"},
		{"internal/api/server/githost_app.go", "Handler.unassignGitHostInstallation", "checkTokenFreshness"},
	}
}

// gatedFreshChecks lists the freshness checks that ask their Guard (a call
// of Require or freshRequired) before they check the session. The test
// fails when one of them is not in the code, or no longer asks the guard.
func gatedFreshChecks() []freshSite {
	return []freshSite{
		{"pkg/accesstoken/service.go", "Service.requireFresh", ""},
		{"pkg/profile/service.go", "Service.requireFresh", ""},
		{"internal/trustrulesvc/service.go", "requireFresh", ""},
		{"internal/api/server/freshness.go", "requireFresh", ""},
		{"internal/api/server/freshness.go", "Handler.checkTokenFreshness", ""},
	}
}

// skipFreshFile reports whether the file rel needs no check: a file that is
// not Go source, a test, or the package that defines the check.
func skipFreshFile(rel string) bool {
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return true
	}

	return strings.HasPrefix(rel, "pkg/freshstep/") || strings.HasPrefix(rel, "internal/sessionauth/")
}

// funcName returns the name of fn, with the receiver type for a method.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}

	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}

	if idx, ok := t.(*ast.IndexExpr); ok {
		t = idx.X
	}

	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}

	return fn.Name.Name
}

// calleeName returns the name of the function that call calls, and false
// when it is not a freshness check.
func calleeName(call *ast.CallExpr) (string, bool) {
	var name string

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// A bare Fresh is not sessionauth.Fresh.
		name = fun.Name
		if name == "Fresh" {
			return "", false
		}
	case *ast.SelectorExpr:
		name = fun.Sel.Name
		if pkg, ok := fun.X.(*ast.Ident); name == "Fresh" && (!ok || pkg.Name != "sessionauth") {
			return "", false
		}
	default:
		return "", false
	}

	return name, slices.Contains(freshCallees(), name)
}

// asksGuard reports whether body calls Require or freshRequired.
func asksGuard(body *ast.BlockStmt) bool {
	found := false

	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			found = found || asksGuardCall(call)
		}

		return !found
	})

	return found
}

// asksGuardCall reports whether call is a call of Require or freshRequired.
func asksGuardCall(call *ast.CallExpr) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name == "Require" || sel.Sel.Name == "freshRequired"
	}

	id, ok := call.Fun.(*ast.Ident)

	return ok && id.Name == "freshRequired"
}

// freshScan is the result of one walk of the repository.
type freshScan struct {
	// sites are the functions that call a freshness check.
	sites []freshSite
	// gated are the gated checks that ask their guard.
	gated []freshSite
}

// scanFreshFile adds the call sites and the gated checks of one file.
func (s *freshScan) scanFreshFile(rel string, src []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rel, err)
	}

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			s.scanFunc(rel, fn)
		}
	}

	return nil
}

// scanFunc adds the call sites of fn, and fn when it is a gated check that
// asks its guard.
func (s *freshScan) scanFunc(rel string, fn *ast.FuncDecl) {
	name := funcName(fn)

	if slices.Contains(gatedFreshChecks(), freshSite{rel, name, ""}) && asksGuard(fn.Body) {
		s.gated = append(s.gated, freshSite{rel, name, ""})
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		callee, ok := calleeName(call)
		if site := (freshSite{rel, name, callee}); ok && !slices.Contains(s.sites, site) {
			s.sites = append(s.sites, site)
		}

		return true
	})
}

// violations returns a line for each call site that is not in
// freshCallSites, for each site of freshCallSites that is not in the code,
// and for each gated check that does not ask its guard.
func (s *freshScan) violations() []string {
	var found []string

	for _, site := range s.sites {
		if !slices.Contains(freshCallSites(), site) {
			found = append(found, site.String()+": a call site that is not in the list")
		}
	}

	for _, site := range freshCallSites() {
		if !slices.Contains(s.sites, site) {
			found = append(found, site.String()+": a listed call site that is not in the code")
		}
	}

	for _, check := range gatedFreshChecks() {
		if !slices.Contains(s.gated, check) {
			found = append(found, check.file+": "+check.fn+" does not ask its fresh step guard")
		}
	}

	return found
}

// freshCallViolations returns a line for each call site of a freshness check
// that is not in freshCallSites, for each site of freshCallSites that is not
// in the code, and for each gated check that does not ask its guard.
func freshCallViolations(fsys fs.FS) ([]string, error) {
	var scan freshScan

	err := fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if slices.Contains([]string{".git", "node_modules", "clients", "e2e", "load"}, d.Name()) {
				return fs.SkipDir
			}

			return nil
		}

		if skipFreshFile(rel) {
			return nil
		}

		src, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}

		return scan.scanFreshFile(rel, src)
	})
	if err != nil {
		return nil, fmt.Errorf("walk the repository: %w", err)
	}

	return scan.violations(), nil
}

// TestEveryFreshCallSiteUsesPolicy checks that each call of a freshness check
// is a known site in freshCallSites, and that each gated check asks its
// Guard, so that a Guard with a Policy decides whether the step is asked at
// every site that the setting turns off.
func TestEveryFreshCallSiteUsesPolicy(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("open the repository root: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close the repository root: %v", closeErr)
		}
	})

	found, err := freshCallViolations(root.FS())
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range found {
		t.Errorf("%s: ask a freshstep.Guard, or add the site to freshCallSites with a reason", line)
	}
}

var (
	errStore   = errors.New("store is down")
	errRefused = errors.New("needs a step")
)

// TestPolicyRequiredFailsClosed checks that a setting store that returns an
// error makes the policy require a step.
func TestPolicyRequiredFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		read func(context.Context) (bool, error)
		want bool
	}{
		{
			name: "store error gives required",
			read: func(context.Context) (bool, error) { return false, errStore },
			want: true,
		},
		{
			name: "store error with true gives required",
			read: func(context.Context) (bool, error) { return true, errStore },
			want: true,
		},
		{
			name: "setting on gives required",
			read: func(context.Context) (bool, error) { return true, nil },
			want: true,
		},
		{
			name: "setting off gives not required",
			read: func(context.Context) (bool, error) { return false, nil },
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := freshstep.NewPolicy(tt.read)
			if got := p.Required(t.Context()); got != tt.want {
				t.Fatalf("Required() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGuardRequire checks that a Guard runs its check only when the policy
// requires a step, and that it refuses a Guard that has no policy or check.
func TestGuardRequire(t *testing.T) {
	t.Parallel()

	refused := errRefused

	tests := []struct {
		name      string
		read      func(context.Context) (bool, error)
		wantCheck bool
		wantErr   error
	}{
		{
			name:      "setting off skips the check",
			read:      func(context.Context) (bool, error) { return false, nil },
			wantCheck: false,
			wantErr:   nil,
		},
		{
			name:      "setting on runs the check",
			read:      func(context.Context) (bool, error) { return true, nil },
			wantCheck: true,
			wantErr:   refused,
		},
		{
			name:      "store error runs the check",
			read:      func(context.Context) (bool, error) { return false, errStore },
			wantCheck: true,
			wantErr:   refused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			check := func(context.Context, string) error {
				called = true

				return refused
			}

			g, err := freshstep.NewGuard(freshstep.NewPolicy(tt.read), check)
			if err != nil {
				t.Fatalf("NewGuard() error = %v", err)
			}

			err = g.Require(t.Context(), "subject")
			if called != tt.wantCheck {
				t.Fatalf("check called = %v, want %v", called, tt.wantCheck)
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Require() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestPolicyFuncGuardFailsClosed checks that a Guard of a nil PolicyFunc, and
// the zero Guard, run the check or refuse, and that a PolicyFunc that answers
// false skips the check.
func TestPolicyFuncGuardFailsClosed(t *testing.T) {
	t.Parallel()

	var nilFunc freshstep.PolicyFunc

	on := freshstep.PolicyFunc(func(context.Context) bool { return true })
	off := freshstep.PolicyFunc(func(context.Context) bool { return false })

	tests := []struct {
		name    string
		policy  freshstep.Policy
		wantErr error
	}{
		{name: "nil func", policy: nilFunc, wantErr: errRefused},
		{name: "func on", policy: on, wantErr: errRefused},
		{name: "func off", policy: off, wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, err := freshstep.NewGuard(tt.policy, func(context.Context, string) error { return errRefused })
			if err != nil {
				t.Fatalf("NewGuard() error = %v", err)
			}

			if reqErr := g.Require(t.Context(), "subject"); !errors.Is(reqErr, tt.wantErr) {
				t.Fatalf("Require() error = %v, want %v", reqErr, tt.wantErr)
			}
		})
	}

	var zero freshstep.Guard
	if err := zero.Require(t.Context(), "subject"); !errors.Is(err, freshstep.ErrNilCheck) {
		t.Fatalf("zero Guard Require() error = %v, want ErrNilCheck", err)
	}
}

// TestNewGuardRefusesNil checks that NewGuard refuses a nil policy or a nil
// check, so that a Guard always has both.
func TestNewGuardRefusesNil(t *testing.T) {
	t.Parallel()

	check := func(context.Context, string) error { return nil }
	policy := freshstep.NewPolicy(func(context.Context) (bool, error) { return true, nil })

	if _, err := freshstep.NewGuard(nil, check); err == nil {
		t.Fatal("NewGuard(nil policy) error = nil, want an error")
	}

	if _, err := freshstep.NewGuard(policy, nil); err == nil {
		t.Fatal("NewGuard(nil check) error = nil, want an error")
	}
}

// TestGuardIsZero checks that only a Guard of NewGuard is not zero.
func TestGuardIsZero(t *testing.T) {
	t.Parallel()

	if !(freshstep.Guard{}).IsZero() {
		t.Fatal("zero Guard: IsZero = false, want true")
	}

	g, err := freshstep.NewGuard(freshstep.PolicyFunc(nil), func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	if g.IsZero() {
		t.Fatal("Guard of NewGuard: IsZero = true, want false")
	}
}
