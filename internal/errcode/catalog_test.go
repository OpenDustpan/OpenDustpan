package errcode_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
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

// shippedDir holds the frozen lists of shipped codes, one file for each
// release, and newDir holds the golden file of each area for the codes that
// no release shipped yet.
const (
	shippedDir = "testdata/shipped"
	newDir     = "testdata/catalog"
)

// shippedHashes pins the SHA-256 of each file of shippedDir. A shipped file
// never changes: a release adds a new file and its hash here.
func shippedHashes() map[string]string {
	return map[string]string{
		"main.txt": "622e08d9521289c808594cb96246814d3d9cb6d78ebea96ab3994451259d5dfb",
	}
}

// TestShippedFilesAreFrozen fails for a file of shippedDir whose SHA-256 is
// not its pinned hash, for a file without a pinned hash, and for a pinned
// hash without a file. So a test cannot change a shipped line and the catalog
// together.
func TestShippedFilesAreFrozen(t *testing.T) {
	t.Parallel()

	files := readDir(t, shippedDir)
	pins := shippedHashes()

	for name, data := range files {
		want, ok := pins[name]
		if !ok {
			t.Errorf("%s/%s has no pinned hash: add it to shippedHashes", shippedDir, name)

			continue
		}

		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s/%s hash = %s, want %s: a shipped file never changes", shippedDir, name, got, want)
		}
	}

	for name := range pins {
		if _, ok := files[name]; !ok {
			t.Errorf("the pinned file %s/%s is gone: a shipped file is never removed", shippedDir, name)
		}
	}
}

// TestShippedCodesStay fails when a code of a shipped file is not in the
// catalog, and when the catalog does not give the code each argument of its
// line with the same kind. A code may gain arguments, so the catalog may give
// it more arguments than its line.
func TestShippedCodesStay(t *testing.T) {
	t.Parallel()

	shipped := shippedLines(t)
	if len(shipped) == 0 {
		t.Fatalf("%s holds no code", shippedDir)
	}

	for code, line := range shipped {
		spec, ok := errcode.Lookup(code)
		if !ok {
			t.Errorf("shipped code %q is not in the catalog: a code is never removed or renamed", code)

			continue
		}

		if lost := lostArgs(line, goldenLine(spec)); len(lost) > 0 {
			t.Errorf("shipped code %q lost the arguments %q: an argument is never removed and never changes its kind",
				code, lost)
		}
	}
}

// lostArgs returns the arguments of the golden line want that the golden line
// got does not have with the same kind.
func lostArgs(want, got string) []string {
	have := strings.Fields(got)[1:]

	var lost []string

	for _, arg := range strings.Fields(want)[1:] {
		if !slices.Contains(have, arg) {
			lost = append(lost, arg)
		}
	}

	return lost
}

func TestLostArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, want, got string
		lost            []string
	}{
		{"same", "a.b x:identifier", "a.b x:identifier", nil},
		{"no arguments", "a.b", "a.b", nil},
		{"gained an argument", "a.b x:identifier", "a.b x:identifier y:quantity", nil},
		{"gained the first argument", "a.b", "a.b y:quantity", nil},
		{"other order", "a.b x:identifier y:time", "a.b y:time x:identifier", nil},
		{"removed", "a.b x:identifier", "a.b", []string{"x:identifier"}},
		{"renamed", "a.b x:identifier", "a.b z:identifier", []string{"x:identifier"}},
		{"other kind", "a.b x:identifier", "a.b x:quantity", []string{"x:identifier"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := lostArgs(tc.want, tc.got); !slices.Equal(got, tc.lost) {
				t.Fatalf("lostArgs(%q, %q) = %q, want %q", tc.want, tc.got, got, tc.lost)
			}
		})
	}
}

