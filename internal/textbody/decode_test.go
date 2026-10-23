package textbody_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/textbody"
)

// note is the body of the tests: one free-text field.
type note struct {
	Summary string `json:"summary"`
}

// secretMark is a value in a hostile body. No error text may hold it.
const secretMark = "hunter2"

// failingReader is a body whose read fails.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestReadLimit proves that Read refuses a body over the limit with the
// limit in the error, reads a body at the limit, and fails closed for a limit
// of zero or less.
func TestReadLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		body      string
		limit     int64
		wantLimit int64
		wantErr   bool
	}{
		{name: "at the limit", body: `{"summary":"a"}`, limit: 15, wantLimit: 0, wantErr: false},
		{name: "one byte over the limit", body: `{"summary":"ab"}`, limit: 15, wantLimit: 15, wantErr: true},
		{name: "a large body", body: strings.Repeat("a", 1<<20), limit: 64, wantLimit: 64, wantErr: true},
		{name: "a limit of zero", body: `{}`, limit: 0, wantLimit: 0, wantErr: true},
		{name: "a negative limit", body: `{}`, limit: -1, wantLimit: 0, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tc.body))

			body, err := textbody.Read(httptest.NewRecorder(), r, tc.limit)
			if !tc.wantErr {
				if err != nil || string(body) != tc.body {
					t.Fatalf("Read = %q, %v, want the body", body, err)
				}

				return
			}

			tooLarge, ok := errors.AsType[*textbody.TooLargeError](err)
			if !ok || !errors.Is(err, textbody.ErrTooLarge) || tooLarge.Limit != tc.wantLimit {
				t.Fatalf("Read = %v, want a TooLargeError with the limit %d", err, tc.wantLimit)
			}
		})
	}
}

// TestReadUnreadable proves that a read fault gives ErrUnreadable and no
// part of the body.
func TestReadUnreadable(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", failingReader{})

	body, err := textbody.Read(httptest.NewRecorder(), r, 64)
	if !errors.Is(err, textbody.ErrUnreadable) || body != nil {
		t.Fatalf("Read = %q, %v, want ErrUnreadable", body, err)
	}
}

// TestUnmarshalRefuses proves each refusal of Unmarshal, with hostile bodies,
// and that no error text holds a part of the body.
func TestUnmarshalRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		opts []textbody.Option
		want error
	}{
		{name: "an empty body", body: ``, opts: nil, want: textbody.ErrEmpty},
		{name: "a body of spaces", body: " \n\t ", opts: nil, want: textbody.ErrEmpty},
		{name: "a form feed with AllowEmpty", body: "\f", opts: []textbody.Option{textbody.AllowEmpty()}, want: textbody.ErrJSON},
		{name: "a vertical tab with AllowEmpty", body: "\v", opts: []textbody.Option{textbody.AllowEmpty()}, want: textbody.ErrJSON},
		{name: "a no-break space with AllowEmpty", body: "\u00a0", opts: []textbody.Option{textbody.AllowEmpty()}, want: textbody.ErrJSON},
		{name: "a next line with AllowEmpty", body: "\u0085", opts: []textbody.Option{textbody.AllowEmpty()}, want: textbody.ErrJSON},
		{name: "a line separator with AllowEmpty", body: "\u2028", opts: []textbody.Option{textbody.AllowEmpty()}, want: textbody.ErrJSON},
		{name: "a raw byte ff", body: "{\"summary\":\"" + secretMark + "\xff\"}", opts: nil, want: textbody.ErrText},
		{name: "a cut sequence", body: "{\"summary\":\"" + secretMark + "\xc3\"}", opts: nil, want: textbody.ErrText},
		{
			name: "a lone high surrogate",
			body: `{"summary":"` + secretMark + `\ud800"}`,
			opts: nil,
			want: textbody.ErrText,
		},
		{
			name: "a lone low surrogate",
			body: `{"summary":"` + secretMark + `\udc00"}`,
			opts: nil,
			want: textbody.ErrText,
		},
		{
			name: "a lone surrogate with unknown fields allowed",
			body: `{"x":"` + secretMark + `\ud800"}`, opts: []textbody.Option{textbody.AllowUnknownFields()},
			want: textbody.ErrText,
		},
		{
			name: "an unknown field",
			body: `{"summary":"a","owner":"` + secretMark + `"}`,
			opts: nil,
			want: textbody.ErrJSON,
		},
		{name: "a syntax fault", body: `{"summary":"` + secretMark + `",}`, opts: nil, want: textbody.ErrJSON},
		{name: "a field of the wrong type", body: `{"summary":7}`, opts: nil, want: textbody.ErrJSON},
		{
			name: "a second value",
			body: `{"summary":"a"} {"summary":"` + secretMark + `"}`,
			opts: nil,
			want: textbody.ErrJSON,
		},
		{name: "a closing brace after the value", body: `{"summary":"a"} }`, opts: nil, want: textbody.ErrJSON},
		{name: "a value that is not an object", body: `"` + secretMark + `"`, opts: nil, want: textbody.ErrJSON},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var v note

			err := textbody.Unmarshal([]byte(tc.body), &v, tc.opts...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Unmarshal(%q) = %v, want %v", tc.body, err, tc.want)
			}

			if strings.Contains(err.Error(), secretMark) {
				t.Fatalf("Unmarshal(%q) = %v, want an error with no part of the body", tc.body, err)
			}
		})
	}
}

