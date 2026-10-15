package tokengrants_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/tokengrants"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// fakeGrants answers err for every check.
type fakeGrants struct {
	err error
}

func (f fakeGrants) CheckPrincipalGrants(context.Context, identity.Principal, string) error {
	return f.err
}

// fakePrincipals knows the principal prn-1 only, or answers err.
type fakePrincipals struct {
	err error
}

func (f fakePrincipals) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	if f.err != nil {
		return principal.Principal{}, f.err
	}

	if id != "prn-1" {
		return principal.Principal{}, principal.ErrNotFound
	}

	return principal.Principal{
		CreatedAt:   time.Time{},
		UpdatedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     "ci",
		DisplayName: "",
		Kind:        principal.KindService,
		Source:      principal.SourceLocal,
		Namespace:   "", GroupCount: 0,
	}, nil
}

var errStore = errors.New("store down")

func TestRequireHeld(t *testing.T) {
	t.Parallel()

	caller := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "alice",
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}

	cases := []struct {
		grants     error
		principals error
		id         string
		name       string
		wantDenied bool
		wantErr    bool
	}{
		{grants: nil, principals: nil, id: "prn-1", name: "held", wantDenied: false, wantErr: false},
		{
			grants: access.ErrEscalation, principals: nil, id: "prn-1", name: "escalation is denied",
			wantDenied: true, wantErr: true,
		},
		{
			grants: nil, principals: nil, id: "prn-none", name: "a missing principal is denied",
			wantDenied: true, wantErr: true,
		},
		{
			grants: errStore, principals: nil, id: "prn-1", name: "a store error is not a denial",
			wantDenied: false, wantErr: true,
		},
		{
			grants: nil, principals: errStore, id: "prn-1", name: "a principal read error is not a denial",
			wantDenied: false, wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			check := tokengrants.Check{
				Grants:     fakeGrants{err: tc.grants},
				Principals: fakePrincipals{err: tc.principals},
			}
			err := check.RequireHeld(t.Context(), caller, tc.id)

			if (err != nil) != tc.wantErr || errors.Is(err, identity.ErrDenied) != tc.wantDenied {
				t.Fatalf("RequireHeld = %v, want an error %t, denied %t", err, tc.wantErr, tc.wantDenied)
			}

			if (errors.Is(tc.grants, errStore) || errors.Is(tc.principals, errStore)) && !errors.Is(err, errStore) {
				t.Fatalf("RequireHeld = %v, want it to wrap the store error", err)
			}
		})
	}
}
