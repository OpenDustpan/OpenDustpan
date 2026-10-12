// Package identitytest is the conformance suite for identity.Authenticator
// and identity.Authorizer implementations. Every built-in adapter runs it,
// and custom adapters should too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		identitytest.Run(t, func(t *testing.T) identity.Authorizer { return newMyAuthorizer(t) })
//	}
//
// Run covers the generic contract: administrators pass on every object and
// every other principal fails with ErrDenied. RunObjectSensitive covers the
// object contract with a policy authorizer: a grant on one unit allows only
// that unit, and a grant on a namespace allows the units in it.
package identitytest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// AuthorizerFactory returns a fresh Authorizer for one subtest.
type AuthorizerFactory func(t *testing.T) identity.Authorizer

// AuthenticatorFactory returns a fresh Authenticator and one valid token
// for it for one subtest.
type AuthenticatorFactory func(t *testing.T) (identity.Authenticator, string)

// Run executes the generic Authorizer suite against Authorizers produced by
// newAuthorizer.
func Run(t *testing.T, newAuthorizer AuthorizerFactory) {
	t.Helper()

	t.Run("admin passes on every object", func(t *testing.T) {
		t.Parallel()
		testAdminPasses(t, newAuthorizer(t))
	})

	t.Run("other principals fail with ErrDenied on every object", func(t *testing.T) {
		t.Parallel()
		testDenied(t, newAuthorizer(t))
	})

	t.Run("denials name the object", func(t *testing.T) {
		t.Parallel()
		testDenialNamesObject(t, newAuthorizer(t))
	})

	t.Run("unit A and unit B are distinct objects", func(t *testing.T) {
		t.Parallel()
		testUnitsDistinct(t, newAuthorizer(t))
	})
}

// RunAuthenticator executes the generic Authenticator suite against
// Authenticators produced by newAuthenticator.
func RunAuthenticator(t *testing.T, newAuthenticator AuthenticatorFactory) {
	t.Helper()

	t.Run("valid token signs in", func(t *testing.T) {
		t.Parallel()

		authn, token := newAuthenticator(t)
		testValidToken(t, authn, token)
	})

	t.Run("unknown token is ErrUnauthenticated", func(t *testing.T) {
		t.Parallel()

		authn, token := newAuthenticator(t)
		testUnknownToken(t, authn, token)
	})

	t.Run("empty token is ErrUnauthenticated", func(t *testing.T) {
		t.Parallel()

		authn, token := newAuthenticator(t)
		testEmptyToken(t, authn, token)
	})
}

// RunObjectSensitive executes the object-sensitive suite against the policy
// Authorizer: a grant on unit A allows A and denies B, and a grant on a
// namespace allows the units in it but no unit outside it.
func RunObjectSensitive(t *testing.T) {
	t.Helper()

	t.Run("grant on unit A denies unit B", func(t *testing.T) {
		t.Parallel()
		testUnitGrant(t)
	})

	t.Run("grant on a namespace allows its units", func(t *testing.T) {
		t.Parallel()
		testNamespaceGrant(t)
	})

	t.Run("admin passes despite grants", func(t *testing.T) {
		t.Parallel()
		testAdminDespiteGrants(t)
	})
}

// testSubjects names the principals the suite signs in as.
const (
	adminSubject  = "admin"
	viewerSubject = "viewer"
	aliceSubject  = "alice"
)

// PolicyAuthorizer is an object-sensitive test Authorizer. It allows
// administrators on every object, allows one subject the listed (action,
// object) pairs, and denies the rest with ErrDenied. A listed namespace
// object grants the action on every unit in that namespace, so tests can
// prove namespace-wide grants. It exists for the conformance suite and for
// route tests that must prove which object a route authorizes.
type PolicyAuthorizer struct {
	subject string
	allow   map[identity.Action]map[string]bool
}

var _ identity.Authorizer = (*PolicyAuthorizer)(nil)

// NewPolicyAuthorizer returns a PolicyAuthorizer for subject with the listed
// grants: allow maps each action to the wire forms of the objects it allows.
func NewPolicyAuthorizer(subject string, allow map[identity.Action][]string) *PolicyAuthorizer {
	set := make(map[identity.Action]map[string]bool, len(allow))
	for action, objects := range allow {
		got := make(map[string]bool, len(objects))
		for _, obj := range objects {
			got[obj] = true
		}
		set[action] = got
	}

	return &PolicyAuthorizer{subject: subject, allow: set}
}

