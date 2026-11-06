package mfa_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/mfa"
)

// TestErrorsAreDistinct checks that no sentinel matches another, because an
// adapter maps each one to its own error code.
func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []error{
		mfa.ErrNotFound, mfa.ErrAlreadyExists, mfa.ErrInvalid, mfa.ErrLimit,
		mfa.ErrCounterRegressed, mfa.ErrSuspended, mfa.ErrVoid, mfa.ErrEnrollmentRequired,
		mfa.ErrLastCredential, mfa.ErrUnavailable, mfa.ErrVerification, mfa.ErrWrongPerson,
	}

	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}

// TestDefaultPolicyIsOff checks that built-in MFA is off by default and that
// each value of the default is the strict reading of off.
func TestDefaultPolicyIsOff(t *testing.T) {
	t.Parallel()

	p := mfa.DefaultPolicy()

	if p.Enabled || p.RequireForAll || p.RequireForSecretReveal {
		t.Errorf("default policy = %+v, want every switch off", p)
	}

	if p.EnrollmentGraceDays != 0 {
		t.Errorf("grace = %d, want 0", p.EnrollmentGraceDays)
	}

	if len(p.AllowedAAGUIDs) != 0 || p.AllowedAAGUIDsScope != mfa.AAGUIDScopeNone {
		t.Errorf("allowlist = %v %q, want none", p.AllowedAAGUIDs, p.AllowedAAGUIDsScope)
	}
}
