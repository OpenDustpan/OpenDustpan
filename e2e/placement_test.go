//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// placementNamespace is the namespace that the placement rule steps use.
const placementNamespace = "placements"

// placementBase is the namespace path of the placement rule routes, and
// placementRules is the rule list below it.
const (
	placementBase  = "/api/dustpan/v1/namespaces/" + placementNamespace
	placementRules = placementBase + "/placement-rules"
)

// placementRule holds the fields of a placement rule that the steps check.
type placementRule struct {
	ID         string `json:"id"`
	Position   int    `json:"position"`
	Stack      string `json:"stack"`
	Collection string `json:"collection"`
}

// placementRuleList is the answer of the rule list.
type placementRuleList struct {
	Rules []placementRule `json:"rules"`
}

// TestPlacementRuleAPI proves the placement rule routes of a namespace through
// the real server on each store. No CLI reads or writes a rule, and the server
// does not apply the rules to a new stack yet, so the steps call the routes
// only. The steps make, list, read, replace, move and delete rules, refuse a
// rule that a hidden rule would make, and refuse a caller that holds no
// namespace:manage-placement-rules.
func TestPlacementRuleAPI(t *testing.T) {
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
			runPlacementRuleAPIOnStore(t, cli, db)
		})
	}
}

func runPlacementRuleAPIOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + placementNamespace + `"}`, http.StatusCreated,
	}, nil)

	// A rule names a collection that exists.
	for _, name := range []string{"payments", "payments-prod"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, placementBase + "/collections", collectionBody(t, name), http.StatusCreated,
		}, nil)
	}

	placementRulesMakeAndRead(ctx, t, env)
	placementRulesRefusals(ctx, t, env)
	placementRulesMoveAndDelete(ctx, t, env)
	placementRulesDenied(ctx, t, env)
}

// placementCall sends s and fails unless it answers s.want. It checks that the
// body holds each part, and it answers the body.
func placementCall(ctx context.Context, t *testing.T, env *e2e.Env, s principalStep, parts ...string) string {
	t.Helper()

	status, body := freshServerCall(ctx, t, env, s)
	if status != s.want {
		t.Fatalf("%s %s answers %d, want %d (%s)", s.method, s.path, status, s.want, body)
	}

	wantBodyHas(t, s.method+" "+s.path, body, parts...)

	return body
}

// placementList reads the rule list and returns its rules in list order.
func placementList(ctx context.Context, t *testing.T, env *e2e.Env) []placementRule {
	t.Helper()

	var doc placementRuleList
	body := placementCall(ctx, t, env, principalStep{http.MethodGet, placementRules, "", http.StatusOK})

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the rule list %s: %v", body, err)
	}

	return doc.Rules
}

// placementRulesMakeAndRead makes two rules, lists them in order and reads
// one of them by ID. A rule that no earlier rule hides gets the next place.
func placementRulesMakeAndRead(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	if rules := placementList(ctx, t, env); len(rules) != 0 {
		t.Fatalf("a new namespace holds %d placement rules, want 0", len(rules))
	}

	narrow := placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"payments","stack":"prod*","collection":"payments-prod"}`,
		http.StatusCreated,
	}, `"position":1`, `"id":"plr-`)

	wide := placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"payments","stack":"*","collection":"payments"}`,
		http.StatusCreated,
	}, `"position":2`)

	rules := placementList(ctx, t, env)
	if len(rules) != 2 {
		t.Fatalf("the list holds %d rules, want 2", len(rules))
	}

	first := placementRuleOf(t, narrow)
	second := placementRuleOf(t, wide)

	if rules[0].ID != first.ID || rules[1].ID != second.ID {
		t.Fatalf("the list is out of order: %+v, want %s then %s", rules, first.ID, second.ID)
	}

	placementCall(ctx, t, env, principalStep{
		http.MethodGet, placementRules + "/" + first.ID, "", http.StatusOK,
	}, `"collection":"payments-prod"`)

	placementCall(ctx, t, env, principalStep{
		http.MethodGet, placementRules + "/plr-missing", "", http.StatusNotFound,
	}, "placement.rule-not-found")
}

// placementRulesRefusals sends the writes that the server must refuse. A
// refused write stores no rule, so the list keeps its two rules.
func placementRulesRefusals(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	// The rule wide hides a later rule with the patterns payments and prod*.
	placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"payments","stack":"prod*","collection":"payments"}`,
		http.StatusConflict,
	}, "placement.conflict")

	placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"payments","stack":"prod?","collection":"payments"}`,
		http.StatusBadRequest,
	}, "placement.invalid-pattern")

	placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"payments","stack":"dev","collection":"payments","position":9}`,
		http.StatusBadRequest,
	}, "placement.invalid-position")

	if rules := placementList(ctx, t, env); len(rules) != 2 {
		t.Fatalf("refused writes changed the list: %d rules, want 2", len(rules))
	}
}