// Authorize implements identity.Authorizer.
func (a *PolicyAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if p.Admin {
		return nil
	}

	if p.Subject != a.subject {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	if a.allow[action][obj.String()] {
		return nil
	}

	if obj.IsUnit() {
		if a.allow[action][obj.Namespace().String()] {
			return nil
		}
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// testObjects returns the objects every generic case runs: a namespace, two
// units in it, the caller itself and the installation itself.
func testObjects(t *testing.T) []identity.Object {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	unitA, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit A: %v", err)
	}

	unitB, err := identity.UnitObject("pulumi", ns, "web/dev")
	if err != nil {
		t.Fatalf("unit B: %v", err)
	}

	return []identity.Object{
		identity.NamespaceObject(ns),
		unitA,
		unitB,
		identity.CallerObject(),
		identity.ServerObject(),
	}
}

// testActions returns every core action the generic cases run.
func testActions() []identity.Action {
	return []identity.Action{
		identity.ActionNamespaceRead,
		identity.ActionNamespaceAdmin,
		identity.ActionNamespaceReadAudit,
		identity.ActionUnitRead,
		identity.ActionUnitWrite,
		identity.ActionUnitManage,
		identity.ActionUnitEncrypt,
		identity.ActionUnitDecrypt,
		identity.ActionUnitReadSecrets,
		identity.ActionCallerRead,
		identity.ActionServerBackup,
		identity.ActionServerObserve,
		identity.ActionServerMaintenance,
		identity.ActionServerRestore,
	}
}

// testAdminPasses proves an administrator passes every action on every object.
func testAdminPasses(t *testing.T, authz identity.Authorizer) {
	t.Helper()

	admin := identity.Principal{Subject: adminSubject, DisplayName: adminSubject, Admin: true}

	for _, obj := range testObjects(t) {
		for _, action := range testActions() {
			if err := authz.Authorize(t.Context(), admin, action, obj); err != nil {
				t.Fatalf("admin %s on %q: %v", action, obj.String(), err)
			}
		}
	}
}

// testDenied proves a principal without admin fails every action on every
// object with ErrDenied.
func testDenied(t *testing.T, authz identity.Authorizer) {
	t.Helper()

	viewer := identity.Principal{Subject: viewerSubject, DisplayName: viewerSubject, Admin: false}

	for _, obj := range testObjects(t) {
		for _, action := range testActions() {
			if err := authz.Authorize(t.Context(), viewer, action, obj); !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("viewer %s on %q = %v, want ErrDenied", action, obj.String(), err)
			}
		}
	}
}

// testDenialNamesObject proves a denial names the object it denied, so a
// caller can tell which of several checks failed.
func testDenialNamesObject(t *testing.T, authz identity.Authorizer) {
	t.Helper()

	viewer := identity.Principal{Subject: viewerSubject, DisplayName: viewerSubject, Admin: false}

	for _, obj := range testObjects(t) {
		err := authz.Authorize(t.Context(), viewer, identity.ActionUnitRead, obj)
		if err == nil || !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("viewer unit:read on %q = %v, want ErrDenied", obj.String(), err)
		}

		want := `"` + obj.String() + `"`
		if got := err.Error(); !strings.Contains(got, want) {
			t.Fatalf("denial %q lacks object %q", got, want)
		}
	}
}

// testUnitsDistinct proves the two unit fixtures differ: a suite that only
// ever authorizes one object cannot tell them apart, so the check fails
// closed when they compare equal.
func testUnitsDistinct(t *testing.T, authz identity.Authorizer) {
	t.Helper()

	objects := testObjects(t)
	unitA, unitB := objects[1], objects[2]

	if unitA.String() == unitB.String() {
		t.Fatalf("unit A %q equals unit B", unitA.String())
	}

	viewer := identity.Principal{Subject: viewerSubject, DisplayName: viewerSubject, Admin: false}

	errA := authz.Authorize(t.Context(), viewer, identity.ActionUnitRead, unitA)
	errB := authz.Authorize(t.Context(), viewer, identity.ActionUnitRead, unitB)

	if errA == nil || errB == nil || !errors.Is(errA, identity.ErrDenied) || !errors.Is(errB, identity.ErrDenied) {
		t.Fatalf("viewer denials = (%v, %v), want ErrDenied on both units", errA, errB)
	}

	if errA.Error() == errB.Error() {
		t.Fatalf("denials on distinct units compare equal: %q", errA.Error())
	}
}

