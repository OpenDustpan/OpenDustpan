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
	"slices"
	"strings"
	"testing"
	"time"

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

	t.Run("admin lists every object", func(t *testing.T) {
		t.Parallel()
		testAdminListsAll(t, newAuthorizer(t))
	})

	t.Run("other principals list no object", func(t *testing.T) {
		t.Parallel()
		testListsNone(t, newAuthorizer(t))
	})

	t.Run("the list of an administrator is never wider than Authorize", func(t *testing.T) {
		t.Parallel()

		admin := identity.Principal{
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                adminSubject,
			DisplayName:            adminSubject,
			Admin:                  true,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			Kind:                   "",
			Credential:             "",
			JobID:                  "",
		}
		authz := newAuthorizer(t)

		for _, action := range testActions() {
			CheckListWithinAuthorize(t.Context(), t, ListCheck{
				Authorizer: authz, Principal: admin, Action: action, Every: testObjects(t),
			})
		}
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

	t.Run("a grant on unit A lists only unit A", func(t *testing.T) {
		t.Parallel()
		testListUnitGrant(t)
	})

	t.Run("a grant on a namespace lists that namespace", func(t *testing.T) {
		t.Parallel()
		testListNamespaceGrant(t)
	})

	t.Run("a list is never wider than Authorize", func(t *testing.T) {
		t.Parallel()
		testListWithinAuthorize(t)
	})
}

// ListCheck is one case of CheckListWithinAuthorize: the authorizer, the
// principal and the action, and Every, the objects that the test can name,
// which a scope that covers every object must all allow.
type ListCheck struct {
	Authorizer identity.Authorizer
	Principal  identity.Principal
	Action     identity.Action
	Every      []identity.Object
}

// CheckListWithinAuthorize fails the test unless Authorize allows the
// principal of c the action of c on each object that ListObjects lists for
// them, both called with ctx. For a scope that covers every object, it checks
// each object of c.Every. Run it against each Authorizer and each decorator,
// with the grants of the test, so a list never shows an object that a
// check of that object denies.
func CheckListWithinAuthorize(ctx context.Context, t *testing.T, c ListCheck) {
	t.Helper()

	authz, p, action, every := c.Authorizer, c.Principal, c.Action, c.Every

	scope, err := authz.ListObjects(ctx, p, action)
	if err != nil {
		t.Fatalf("list %s for %s: %v", action, p.Subject, err)
	}

	listed := slices.Concat(scope.Namespaces, scope.Collections, scope.Units, scope.Principals)
	if scope.CoversEveryObject() {
		listed = append(listed, every...)
	}

	for i := range listed {
		obj := listed[i]
		if err := authz.Authorize(ctx, p, action, obj); err != nil {
			t.Fatalf("%s lists %q for %s, but Authorize denies it: %v", p.Subject, obj.String(), action, err)
		}
	}
}

// CheckExactScope fails the test when the authorizer of c says that its scope
// is exact (identity.HasExactScope) and Authorize denies an object that its
// scope holds. A scope that covers every object holds each object of c.Every.
// It passes for an authorizer that does not say so: the callers of such an
// authorizer check each object. Run it against each authorizer with a
// principal that is not an administrator and, where the authorizer can give
// one, a scope that covers every object, so that a decorator cannot claim an
// exact scope that it does not have.
func CheckExactScope(ctx context.Context, t *testing.T, c ListCheck) {
	t.Helper()

	if !identity.HasExactScope(c.Authorizer) {
		return
	}

	CheckListWithinAuthorize(ctx, t, c)
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
// prove namespace-wide grants. A listed server object grants the action on
// every object. It exists for the conformance suite and for
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

	if a.allow[action][obj.String()] || a.allow[action][identity.ServerObject().String()] {
		return nil
	}

	if obj.IsUnit() {
		if a.allow[action][obj.Namespace().String()] {
			return nil
		}
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects implements identity.Authorizer. An administrator gets every
// object. The subject gets the listed objects of action, each in the set of
// its kind, and the server object as every object, as Authorize allows
// them. Any other principal gets none.
func (a *PolicyAuthorizer) ListObjects(
	_ context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	out := identity.AdminScope(p)
	if p.Admin || p.Subject != a.subject {
		return out, nil
	}

	names := make([]string, 0, len(a.allow[action]))
	for name := range a.allow[action] {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		if name == identity.CallerObject().String() {
			continue
		}

		obj, err := identity.ParseObject(name)
		if err != nil {
			return identity.AdminScope(p), fmt.Errorf("parse granted object %q: %w", name, err)
		}

		switch {
		case obj.IsServer():
			every := identity.Every()
			every.Namespaces = out.Namespaces
			every.Collections = out.Collections
			every.Units = out.Units
			every.Principals = out.Principals
			out = every
		case obj.IsPrincipal():
			out.Principals = append(out.Principals, obj)
		case obj.IsUnit():
			out.Units = append(out.Units, obj)
		case obj.IsCollection():
			out.Collections = append(out.Collections, obj)
		default:
			out.Namespaces = append(out.Namespaces, obj)
		}
	}

	return out, nil
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
		identity.ActionUnitWriteForced,
		identity.ActionUnitWriteUnlocked,
		identity.ActionUnitManage,
		identity.ActionUnitEncrypt,
		identity.ActionUnitDecrypt,
		identity.ActionUnitReadSecrets,
		identity.ActionCallerRead,
		identity.ActionServerBackup,
		identity.ActionServerObserve,
		identity.ActionServerMaintenance,
		identity.ActionServerRestore,
		identity.ActionServerRestoreAccess,
	}
}

// testAdminPasses proves an administrator passes every action on every object.
func testAdminPasses(t *testing.T, authz identity.Authorizer) {
	t.Helper()

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        adminSubject,
		DisplayName:    adminSubject,
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

	objs := testObjects(t)
	for i := range objs {
		obj := objs[i]
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

	viewer := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        viewerSubject,
		DisplayName:    viewerSubject,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

	objs := testObjects(t)
	for i := range objs {
		obj := objs[i]
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

	viewer := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        viewerSubject,
		DisplayName:    viewerSubject,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

	objs := testObjects(t)
	for i := range objs {
		obj := objs[i]
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

	viewer := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        viewerSubject,
		DisplayName:    viewerSubject,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

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
	alice := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        aliceSubject,
		DisplayName:    aliceSubject,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

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
	alice := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        aliceSubject,
		DisplayName:    aliceSubject,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

	objs := []identity.Object{insideA, insideB}
	for i := range objs {
		obj := objs[i]
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
	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        adminSubject,
		DisplayName:    adminSubject,
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

	if err := authz.Authorize(t.Context(), admin, identity.ActionUnitRead, unitB); err != nil {
		t.Fatalf("admin unit:read on unit B: %v", err)
	}
}

// testAdminListsAll proves ListObjects gives an administrator every object
// of each action.
func testAdminListsAll(t *testing.T, authz identity.Authorizer) {
	t.Helper()

	admin := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                adminSubject,
		DisplayName:            adminSubject,
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}

	for _, action := range testActions() {
		scope, err := authz.ListObjects(t.Context(), admin, action)
		if err != nil {
			t.Fatalf("admin list %s: %v", action, err)
		}

		if !scope.CoversEveryObject() {
			t.Fatalf("admin list %s: all = false, want true", action)
		}
	}
}

// testListsNone proves ListObjects gives a principal without admin and
// without grants no object and no error.
func testListsNone(t *testing.T, authz identity.Authorizer) {
	t.Helper()

	viewer := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                viewerSubject,
		DisplayName:            viewerSubject,
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}

	for _, action := range testActions() {
		scope, err := authz.ListObjects(t.Context(), viewer, action)
		if err != nil {
			t.Fatalf("viewer list %s: %v", action, err)
		}

		if !scope.IsEmpty() {
			t.Fatalf("viewer list %s = %+v, want no object", action, scope)
		}
	}
}

// testListUnitGrant proves ListObjects gives a grant on unit A as unit A
// only, and gives nothing for an action with no grant.
func testListUnitGrant(t *testing.T) {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	unitA, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit A: %v", err)
	}

	authz := NewPolicyAuthorizer(aliceSubject, map[identity.Action][]string{
		identity.ActionUnitRead: {unitA.String()},
	})
	alice := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                aliceSubject,
		DisplayName:            aliceSubject,
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}

	scope, err := authz.ListObjects(t.Context(), alice, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("alice list unit:read: %v", err)
	}

	if scope.CoversEveryObject() || len(scope.Namespaces) != 0 || len(scope.Collections) != 0 ||
		len(scope.Principals) != 0 || len(scope.Units) != 1 ||
		scope.Units[0].String() != unitA.String() {
		t.Fatalf("alice list unit:read = %+v, want unit %q only", scope, unitA.String())
	}

	scope, err = authz.ListObjects(t.Context(), alice, identity.ActionUnitWrite)
	if err != nil {
		t.Fatalf("alice list unit:write: %v", err)
	}

	if !scope.IsEmpty() {
		t.Fatalf("alice list unit:write = %+v, want no object", scope)
	}
}

// testListNamespaceGrant proves ListObjects gives a grant on a namespace as
// that namespace, and gives another subject nothing.
func testListNamespaceGrant(t *testing.T) {
	t.Helper()

	acme, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	authz := NewPolicyAuthorizer(aliceSubject, map[identity.Action][]string{
		identity.ActionUnitRead: {identity.NamespaceObject(acme).String()},
	})
	alice := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                aliceSubject,
		DisplayName:            aliceSubject,
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}

	scope, err := authz.ListObjects(t.Context(), alice, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("alice list unit:read: %v", err)
	}

	if scope.CoversEveryObject() || len(scope.Units) != 0 || len(scope.Namespaces) != 1 ||
		scope.Namespaces[0].Namespace() != acme {
		t.Fatalf("alice list unit:read = %+v, want namespace %q only", scope, acme)
	}

	viewer := alice
	viewer.Subject = viewerSubject

	scope, err = authz.ListObjects(t.Context(), viewer, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("viewer list unit:read: %v", err)
	}

	if !scope.IsEmpty() {
		t.Fatalf("viewer list unit:read = %+v, want no object", scope)
	}
}

// testListWithinAuthorize proves for the policy authorizer and for
// SelfAllowed around it that each object that a grant lists passes
// Authorize: a grant on a unit, on a namespace, and on the installation.
func testListWithinAuthorize(t *testing.T) {
	t.Helper()

	acme, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	unitA, err := identity.UnitObject("pulumi", acme, "web/prod")
	if err != nil {
		t.Fatalf("unit A: %v", err)
	}

	authz := NewPolicyAuthorizer(aliceSubject, map[identity.Action][]string{
		identity.ActionUnitRead:      {unitA.String()},
		identity.ActionNamespaceRead: {identity.NamespaceObject(acme).String()},
		identity.ActionServerBackup:  {identity.ServerObject().String()},
	})
	alice := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                aliceSubject,
		DisplayName:            aliceSubject,
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}

	for _, under := range []identity.Authorizer{
		authz,
		identity.SelfAllowed{Next: authz, Actions: []identity.Action{identity.ActionTokenManageOwn}},
	} {
		for _, action := range testActions() {
			CheckListWithinAuthorize(t.Context(), t, ListCheck{
				Authorizer: under, Principal: alice, Action: action, Every: testObjects(t),
			})
		}
	}
}
