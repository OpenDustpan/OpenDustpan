package identity_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/identity/identitytest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// testAuthorizer allows administrators and denies everyone else, like
// AdminOnly but written independently for the conformance check.
type testAuthorizer struct{}

func (testAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if p.Admin {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (testAuthorizer) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: p.Admin}, nil
}

func mustName(t *testing.T, s string) namespace.Name {
	t.Helper()

	ns, err := namespace.ParseName(s)
	if err != nil {
		t.Fatalf("parse namespace %q: %v", s, err)
	}

	return ns
}

func TestActionsAreToolNeutral(t *testing.T) {
	t.Parallel()

	for _, a := range []identity.Action{
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
		identity.ActionServerRestoreAccess,
		identity.ActionServerReadPrincipals,
		identity.ActionServerManagePrincipals,
		identity.ActionServerManageGroups,
		identity.ActionUnitRunPlan,
		identity.ActionUnitRunApply,
		identity.ActionUnitRunDestroy,
		identity.ActionUnitWriteBack,
		identity.ActionUnitDefineSteps,
		identity.ActionJobRead,
		identity.ActionJobReadLogs,
		identity.ActionJobReadPlans,
		identity.ActionJobCancel,
		identity.ActionJobApprove,
		identity.ActionNamespaceManageFreezes,
		identity.ActionNamespaceBreakFreeze,
		identity.ActionNamespaceManageRunners,
		identity.ActionRunnerClaim,
		identity.ActionAgentUse,
		identity.ActionAgentStartRun,
		identity.ActionNamespaceManageAgentRuns,
		identity.ActionNamespaceManageAgent,
		identity.ActionServerManageAgent,
		identity.ActionServerAcceptAgentSecretAccess,
		identity.ActionCallerManageAgentOptIns,
		identity.ActionUnitObserve,
		identity.ActionNamespaceReadRunners,
		identity.ActionRunnerJoin,
		identity.ActionNamespaceReadUnmanaged,
		identity.ActionNamespaceTriageUnmanaged,
		identity.ActionNamespaceManageSweeps,
		identity.ActionNamespaceStartSweep,
		identity.ActionServerLinkNamespaces,
		identity.ActionUnitSetContext,
		identity.ActionCollectionSetContext,
		identity.ActionNamespaceSetContext,
		identity.ActionBlueprintRead,
		identity.ActionBlueprintSetContext,
		identity.ActionUnitRedact,
		identity.ActionProfileRead,
		identity.ActionProfileWrite,
		identity.ActionProfileOpen,
		identity.ActionProfileManage,
	} {
		lowered := strings.ToLower(string(a))
		if strings.Contains(lowered, "stack") || strings.Contains(lowered, "pulumi") {
			t.Fatalf("action %q names a tool", a)
		}
	}
}

// validCase is one reference that ParseObject must accept.
type validCase struct {
	name       string
	raw        string
	wantNS     string
	wantTool   string
	wantUnit   string
	wantIsUnit bool
}

func TestParseObjectValid(t *testing.T) {
	t.Parallel()

	valid := []validCase{
		{"namespace", "acme", "acme", "", "", false},
		{"unit with slash", "pulumi:acme/web/prod", "acme", "pulumi", "web/prod", true},
		{"unit without slash", "opentofu:acme/network", "acme", "opentofu", "network", true},
	}

	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkValidObject(t, tc)
		})
	}
}

func TestParseObjectInvalid(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"",
		"@caller",
		"@server/x",
		"pulumi:",
		"pulumi:acme",
		"pulumi:/web",
		"pulumi:acme/",
		":acme/web",
		"pulumi:acme/web:prod",
		"acme/",
		"acme/web/prod",
		"acme/we b",
		"acme/web\n",
		"acme/.web",
		"/web",
		"../acme",
	}

	for _, raw := range invalid {
		t.Run("invalid "+raw, func(t *testing.T) {
			t.Parallel()

			if _, err := identity.ParseObject(raw); !errors.Is(err, identity.ErrInvalidObject) {
				t.Fatalf("parse %q = %v, want ErrInvalidObject", raw, err)
			}
		})
	}
}