// testValidToken proves the valid token signs in.
func testValidToken(t *testing.T, authn identity.Authenticator, token string) {
	t.Helper()

	got, err := authn.AuthenticateToken(t.Context(), token)
	if err != nil {
		t.Fatalf("valid token: %v", err)
	}

	if got.Subject == "" {
		t.Fatal("valid token signs in with an empty subject")
	}
}

// testUnknownToken proves a wrong token is ErrUnauthenticated.
func testUnknownToken(t *testing.T, authn identity.Authenticator, _ string) {
	t.Helper()

	const wrongToken = "wrong-token-that-is-long-enough-to-pass-length"

	_, err := authn.AuthenticateToken(t.Context(), wrongToken)
	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("wrong token = %v, want ErrUnauthenticated", err)
	}
}

// testEmptyToken proves an empty token is ErrUnauthenticated.
func testEmptyToken(t *testing.T, authn identity.Authenticator, _ string) {
	t.Helper()

	if _, err := authn.AuthenticateToken(t.Context(), ""); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("empty token = %v, want ErrUnauthenticated", err)
	}
}

// testUnitGrant proves a grant on unit A allows A and still denies unit B for
// the same subject and action.
func testUnitGrant(t *testing.T) {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	unitA, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit A: %v", err)
	}

	unitB, err := identity.UnitObject("pulumi", ns, "web/dev")
	if err != nil {
		t.Fatalf("unit B: %v", err)
	}

	authz := NewPolicyAuthorizer(aliceSubject, map[identity.Action][]string{
		identity.ActionUnitRead: {unitA.String()},
	})
	alice := identity.Principal{Subject: aliceSubject, DisplayName: aliceSubject, Admin: false}

	if err := authz.Authorize(t.Context(), alice, identity.ActionUnitRead, unitA); err != nil {
		t.Fatalf("alice unit:read on unit A: %v", err)
	}

	if err := authz.Authorize(t.Context(), alice, identity.ActionUnitRead, unitB); !errors.Is(
		err, identity.ErrDenied,
	) {
		t.Fatalf("alice unit:read on unit B = %v, want ErrDenied", err)
	}

	if err := authz.Authorize(t.Context(), alice, identity.ActionUnitWrite, unitA); !errors.Is(
		err, identity.ErrDenied,
	) {
		t.Fatalf("alice unit:write on unit A = %v, want ErrDenied", err)
	}
}

// testNamespaceGrant proves a grant on a namespace allows the units in it but
// denies a unit in another namespace.
func testNamespaceGrant(t *testing.T) {
	t.Helper()

	acme, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	other, err := namespace.ParseName("other")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	insideA, err := identity.UnitObject("pulumi", acme, "web/prod")
	if err != nil {
		t.Fatalf("unit in acme: %v", err)
	}

	insideB, err := identity.UnitObject("opentofu", acme, "network")
	if err != nil {
		t.Fatalf("unit in acme: %v", err)
	}

	outside, err := identity.UnitObject("pulumi", other, "web/prod")
	if err != nil {
		t.Fatalf("unit in other: %v", err)
	}

	authz := NewPolicyAuthorizer(aliceSubject, map[identity.Action][]string{
		identity.ActionUnitRead: {identity.NamespaceObject(acme).String()},
	})
	alice := identity.Principal{Subject: aliceSubject, DisplayName: aliceSubject, Admin: false}

	for _, obj := range []identity.Object{insideA, insideB} {
		if err := authz.Authorize(t.Context(), alice, identity.ActionUnitRead, obj); err != nil {
			t.Fatalf("alice unit:read on %q in acme: %v", obj.String(), err)
		}
	}

	if err := authz.Authorize(t.Context(), alice, identity.ActionUnitRead, outside); !errors.Is(
		err, identity.ErrDenied,
	) {
		t.Fatalf("alice unit:read on %q = %v, want ErrDenied", outside.String(), err)
	}
}

// testAdminDespiteGrants proves an administrator passes on units no grant
// lists.
func testAdminDespiteGrants(t *testing.T) {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	unitB, err := identity.UnitObject("pulumi", ns, "web/dev")
	if err != nil {
		t.Fatalf("unit B: %v", err)
	}

	authz := NewPolicyAuthorizer(aliceSubject, map[identity.Action][]string{})
	admin := identity.Principal{Subject: adminSubject, DisplayName: adminSubject, Admin: true}

	if err := authz.Authorize(t.Context(), admin, identity.ActionUnitRead, unitB); err != nil {
		t.Fatalf("admin unit:read on unit B: %v", err)
	}
}
