package access_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// nsService returns the principal that the server builds for the namespace
// acme, changed by edit.
func nsService(tb testing.TB, edit func(*identity.Principal)) identity.Principal {
	tb.Helper()

	p, err := identity.NamespaceService("acme")
	if err != nil {
		tb.Fatalf("namespace service principal of acme: %v", err)
	}

	if edit != nil {
		edit(&p)
	}

	return p
}

// TestNamespaceServicePrincipalAuthorize sends hostile principals straight to
// the Authorizer. Only the principal that the server builds, with its one
// action on a unit of its own namespace, gets through.
func TestNamespaceServicePrincipalAuthorize(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	own := unitObject(t, "acme", "proj/dev")
	other := unitObject(t, "beta", "proj/dev")

	tests := []struct {
		name   string
		p      identity.Principal
		action identity.Action
		obj    identity.Object
		allow  bool
	}{
		{"the one allowed case", nsService(t, nil), identity.ActionUnitRunPlan, own, true},
		{"other namespace", nsService(t, nil), identity.ActionUnitRunPlan, other, false},
		{
			"namespace object",
			nsService(t, nil),
			identity.ActionUnitRunPlan,
			identity.NamespaceObject("acme"),
			false,
		},
		{"server object", nsService(t, nil), identity.ActionUnitRunPlan, identity.ServerObject(), false},
		{"caller object", nsService(t, nil), identity.ActionUnitRunPlan, identity.CallerObject(), false},
		{"unit read", nsService(t, nil), identity.ActionUnitRead, own, false},
		{"unit read secrets", nsService(t, nil), identity.ActionUnitReadSecrets, own, false},
		{"unit run apply", nsService(t, nil), identity.ActionUnitRunApply, own, false},
		{
			"namespace read",
			nsService(t, nil),
			identity.ActionNamespaceRead,
			identity.NamespaceObject("acme"),
			false,
		},
		{"self action", nsService(t, nil), identity.ActionCallerRead, identity.CallerObject(), false},
		{"unclassified action", nsService(t, nil), identity.Action("server:future"), own, false},
		{
			"admin flag and access token", nsService(t, func(p *identity.Principal) {
				p.Admin, p.Credential = true, identity.CredentialAccessToken
			}),
			identity.ActionUnitRead, own, false,
		},
		{
			"admin flag and access token, the allowed action", nsService(t, func(p *identity.Principal) {
				p.Admin, p.Credential = true, identity.CredentialAccessToken
			}),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"admin flag with the server credential", nsService(t, func(p *identity.Principal) {
				p.Admin = true
			}),
			identity.ActionServerBackup, identity.ServerObject(), false,
		},
		{
			"temporary administrator", nsService(t, func(p *identity.Principal) {
				p.TemporaryAdministrator = true
			}),
			identity.ActionUnitRead, own, false,
		},
		{
			"access token credential", nsService(t, func(p *identity.Principal) {
				p.Credential = identity.CredentialAccessToken
			}),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"empty credential", nsService(t, func(p *identity.Principal) { p.Credential = "" }),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"person kind", nsService(t, func(p *identity.Principal) { p.Kind = identity.KindPerson }),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"uppercase prefix", nsService(t, func(p *identity.Principal) {
				p.Subject = "SYSTEM:NAMESPACE-SERVICE:acme"
			}),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"uppercase prefix with admin flag", nsService(t, func(p *identity.Principal) {
				p.Subject, p.Admin = "System:Namespace-Service:acme", true
			}),
			identity.ActionUnitRead, own, false,
		},
		{
			"trailing text", nsService(t, func(p *identity.Principal) {
				p.Subject = identity.NamespaceServicePrefix + "acme "
			}),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"uppercase namespace", nsService(t, func(p *identity.Principal) {
				p.Subject = identity.NamespaceServicePrefix + "ACME"
			}),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"empty namespace", nsService(t, func(p *identity.Principal) {
				p.Subject = identity.NamespaceServicePrefix
			}),
			identity.ActionUnitRunPlan, own, false,
		},
		{
			"subject of a namespace path", nsService(t, func(p *identity.Principal) {
				p.Subject = identity.NamespaceServicePrefix + "acme/beta"
			}),
			identity.ActionUnitRunPlan, own, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := f.authz.Authorize(t.Context(), tt.p, tt.action, tt.obj)

			switch {
			case tt.allow && err != nil:
				t.Fatalf("Authorize = %v, want allowed", err)
			case !tt.allow && !errors.Is(err, identity.ErrDenied):
				t.Fatalf("Authorize = %v, want ErrDenied", err)
			default:
			}
		})
	}
}

