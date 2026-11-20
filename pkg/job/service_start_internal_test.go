package job

import (
	"errors"
	"testing"
)

// TestCheckUnitNameRefusesHiddenRunes sends each hidden rune and bytes that
// are not valid UTF-8 directly to the name check of Start, as if
// identity.UnitObject and the handler accepted the name.
func TestCheckUnitNameRefusesHiddenRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		unit string
	}{
		{"tab", "a\tb"},
		{"line feed", "a\nb"},
		{"carriage return", "a\rb"},
		{"NUL", "a\x00b"},
		{"right-to-left override U+202E", "a" + string(rune(0x202e)) + "b"},
		{"left-to-right isolate U+2066", "a" + string(rune(0x2066)) + "b"},
		{"line separator U+2028", "a" + string(rune(0x2028)) + "b"},
		{"zero width space U+200B", "a" + string(rune(0x200b)) + "b"},
		{"invalid UTF-8", "a\xffb"},
	}

	for _, clean := range []string{"network", "web/prod", "web/équipe"} {
		if err := checkUnitName(clean); err != nil {
			t.Errorf("checkUnitName(%q) = %v, want nil", clean, err)
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := checkUnitName(tt.unit); !errors.Is(err, ErrInvalid) {
				t.Errorf("checkUnitName(%q) = %v, want ErrInvalid", tt.unit, err)
			}
		})
	}
}