func checkValidObject(t *testing.T, tc validCase) {
	t.Helper()

	obj, err := identity.ParseObject(tc.raw)
	if err != nil {
		t.Fatalf("parse %q: %v", tc.raw, err)
	}

	if obj.String() != tc.raw {
		t.Fatalf("String() = %q, want %q", obj.String(), tc.raw)
	}

	if obj.IsUnit() != tc.wantIsUnit {
		t.Fatalf("IsUnit() = %v, want %v", obj.IsUnit(), tc.wantIsUnit)
	}

	if obj.Namespace().String() != tc.wantNS {
		t.Fatalf("Namespace() = %q, want %q", obj.Namespace(), tc.wantNS)
	}

	if obj.Tool() != tc.wantTool {
		t.Fatalf("Tool() = %q, want %q", obj.Tool(), tc.wantTool)
	}

	if obj.UnitName() != tc.wantUnit {
		t.Fatalf("UnitName() = %q, want %q", obj.UnitName(), tc.wantUnit)
	}
}

func TestObjectConstructors(t *testing.T) {
	t.Parallel()

	ns := mustName(t, "acme")

	nobj := identity.NamespaceObject(ns)
	if nobj.IsUnit() {
		t.Fatal("namespace object reports IsUnit")
	}

	if nobj.String() != "acme" {
		t.Fatalf("namespace String() = %q", nobj.String())
	}

	uobj, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	if !uobj.IsUnit() {
		t.Fatal("unit object reports no unit")
	}

	if uobj.String() != "pulumi:acme/web/prod" {
		t.Fatalf("unit String() = %q", uobj.String())
	}

	if _, err := identity.UnitObject("", ns, "web/prod"); !errors.Is(err, identity.ErrInvalidObject) {
		t.Fatalf("empty tool = %v, want ErrInvalidObject", err)
	}

	if _, err := identity.UnitObject("pulumi", ns, ""); !errors.Is(err, identity.ErrInvalidObject) {
		t.Fatalf("empty name = %v, want ErrInvalidObject", err)
	}
}

// TestCallerObject proves the caller reference names no namespace or unit,
// that ParseObject rejects its wire form, and that a namespace named
// "caller" never equals it.
func TestCallerObject(t *testing.T) {
	t.Parallel()

	cobj := identity.CallerObject()
	if cobj.IsUnit() {
		t.Fatal("caller object reports IsUnit")
	}

	if !cobj.IsCaller() {
		t.Fatal("caller object reports no caller")
	}

	if cobj.String() != "@caller" {
		t.Fatalf("caller String() = %q", cobj.String())
	}

	if cobj.Namespace().String() != "" || cobj.Tool() != "" || cobj.UnitName() != "" {
		t.Fatalf(
			"caller object names %q/%q/%q, want no namespace, tool or unit",
			cobj.Namespace().String(),
			cobj.Tool(),
			cobj.UnitName(),
		)
	}

	if _, err := identity.ParseObject(cobj.String()); !errors.Is(err, identity.ErrInvalidObject) {
		t.Fatalf("parse caller form %q = %v, want ErrInvalidObject", cobj.String(), err)
	}

	nsObj, err := identity.ParseObject("caller")
	if err != nil {
		t.Fatalf("parse namespace caller: %v", err)
	}

	if nsObj.IsCaller() {
		t.Fatal("namespace caller reports caller")
	}

	if nsObj == cobj {
		t.Fatal("namespace caller equals the caller object")
	}
}

// TestServerObject proves the server reference names no namespace, unit or
// caller, that ParseObject reads its wire form back, and that a namespace
// named "server" never equals it.
func TestServerObject(t *testing.T) {
	t.Parallel()

	sobj := identity.ServerObject()
	checkServerIdentity(t, sobj)
	checkServerParse(t, sobj)
}