// TestUnmarshalNilTarget proves that a decode into nil fails closed.
func TestUnmarshalNilTarget(t *testing.T) {
	t.Parallel()

	if err := textbody.Unmarshal([]byte(`{"summary":"a"}`), nil); !errors.Is(err, textbody.ErrJSON) {
		t.Fatalf("Unmarshal into nil = %v, want ErrJSON", err)
	}
}

// TestUnmarshalAccepts proves the bodies that Unmarshal accepts, with and
// without the options.
func TestUnmarshalAccepts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		opts []textbody.Option
		want string
	}{
		{name: "plain text", body: `{"summary":"plain"}`, opts: nil, want: "plain"},
		{name: "a surrogate pair escape", body: `{"summary":"pager \ud83d\udcdf"}`, opts: nil, want: "pager \U0001F4DF"},
		{name: "spaces after the value", body: "{\"summary\":\"a\"}\n", opts: nil, want: "a"},
		{name: "an empty body that is allowed", body: ``, opts: []textbody.Option{textbody.AllowEmpty()}, want: "kept"},
		{name: "JSON whitespace that is allowed", body: " \t\r\n", opts: []textbody.Option{textbody.AllowEmpty()}, want: "kept"},
		{
			name: "an unknown field that is allowed",
			body: `{"summary":"a","owner":"b"}`, opts: []textbody.Option{textbody.AllowUnknownFields()}, want: "a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := note{Summary: "kept"}

			if err := textbody.Unmarshal([]byte(tc.body), &v, tc.opts...); err != nil || v.Summary != tc.want {
				t.Fatalf("Unmarshal(%q) = %q, %v, want %q", tc.body, v.Summary, err, tc.want)
			}
		})
	}
}

// TestDecode proves that Decode reads under the limit and then refuses as
// Unmarshal does.
func TestDecode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want error
	}{
		{name: "a valid body", body: `{"summary":"a"}`, want: nil},
		{name: "over the limit", body: `{"summary":"` + strings.Repeat("a", 64) + `"}`, want: textbody.ErrTooLarge},
		{name: "a lone surrogate", body: `{"summary":"\ud800"}`, want: textbody.ErrText},
		{name: "an unknown field", body: `{"owner":"b"}`, want: textbody.ErrJSON},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tc.body))

			var v note

			if err := textbody.Decode(httptest.NewRecorder(), r, 64, &v); !errors.Is(err, tc.want) {
				t.Fatalf("Decode(%q) = %v, want %v", tc.body, err, tc.want)
			}
		})
	}
}
