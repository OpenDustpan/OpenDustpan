package workspace

import (
	"bytes"
	"encoding/json/jsontext"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// TestDigestKeyOrder proves the same object with a different key order gives
// equal digests.
func TestDigestKeyOrder(t *testing.T) {
	t.Parallel()

	first, _, err := digestRaw([]byte(`{"x":1,"y":[1,2]}`))
	if err != nil {
		t.Fatalf("digest first: %v", err)
	}

	second, _, err := digestRaw([]byte(`{"y":[1,2],"x":1}`))
	if err != nil {
		t.Fatalf("digest second: %v", err)
	}

	if first != second {
		t.Fatal("key order changed the digest, want equal digests")
	}
}

// TestDigestArrayOrder proves arrays with a different element order give
// different digests.
func TestDigestArrayOrder(t *testing.T) {
	t.Parallel()

	first, _, err := digestRaw([]byte(`[1,2]`))
	if err != nil {
		t.Fatalf("digest first: %v", err)
	}

	second, _, err := digestRaw([]byte(`[2,1]`))
	if err != nil {
		t.Fatalf("digest second: %v", err)
	}

	if first == second {
		t.Fatal("element order kept the digest, want different digests")
	}
}

// TestDigestNumberLiterals proves 1, 1.0 and 1e0 give pairwise different
// digests.
func TestDigestNumberLiterals(t *testing.T) {
	t.Parallel()

	raws := []string{`1`, `1.0`, `1e0`}
	digests := make([]digest, len(raws))

	for i, raw := range raws {
		d, _, err := digestRaw([]byte(raw))
		if err != nil {
			t.Fatalf("digest %q: %v", raw, err)
		}

		digests[i] = d
	}

	for i := range digests {
		for j := i + 1; j < len(digests); j++ {
			if digests[i] == digests[j] {
				t.Fatalf("digests of %q and %q match, want different", raws[i], raws[j])
			}
		}
	}
}

// TestDigestEscapedString proves a plain and an escaped encoding of the same
// string give equal digests.
func TestDigestEscapedString(t *testing.T) {
	t.Parallel()

	plain, _, err := digestRaw([]byte(`"xA"`))
	if err != nil {
		t.Fatalf("digest plain: %v", err)
	}

	escaped, _, err := digestRaw([]byte(`"x\u0041"`))
	if err != nil {
		t.Fatalf("digest escaped: %v", err)
	}

	if plain != escaped {
		t.Fatal("escaped string changed the digest, want equal digests")
	}
}

// TestDigestNestedValue proves a nested change gives a different digest.
func TestDigestNestedValue(t *testing.T) {
	t.Parallel()

	first, _, err := digestRaw([]byte(`{"a":{"b":1}}`))
	if err != nil {
		t.Fatalf("digest first: %v", err)
	}

	second, _, err := digestRaw([]byte(`{"a":{"b":2}}`))
	if err != nil {
		t.Fatalf("digest second: %v", err)
	}

	if first == second {
		t.Fatal("nested change kept the digest, want different digests")
	}
}

// TestDigestBoundary proves split key and value text cannot collide.
func TestDigestBoundary(t *testing.T) {
	t.Parallel()

	first, _, err := digestRaw([]byte(`{"ab":"c"}`))
	if err != nil {
		t.Fatalf("digest first: %v", err)
	}

	second, _, err := digestRaw([]byte(`{"a":"bc"}`))
	if err != nil {
		t.Fatalf("digest second: %v", err)
	}

	if first == second {
		t.Fatal("boundary change kept the digest, want different digests")
	}
}

// TestDigestShapesDistinct proves empty containers, strings, numbers and
// literals give pairwise different digests.
func TestDigestShapesDistinct(t *testing.T) {
	t.Parallel()

	raws := []string{`{}`, `[]`, `""`, `null`, `"null"`, `0`, `false`}
	digests := make([]digest, len(raws))

	for i, raw := range raws {
		d, _, err := digestRaw([]byte(raw))
		if err != nil {
			t.Fatalf("digest %q: %v", raw, err)
		}

		digests[i] = d
	}

	for i := range digests {
		for j := i + 1; j < len(digests); j++ {
			if digests[i] == digests[j] {
				t.Fatalf("digests of %q and %q match, want different", raws[i], raws[j])
			}
		}
	}
}

// TestDigestNumberVsString proves the number 1 and the string 1 give
// different digests.
func TestDigestNumberVsString(t *testing.T) {
	t.Parallel()

	number, _, err := digestRaw([]byte(`1`))
	if err != nil {
		t.Fatalf("digest number: %v", err)
	}

	text, _, err := digestRaw([]byte(`"1"`))
	if err != nil {
		t.Fatalf("digest string: %v", err)
	}

	if number == text {
		t.Fatal("number and string share a digest, want different digests")
	}
}

// buildDigestObject renders one object with keys in order.
func buildDigestObject(keys []string) []byte {
	var sb strings.Builder

	sb.WriteByte('{')

	for i, key := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString(strconv.Quote(key))
		sb.WriteByte(':')
		sb.WriteString(strings.TrimPrefix(key, "k"))
	}

	sb.WriteByte('}')

	return []byte(sb.String())
}

