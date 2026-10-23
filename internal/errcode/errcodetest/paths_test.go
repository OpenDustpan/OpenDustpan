package errcodetest_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// header is the start of each test source file.
const header = `package demo

import (
	"errors"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/stack"
)

type errorResponse struct {
	Message   string
	Code      int
	ErrorCode string
}
`

// demo returns the adapter of the test sources.
func demo() errcodetest.Adapter {
	return errcodetest.Adapter{Name: "demo", Bodies: []string{"errorResponse"}, Skip: nil}
}

func TestUncodedPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		skip []string
		want []string
	}{
		{
			name: "a case without a code",
			src: `func classify(err error) (int, string) {
	switch {
	case errors.Is(err, stack.ErrNotFound), errors.Is(err, stack.ErrNoState):
		return 404, err.Error()
	default:
		return 500, "internal error"
	}
}`,
			skip: nil,
			want: []string{"classify: default", "classify: stack.ErrNoState", "classify: stack.ErrNotFound"},
		},
		{
			name: "a case with a code",
			src: `func classify(err error) (int, string, string) {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		return 404, err.Error(), errcode.StackNotFound
	case errors.Is(err, stack.ErrDenied):
		return 403, "forbidden", errcode.Forbidden(err)
	default:
		return 500, "internal error", errcode.ServerInternal
	}
}`,
			skip: nil,
			want: nil,
		},
		{
			name: "a case that hands the error to another classify function",
			src: `func classify(err error) (int, string) {
	switch {
	case errors.Is(err, stack.ErrTooLarge):
		return classifySecret(err)
	default:
		return classifyUpdate(err)
	}
}`,
			skip: nil,
			want: nil,
		},
		{
			name: "an if statement with a typed error",
			src: `func classifyUpdate(err error) (int, string) {
	if locked, ok := errors.AsType[*stack.LockedError](err); ok {
		return 409, locked.Error()
	}

	if errors.Is(err, stack.ErrBusy) {
		return 503, "busy", errcode.StackBusy
	}

	var target *stack.Other
	if errors.As(err, &target) {
		return 400, "other"
	}

	return 500, "internal error"
}`,
			skip: nil,
			want: []string{"classifyUpdate: As(&target)", "classifyUpdate: AsType[*stack.LockedError]"},
		},
		{
			name: "a switch on another value is no error path",
			src: `func classify(status int) string {
	switch status {
	case 404:
		return "not found"
	default:
		return ""
	}
}`,
			skip: nil,
			want: nil,
		},
		{
			name: "a type switch",
			src: `func classifyTyped(err error) int {
	switch err.(type) {
	case *stack.LockedError:
		return 409
	default:
		return 500
	}
}`,
			skip: nil,
			want: []string{"classifyTyped: *stack.LockedError", "classifyTyped: default"},
		},
		{
			name: "a skipped function",
			src: `func classifyProtocol(err error) int {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		return 404
	}
	return 500
}`,
			skip: []string{"classifyProtocol"},
			want: nil,
		},
		{
			name: "error bodies outside the classify functions",
			src: `type Handler struct{}

func (h *Handler) writeError(err error) any {
	if code := errcode.Forbidden(err); code != "" {
		return errorResponse{Code: 403, Message: "forbidden", ErrorCode: code}
	}

	if err != nil {
		return &errorResponse{Code: 400, Message: "bad"}
	}

	return errorResponse{Code: 500, Message: "internal error"}
}

func badBody() any {
	return errorResponse{Code: 400, Message: "bad", ErrorCode: errcode.RequestInvalid}
}`,
			skip: nil,
			want: []string{"Handler.writeError: errorResponse{}", "Handler.writeError: errorResponse{} #2"},
		},
		{
			name: "the same sentinel in two cases",
			src: `func classify(err error, write bool) int {
	switch {
	case write && errors.Is(err, stack.ErrNotFound):
		return 409
	case errors.Is(err, stack.ErrNotFound):
		return 404
	}
	return 500
}`,
			skip: nil,
			want: []string{"classify: stack.ErrNotFound", "classify: stack.ErrNotFound #2"},
		},
		{
			name: "a case that is no errors call",
			src: `func classify(err error, write bool) int {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		return 404
	case write:
		return 409
	}
	return 500
}`,
			skip: nil,
			want: []string{"classify: stack.ErrNotFound", "classify: write"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := writeSource(t, header+"\n"+tc.src+"\n")

			a := demo()
			a.Skip = tc.skip

			got, err := errcodetest.UncodedPaths(dir, a)
			if err != nil {
				t.Fatalf("UncodedPaths: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("paths = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUncodedPathsRenamedImport proves that the scanner follows the local
// name of the errcode import.
func TestUncodedPathsRenamedImport(t *testing.T) {
	t.Parallel()

	src := `package demo

import (
	"errors"

	codes "opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/stack"
)

type errorResponse struct{ Code int }

func classify(err error) (int, string) {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		return 404, codes.StackNotFound
	case errors.Is(err, stack.ErrNoState):
		return 404, errcode.StackNoState
	}
	return 500, codes.ServerInternal
}
`

	got, err := errcodetest.UncodedPaths(writeSource(t, src), demo())
	if err != nil {
		t.Fatalf("UncodedPaths: %v", err)
	}

	if want := []string{"classify: stack.ErrNoState"}; !slices.Equal(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
}

// TestUncodedPathsSkipsTests proves that a test file is no error path.
func TestUncodedPathsSkipsTests(t *testing.T) {
	t.Parallel()

	dir := writeSource(t, header)

	test := header + `
func classify(err error) int {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		return 404
	}
	return 500
}
`
	if err := os.WriteFile(filepath.Join(dir, "demo_test.go"), []byte(test), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := errcodetest.UncodedPaths(dir, demo())
	if err != nil {
		t.Fatalf("UncodedPaths: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("paths = %q, want none", got)
	}
}

// TestUncodedPathsNeedsTheBodies fails the scan when a declared body type is
// gone, so that a rename cannot hide the literals of the type.
func TestUncodedPathsNeedsTheBodies(t *testing.T) {
	t.Parallel()

	a := errcodetest.Adapter{Name: "demo", Bodies: []string{"errorResponse", "renamedBody"}, Skip: nil}

	_, err := errcodetest.UncodedPaths(writeSource(t, header), a)
	if !errors.Is(err, errcodetest.ErrBodyMissing) {
		t.Fatalf("error = %v, want %v", err, errcodetest.ErrBodyMissing)
	}
}

// TestUncodedPathsNeedsTheSkippedFunctions fails the scan when a skipped
// function is gone, so that the skip list cannot keep a stale name.
func TestUncodedPathsNeedsTheSkippedFunctions(t *testing.T) {
	t.Parallel()

	a := errcodetest.Adapter{Name: "demo", Bodies: []string{"errorResponse"}, Skip: []string{"classifyGone"}}

	_, err := errcodetest.UncodedPaths(writeSource(t, header), a)
	if !errors.Is(err, errcodetest.ErrSkipMissing) {
		t.Fatalf("error = %v, want %v", err, errcodetest.ErrSkipMissing)
	}
}

func TestUncodedPathsRefusesBadSource(t *testing.T) {
	t.Parallel()

	if _, err := errcodetest.UncodedPaths(writeSource(t, "package demo\nfunc {"), demo()); err == nil {
		t.Fatal("UncodedPaths accepted a file that does not parse")
	}
}

// writeSource writes src as the one source file of a new directory.
func writeSource(t *testing.T, src string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return dir
}
