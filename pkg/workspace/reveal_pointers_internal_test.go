package workspace

import (
	"encoding/json/jsontext"
	"errors"
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
			wantSecrets:   []bool{true, true, false},
			wantEntangled: false,
		},
		{
			name:          "containers",
			attributes:    `{"o":{"k":1},"e":{},"l":[],"z":"","t":true,"u":null}`,
			literals:      []string{"/o", "/e", "/l", "/z", "/t", "/u"},
			wantApplied:   []string{"/o", "/e", "/l", "/z", "/t", "/u"},
			wantSecrets:   []bool{true, false, false, false, false, false},
			wantEntangled: false,
		},
		{
			name:          "unresolved",
			attributes:    `{"a":"s","b":{"c":[1,"x"]},"n":7,"u":null}`,
			literals:      []string{"/missing", "/b/x", "/b/c/5", "/a/x", "/u/x", "/n/0"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "array index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/1"},
			wantApplied:   []string{"/l/1"},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "array leading zero",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/01"},
			wantApplied:   []string{"/l/01"},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "array duplicate index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/1", "/l/01"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "array plus index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/+1"},
			wantApplied:   []string{"/l/+1"},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "array negative index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/-1"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "array text index",
			attributes:    `{"l":["p","q"]}`,
			literals:      []string{"/l/x"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "ancestor first",
			attributes:    `{"a":{"b":"s"}}`,
			literals:      []string{"/a", "/a/b"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "descendant first",
			attributes:    `{"a":{"b":"s"}}`,
			literals:      []string{"/a/b", "/a"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "missing descendant",
			attributes:    `{"a":{"b":"s"}}`,
			literals:      []string{"/a", "/a/zz"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "equal tokens",
			attributes:    `{"~":"s"}`,
			literals:      []string{"/~", "/~0"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "wrapper target",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w"},
			wantApplied:   []string{"/w"},
			wantSecrets:   []bool{true},
			wantEntangled: false,
		},
		{
			name:          "wrapper step",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w/k"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "wrapper value step",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w/value/k"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "wrapper type step",
			attributes:    `{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
			literals:      []string{"/w/type"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "value without type",
			attributes:    `{"o":{"value":1,"x":2}}`,
			literals:      []string{"/o/k"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: false,
		},
		{
			name:          "quirk",
			attributes:    `{"p":{"a":"v"}}`,
			literals:      []string{"/p", "/p/secret"},
			wantApplied:   nil,
			wantSecrets:   nil,
			wantEntangled: true,
		},
		{
			name:          "escaped keys",
			attributes:    `{"a/b":"s1","c~d":"s2"}`,
			literals:      []string{"/a~1b", "/c~0d"},
			wantApplied:   []string{"/a~1b", "/c~0d"},
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
		if len(got.applied) != 0 {
			t.Fatalf("applied = %d pointers, want none when entangled", len(got.applied))
		}

		return
	}

	if len(got.applied) != len(tc.wantApplied) {
		t.Fatalf("applied = %d pointers, want %d", len(got.applied), len(tc.wantApplied))
	}

	for i, want := range tc.wantApplied {
		if got.applied[i].literal != want {
			t.Fatalf("applied %d = %q, want %q", i, got.applied[i].literal, want)
		}

		if (got.applied[i].secret != nil) != tc.wantSecrets[i] {
			t.Fatalf("secret of %q mismatch, want present %v", want, tc.wantSecrets[i])
		}
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

// TestScanPointersManyPointersManyMembers proves the scan cost does not grow
// with members times pointers.
func TestScanPointersManyPointersManyMembers(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	b.WriteString("{")

	for i := range 200000 {
		b.WriteString(`"k`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":"v",`)
	}

	b.WriteString(`"p":{"q":"s"}}`)

	literals := make([]string, 0, 20001)
	for i := range 20000 {
		literals = append(literals, "/k"+strconv.Itoa(i))
	}

	literals = append(literals, "/p/q")

	start := time.Now()

	got, err := scan(t, b.String(), literals...)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("scan pointers: %v", err)
	}

	if got.entangled {
		t.Fatal("entangled = true, want false")
	}

	if len(got.applied) != 20001 {
		t.Fatalf("applied = %d pointers, want 20001", len(got.applied))
	}

	if elapsed > 3*time.Second {
		t.Fatalf("scan took %v, want within 3s", elapsed)
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