// TestDigestKeyPermutation proves shuffled copies of a 200-key object give
// equal digests.
func TestDigestKeyPermutation(t *testing.T) {
	t.Parallel()

	keys := make([]string, 200)
	for i := range keys {
		keys[i] = "k" + strconv.Itoa(i)
	}

	want, _, err := digestRaw(buildDigestObject(keys))
	if err != nil {
		t.Fatalf("digest base: %v", err)
	}

	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // Seeded test data, not a security use.

	for i := range 20 {
		shuffled := append([]string(nil), keys...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		got, _, err := digestRaw(buildDigestObject(shuffled))
		if err != nil {
			t.Fatalf("digest copy %d: %v", i, err)
		}

		if got != want {
			t.Fatalf("copy %d changed the digest, want equal digests", i)
		}
	}
}

// TestDigestLargeString proves a 4 MiB string hashes twice to the same
// digest with the byte length as size.
func TestDigestLargeString(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("a", 4*1024*1024)
	raw := []byte(strconv.Quote(text))

	first, firstShape, err := digestRaw(raw)
	if err != nil {
		t.Fatalf("digest first: %v", err)
	}

	second, _, err := digestRaw(raw)
	if err != nil {
		t.Fatalf("digest second: %v", err)
	}

	if first != second {
		t.Fatal("large string changed the digest, want equal digests")
	}

	if firstShape.size != 4194304 {
		t.Fatalf("size = %d, want 4194304", firstShape.size)
	}
}

// TestDigestDeepNesting proves 5000 nested arrays hash without a stack
// overflow, with outer size 1.
func TestDigestDeepNesting(t *testing.T) {
	t.Parallel()

	raw := strings.Repeat("[", 5000) + "1" + strings.Repeat("]", 5000)

	_, gotShape, err := digestRaw([]byte(raw))
	if err != nil {
		t.Fatalf("digest nested: %v", err)
	}

	if gotShape.size != 1 {
		t.Fatalf("size = %d, want 1", gotShape.size)
	}
}

// TestDigestDuplicateName proves duplicate member names fail.
func TestDigestDuplicateName(t *testing.T) {
	t.Parallel()

	if _, _, err := digestRaw([]byte(`{"a":1,"a":2}`)); err == nil {
		t.Fatal("duplicate names succeeded, want an error")
	}
}

// TestDigestPropagable proves the propagable truth table.
func TestDigestPropagable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want bool
	}{
		{`""`, false},
		{`"x"`, true},
		{`{}`, false},
		{`{"a":1}`, true},
		{`[]`, false},
		{`[0]`, true},
		{`0`, false},
		{`true`, false},
		{`null`, false},
	}

	for _, tc := range cases {
		_, gotShape, err := digestRaw([]byte(tc.raw))
		if err != nil {
			t.Fatalf("digest %q: %v", tc.raw, err)
		}

		if gotShape.propagable() != tc.want {
			t.Errorf("propagable(%q) = %v, want %v", tc.raw, gotShape.propagable(), tc.want)
		}
	}
}

// TestDigestConsumesOneValue proves digestStreamed consumes exactly one
// value, and the next token read is the number 3.
func TestDigestConsumesOneValue(t *testing.T) {
	t.Parallel()

	dec := jsontext.NewDecoder(bytes.NewReader([]byte("[1,2] 3")))

	if _, _, err := digestStreamed(dec); err != nil {
		t.Fatalf("digest streamed: %v", err)
	}

	tok, err := dec.ReadToken()
	if err != nil {
		t.Fatalf("read next token: %v", err)
	}

	if tok.Kind() != jsontext.KindNumber || tok.String() != "3" {
		t.Fatalf("next token = %q, want number 3", tok.String())
	}
}
