package accesstoken_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// authorizerMakers is a MakerChecker that decides with the Authorizer of the
// grants and reads through the cache of its context, as it is: it does not
// drop the cache. It stands for any checker that a caller might wire, so a
// test of RevokeLostMakers does not depend on the guard of access.Service.
type authorizerMakers struct {
	authz      *access.Authorizer
	principals accesstoken.PrincipalReader
}

func (m authorizerMakers) CanActAs(ctx context.Context, maker, principalID string) (bool, error) {
	p, err := m.principals.GetPrincipalBySubject(ctx, maker)
	if err != nil {
		return false, fmt.Errorf("read the maker %q: %w", maker, err)
	}

	err = m.authz.Authorize(ctx, as(p), identity.ActionPrincipalManageTokens, identity.PrincipalObject(principalID))

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, identity.ErrDenied):
		return false, nil
	default:
		return false, fmt.Errorf("decide: %w", err)
	}
}

// cacheCase is one change that ends the act-as path of a maker. setup
// stores the path and returns the maker and the change. The change runs
// after the route check filled the request cache.
type cacheCase struct {
	name  string
	setup func(t *testing.T, rf *reachFixture) cachePath
}

// cachePath is the path of a maker to the service principal of its token,
// and the change that ends it.
type cachePath struct {
	maker  principal.Principal
	target principal.Principal
	change func(ctx context.Context) error
	cause  accesstoken.Cause
}

// TestRevokeLostMakersReadsTheGroupsAfterTheChange sends the case of a
// request whose route check filled the request cache with the groups and the
// roles of the maker. The change ends the path of the maker, and
// RevokeLostMakers runs with the same context. The service drops the cache
// itself, so the token ends even with a checker that reads through the cache.
// One row ends the path by a group membership, the other by the edit of a
// custom role.
func TestRevokeLostMakersReadsTheGroupsAfterTheChange(t *testing.T) {
	t.Parallel()

	cases := []cacheCase{
		{name: "group membership", setup: groupPathCase},
		{name: "custom role", setup: customRolePathCase},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rf := newReachFixture(t)
			wantRevokedAfterCachedRoute(t, &rf, tc.setup(t, &rf))
		})
	}
}

// wantRevokedAfterCachedRoute makes the token of the maker of path, fills the
// request cache with the route check, runs the change of path and runs
// RevokeLostMakers with the same context. It fails unless the token ends.
func wantRevokedAfterCachedRoute(t *testing.T, rf *reachFixture, path cachePath) {
	t.Helper()

	authz := rf.authorizerOf(t)

	// The token of the maker for the service principal of the path.
	made, _, err := rf.tokens.Create(
		t.Context(),
		as(path.maker),
		caller(),
		accesstoken.ScopeService,
		accesstoken.Input{
			ExpiresAt:   rf.clock.Now().Add(accesstoken.DefaultLifetime),
			PrincipalID: path.target.ID,
			Name:        "ci",
		},
	)
	if err != nil {
		t.Fatalf("create the token of the maker: %v", err)
	}

	checked, err := accesstoken.NewService(accesstoken.Config{
		Makers: authorizerMakers{authz: authz, principals: rf.store}, TempAdmins: nil,
		Store: rf.store, Principals: rf.store, Freshness: noStepGuard(t), Authorizer: nil, Reach: rf.reach,
		Logger: nil, Now: rf.clock.Now, NewID: nil,
		Guard: freshstep.Guard{},
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	// The route check of the request: it reads the groups and the roles of
	// the maker into the cache of the request.
	ctx := access.WithRequestCache(t.Context())

	routeErr := authz.Authorize(ctx, as(path.maker), identity.ActionPrincipalManageTokens,
		identity.PrincipalObject(path.target.ID))
	if routeErr != nil {
		t.Fatalf("the route check before the change: %v", routeErr)
	}

	if err = path.change(ctx); err != nil {
		t.Fatalf("the change: %v", err)
	}

	got, err := checked.RevokeLostMakers(ctx, path.cause)
	if err != nil {
		t.Fatalf("revoke lost makers: %v", err)
	}

	if len(got.Revoked) != 1 || got.Revoked[0] != made.ID {
		t.Fatalf("revoked = %v, want the token %s of the maker who lost the path", got.Revoked, made.ID)
	}
}

// groupPathCase ends the path of member by the removal from the group.
func groupPathCase(
	t *testing.T, rf *reachFixture,
) cachePath {
	t.Helper()

	change := func(ctx context.Context) error {
		err := rf.principals.RemoveMember(ctx, caller(), admin(), rf.group.ID, rf.member.ID)
		if err != nil {
			return fmt.Errorf("remove the maker from the group: %w", err)
		}

		return nil
	}

	return cachePath{maker: rf.member, target: rf.ops, change: change, cause: accesstoken.Cause{
		Subject: rf.member.Subject, Action: "server:manage-groups", Object: "group:" + rf.group.ID,
	}}
}

// customRolePathCase ends the path of a person who holds a custom role on the installation
// by the edit of the role: the new set has no token action. The token is for
// bare, which holds nothing, so the reach of the maker covers it.
func customRolePathCase(
	t *testing.T, rf *reachFixture,
) cachePath {
	t.Helper()

	person := rf.personOf(t, "custom@example.com")

	svc, err := access.NewService(access.ServiceConfig{
		Store: rf.store, Directory: rf.store, Namespaces: rf.store, Authorizer: rf.authorizerOf(t),
		Reach: rf.reach, Now: rf.clock.Now, NewID: nil, BootstrapAdmins: []string{"admin"},
		Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	role, err := svc.CreateRole(t.Context(), caller(), admin(), access.RoleInput{
		Name: "ci-keeper", Description: "",
		Actions: []identity.Action{identity.ActionPrincipalManageTokens, identity.ActionNamespaceRead},
	})
	if err != nil {
		t.Fatalf("create the custom role: %v", err)
	}

	rf.grant(t, "grt-custom", principalSubject(person), role.ID, identity.ServerObject())

	change := func(ctx context.Context) error {
		_, err := svc.UpdateRole(ctx, caller(), admin(), role.ID, access.RoleInput{
			Name: "ci-keeper", Description: "", Actions: []identity.Action{identity.ActionNamespaceRead},
		})
		if err != nil {
			return fmt.Errorf("take the token action from the role: %w", err)
		}

		return nil
	}

	return cachePath{maker: person, target: rf.bare, change: change, cause: accesstoken.Cause{
		Subject: "admin", Action: "server:manage-roles", Object: "role:" + role.ID,
	}}
}
