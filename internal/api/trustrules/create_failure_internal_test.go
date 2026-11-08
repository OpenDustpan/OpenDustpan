package trustrules

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errStoreDown is an error that is not a denial.
var errStoreDown = errors.New("the store is not available")

// TestCreateFailureReasonNamesOnlyAMissingHold checks that only the denial of
// principal:manage-tokens on the principal names that action in the reason. A
// denial without that mark, for example a reach check that cannot run, names
// no action.
func TestCreateFailureReasonNamesOnlyAMissingHold(t *testing.T) {
	t.Parallel()

	const principalID = "prn-worker"

	tests := []struct {
		name       string
		err        error
		wantReason string
		wantOK     bool
	}{
		{
			name:       "a denial of principal:manage-tokens",
			err:        fmt.Errorf("make: %w: %w", trustrulesvc.ErrManageTokensDenied, identity.ErrDenied),
			wantReason: "outside the reach of the caller: principal:manage-tokens on principal:" + principalID,
			wantOK:     true,
		},
		{
			name:       "a denial without the mark",
			err:        fmt.Errorf("make: %w", identity.ErrDenied),
			wantReason: "",
			wantOK:     false,
		},
		{
			name:       "an error that is not a denial",
			err:        errStoreDown,
			wantReason: "",
			wantOK:     false,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reason, ok := createFailureReason(tc.err, principalID)
			if reason != tc.wantReason || ok != tc.wantOK {
				t.Fatalf("reason = %q, %t; want %q, %t", reason, ok, tc.wantReason, tc.wantOK)
			}
		})
	}
}
