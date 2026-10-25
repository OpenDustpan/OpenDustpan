package identity_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// errStore is the error of a store that kept an Authorizer from a decision.
var errStore = errors.New("store down")

// TestDeniedError proves that a denial with a cause is a denial, keeps the
// error that caused it, and gives its cause to errors.As.
func TestDeniedError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err   *identity.DeniedError
		inner error
		want  string
	}{
		{
			err:   &identity.DeniedError{Err: nil, Cause: identity.DenyNoGrant, Rule: "", Action: identity.ActionUnitRunPlan},
			inner: nil,
			want:  "permission denied: unit:run-plan (no-grant)",
		},
		{
			err: &identity.DeniedError{
				Err: nil, Cause: identity.DenyCodeRule, Rule: "server-subject-guard", Action: identity.ActionUnitRunPlan,
			},
			inner: nil,
			want:  "permission denied: unit:run-plan (code-rule): rule server-subject-guard",
		},
		{
			err: &identity.DeniedError{
				Err: errStore, Cause: identity.DenyFailClosed, Rule: "", Action: identity.ActionUnitRunPlan,
			},
			inner: errStore,
			want:  "permission denied: unit:run-plan (fail-closed): store down",
		},
	} {
		t.Run(string(tc.err.Cause), func(t *testing.T) {
			t.Parallel()

			var err error = tc.err
			if !errors.Is(err, identity.ErrDenied) {
				t.Errorf("errors.Is(%v, ErrDenied) = false, want true", err)
			}

			if tc.inner != nil && !errors.Is(err, tc.inner) {
				t.Errorf("errors.Is(%v, %v) = false, want true", err, tc.inner)
			}

			var got *identity.DeniedError
			if !errors.As(err, &got) || got.Cause != tc.err.Cause {
				t.Errorf("errors.As(%v) = %v, want the cause %q", err, got, tc.err.Cause)
			}

			if err.Error() != tc.want {
				t.Errorf("Error() = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}
