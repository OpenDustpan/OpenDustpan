package logguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
)

// The tests in this file reach the fail-closed layers of the JSON walk
// directly. An encoder writes only valid compact JSON, so no value that a
// caller logs reaches these layers.

func TestEscapeJSONStringsRefusesInvalidJSON(t *testing.T) {
	t.Parallel()

	for name, js := range map[string]string{
		"two values":          `"a" "b"`,
		"object then text":    `{} x`,
		"two objects":         `{}{}`,
		"unterminated string": "\"\u0085",
		"trailing comma":      `[1,]`,
		"missing value":       `{"a":}`,
		"missing colon":       `{"a" 1}`,
		"number key":          `{1:2}`,
		"open array":          `[1`,
		"empty":               ``,
		"bare word":           `nul`,
		"close only":          `]`,
	} {
		if out, err := escapeJSONStrings([]byte(js)); err == nil {
			t.Errorf("%s: escapeJSONStrings(%q) = %q, want an error", name, js, out)
		}
	}
}

func TestEscapeJSONStringsRefusesTrailingText(t *testing.T) {
	t.Parallel()

	for _, js := range []string{`"a" "b"`, `{} x`, `1 2`, `[] []`} {
		_, err := escapeJSONStrings([]byte(js))
		if !errors.Is(err, errTrailing) {
			t.Errorf("escapeJSONStrings(%q): error %v, want errTrailing", js, err)
		}
	}
}

// TestEscapeEncodedFailsClosed sends invalid JSON with raw hidden runes to
// the walk. The value must be "!ERROR:" with escaped text, and neither the
// value nor its decoded form may hold a hidden rune.
func TestEscapeEncodedFailsClosed(t *testing.T) {
	t.Parallel()

	for name, js := range map[string]string{
		"two values":    "\"a\u0085\" \"b\r\nX\"",
		"bad object":    "{\"a\u202e\":}",
		"unterminated":  "[\"a\x7f",
		"text after":    "{} \u2028x",
		"invalid UTF-8": "\"\xff\" \"\xfe\"",
		"forged line":   "\"ok\"\nlevel=ERROR msg=forged",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := escapeEncoded([]byte(js))

			text := v.String()
			if !strings.HasPrefix(text, "!ERROR:") {
				t.Fatalf("value = %q, want !ERROR: text", text)
			}

			if r, found := ident.FirstHidden(text); found {
				t.Errorf("value holds hidden rune %U: %q", r, text)
			}

			checkDecodedValue(t, v.Any())
		})
	}
}

// checkDecodedValue encodes v as JSON, decodes it as a string and fails
// when the decoded string holds a hidden rune.
func checkDecodedValue(t *testing.T, v any) {
	t.Helper()

	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		t.Fatalf("encode value: %v", err)
	}

	var back string
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatalf("value is not a JSON string: %v: %q", err, b.String())
	}

	if r, found := ident.FirstHidden(back); found {
		t.Errorf("decoded value holds hidden rune %U: %q", r, back)
	}
}

// walkError is an error whose text is the test value.
type walkError string

func (e walkError) Error() string { return string(e) }

func TestErrorValueEscapesErrorText(t *testing.T) {
	t.Parallel()

	got := errorValue(walkError("json: error calling MarshalJSON: a\u0085b\r\nX\x00")).String()
	if want := `!ERROR:json: error calling MarshalJSON: a\x85b\r\nX\x00`; got != want {
		t.Errorf("errorValue = %q, want %q", got, want)
	}
}

// fakeTokens is a token source that answers fixed tokens, so a test can
// send a token that json.Decoder never answers.
type fakeTokens struct {
	tokens []json.Token
}

func (f *fakeTokens) Token() (json.Token, error) {
	if len(f.tokens) == 0 {
		return nil, io.EOF
	}

	tok := f.tokens[0]
	f.tokens = f.tokens[1:]

	return tok, nil
}

func (f *fakeTokens) More() bool {
	if len(f.tokens) == 0 {
		return false
	}

	d, ok := f.tokens[0].(json.Delim)

	return !ok || (d != '}' && d != ']')
}

func TestCopyValueRefusesUnknownTokens(t *testing.T) {
	t.Parallel()

	for name, tokens := range map[string][]json.Token{
		"number key":    {json.Delim('{'), json.Number("1"), "v", json.Delim('}')},
		"bool key":      {json.Delim('{'), true, "v", json.Delim('}')},
		"nil key":       {json.Delim('{'), nil, "v", json.Delim('}')},
		"int value":     {42},
		"float in list": {json.Delim('['), 1.5, json.Delim(']')},
	} {
		var out bytes.Buffer

		err := copyValue(&out, &fakeTokens{tokens: tokens})
		if !errors.Is(err, errToken) {
			t.Errorf("%s: error %v, want errToken; output %q", name, err, out.String())
		}
	}
}

func TestJSONValueRefusesTooDeepValues(t *testing.T) {
	t.Parallel()

	var deep any = "x"
	for range 10001 {
		deep = []any{deep}
	}

	if got := jsonValue(deep).String(); !strings.HasPrefix(got, "!ERROR:") {
		t.Errorf("value = %.80q, want !ERROR: text", got)
	}
}
