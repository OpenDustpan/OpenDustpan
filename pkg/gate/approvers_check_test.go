package gate_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

// TestCheckUserNameRefusesANameThatIsNotUTF8 checks the user name check alone,
// so a caller that skips Approvers.Validate cannot store a name that is not
// UTF-8.
func TestCheckUserNameRefusesANameThatIsNotUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		user     string
		wantText string
	}{
		{"a valid name", "alice", ""},
		{"a byte that is not UTF-8", "\xff", "not UTF-8"},
		{"a truncated sequence", "ab\xc3", "not UTF-8"},
		{"a control character", "a\x07b", "control or format character"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := gate.CheckUserNameForTest(tt.user)

			if tt.wantText == "" {
				if err != nil {
					t.Fatalf("CheckUserNameForTest(%q) = %v, want nil", tt.user, err)
				}

				return
			}

			if !errors.Is(err, gate.ErrApprovers) || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("CheckUserNameForTest(%q) = %v, want ErrApprovers with %q", tt.user, err, tt.wantText)
			}
		})
	}
}
