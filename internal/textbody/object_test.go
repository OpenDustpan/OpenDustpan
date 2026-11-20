package textbody_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/textbody"
)

// nested is a body of the object tests with an object, an array and a list of
// objects.
type nested struct {
	Summary string            `json:"summary"`
	Inner   map[string]string `json:"inner"`
	Items   []note            `json:"items"`
}

// TestObjectRefuses proves that the option Object refuses a body that is not
// one object, and a key that an object names two times at any depth. No error
// text holds a part of the body.
func TestObjectRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want error
		name string
		body string
	}{
		{name: "null", body: `null`, want: textbody.ErrNotObject},
		{name: "null with space", body: " \n null \t", want: textbody.ErrNotObject},
		{name: "an array", body: `[]`, want: textbody.ErrNotObject},
		{name: "a string", body: `"` + secretMark + `"`, want: textbody.ErrNotObject},
		{name: "a number", body: `1`, want: textbody.ErrNotObject},
		{name: "true", body: `true`, want: textbody.ErrNotObject},
		{
			name: "a key two times",
			body: `{"summary":"a","summary":"` + secretMark + `"}`,
			want: textbody.ErrRepeatedKey,
		},
		{
			name: "a key two times with an object between",
			body: `{"summary":"a","inner":{"x":"1"},"summary":"b"}`, want: textbody.ErrRepeatedKey,
		},
		{
			name: "a key two times in an inner object",
			body: `{"inner":{"x":"1","x":"2"}}`,
			want: textbody.ErrRepeatedKey,
		},
		{
			name: "a key two times in an object of an array",
			body: `{"items":[{"summary":"a"},{"summary":"b","summary":"c"}]}`, want: textbody.ErrRepeatedKey,
		},
		{
			name: "a key two times in another spelling",
			body: "{\"summary\":\"a\",\"\\u0073ummary\":\"b\"}",
			want: textbody.ErrRepeatedKey,
		},
		{name: "an object that does not end", body: `{"summary":"a"`, want: textbody.ErrJSON},
		{name: "a key that is not text", body: `{1:"a"}`, want: textbody.ErrJSON},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var v nested

			err := textbody.Unmarshal([]byte(tc.body), &v, textbody.Object())
			if !errors.Is(err, tc.want) || !errors.Is(err, textbody.ErrJSON) {
				t.Fatalf("Unmarshal(%q) = %v, want %v", tc.body, err, tc.want)
			}

			if strings.Contains(err.Error(), secretMark) {
				t.Fatalf("error %q holds a part of the body", err)
			}
		})
	}
}

// TestObjectAccepts proves that the option Object accepts one object, also
// with the same key in two different objects and with null as a member value.
func TestObjectAccepts(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{}`,
		` {"summary":"a"} `,
		`{"summary":null}`,
		`{"items":[{"summary":"a"},{"summary":"b"}],"summary":"c"}`,
		`{"inner":{"summary":"a"},"summary":"b"}`,
		`{"items":[],"inner":{}}`,
	} {
		var v nested
		if err := textbody.Unmarshal([]byte(body), &v, textbody.Object()); err != nil {
			t.Errorf("Unmarshal(%q) = %v, want nil", body, err)
		}
	}
}

// TestWithoutObjectNullStaysValid proves that the option changes only the
// routes that use it: without it, null decodes and changes nothing.
func TestWithoutObjectNullStaysValid(t *testing.T) {
	t.Parallel()

	v := note{Summary: "kept"}
	if err := textbody.Unmarshal([]byte(`null`), &v); err != nil || v.Summary != "kept" {
		t.Fatalf("Unmarshal(null) = %v, %+v; want nil and no change", err, v)
	}
}
