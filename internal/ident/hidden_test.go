package ident_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
)

func TestHasHidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    rune
		want bool
	}{
		{"letter", 'a', false},
		{"space", ' ', false},
		{"non-ASCII letter", 0xeb, false},
		{"control", 0x00, true},
		{"newline", '\n', true},
		{"format right-to-left override", 0x202e, true},
		{"zero-width space", 0x200b, true},
		{"line separator", 0x2028, true},
		{"paragraph separator", 0x2029, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ident.IsHiddenRune(tc.r); got != tc.want {
				t.Errorf("IsHiddenRune(%U) = %v, want %v", tc.r, got, tc.want)
			}

			if got := ident.HasHidden("a" + string(tc.r) + "b"); got != tc.want {
				t.Errorf("HasHidden with %U = %v, want %v", tc.r, got, tc.want)
			}
		})
	}
}

func TestFirstHidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		s     string
		want  rune
		found bool
	}{
		{"empty", "", 0, false},
		{"plain", "alpha", 0, false},
		{"line separator", "a\u2028b", 0x2028, true},
		{"first of two", "a\u202eb\x07", 0x202e, true},
		{"invalid UTF-8 is not hidden", "a\xffb", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, found := ident.FirstHidden(tc.s)
			if got != tc.want || found != tc.found {
				t.Errorf("FirstHidden(%q) = %U, %v, want %U, %v", tc.s, got, found, tc.want, tc.found)
			}
		})
	}
}

func TestIsHiddenRuneExcept(t *testing.T) {
	t.Parallel()

	tests := []struct {
		r    rune
		want map[string]bool
	}{
		{'a', map[string]bool{"none": false, "breaks": false, "returns": false}},
		{0x00, map[string]bool{"none": true, "breaks": true, "returns": true}},
		{'\n', map[string]bool{"none": true, "breaks": false, "returns": false}},
		{'\r', map[string]bool{"none": true, "breaks": true, "returns": false}},
		{'\t', map[string]bool{"none": true, "breaks": false, "returns": false}},
		{0x202e, map[string]bool{"none": true, "breaks": true, "returns": true}},
		{0x2028, map[string]bool{"none": true, "breaks": true, "returns": true}},
		{0x2029, map[string]bool{"none": true, "breaks": true, "returns": true}},
		{0x200d, map[string]bool{"none": true, "breaks": true, "returns": true}},
	}

	allowed := map[string][]rune{
		"none":    {},
		"breaks":  {'\n', '\t'},
		"returns": {'\n', '\r', '\t'},
	}

	for _, tc := range tests {
		for name, allow := range allowed {
			if got := ident.IsHiddenRuneExcept(tc.r, allow...); got != tc.want[name] {
				t.Errorf("IsHiddenRuneExcept(%U, %q) = %v, want %v", tc.r, allow, got, tc.want[name])
			}
		}
	}
}

func TestIsControlRune(t *testing.T) {
	t.Parallel()

	tests := []struct {
		r    rune
		want bool
	}{
		{'a', false},
		{0x00, true},
		{'\n', true},
		{'\r', true},
		{'\t', true},
		{0x202e, false},
		{0x2028, false},
		{0x2029, false},
		{0x200d, false},
	}

	for _, tc := range tests {
		if got := ident.IsControlRune(tc.r); got != tc.want {
			t.Errorf("IsControlRune(%U) = %v, want %v", tc.r, got, tc.want)
		}
	}
}
