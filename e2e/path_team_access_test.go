//go:build e2e

// This file covers the team access path. The persona is Ana, a payments
// developer. Ana signs in through LDAP, and her group maps to payments-dev, a
// writer on the collection payments. A placement rule puts a Pulumi stack and
// an OpenTofu HTTP-backend state into payments. Bob is not in the group, so he
// gets a 403 that names the collection. The access review lists Ana through
// the group.
//
// User-visible end checks: the docs page for LDAP group mapping, the output of
// "pulumi stack init" and the unit list of the namespace, the OpenTofu init
// output, the 403 body that names the collection, the access review export,
// and the members view of the console.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// teamAccessTest is the name of this test, used in the names of skipped steps.
const teamAccessTest = "TestPathTeamAccess"

// teamAccessNamespace is the namespace that the team access steps use, and
// teamAccessBase is its path in the namespace API.
const (
	teamAccessNamespace = "teamaccess"
	teamAccessBase      = "/api/dustpan/v1/namespaces/" + teamAccessNamespace
	teamAccessRules     = teamAccessBase + "/placement-rules"
)

// teamAccessUnitList is the part of the unit list that the steps read.
type teamAccessUnitList struct {
	Units []teamAccessUnit `json:"units"`
}

// teamAccessUnit is one unit of the unit list.
type teamAccessUnit struct {
	Reference  string `json:"reference"`
	Collection string `json:"collection"`
}

// TestPathTeamAccess runs the team access steps on each store. The steps that
// need a missing feature are skipped with the plan items that they wait for.
func TestPathTeamAccess(t *testing.T) {
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

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runTeamAccessOnStore(t, cli, db)
		})
	}
}

// runTeamAccessOnStore runs the steps in order on one store. The steps share
// one environment, so a later step sees the namespace that an earlier one made.
func runTeamAccessOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	t.Run("ldap_sign_in", func(t *testing.T) {
		teamAccessBlocked(t, env, "ldap_sign_in", "blocked: LDAP sign-in is not built")
	})

	t.Run("group_rule_maps_payments", func(t *testing.T) {
		teamAccessBlocked(t, env, "group_rule_maps_payments",
			"blocked: group rules do not map groups to collections yet")
	})

	t.Run("placement_pulumi", func(t *testing.T) {
		teamAccessPlacementPulumi(ctx, t, env)
	})

	t.Run("placement_tofu_http", func(t *testing.T) {
		teamAccessBlocked(t, env, "placement_tofu_http",
			"blocked: a unit with no collection is still accepted")
	})

	t.Run("cli_up_and_apply_with_token", func(t *testing.T) {
		teamAccessBlocked(t, env, "cli_up_and_apply_with_token",
			"blocked: LDAP sign-in and group rules are not built")
	})

	t.Run("outsider_403_names_collection", func(t *testing.T) {
		teamAccessBlocked(t, env, "outsider_403_names_collection",
			"blocked: a unit with no collection is still accepted")
	})

	t.Run("access_review_lists_group_member", func(t *testing.T) {
		teamAccessReviewExport(ctx, t, env)
		teamAccessBlocked(t, env, "access_review_lists_group_member",
			"blocked: group rules do not map groups to collections yet")
	})

	t.Run("console_members_view", func(t *testing.T) {
		teamAccessBlocked(t, env, "console_members_view", "blocked: the console members view is not built")
	})
}

// teamAccessBlocked records that the step name does not run in this mode, and
// skips it. The reason names the missing capability.
func teamAccessBlocked(t *testing.T, env *e2e.Env, name, reason string) {
	t.Helper()

	env.SkipScenario(t, teamAccessTest+"/"+name, reason)
	t.Skip(reason)
}

// teamAccessPlacementPulumi makes the namespace, a collection payments and a
// rule that sends every stack of the e2e project into it. It then runs
// "pulumi stack init" and checks that the new unit is in payments.
func teamAccessPlacementPulumi(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + teamAccessNamespace + `"}`, http.StatusCreated,
	}, nil)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, teamAccessBase + "/collections", collectionBody(t, "payments"), http.StatusCreated,
	}, nil)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, teamAccessRules,
		`{"tool":"pulumi","project":"` + e2e.Project + `","stack":"*","collection":"payments"}`,
		http.StatusCreated,
	}, nil)

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, step("stack init dev", nil,
		"stack", "init", teamAccessNamespace+"/"+e2e.Project+"/dev", "--no-select"))

	got, found := teamAccessCollectionOf(ctx, t, env, "dev")
	if !found || got != "payments" {
		t.Fatalf("stack dev is in collection %q (found %t), want %q", got, found, "payments")
	}
}

// teamAccessCollectionOf returns the collection of the Pulumi stack stackName
// of the e2e project in the team access namespace, and false when the namespace
// holds no such unit.
func teamAccessCollectionOf(ctx context.Context, t *testing.T, env *e2e.Env, stackName string) (string, bool) {
	t.Helper()

	var doc teamAccessUnitList

	status, body := freshServerCall(ctx, t, env, principalStep{
		http.MethodGet, teamAccessBase + "/units", "", http.StatusOK,
	})
	if status != http.StatusOK {
		t.Fatalf("GET units answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the unit list %s: %v", body, err)
	}

	want := "pulumi:" + teamAccessNamespace + "/" + e2e.Project + "/" + stackName

	for _, u := range doc.Units {
		if u.Reference == want {
			return u.Collection, true
		}
	}

	return "", false
}

// teamAccessReviewExport calls the access review export of the namespace. The
// group assertion is a separate, blocked step.
func teamAccessReviewExport(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/" + teamAccessNamespace + "/access-review", "", http.StatusOK,
	}, nil)
}
