package dustpan

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freshstep"
)

// TestApproveOffFreshPolicy proves that the setting off lets an approval of a
// change to off pass with no MFA step, and that the setting on, or no policy,
// keeps the refusal.
func TestApproveOffFreshPolicy(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	for name, tc := range map[string]struct {
		policy  freshstep.Policy
		wantErr bool
	}{
		"setting off": {policy: off, wantErr: false},
		"setting on":  {policy: on, wantErr: true},
		"nil policy":  {policy: nil, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			chain := newTestAuditChain(t, audit.ChainOff)
			chain.freshness = stepFreshness{}
			chain.freshPolicy = tc.policy

			err := chain.Approve(t.Context(), approver())
			if tc.wantErr {
				if !errors.Is(err, audit.ErrChainStepRequired) || !errors.Is(err, errNoStep) {
					t.Fatalf("approve = %v, want ErrChainStepRequired", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("approve with the setting off: %v", err)
			}
		})
	}
}
