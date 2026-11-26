package accesstoken_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestInstallationReach is the hostile case of the reach setting at the
// scope layer. A caller whose only grant is on the installation, with a role
// that holds a namespace action, reaches that namespace action only while
// the setting lets a grant on the installation reach namespaces. The change
// of the token of a service principal that holds the action in the namespace
// follows the same value, and the operator action on the installation stays
// allowed in both rows.
func TestInstallationReach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   bool
		refused bool
	}{
		{name: "the setting is true", value: true, refused: false},
		{name: "the setting is false", value: false, refused: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := newInstallationReachCase(t, tc.value)

			_, _, err := c.svc.Create(t.Context(), as(c.callerPerson), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: c.target.ID, Name: "ci"})

			if !tc.refused {
				if err != nil {
					t.Fatalf("create for the caller = %v, want it to pass", err)
				}

				return
			}

			checkRefusedByReachSetting(t, err, c.reach, c.callerPerson.ID, c.acme)
		})
	}
}

// installationReachCase holds the records of one row of the reach setting
// case.
type installationReachCase struct {
	callerPerson principal.Principal
	target       principal.Principal
	acme         identity.Object
	reach        access.ReachConfig
	svc          *accesstoken.Service
}

// newInstallationReachCase seeds one row of the reach setting case: the
// caller with its grants on the installation, the service principal with its
// grant of viewer in the namespace acme, and the token service whose reach
// config reads the setting of the row.
func newInstallationReachCase(t *testing.T, value bool) installationReachCase {
	t.Helper()

	f := newReachFixture(t)
	seedReachNamespace(t, f.store)

	callerPerson := f.personOf(t, "reach@example.com")
	target := f.serviceOf(t, "acme-bot")

	server := identity.ServerObject()
	acme := identity.NamespaceObject("acme")

	f.grant(t, "grt-ir-tokens", principalSubject(callerPerson), access.RoleTokenManager, server)
	f.grant(t, "grt-ir-viewer", principalSubject(callerPerson), access.RoleViewer, server)
	f.grant(t, "grt-ir-operator", principalSubject(callerPerson), access.RoleInstallationOperator, server)
	f.grant(t, "grt-ir-target", principalSubject(target), access.RoleViewer, acme)

	reach := f.reach
	reach.Settings = access.FixedSettings(value)

	return installationReachCase{
		callerPerson: callerPerson, target: target, acme: acme, reach: reach,
		svc: f.newServiceWith(t, reach, f.store),
	}
}

// seedReachNamespace stores the namespace acme in st, which the fixture of
// the reach cases holds with no namespace.
func seedReachNamespace(t *testing.T, st *sqlite.Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: "acme", PrimaryInstallation: "acme",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

// checkRefusedByReachSetting checks the refusal of a token change that the
// reach setting caused: the refusal wraps identity.ErrDenied and
// accesstoken.ErrReachExceeded and stays opaque, and the reach of the caller
// holds no namespace action through the grant on the installation, while the
// operator action on the installation stays allowed.
func checkRefusedByReachSetting(
	t *testing.T, err error, reach access.ReachConfig, callerID string, acme identity.Object,
) {
	t.Helper()

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("create = %v, want identity.ErrDenied", err)
	}

	if !errors.Is(err, accesstoken.ErrReachExceeded) {
		t.Fatalf("create = %v, want accesstoken.ErrReachExceeded", err)
	}

	// The refusal stays opaque: the reach of the caller reads no namespace,
	// so the text names no object of it.
	if !strings.Contains(err.Error(), "holds actions that you do not hold on objects that you cannot read") {
		t.Fatalf("refusal %q is not the opaque reach refusal", err)
	}

	holder, reachErr := access.ReachOf(t.Context(), reach, callerID)
	if reachErr != nil {
		t.Fatalf("reach of the caller: %v", reachErr)
	}

	inside, hasErr := holder.Has(t.Context(), identity.ActionNamespaceRead, acme)
	if hasErr != nil {
		t.Fatalf("read the reach on the namespace: %v", hasErr)
	}

	if inside {
		t.Fatal("the reach of the caller holds namespace:read on acme, want no action through @server")
	}

	operator, hasErr := holder.Has(t.Context(), identity.ActionServerObserve, identity.ServerObject())
	if hasErr != nil {
		t.Fatalf("read the reach on the installation: %v", hasErr)
	}

	if !operator {
		t.Fatal("the reach of the caller holds no operator action on @server, want it to stay allowed")
	}
}
