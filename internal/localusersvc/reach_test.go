package localusersvc_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// grantInstallation gives the principal with the ID principalID the role on
// the installation, directly in the store.
func (f *fixture) grantInstallation(t *testing.T, principalID, role, grantID string) {
	t.Helper()

	obj := identity.ServerObject()
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: grantID, Role: role, CreatedBy: "test",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}

	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:"+grantID)
	if err := f.store.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("seed grant %s: %v", grantID, err)
	}
}

// TestResetNeedsTheReachOfThePerson proves that an administrator whose
// reach does not cover the reach of the person cannot reset the password of
// the person, that the refusal is a denied audit entry, and that an
// administrator with the same reach can.
func TestResetNeedsTheReachOfThePerson(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		adminRole string
		want      error
	}{
		{name: "an administrator with no grant", adminRole: "", want: localuser.ErrOutsideReach},
		{
			name: "an account administrator resets a site administrator", adminRole: access.RoleAccountAdministrator,
			want: localuser.ErrOutsideReach,
		},
		{name: "a site administrator resets a site administrator", adminRole: access.RoleSiteAdministrator, want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			c := f.mustCreate(t, "alice", goodPassword)
			f.grantInstallation(t, c.Principal.ID, access.RoleSiteAdministrator, "grt-person")

			// The administrator is a stored principal, so that its grants
			// make its reach.
			holder := f.mustCreate(t, "bob", otherPassword)
			if tc.adminRole != "" {
				f.grantInstallation(t, holder.Principal.ID, tc.adminRole, "grt-admin")
			}

			actor := localusersvc.Actor{Entry: base("local:bob"), PrincipalID: holder.Principal.ID, Admin: false}

			_, err := f.svc.StartReset(t.Context(), actor, c.Principal.ID, localusersvc.ResetInput{
				Reason: "the person lost the password", RevokeAccessTokens: false,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("reset = %v, want %v", err, tc.want)
			}

			if want := deniedWant(tc.want); f.denied(t, localusersvc.ActionResetCreated) != want {
				t.Fatalf("denied reset entries = %d, want %d", f.denied(t, localusersvc.ActionResetCreated), want)
			}
		})
	}
}

// TestResetFailsClosedWithNoReach proves that a service whose reach config
// cannot read the grants refuses each reset, even of a person with no grant.
func TestResetFailsClosedWithNoReach(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	svc := f.serviceWith(t, access.ReachConfig{
		Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
	})

	_, err := svc.StartReset(t.Context(), admin(), c.Principal.ID, localusersvc.ResetInput{
		Reason: "the person lost the password", RevokeAccessTokens: false,
	})
	if !errors.Is(err, localuser.ErrOutsideReach) {
		t.Fatalf("reset with no reach config = %v, want ErrOutsideReach", err)
	}
}

// deniedWant returns the count of denied entries that a reset with the
// answer want writes: 1 for a refusal, 0 for a reset that passes.
func deniedWant(want error) int {
	if want == nil {
		return 0
	}

	return 1
}

// denied returns the count of the denied entries of the action.
func (f *fixture) denied(t *testing.T, action string) int {
	t.Helper()

	n := 0

	entries := f.entriesWith(t, action)
	for i := range entries {
		if entries[i].Outcome == audit.OutcomeDenied {
			n++
		}
	}

	return n
}
