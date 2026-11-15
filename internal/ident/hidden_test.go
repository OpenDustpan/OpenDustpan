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

func TestIsHiddenRuneExcept(t *testing.T) {
	t.Parallel()

	var (
		noneAllowed       []rune
		newlineTabAllowed = []rune{'\n', '\t'}
		lineBreaksAllowed = []rune{'\n', '\r', '\t'}
	)

	tests := []struct {
		name    string
		r       rune
		allowed []rune
		want    bool
	}{
		{"NUL, no allowed rune", 0x00, noneAllowed, true},
		{"newline, no allowed rune", '\n', noneAllowed, true},
		{"carriage return, no allowed rune", '\r', noneAllowed, true},
		{"tab, no allowed rune", '\t', noneAllowed, true},
		{"right-to-left override, no allowed rune", 0x202e, noneAllowed, true},
		{"line separator, no allowed rune", 0x2028, noneAllowed, true},
		{"paragraph separator, no allowed rune", 0x2029, noneAllowed, true},
		{"zero-width joiner, no allowed rune", 0x200d, noneAllowed, true},
		{"letter, no allowed rune", 'a', noneAllowed, false},

		{"NUL, newline and tab allowed", 0x00, newlineTabAllowed, true},
		{"newline, newline and tab allowed", '\n', newlineTabAllowed, false},
		{"carriage return, newline and tab allowed", '\r', newlineTabAllowed, true},
		{"tab, newline and tab allowed", '\t', newlineTabAllowed, false},
		{"right-to-left override, newline and tab allowed", 0x202e, newlineTabAllowed, true},
		{"line separator, newline and tab allowed", 0x2028, newlineTabAllowed, true},
		{"paragraph separator, newline and tab allowed", 0x2029, newlineTabAllowed, true},
		{"zero-width joiner, newline and tab allowed", 0x200d, newlineTabAllowed, true},
		{"letter, newline and tab allowed", 'a', newlineTabAllowed, false},

		{"NUL, line breaks and tab allowed", 0x00, lineBreaksAllowed, true},
		{"newline, line breaks and tab allowed", '\n', lineBreaksAllowed, false},
		{"carriage return, line breaks and tab allowed", '\r', lineBreaksAllowed, false},
		{"tab, line breaks and tab allowed", '\t', lineBreaksAllowed, false},
		{"right-to-left override, line breaks and tab allowed", 0x202e, lineBreaksAllowed, true},
		{"line separator, line breaks and tab allowed", 0x2028, lineBreaksAllowed, true},
		{"paragraph separator, line breaks and tab allowed", 0x2029, lineBreaksAllowed, true},
		{"zero-width joiner, line breaks and tab allowed", 0x200d, lineBreaksAllowed, true},
		{"letter, line breaks and tab allowed", 'a', lineBreaksAllowed, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ident.IsHiddenRuneExcept(tc.r, tc.allowed...); got != tc.want {
				t.Errorf("IsHiddenRuneExcept(%U, %U) = %v, want %v", tc.r, tc.allowed, got, tc.want)
			}
		})
	}
}

func TestIsControlRune(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    rune
		want bool
	}{
		{"NUL", 0x00, true},
		{"newline", '\n', true},
		{"carriage return", '\r', true},
		{"tab", '\t', true},
		{"format right-to-left override", 0x202e, false},
		{"line separator", 0x2028, false},
		{"paragraph separator", 0x2029, false},
		{"zero-width joiner", 0x200d, false},
		{"letter", 'a', false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ident.IsControlRune(tc.r); got != tc.want {
				t.Errorf("IsControlRune(%U) = %v, want %v", tc.r, got, tc.want)
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
