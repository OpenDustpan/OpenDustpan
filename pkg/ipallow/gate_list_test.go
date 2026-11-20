package ipallow_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/identity/identitytest"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// scopeNext is an identity.Authorizer that lists a fixed scope.
type scopeNext struct {
	scope identity.Scope
}

func (scopeNext) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

func (s scopeNext) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return s.scope, nil
}

// listFixture is a scope with objects in acme, which has a list, and in
// beta, which has none, and the rules of the two namespaces.
type listFixture struct {
	rules *fakeRules
	scope identity.Scope
}

func newListFixture(t *testing.T) listFixture {
	t.Helper()

	unitIn := func(ns namespace.Name) identity.Object {
		obj, err := identity.UnitObject("pulumi", ns, "web/prod")
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		return obj
	}

	return listFixture{
		rules: &fakeRules{
			calls: 0,
			lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
			errs:  map[namespace.Name]error{},
		},
		scope: identity.Scope{
			Namespaces: []identity.Object{identity.NamespaceObject("acme"), identity.NamespaceObject("beta")},
			Collections: []identity.Object{
				identity.CollectionObject("acme", "web"),
				identity.CollectionObject("beta", "web"),
			},
			Units:      []identity.Object{unitIn("acme"), unitIn("beta")},
			Principals: []identity.Object{identity.PrincipalObject("prn-bot")},
		},
	}
}

// objectNames returns the text forms of objs.
func objectNames(objs []identity.Object) []string {
	out := make([]string, 0, len(objs))
	for i := range objs {
		out = append(out, objs[i].String())
	}

	return out
}

// TestGateListObjectsDropsDeniedNamespaces proves that a client address
// outside the list of acme lists no object of acme, and keeps the objects of
// beta and the principals, which no namespace list covers.
func TestGateListObjectsDropsDeniedNamespaces(t *testing.T) {
	t.Parallel()

	f := newListFixture(t)
	gate := ipallow.Gate{Next: scopeNext{scope: f.scope}, Rules: f.rules, Enabled: true, IncludeBootstrap: false}
	ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

	got, err := gate.ListObjects(ctx, userPrincipal("alice"), identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	want := map[string][]string{
		"namespaces":  {"beta"},
		"collections": {"beta/web"},
		"units":       {f.scope.Units[1].String()},
		"principals":  {f.scope.Principals[0].String()},
	}
	gotSets := map[string][]string{
		"namespaces":  objectNames(got.Namespaces),
		"collections": objectNames(got.Collections),
		"units":       objectNames(got.Units),
		"principals":  objectNames(got.Principals),
	}

	for kind, names := range want {
		if !slices.Equal(gotSets[kind], names) {
			t.Fatalf("%s = %v, want %v", kind, gotSets[kind], names)
		}
	}

	if got.CoversEveryObject() {
		t.Fatal("all = true, want false")
	}
}

// TestGateListObjectsKeepsScope proves the cases where the gate gives the
// scope of Next unchanged: an address inside the list, a gate that is off,
// and the bootstrap administrator.
func TestGateListObjectsKeepsScope(t *testing.T) {
	t.Parallel()

	bootstrap := adminPrincipal("admin")

	cases := []struct {
		name    string
		addr    string
		p       identity.Principal
		enabled bool
	}{
		{name: "inside the list", addr: "198.51.100.7", p: userPrincipal("alice"), enabled: true},
		{name: "gate off", addr: "192.0.2.10", p: userPrincipal("alice"), enabled: false},
		{name: "bootstrap administrator", addr: "192.0.2.10", p: bootstrap, enabled: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newListFixture(t)
			gate := ipallow.Gate{
				Next:             scopeNext{scope: f.scope},
				Rules:            f.rules,
				Enabled:          tc.enabled,
				IncludeBootstrap: false,
			}

			got, err := gate.ListObjects(ctxWithAddr(netip.MustParseAddr(tc.addr)), tc.p, identity.ActionUnitRead)
			if err != nil {
				t.Fatalf("list objects: %v", err)
			}

			if !slices.Equal(objectNames(got.Namespaces), []string{"acme", "beta"}) || len(got.Units) != 2 ||
				len(got.Collections) != 2 {
				t.Fatalf("scope = %+v, want the scope of Next", got)
			}
		})
	}
}

