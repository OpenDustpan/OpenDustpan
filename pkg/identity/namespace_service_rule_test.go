package identity_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/identity/identitytest"
)

// allowAll is a hostile Authorizer that allows every check, as an Authorizer
// that does not know the namespace service rule might. It records whether it
// was asked.
type allowAll struct{ asked *int }

func (a allowAll) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	*a.asked++

	return nil
}

func (a allowAll) ListObjects(_ context.Context, _ identity.Principal, _ identity.Action) (identity.Scope, error) {
	*a.asked++

	return identity.Every(), nil
}

// TestNamespaceServiceRuleSuite runs the namespace service principal suite
// against the rule around an Authorizer that allows everything: the rule alone
// keeps the principal to its one action.
func TestNamespaceServiceRuleSuite(t *testing.T) {
	t.Parallel()

	identitytest.RunNamespaceService(t, func(t *testing.T) identity.Authorizer {
		t.Helper()

		return identity.NamespaceServiceRule{Next: allowAll{asked: new(int)}}
	})
}

// TestNamespaceServiceRuleGenericSuite runs the generic suite against the rule
// around the default: the rule changes nothing for other principals.
func TestNamespaceServiceRuleGenericSuite(t *testing.T) {
	t.Parallel()

	identitytest.Run(t, func(t *testing.T) identity.Authorizer {
		t.Helper()

		return identity.NamespaceServiceRule{Next: identity.AdminOnly{}}
	})
}

// TestNamespaceServiceRuleNeverAsksNext proves that Next never sees a check or
// a list of a principal with the subject of a namespace service principal,
// and that it sees every other principal.
func TestNamespaceServiceRuleNeverAsksNext(t *testing.T) {
	t.Parallel()

	svc, err := identity.NamespaceService("acme")
	if err != nil {
		t.Fatalf("namespace service principal: %v", err)
	}

	forged := svc
	forged.Credential, forged.Admin = identity.CredentialAccessToken, true

	other := svc
	other.Subject = "user:ada"

	for _, tc := range []struct {
		name      string
		p         identity.Principal
		wantAsked int
	}{
		{name: "the server principal", p: svc, wantAsked: 0},
		{name: "a forged principal with the subject", p: forged, wantAsked: 0},
		{name: "another principal", p: other, wantAsked: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			asked := 0
			rule := identity.NamespaceServiceRule{Next: allowAll{asked: &asked}}

			authErr := rule.Authorize(t.Context(), tc.p, identity.ActionServerBackup, identity.ServerObject())
			scope, listErr := rule.ListObjects(t.Context(), tc.p, identity.ActionUnitRead)

			if asked != tc.wantAsked {
				t.Errorf("Next asked %d times, want %d", asked, tc.wantAsked)
			}

			if tc.wantAsked == 0 &&
				(!errors.Is(authErr, identity.ErrDenied) || listErr != nil || scope.CoversEveryObject()) {
				t.Errorf(
					"Authorize = %v, ListObjects = %+v, %v; want a denial and an empty scope",
					authErr, scope, listErr,
				)
			}
		})
	}
}
