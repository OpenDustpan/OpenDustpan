package access_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// cachedTina returns a context with a request cache whose cache holds the
// access of tina to prn-ci: the first decision of a request, as the route
// check makes it, before a change. It fails unless tina has the path.
func cachedTina(t *testing.T, f *fixture, ci identity.Object) context.Context {
	t.Helper()

	ctx := access.WithRequestCache(t.Context())

	tina := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "tina", DisplayName: "",
		Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: "", JobID: "",
	}

	authErr := f.svc.Authorizer().Authorize(ctx, tina, identity.ActionPrincipalManageTokens, ci)
	if authErr != nil {
		t.Fatalf("the route check before the change: %v", authErr)
	}

	return ctx
}

// wantNoPathWithCache fails when CanActAs answers true or fails with ctx,
// whose cache holds the reads from before the change.
func wantNoPathWithCache(ctx context.Context, t *testing.T, f *fixture) {
	t.Helper()

	if !access.HasRequestCache(ctx) {
		t.Fatal("the test context has no cache")
	}

	got, err := f.svc.CanActAs(ctx, "tina", "prn-ci")
	if err != nil {
		t.Fatalf("CanActAs after the change: %v", err)
	}

	if got {
		t.Fatal("CanActAs answered true from the cache of the request after the change")
	}
}

// TestCanActAsIgnoresTheGroupCache removes the maker from the group that
// gives her the path after the request cache read her groups. CanActAs with
// the same context must read the groups after the removal.
func TestCanActAsIgnoresTheGroupCache(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.installationService(t, "prn-ci", "ci-deploy")
	f.person(t, "tina")

	ci := identity.PrincipalObject("prn-ci")
	f.grant(t, f.group(t, "deployers", "tina"), access.RoleTokenManager, ci)

	ctx := cachedTina(t, f, ci)

	err := f.store.RemoveMember(t.Context(), "grp-deployers", "prn-tina",
		storetest.ServerChange(time.Unix(1, 0), "group:grp-deployers"))
	if err != nil {
		t.Fatalf("remove tina: %v", err)
	}

	wantNoPathWithCache(ctx, t, f)
}

// TestCanActAsIgnoresTheRoleCache removes the action from the custom role
// that gives the maker her path after the request cache read the role.
func TestCanActAsIgnoresTheRoleCache(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.installationService(t, "prn-ci", "ci-deploy")
	f.person(t, "tina")

	role := f.customRole(t, identity.ActionPrincipalManageTokens, identity.ActionServerReadPrincipals)
	f.grant(t, prn("tina"), role.ID, identity.ServerObject())

	ctx := cachedTina(t, f, identity.PrincipalObject("prn-ci"))

	_, err := f.svc.UpdateRole(t.Context(), testEntry(), admin(), role.ID, access.RoleInput{
		Name: role.Name, Description: "", Actions: []identity.Action{identity.ActionServerReadPrincipals},
	})
	if err != nil {
		t.Fatalf("update the role: %v", err)
	}

	wantNoPathWithCache(ctx, t, f)
}
