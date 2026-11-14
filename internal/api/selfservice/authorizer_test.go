package selfservice_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	selfserviceapi "opendustpan.org/opendustpan/internal/api/selfservice"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// authorizerEnv is the real access authorizer over a store with one namespace
// and three people: the caller, the source principal of a blueprint, and a
// principal that the blueprint does not name.
type authorizerEnv struct {
	store  *sqlite.Store
	authz  *access.Authorizer
	caller identity.Principal
}

// newAuthorizerEnv opens a store, and seeds the people and the namespace.
func newAuthorizerEnv(t *testing.T) *authorizerEnv {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, createErr := namespaces.Create(t.Context(), "acme"); createErr != nil {
		t.Fatalf("seed namespace: %v", createErr)
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: st, Callers: st, Collections: nil, Units: st, Principals: st,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	var caller identity.Principal

	caller.Subject = "ann"

	seedPerson(t, st, "prn-ann", "ann")
	seedPerson(t, st, "prn-source", "source")
	seedPerson(t, st, "prn-other", "other")

	return &authorizerEnv{store: st, authz: authz, caller: caller}
}

// seedPerson stores a person with the ID id and the subject subject.
func seedPerson(t *testing.T, st *sqlite.Store, id, subject string) {
	t.Helper()

	at := time.Date(2026, time.November, 7, 12, 0, 0, 0, time.UTC)

	prn := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          id,
		Subject:     subject,
		DisplayName: "",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
		DisabledAt:  time.Time{},
	}
	if err := st.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID)); err != nil {
		t.Fatalf("seed principal %s: %v", subject, err)
	}
}

// grantTo gives the caller the role on obj.
func (env *authorizerEnv) grantTo(t *testing.T, obj identity.Object) {
	t.Helper()

	at := time.Date(2026, time.November, 7, 12, 0, 0, 0, time.UTC)

	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-" + access.RoleAccountAdministrator + "-" + obj.String(),
		Role: access.RoleAccountAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-ann"},
	}

	fence, e := storetest.GrantWrite(t, env.store, obj, at, "grant:"+grt.ID)
	if err := env.store.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("grant %s on %s: %v", access.RoleAccountAdministrator, obj, err)
	}
}

// sourceRequirement is the requirement on the source principal of a blueprint
// that names source as its source principal. The requirement comes from
// PublishRequirements, so the test follows the code.
func sourceRequirement(t *testing.T, source string) selfservice.Requirement {
	t.Helper()

	var bp selfservice.Blueprint

	bp.Name = "blueprint-a"
	bp.SourcePrincipal = source

	reqs := selfservice.PublishRequirements(parseNamespace(t, "acme"), &bp)
	if len(reqs) != 2 {
		t.Fatalf("PublishRequirements gave %d requirements, want the blueprint and the source principal", len(reqs))
	}

	return reqs[1]
}

// TestSourcePrincipalIsCheckedOnItself runs the source-principal requirement of
// a blueprint through the real authorizer. A grant on the principal allows the
// requirement; a grant on another principal or on the namespace alone does not.
func TestSourcePrincipalIsCheckedOnItself(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		grant   func(*testing.T, *authorizerEnv)
		allowed bool
	}{
		{
			name: "a grant on the source principal allows",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.PrincipalObject("prn-source"))
			},
			allowed: true,
		},
		{
			name: "a grant on another principal denies",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.PrincipalObject("prn-other"))
			},
			allowed: false,
		},
		{
			name: "a grant on the namespace alone denies the principal requirement",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.NamespaceObject("acme"))
			},
			allowed: false,
		},
	}

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newAuthorizerEnv(t)
			tc.grant(t, env)

			req := sourceRequirement(t, "prn-source")
			adapter := selfserviceapi.NewNamespaceAuthorizer(env.authz)
			err := adapter.Authorize(t.Context(), env.caller, req.Action, req.Object)

			if tc.allowed && err != nil {
				t.Fatalf("authorize: %v", err)
			}

			if !tc.allowed && !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("authorize error = %v, want a denial", err)
			}
		})
	}
}

// TestUnmappedObjectKindIsRefused gives the caller a namespace grant that
// allows the action on the namespace. The adapter must still refuse an object
// kind that it does not map: the refusal is its own denial, not the authorizer's.
func TestUnmappedObjectKindIsRefused(t *testing.T) {
	t.Parallel()

	env := newAuthorizerEnv(t)
	env.grantTo(t, identity.NamespaceObject("acme"))

	obj := selfservice.Object{
		Unit:      identity.Object{},
		Kind:      selfservice.ObjectKind("unmapped"),
		Namespace: "acme",
		Blueprint: "",
	}

	adapter := selfserviceapi.NewNamespaceAuthorizer(env.authz)
	err := adapter.Authorize(t.Context(), env.caller, identity.ActionNamespaceRead, obj)

	if err == nil {
		t.Fatal("authorize allowed an object kind that the adapter does not map")
	}

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("authorize error = %v, want a denial", err)
	}

	if msg := err.Error(); !strings.Contains(msg, "has no authorization here") {
		t.Errorf("error = %q, want the adapter's own refusal of the kind", msg)
	}
}

// TestHostileObjectsAreRefused gives the caller a grant that would allow
// principal:manage-tokens on the object's real target. Each case pairs an
// object with a half-valid shape: the adapter must refuse it, because it needs
// both the principal kind and a principal unit.
func TestHostileObjectsAreRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		grant  func(*testing.T, *authorizerEnv)
		object func() selfservice.Object
	}{
		{
			name: "an unmapped kind with a principal unit is refused",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.PrincipalObject("prn-source"))
			},
			object: func() selfservice.Object {
				return selfservice.Object{
					Unit:      identity.PrincipalObject("prn-source"),
					Kind:      selfservice.ObjectKind("unmapped"),
					Namespace: "acme",
					Blueprint: "",
				}
			},
		},
		{
			name: "the principal kind with a namespace unit is refused",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.ServerObject())
			},
			object: func() selfservice.Object {
				return selfservice.Object{
					Unit:      identity.NamespaceObject("acme"),
					Kind:      selfservice.ObjectPrincipal,
					Namespace: "acme",
					Blueprint: "",
				}
			},
		},
	}

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newAuthorizerEnv(t)
			tc.grant(t, env)

			action := sourceRequirement(t, "prn-source").Action
			adapter := selfserviceapi.NewNamespaceAuthorizer(env.authz)
			err := adapter.Authorize(t.Context(), env.caller, action, tc.object())

			if err == nil {
				t.Fatal("authorize allowed a hostile object")
			}

			if !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("authorize error = %v, want a denial", err)
			}
		})
	}
}
