package localusersvc_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/localuser"
)

// errMemoryFault is an error of the hasher that is not one of the errors of
// its port.
var errMemoryFault = errors.New("memory fault")

// failingHasher hashes with the real hasher, and makes Reserve or the
// Verify of each reservation fail with the error of the case.
type failingHasher struct {
	localuser.PasswordHasher

	reserveErr error
	verifyErr  error
}

func (h failingHasher) Reserve(context.Context, localuser.PasswordHash) (*localuser.Reservation, error) {
	if h.reserveErr != nil {
		return nil, h.reserveErr
	}

	verify := func(string, localuser.HashParams) (localuser.Verdict, error) {
		return localuser.Verdict{NeedsRehash: false}, h.verifyErr
	}

	return localuser.NewReservation(verify, func() {}), nil
}

// TestCurrentPasswordHasherErrors proves that only a password that does not
// match, a password over the maximum length and a stored hash that does not
// parse answer a wrong current password, and that every other error of the
// hasher answers that it cannot answer.
func TestCurrentPasswordHasherErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		reserveErr error
		verifyErr  error
		want       error
		name       string
	}{
		{
			name: "a wrong password", reserveErr: nil, verifyErr: localuser.ErrWrongPassword,
			want: localuser.ErrWrongPassword,
		},
		{
			name: "a password over the maximum", reserveErr: nil, verifyErr: localuser.ErrPasswordTooLong,
			want: localuser.ErrWrongPassword,
		},
		{
			name: "a stored hash that does not parse", reserveErr: nil, verifyErr: localuser.ErrInvalid,
			want: localuser.ErrWrongPassword,
		},
		{
			name: "a full budget at the compare", reserveErr: nil, verifyErr: localuser.ErrHashBusy,
			want: localuser.ErrHashBusy,
		},
		{
			name: "a full budget at the reserve", reserveErr: localuser.ErrHashBusy, verifyErr: nil,
			want: localuser.ErrHashBusy,
		},
		{
			name: "another error of the compare", reserveErr: nil, verifyErr: errMemoryFault,
			want: localuser.ErrUnavailable,
		},
		{
			name: "another error of the reserve", reserveErr: errMemoryFault, verifyErr: nil,
			want: localuser.ErrUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			c := f.mustCreate(t, "alice", goodPassword)

			f.hasher = failingHasher{PasswordHasher: f.hasher, reserveErr: tc.reserveErr, verifyErr: tc.verifyErr}
			svc := f.serviceWith(t, access.ReachConfig{
				Grants:      f.store,
				Roles:       f.store,
				Callers:     f.store,
				Principals:  f.store,
				Collections: nil,
				Units:       f.store,
			})

			me := localusersvc.Actor{Entry: base(c.Principal.Subject), PrincipalID: c.Principal.ID, Admin: false}

			_, err := svc.ChangePassword(t.Context(), me, localusersvc.ChangeInput{
				Current: goodPassword, New: otherPassword, Generation: f.generation(t, c.Principal.ID), Fresh: false,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("change = %v, want %v", err, tc.want)
			}

			if !errors.Is(tc.want, localuser.ErrWrongPassword) && errors.Is(err, localuser.ErrWrongPassword) {
				t.Fatalf("change = %v, want no wrong password", err)
			}
		})
	}
}
