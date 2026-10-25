package errcodetest_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// header is the start of each test source file.
const header = `package demo

import (
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/stack"
)

type errorResponse struct {
	Message   string
	Code      int
	ErrorCode string
}
`

// demo returns the adapter of the test sources in dir.
func demo(dir string) errcodetest.Adapter {
	return errcodetest.Adapter{Name: "demo", Dir: dir, Bodies: []string{"errorResponse"}, Skip: nil}
}

// coded are the names of the errcode package that give a path of the test
// sources a code.
func coded() []string {
	return []string{
		"Forbidden",
		"New",
		"RequestInvalid",
		"ServerInternal",
		"StackBusy",
		"StackNoState",
		"StackNotFound",
	}
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
			name: "a case that only logs, with a status in its condition",
			src: `func write(w http.ResponseWriter, err error, p errorResponse) {
	switch {
	case errors.Is(err, stack.ErrFenced):
		log.Warn("fenced")
	case p.Message != err.Error() && p.Code < http.StatusInternalServerError:
		log.Debug("refused")
	default:
	}
}`,
			skip: nil,
			want: nil,
		},
		{
			name: "a case with a status in its condition that writes an answer without a code",
			src: `func write(w http.ResponseWriter, err error, p errorResponse) {
	switch {
	case errors.Is(err, stack.ErrFenced):
		log.Warn("fenced")
	case p.Message != err.Error() && p.Code < http.StatusInternalServerError:
		w.WriteHeader(http.StatusBadRequest)
	default:
	}
}`,
			skip: nil,
			want: []string{"write: p.Message != err.Error() && p.Code < http.StatusInternalServerError"},
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
			name: "a body that copies the code of an answer",
			src: `func write(w http.ResponseWriter, status int, answer errcode.Answer) {
	_ = errorResponse{Code: status, Message: "x", ErrorCode: answer.Code()}
}

func writeUncoded(w http.ResponseWriter, code func() string) {
	_ = errorResponse{Code: 500, Message: "x", ErrorCode: code()}
}

func writeOther(w http.ResponseWriter, other interface{ Name() string }) {
	_ = errorResponse{Code: 500, Message: "x", ErrorCode: other.Name()}
}`,
			skip: nil,
			want: []string{"writeOther: errorResponse{}", "writeUncoded: errorResponse{}"},
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
		{
			name: "a name of the errcode package that is no code",
			src: `func classify(err error) (int, string, bool) {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		return 404, err.Error(), errcode.Valid("x")
	default:
		return 500, "internal error", errcode.KindIdentifier == 0
	}
}`,
			skip: nil,
			want: []string{"classify: default", "classify: stack.ErrNotFound"},
		},
		{
			name: "the else branch of a coded if statement has no code",
			src: `type Handler struct{}

func (h *Handler) writeError(err error) any {
	if answer, err := errcode.New(errcode.StackNotFound); err == nil {
		return errorResponse{Code: 404, Message: "not found", ErrorCode: answer.Code()}
	} else {
		return errorResponse{Code: 500, Message: "internal error"}
	}
}`,
			skip: nil,
			want: []string{"Handler.writeError: errorResponse{}"},
		},
		{
			name: "a mapping function with another name",
			src: `func preferencesError(err error) (int, string) {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, stack.ErrBusy):
		return http.StatusServiceUnavailable, errcode.StackBusy
	}

	if errors.Is(err, stack.ErrNoState) {
		return http.StatusConflict, "no state"
	}

	if errors.Is(err, stack.ErrTooLarge) {
		return 0, ""
	}

	return http.StatusInternalServerError, "internal error"
}`,
			skip: nil,
			want: []string{"preferencesError: stack.ErrNoState", "preferencesError: stack.ErrNotFound"},
		},
		{
			name: "a function that tests an error and names no error status",
			src: `func readAll(err error) int {
	if errors.Is(err, stack.ErrNotFound) {
		return http.StatusOK
	}
	return http.StatusNoContent
}`,
			skip: nil,
			want: nil,
		},
		{
			name: "a table of errors and statuses",
			src: `var problems = []struct {
	err    error
	code   string
	status int
}{
	{stack.ErrNotFound, "stack.not-found", http.StatusNotFound},
	{stack.ErrBusy, errcode.StackBusy, http.StatusServiceUnavailable},
	{err: stack.ErrNoState, code: "stack.no-state", status: http.StatusConflict},
}

var routes = []struct {
	method string
	status int
}{
	{http.MethodGet, http.StatusNotFound},
}`,
			skip: nil,
			want: []string{"problems: stack.ErrNoState", "problems: stack.ErrNotFound"},
		},
		{
			name: "an error body in the value of a variable",
			src: `var (
	errBusy  = &errorResponse{Code: 503, Message: "busy"}
	errCoded = &errorResponse{Code: 404, Message: "not found", ErrorCode: errcode.StackNotFound}
)`,
			skip: nil,
			want: []string{"errBusy: errorResponse{}"},
		},
		{
			name: "a raw JSON error body and a plain text error",
			src: `func notFound(w http.ResponseWriter) {
	_, _ = w.Write([]byte(` + "`" + `{"code":404,"message":"not found"}` + "`" + `))
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	http.NotFound(w, nil)
}

func tfeError() string {
	return "{\"errors\":[]}"
}`,
			skip: nil,
			want: []string{
				"methodNotAllowed: http.Error", "methodNotAllowed: http.NotFound",
				"notFound: raw JSON", "tfeError: raw JSON",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := writeSource(t, header+"\n"+tc.src+"\n")

			a := demo(dir)
			a.Skip = tc.skip

			got, err := errcodetest.UncodedPaths(a, coded())
			if err != nil {
				t.Fatalf("UncodedPaths: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("paths = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUncodedPathsAnswerCode proves that a call of Code counts as a code only
// as the code member of a body, on an errcode.Answer.
func TestUncodedPathsAnswerCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		skip []string
		want []string
	}{
		{
			name: "a body that copies the code of an answer in a field of an answer",
			src: `type holder struct{ Answer errcode.Answer }

func writeField(w http.ResponseWriter, ie holder) {
	_ = errorResponse{Code: 409, Message: "x", ErrorCode: ie.Answer.Code()}
}

func writeVar(w http.ResponseWriter) {
	var a errcode.Answer
	_ = errorResponse{Code: 409, Message: "x", ErrorCode: a.Code()}
}`,
			skip: nil,
			want: nil,
		},
		{
			name: "a code call in a case clause that is no member of a body",
			src: `type other struct{}

func (other) Code() string { return "" }

func classify(err error, r other) (int, string) {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		_ = r.Code()
		return 404, "stack.whatever"
	default:
		return 500, "internal error"
	}
}

func writeCase(err error, r other) any {
	switch {
	case errors.Is(err, stack.ErrNotFound):
		_ = r.Code()
		return errorResponse{Code: 404, Message: "x", ErrorCode: "stack.whatever"}
	}
	return nil
}`,
			skip: nil,
			want: []string{"classify: default", "classify: stack.ErrNotFound", "writeCase: errorResponse{}"},
		},
		{
			name: "a code call of a type that is no answer in a body",
			src: `type other struct{}

func (other) Code() string { return "" }

func writeOther(err error, r other) any {
	if errors.Is(err, stack.ErrNotFound) {
		return errorResponse{Code: 404, Message: "x", ErrorCode: r.Code()}
	}
	return nil
}`,
			skip: nil,
			want: []string{"writeOther: errorResponse{}"},
		},
		{
			name: "a code call in an if condition with an uncoded fallback",
			src: `func writeGuard(w http.ResponseWriter, a errcode.Answer) any {
	if a.Code() == "" {
		return errorResponse{Code: 500, Message: "x"}
	}
	return errorResponse{Code: 409, Message: "x", ErrorCode: a.Code()}
}`,
			skip: nil,
			want: []string{"writeGuard: errorResponse{}"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := writeSource(t, header+"\n"+tc.src+"\n")

			a := demo(dir)
			a.Skip = tc.skip

			got, err := errcodetest.UncodedPaths(a, coded())
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

	got, err := errcodetest.UncodedPaths(demo(writeSource(t, src)), coded())
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

	got, err := errcodetest.UncodedPaths(demo(dir), coded())
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

	a := demo(writeSource(t, header))
	a.Bodies = []string{"errorResponse", "renamedBody"}

	_, err := errcodetest.UncodedPaths(a, coded())
	if !errors.Is(err, errcodetest.ErrBodyMissing) {
		t.Fatalf("error = %v, want %v", err, errcodetest.ErrBodyMissing)
	}
}

// TestUncodedPathsNeedsTheSkippedFunctions fails the scan when a skipped
// function is gone, so that the skip list cannot keep a stale name.
func TestUncodedPathsNeedsTheSkippedFunctions(t *testing.T) {
	t.Parallel()

	a := demo(writeSource(t, header))
	a.Skip = []string{"classifyGone"}

	_, err := errcodetest.UncodedPaths(a, coded())
	if !errors.Is(err, errcodetest.ErrSkipMissing) {
		t.Fatalf("error = %v, want %v", err, errcodetest.ErrSkipMissing)
	}
}

func TestUncodedPathsRefusesBadSource(t *testing.T) {
	t.Parallel()

	if _, err := errcodetest.UncodedPaths(demo(writeSource(t, "package demo\nfunc {")), coded()); err == nil {
		t.Fatal("UncodedPaths accepted a file that does not parse")
	}
}

// TestCodedNames proves that only New, Forbidden and the constants that hold
// a code of the catalog give a path a code.
func TestCodedNames(t *testing.T) {
	t.Parallel()

	src := `package errcode

const (
	AccessDenied = "access.denied"
	NotACode     = "stack.no-such-code"
	MaxArgs      = 8
)

const ServerInternal = "server.internal"

func Valid(string) bool { return true }
`

	got, err := errcodetest.CodedNames(writeSource(t, src))
	if err != nil {
		t.Fatalf("CodedNames: %v", err)
	}

	if want := []string{"AccessDenied", "Forbidden", "New", "ServerInternal"}; !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
}

// TestCodedNamesOfTheCatalog proves that every code of the catalog has a
// constant that gives a path a code.
func TestCodedNamesOfTheCatalog(t *testing.T) {
	t.Parallel()

	names, err := errcodetest.CodedNames("..")
	if err != nil {
		t.Fatalf("CodedNames: %v", err)
	}

	// New and Forbidden, and one constant for each code.
	if got, want := len(names), len(errcode.Catalog())+2; got != want {
		t.Fatalf("%d coded names, want %d: %q", got, want, names)
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

// TestUncodedPathsLiteralShapes proves that an anonymous struct and a map
// literal are error bodies when they have a member or a key of an error body.
func TestUncodedPathsLiteralShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		skip []string
		want []string
	}{
		{
			name: "an anonymous struct body",
			src: `func writeAnon() any {
	return struct {
		Message string ` + "`json:\"message\"`" + `
		Code    int    ` + "`json:\"errorCode\"`" + `
	}{Message: "bad", Code: 400}
}`,
			skip: nil,
			want: []string{"writeAnon: anonymous struct"},
		},
		{
			name: "a map body",
			src: `func writeMap() any {
	return map[string]any{"message": "bad", "errorCode": 400}
}`,
			skip: nil,
			want: []string{"writeMap: map literal"},
		},
		{
			name: "a map body and an anonymous struct with a code",
			src: `func writeCoded() any {
	_ = struct {
		Code string ` + "`json:\"errorCode\"`" + `
	}{Code: errcode.RequestInvalid}

	return map[string]any{"errors": errcode.RequestInvalid}
}`,
			skip: nil,
			want: nil,
		},
		{
			name: "a map and a struct with other members",
			src: `func other() any {
	_ = struct {
		Name string ` + "`json:\"name\"`" + `
	}{Name: "x"}

	return map[string]any{"name": "x"}
}`,
			skip: nil,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := errcodetest.UncodedPaths(demo(writeSource(t, header+"\n"+tc.src+"\n")), coded())
			if err != nil {
				t.Fatalf("UncodedPaths: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("paths = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBodyTypes proves that BodyTypes finds each struct type with a field
// whose JSON name is code, errorCode or errors, also in a nested or an
// anonymous struct, and no other type.
func TestBodyTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "code",
			src:  "type a struct {\n\tCode int `json:\"code\"`\n}",
			want: []string{"a"},
		},
		{
			name: "errorCode with options",
			src:  "type a struct {\n\tErrorCode string `json:\"errorCode,omitempty\"`\n}",
			want: []string{"a"},
		},
		{
			name: "errors",
			src:  "type registryErrors struct {\n\tErrors []string `json:\"errors\"`\n}",
			want: []string{"registryErrors"},
		},
		{
			name: "other tags too",
			src:  "type a struct {\n\tCode int `yaml:\"c\" json:\"code\"`\n}",
			want: []string{"a"},
		},
		{
			name: "nested struct",
			src:  "type a struct {\n\tError struct {\n\t\tCode int `json:\"code\"`\n\t} `json:\"error\"`\n}",
			want: []string{"a"},
		},
		{
			name: "type group",
			src:  "type (\n\ta struct{ X int }\n\tb struct {\n\t\tCode int `json:\"code\"`\n\t}\n)",
			want: []string{"b"},
		},
		{
			name: "sorted",
			src: "type z struct {\n\tCode int `json:\"code\"`\n}\n" +
				"type m struct {\n\tErrors []int `json:\"errors\"`\n}",
			want: []string{"m", "z"},
		},
		{
			name: "other JSON names",
			src: "type a struct {\n\tCode int `json:\"statusCode\"`\n\tB int `json:\"codes\"`\n" +
				"\tC int `json:\"-\"`\n\tD int\n\tE int `yaml:\"code\"`\n}",
			want: nil,
		},
		{
			name: "field named Code without a tag",
			src:  "type a struct{ Code int }",
			want: nil,
		},
		{
			name: "type declared in a function",
			src:  "func f() {\n\ttype local struct {\n\t\tCode int `json:\"code\"`\n\t}\n}",
			want: []string{"local"},
		},
		{
			name: "not a struct",
			src:  "type a int",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := errcodetest.BodyTypes(writeSource(t, "package demo\n\n"+tc.src+"\n"))
			if err != nil {
				t.Fatalf("BodyTypes: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("types = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBodyTypesSkipsTests proves that a type of a test file is no body type.
func TestBodyTypesSkipsTests(t *testing.T) {
	t.Parallel()

	dir := writeSource(t, "package demo\n")

	test := "package demo\n\ntype a struct {\n\tCode int `json:\"code\"`\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "demo_test.go"), []byte(test), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := errcodetest.BodyTypes(dir)
	if err != nil {
		t.Fatalf("BodyTypes: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("types = %q, want none", got)
	}
}

func TestBodyTypesRefusesBadSource(t *testing.T) {
	t.Parallel()

	if _, err := errcodetest.BodyTypes(writeSource(t, "package demo\ntype {")); err == nil {
		t.Fatal("BodyTypes accepted a file that does not parse")
	}
}

// TestMissingBodies proves that a body type that is neither in the Bodies of
// the adapter nor ignored is missing, so that its literals cannot hide from
// UncodedPaths.
func TestMissingBodies(t *testing.T) {
	t.Parallel()

	src := "package demo\n\n" +
		"type errorResponse struct {\n\tCode int `json:\"code\"`\n}\n" +
		"type registryErrors struct {\n\tErrors []string `json:\"errors\"`\n}\n" +
		"type devCodeRequest struct {\n\tCode string `json:\"code\"`\n}\n" +
		"type statusError struct{ status int }\n"

	tests := []struct {
		name    string
		bodies  []string
		ignored []string
		want    []string
	}{
		{"all gated or ignored", []string{"errorResponse", "registryErrors"}, []string{"devCodeRequest"}, nil},
		{"a new body type", []string{"errorResponse"}, []string{"devCodeRequest"}, []string{"registryErrors"}},
		{"nothing ignored", []string{"errorResponse"}, nil, []string{"devCodeRequest", "registryErrors"}},
		{
			"a body without a JSON tag",
			[]string{"errorResponse", "registryErrors", "statusError"},
			[]string{"devCodeRequest"},
			nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := demo(writeSource(t, src))
			a.Bodies = tc.bodies

			got, err := errcodetest.MissingBodies(a, tc.ignored)
			if err != nil {
				t.Fatalf("MissingBodies: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("missing = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMissingBodiesNeedsTheIgnoredTypes fails when an ignored type is gone or
// is no body type, so that the ignore list cannot keep a stale name.
func TestMissingBodiesNeedsTheIgnoredTypes(t *testing.T) {
	t.Parallel()

	src := "package demo\n\ntype errorResponse struct {\n\tCode int `json:\"code\"`\n}\ntype other struct{ X int }\n"

	for _, ignored := range []string{"renamedRequest", "other", "errorResponse"} {
		t.Run(ignored, func(t *testing.T) {
			t.Parallel()

			_, err := errcodetest.MissingBodies(demo(writeSource(t, src)), []string{ignored})
			if !errors.Is(err, errcodetest.ErrIgnoreStale) {
				t.Fatalf("error = %v, want %v", err, errcodetest.ErrIgnoreStale)
			}
		})
	}
}
