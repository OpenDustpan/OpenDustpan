package identitytest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// nsCase is one check of a principal with the subject of a namespace service
// principal.
type nsCase struct {
	name   string
	edit   func(p *identity.Principal)
	action identity.Action
	obj    identity.Object
	allow  bool
}

// RunNamespaceService executes the namespace service principal suite against
// Authorizers produced by newAuthorizer. The factory gives an Authorizer with
// no grant. The principal that NamespaceService builds for acme may do
// ActionUnitRunPlan on a unit of acme. Every other action and object, and every
// principal with such a subject that the server did not build, is denied with
// ErrDenied.
func RunNamespaceService(t *testing.T, newAuthorizer AuthorizerFactory) {
	t.Helper()

	cases := nsCases(t, mustUnit(t, "acme", "web/prod"))

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := identity.NamespaceService("acme")
			if err != nil {
				t.Fatalf("namespace service principal of acme: %v", err)
			}

			if tc.edit != nil {
				tc.edit(&p)
			}

			err = newAuthorizer(t).Authorize(t.Context(), p, tc.action, tc.obj)

			if tc.allow && err != nil {
				t.Errorf("Authorize(%s, %s, %s) = %v, want allowed", p.Subject, tc.action, tc.obj, err)
			}

			if !tc.allow && !errors.Is(err, identity.ErrDenied) {
				t.Errorf("Authorize(%s, %s, %s) = %v, want ErrDenied", p.Subject, tc.action, tc.obj, err)
			}
		})
	}
}

// nsCases returns the cases of RunNamespaceService. own is a unit of acme.
func nsCases(t *testing.T, own identity.Object) []nsCase {
	t.Helper()

	other := mustUnit(t, "beta", "web/prod")
	admin := func(p *identity.Principal) { p.Admin = true }
	actions := testActions()
	forged := forgedEdits()

	cases := make([]nsCase, 0, 8+2*len(actions)+len(forged))
	cases = append(
		cases,
		nsCase{
			name: "the one action on a unit of its namespace", edit: nil, action: identity.ActionUnitRunPlan,
			obj: own, allow: true,
		},
		nsCase{
			name: "a unit of another namespace", edit: nil, action: identity.ActionUnitRunPlan,
			obj: other, allow: false,
		},
		nsCase{
			name: "the namespace object", edit: nil, action: identity.ActionUnitRunPlan,
			obj: identity.NamespaceObject("acme"), allow: false,
		},
		nsCase{
			name: "the server object", edit: nil, action: identity.ActionUnitRunPlan,
			obj: identity.ServerObject(), allow: false,
		},
		nsCase{name: "the apply action", edit: nil, action: identity.ActionUnitRunApply, obj: own, allow: false},
		nsCase{name: "an unknown action", edit: nil, action: identity.Action("server:future"), obj: own, allow: false},
		nsCase{
			name: "the admin flag on the server", edit: admin, action: identity.ActionServerBackup,
			obj: identity.ServerObject(), allow: false,
		},
		nsCase{
			name: "the admin flag on another namespace", edit: admin, action: identity.ActionUnitRunPlan,
			obj: other, allow: false,
		},
	)

	for _, action := range actions {
		cases = append(cases,
			nsCase{name: "action " + string(action), edit: nil, action: action, obj: own, allow: false},
			nsCase{
				name: "action " + string(action) + " with the admin flag", edit: admin, action: action,
				obj: own, allow: false,
			},
		)
	}

	for name, edit := range forged {
		cases = append(
			cases,
			nsCase{name: name, edit: edit, action: identity.ActionUnitRunPlan, obj: own, allow: false},
		)
	}

	return cases
}

// forgedEdits returns the changes that make a principal with the subject of a
// namespace service principal one that the server did not build.
func forgedEdits() map[string]func(p *identity.Principal) {
	return map[string]func(p *identity.Principal){
		"an access token credential": func(p *identity.Principal) { p.Credential = identity.CredentialAccessToken },
		"an empty credential":        func(p *identity.Principal) { p.Credential = "" },
		"a person kind":              func(p *identity.Principal) { p.Kind = identity.KindPerson },
		"an uppercase prefix":        func(p *identity.Principal) { p.Subject = "SYSTEM:NAMESPACE-SERVICE:acme" },
		"trailing text":              func(p *identity.Principal) { p.Subject = identity.NamespaceServicePrefix + "acme " },
		"an empty namespace":         func(p *identity.Principal) { p.Subject = identity.NamespaceServicePrefix },
		"a namespace path": func(p *identity.Principal) {
			p.Subject = identity.NamespaceServicePrefix + "acme/beta"
		},
		"an access token and the admin flag": func(p *identity.Principal) {
			p.Credential, p.Admin = identity.CredentialAccessToken, true
		},
	}
}

// mustUnit returns the object of the Pulumi unit name in the namespace ns.
func mustUnit(t *testing.T, ns, name string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("pulumi", namespace.Name(ns), name)
	if err != nil {
		t.Fatalf("unit %s/%s: %v", ns, name, err)
	}

	return obj
}
