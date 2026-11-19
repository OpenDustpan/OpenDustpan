package localusersvc_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
)

// reasonCase is one reason of an administrator change and the error that
// the service must return for it. A nil want is a reason that the service
// accepts.
type reasonCase struct {
	want   error
	name   string
	reason string
}

// reasonCases returns the reasons at the limits of the audit log: the
// longest reason in one-byte, two-byte, three-byte and four-byte code
// points, one code point more, a reason that is longer than the old byte
// limit, an empty reason and hidden characters.
func reasonCases() []reasonCase {
	return []reasonCase{
		{name: "500 ASCII characters", reason: strings.Repeat("a", audit.MaxReasonLen), want: nil},
		{name: "500 two-byte characters", reason: strings.Repeat("é", audit.MaxReasonLen), want: nil},
		{name: "500 three-byte characters", reason: strings.Repeat("界", audit.MaxReasonLen), want: nil},
		{name: "500 four-byte characters", reason: strings.Repeat("😀", audit.MaxReasonLen), want: nil},
		{name: "501 ASCII characters", reason: strings.Repeat("a", audit.MaxReasonLen+1), want: audit.ErrInvalidReason},
		{
			name:   "501 two-byte characters",
			reason: strings.Repeat("é", audit.MaxReasonLen+1),
			want:   audit.ErrInvalidReason,
		},
		{name: "1025 ASCII characters", reason: strings.Repeat("a", 1025), want: audit.ErrInvalidReason},
		{name: "a bidirectional override", reason: "lost\u202epassword", want: audit.ErrInvalidReason},
		{name: "bytes that are not UTF-8", reason: "lost \xff password", want: audit.ErrInvalidReason},
		{name: "only space", reason: "   ", want: localuser.ErrReasonRequired},
		{name: "a line break", reason: "lost\nforged", want: localuser.ErrReasonRequired},
	}
}

// checkReason fails t when err is not the error that tc wants.
func checkReason(t *testing.T, op string, tc reasonCase, err error) {
	t.Helper()

	if tc.want == nil {
		if err != nil {
			t.Fatalf("%s with %s = %v, want no error", op, tc.name, err)
		}

		return
	}

	if !errors.Is(err, tc.want) {
		t.Fatalf("%s with %s = %v, want %v", op, tc.name, err, tc.want)
	}

	// A reason that is present but not valid must not be reported as a
	// missing reason, and the other way around.
	other := localuser.ErrReasonRequired
	if errors.Is(tc.want, localuser.ErrReasonRequired) {
		other = audit.ErrInvalidReason
	}

	if errors.Is(err, other) {
		t.Fatalf("%s with %s = %v, also matches %v", op, tc.name, err, other)
	}
}

// TestCreateChecksTheReasonLimit proves that a create refuses a reason that
// the audit log refuses with audit.ErrInvalidReason, before it makes the
// person, and accepts the longest reason of each byte width.
func TestCreateChecksTheReasonLimit(t *testing.T) {
	t.Parallel()

	for i, tc := range reasonCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			name := "user" + strings.Repeat("x", i)

			_, err := f.svc.CreateUser(t.Context(), admin(), localusersvc.CreateInput{
				UserName: name, DisplayName: "User", Reason: tc.reason,
			})
			checkReason(t, "create", tc, err)
		})
	}
}

// TestResetAndUnlockCheckTheReasonLimit proves that a reset, an unlock and
// the removal of a password refuse a reason that the audit log refuses with
// audit.ErrInvalidReason, and accept the longest reason of each byte width.
func TestResetAndUnlockCheckTheReasonLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range reasonCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			c := f.mustCreate(t, "alice", goodPassword)

			_, err := f.svc.StartReset(t.Context(), admin(), c.Principal.ID, localusersvc.ResetInput{
				Reason: tc.reason, RevokeAccessTokens: false,
			})
			checkReason(t, "reset", tc, err)

			_, err = f.svc.Unlock(t.Context(), admin(), c.Principal.ID, tc.reason)
			checkReason(t, "unlock", tc, err)

			if tc.want != nil {
				err = f.svc.RemovePassword(t.Context(), admin(), c.Principal.ID, tc.reason)
				checkReason(t, "remove password", tc, err)
			}
		})
	}
}
