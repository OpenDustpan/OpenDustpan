package accesstoken_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
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

// TestRevokeLostMakersReadsTheGroupsAfterTheChange sends the case of a
// request whose route check filled the request cache with the groups of the
// maker. The maker leaves the group that gives her the path, and
// RevokeLostMakers runs with the same context. The service drops the cache
// itself, so the token ends even with a checker that reads through the cache.
func TestRevokeLostMakersReadsTheGroupsAfterTheChange(t *testing.T) {
	t.Parallel()

	rf := newReachFixture(t)
	authz := rf.authorizerOf(t)

	// The token of member for the service principal ops.
	made, _, err := rf.tokens.Create(t.Context(), as(rf.member), caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: rf.clock.Now().Add(accesstoken.DefaultLifetime), PrincipalID: rf.ops.ID, Name: "ci"})
	if err != nil {
		t.Fatalf("create the token of the maker: %v", err)
	}

	checked, err := accesstoken.NewService(accesstoken.Config{
		Makers: authorizerMakers{authz: authz, principals: rf.store}, TempAdmins: nil,
		Store: rf.store, Principals: rf.store, Freshness: nil, Authorizer: nil, Reach: rf.reach,
		Logger: nil, Now: rf.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	// The route check of the request: it reads the groups of the maker into
	// the cache of the request.
	ctx := access.WithRequestCache(t.Context())

	routeErr := authz.Authorize(ctx, as(rf.member), identity.ActionPrincipalManageTokens,
		identity.PrincipalObject(rf.ops.ID))
	if routeErr != nil {
		t.Fatalf("the route check before the change: %v", routeErr)
	}

	err = rf.principals.RemoveMember(ctx, caller(), admin(), rf.group.ID, rf.member.ID)
	if err != nil {
		t.Fatalf("remove the maker from the group: %v", err)
	}

	cause := accesstoken.Cause{Subject: rf.member.Subject, Action: "server:manage-groups", Object: "group:" + rf.group.ID}

	got, err := checked.RevokeLostMakers(ctx, cause)
	if err != nil {
		t.Fatalf("revoke lost makers: %v", err)
	}

	if len(got.Revoked) != 1 || got.Revoked[0] != made.ID {
		t.Fatalf("revoked = %v, want the token %s of the maker who left the group", got.Revoked, made.ID)
	}

}
