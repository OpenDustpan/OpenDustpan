package accesstoken_test

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
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
func TestTokenIsRefusedAfterAPathWithNoSweep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// end ends the path of the maker with a write that no sweep follows.
		end func(t *testing.T, rf *reachFixture, other *access.Service) error
	}{
		{
			// The path of the sign-in service: it disables the person.
			name: "disable of the maker at sign-in",
			end: func(t *testing.T, rf *reachFixture, _ *access.Service) error {
				t.Helper()

				e := storetest.ServerChange(rf.clock.Now(), principal.Ref(rf.holder.ID))

				return wrapStep("disable the maker", rf.store.DisablePrincipal(t.Context(), rf.holder.ID, e))
			},
		},
		{
			// The seed of the demo person revokes a grant with the store.
			name: "revoke of a grant by the demo seed",
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
			name: "maker missing after a restore",
			end: func(t *testing.T, rf *reachFixture, _ *access.Service) error {
				t.Helper()

				e := storetest.ServerChange(rf.clock.Now(), principal.Ref(rf.holder.ID))

				return wrapStep("delete the maker", rf.store.DeletePrincipal(t.Context(), rf.holder.ID, e))
			},
		},
		{
			// Another instance of the same database revokes the grant with
			// its own access service.
			name: "revoke of the grant on another instance",
			end: func(t *testing.T, _ *reachFixture, other *access.Service) error {
				t.Helper()

				_, err := other.RevokeByID(t.Context(), caller(), admin(), "grt-nosweep")

				return wrapStep("revoke the grant on the other instance", err)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wantRefusedAfterNoSweep(t, tc.end)
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
	t *testing.T, end func(t *testing.T, rf *reachFixture, other *access.Service) error,
) {
	t.Helper()

	rf := newReachFixture(t)
	obj := identity.PrincipalObject(rf.bare.ID)
	rf.grant(t, "grt-nosweep", principalSubject(rf.holder), access.RoleTokenManager, obj)

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
}

// wrapStep returns err with the step that failed, or nil.
func wrapStep(step string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", step, err)
	}

	return nil
}
