package errcode_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// camelCase is the form of an argument name.
var camelCase = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// TestCatalogIsWellFormed checks each entry of the catalog: a valid code, once
// only, with a meaning, and with arguments that a client can read.
func TestCatalogIsWellFormed(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, spec := range errcode.Catalog() {
		if !errcode.Valid(spec.Code) {
			t.Errorf("code %q does not match the grammar", spec.Code)
		}

		if seen[spec.Code] {
			t.Errorf("code %q is in the catalog two times", spec.Code)
		}

		seen[spec.Code] = true

		if spec.Meaning == "" {
			t.Errorf("code %q has no meaning", spec.Code)
		}

		checkArgs(t, spec)
	}
}

func checkArgs(t *testing.T, spec errcode.Spec) {
	t.Helper()

	if len(spec.Args) > errcode.MaxArgs {
		t.Errorf("code %q has %d arguments, the limit is %d", spec.Code, len(spec.Args), errcode.MaxArgs)
	}

	names := map[string]bool{}

	for _, arg := range spec.Args {
		if !camelCase.MatchString(arg.Name) {
			t.Errorf("code %q: argument %q is not camelCase", spec.Code, arg.Name)
		}

		if names[arg.Name] {
			t.Errorf("code %q: argument %q is declared two times", spec.Code, arg.Name)
		}

		names[arg.Name] = true

		if !slices.Contains([]errcode.Kind{errcode.KindIdentifier, errcode.KindQuantity, errcode.KindTime}, arg.Kind) {
			t.Errorf("code %q: argument %q has the kind %d", spec.Code, arg.Name, arg.Kind)
		}
	}
}

// TestCatalogIsSorted checks that the catalog comes in the order of its
// codes.
func TestCatalogIsSorted(t *testing.T) {
	t.Parallel()

	specs := errcode.Catalog()

	codes := make([]string, 0, len(specs))
	for _, spec := range specs {
		codes = append(codes, spec.Code)
	}

	if !slices.IsSorted(codes) {
		t.Fatalf("the catalog is not in the order of its codes: %v", codes)
	}
}

// TestExistingCodesKeepTheirText pins the codes that the adapters answered
// before the catalog. A code never changes, so this list only grows.
func TestExistingCodesKeepTheirText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		got  string
		want string
	}{
		{errcode.AccessDenied, "access.denied"},
		{errcode.AccessIPNotAllowed, "access.ip-not-allowed"},
		{errcode.GateLimited, "gate.limited"},
		{errcode.GateRuleFailed, "gate.rule-failed"},
		{errcode.SessionCSRF, "session.csrf"},
		{errcode.SessionOtherSite, "session.other-site"},
		{errcode.ServerInternal, "server.internal"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			if tc.got != tc.want {
				t.Fatalf("code = %q, want %q", tc.got, tc.want)
			}

			if _, ok := errcode.Lookup(tc.want); !ok {
				t.Fatalf("code %q is not in the catalog", tc.want)
			}
		})
	}
}

// TestForbiddenAnswersCatalogCodes proves that each code of a 403 is in the
// catalog.
func TestForbiddenAnswersCatalogCodes(t *testing.T) {
	t.Parallel()

	for _, code := range []string{
		errcode.AccessDenied, errcode.AccessIPNotAllowed, errcode.GateLimited,
		errcode.GateRuleFailed, errcode.SessionCSRF, errcode.SessionOtherSite,
		errcode.SignInRefused, errcode.AccessNoAction,
	} {
		spec, ok := errcode.Lookup(code)
		if !ok {
			t.Fatalf("code %q is not in the catalog", code)
		}

		if len(spec.Args) != 0 {
			t.Fatalf("code %q has arguments %v; the 403 answers have none", code, spec.Args)
		}
	}
}

func TestValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		want bool
	}{
		{"stack.not-found", true},
		{"update.conflict", true},
		{"request.idempotency-key-reused", true},
		{"a.b", true},
		{"backup.corrupt.v2", true},
		{"stack", false},
		{"", false},
		{"Stack.not-found", false},
		{"stack.not_found", false},
		{"stack..found", false},
		{"stack.-found", false},
		{"stack.found-", false},
		{"stack.not--found", false},
		{"1stack.found", false},
		{"stack.not found", false},
		{"a.b" + longSegment(61), true},
		{"a.b" + longSegment(62), false},
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			if got := errcode.Valid(tc.code); got != tc.want {
				t.Fatalf("Valid(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// longSegment returns n letters.
func longSegment(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}

	return string(b)
}

func TestLookupUnknown(t *testing.T) {
	t.Parallel()

	if _, ok := errcode.Lookup("stack.unknown-code"); ok {
		t.Fatal("Lookup found a code that is not in the catalog")
	}
}

// goldenFile holds one line for each code of the catalog: the code, then the
// name and the kind of each argument. At the first tag, it becomes the first
// list of shipped codes.
const goldenFile = "testdata/catalog.txt"

// TestCatalogKeepsEveryCode fails when a line of the golden file is not in the
// catalog, when the catalog gives a code other arguments than its line, and
// when a code of the catalog has no line. The console reads these codes, so a
// code never goes away and never changes: a new code adds its line.
func TestCatalogKeepsEveryCode(t *testing.T) {
	t.Parallel()

	golden := readGolden(t)

	catalog := map[string]string{}
	for _, spec := range errcode.Catalog() {
		catalog[spec.Code] = goldenLine(spec)
	}

	for code, want := range golden {
		got, ok := catalog[code]

		switch {
		case !ok:
			t.Errorf("code %q of %s is not in the catalog: a code is never removed or renamed", code, goldenFile)
		case got != want:
			t.Errorf("code %q is %q in the catalog and %q in %s: the arguments of a code never change",
				code, got, want, goldenFile)
		default:
		}
	}

	for code, line := range catalog {
		if _, ok := golden[code]; !ok {
			t.Errorf("code %q has no line in %s: add the line %q", code, goldenFile, line)
		}
	}
}

// readGolden returns the lines of the golden file by their code.
func readGolden(t *testing.T) map[string]string {
	t.Helper()

	data, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("read %s: %v", goldenFile, err)
	}

	golden := map[string]string{}

	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		code, _, _ := strings.Cut(line, " ")
		if _, seen := golden[code]; seen {
			t.Errorf("%s: code %q has two lines", goldenFile, code)
		}

		golden[code] = line
	}

	return golden
}

// goldenLine returns the line of spec in the golden file.
func goldenLine(spec errcode.Spec) string {
	parts := make([]string, 0, 1+len(spec.Args))
	parts = append(parts, spec.Code)

	for _, arg := range spec.Args {
		parts = append(parts, arg.Name+":"+kindName(arg.Kind))
	}

	return strings.Join(parts, " ")
}

// kindName returns the name of a kind in the golden file.
func kindName(k errcode.Kind) string {
	switch k {
	case errcode.KindIdentifier:
		return "identifier"
	case errcode.KindQuantity:
		return "quantity"
	case errcode.KindTime:
		return "time"
	default:
		return "unknown"
	}
}
