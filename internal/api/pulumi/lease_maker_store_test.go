package pulumi_test

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// makerGrantID and makerGroupID name the rows of the act-as path of the maker.
const (
	makerGrantID = "grt-maker"
	makerGroupID = "grp-deployers"
)

// openMakerStore opens a store that the test closes.
func openMakerStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// seedMakerPerson stores the person "maker", who made the service token.
func seedMakerPerson(t *testing.T, st *sqlite.Store, at time.Time) {
	t.Helper()

	maker := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-maker", Subject: "maker",
		DisplayName: "maker", Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := st.CreatePrincipal(t.Context(), maker, storetest.ServerChange(at, "principal:prn-maker")); err != nil {
		t.Fatalf("create maker: %v", err)
	}
}

// seedMakerGrant stores the grant of token-manager on prn-ci to subject.
func seedMakerGrant(t *testing.T, st *sqlite.Store, at time.Time, subject access.Subject) {
	t.Helper()

	obj, err := identity.ParsePrincipalObject("prn-ci")
	if err != nil {
		t.Fatalf("parse the principal object: %v", err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: makerGrantID, Role: access.RoleTokenManager,
		CreatedBy: "admin", Subject: subject,
	}

	fence, e := storetest.GrantWrite(t, st, obj, at, "grant:"+makerGrantID)
	if err := st.CreateGrant(t.Context(), grant, fence, e, nil); err != nil {
		t.Fatalf("create grant: %v", err)
	}
}

// seedMakerGroup stores the group with the maker as its member.
func seedMakerGroup(t *testing.T, st *sqlite.Store, at time.Time) {
	t.Helper()

	grp := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: makerGroupID, Name: "deployers", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := st.CreateGroup(t.Context(), grp, storetest.ServerChange(at, "group:"+makerGroupID)); err != nil {
		t.Fatalf("create group: %v", err)
	}

	mem := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: makerGroupID, PrincipalID: "prn-maker",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	if err := st.AddMember(t.Context(), mem, storetest.ServerChange(at, "group:"+makerGroupID), nil); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// makerChecker returns the real access service over st: the checker that the
// server gives the token service.
func makerChecker(t *testing.T, st *sqlite.Store) *access.Service {
	t.Helper()

	authz, err := access.NewAuthorizer(
		access.AuthorizerConfig{Store: st, Callers: st, Collections: nil, Units: st, Principals: st},
	)
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	grants, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: st, Roles: st, Callers: st, Principals: st, Collections: nil, Units: st,
		},
		Now: nil, NewID: nil, BootstrapAdmins: nil, Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	return grants
}

// TestLeaseRequestsRefuseATokenWhoseMakerRowIsGone runs the real access
// service as the maker checker, over a real store. The act-as path of the
// maker is a stored row. A write straight to the store removes the row, with
// no run after a change. Each next lease request answers 401, and the token is
// revoked with one audit entry. Before the removal, a renew answers 200: the
// stored row is the path.
func TestLeaseRequestsRefuseATokenWhoseMakerRowIsGone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		seed   func(t *testing.T, st *sqlite.Store, at time.Time)
		remove func(t *testing.T, st *sqlite.Store, at time.Time)
	}{
		{
			name: "grant of the maker revoked",
			seed: func(t *testing.T, st *sqlite.Store, at time.Time) {
				t.Helper()
				seedMakerGrant(t, st, at, access.Subject{Kind: access.SubjectPrincipal, ID: "prn-maker"})
			},
			remove: revokeMakerGrant,
		},
		{
			name: "maker removed from the group",
			seed: func(t *testing.T, st *sqlite.Store, at time.Time) {
				t.Helper()
				seedMakerGroup(t, st, at)
				seedMakerGrant(t, st, at, access.Subject{Kind: access.SubjectGroup, ID: makerGroupID})
			},
			remove: func(t *testing.T, st *sqlite.Store, at time.Time) {
				t.Helper()

				e := storetest.ServerChange(at, "group:"+makerGroupID)
				if err := st.RemoveMember(t.Context(), makerGroupID, "prn-maker", e); err != nil {
					t.Fatalf("remove member: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := openMakerStore(t)
			at := makerAt()

			seedMakerToken(t, st)
			seedMakerPerson(t, st, at)
			tc.seed(t, st, at)

			svc, err := accesstoken.NewService(accesstoken.Config{
				Makers: makerChecker(t, st), TempAdmins: nil, Store: st, Principals: st, Freshness: noFreshStepGuard(t),
				Authorizer: nil, Logger: nil, Now: makerAt, NewID: nil,
				Reach: access.ReachConfig{
					Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
				},
			})
			if err != nil {
				t.Fatalf("token service: %v", err)
			}

			f := newUpdateFixtureWithAuthenticator(t, allowAll(), makerAuthn{}, fixtureOptions{
				manual: nil, checker: svc, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: nil,
				placement: nil,
			})
			paths, lease := startWithToken(t, f, "service-token")

			status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
			if status != http.StatusOK {
				t.Fatalf("renew with the stored path answers %d, want %d (%s)", status, http.StatusOK, body)
			}

			tc.remove(t, st, at)

			status, body = f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
			wantCliRefusal(t, "renew", status, body)

			status, body = f.doUpdate(t, leaseRequest(http.MethodPost, paths.Complete, lease, `{"status":"succeeded"}`))
			wantCliRefusal(t, "complete", status, body)

			wantOneMakerRevocation(t, st)
		})
	}
}

// revokeMakerGrant ends the grant of the maker with a write straight to the
// store.
func revokeMakerGrant(t *testing.T, st *sqlite.Store, at time.Time) {
	t.Helper()

	obj, err := identity.ParsePrincipalObject("prn-ci")
	if err != nil {
		t.Fatalf("parse the principal object: %v", err)
	}

	fence, e := storetest.GrantWrite(t, st, obj, at, "grant:"+makerGrantID)
	if err := st.RevokeGrant(t.Context(), makerGrantID, fence, e, nil); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
}
