package ident_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
)

func TestValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"acme", true},
		{"Acme_Corp-1.0", true},
		{"", false},
		{".hidden", false},
		{"-flag", false},
		{"a/b", false},
		{"a b", false},
		{"ünicode", false},
		{strings.Repeat("a", 100), true},
		{strings.Repeat("a", 101), false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := ident.Valid(tc.in, 100); got != tc.want {
				t.Fatalf("Valid(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidSlug(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"letters", "web", true},
		{"letters digits and dash", "web-2-prod", true},
		{"one byte", "a", true},
		{"max length", strings.Repeat("a", 63), true},
		{"over max length", strings.Repeat("a", 64), false},
		{"empty", "", false},
		{"digit first", "1web", false},
		{"dash first", "-web", false},
		{"uppercase", "Web", false},
		{"uppercase inside", "weB", false},
		{"underscore", "web_prod", false},
		{"dot", "web.prod", false},
		{"space", "web prod", false},
		{"non-ASCII", "wéb", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ident.ValidSlug(tc.in, 63); got != tc.want {
				t.Fatalf("ValidSlug(%q, 63) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	if ident.ValidSlug("abc", 2) || !ident.ValidSlug("ab", 2) {
		t.Fatal("ValidSlug does not apply maxLen")
	}
}

func TestNewIDIsPrefixedLowercaseAndUnique(t *testing.T) {
	t.Parallel()

	a, b := ident.NewID("ws-"), ident.NewID("ws-")
	if a == b {
		t.Fatal("two IDs are equal")
	}

	if !strings.HasPrefix(a, "ws-") || len(a) != len("ws-")+20 || strings.ToLower(a) != a {
		t.Errorf("NewID(\"ws-\") = %q, want ws- and 20 lowercase characters", a)
	}
}

// TestEcho proves that Echo quotes a short value as it is, and that it cuts a
// value of more than MaxEcho characters at a character boundary and marks the
// cut with the full count.
func TestEcho(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{in: "", want: `""`},
		{in: "-x", want: `"-x"`},
		{in: "a\nb", want: `"a\nb"`},
		{in: strings.Repeat("a", ident.MaxEcho), want: `"` + strings.Repeat("a", ident.MaxEcho) + `"`},
		{
			in:   strings.Repeat("a", 300),
			want: `"` + strings.Repeat("a", ident.MaxEcho) + `"... (cut, 300 characters)`,
		},
		{
			in:   strings.Repeat("é", 41),
			want: `"` + strings.Repeat("é", ident.MaxEcho) + `"... (cut, 41 characters)`,
		},
	} {
		if got := ident.Echo(tc.in); got != tc.want {
			t.Errorf("Echo(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestCheck proves that Check accepts what Valid accepts, and that it refuses
// an empty value and a value that breaks the rule with one *RuleError text:
// the bounded echo and the rule.
func TestCheck(t *testing.T) {
	t.Parallel()

	if err := ident.Check("acme", 100); err != nil {
		t.Fatalf("Check(acme) = %v, want nil", err)
	}

	const rule = "use 1 to 100 ASCII letters, digits, -, _ and ., not starting with - or ."

	for _, tc := range []struct{ in, want string }{
		{in: "", want: `"": ` + rule},
		{in: "-x", want: `"-x": ` + rule},
		{in: "a b", want: `"a b": ` + rule},
		{
			in:   strings.Repeat("a", 101),
			want: `"` + strings.Repeat("a", ident.MaxEcho) + `"... (cut, 101 characters): ` + rule,
		},
	} {
		err := ident.Check(tc.in, 100)
		if err == nil {
			t.Fatalf("Check(%q) = nil, want %s", tc.in, tc.want)
		}

		var ruleErr *ident.RuleError
		if !errors.As(err, &ruleErr) || err.Error() != tc.want {
			t.Errorf("Check(%q) = %v, want %s", tc.in, err, tc.want)
		}
	}
}