// TestGateListObjectsFailsClosed proves that a failed read of a list fails
// the whole list.
func TestGateListObjectsFailsClosed(t *testing.T) {
	t.Parallel()

	f := newListFixture(t)
	f.rules.errs["beta"] = errStoreDown
	gate := ipallow.Gate{Next: scopeNext{scope: f.scope}, Rules: f.rules, Enabled: true, IncludeBootstrap: false}

	_, err := gate.ListObjects(ctxWithAddr(netip.MustParseAddr("198.51.100.7")), userPrincipal("alice"),
		identity.ActionUnitRead)
	if !errors.Is(err, errStoreDown) {
		t.Fatalf("list objects = %v, want the store error", err)
	}
}

// TestGateListWithinAuthorize runs the conformance check that a list is
// never wider than Authorize against the gate, for a client address outside
// the list of acme and inside no other list.
func TestGateListWithinAuthorize(t *testing.T) {
	t.Parallel()

	unitIn := func(ns namespace.Name) identity.Object {
		obj, err := identity.UnitObject("pulumi", ns, "web/prod")
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		return obj
	}

	next := identitytest.NewPolicyAuthorizer("alice", map[identity.Action][]string{
		identity.ActionNamespaceRead: {"acme", "beta"},
		identity.ActionUnitRead:      {unitIn("acme").String(), unitIn("beta").String()},
	})
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
		errs:  map[namespace.Name]error{},
	}
	gate := ipallow.Gate{Next: next, Rules: rules, Enabled: true, IncludeBootstrap: false}

	every := []identity.Object{
		identity.NamespaceObject("acme"), identity.NamespaceObject("beta"), unitIn("acme"), unitIn("beta"),
	}

	for _, action := range []identity.Action{identity.ActionNamespaceRead, identity.ActionUnitRead} {
		identitytest.CheckListWithinAuthorize(ctxWithAddr(netip.MustParseAddr("192.0.2.10")), t, identitytest.ListCheck{
			Authorizer: gate, Principal: userPrincipal("alice"), Action: action, Every: every,
		})
	}
}

// exactAll is an exact identity.Authorizer that lists every object and
// allows each.
type exactAll struct{ scopeNext }

func (exactAll) ScopeExact() bool { return true }

