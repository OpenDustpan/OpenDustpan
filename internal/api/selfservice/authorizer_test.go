package selfservice_test

import (
	"errors"
	"path/filepath"
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
// and three people: the caller, the principal that a blueprint names as its
// source, and a principal that is not named.
type authorizerEnv struct {
	store  *sqlite.Store
	authz  *access.Authorizer
	caller identity.Principal
	source string
	other  string
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

	env := &authorizerEnv{
		store:  st,
		authz:  authz,
		caller: caller,
		source: "prn-source",
		other:  "prn-other",
	}

	seedPerson(t, st, "prn-ann", "ann")
	seedPerson(t, st, "prn-source", "source")
	seedPerson(t, st, "prn-other", "other")

	return env
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
func (env *authorizerEnv) grantTo(t *testing.T, obj identity.Object, role string) {
	t.Helper()

	at := time.Date(2026, time.November, 7, 12, 0, 0, 0, time.UTC)

	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-" + role + "-" + obj.String(),
		Role: role, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-ann"},
	}

	fence, e := storetest.GrantWrite(t, env.store, obj, at, "grant:"+grt.ID)
	if err := env.store.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("grant %s on %s: %v", role, obj, err)
	}
}

// sourcePrincipal is the object of the requirement on a source principal of a
// blueprint.
func sourcePrincipal(principalID string) selfservice.Object {
	return selfservice.Object{
		Unit:      identity.PrincipalObject(principalID),
		Kind:      selfservice.ObjectPrincipal,
		Namespace: "acme",
		Blueprint: "",
	}
}

// TestSourcePrincipalIsCheckedOnItself runs the principal requirement of a
// blueprint through the real authorizer. A grant on the principal allows the
// requirement, a grant on another principal or on the namespace alone does
// not, and an object kind that the adapter does not map is refused.
func TestSourcePrincipalIsCheckedOnItself(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		grant   func(*testing.T, *authorizerEnv)
		object  func(*authorizerEnv) selfservice.Object
		allowed bool
	}{
		{
			name: "a grant on the source principal allows",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.PrincipalObject("prn-source"), access.RoleAccountAdministrator)
			},
			object: func(env *authorizerEnv) selfservice.Object {
				obj := sourcePrincipal(env.source)

				return obj
			},
			allowed: true,
		},
		{
			name: "a grant on another principal denies",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.PrincipalObject("prn-other"), access.RoleAccountAdministrator)
			},
			object: func(env *authorizerEnv) selfservice.Object {
				obj := sourcePrincipal(env.source)

				return obj
			},
			allowed: false,
		},
		{
			name: "a grant on the namespace alone denies the principal requirement",
			grant: func(t *testing.T, env *authorizerEnv) {
				t.Helper()
				env.grantTo(t, identity.NamespaceObject("acme"), access.RoleAccountAdministrator)
			},
			object: func(env *authorizerEnv) selfservice.Object {
				obj := sourcePrincipal(env.source)

				return obj
			},
			allowed: false,
		},
		{
			name:  "an unknown object kind denies",
			grant: func(*testing.T, *authorizerEnv) {},
			object: func(*authorizerEnv) selfservice.Object {
				return selfservice.Object{
					Unit:      identity.Object{},
					Kind:      selfservice.ObjectKind("unmapped"),
					Namespace: "acme",
					Blueprint: "",
				}
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

			adapter := selfserviceapi.NewNamespaceAuthorizer(env.authz)
			err := adapter.Authorize(t.Context(), env.caller, identity.ActionPrincipalManageTokens, tc.object(env))

			if tc.allowed && err != nil {
				t.Fatalf("authorize: %v", err)
			}

			if !tc.allowed && !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("authorize error = %v, want a denial", err)
			}
		})
	}
}
