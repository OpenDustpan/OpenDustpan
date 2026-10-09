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
