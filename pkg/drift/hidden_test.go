package drift_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/drift"
)

// The address check sees hostile input directly, as if the HTTP adapter and
// every other layer had failed.
func TestScopeValidateRefusesHiddenRunes(t *testing.T) {
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

			scope := drift.Scope{
				Namespace: "acme",
				Kind:      drift.ScopeCollection,
				Name:      "a" + string(tc.r) + "b",
			}

			mustInvalid(t, scope.Validate())
		})
	}
}
