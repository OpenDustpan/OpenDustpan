package access_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// listedScope is an Authorizer that lists a fixed scope, or fails the list
// with err. It allows nothing: Objects reads the list only.
type listedScope struct {
	err   error
	scope identity.Scope
}

func (listedScope) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return identity.ErrDenied
}

func (l listedScope) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return l.scope, l.err
}

// errListDown is the error of a failed list.
var errListDown = errors.New("list down")

// objectsCaller returns the caller of the tests of Objects.
func objectsCaller() identity.Principal {
	return identity.Principal{
		Subject: "ops", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
}

// TestObjectsListsThroughTheLister sends the scopes that the IP allowlist
// gate gives directly to Objects: the page lists the scope of the lister, not
// of the grants alone, names the left-out namespaces next to all=true, and
// names none for the principal kind, whose objects are in no namespace.
func TestObjectsListsThroughTheLister(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	explicit := identity.Scope{
		Namespaces: []identity.Object{identity.NamespaceObject("beta")}, Collections: nil, Units: nil,
		Principals: nil,
	}

	cases := []struct {
		name         string
		scope        identity.Scope
		kind         access.ObjectKind
		wantAll      bool
		wantExcluded []namespace.Name
		wantObjects  []string
	}{
		{
			name: "every object", scope: identity.Every(), kind: access.ObjectNamespace,
			wantAll: true, wantExcluded: []namespace.Name{}, wantObjects: []string{},
		},
		{
			name: "every object but two", scope: identity.Every("beta", "acme"), kind: access.ObjectNamespace,
			wantAll: true, wantExcluded: []namespace.Name{"acme", "beta"}, wantObjects: []string{},
		},
		{
			name: "every object but one, for units", scope: identity.Every("acme"), kind: access.ObjectUnit,
			wantAll: true, wantExcluded: []namespace.Name{"acme"}, wantObjects: []string{},
		},
		{
			name: "every object but one, for principals", scope: identity.Every("acme"), kind: access.ObjectPrincipal,
			wantAll: true, wantExcluded: []namespace.Name{}, wantObjects: []string{},
		},
		{
			name: "explicit objects", scope: explicit, kind: access.ObjectNamespace,
			wantAll: false, wantExcluded: []namespace.Name{}, wantObjects: []string{"beta"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			page, err := f.svc.Objects(t.Context(), listedScope{err: nil, scope: tc.scope}, objectsCaller(),
				access.ObjectsRequest{Kind: tc.kind, Action: "", Cursor: "", Limit: 0})
			if err != nil {
				t.Fatalf("objects: %v", err)
			}

			got := make([]string, 0, len(page.Objects))
			for i := range page.Objects {
				got = append(got, page.Objects[i].String())
			}

			if page.All != tc.wantAll || page.Excluded == nil || !slices.Equal(page.Excluded, tc.wantExcluded) ||
				!slices.Equal(got, tc.wantObjects) {
				t.Fatalf("page = all %t excluded %v objects %v, want all %t excluded %v objects %v",
					page.All, page.Excluded, got, tc.wantAll, tc.wantExcluded, tc.wantObjects)
			}
		})
	}
}

// TestObjectsFailsClosed proves that Objects lists nothing with no lister and
// with a failed list.
func TestObjectsFailsClosed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	req := access.ObjectsRequest{Kind: access.ObjectNamespace, Action: "", Cursor: "", Limit: 0}

	page, err := f.svc.Objects(t.Context(), nil, objectsCaller(), req)
	if !errors.Is(err, access.ErrMissingDependency) || page.All || len(page.Objects) != 0 {
		t.Fatalf("no lister: page = %+v, err = %v, want ErrMissingDependency and no object", page, err)
	}

	page, err = f.svc.Objects(t.Context(), listedScope{err: errListDown, scope: identity.Every()}, objectsCaller(), req)
	if !errors.Is(err, errListDown) || page.All || len(page.Objects) != 0 {
		t.Fatalf("failed list: page = %+v, err = %v, want the list error and no object", page, err)
	}
}
