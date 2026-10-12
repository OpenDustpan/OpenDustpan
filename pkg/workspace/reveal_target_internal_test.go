package workspace

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"slices"
	"testing"
)

// scanTargetDecoder returns a decoder at the first token of raw.
func scanTargetDecoder(raw string) *jsontext.Decoder {
	return jsontext.NewDecoder(bytes.NewReader([]byte(raw)))
}

// mustDigestRaw digests raw for the tests or fails.
func mustDigestRaw(t *testing.T, raw string) digest {
	t.Helper()

	sum, _, err := digestRaw([]byte(raw))
	if err != nil {
		t.Fatalf("digest raw: %v", err)
	}

	return sum
}

func TestScanTargetFindsValue(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"a":"s","b":"x"}`)
	want := mustDigestRaw(t, `"s"`)
	secrets := map[digest]struct{}{want: {}}

	res, err := scanTarget(dec, []string{"a"}, secrets, true)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !res.found {
		t.Fatal("found = false, want true")
	}

	if string(res.raw) != `"s"` {
		t.Fatalf("raw = %s, want %s", res.raw, `"s"`)
	}

	if res.sum != want {
		t.Fatal("sum does not match digestRaw of the value")
	}

	if !slices.Equal(res.route, []string{"a"}) {
		t.Fatalf("route = %q, want [a]", res.route)
	}

	if len(res.maskedDepths) != 0 {
		t.Fatalf("maskedDepths = %v, want empty", res.maskedDepths)
	}
}

func TestScanTargetAncestorMasked(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"p":{"q":"v"},"other":"x"}`)
	parent := mustDigestRaw(t, `{"q":"v"}`)
	secrets := map[digest]struct{}{parent: {}}

	res, err := scanTarget(dec, []string{"p", "q"}, secrets, true)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !res.found {
		t.Fatal("found = false, want true")
	}

	if !slices.Equal(res.maskedDepths, []int{1}) {
		t.Fatalf("maskedDepths = %v, want [1]", res.maskedDepths)
	}
}

func TestScanTargetNoHashingStopsAfterValue(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"a":"s","b":"x"}`)

	res, err := scanTarget(dec, []string{"a"}, nil, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !res.found {
		t.Fatal("found = false, want true")
	}

	if string(res.raw) != `"s"` {
		t.Fatalf("raw = %s, want %s", res.raw, `"s"`)
	}

	if dec.PeekKind() != jsontext.KindString {
		t.Fatalf("peek after read = %v, want a string (the next member)", dec.PeekKind())
	}
}

func TestScanTargetHashingConsumesValue(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"a":"s","b":"x"}`)
	want := mustDigestRaw(t, `"s"`)

	res, err := scanTarget(dec, []string{"a"}, map[digest]struct{}{want: {}}, true)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !res.found {
		t.Fatal("found = false, want true")
	}

	if dec.PeekKind() != jsontext.KindInvalid {
		t.Fatalf("peek after hashing read = %v, want invalid (consumed)", dec.PeekKind())
	}
}

func TestScanTargetMissing(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"a":"s"}`)

	res, err := scanTarget(dec, []string{"missing"}, nil, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if res.found {
		t.Fatal("found = true, want false")
	}
}

func TestScanTargetScalarWithTokensLeft(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"a":"s"}`)

	res, err := scanTarget(dec, []string{"a", "b"}, map[digest]struct{}{}, true)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if res.found {
		t.Fatal("found = true, want false")
	}
}

