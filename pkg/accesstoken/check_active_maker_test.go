package accesstoken_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
)

// switchMakers is a MakerChecker whose answer a test changes after the
// service exists, as a direct change of a grant in the store does: no run of
// RevokeLostMakers follows it.
type switchMakers struct {
	can atomic.Bool
	err error
}

func (m *switchMakers) CanActAs(context.Context, string, string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}

	return m.can.Load(), nil
}

// TestCheckActiveChecksTheMaker sends the hostile cases of the lease check of
// a service token to CheckActive, the same check that runs at the use of the
// token. A refusal wraps ErrUnauthenticated. A maker with no path, and a check
// that cannot decide, both refuse. Only the first revokes, with the entry of
// a use.
func TestCheckActiveChecksTheMaker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		can         bool
		err         error
		personal    bool
		wantRefused bool
		wantRevoked bool
	}{
		{name: "maker keeps the path", can: true, err: nil, personal: false, wantRefused: false, wantRevoked: false},
		{name: "maker lost the path", can: false, err: nil, personal: false, wantRefused: true, wantRevoked: true},
		{
			name: "checker cannot decide", can: true, err: errDecide, personal: false,
			wantRefused: true, wantRevoked: false,
		},
		{
			name: "personal token needs no maker", can: false, err: nil, personal: true,
			wantRefused: false, wantRevoked: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			makers := &switchMakers{can: atomic.Bool{}, err: tc.err}
			makers.can.Store(tc.can)

			f := newMakerFixture(t, makers)

			id := f.token.ID
			if tc.personal {
				id = f.personal.ID
			}

			err := f.checked.CheckActive(t.Context(), id)

			if tc.wantRefused != errors.Is(err, identity.ErrUnauthenticated) {
				t.Fatalf("CheckActive = %v, want refused = %v", err, tc.wantRefused)
			}

			if tc.wantRevoked {
				wantRevokedAtUse(t, &f, id)
			} else if f.revoked(t, id) {
				t.Fatal("the token is revoked")
			}
		})
	}
}

// wantRevokedAtUse fails the test unless the token id is revoked with the one
// entry of a revocation at its use.
func wantRevokedAtUse(t *testing.T, f *makerFixture, id string) {
	t.Helper()

	if !f.revoked(t, id) {
		t.Fatal("the token is not revoked")
	}

	e := f.revocationEntry(t, id)
	if e.Reason != accesstoken.ReasonMakerLostPathAtUse || changeOf(e, "maker") != "admin" {
		t.Fatalf("revocation entry = %+v, want the reason of a use and the maker", e)
	}
}

// TestCheckActiveRefusesAfterTheMakerLosesThePath proves that the path can end
// while the token is active: the first check passes, the maker loses the path
// with no run after a change, the next check refuses and revokes, and a later
// check refuses without a second entry.
func TestCheckActiveRefusesAfterTheMakerLosesThePath(t *testing.T) {
	t.Parallel()

	makers := &switchMakers{can: atomic.Bool{}, err: nil}
	makers.can.Store(true)

	f := newMakerFixture(t, makers)

	if err := f.checked.CheckActive(t.Context(), f.token.ID); err != nil {
		t.Fatalf("check with a path: %v", err)
	}

	makers.can.Store(false)

	for range 2 {
		if err := f.checked.CheckActive(t.Context(), f.token.ID); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("check without a path = %v, want ErrUnauthenticated", err)
		}
	}

	if !f.revoked(t, f.token.ID) {
		t.Fatal("the token is not revoked")
	}

	// One entry: revocationEntry fails the test when there are more.
	f.revocationEntry(t, f.token.ID)
}
