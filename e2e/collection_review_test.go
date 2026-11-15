//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// putInCollection makes the collection name in the namespace review, and
// moves the pulumi stack unit into it, as the admin principal.
func putInCollection(ctx context.Context, t *testing.T, env *e2e.Env, collectionName, unit string) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodPost, "/api/dustpan/v1/namespaces/review/collections",
		collectionBody(t, collectionName))
	if err != nil || (status != http.StatusCreated && status != http.StatusConflict) {
		t.Fatalf("make collection %s = %d, %v (%s)", collectionName, status, err, body)
	}

	status, body, err = env.APIStatus(ctx, http.MethodPut,
		"/api/inventory/unit-collection?unit="+url.QueryEscape("pulumi:review/"+unit), moveBody(t, collectionName))
	if err != nil || status != http.StatusOK {
		t.Fatalf("move %s into %s = %d, %v (%s)", unit, collectionName, status, err, body)
	}
}

// checkCollectionReview checks the export of a collection. It holds the
// grants on the collection, on each unit in it and on the namespace that
// contains it, and no grant on another collection, on a unit of another
// collection, or on a unit with no collection. An unknown collection answers
// 404.
func checkCollectionReview(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	createStack(ctx, t, env, "review/api/stage")
	createStack(ctx, t, env, "review/web/other")
	createStack(ctx, t, env, "review/db/loose")
	putInCollection(ctx, t, env, "payments", "api/stage")
	putInCollection(ctx, t, env, "platform", "web/other")

	// A new principal, so that the later steps of the test see no new grant of carol.
	var erin e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("review-collection") + `","kind":"person"}`, http.StatusCreated,
	}, &erin)

	grant := func(path, role string) {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, path, `{"subject":"principal:` + erin.ID + `","role":"` + role + `"}`, http.StatusCreated,
		}, nil)
	}

	grant("/api/server/namespaces/review/collections/payments/grants", "viewer")
	grant("/api/server/namespaces/review/unit-grants?tool=pulumi&unit=api%2Fstage", "maintainer")
	grant("/api/server/namespaces/review/collections/platform/grants", "writer")
	grant("/api/server/namespaces/review/unit-grants?tool=pulumi&unit=web%2Fother", "maintainer")
	grant("/api/server/namespaces/review/unit-grants?tool=pulumi&unit=db%2Floose", "auditor")

	rows := reviewOf(ctx, t, env, "/api/server/namespaces/review/collections/payments/access-review", url.Values{})

	got := make([]string, 0, len(rows))

	for i := range rows {
		got = append(got, rows[i].Role+"@"+rows[i].Object)
	}

	slices.Sort(got)

	for _, want := range []string{"viewer@review/payments", "maintainer@pulumi:review/api/stage"} {
		if !slices.ContainsFunc(got, func(g string) bool { return strings.HasPrefix(g, want) }) {
			t.Fatalf("collection export = %q, want a row %q", got, want)
		}
	}

	for _, leak := range []string{"platform", "web/other", "db/loose"} {
		if slices.ContainsFunc(got, func(g string) bool { return strings.Contains(g, leak) }) {
			t.Fatalf("collection export = %q, want no row that names %q", got, leak)
		}
	}

	for _, inherited := range []string{"writer@review<review", "auditor@review<review"} {
		if !slices.Contains(reviewRoles(rows), inherited) {
			t.Fatalf("collection export = %q, want the row %q from the namespace", reviewRoles(rows), inherited)
		}
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/review/collections/absent/access-review", "", http.StatusNotFound,
	}, nil)
}

// deletedNames returns the names of the deleted units that the token reads.
func deletedNames(ctx context.Context, t *testing.T, env *e2e.Env, secret, namespace string) []string {
	t.Helper()

	status, body, err := env.APIStatusAs(ctx, secret, http.MethodGet,
		"/api/dustpan/v1/namespaces/"+namespace+"/deleted-units", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list deleted units = %d, %v (%s), want 200", status, err, body)
	}

	var doc deletedUnitsDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode deleted units: %v", err)
	}

	names := make([]string, 0, len(doc.Units))
	for i := range doc.Units {
		names = append(names, doc.Units[i].Name)
	}

	slices.Sort(names)

	return names
}

// TestDeletedUnits drives the read of deleted units through the real binary
// with a caller that has no grant on the namespace.
func TestDeletedUnits(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s: %v", e2e.Pulumi().Name, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			t.Run("collection_grant_reads", func(t *testing.T) {
				checkCollectionGrantReads(t, cli, db)
			})
		})
	}
}

// checkCollectionGrantReads proves that a grant on a collection reads the
// deleted units whose last collection it was, and no other deleted unit.
func checkCollectionGrantReads(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var dana e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("dana") + `","kind":"person"}`, http.StatusCreated,
	}, &dana)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"review"}`, http.StatusCreated,
	}, nil)

	for _, unit := range []string{"api/stage", "web/other", "db/loose"} {
		createStack(ctx, t, env, "review/"+unit)
	}

	putInCollection(ctx, t, env, "payments", "api/stage")
	putInCollection(ctx, t, env, "platform", "web/other")

	// The list needs namespace:read on the namespace. This role gives no
	// unit:read, so only the grant on the collection reads a unit.
	var role e2eRole
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles", `{"name":"ns-reader","actions":["namespace:read"]}`, http.StatusCreated,
	}, &role)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/review/grants",
		`{"subject":"principal:` + dana.ID + `","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/review/collections/payments/grants",
		`{"subject":"principal:` + dana.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)

	secret := env.PersonalToken(ctx, t, dana.ID, "review").Secret

	if got := deletedNames(ctx, t, env, secret, "review"); len(got) != 0 {
		t.Fatalf("deleted units before any delete = %q, want none", got)
	}

	for _, unit := range []string{"api/stage", "web/other", "db/loose"} {
		status, body, err := env.APIStatus(ctx, http.MethodDelete, "/api/stacks/review/"+unit, "")
		if err != nil || status != http.StatusNoContent {
			t.Fatalf("delete stack %s = %d, %v (%s), want 204", unit, status, err, body)
		}
	}

	if got := deletedNames(ctx, t, env, secret, "review"); !slices.Equal(got, []string{"api/stage"}) {
		t.Fatalf("deleted units of a collection grant = %q, want only the stack of payments", got)
	}
}