// placementRulesMoveAndDelete changes a rule in place, refuses a move that
// hides a rule, and deletes the rules. A delete closes the gap in the places.
func placementRulesMoveAndDelete(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	rules := placementList(ctx, t, env)
	narrow, wide := rules[0].ID, rules[1].ID

	// A change without a position keeps the place of the rule.
	placementCall(ctx, t, env, principalStep{
		http.MethodPut, placementRules + "/" + narrow,
		`{"tool":"pulumi","project":"payments","stack":"prod-eu*","collection":"payments-prod"}`,
		http.StatusOK,
	}, `"stack":"prod-eu*"`, `"position":1`)

	// Moved to the front, the rule payments and * hides the rule that follows it.
	placementCall(ctx, t, env, principalStep{
		http.MethodPut, placementRules + "/" + wide,
		`{"tool":"pulumi","project":"payments","stack":"*","collection":"payments","position":1}`,
		http.StatusConflict,
	}, "placement.conflict")

	placementCall(ctx, t, env, principalStep{
		http.MethodDelete, placementRules + "/" + narrow, "", http.StatusNoContent,
	})
	placementCall(ctx, t, env, principalStep{
		http.MethodDelete, placementRules + "/" + narrow, "", http.StatusNotFound,
	}, "placement.rule-not-found")

	rules = placementList(ctx, t, env)
	if len(rules) != 1 || rules[0].ID != wide || rules[0].Position != 1 {
		t.Fatalf("after the delete the list is %+v, want the rule %s at place 1", rules, wide)
	}

	placementCall(ctx, t, env, principalStep{
		http.MethodDelete, placementRules + "/" + wide, "", http.StatusNoContent,
	})
}

// placementRulesDenied proves that a person with the maintainer role on the
// namespace reads the rules and cannot write them. The role has
// namespace:read-placement-rules and not namespace:manage-placement-rules.
func placementRulesDenied(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	// The administrator makes the rule that the maintainer tries to change.
	seed := placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"payments","stack":"*","collection":"payments"}`,
		http.StatusCreated,
	}, `"position":1`)
	seedID := placementRuleOf(t, seed).ID

	var maintainer e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("placements-maintainer") + `","kind":"person"}`,
		http.StatusCreated,
	}, &maintainer)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + placementNamespace + "/grants",
		`{"subject":"principal:` + maintainer.ID + `","role":"maintainer"}`,
		http.StatusCreated,
	}, nil)

	token := env.PersonalToken(ctx, t, maintainer.ID, "placement-check")

	status, body, err := env.ServerAPIStatusAs(ctx, token.Secret, http.MethodGet, placementRules, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("maintainer reads the rules = %d, %v (%s); want 200", status, err, body)
	}

	const denied = `{"tool":"pulumi","project":"payments","stack":"dev","collection":"payments"}`

	for _, s := range []principalStep{
		{method: http.MethodPost, path: placementRules, body: denied, want: http.StatusForbidden},
		{method: http.MethodPut, path: placementRules + "/" + seedID, body: denied, want: http.StatusForbidden},
		{method: http.MethodDelete, path: placementRules + "/" + seedID, body: "", want: http.StatusForbidden},
	} {
		status, body, err := env.ServerAPIStatusAs(ctx, token.Secret, s.method, s.path, s.body)
		if err != nil || status != s.want {
			t.Fatalf("maintainer %s %s = %d, %v (%s); want %d", s.method, s.path, status, err, body, s.want)
		}
	}

	// The refused writes left the seed rule in place.
	if rules := placementList(ctx, t, env); len(rules) != 1 || rules[0].ID != seedID {
		t.Fatalf("refused writes changed the list: %+v, want the rule %s", rules, seedID)
	}

	placementCall(ctx, t, env, principalStep{
		http.MethodDelete, placementRules + "/" + seedID, "", http.StatusNoContent,
	})
}

// placementRuleOf decodes one rule from the body of an answer.
func placementRuleOf(t *testing.T, body string) placementRule {
	t.Helper()

	var r placementRule
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode the rule %s: %v", body, err)
	}

	return r
}
