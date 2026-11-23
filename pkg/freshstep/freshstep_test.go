package freshstep_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/freshstep"
)

// bareFreshCall matches a call of a freshness check that a Policy must gate.
var bareFreshCall = regexp.MustCompile(`(sessionauth\.Fresh|\.RequireFresh)\(`)

// skipFreshFile reports whether the file rel needs no Policy: a file that is
// not Go source, a test, the package that defines the check, and the code of
// the first administrator, which keeps its own step.
func skipFreshFile(rel string) bool {
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return true
	}

	return strings.HasPrefix(rel, "pkg/freshstep/") || strings.HasPrefix(rel, "internal/sessionauth/") ||
		rel == "internal/api/server/firstadmin.go"
}

// freshCallViolations returns the Go files of fsys that call a freshness check
// and do not import this package.
func freshCallViolations(fsys fs.FS) ([]string, error) {
	var found []string

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

		text := string(src)
		if bareFreshCall.MatchString(text) && !strings.Contains(text, `"opendustpan.org/opendustpan/pkg/freshstep"`) {
			found = append(found, rel)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk the repository: %w", err)
	}

	return found, nil
}

// TestEveryFreshCallSiteUsesPolicy checks that each Go file outside this
// package that calls a freshness check also imports this package, so that a
// Policy decides whether the check runs.
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

	for _, rel := range found {
		t.Errorf("%s calls a freshness check and does not import pkg/freshstep", rel)
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