// checkServerIdentity proves sobj names the installation and nothing else.
func checkServerIdentity(t *testing.T, sobj identity.Object) {
	t.Helper()

	if sobj.IsUnit() {
		t.Fatal("server object reports IsUnit")
	}

	if sobj.IsCaller() {
		t.Fatal("server object reports caller")
	}

	if !sobj.IsServer() {
		t.Fatal("server object reports no server")
	}

	if sobj.String() != "@server" {
		t.Fatalf("server String() = %q", sobj.String())
	}

	if sobj.Namespace().String() != "" || sobj.Tool() != "" || sobj.UnitName() != "" {
		t.Fatalf(
			"server object names %q/%q/%q, want no namespace, tool or unit",
			sobj.Namespace().String(),
			sobj.Tool(),
			sobj.UnitName(),
		)
	}
}

// checkServerParse proves ParseObject rejects the server wire form and that a
// namespace named server never equals the server object.
func checkServerParse(t *testing.T, sobj identity.Object) {
	t.Helper()

	parsed, err := identity.ParseObject(sobj.String())
	if err != nil || !parsed.IsServer() || parsed.String() != sobj.String() {
		t.Fatalf("parse server form %q = %+v, %v; want the server object", sobj.String(), parsed, err)
	}

	nsObj, err := identity.ParseObject("server")
	if err != nil {
		t.Fatalf("parse namespace server: %v", err)
	}

	if nsObj.IsServer() {
		t.Fatal("namespace server reports server")
	}

	if nsObj == sobj {
		t.Fatal("namespace server equals the server object")
	}

	if identity.CallerObject() == sobj {
		t.Fatal("caller object equals the server object")
	}
}

// TestParseCollectionObject proves that ParseObject reads the wire form
// <namespace>/<collection> of a collection back (ADR 51).
func TestParseCollectionObject(t *testing.T) {
	t.Parallel()

	obj, err := identity.ParseObject("acme/payments")
	if err != nil {
		t.Fatalf("parse collection: %v", err)
	}

	if !obj.IsCollection() || obj.Namespace() != "acme" || obj.CollectionName() != "payments" ||
		obj.String() != identity.CollectionObject("acme", "payments").String() {
		t.Fatalf("collection = %+v, want payments in acme", obj)
	}
}

// TestServerActionsProveWireForms proves the server actions use the server
// wire forms that the installation API declares.
func TestServerActionsProveWireForms(t *testing.T) {
	t.Parallel()

	if identity.ActionServerBackup != "server:backup" {
		t.Fatalf("backup action = %q, want %q", identity.ActionServerBackup, "server:backup")
	}

	if identity.ActionServerObserve != "server:observe" {
		t.Fatalf("observe action = %q, want %q", identity.ActionServerObserve, "server:observe")
	}

	if identity.ActionServerMaintenance != "server:maintenance" {
		t.Fatalf(
			"maintenance action = %q, want %q",
			identity.ActionServerMaintenance,
			"server:maintenance",
		)
	}

	if identity.ActionServerRestore != "server:restore" {
		t.Fatalf("restore action = %q, want %q", identity.ActionServerRestore, "server:restore")
	}

	if identity.ActionServerRestoreAccess != "server:restore-access" {
		t.Fatalf(
			"restore-access action = %q, want %q",
			identity.ActionServerRestoreAccess,
			"server:restore-access",
		)
	}

	for action, want := range map[identity.Action]string{
		identity.ActionServerReadPrincipals:   "server:read-principals",
		identity.ActionServerManagePrincipals: "server:manage-principals",
		identity.ActionServerManageGroups:     "server:manage-groups",
	} {
		if string(action) != want {
			t.Fatalf("action = %q, want %q", action, want)
		}
	}
}

