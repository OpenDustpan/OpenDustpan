package accesstoken_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
)

// ValidateName sees hostile input directly, as if the HTTP adapter and every
// other layer had failed.
func TestValidateNameRefusesHiddenRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    rune
	}{
		{"control", 0x07},
		{"format right-to-left override", 0x202e},
		{"line separator", 0x2028},
		{"paragraph separator", 0x2029},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text := "a" + string(tc.r) + "b"

			if err := accesstoken.ValidateName(text); !errors.Is(err, accesstoken.ErrInvalid) {
				t.Fatalf("ValidateName(%q) = %v, want ErrInvalid", text, err)
			}
		})
	}
}
