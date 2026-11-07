package principal_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
)

// The text check sees hostile input directly, as if the HTTP adapter and
// every other layer had failed.
func TestGroupValidateRefusesHiddenRunesInDescription(t *testing.T) {
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

			g := principal.Group{
				CreatedAt:   time.Time{},
				UpdatedAt:   time.Time{},
				ID:          "",
				Name:        "ops",
				Description: "a" + string(tc.r) + "b",
				Source:      principal.SourceLocal,
				MemberCount: 0,
			}

			if err := g.Validate(); !errors.Is(err, principal.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}