func checkAuthorizer(t *testing.T, authorizer identity.Authorizer) {
	t.Helper()

	actions := []identity.Action{
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
		identity.ActionServerRestoreAccess,
	}

	nsObj := identity.NamespaceObject(mustName(t, "acme"))
	unitObj, err := identity.UnitObject("pulumi", mustName(t, "acme"), "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "admin",
		DisplayName:    "admin",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	viewer := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "viewer",
		DisplayName:    "viewer",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}

	objs := []identity.Object{nsObj, unitObj, identity.CallerObject(), identity.ServerObject()}
	for i := range objs {
		obj := objs[i]
		for _, action := range actions {
			if err := authorizer.Authorize(t.Context(), admin, action, obj); err != nil {
				t.Fatalf("admin %s on %q: %v", action, obj.String(), err)
			}

			if err := authorizer.Authorize(t.Context(), viewer, action, obj); !errors.Is(
				err,
				identity.ErrDenied,
			) {
				t.Fatalf("viewer %s on %q = %v, want ErrDenied", action, obj.String(), err)
			}
		}
	}
}

func TestAdminOnlyObjects(t *testing.T) {
	t.Parallel()

	checkAuthorizer(t, identity.AdminOnly{})
}

func TestAuthorizerConformance(t *testing.T) {
	t.Parallel()

	checkAuthorizer(t, testAuthorizer{})
}

// TestAdminOnlySuite runs the shared Authorizer suite against the built-in
// default: administrators pass on every object, everyone else fails closed.
func TestAdminOnlySuite(t *testing.T) {
	t.Parallel()

	identitytest.Run(t, func(t *testing.T) identity.Authorizer {
		t.Helper()

		return identity.AdminOnly{}
	})
}

// TestPolicyAuthorizerSuite runs the shared Authorizer suite against the
// object-sensitive test Authorizer with no grants: it behaves like the
// default, so administrators pass and everyone else fails on every object.
func TestPolicyAuthorizerSuite(t *testing.T) {
	t.Parallel()

	identitytest.Run(t, func(t *testing.T) identity.Authorizer {
		t.Helper()

		return identitytest.NewPolicyAuthorizer("viewer", map[identity.Action][]string{})
	})
}

// TestObjectSensitiveSuite runs the object-sensitive cases: a grant on one
// unit allows only that unit, and a grant on a namespace allows its units.
func TestObjectSensitiveSuite(t *testing.T) {
	t.Parallel()

	identitytest.RunObjectSensitive(t)
}

// TestStaticTokenSuite runs the shared Authenticator suite against the
// single-token bootstrap authenticator.
func TestStaticTokenSuite(t *testing.T) {
	t.Parallel()

	token := strings.Repeat("a", 32)

	identitytest.RunAuthenticator(t, func(t *testing.T) (identity.Authenticator, string) {
		t.Helper()

		authn, err := identity.NewStaticToken("admin", token)
		if err != nil {
			t.Fatalf("static token: %v", err)
		}

		return authn, token
	})
}

// TestStaticTokensSuite runs the shared Authenticator suite against the
// multi-token bootstrap authenticator.
func TestStaticTokensSuite(t *testing.T) {
	t.Parallel()

	token := strings.Repeat("b", 32)

	identitytest.RunAuthenticator(t, func(t *testing.T) (identity.Authenticator, string) {
		t.Helper()

		authn, err := identity.NewStaticTokens(token)
		if err != nil {
			t.Fatalf("static tokens: %v", err)
		}

		return authn, token
	})
}

// TestStaticTokensSingle proves one bootstrap token signs in as admin.
func TestStaticTokensSingle(t *testing.T) {
	t.Parallel()

	token := strings.Repeat("a", 32)

	authn, err := identity.NewStaticTokens(token)
	if err != nil {
		t.Fatalf("one token: %v", err)
	}

	got, err := authn.AuthenticateToken(t.Context(), token)
	if err != nil {
		t.Fatalf("one token signs in: %v", err)
	}

	if got.Subject != "admin" || got.DisplayName != "admin" || !got.Admin {
		t.Fatalf("one token signs in as %+v, want admin", got)
	}
}