// TestGateScopeExact proves that the gate says its scope is exact only when
// it is off and the scope of Next is exact: an enabled gate refuses objects
// that a scope that covers every object holds.
func TestGateScopeExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		gate ipallow.Gate
		want bool
	}{
		{
			name: "off over exact",
			gate: ipallow.Gate{Next: identity.AdminOnly{}, Rules: nil, Enabled: false, IncludeBootstrap: false},
			want: true,
		},
		{
			name: "on over exact",
			gate: ipallow.Gate{Next: identity.AdminOnly{}, Rules: nil, Enabled: true, IncludeBootstrap: false},
			want: false,
		},
		{
			name: "off over no marker",
			gate: ipallow.Gate{
				Next: &fakeNext{calls: 0, err: nil}, Rules: nil, Enabled: false, IncludeBootstrap: false,
			},
			want: false,
		},
		{
			name: "on over no marker",
			gate: ipallow.Gate{
				Next: &fakeNext{calls: 0, err: nil}, Rules: nil, Enabled: true, IncludeBootstrap: false,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := identity.HasExactScope(tc.gate); got != tc.want {
				t.Fatalf("HasExactScope = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGateExactScopeWithinAuthorize runs the conformance check of an exact
// scope with a principal who is not an administrator and an All scope from
// Next, for a gate that refuses the client address in acme. It fails when
// the gate says that its scope is exact.
func TestGateExactScopeWithinAuthorize(t *testing.T) {
	t.Parallel()

	f := newListFixture(t)
	unit, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	for _, enabled := range []bool{true, false} {
		next := exactAll{scopeNext{scope: identity.Every()}}
		gate := ipallow.Gate{Next: next, Rules: f.rules, Enabled: enabled, IncludeBootstrap: false}

		identitytest.CheckExactScope(
			clientip.WithAddr(t.Context(), netip.MustParseAddr("192.0.2.10")),
			t,
			identitytest.ListCheck{
				Authorizer: gate, Principal: userPrincipal("alice"), Action: identity.ActionUnitRead,
				Every: []identity.Object{identity.NamespaceObject("acme"), unit},
			},
		)
	}
}

// everyRules returns the rules of the every-object tests: acme allows
// 198.51.100.0/24, beta allows 203.0.113.0/24, and gamma has no rule.
func everyRules(t *testing.T) *fakeRules {
	t.Helper()

	return &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{
			"acme":  mustList(t, []string{"198.51.100.0/24"}),
			"beta":  mustList(t, []string{"203.0.113.0/24"}),
			"gamma": {},
		},
		errs: map[namespace.Name]error{},
	}
}

// TestGateListObjectsEveryObjectLeavesOutRefused proves that a scope that
// covers every object, as an administrator that is not the bootstrap
// administrator gets it, leaves out each namespace whose list refuses the
// client address, and keeps every other object.
func TestGateListObjectsEveryObjectLeavesOutRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		addr     netip.Addr
		next     identity.Scope
		wantLeft []namespace.Name
	}{
		{
			name: "an address outside both lists", addr: netip.MustParseAddr("192.0.2.10"),
			next: identity.Every(), wantLeft: []namespace.Name{"acme", "beta"},
		},
		{
			name: "an address inside the list of acme", addr: netip.MustParseAddr("198.51.100.7"),
			next: identity.Every(), wantLeft: []namespace.Name{"beta"},
		},
		{
			name: "an IPv6 address outside both lists", addr: netip.MustParseAddr("2001:db8::1"),
			next: identity.Every(), wantLeft: []namespace.Name{"acme", "beta"},
		},
		{
			name: "no client address", addr: netip.Addr{},
			next: identity.Every(), wantLeft: []namespace.Name{"acme", "beta"},
		},
		{
			name: "a namespace that Next leaves out stays out", addr: netip.MustParseAddr("198.51.100.7"),
			next: identity.Every("delta"), wantLeft: []namespace.Name{"beta", "delta"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gate := ipallow.Gate{
				Next: scopeNext{scope: tc.next}, Rules: everyRules(t), Enabled: true, IncludeBootstrap: false,
			}

			got, err := gate.ListObjects(
				ctxWithAddr(tc.addr),
				adminPrincipal("ops-admin"),
				identity.ActionNamespaceRead,
			)
			if err != nil {
				t.Fatalf("list objects: %v", err)
			}

			checkLeftOut(t, got, tc.wantLeft)
		})
	}
}

// checkLeftOut fails the test unless got covers every object except the
// namespaces in want, and still holds gamma, which has no rule, and a
// principal.
func checkLeftOut(t *testing.T, got identity.Scope, want []namespace.Name) {
	t.Helper()

	left, every := got.EveryObjectExcept()
	if !every || !slices.Equal(left, want) {
		t.Fatalf("left-out namespaces = %v (every %t), want %v", left, every, want)
	}

	if got.CoversEveryObject() {
		t.Fatal("a scope that leaves out a namespace covers every object")
	}

	for _, ns := range want {
		if got.Contains(identity.NamespaceObject(ns)) {
			t.Fatalf("scope holds the left-out namespace %s", ns)
		}
	}

	if !got.Contains(identity.NamespaceObject("gamma")) || !got.Contains(identity.PrincipalObject("prn-bot")) {
		t.Fatal("scope drops a namespace with no rule or a principal")
	}
}

// TestGateListObjectsEveryObjectInsideEveryList proves that a scope that
// covers every object stays whole for an address inside each list.
func TestGateListObjectsEveryObjectInsideEveryList(t *testing.T) {
	t.Parallel()

	rules := everyRules(t)
	rules.lists["beta"] = mustList(t, []string{"198.51.100.0/24"})
	gate := ipallow.Gate{Next: scopeNext{scope: identity.Every()}, Rules: rules, Enabled: true, IncludeBootstrap: false}

	got, err := gate.ListObjects(ctxWithAddr(netip.MustParseAddr("198.51.100.7")), adminPrincipal("ops-admin"),
		identity.ActionNamespaceRead)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	if !got.CoversEveryObject() {
		t.Fatalf("scope = %+v, want every object", got)
	}
}

// TestGateListObjectsEveryObjectExempt proves that the bootstrap
// administrator and a gate that is off keep a scope that covers every object,
// and read no list.
func TestGateListObjectsEveryObjectExempt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		p       identity.Principal
		enabled bool
	}{
		{name: "the bootstrap administrator", p: adminPrincipal("admin"), enabled: true},
		{name: "a gate that is off", p: adminPrincipal("ops-admin"), enabled: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rules := everyRules(t)
			gate := ipallow.Gate{
				Next: scopeNext{scope: identity.Every()}, Rules: rules, Enabled: tc.enabled, IncludeBootstrap: false,
			}

			got, err := gate.ListObjects(ctxWithAddr(netip.MustParseAddr("192.0.2.10")), tc.p,
				identity.ActionNamespaceRead)
			if err != nil {
				t.Fatalf("list objects: %v", err)
			}

			if !got.CoversEveryObject() || rules.calls != 0 {
				t.Fatalf("scope = %+v after %d reads, want every object and no read", got, rules.calls)
			}
		})
	}
}

