package dustpan_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
)

// allowEverything is an authorizer of an embedder that allows every check.
type allowEverything struct{}

func (allowEverything) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

func (allowEverything) ListObjects(_ context.Context, _ identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.Every(), nil
}

// TestWiredAuthorizerKeepsTheNamespaceServiceRule wires an authorizer that
// allows everything, as an embedder can, and proves that the wired authorizer
// still gives the namespace service principal its one action only.
func TestWiredAuthorizerKeepsTheNamespaceServiceRule(t *testing.T) {
	t.Parallel()

	svc, err := identity.NamespaceService("acme")
	if err != nil {
		t.Fatalf("namespace service principal: %v", err)
	}

	own, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	for _, tc := range []struct {
		name   string
		action identity.Action
		obj    identity.Object
		allow  bool
	}{
		{name: "the one action", action: identity.ActionUnitRunPlan, obj: own, allow: true},
		{name: "a read of the unit", action: identity.ActionUnitRead, obj: own, allow: false},
		{name: "a self action", action: identity.ActionTokenManageOwn, obj: identity.CallerObject(), allow: false},
		{name: "a server action", action: identity.ActionServerBackup, obj: identity.ServerObject(), allow: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := dustpan.AuthorizeWired(allowEverything{}, svc, tc.action, tc.obj)
			if tc.allow != (err == nil) || (!tc.allow && !errors.Is(err, identity.ErrDenied)) {
				t.Errorf("Authorize(%s, %s) = %v, want allowed %t", tc.action, tc.obj, err, tc.allow)
			}
		})
	}
}