// TestNamespaceServicePrincipalIgnoresStoredGrants plants a record and grants
// for the subject of the namespace service principal, as a restore or a row
// of another version could. The Authorizer reads none of them.
func TestNamespaceServicePrincipalIgnoresStoredGrants(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	subject := identity.NamespaceServiceSubject("acme")
	f.person(t, subject)
	f.grant(t, prn(subject), access.RoleSiteAdministrator, identity.ServerObject())
	f.grant(t, prn(subject), access.RoleAdmin, identity.NamespaceObject("acme"))

	own := unitObject(t, "acme", "proj/dev")
	p := nsService(t, nil)

	assertChecks(t, f.authz, p, []check{
		{own, identity.ActionUnitRunPlan, true},
		{own, identity.ActionUnitRead, false},
		{own, identity.ActionUnitReadSecrets, false},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, false},
		{identity.ServerObject(), identity.ActionServerBackup, false},
	})

	// The same record behind another credential and the admin flag.
	hostile := nsService(t, func(p *identity.Principal) {
		p.Admin, p.Credential = true, identity.CredentialAccessToken
	})

	assertChecks(t, f.authz, hostile, []check{
		{own, identity.ActionUnitRunPlan, false},
		{own, identity.ActionUnitRead, false},
		{identity.ServerObject(), identity.ActionServerBackup, false},
	})
}

// TestNamespaceServicePrincipalListsAndExplainsNothing checks that the
// scope of every action is empty and that no action is explained, for the
// principal and for hostile forms of it.
func TestNamespaceServicePrincipalListsAndExplainsNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	subject := identity.NamespaceServiceSubject("acme")
	f.person(t, subject)
	f.grant(t, prn(subject), access.RoleSiteAdministrator, identity.ServerObject())

	own := unitObject(t, "acme", "proj/dev")

	principals := map[string]identity.Principal{
		"server credential": nsService(t, nil),
		"admin flag": nsService(t, func(p *identity.Principal) {
			p.Admin, p.Credential = true, identity.CredentialAccessToken
		}),
		"uppercase prefix": nsService(t, func(p *identity.Principal) {
			p.Subject = "SYSTEM:NAMESPACE-SERVICE:acme"
			p.Admin = true
		}),
	}

	for name, p := range principals {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertNoScope(t, f.authz, p)

			got, err := f.authz.Explain(t.Context(), p, own)
			if err != nil {
				t.Fatalf("Explain = %v", err)
			}

			if len(got.Actions) != 0 || len(got.Grants) != 0 {
				t.Fatalf("Explain = %+v, want no action and no grant", got)
			}
		})
	}
}

// assertNoScope checks that p lists no object for any action.
func assertNoScope(t *testing.T, authz *access.Authorizer, p identity.Principal) {
	t.Helper()

	for _, action := range []identity.Action{
		identity.ActionUnitRunPlan, identity.ActionUnitRead, identity.ActionNamespaceRead,
		identity.ActionServerBackup, identity.ActionPrincipalManageTokens,
	} {
		scope, err := authz.ListObjects(t.Context(), p, action)
		if err != nil {
			t.Fatalf("ListObjects(%s) = %v", action, err)
		}

		if scope.CoversEveryObject() ||
			len(scope.Namespaces)+len(scope.Collections)+len(scope.Units)+len(scope.Principals) != 0 {
			t.Fatalf("ListObjects(%s) = %+v, want an empty scope", action, scope)
		}
	}
}

// TestRunPlanIsADataAction checks that unit:run-plan is a data action, so a
// stored person can hold it through a role, and that the admin role holds it.
func TestRunPlanIsADataAction(t *testing.T) {
	t.Parallel()

	family, ok := access.FamilyOf(identity.ActionUnitRunPlan)
	if !ok || family != access.FamilyData {
		t.Fatalf("FamilyOf(unit:run-plan) = %q, %t, want data", family, ok)
	}

	role, ok := access.BuiltInRole(access.RoleAdmin)
	if !ok || !role.Has(identity.ActionUnitRunPlan) {
		t.Fatalf("role %q does not hold unit:run-plan", access.RoleAdmin)
	}
}