// TestGateListObjectsEveryObjectFailsClosed sends hostile answers of the
// Rules port directly to the gate: a store error, too many lists, and names
// that no create path accepts. Each one fails the whole list, so the caller
// never gets a scope that covers every object.
func TestGateListObjectsEveryObjectFailsClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		prepare func(r *fakeRules)
		want    error
	}{
		{
			name:    "a store error",
			prepare: func(r *fakeRules) { r.errs[everyListKey] = errStoreDown },
			want:    errStoreDown,
		},
		{
			name:    "too many lists",
			prepare: func(r *fakeRules) { r.errs[everyListKey] = ipallow.ErrTooManyLists },
			want:    ipallow.ErrTooManyLists,
		},
		{
			name:    "an empty name",
			prepare: func(r *fakeRules) { r.lists[""] = r.lists["acme"] },
			want:    namespace.ErrInvalidName,
		},
		{
			name:    "a name with a space",
			prepare: func(r *fakeRules) { r.lists["bad name"] = r.lists["acme"] },
			want:    namespace.ErrInvalidName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rules := everyRules(t)
			tc.prepare(rules)
			gate := ipallow.Gate{
				Next: scopeNext{scope: identity.Every()}, Rules: rules, Enabled: true, IncludeBootstrap: false,
			}

			got, err := gate.ListObjects(ctxWithAddr(netip.MustParseAddr("198.51.100.7")), adminPrincipal("ops-admin"),
				identity.ActionNamespaceRead)
			if !errors.Is(err, tc.want) {
				t.Fatalf("list objects = %v, want %v", err, tc.want)
			}

			if _, every := got.EveryObjectExcept(); every || !got.IsEmpty() {
				t.Fatalf("scope = %+v, want the empty scope", got)
			}
		})
	}
}

// TestGateListObjectsEveryObjectFiltersExplicitSets proves that a scope that
// covers every object and also names explicit objects, as a grant on the
// installation and a grant on a namespace give it, drops the explicit objects
// of a refused namespace too.
func TestGateListObjectsEveryObjectFiltersExplicitSets(t *testing.T) {
	t.Parallel()

	next := identity.Every()
	next.Namespaces = []identity.Object{identity.NamespaceObject("acme"), identity.NamespaceObject("gamma")}
	gate := ipallow.Gate{Next: scopeNext{scope: next}, Rules: everyRules(t), Enabled: true, IncludeBootstrap: false}

	got, err := gate.ListObjects(ctxWithAddr(netip.MustParseAddr("192.0.2.10")), userPrincipal("alice"),
		identity.ActionNamespaceRead)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	if !slices.Equal(objectNames(got.Namespaces), []string{"gamma"}) {
		t.Fatalf("namespaces = %v, want [gamma]", objectNames(got.Namespaces))
	}
}

// TestGateEveryObjectListWithinAuthorize runs the conformance check that a
// list is never wider than Authorize against the gate, for a Next whose scope
// covers every object and a client address outside the list of beta.
func TestGateEveryObjectListWithinAuthorize(t *testing.T) {
	t.Parallel()

	unitIn := func(ns namespace.Name) identity.Object {
		obj, err := identity.UnitObject("pulumi", ns, "web/prod")
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		return obj
	}

	gate := ipallow.Gate{
		Next: exactAll{
			scopeNext{scope: identity.Every()},
		},
		Rules:            everyRules(t),
		Enabled:          true,
		IncludeBootstrap: false,
	}
	every := []identity.Object{
		identity.NamespaceObject("acme"), identity.NamespaceObject("beta"), identity.NamespaceObject("gamma"),
		unitIn("acme"), unitIn("beta"), unitIn("gamma"), identity.CollectionObject("beta", "web"),
	}

	for _, action := range []identity.Action{identity.ActionNamespaceRead, identity.ActionUnitRead} {
		identitytest.CheckListWithinAuthorize(
			ctxWithAddr(netip.MustParseAddr("198.51.100.7")),
			t,
			identitytest.ListCheck{
				Authorizer: gate, Principal: adminPrincipal("ops-admin"), Action: action, Every: every,
			},
		)
	}
}
