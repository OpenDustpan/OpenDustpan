package inventory_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
)

func TestValidLabelKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		ok   bool
	}{
		{"letters", "team", true},
		{"all allowed characters", "a.b_c:d/e-F9", true},
		{"128 bytes", strings.Repeat("k", inventory.MaxLabelKeyLen), true},
		{"129 bytes", strings.Repeat("k", inventory.MaxLabelKeyLen+1), false},
		{"empty", "", false},
		{"space", "a b", false},
		{"equals sign", "a=b", false},
		{"newline", "a\nb", false},
		{"NUL", "a\x00b", false},
		{"non ASCII letter", "tëam", false},
		{"percent", "a%2fb", false},
		{"wildcard", "a*", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := inventory.ValidLabelKey(tc.key); got != tc.ok {
				t.Fatalf("ValidLabelKey(%q) = %v, want %v", tc.key, got, tc.ok)
			}
		})
	}
}

func TestValidLabelValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"empty", "", true},
		{"text with spaces", "payments team", true},
		{"non ASCII", "équipe", true},
		{"256 characters", strings.Repeat("é", inventory.MaxLabelValueLen), true},
		{"257 characters", strings.Repeat("é", inventory.MaxLabelValueLen+1), false},
		{"newline", "a\nb", false},
		{"tab", "a\tb", false},
		{"NUL", "a\x00", false},
		{"DEL", "a\x7f", false},
		{"C1 control", "a\u0085", false},
		{"invalid UTF-8", "a\xffb", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := inventory.ValidLabelValue(tc.value); got != tc.ok {
				t.Fatalf("ValidLabelValue(%q) = %v, want %v", tc.value, got, tc.ok)
			}
		})
	}
}

func TestHasControl(t *testing.T) {
	t.Parallel()

	if inventory.HasControl("plain text é") {
		t.Fatal("HasControl(plain) = true")
	}

	for _, s := range []string{"\n", "a\rb", "\x00", "\x1b[0m", "\u0085"} {
		if !inventory.HasControl(s) {
			t.Fatalf("HasControl(%q) = false", s)
		}
	}
}

func TestQueryLimitsAreTheLimitsOfTheUnitList(t *testing.T) {
	t.Parallel()

	if inventory.MaxLabelFilters != 10 || inventory.MaxReferenceFilters != 20 {
		t.Fatalf("limits = %d labels, %d references; the unit list takes 10 and 20",
			inventory.MaxLabelFilters, inventory.MaxReferenceFilters)
	}
}