func TestScanTargetNonCanonicalIndex(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"l":["a","b","c"]}`)

	res, err := scanTarget(dec, []string{"l", "01"}, nil, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !res.found {
		t.Fatal("found = false, want true")
	}

	if string(res.raw) != `"b"` {
		t.Fatalf("raw = %s, want %s", res.raw, `"b"`)
	}

	if !slices.Equal(res.route, []string{"l", "1"}) {
		t.Fatalf("route = %q, want [l 1]", res.route)
	}
}

func TestDecideMaskedTable(t *testing.T) {
	t.Parallel()

	const base = "/resources/0/instances/0/attributes"

	secret := mustDigestRaw(t, `"s"`)
	other := mustDigestRaw(t, `"other"`)
	secretsOf := func(sums ...digest) map[digest]struct{} {
		out := map[digest]struct{}{}
		for _, sum := range sums {
			out[sum] = struct{}{}
		}

		return out
	}

	appliedAt := func(literal string, tokens, route []string, secret *digest) appliedPointer {
		return appliedPointer{literal: literal, tokens: tokens, route: route, secret: secret}
	}

	kept := secret

	cases := []struct {
		name    string
		path    string
		applied []appliedPointer
		secrets map[digest]struct{}
		res     targetResult
		want    bool
	}{
		{
			name:    "literal",
			path:    base + "/a",
			applied: []appliedPointer{appliedAt("/a", []string{"a"}, []string{"a"}, nil)},
			secrets: secretsOf(),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          digest{},
				route:        []string{"a"},
				maskedDepths: nil,
			},
			want: true,
		},
		{
			name:    "duplicate",
			path:    base + "/copy",
			applied: []appliedPointer{appliedAt("/a", []string{"a"}, []string{"a"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"copy"},
				maskedDepths: nil,
			},
			want: true,
		},
		{
			name:    "ancestor masked",
			path:    base + "/p/q",
			applied: []appliedPointer{appliedAt("/a", []string{"a"}, []string{"a"}, &kept)},
			secrets: secretsOf(secret, other),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"p", "q"},
				maskedDepths: []int{1},
			},
			want: false,
		},
		{
			name:    "inside applied location",
			path:    base + "/p/q",
			applied: []appliedPointer{appliedAt("/p", []string{"p"}, []string{"p"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"p", "q"},
				maskedDepths: nil,
			},
			want: false,
		},
		{
			name:    "at applied location is literal",
			path:    base + "/p",
			applied: []appliedPointer{appliedAt("/p", []string{"p"}, []string{"p"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          other,
				route:        []string{"p"},
				maskedDepths: nil,
			},
			want: true,
		},
		{
			name:    "non canonical index",
			path:    base + "/l/01",
			applied: []appliedPointer{appliedAt("/a", []string{"a"}, []string{"a"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"l", "1"},
				maskedDepths: nil,
			},
			want: false,
		},
		{
			name:    "no secrets",
			path:    base + "/copy",
			applied: []appliedPointer{appliedAt("/a", []string{"a"}, []string{"a"}, nil)},
			secrets: secretsOf(),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"copy"},
				maskedDepths: nil,
			},
			want: false,
		},
		{
			name:    "missing",
			path:    base + "/missing",
			applied: []appliedPointer{appliedAt("/a", []string{"a"}, []string{"a"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        false,
				raw:          nil,
				sum:          digest{},
				route:        nil,
				maskedDepths: nil,
			},
			want: false,
		},
		{
			name:    "index canonicalizes",
			path:    base + "/l/1",
			applied: []appliedPointer{appliedAt("/a", []string{"a"}, []string{"a"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"l", "1"},
				maskedDepths: nil,
			},
			want: true,
		},
		{
			name:    "non-canonical route",
			path:    base + "/l/1",
			applied: []appliedPointer{appliedAt("/l/01", []string{"l", "01"}, []string{"l", "1"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"l", "1"},
				maskedDepths: nil,
			},
			want: false,
		},
		{
			name:    "dirty P",
			path:    base + "/t",
			applied: []appliedPointer{appliedAt("/t/k", []string{"t", "k"}, []string{"t", "k"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"t"},
				maskedDepths: nil,
			},
			want: false,
		},
		{
			name:    "dirty masked ancestor does not block",
			path:    base + "/t/z",
			applied: []appliedPointer{appliedAt("/t/k", []string{"t", "k"}, []string{"t", "k"}, &kept)},
			secrets: secretsOf(secret, other),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"t", "z"},
				maskedDepths: []int{1},
			},
			want: true,
		},
		{
			name:    "clean masked ancestor blocks",
			path:    base + "/t/z",
			applied: []appliedPointer{appliedAt("/x", []string{"x"}, []string{"x"}, &kept)},
			secrets: secretsOf(secret, other),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          secret,
				route:        []string{"t", "z"},
				maskedDepths: []int{1},
			},
			want: false,
		},
		{
			name:    "literal match true",
			path:    base + "/t/k",
			applied: []appliedPointer{appliedAt("/t/k", []string{"t", "k"}, []string{"t", "k"}, &kept)},
			secrets: secretsOf(secret),
			res: targetResult{
				found:        true,
				raw:          nil,
				sum:          other,
				route:        []string{"t", "k"},
				maskedDepths: nil,
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := decideMasked(tc.path, base, tc.applied, tc.secrets, tc.res); got != tc.want {
				t.Fatalf("decideMasked = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecodeTargetMarkerShaped(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"secret":true,"path":"x"}`)

	_, ok, err := decodeTargetValue(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if ok {
		t.Fatal("marker shaped value answered, want not found")
	}
}

func TestDecodeTargetNumberKeepsText(t *testing.T) {
	t.Parallel()

	raw := []byte(`1.0`)

	value, ok, err := decodeTargetValue(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !ok {
		t.Fatal("found = false, want true")
	}

	num, ok := value.(json.Number)
	if !ok {
		t.Fatalf("value type = %T, want json.Number", value)
	}

	if string(num) != "1.0" {
		t.Fatalf("number = %q, want 1.0", string(num))
	}
}

func TestScanTargetEscapedKeyRoute(t *testing.T) {
	t.Parallel()

	dec := scanTargetDecoder(`{"a/b":"s"}`)

	res, err := scanTarget(dec, []string{"a/b"}, nil, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !res.found {
		t.Fatal("found = false, want true")
	}

	if !slices.Equal(res.route, []string{"a/b"}) {
		t.Fatalf("route = %q, want [a/b]", res.route)
	}

	got := canonicalPath("/resources/0/instances/0/attributes", res.route)
	if got != "/resources/0/instances/0/attributes/a~1b" {
		t.Fatalf("canonical = %q", got)
	}
}
