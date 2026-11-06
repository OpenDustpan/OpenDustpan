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
		localuser.ErrNotFound,
		localuser.ErrAlreadyExists,
		localuser.ErrInvalid,
		localuser.ErrPasswordTooShort,
		localuser.ErrPasswordTooLong,
		localuser.ErrPasswordForm,
		localuser.ErrPasswordBlocked,
		localuser.ErrPasswordLettersAndDigits,
		localuser.ErrWrongPassword,
		localuser.ErrSignInFailed,
		localuser.ErrCurrentPasswordRequired,
		localuser.ErrLastCredential,
		localuser.ErrDemoOnly,
		localuser.ErrDemoFixed,
		localuser.ErrBreakGlass,
		localuser.ErrPasswordSignInOff,
		localuser.ErrUnavailable,
		localuser.ErrHashBusy,
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
		{name: "parallelism", value: uint32(p.Parallelism), lo: localuser.MinParallelism, hi: localuser.MaxParallelism},
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

// TestServiceRules names the rules of the local user service that no store
// can check. Each case skips until the service exists, so that the names are
// the contract that the service meets.
func TestServiceRules(t *testing.T) {
	t.Parallel()

	rules := []string{
		"a sign-in that gets no room in the hash memory budget answers 429 and counts no name failure",
		"the name failure counts after the hash gets its budget and before the compare",
		"a full sign-in clears the count of its own source prefix and the global count of the name",
		"an administrator unlock clears every prefix count and the global count of the name",
		"the global lock of a name lasts 30 minutes and doubles for each new global lock up to 24 hours",
		"the level of the global lock goes back to the start after 24 hours with no global lock",
		"a passkey sign-in reads no name lock and no factor lock",
		"a factor lock notifies the person and offers a password change",
		"an SSO change that changes the effective local password setting writes one audit entry with the cause",
		"an SSO change that turns the local password sign-in from off to on needs a fresh authentication",
		"the answer of an SSO change names the new effective local password setting",
		"next to an SSO provider a local password user needs a factor after the enrollment grace",
		"the counts clear after the commit of the session, and a failed clear leaves them",
		"the demo password is in no log record and no audit entry",
	}

	for _, rule := range rules {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()
			t.Skip("the local user service does not exist yet")
		})
	}
}

// TestReservationWorksOnce checks that a reservation calls the verify of its
// adapter at most once and its release at most once, and refuses a verify
// after the release, whatever the adapter does.
func TestReservationWorksOnce(t *testing.T) {
	t.Parallel()

	verifies, releases := 0, 0
	verify := func(string, localuser.HashParams) (localuser.Verdict, error) {
		verifies++

		return localuser.Verdict{NeedsRehash: false}, nil
	}
	release := func() { releases++ }

	if localuser.NewReservation(nil, release) != nil || localuser.NewReservation(verify, nil) != nil {
		t.Fatal("a reservation with a nil function is not nil")
	}

	r := localuser.NewReservation(verify, release)
	if r == nil {
		t.Fatal("NewReservation returned nil for two functions")
	}

	if _, err := r.Verify("pw", localuser.DefaultHashParams()); err != nil {
		t.Fatalf("first verify: %v", err)
	}

	if _, err := r.Verify("pw", localuser.DefaultHashParams()); !errors.Is(err, localuser.ErrInvalid) {
		t.Fatalf("second verify: err = %v, want ErrInvalid", err)
	}

	r.Release()
	r.Release()

	if verifies != 1 || releases != 1 {
		t.Fatalf("verifies = %d, releases = %d; want 1 and 1", verifies, releases)
	}

	unused := localuser.NewReservation(verify, release)
	if unused == nil {
		t.Fatal("NewReservation returned nil for two functions")
	}

	unused.Release()

	if _, err := unused.Verify("pw", localuser.DefaultHashParams()); !errors.Is(err, localuser.ErrInvalid) {
		t.Fatalf("verify after release: err = %v, want ErrInvalid", err)
	}

	if verifies != 1 {
		t.Fatalf("verifies after a refused verify = %d, want 1", verifies)
	}
}
