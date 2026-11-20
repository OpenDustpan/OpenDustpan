package trustrule_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// TestValidateSpiffePatternWildcard sends each pattern with a * segment
// straight to the pattern check, as if every other layer had failed: the
// check refuses it with ErrSpiffeWildcard, and the error wraps ErrInvalid.
func TestValidateSpiffePatternWildcard(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{
		"spiffe://example.org/ns/acme/*",
		"spiffe://td/*",
		"spiffe://td/*/x/*",
	} {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()

			err := trustrule.ValidateSpiffePattern(pattern)
			if !errors.Is(err, trustrule.ErrSpiffeWildcard) {
				t.Fatalf("ValidateSpiffePattern(%q) = %v, want ErrSpiffeWildcard", pattern, err)
			}

			if !errors.Is(err, trustrule.ErrInvalid) {
				t.Errorf("ValidateSpiffePattern(%q) = %v, want an error that wraps ErrInvalid", pattern, err)
			}

			for _, want := range []string{"spiffePattern", "exact SPIFFE ID"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not hold %q", err, want)
				}
			}
		})
	}
}

// TestRuleValidateSpiffeWildcard sends a rule with a wildcard pattern to
// Rule.Validate, the check that the create, the update, the store and the
// restore all call.
func TestRuleValidateSpiffeWildcard(t *testing.T) {
	t.Parallel()

	r := validRule()
	r.SpiffeIDPattern = "spiffe://example.org/ns/acme/*"

	if err := r.Validate(); !errors.Is(err, trustrule.ErrSpiffeWildcard) {
		t.Fatalf("Validate = %v, want ErrSpiffeWildcard", err)
	}

	r.SpiffeIDPattern = "spiffe://example.org/ns/acme/sa/deploy"
	r.Bundle = "trb-0123456789abcdef0123456789abcdef"

	if err := r.Validate(); err != nil {
		t.Fatalf("Validate with an exact SPIFFE ID = %v, want nil", err)
	}
}
