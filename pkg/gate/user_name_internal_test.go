package gate

import (
	"errors"
	"testing"
)

// checkUserName sees hostile input directly, as if the Validate method that
// runs before it had failed. Invalid UTF-8 is the case that ident.HasHidden
// does not refuse, so this check has to.
func TestCheckUserNameRefusesHostileNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		user string
	}{
		{"invalid UTF-8", "a\xffb"},
		{"line separator", "a\u2028b"},
		{"paragraph separator", "a\u2029b"},
		{"right-to-left override", "a\u202eb"},
		{"control", "a\x07b"},
		{"leading white space", " ab"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkUserName(tc.user)

			got, isApprovers := errors.AsType[*ApproversError](err)
			if !isApprovers || got.Reason != ApproversUserInvalid {
				t.Fatalf("checkUserName(%q) = %v, want reason ApproversUserInvalid", tc.user, err)
			}
		})
	}

	if err := checkUserName("alice@example.com"); err != nil {
		t.Fatalf("checkUserName(valid) = %v, want nil", err)
	}
}
