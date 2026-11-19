package identitytest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// denyNamespace is an Authorizer that is wider than it says. Its list covers
// every object except the objects of "beta", and Authorize denies each object
// of the namespace deny.
type denyNamespace struct {
	deny namespace.Name
	// unitsOnly makes Authorize deny only the unit objects of deny. The
	// namespace object of deny stays allowed.
	unitsOnly bool
}

func (d denyNamespace) Authorize(_ context.Context, _ identity.Principal, _ identity.Action, o identity.Object) error {
	if o.Namespace() == d.deny && (!d.unitsOnly || o.IsUnit()) {
		return identity.ErrDenied
	}

	return nil
}

func (denyNamespace) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Every("beta"), nil
}

// TestCheckListWithinAuthorizeCatchesALeak is the hostile test of the check
// itself: it must fail for a list that holds an object that Authorize denies,
// and must pass when Authorize denies only an object that the list leaves out.
// A check that lists the objects of a left-out namespace fails the second row.
func TestCheckListWithinAuthorizeCatchesALeak(t *testing.T) {
	t.Parallel()

	unitIn := func(ns namespace.Name) identity.Object {
		obj, err := identity.UnitObject("pulumi", ns, "web/prod")
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		return obj
	}

	every := []identity.Object{
		identity.NamespaceObject("acme"), unitIn("acme"), identity.NamespaceObject("beta"), unitIn("beta"),
	}

	alice := identity.Principal{
		Subject: aliceSubject, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	cases := []struct {
		name      string
		deny      namespace.Name
		unitsOnly bool
		wantErr   bool
	}{
		{name: "Authorize denies a unit that the list holds", deny: "acme", unitsOnly: false, wantErr: true},
		{
			name: "Authorize denies only the unit of a namespace that the list holds",
			deny: "acme", unitsOnly: true, wantErr: true,
		},
		{
			name: "Authorize denies only objects that the list leaves out",
			deny: "beta", unitsOnly: false, wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := listWithinAuthorizeError(t.Context(), ListCheck{
				Authorizer: denyNamespace{deny: tc.deny, unitsOnly: tc.unitsOnly},
				Principal:  alice,
				Action:     identity.ActionUnitRead,
				Every:      every,
			})

			if tc.wantErr {
				if !errors.Is(err, identity.ErrDenied) {
					t.Fatalf("error = %v, want ErrDenied", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}
