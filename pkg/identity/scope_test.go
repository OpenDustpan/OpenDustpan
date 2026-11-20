package identity_test

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// scopePrincipal returns a person principal for the scope tests.
func scopePrincipal(admin bool) identity.Principal {
	return identity.Principal{
		Subject: "alice", DisplayName: "Alice", Admin: admin, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// TestEveryKeepsEachNameOnceInOrder checks Every sorts its names, removes a
// duplicate, and copies the input slice.
func TestEveryKeepsEachNameOnceInOrder(t *testing.T) {
	t.Parallel()

	input := []namespace.Name{mustName(t, "beta"), mustName(t, "acme"), mustName(t, "beta")}
	scope := identity.Every(input...)

	want := []namespace.Name{mustName(t, "acme"), mustName(t, "beta")}
	got, every := scope.EveryObjectExcept()
	if !every || !slices.Equal(got, want) {
		t.Fatalf("EveryObjectExcept = %v, %t, want %v, true", got, every, want)
	}

	for _, tc := range []struct {
		ns   namespace.Name
		want bool
	}{
		{ns: mustName(t, "acme"), want: false},
		{ns: mustName(t, "beta"), want: false},
		{ns: mustName(t, "gamma"), want: true},
	} {
		if got := scope.Contains(identity.NamespaceObject(tc.ns)); got != tc.want {
			t.Fatalf("Contains(namespace %s) = %v, want %v", tc.ns, got, tc.want)
		}
	}

	if scope.CoversEveryObject() {
		t.Fatal("Every with a left-out namespace covers every object")
	}

	input[0] = mustName(t, "gamma")
	if !scope.Contains(identity.NamespaceObject("gamma")) {
		t.Fatal("a change to the input slice changed the left-out names")
	}

	if scope.Namespaces != nil || scope.Collections != nil || scope.Units != nil || scope.Principals != nil {
		t.Fatalf("explicit sets = %+v, want nil", scope)
	}
}

// TestEveryWithoutExcept checks Every with no names covers every object.
func TestEveryWithoutExcept(t *testing.T) {
	t.Parallel()

	scope := identity.Every()
	if !scope.CoversEveryObject() {
		t.Fatal("Every() does not cover every object")
	}

	if !scope.Contains(identity.NamespaceObject("gamma")) {
		t.Fatal("Every() does not hold a namespace")
	}
}

// TestCoversEveryObject checks the method reports true only for a scope that
// covers every object and leaves out no namespace.
func TestCoversEveryObject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope identity.Scope
		name  string
		want  bool
	}{
		{name: "every object", scope: identity.Every(), want: true},
		{name: "every object except one namespace", scope: identity.Every("acme"), want: false},
		{name: "an explicit namespace", scope: identity.Scope{
			Namespaces:  []identity.Object{identity.NamespaceObject("acme")},
			Collections: nil, Units: nil, Principals: nil,
		}, want: false},
		{
			name:  "the zero scope",
			scope: identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.scope.CoversEveryObject(); got != tc.want {
				t.Fatalf("CoversEveryObject = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestScopeContains checks each kind of object against the every-object case,
// the explicit sets and a collection-only scope.
func TestScopeContains(t *testing.T) {
	t.Parallel()

	acme := identity.NamespaceObject("acme")
	beta := identity.NamespaceObject("beta")
	acmeCollection := identity.CollectionObject("acme", "web")
	betaCollection := identity.CollectionObject("beta", "web")
	betaOtherCollection := identity.CollectionObject("beta", "other")
	acmeUnit := scopeUnit(t, "acme", "web/prod")
	betaUnit := scopeUnit(t, "beta", "web/prod")
	gammaUnit := scopeUnit(t, "gamma", "web/prod")
	bot := identity.PrincipalObject("prn-bot")
	otherBot := identity.PrincipalObject("prn-other")
	acmeDev := identity.NamespaceObject("acme-dev")
	acm := identity.NamespaceObject("acm")
	acmeDevUnit := scopeUnit(t, "acme-dev", "web/prod")
	acmUnit := scopeUnit(t, "acm", "web/prod")
	// A unit of acme whose name looks like the namespace beta.
	acmeUnitNamedBeta := scopeUnit(t, "acme", "beta/prod")

	explicit := identity.Scope{
		Namespaces:  []identity.Object{acme},
		Collections: []identity.Object{betaCollection},
		Units:       []identity.Object{gammaUnit},
		Principals:  []identity.Object{bot},
	}
	collectionOnly := identity.Scope{
		Namespaces: nil, Collections: []identity.Object{acmeCollection}, Units: nil, Principals: nil,
	}

	cases := []struct {
		scope  identity.Scope
		object identity.Object
		name   string
		want   bool
	}{
		{name: "every object holds a namespace", scope: identity.Every(), object: acme, want: true},
		{name: "every object holds a collection", scope: identity.Every(), object: acmeCollection, want: true},
		{name: "every object holds a unit", scope: identity.Every(), object: acmeUnit, want: true},
		{name: "every object holds a principal", scope: identity.Every(), object: bot, want: true},
		{name: "every object holds the server", scope: identity.Every(), object: identity.ServerObject(), want: true},
		{name: "every object holds the caller", scope: identity.Every(), object: identity.CallerObject(), want: true},
		{name: "a left-out namespace is not held", scope: identity.Every("acme"), object: acme, want: false},
		{
			name:   "a collection of a left-out namespace is not held",
			scope:  identity.Every("acme"),
			object: acmeCollection,
			want:   false,
		},
		{
			name:   "a unit of a left-out namespace is not held",
			scope:  identity.Every("acme"),
			object: acmeUnit,
			want:   false,
		},
		{name: "another namespace is held", scope: identity.Every("acme"), object: beta, want: true},
		{name: "a longer name is not a left-out namespace", scope: identity.Every("acme"), object: acmeDev, want: true},
		{name: "a prefix is not a left-out namespace", scope: identity.Every("acme"), object: acm, want: true},
		{
			name:   "a unit of a longer name is not a left-out namespace",
			scope:  identity.Every("acme"),
			object: acmeDevUnit,
			want:   true,
		},
		{
			name:   "a unit of a prefix is not a left-out namespace",
			scope:  identity.Every("acme"),
			object: acmUnit,
			want:   true,
		},
		{
			name:   "a unit named like another namespace stays in its left-out namespace",
			scope:  identity.Every("acme"),
			object: acmeUnitNamedBeta,
			want:   false,
		},
		{
			name:   "two left-out namespaces hold neither namespace",
			scope:  identity.Every("acme", "beta"),
			object: beta,
			want:   false,
		},
		{
			name:   "two left-out namespaces hold no unit of the first",
			scope:  identity.Every("acme", "beta"),
			object: acmeUnit,
			want:   false,
		},
		{
			name:   "two left-out namespaces hold no unit of the second",
			scope:  identity.Every("acme", "beta"),
			object: betaUnit,
			want:   false,
		},
		{
			name:   "two left-out namespaces hold no collection",
			scope:  identity.Every("acme", "beta"),
			object: betaCollection,
			want:   false,
		},
		{
			name:   "two left-out namespaces hold a principal",
			scope:  identity.Every("acme", "beta"),
			object: bot,
			want:   true,
		},
		{
			name:   "two left-out namespaces hold the server",
			scope:  identity.Every("acme", "beta"),
			object: identity.ServerObject(),
			want:   true,
		},
		{
			name:   "two left-out namespaces hold the caller",
			scope:  identity.Every("acme", "beta"),
			object: identity.CallerObject(),
			want:   true,
		},
		{name: "a principal is held", scope: identity.Every("acme"), object: bot, want: true},
		{name: "the server is held", scope: identity.Every("acme"), object: identity.ServerObject(), want: true},
		{name: "the caller is held", scope: identity.Every("acme"), object: identity.CallerObject(), want: true},
		{name: "an explicit namespace is held", scope: explicit, object: acme, want: true},
		{name: "another explicit namespace is not held", scope: explicit, object: beta, want: false},
		{name: "an explicit collection is held", scope: explicit, object: betaCollection, want: true},
		{name: "a collection through its namespace is held", scope: explicit, object: acmeCollection, want: true},
		{name: "a collection of another name is not held", scope: explicit, object: betaOtherCollection, want: false},
		{name: "an explicit unit is held", scope: explicit, object: gammaUnit, want: true},
		{name: "a unit through its namespace is held", scope: explicit, object: acmeUnit, want: true},
		{name: "a unit of another namespace is not held", scope: explicit, object: betaUnit, want: false},
		{name: "an explicit principal is held", scope: explicit, object: bot, want: true},
		{name: "another principal is not held", scope: explicit, object: otherBot, want: false},
		{
			name:   "a unit held only through a collection is not held",
			scope:  collectionOnly,
			object: acmeUnit,
			want:   false,
		},
		{
			name:   "the zero scope holds no namespace",
			scope:  identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil},
			object: acme,
			want:   false,
		},
		{
			name:   "the zero scope holds no principal",
			scope:  identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil},
			object: bot,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.scope.Contains(tc.object); got != tc.want {
				t.Fatalf("Contains(%q) = %v, want %v", tc.object.String(), got, tc.want)
			}
		})
	}
}

// TestScopeIsEmpty checks a scope that covers every object is not empty, and
// the zero scope is empty.
func TestScopeIsEmpty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope identity.Scope
		name  string
		want  bool
	}{
		{name: "every object except one namespace", scope: identity.Every("acme"), want: false},
		{name: "every object", scope: identity.Every(), want: false},
		{name: "an explicit namespace", scope: identity.Scope{
			Namespaces:  []identity.Object{identity.NamespaceObject("acme")},
			Collections: nil, Units: nil, Principals: nil,
		}, want: false},
		{
			name:  "the zero scope",
			scope: identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil},
			want:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.scope.IsEmpty(); got != tc.want {
				t.Fatalf("IsEmpty = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAdminScopeCoversEveryObject checks an administrator gets every object
// and another principal gets none.
func TestAdminScopeCoversEveryObject(t *testing.T) {
	t.Parallel()

	admin := identity.AdminScope(scopePrincipal(true))
	if !admin.CoversEveryObject() {
		t.Fatal("AdminScope of an administrator does not cover every object")
	}

	if !admin.Contains(identity.NamespaceObject("acme")) {
		t.Fatal("AdminScope of an administrator does not hold a namespace")
	}

	person := identity.AdminScope(scopePrincipal(false))
	if !person.IsEmpty() {
		t.Fatalf("AdminScope of another principal = %+v, want the empty scope", person)
	}

	if person.Contains(identity.NamespaceObject("acme")) {
		t.Fatal("AdminScope of another principal holds a namespace")
	}
}

// scopeUnit returns the unit object of name in ns.
func scopeUnit(t *testing.T, ns, name string) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject("pulumi", mustName(t, ns), name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return unit
}

// TestEveryObjectExcept checks that EveryObjectExcept reports the every-object
// case with its left-out namespaces, reports false for every other scope, and
// returns a copy that a caller cannot use to change the scope.
func TestEveryObjectExcept(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		scope     identity.Scope
		wantNames []namespace.Name
		wantEvery bool
	}{
		{name: "every object", scope: identity.Every(), wantNames: []namespace.Name{}, wantEvery: true},
		{
			name: "every object but two", scope: identity.Every("beta", "acme"),
			wantNames: []namespace.Name{"acme", "beta"}, wantEvery: true,
		},
		{
			name: "explicit namespaces", scope: identity.Scope{
				Namespaces: []identity.Object{identity.NamespaceObject("acme")}, Collections: nil, Units: nil,
				Principals: nil,
			},
			wantNames: nil, wantEvery: false,
		},
		{
			name:      "a non-administrator",
			scope:     identity.AdminScope(scopePrincipal(false)),
			wantNames: nil,
			wantEvery: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, every := tc.scope.EveryObjectExcept()
			if every != tc.wantEvery || len(got) != len(tc.wantNames) || !slices.Equal(got, tc.wantNames) {
				t.Fatalf("EveryObjectExcept = %v, %t, want %v, %t", got, every, tc.wantNames, tc.wantEvery)
			}
		})
	}

	scope := identity.Every("acme")
	got, _ := scope.EveryObjectExcept()
	if len(got) != 1 {
		t.Fatalf("left-out names = %v, want [acme]", got)
	}

	got[0] = "gamma"

	if scope.Contains(identity.NamespaceObject("acme")) || !scope.Contains(identity.NamespaceObject("gamma")) {
		t.Fatal("a change to the returned names changed the scope")
	}
}

// TestLeaveOut checks that LeaveOut drops the objects of the named namespaces
// from every case of a scope, never adds an object, and does not change the
// scope it starts from. Each case is a hostile input to the method: a name
// that only shares a prefix, an empty name, a repeat, and a unit whose name
// looks like another namespace.
func TestLeaveOut(t *testing.T) {
	t.Parallel()

	acmeUnit := scopeUnit(t, "acme", "web/prod")
	betaUnit := scopeUnit(t, "beta", "web/prod")
	lookalike := scopeUnit(t, "gamma", "acme/prod")
	explicit := identity.Scope{
		Namespaces: []identity.Object{identity.NamespaceObject("acme"), identity.NamespaceObject("acme-dev")},
		Collections: []identity.Object{
			identity.CollectionObject("acme", "web"),
			identity.CollectionObject("beta", "web"),
		},
		Units:      []identity.Object{acmeUnit, betaUnit, lookalike},
		Principals: []identity.Object{identity.PrincipalObject("prn-bot")},
	}

	cases := []struct {
		name  string
		scope identity.Scope
		names []namespace.Name
		obj   identity.Object
		want  bool
	}{
		{
			"every: the left-out namespace",
			identity.Every(),
			[]namespace.Name{"acme"},
			identity.NamespaceObject("acme"),
			false,
		},
		{"every: a unit of the left-out namespace", identity.Every(), []namespace.Name{"acme"}, acmeUnit, false},
		{
			"every: a longer name stays",
			identity.Every(),
			[]namespace.Name{"acme"},
			identity.NamespaceObject("acme-dev"),
			true,
		},
		{"every: a prefix stays", identity.Every(), []namespace.Name{"acme"}, identity.NamespaceObject("acm"), true},
		{"every: a unit named like the namespace stays", identity.Every(), []namespace.Name{"acme"}, lookalike, true},
		{
			"every: a principal stays",
			identity.Every(),
			[]namespace.Name{"acme"},
			identity.PrincipalObject("prn-bot"),
			true,
		},
		{"every: the server stays", identity.Every(), []namespace.Name{"acme"}, identity.ServerObject(), true},
		{"every: an empty name leaves out nothing", identity.Every(), []namespace.Name{""}, acmeUnit, true},
		{
			"every: an earlier left-out name stays out",
			identity.Every("beta"),
			[]namespace.Name{"acme"},
			betaUnit,
			false,
		},
		{
			"explicit: the left-out namespace",
			explicit,
			[]namespace.Name{"acme"},
			identity.NamespaceObject("acme"),
			false,
		},
		{
			"explicit: a collection of it",
			explicit,
			[]namespace.Name{"acme"},
			identity.CollectionObject("acme", "web"),
			false,
		},
		{"explicit: a unit of it", explicit, []namespace.Name{"acme"}, acmeUnit, false},
		{
			"explicit: a longer name stays",
			explicit,
			[]namespace.Name{"acme"},
			identity.NamespaceObject("acme-dev"),
			true,
		},
		{"explicit: a unit named like it stays", explicit, []namespace.Name{"acme"}, lookalike, true},
		{"explicit: another namespace stays", explicit, []namespace.Name{"acme"}, betaUnit, true},
		{"explicit: a principal stays", explicit, []namespace.Name{"acme"}, identity.PrincipalObject("prn-bot"), true},
		{
			"explicit: an unlisted object is not added",
			explicit,
			[]namespace.Name{"acme"},
			identity.NamespaceObject("zeta"),
			false,
		},
		{
			"the empty scope stays empty",
			identity.AdminScope(scopePrincipal(false)),
			[]namespace.Name{"acme"},
			betaUnit,
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.scope.LeaveOut(tc.names...).Contains(tc.obj); got != tc.want {
				t.Fatalf("LeaveOut(%v).Contains(%q) = %t, want %t", tc.names, tc.obj.String(), got, tc.want)
			}
		})
	}

	every := identity.Every("beta").LeaveOut("acme", "beta", "acme")
	if got, ok := every.EveryObjectExcept(); !ok || !slices.Equal(got, []namespace.Name{"acme", "beta"}) {
		t.Fatalf("left-out names = %v, %t, want [acme beta], true", got, ok)
	}

	if every.CoversEveryObject() {
		t.Fatal("a scope that leaves out a namespace covers every object")
	}

	if !identity.AdminScope(scopePrincipal(false)).LeaveOut("acme").IsEmpty() {
		t.Fatal("LeaveOut made the empty scope hold an object")
	}

	before := len(explicit.Units)
	_ = explicit.LeaveOut("acme")

	if len(explicit.Units) != before || !explicit.Contains(acmeUnit) {
		t.Fatal("LeaveOut changed the scope it started from")
	}
}
