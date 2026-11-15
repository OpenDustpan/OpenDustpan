//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestNamespaceListCursor proves through the real binary that a cursor works
// only with the query that made it: a principal with a smaller scope gets 400
// for the cursor of another principal.
func TestNamespaceListCursor(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runNamespaceListCursorOnStore(t, cli, db)
		})
	}
}

// runNamespaceListCursorOnStore runs the cursor steps on one store.
func runNamespaceListCursorOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	people := seedNamespaceListCursor(ctx, t, env)
	openerToken := env.PersonalToken(ctx, t, people.opener.ID, "cursor-opener")
	otherToken := env.PersonalToken(ctx, t, people.other.ID, "cursor-other")
	first := walkNamespaceListCursor(ctx, t, env, openerToken.Secret)
	checkNamespaceListCursorRefused(ctx, t, env, otherToken.Secret, first.NextCursor)
}

// cursorPeople holds the two principals of the cursor steps.
type cursorPeople struct {
	opener e2ePrincipal
	other  e2ePrincipal
}

// seedNamespaceListCursor creates the namespaces and the two principals of
// the cursor steps. The opener reads two namespaces and the other principal
// reads one.
func seedNamespaceListCursor(
	ctx context.Context, t *testing.T, env *e2e.Env,
) cursorPeople {
	t.Helper()

	for _, ns := range []string{"cursor-alpha", "cursor-bravo", "cursor-charlie"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/namespaces", `{"name":"` + ns + `"}`, http.StatusCreated,
		}, nil)
	}

	var people cursorPeople
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("cursor-opener") + `","kind":"person"}`,
		http.StatusCreated,
	}, &people.opener)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("cursor-other") + `","kind":"person"}`,
		http.StatusCreated,
	}, &people.other)

	for _, grant := range []struct {
		namespace string
		principal string
	}{
		{namespace: "cursor-alpha", principal: people.opener.ID},
		{namespace: "cursor-bravo", principal: people.opener.ID},
		{namespace: "cursor-charlie", principal: people.other.ID},
	} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/namespaces/" + grant.namespace + "/grants",
			`{"subject":"principal:` + grant.principal + `","role":"viewer"}`, http.StatusCreated,
		}, nil)
	}

	return people
}

// walkNamespaceListCursor pages the namespaces of the opener and proves that
// the two pages hold the two namespaces in order.
func walkNamespaceListCursor(
	ctx context.Context, t *testing.T, env *e2e.Env, token string,
) e2eNamespaceList {
	t.Helper()

	first := fetchNamespaceListAs(ctx, t, env, token, "/api/server/namespaces?limit=1")
	if len(first.Namespaces) != 1 || first.Namespaces[0].Name != "cursor-alpha" || first.NextCursor == "" {
		t.Fatalf("first page = %+v, want cursor-alpha and a cursor", first)
	}

	second := fetchNamespaceListAs(
		ctx, t, env, token,
		"/api/server/namespaces?limit=1&cursor="+url.QueryEscape(first.NextCursor),
	)
	if len(second.Namespaces) != 1 || second.Namespaces[0].Name != "cursor-bravo" || second.NextCursor != "" {
		t.Fatalf("second page = %+v, want cursor-bravo and no cursor", second)
	}

	return first
}

// checkNamespaceListCursorRefused proves the other principal gets 400 for the
// cursor of the opener.
func checkNamespaceListCursorRefused(
	ctx context.Context, t *testing.T, env *e2e.Env, token, cursor string,
) {
	t.Helper()

	path := "/api/server/namespaces?limit=1&cursor=" + url.QueryEscape(cursor)
	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, path, "")
	if err != nil {
		t.Fatalf("GET %s as the other principal: %v", path, err)
	}

	if status != http.StatusBadRequest || !strings.Contains(body, `"errorCode":"request.page-token-invalid"`) {
		t.Fatalf("GET %s as the other principal answers %d, want 400 with request.page-token-invalid (%s)",
			path, status, body)
	}
}

// fetchNamespaceListAs reads one page of the namespace list as token and
// decodes it.
func fetchNamespaceListAs(
	ctx context.Context, t *testing.T, env *e2e.Env, token, route string,
) e2eNamespaceList {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, route, "")
	if err != nil {
		t.Fatalf("GET %s: %v", route, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET %s answers %d, want %d (%s)", route, status, http.StatusOK, body)
	}

	var list e2eNamespaceList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode GET %s %q: %v", route, body, err)
	}

	return list
}
