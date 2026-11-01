package access_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// collectionReviewFixture seeds two collections in acme and one in beta, each
// with a unit, and one grant for each holder on a different object. The
// holders name what each grant is on, so that a test sees which grant leaked.
func collectionReviewFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t)
	f.units["pulumi:acme/web/prod"] = "payments"
	f.units["pulumi:acme/api/prod"] = "payments"
	f.units["pulumi:acme/db/prod"] = "billing"
	f.units["pulumi:beta/ledger/prod"] = "ledger"

	for _, subject := range []string{
		"oncollection", "onwebunit", "onapiunit", "onnamespace", "onbilling", "onbillingunit",
		"onbeta", "onledger", "onledgerunit",
	} {
		f.person(t, subject)
	}

	payments := identity.CollectionObject("acme", "payments")
	f.grant(t, prn("oncollection"), access.RoleViewer, payments)
	f.grant(t, prn("onwebunit"), access.RoleViewer, unitObject(t, "acme", "web/prod"))
	f.grant(t, prn("onapiunit"), access.RoleViewer, unitObject(t, "acme", "api/prod"))
	f.grant(t, prn("onnamespace"), access.RoleViewer, identity.NamespaceObject("acme"))
	f.grant(t, prn("onbilling"), access.RoleViewer, identity.CollectionObject("acme", "billing"))
	f.grant(t, prn("onbillingunit"), access.RoleViewer, unitObject(t, "acme", "db/prod"))
	f.grant(t, prn("onbeta"), access.RoleViewer, identity.NamespaceObject("beta"))
	f.grant(t, prn("onledger"), access.RoleViewer, identity.CollectionObject("beta", "ledger"))
	f.grant(t, prn("onledgerunit"), access.RoleViewer, unitObject(t, "beta", "ledger/prod"))

	return f
}

// reviewSubjects returns the sorted subject IDs of the grant rows of page.
func reviewSubjects(page access.ReviewPage) []string {
	var out []string

	for i := range page.Entries {
		if page.Entries[i].Holder == "" {
			out = append(out, page.Entries[i].Row.Grant.Subject.ID)
		}
	}

	slices.Sort(out)

	return out
}

// TestReviewOfACollection proves that the export of a collection holds the
// grants on the collection, on each unit in it and on the objects that
// contain it, and that a grant on another collection, on a unit of another
// collection, or in another namespace never appears.
func TestReviewOfACollection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		obj  identity.Object
		name string
		want []string
	}{
		{
			name: "payments holds its collection, its units and the namespace",
			obj:  identity.CollectionObject("acme", "payments"),
			want: []string{"prn-onapiunit", "prn-oncollection", "prn-onnamespace", "prn-onwebunit"},
		},
		{
			name: "billing holds its own collection and unit only",
			obj:  identity.CollectionObject("acme", "billing"),
			want: []string{"prn-onbilling", "prn-onbillingunit", "prn-onnamespace"},
		},
		{
			name: "a collection of another namespace holds nothing of acme",
			obj:  identity.CollectionObject("beta", "ledger"),
			want: []string{"prn-onbeta", "prn-onledger", "prn-onledgerunit"},
		},
	}

	f := collectionReviewFixture(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			page, err := f.svc.Review(t.Context(), tc.obj, access.ReviewRequest{
				From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
			})
			if err != nil {
				t.Fatalf("review: %v", err)
			}

			if got := reviewSubjects(page); !slices.Equal(got, tc.want) {
				t.Fatalf("subjects = %v, want %v", got, tc.want)
			}

			// The total counts the grant rows and the bootstrap row.
			if page.Total == nil || *page.Total != int64(len(tc.want)+1) {
				t.Fatalf("total = %v, want %d", page.Total, len(tc.want)+1)
			}
		})
	}
}

// TestReviewOfACollectionStates proves that a grant on the namespace shows as
// inherited, and that a grant on a deleted unit of the collection shows with
// the state deleted.
func TestReviewOfACollectionStates(t *testing.T) {
	t.Parallel()

	f := collectionReviewFixture(t)
	deleteStack(t, f, "web", "prod")

	page, err := f.svc.Review(t.Context(), identity.CollectionObject("acme", "payments"), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	for i := range page.Entries {
		checkStateRow(t, &page.Entries[i])
	}
}

// checkStateRow checks the state and the inheritance of one row of the export
// of payments after the unit web/prod was deleted.
func checkStateRow(t *testing.T, e *access.ReviewEntry) {
	t.Helper()

	id := e.Row.Grant.Subject.ID
	inherited := e.InheritedFrom != nil

	switch id {
	case "prn-onwebunit":
		if e.ObjectState != access.ObjectDeleted || inherited {
			t.Errorf(
				"deleted unit row = %q inherited %v, want deleted and not inherited",
				e.ObjectState,
				e.InheritedFrom,
			)
		}
	case "prn-onnamespace":
		if !inherited || *e.InheritedFrom != identity.NamespaceObject("acme") {
			t.Errorf("namespace row inherited from %v, want the namespace acme", e.InheritedFrom)
		}
	case "prn-oncollection", "prn-onapiunit":
		if e.ObjectState != access.ObjectLive || inherited {
			t.Errorf("%s row = %q inherited %v, want live and not inherited", id, e.ObjectState, e.InheritedFrom)
		}
	default:
	}
}

// TestReviewOfACollectionFailsClosed proves that the export is refused, with
// no row, for a collection that does not exist and for a server whose
// collection port cannot list units.
func TestReviewOfACollectionFailsClosed(t *testing.T) {
	t.Parallel()

	f := collectionReviewFixture(t)

	_, err := f.svc.Review(t.Context(), identity.CollectionObject("acme", "absent"), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if !errors.Is(err, access.ErrNotFound) {
		t.Errorf("review of a missing collection = %v, want ErrNotFound", err)
	}

	_, err = f.svc.Review(t.Context(), identity.CollectionObject("nowhere", "payments"), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if !errors.Is(err, access.ErrNotFound) {
		t.Errorf("review in a missing namespace = %v, want ErrNotFound", err)
	}

	listless := newFixtureWithCollections(t, noUnitList{f.units})

	_, err = listless.svc.Review(t.Context(), identity.CollectionObject("acme", "payments"), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if !errors.Is(err, access.ErrCollectionReview) {
		t.Errorf("review without a unit list = %v, want ErrCollectionReview", err)
	}
}

// noUnitList is a collection port that cannot list the units of a collection.
type noUnitList struct {
	c collections
}

func (n noUnitList) CollectionOf(ctx context.Context, unit identity.Object) (string, bool, error) {
	return n.c.CollectionOf(ctx, unit)
}

func (n noUnitList) CollectionID(ctx context.Context, col identity.Object) (string, bool, error) {
	return n.c.CollectionID(ctx, col)
}