// TestStaticTokensTwo proves two bootstrap tokens sign in as two distinct
// administrators, and a third token signs in as admin-3.
func TestStaticTokensTwo(t *testing.T) {
	t.Parallel()

	first := strings.Repeat("a", 32)
	second := strings.Repeat("b", 32)
	third := strings.Repeat("c", 32)

	authn, err := identity.NewStaticTokens(first, second, third)
	if err != nil {
		t.Fatalf("three tokens: %v", err)
	}

	gotA, err := authn.AuthenticateToken(t.Context(), first)
	if err != nil {
		t.Fatalf("first token signs in: %v", err)
	}

	gotB, err := authn.AuthenticateToken(t.Context(), second)
	if err != nil {
		t.Fatalf("second token signs in: %v", err)
	}

	gotC, err := authn.AuthenticateToken(t.Context(), third)
	if err != nil {
		t.Fatalf("third token signs in: %v", err)
	}

	if gotA.Subject != "admin" || !gotA.Admin {
		t.Fatalf("first token signs in as %+v, want admin", gotA)
	}

	if gotB.Subject != "admin-2" || !gotB.Admin {
		t.Fatalf("second token signs in as %+v, want admin-2", gotB)
	}

	if gotC.Subject != "admin-3" || !gotC.Admin {
		t.Fatalf("third token signs in as %+v, want admin-3", gotC)
	}

	if gotA.Subject == gotB.Subject {
		t.Fatalf("both tokens sign in as %q, want distinct subjects", gotA.Subject)
	}
}

// TestStaticTokensUnknown proves a wrong token is ErrUnauthenticated.
func TestStaticTokensUnknown(t *testing.T) {
	t.Parallel()

	authn, err := identity.NewStaticTokens(strings.Repeat("a", 32))
	if err != nil {
		t.Fatalf("one token: %v", err)
	}

	if _, err := authn.AuthenticateToken(t.Context(), strings.Repeat("z", 32)); !errors.Is(
		err,
		identity.ErrUnauthenticated,
	) {
		t.Fatalf("wrong token = %v, want ErrUnauthenticated", err)
	}
}

// TestStaticTokensWeak proves a short token, an empty entry from a
// trailing comma, and a duplicate token are ErrWeakToken.
func TestStaticTokensWeak(t *testing.T) {
	t.Parallel()

	first := strings.Repeat("a", 32)
	second := strings.Repeat("b", 32)

	if _, err := identity.NewStaticTokens("short"); !errors.Is(err, identity.ErrWeakToken) {
		t.Fatalf("short token = %v, want ErrWeakToken", err)
	}

	trailing := strings.Split(first+",", ",")
	if _, err := identity.NewStaticTokens(trailing...); !errors.Is(err, identity.ErrWeakToken) {
		t.Fatalf("trailing comma = %v, want ErrWeakToken", err)
	}

	if _, err := identity.NewStaticTokens(first, second, first); !errors.Is(err, identity.ErrWeakToken) {
		t.Fatalf("duplicate token = %v, want ErrWeakToken", err)
	}

	if _, err := identity.NewStaticTokens(); !errors.Is(err, identity.ErrWeakToken) {
		t.Fatalf("no token = %v, want ErrWeakToken", err)
	}
}

// fixedAuthenticator answers one principal or one error for every token.
type fixedAuthenticator struct {
	err       error
	principal identity.Principal
}

func (f fixedAuthenticator) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	return f.principal, f.err
}

func TestChain(t *testing.T) {
	t.Parallel()

	var none identity.Principal

	alice := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "alice",
		DisplayName:    "Alice",
		Admin:          false,
		TokenID:        "tok-1",
		TokenExpiresAt: time.Time{},
	}
	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "admin",
		DisplayName:    "admin",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	unauth := fixedAuthenticator{err: identity.ErrUnauthenticated, principal: none}
	broken := fixedAuthenticator{err: errStoreDown, principal: none}
	acceptAdmin := fixedAuthenticator{err: nil, principal: admin}
	acceptAlice := fixedAuthenticator{err: nil, principal: alice}

	cases := []struct {
		want    error
		name    string
		wantSub string
		chain   identity.Chain
	}{
		{want: nil, name: "first accepts", wantSub: "admin", chain: identity.Chain{acceptAdmin, broken}},
		{want: nil, name: "second accepts", wantSub: "alice", chain: identity.Chain{unauth, acceptAlice}},
		{want: identity.ErrUnauthenticated, name: "none accepts", wantSub: "", chain: identity.Chain{unauth, unauth}},
		{want: identity.ErrUnauthenticated, name: "empty", wantSub: "", chain: identity.Chain{}},
		{
			want:    errStoreDown,
			name:    "a failure stops the chain",
			wantSub: "",
			chain:   identity.Chain{unauth, broken, acceptAlice},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := tc.chain.AuthenticateToken(t.Context(), "token")
			if !errors.Is(err, tc.want) || p.Subject != tc.wantSub {
				t.Fatalf("chain = %+v, %v; want subject %q, err %v", p, err, tc.wantSub, tc.want)
			}
		})
	}
}

