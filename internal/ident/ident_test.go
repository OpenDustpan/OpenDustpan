package ident_test

import (
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