// TestNewCodesHaveTheirLine fails for a code of the catalog that no shipped
// file holds and that has no line, or another line, in the golden file of its
// area, for a line of a golden file that is not in the catalog, and for a line
// in the golden file of another area. Each area has its own file, so that two
// changes to two areas touch different files.
func TestNewCodesHaveTheirLine(t *testing.T) {
	t.Parallel()

	shipped := shippedLines(t)
	golden := newLines(t)

	for _, spec := range errcode.Catalog() {
		_, isShipped := shipped[spec.Code]
		line, isNew := golden[spec.Code]

		switch {
		case isShipped && isNew:
			t.Errorf("code %q is shipped: remove its line from %s", spec.Code, areaFile(spec.Code))
		case isShipped:
		case !isNew:
			t.Errorf("code %q has no line in %s: add the line %q", spec.Code, areaFile(spec.Code), goldenLine(spec))
		case line != goldenLine(spec):
			t.Errorf(
				"code %q is %q in the catalog and %q in %s",
				spec.Code,
				goldenLine(spec),
				line,
				areaFile(spec.Code),
			)
		default:
		}
	}

	for code := range golden {
		if _, ok := errcode.Lookup(code); !ok {
			t.Errorf("code %q of %s is not in the catalog", code, areaFile(code))
		}
	}
}

// TestEveryAreaHasAGoldenFile fails for a file catalog_<area>.go without the
// golden file of its area, and for a golden file without the list of its
// area. An area name drops its hyphens in the name of its Go file.
func TestEveryAreaHasAGoldenFile(t *testing.T) {
	t.Parallel()

	sources, err := filepath.Glob("catalog_*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	lists := map[string]bool{}

	for _, source := range sources {
		if !strings.HasSuffix(source, "_test.go") {
			lists[strings.TrimSuffix(strings.TrimPrefix(source, "catalog_"), ".go")] = true
		}
	}

	for name := range readDir(t, newDir) {
		area := strings.TrimSuffix(name, ".txt")

		if !lists[strings.ReplaceAll(area, "-", "")] {
			t.Errorf("%s/%s has no file catalog_%s.go", newDir, name, strings.ReplaceAll(area, "-", ""))
		}

		delete(lists, strings.ReplaceAll(area, "-", ""))
	}

	for list := range lists {
		t.Errorf("catalog_%s.go has no golden file in %s: add it, empty", list, newDir)
	}
}

// areaFile returns the golden file of the area of code.
func areaFile(code string) string {
	area, _, _ := strings.Cut(code, ".")

	return newDir + "/" + area + ".txt"
}

// shippedLines returns the lines of every shipped file by their code.
func shippedLines(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	for name, data := range readDir(t, shippedDir) {
		addLines(t, shippedDir+"/"+name, data, "", out)
	}

	return out
}

// newLines returns the lines of the golden file of each area by their code.
func newLines(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	for name, data := range readDir(t, newDir) {
		addLines(t, newDir+"/"+name, data, strings.TrimSuffix(name, ".txt"), out)
	}

	return out
}

// addLines adds the lines of data, the file name, to out by their code. A
// line that starts with "#" is a comment. When area is not "", each code of
// the file must be of that area. A code may have one line only.
func addLines(t *testing.T, name string, data []byte, area string, out map[string]string) {
	t.Helper()

	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		code, _, _ := strings.Cut(line, " ")
		if lineArea, _, _ := strings.Cut(code, "."); area != "" && lineArea != area {
			t.Errorf("%s: code %q belongs in %s", name, code, areaFile(code))
		}

		if _, seen := out[code]; seen {
			t.Errorf("%s: code %q has two lines", name, code)
		}

		out[code] = line
	}
}

// readDir returns the content of each .txt file of dir by its name.
func readDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close %s: %v", dir, closeErr)
		}
	}()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	out := map[string][]byte{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}

		data, err := root.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s/%s: %v", dir, e.Name(), err)
		}

		out[e.Name()] = data
	}

	return out
}

// goldenLine returns the line of spec in a golden file: the code, then the
// name and the kind of each argument.
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