var errStoreDown = errors.New("store down")

func TestSelfAllowed(t *testing.T) {
	t.Parallel()

	authz := identity.SelfAllowed{Next: identity.AdminOnly{}, Actions: []identity.Action{identity.ActionTokenManageOwn}}
	person := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "bob",
		DisplayName:    "Bob",
		Admin:          false,
		TokenID:        "tok-1",
		TokenExpiresAt: time.Time{},
	}
	ns, err := identity.ParseObject("acme")
	if err != nil {
		t.Fatalf("parse object: %v", err)
	}

	caller := identity.CallerObject()
	if authErr := authz.Authorize(t.Context(), person, identity.ActionTokenManageOwn, caller); authErr != nil {
		t.Fatalf("self action on the caller: %v", authErr)
	}

	err = authz.Authorize(t.Context(), person, identity.ActionTokenManageOwn, ns)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("self action on a namespace: err = %v, want ErrDenied", err)
	}

	err = authz.Authorize(t.Context(), person, identity.ActionCallerRead, identity.CallerObject())
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("another action on the caller: err = %v, want ErrDenied", err)
	}
}

func TestAdminSubjects(t *testing.T) {
	t.Parallel()

	two, err := identity.NewStaticTokens(strings.Repeat("a", 32), strings.Repeat("b", 32))
	if err != nil {
		t.Fatalf("static tokens: %v", err)
	}

	one, err := identity.NewStaticToken("root", strings.Repeat("c", 32))
	if err != nil {
		t.Fatalf("static token: %v", err)
	}

	got := identity.Chain{two, identity.Chain{one}}.AdminSubjects()
	if want := []string{"admin", "admin-2", "root"}; !slices.Equal(got, want) {
		t.Fatalf("admin subjects = %v, want %v", got, want)
	}
}

// TestSelfAllowedListObjectsForwards proves SelfAllowed gives the scope of
// Next unchanged: a self action adds no object to a list.
func TestSelfAllowedListObjectsForwards(t *testing.T) {
	t.Parallel()

	acme := namespace.Name("acme")
	next := identitytest.NewPolicyAuthorizer("alice", map[identity.Action][]string{
		identity.ActionUnitRead: {identity.NamespaceObject(acme).String()},
	})
	authz := identity.SelfAllowed{Next: next, Actions: []identity.Action{identity.ActionTokenManageOwn}}
	alice := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "alice", DisplayName: "alice", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
	}

	scope, err := authz.ListObjects(t.Context(), alice, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("list unit:read: %v", err)
	}

	if scope.All || len(scope.Namespaces) != 1 || scope.Namespaces[0].Namespace() != acme {
		t.Fatalf("list unit:read = %+v, want namespace acme only", scope)
	}

	scope, err = authz.ListObjects(t.Context(), alice, identity.ActionTokenManageOwn)
	if err != nil {
		t.Fatalf("list token:manage-own: %v", err)
	}

	if !scope.IsEmpty() {
		t.Fatalf("list token:manage-own = %+v, want no object", scope)
	}
}

// TestSelfAllowedSuite runs the shared Authorizer suite against SelfAllowed
// with no self action around the default.
func TestSelfAllowedSuite(t *testing.T) {
	t.Parallel()

	identitytest.Run(t, func(t *testing.T) identity.Authorizer {
		t.Helper()

		return identity.SelfAllowed{Next: identity.AdminOnly{}, Actions: nil}
	})
}
