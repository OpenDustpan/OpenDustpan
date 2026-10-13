package workspace

import (
	"encoding/json/jsontext"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scan builds pointers with splitPointer and runs scanPointers over the
// attributes JSON.
func scan(t *testing.T, attributesJSON string, literals ...string) (pointerScan, error) {
	t.Helper()

	pointers := make([]revealPointer, 0, len(literals))
	for _, literal := range literals {
		pointers = append(pointers, revealPointer{literal: literal, tokens: splitPointer(literal)})
	}

	return scanPointers(jsontext.NewDecoder(strings.NewReader(attributesJSON)), pointers)
}

// pointerCase is one streamed layout the table walk checks.
type pointerCase struct {
	name          string
	attributes    string
	literals      []string
	wantApplied   []string
	wantRoutes    [][]string
	wantSecrets   []bool
	wantEntangled bool
}

// TestScanPointers proves the pointer walk resolves pointers on the stream,
// reports entangled layouts, and keeps secret digests in pointer order.
func TestScanPointers(t *testing.T) {
	t.Parallel()

	cases := []pointerCase{
		{
			name:          "disjoint",
			attributes:    `{"a":"s","b":{"c":[1,"x"]},"n":7}`,
			literals:      []string{"/a", "/b/c/1", "/n"},
			wantApplied:   []string{"/a", "/b/c/1", "/n"},
			wantRoutes:    [][]string{{"a"}, {"b", "c", "1"}, {"n"}},
			wantSecrets:   []bool{true, true, false},
			wantEntangled: false,
		},
		{
			name:          "containers",
			attributes:    `{"o":{"k":1},"e":{},"l":[],"z":"","t":true,"u":null}`,
			literals:      []string{"/o", "/e", "/l", "/z", "/t", "/u"},
			wantApplied:   []string{"/o", "/e", "/l", "/z", "/t", "/u"},
			wantRoutes:    [][]string{{"o"}, {"e"}, {"l"}, {"z"}, {"t"}, {"u"}},
			wantSecrets:   []bool{true, false, false, false, false, false},
			wantEntangled: false,
		},
		{
			name:          "unresolved",
			attributes:    `{"a":"s","b":{"c":[1,"x"]},"n":7,"u":null}`,
			literals:      []string{"/missing", "/b/x", "/b/c/5", "/a/x", "/u/x", "/n/0"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "array index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/1"},
			wantApplied:   []string{"/l/1"},
			wantRoutes:    [][]string{{"l", "1"}},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "array leading zero",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/01"},
			wantApplied:   []string{"/l/01"},
			wantRoutes:    [][]string{{"l", "1"}},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "array duplicate index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/1", "/l/01"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "array plus index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/+1"},
			wantApplied:   []string{"/l/+1"},
			wantRoutes:    [][]string{{"l", "1"}},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "array negative index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/-1"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "array text index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/x"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "ancestor first",
			attributes:    `{"a":{"b":"s"}}`,
			literals:      []string{"/a", "/a/b"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "descendant first",
			attributes:    `{"a":{"b":"s"}}`,
			literals:      []string{"/a/b", "/a"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "missing descendant",
			attributes:    `{"a":{"b":"s"}}`,
			literals:      []string{"/a", "/a/zz"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "equal tokens",
			attributes:    `{"~":"s"}`,
			literals:      []string{"/~", "/~0"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "wrapper target",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w"},
			wantApplied:   []string{"/w"},
			wantRoutes:    [][]string{{"w"}},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "wrapper step",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w/k"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "wrapper value step",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w/value/k"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "wrapper type step",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w/type"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "value without type",
			attributes:    `{"o":{"value":1,"x":2}}`,
			literals:      []string{"/o/k"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "quirk",
			attributes:    `{"p":{"a":"v"}}`,
			literals:      []string{"/p", "/p/secret"},
			wantApplied:   nil,
			wantRoutes:    nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "escaped keys",
			attributes:    `{"a/b":"s1","c~d":"s2"}`,
			literals:      []string{"/a~1b", "/c~0d"},
			wantApplied:   []string{"/a~1b", "/c~0d"},
			wantRoutes:    [][]string{{"a/b"}, {"c~d"}},
			wantSecrets:   []bool{true, true},
			wantEntangled: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := scan(t, tc.attributes, tc.literals...)
			if err != nil {
				t.Fatalf("scan pointers: %v", err)
			}

			checkPointerScan(t, got, tc)
		})
	}
}

// checkPointerScan proves one scan holds the applied literals in order with
// the secret flags, or the entangled flag with no applied pointer.
func checkPointerScan(t *testing.T, got pointerScan, tc pointerCase) {
	t.Helper()

	if got.entangled != tc.wantEntangled {
		t.Fatalf("entangled = %v, want %v", got.entangled, tc.wantEntangled)
	}

	if tc.wantEntangled {
		checkNoApplied(t, got)

		return
	}

	checkAppliedCount(t, got, tc)
	checkAppliedPointers(t, got, tc)
}

// checkNoApplied proves an entangled scan holds no applied pointer.
func checkNoApplied(t *testing.T, got pointerScan) {
	t.Helper()

	if len(got.applied) != 0 {
		t.Fatalf("applied = %d pointers, want none when entangled", len(got.applied))
	}
}

// checkAppliedCount proves the scan holds the wanted number of pointers.
func checkAppliedCount(t *testing.T, got pointerScan, tc pointerCase) {
	t.Helper()

	if len(got.applied) != len(tc.wantApplied) {
		t.Fatalf("applied = %d pointers, want %d", len(got.applied), len(tc.wantApplied))
	}
}

// checkAppliedPointers proves each applied pointer holds the wanted literal,
// secret flag and physical route.
func checkAppliedPointers(t *testing.T, got pointerScan, tc pointerCase) {
	t.Helper()

	for i, want := range tc.wantApplied {
		checkOnePointer(t, got.applied[i], i, want, tc)
	}
}

// checkOnePointer proves one applied pointer matches the expectation.
func checkOnePointer(t *testing.T, got appliedPointer, i int, want string, tc pointerCase) {
	t.Helper()

	if got.literal != want {
		t.Fatalf("applied %d = %q, want %q", i, got.literal, want)
	}

	if (got.secret != nil) != tc.wantSecrets[i] {
		t.Fatalf("secret of %q mismatch, want present %v", want, tc.wantSecrets[i])
	}

	checkOneRoute(t, got, want, tc, i)
}

// checkOneRoute proves the physical route of one applied pointer.
func checkOneRoute(t *testing.T, got appliedPointer, want string, tc pointerCase, i int) {
	t.Helper()

	if tc.wantRoutes == nil || i >= len(tc.wantRoutes) {
		return
	}

	if !slices.Equal(got.route, tc.wantRoutes[i]) {
		t.Fatalf("route of %q = %q, want %q", want, got.route, tc.wantRoutes[i])
	}
}

// TestScanPointersSecrets proves the kept digests match the raw digests of
// the original values.
func TestScanPointersSecrets(t *testing.T) {
	t.Parallel()

	got, err := scan(t, `{"a":"s","o":{"k":1}}`, "/a", "/o")
	if err != nil {
		t.Fatalf("scan pointers: %v", err)
	}

	if got.entangled {
		t.Fatal("entangled = true, want false")
	}

	if len(got.applied) != 2 {
		t.Fatalf("applied = %d pointers, want 2", len(got.applied))
	}

	wantString, _, err := digestRaw([]byte(`"s"`))
	if err != nil {
		t.Fatalf("digest string: %v", err)
	}

	if got.applied[0].secret == nil || *got.applied[0].secret != wantString {
		t.Fatal("secret of /a differs from the raw digest, want equal digests")
	}

	wantObject, _, err := digestRaw([]byte(`{"k":1}`))
	if err != nil {
		t.Fatalf("digest object: %v", err)
	}

	if got.applied[1].secret == nil || *got.applied[1].secret != wantObject {
		t.Fatal("secret of /o differs from the raw digest, want equal digests")
	}
}

// TestInstancePointers proves the same literal is kept once, in the order of
// tofuSensitivePointers.
func TestInstancePointers(t *testing.T) {
	t.Parallel()

	sensitive := []any{
		[]any{map[string]any{"type": "get_attr", "value": "a"}},
		[]any{map[string]any{"type": "get_attr", "value": "a"}},
	}
	info := instanceInfo{
		fields:     nil,
		indexKey:   nil,
		sensitive:  sensitive,
		attributes: jsontext.KindInvalid,
		size:       0,
	}

	got := instancePointers(info, "terraform_data.x", SensitiveMarks{"other.none": {"/id"}})
	if len(got) != 1 || got[0].literal != "/a" {
		t.Fatalf("pointers = %v, want one pointer /a", got)
	}

	got = instancePointers(info, "terraform_data.x", SensitiveMarks{"terraform_data.x": {"/m"}})
	if len(got) != 2 || got[0].literal != "/a" || got[1].literal != "/m" {
		t.Fatalf("pointers = %v, want /a and /m in order", got)
	}
}

// TestScanPointersStopsEarly proves the walk ends at the object end, before
// trailing garbage, and that a duplicate name in the walked part fails.
func TestScanPointersStopsEarly(t *testing.T) {
	t.Parallel()

	got, err := scan(t, `{"a":"s","b":"zz"}GARBAGE`, "/a")
	if err != nil {
		t.Fatalf("scan pointers: %v", err)
	}

	if got.entangled {
		t.Fatal("entangled = true, want false")
	}

	if len(got.applied) != 1 || got.applied[0].literal != "/a" {
		t.Fatalf("applied = %v, want one pointer /a", got.applied)
	}

	if _, err := scan(t, `{"a":"s","a":"t"}`, "/a"); err == nil {
		t.Fatal("duplicate names succeeded, want an error")
	}
}

// manyMembers is the attributes JSON of the scaling tests: members "k0" to
// "k<n-1>" with string values, then "p" with one nested member "q".
func manyMembers(n int) string {
	var b strings.Builder

	b.WriteString("{")

	for i := range n {
		b.WriteString(`"k`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":"v",`)
	}

	b.WriteString(`"p":{"q":"s"}}`)

	return b.String()
}

// manyPointers returns the pointers "/k0" to "/k<n-1>", then "/p/q".
func manyPointers(n int) []string {
	literals := make([]string, 0, n+1)
	for i := range n {
		literals = append(literals, "/k"+strconv.Itoa(i))
	}

	return append(literals, "/p/q")
}

// fastestScan returns the shortest of runs scans of attributes with literals.
// The shortest run is the one that other load on the machine slowed least.
func fastestScan(t *testing.T, runs int, attributes string, literals []string) time.Duration {
	t.Helper()

	fastest := time.Duration(math.MaxInt64)

	for range runs {
		start := time.Now()

		if _, err := scan(t, attributes, literals...); err != nil {
			t.Fatalf("scan pointers: %v", err)
		}

		fastest = min(fastest, time.Since(start))
	}

	return fastest
}

// scanScaleLimit bounds the time of a scan with many pointers as a multiple of
// the time of a scan with one pointer over the same members (issue 466). With
// -race and parallel CPU load, the ratio measured from 0.4 to 2.9. A lookup
// over every pointer for each member, the cost this test guards against, made
// it 90.
const scanScaleLimit = 20

// TestScanPointersManyPointersManyMembers proves the scan cost does not grow
// with members times pointers. It compares two scans on the same machine at
// the same time, so the bound does not depend on the speed of the machine
// (issue 466). BenchmarkScanPointersManyPointersManyMembers measures the
// time itself.
func TestScanPointersManyPointersManyMembers(t *testing.T) {
	t.Parallel()

	attributes := manyMembers(50000)
	literals := manyPointers(5000)

	got, err := scan(t, attributes, literals...)
	if err != nil {
		t.Fatalf("scan pointers: %v", err)
	}

	if got.entangled {
		t.Fatal("entangled = true, want false")
	}

	if len(got.applied) != 5001 {
		t.Fatalf("applied = %d pointers, want 5001", len(got.applied))
	}

	one := fastestScan(t, 3, attributes, []string{"/p/q"})
	many := fastestScan(t, 3, attributes, literals)

	if many > scanScaleLimit*one {
		t.Fatalf("scan with 5001 pointers took %v, %.1f times the %v of one pointer, want at most %d times",
			many, float64(many)/float64(one), one, scanScaleLimit)
	}
}

// BenchmarkScanPointersManyPointersManyMembers measures one scan of 200000
// members with 20001 pointers.
func BenchmarkScanPointersManyPointersManyMembers(b *testing.B) {
	attributes := manyMembers(200000)
	literals := manyPointers(20000)

	pointers := make([]revealPointer, 0, len(literals))
	for _, literal := range literals {
		pointers = append(pointers, revealPointer{literal: literal, tokens: splitPointer(literal)})
	}

	for b.Loop() {
		if _, err := scanPointers(jsontext.NewDecoder(strings.NewReader(attributes)), pointers); err != nil {
			b.Fatalf("scan pointers: %v", err)
		}
	}
}

// TestPointerVisitorScalars proves a null or string attributes value leaves
// applied empty and ends the walk.
func TestPointerVisitorScalars(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`null`, `"s"`} {
		dec := jsontext.NewDecoder(strings.NewReader(raw))

		var out pointerScan

		visit := pointerVisitor([]revealPointer{{literal: "/a", tokens: []string{"a"}}}, &out)

		consumed, err := visit(dec, attributesMemberName)
		if !consumed {
			t.Fatalf("visitor of %s consumed = false, want true", raw)
		}

		if !errors.Is(err, errStopWalk) {
			t.Fatalf("visitor of %s error = %v, want stop walk", raw, err)
		}

		if len(out.applied) != 0 || out.entangled {
			t.Fatalf("visitor of %s scan = %+v, want empty scan", raw, out)
		}
	}
}
