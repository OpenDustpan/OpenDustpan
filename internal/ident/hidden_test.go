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
