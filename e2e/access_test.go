//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2eRole and e2eGrant decode the role and grant wire forms.
type e2eRole struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
	BuiltIn bool     `json:"builtIn"`
}

type e2eRoleList struct {
	Roles []e2eRole `json:"roles"`
}

type e2eGrant struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Role    string `json:"role"`
	Object  string `json:"object"`
}

type e2eGrantList struct {
	Grants     []e2eGrant `json:"grants"`
	NextCursor string     `json:"nextCursor"`
	Total      int64      `json:"total"`
}

// TestRolesAndGrants drives every role and grant route of the installation
// API (ADR 52) through the real binary, on each store. No CLI calls these
// routes, so the harness calls them with the bootstrap admin token.
func TestRolesAndGrants(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runAccessOnStore(t, cli, db)
		})
	}
}

// runAccessOnStore runs the role and grant steps on one store.
func runAccessOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	role := checkRoles(ctx, t, env)

	var bob e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"bob","kind":"person"}`, http.StatusCreated,
	}, &bob)

	var ops e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated,
	}, &ops)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"grants"}`, http.StatusCreated,
	}, nil)

	bobSubject := "principal:" + bob.ID
	opsSubject := "group:" + ops.ID + "#member"

	for _, k := range []struct {
		path, object, subject, role string
	}{
		{"/api/server/grants", "@server", bobSubject, "viewer"},
		{"/api/server/namespaces/grants/grants", "grants", opsSubject, role.ID},
		{"/api/server/namespaces/grants/collections/payments/grants", "grants/payments", opsSubject, "writer"},
		{"/api/server/namespaces/grants/unit-grants", "pulumi:grants/web/prod", bobSubject, "maintainer"},
	} {
		checkGrantRoutes(ctx, t, env, grantKind{path: k.path, object: k.object, subject: k.subject, role: k.role})
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/roles/" + role.ID, "", http.StatusNoContent,
	}, nil)

	checkSubjectDeleteRevokes(ctx, t, env, bob)
}

// checkRoles reads the catalog, makes, changes and reads a custom role, and
// answers it.
func checkRoles(ctx context.Context, t *testing.T, env *e2e.Env) e2eRole {
	t.Helper()

	var list e2eRoleList
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/roles", "", http.StatusOK}, &list)

	names := make([]string, 0, len(list.Roles))
	for _, r := range list.Roles {
		names = append(names, r.Name)
	}

	for _, want := range []string{"viewer", "admin", "installation-operator", "account-administrator", "site-administrator"} {
		if !strings.Contains(" "+strings.Join(names, " ")+" ", " "+want+" ") {
			t.Fatalf("roles = %v, want %s among them", names, want)
		}
	}

	var role e2eRole
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles", `{"name":"deployer","actions":["unit:write","unit:read"]}`,
		http.StatusCreated,
	}, &role)

	if !strings.HasPrefix(role.ID, "rol-") || role.BuiltIn {
		t.Fatalf("role = %+v, want a custom role with a rol- ID", role)
	}

	for _, step := range []principalStep{
		{http.MethodPost, "/api/server/roles", `{"name":"DEPLOYER","actions":["unit:read"]}`, http.StatusConflict},
		{http.MethodPatch, "/api/server/roles/" + role.ID, `{"description":"Runs updates"}`, http.StatusOK},
		{http.MethodPatch, "/api/server/roles/viewer", `{"description":"x"}`, http.StatusConflict},
		{http.MethodGet, "/api/server/roles/viewer", "", http.StatusOK},
	} {
		principalCall(ctx, t, env, step, nil)
	}

	var got e2eRole
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/roles/" + role.ID, "", http.StatusOK}, &got)

	if strings.Join(got.Actions, " ") != "unit:read unit:write" {
		t.Fatalf("role actions = %v, want unit:read unit:write", got.Actions)
	}

	return role
}

// grantKind names the grant routes of one object kind and one grant to
// make there.
type grantKind struct {
	path    string
	object  string
	subject string
	role    string
}

// checkGrantRoutes makes, lists and revokes one grant on one object kind.
func checkGrantRoutes(ctx context.Context, t *testing.T, env *e2e.Env, k grantKind) {
	t.Helper()

	query := ""
	if strings.HasSuffix(k.path, "/unit-grants") {
		query = "?tool=pulumi&unit=web%2Fprod"
	}

	body := `{"subject":"` + k.subject + `","role":"` + k.role + `"}`

	var g e2eGrant
	principalCall(ctx, t, env, principalStep{http.MethodPost, k.path + query, body, http.StatusCreated}, &g)

	if g.Object != k.object || g.Subject != k.subject || g.Role != k.role || !strings.HasPrefix(g.ID, "grt-") {
		t.Fatalf("grant on %s = %+v, want %s %s", k.object, g, k.subject, k.role)
	}

	principalCall(ctx, t, env, principalStep{http.MethodPost, k.path + query, body, http.StatusConflict}, nil)

	var list e2eGrantList
	principalCall(ctx, t, env, principalStep{http.MethodGet, k.path + query, "", http.StatusOK}, &list)

	if len(list.Grants) != 1 || list.Grants[0].ID != g.ID || list.Total != 1 || list.NextCursor != "" {
		t.Fatalf("grants on %s = %+v, want %s alone", k.object, list, g.ID)
	}

	if strings.HasPrefix(k.role, "rol-") {
		// A custom role that a grant holds cannot be deleted.
		principalCall(ctx, t, env, principalStep{
			http.MethodDelete, "/api/server/roles/" + k.role, "", http.StatusConflict,
		}, nil)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, k.path + "/" + g.ID + query, "", http.StatusNoContent,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, k.path + "/" + g.ID + query, "", http.StatusNotFound,
	}, nil)
}

// checkSubjectDeleteRevokes deletes a principal that holds a grant, and
// reads that the grant went with it.
func checkSubjectDeleteRevokes(ctx context.Context, t *testing.T, env *e2e.Env, bob e2ePrincipal) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/grants/grants",
		`{"subject":"principal:` + bob.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/principals/" + bob.ID, "", http.StatusNoContent,
	}, nil)

	var list e2eGrantList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/grants/grants", "", http.StatusOK,
	}, &list)

	if len(list.Grants) != 0 {
		t.Fatalf("grants after the principal delete = %+v, want none", list.Grants)
	}
}
