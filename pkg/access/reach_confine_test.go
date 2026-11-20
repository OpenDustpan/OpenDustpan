package access_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errReachPrincipal fails the second read of a principal in the tests.
var errReachPrincipal = errors.New("reach principal read failed")

// failAfterPrincipals reads principals from the port until reads are used
// up, then fails every read.
type failAfterPrincipals struct {
	access.PrincipalNamespaces

	reads *atomic.Int64
	limit int64
}

func (p failAfterPrincipals) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	if p.reads.Add(1) == p.limit {
		return principal.Principal{}, errReachPrincipal
	}

	return p.PrincipalNamespaces.GetPrincipal(ctx, id)
}

// TestReachConfinesNamespacePrincipalRows proves that the reach walker
// applies the confinement of the Authorizer: a planted grant of a principal
// of a namespace on the installation or on another namespace, and a planted
// group membership of such a principal, widen no reach.
func TestReachConfinesNamespacePrincipalRows(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	namespaceEscalation(t, f)
	team := f.group(t, "team")
	f.grant(t, team, access.RoleSiteAdministrator, identity.ServerObject())
	f.grant(t, team, access.RoleViewer, identity.NamespaceObject("beta"))

	cfg := f.reach()
	cfg.Callers = groupCallers{Callers: f.store, subject: "ci-acme", group: team.ID}

	reach, err := access.ReachOf(t.Context(), cfg, "prn-ci-acme")
	if err != nil {
		t.Fatalf("reach of the namespace principal: %v", err)
	}

	for _, tc := range []struct {
		name   string
		action identity.Action
		obj    identity.Object
		want   bool
	}{
		{"foreign grant on the installation", identity.ActionServerBackup, identity.ServerObject(), false},
		{"foreign grant on another namespace", identity.ActionNamespaceRead, identity.NamespaceObject("beta"), false},
		{"group grant on the installation", identity.ActionServerBackup, identity.ServerObject(), false},
		{"own namespace", identity.ActionNamespaceRead, identity.NamespaceObject("acme"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := reach.Has(t.Context(), tc.action, tc.obj)
			if err != nil {
				t.Fatalf("has: %v", err)
			}

			if got != tc.want {
				t.Fatalf("reach has %s on %s = %v, want %v", tc.action, tc.obj, got, tc.want)
			}
		})
	}
}

// TestReachFailsClosedOnAFailedConfinementRead proves that a failed read of
// a principal during the confinement returns the error and no reach.
func TestReachFailsClosedOnAFailedConfinementRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	namespaceEscalation(t, f)

	cfg := f.reach()
	// The first read is the visit of the principal; the second is the
	// confinement of its grants.
	cfg.Principals = failAfterPrincipals{PrincipalNamespaces: f.store, reads: new(atomic.Int64), limit: 2}

	_, err := access.ReachOf(t.Context(), cfg, "prn-ci-acme")
	if !errors.Is(err, errReachPrincipal) || !strings.Contains(err.Error(), "confine grants") {
		t.Fatalf("reach = %v, want the principal read error of the grant confinement", err)
	}
}
