package localuser_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestErrorsAreDistinct checks that no sentinel matches another, because an
// adapter maps each one to its own error code.
func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []error{
		localuser.ErrNotFound, localuser.ErrAlreadyExists, localuser.ErrInvalid,
		localuser.ErrPasswordTooShort, localuser.ErrPasswordTooLong, localuser.ErrPasswordForm,
		localuser.ErrPasswordBlocked, localuser.ErrWrongPassword, localuser.ErrSignInFailed,
		localuser.ErrCurrentPasswordRequired, localuser.ErrLastCredential, localuser.ErrDemoOnly,
		localuser.ErrDemoFixed, localuser.ErrUnavailable,
	}

	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}

// TestDefaultHashParamsAreInBounds checks that the defaults are inside the
// bounds that a stored hash must meet.
func TestDefaultHashParamsAreInBounds(t *testing.T) {
	t.Parallel()

	p := localuser.DefaultHashParams()

	if p.Algorithm != localuser.AlgorithmArgon2id {
		t.Errorf("algorithm = %q, want %q", p.Algorithm, localuser.AlgorithmArgon2id)
	}

	checks := []struct {
		name          string
		value, lo, hi uint32
	}{
		{name: "memory", value: p.MemoryKiB, lo: localuser.MinMemoryKiB, hi: localuser.MaxMemoryKiB},
		{name: "iterations", value: p.Iterations, lo: localuser.MinIterations, hi: localuser.MaxIterations},
		{name: "parallelism", value: uint32(p.Parallelism), lo: 1, hi: localuser.MaxParallelism},
		{name: "salt", value: p.SaltBytes, lo: localuser.MinSaltBytes, hi: localuser.MaxSaltBytes},
		{name: "key", value: p.KeyBytes, lo: localuser.MinKeyBytes, hi: localuser.MaxKeyBytes},
	}

	for _, c := range checks {
		if c.value < c.lo || c.value > c.hi {
			t.Errorf("%s = %d, want %d to %d", c.name, c.value, c.lo, c.hi)
		}
	}
}

// TestPasswordLengthBounds checks that the default minimum length is inside
// the bounds of its setting and below the maximum length.
func TestPasswordLengthBounds(t *testing.T) {
	t.Parallel()

	if localuser.DefaultMinLength < localuser.MinLengthFloor ||
		localuser.DefaultMinLength > localuser.MinLengthCeiling {
		t.Errorf("default minimum %d is outside %d to %d",
			localuser.DefaultMinLength, localuser.MinLengthFloor, localuser.MinLengthCeiling)
	}

	if localuser.MinLengthCeiling >= localuser.MaxLength {
		t.Errorf(
			"minimum-length ceiling %d is not below the maximum %d",
			localuser.MinLengthCeiling,
			localuser.MaxLength,
		)
	}

	if localuser.DefaultResetLifetime < localuser.MinResetLifetime ||
		localuser.DefaultResetLifetime > localuser.MaxResetLifetime {
		t.Errorf("default reset lifetime %s is outside its bounds", localuser.DefaultResetLifetime)
	}
}

// TestUserNamePattern checks the user name pattern against names that it
// must take and names that it must refuse.
func TestUserNamePattern(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(localuser.UserNamePattern)

	tests := []struct {
		name string
		want bool
	}{
		{name: "alice", want: true},
		{name: "a.b_c-1", want: true},
		{name: "0", want: true},
		{name: "", want: false},
		{name: "Alice", want: false},
		{name: ".alice", want: false},
		{name: "al ice", want: false},
		{name: "alice\n", want: false},
		{name: "dev:demo", want: false},
		{name: "ålice", want: false},
		{name: strings.Repeat("a", localuser.UserNameMaxLength), want: true},
		{name: strings.Repeat("a", localuser.UserNameMaxLength+1), want: false},
	}

	for _, tt := range tests {
		if got := re.MatchString(tt.name); got != tt.want {
			t.Errorf("match(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestDemoSubjectIsADevSubject checks that a production server refuses the
// demo person, because its subject has the prefix of the dev sign-in.
func TestDemoSubjectIsADevSubject(t *testing.T) {
	t.Parallel()

	if !principal.IsDevSubject(localuser.DemoSubject) {
		t.Fatalf("%q is not a dev subject", localuser.DemoSubject)
	}

	if principal.ValidateSubject(localuser.DemoSubject) == nil {
		t.Fatalf("ValidateSubject accepts %q; the API must not make the demo person", localuser.DemoSubject)
	}
}
