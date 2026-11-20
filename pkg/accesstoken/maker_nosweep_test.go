package accesstoken_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestTokenIsRefusedAfterAPathWithNoSweep sends each change of the act-as path
// of a maker that runs no RevokeLostMakers. The maker holds token-manager on
// the service principal bare through a direct grant. Each row ends that path
// by a write that no sweep follows. The token service decides with the access
// service, as a server does. The check at the use of the token must refuse the
// token, revoke it with the reason of a use, and write one entry.
//
// The row "maker missing after a restore" is a stand-in: it deletes the
// maker with DeletePrincipal, which leaves the same state as a restore of a
// database that has the token and has no row of the maker. No test here runs
// a restore.
func TestTokenIsRefusedAfterAPathWithNoSweep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup stores the act-as path of the maker. With nil, the maker
		// gets a direct grant of token-manager on bare.
		setup func(t *testing.T, rf *reachFixture)
		// end ends the path of the maker with a write that no sweep follows.
		end func(t *testing.T, rf *reachFixture, other *access.Service) error
	}{
		{
			// The path of the sign-in service: it disables the person.
			name:  "disable of the maker at sign-in",
			setup: nil,
			end: func(t *testing.T, rf *reachFixture, _ *access.Service) error {
				t.Helper()

				e := storetest.ServerChange(rf.clock.Now(), principal.Ref(rf.holder.ID))

				return wrapStep("disable the maker", rf.store.DisablePrincipal(t.Context(), rf.holder.ID, e))
			},
		},
		{
			// The seed of the demo person revokes a grant with the store.
			name:  "revoke of a grant by the demo seed",
			setup: nil,
			end: func(t *testing.T, rf *reachFixture, _ *access.Service) error {
				t.Helper()

				obj := identity.PrincipalObject(rf.bare.ID)
				fence, e := storetest.GrantWrite(t, rf.store, obj, rf.clock.Now(), "grant:grt-nosweep")

				return wrapStep("revoke the grant", rf.store.RevokeGrant(t.Context(), "grt-nosweep", fence, e, nil))
			},
		},
		{
			// A restore brings a token whose maker is not in the restored
			// directory.
			name:  "maker missing after a restore",
			setup: nil,
			end: func(t *testing.T, rf *reachFixture, _ *access.Service) error {
				t.Helper()

				e := storetest.ServerChange(rf.clock.Now(), principal.Ref(rf.holder.ID))

				return wrapStep("delete the maker", rf.store.DeletePrincipal(t.Context(), rf.holder.ID, e))
			},
		},
		{
			// Another instance of the same database revokes the grant with
			// its own access service.
			name:  "revoke of the grant on another instance",
			setup: nil,
			end: func(t *testing.T, _ *reachFixture, other *access.Service) error {
				t.Helper()

				_, err := other.RevokeByID(t.Context(), caller(), admin(), "grt-nosweep")

				return wrapStep("revoke the grant on the other instance", err)
			},
		},
		{
			// The sign-in of a person applies the group claims of the
			// identity provider. It removes the membership that gave the
			// path, and no sweep follows.
			name:  "removal of a membership by the group claims of the identity provider",
			setup: claimNoSweepPath,
			end: func(t *testing.T, rf *reachFixture, _ *access.Service) error {
				t.Helper()

				applier := rf.store.GroupClaimApplier(noGroupsMapper{})

				res, err := applier.ApplyGroupClaims(t.Context(),
					storetest.ServerChange(rf.clock.Now(), principal.Ref(rf.holder.ID)), principal.GroupClaims{
						PrincipalID: rf.holder.ID, Source: principal.MembershipSourceClaim, SourceID: noSweepIDP,
						Groups: nil, Present: true,
					})
				if err != nil {
					return wrapStep("apply the group claims", err)
				}

				if len(res.Removed) != 1 {
					return fmt.Errorf("%w: removed %v", errClaimsRemoved, res.Removed)
				}

				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup
			if setup == nil {
				setup = directNoSweepPath
			}

			wantRefusedAfterNoSweep(t, setup, tc.end)
		})
	}
}

