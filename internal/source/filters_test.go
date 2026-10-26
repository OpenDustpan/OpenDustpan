package source_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/source"
)

func TestEncodeFilterIsCanonical(t *testing.T) {
	t.Parallel()

	empty, err := source.EncodeFilter(
		source.Filter{Submodules: nil, LargeFiles: nil, FailOnDenied: false, ProtectedBranches: nil},
	)
	if err != nil {
		t.Fatalf("EncodeFilter(empty): %v", err)
	}

	const wantEmpty = `{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`
	if empty != wantEmpty {
		t.Errorf("EncodeFilter(empty) = %s, want %s", empty, wantEmpty)
	}

	full, err := source.EncodeFilter(source.Filter{
		Submodules:        []source.URLPattern{"https://git.example.com/platform/*"},
		LargeFiles:        []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied:      true,
		ProtectedBranches: []string{"main", "release/1"},
	})
	if err != nil {
		t.Fatalf("EncodeFilter(full): %v", err)
	}

	const wantFull = `{"submodules":["https://git.example.com/platform/*"],` +
		`"largeFiles":["https://git.example.com/acme/infra"],"failOnDenied":true,` +
		`"protectedBranches":["main","release/1"]}`
	if full != wantFull {
		t.Errorf("EncodeFilter(full) = %s, want %s", full, wantFull)
	}
}

func TestEncodeFilterRefusesAnInvalidFilter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		f    source.Filter
	}{
		{
			"a bad pattern",
			source.Filter{
				Submodules:        []source.URLPattern{"git@host:a/b"},
				LargeFiles:        nil,
				FailOnDenied:      false,
				ProtectedBranches: nil,
			},
		},
		{
			"a glob branch",
			source.Filter{
				Submodules:        nil,
				LargeFiles:        nil,
				FailOnDenied:      false,
				ProtectedBranches: []string{"release/*"},
			},
		},
		{
			"a branch twice",
			source.Filter{
				Submodules:        nil,
				LargeFiles:        nil,
				FailOnDenied:      false,
				ProtectedBranches: []string{"main", "main"},
			},
		},
	}

	for _, tc := range cases {
		if _, err := source.EncodeFilter(tc.f); !errors.Is(err, source.ErrInvalid) {
			t.Errorf("%s: EncodeFilter = %v, want ErrInvalid", tc.name, err)
		}
	}
}

func TestDecodeFilterRoundTrip(t *testing.T) {
	t.Parallel()

	in := source.Filter{
		Submodules:        []source.URLPattern{"https://git.example.com/platform/*"},
		LargeFiles:        []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied:      true,
		ProtectedBranches: []string{"main"},
	}

	text, err := source.EncodeFilter(in)
	if err != nil {
		t.Fatalf("EncodeFilter: %v", err)
	}

	out, err := source.DecodeFilter(text)
	if err != nil {
		t.Fatalf("DecodeFilter(%s): %v", text, err)
	}

	again, err := source.EncodeFilter(out)
	if err != nil || again != text {
		t.Errorf("a round trip gives %q, %v, want %q", again, err, text)
	}

	none, err := source.DecodeFilter("")
	if err != nil || none.FailOnDenied || len(none.Submodules)+len(none.LargeFiles)+len(none.ProtectedBranches) != 0 {
		t.Errorf("DecodeFilter(empty) = %+v, %v, want the empty filter", none, err)
	}
}

func TestDecodeFilterRefusesHostileText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want error
	}{
		{"not JSON", `not json`, source.ErrInvalid},
		{"an array", `[]`, source.ErrInvalid},
		{"null", `null`, source.ErrInvalid},
		{"an unknown key", `{"submodules":[],"extra":1}`, source.ErrInvalid},
		{"a wrong type", `{"failOnDenied":"yes"}`, source.ErrInvalid},
		{"a second document", `{} {}`, source.ErrInvalid},
		{"data after the document", `{}x`, source.ErrInvalid},
		{"a bad pattern", `{"submodules":["http://git.example.com/a/b"]}`, source.ErrInvalid},
		{"a glob branch", `{"protectedBranches":["a/*"]}`, source.ErrInvalid},
		{"a branch with a newline", `{"protectedBranches":["a\nb"]}`, source.ErrInvalid},
		{"too long", `{"protectedBranches":["` + strings.Repeat("a", source.MaxFilterJSON) + `"]}`, source.ErrTooLarge},
	}

	for _, tc := range cases {
		f, err := source.DecodeFilter(tc.text)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: DecodeFilter = %v, want %v", tc.name, err, tc.want)
		}

		if err != nil && strings.Contains(err.Error(), "git.example.com") {
			t.Errorf("%s: the error holds the text of the filter: %v", tc.name, err)
		}

		if err != nil && (f.FailOnDenied || len(f.Submodules)+len(f.LargeFiles)+len(f.ProtectedBranches) != 0) {
			t.Errorf("%s: DecodeFilter gives a value %+v with the error", tc.name, f)
		}
	}
}
