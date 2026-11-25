package access_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestReachNamespacesSettingRevokedNamespaceGrant is the first probe of the
// reach setting for the housekeeping changes. A member holds a grant on the
// installation and a grant on the namespace acme; the namespace grant is then
// revoked. The member reaches an action inside acme through the installation
// grant only while the setting is true, and the operator action on the
// installation stays allowed in both rows.
func TestReachNamespacesSettingRevokedNamespaceGrant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		reaches bool
	}{
		{name: "the setting is true", value: "true", reaches: true},
		{name: "the setting is false", value: "false", reaches: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			value := tc.value
			f := newFixtureWithSettings(t, nil, reachReader{value: &value, err: nil})
			admin := identity.Principal{
				TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
				Subject:        "admin",
				DisplayName:    "",
				Admin:          true,
				TokenID:        "",
				TokenExpiresAt: time.Time{},
				Kind:           "",
				Credential:     "",
				JobID:          "",
			}
			member := f.person(t, "member")

			f.grant(t, prn("member"), access.RoleViewer, identity.ServerObject())
			f.grant(t, prn("member"), access.RoleInstallationOperator, identity.ServerObject())

			g, err := f.svc.Grant(t.Context(), testEntry(), admin, access.GrantInput{
				Object: identity.NamespaceObject("acme"), Role: access.RoleViewer, Subject: prn("member"),
			})
			if err != nil {
				t.Fatalf("grant on the namespace: %v", err)
			}

			acme := identity.NamespaceObject("acme")
			if _, err := f.svc.Revoke(t.Context(), testEntry(), admin, acme, g.ID); err != nil {
				t.Fatalf("revoke the namespace grant: %v", err)
			}

			inside := f.authz.Authorize(t.Context(), member, identity.ActionNamespaceRead,
				identity.NamespaceObject("acme"))
			if (inside == nil) != tc.reaches {
				t.Fatalf("action inside the namespace = %v, want allowed: %t", inside, tc.reaches)
			}

			operator := f.authz.Authorize(t.Context(), member, identity.ActionServerObserve,
				identity.ServerObject())
			if operator != nil {
				t.Fatalf("operator action on the installation = %v, want allowed", operator)
			}
		})
	}
}