// newNoSweepAccessService returns an access service over the store of rf. A
// second call stands for another instance of the same database.
func newNoSweepAccessService(t *testing.T, rf *reachFixture) *access.Service {
	t.Helper()

	svc, err := access.NewService(access.ServiceConfig{
		Store: rf.store, Directory: rf.store, Namespaces: rf.store, Authorizer: rf.authorizerOf(t),
		Reach: rf.reach, Now: rf.clock.Now, NewID: nil, BootstrapAdmins: []string{"admin"},
		Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	return svc
}

// wantRefusedAfterNoSweep makes the token of a maker with a direct grant,
// runs end, and fails unless the check at use refuses and revokes the token.
func wantRefusedAfterNoSweep(
	t *testing.T, setup func(t *testing.T, rf *reachFixture),
	end func(t *testing.T, rf *reachFixture, other *access.Service) error,
) {
	t.Helper()

	rf := newReachFixture(t)
	setup(t, &rf)

	checked, err := accesstoken.NewService(accesstoken.Config{
		Makers: newNoSweepAccessService(t, &rf), TempAdmins: nil,
		Store: rf.store, Principals: rf.store, Freshness: nil, Authorizer: rf.authorizerOf(t), Reach: rf.reach,
		Logger: nil, Now: rf.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	made, raw, err := checked.Create(t.Context(), as(rf.holder), caller(), accesstoken.ScopeService,
		accesstoken.Input{
			ExpiresAt: rf.clock.Now().Add(accesstoken.DefaultLifetime), PrincipalID: rf.bare.ID, Name: "ci",
		})
	if err != nil {
		t.Fatalf("create the token of the maker: %v", err)
	}

	if _, err = checked.AuthenticateToken(t.Context(), raw.Reveal()); err != nil {
		t.Fatalf("the token before the change: %v", err)
	}

	if err = end(t, &rf, newNoSweepAccessService(t, &rf)); err != nil {
		t.Fatalf("end the path: %v", err)
	}

	// A second use gets the same refusal.
	for range 2 {
		_, err = checked.AuthenticateToken(t.Context(), raw.Reveal())
		if !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("the token after the change = %v, want ErrUnauthenticated", err)
		}
	}

	tok, err := rf.store.GetToken(t.Context(), made.ID)
	if err != nil {
		t.Fatalf("get the token: %v", err)
	}

	if tok.RevokedAt.IsZero() {
		t.Fatal("the check at use refused the token and did not revoke it")
	}

	e := noSweepEntry(t, &rf, made.ID)
	if e.Reason != accesstoken.ReasonMakerLostPathAtUse || changeOf(e, "maker") != rf.holder.Subject {
		t.Fatalf("revocation entry = %+v, want the reason of a use and the maker %q", e, rf.holder.Subject)
	}
}

// noSweepEntry returns the one audit entry of the revocation of the token id.
// It fails the test when there is no entry or more than one.
func noSweepEntry(t *testing.T, rf *reachFixture, id string) audit.Entry {
	t.Helper()

	listing, err := rf.store.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var found []audit.Entry

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Subject == accesstoken.MakerSubject && e.Object == accesstoken.TokenRef(rf.bare.ID, id) {
			found = append(found, *e)
		}
	}

	if len(found) != 1 {
		t.Fatalf("revocation entries of %s = %+v, want exactly one", id, found)
	}

	return found[0]
}

// errClaimsRemoved reports that the group claims did not remove the one
// membership that gave the path.
var errClaimsRemoved = errors.New("the claims did not remove the one membership that gave the path")

// noSweepIDP is the ID of the identity provider of the claim path.
const noSweepIDP = "oidc-1"

// directNoSweepPath gives the holder a direct grant of token-manager on bare.
func directNoSweepPath(t *testing.T, rf *reachFixture) {
	t.Helper()

	obj := identity.PrincipalObject(rf.bare.ID)
	rf.grant(t, "grt-nosweep", principalSubject(rf.holder), access.RoleTokenManager, obj)
}

// claimNoSweepPath gives the holder token-manager on bare through a group,
// and a membership of that group that the identity provider gave.
func claimNoSweepPath(t *testing.T, rf *reachFixture) {
	t.Helper()

	group, err := rf.principals.CreateGroup(t.Context(), caller(),
		principal.GroupInput{Name: "claim-holders", Description: ""})
	if err != nil {
		t.Fatalf("create the group: %v", err)
	}

	rf.grant(t, "grt-nosweep", access.Subject{Kind: access.SubjectGroup, ID: group.ID}, access.RoleTokenManager,
		identity.PrincipalObject(rf.bare.ID))

	at := rf.clock.Now()
	mem := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: group.ID, PrincipalID: rf.holder.ID,
		Source: principal.MembershipSourceClaim, SourceID: noSweepIDP, State: principal.MembershipStateActive,
		Pinned: false,
	}

	err = rf.store.AddMember(t.Context(), mem, storetest.ServerChange(at, principal.Ref(group.ID)), nil)
	if err != nil {
		t.Fatalf("add the membership of the claim: %v", err)
	}
}

// noGroupsMapper maps each value to no group: the identity provider has a
// mapping, and no value of the sign-in matches it.
type noGroupsMapper struct{}

func (noGroupsMapper) MapGroups(
	context.Context, string, principal.MembershipSource, string, []string,
) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// wrapStep returns err with the step that failed, or nil.
func wrapStep(step string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", step, err)
	}

	return nil
}
