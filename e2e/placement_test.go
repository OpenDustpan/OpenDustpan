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
// the real server on each store. No CLI reads or writes a rule, so the steps call
// the routes only. TestPlacement proves that the server applies the rules to a
// new stack. The steps make, list, read, replace, move and delete rules, refuse a
// rule that a hidden rule would make, and refuse a caller that holds no
// namespace:manage-placement-rules.
func TestPlacementRuleAPI(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

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
	placementRulesCustomRole(ctx, t, env)
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

	// A replace of a rule that the namespace does not hold answers 404 and
	// stores nothing.
	placementCall(ctx, t, env, principalStep{
		http.MethodPut, placementRules + "/plr-missing",
		`{"tool":"pulumi","project":"payments","stack":"dev","collection":"payments"}`,
		http.StatusNotFound,
	}, "placement.rule-not-found")

	// An ID with a leading dash has no valid form, so the server refuses it
	// before it reads the store.
	placementCall(ctx, t, env, principalStep{
		http.MethodGet, placementRules + "/-bad", "", http.StatusBadRequest,
	})

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

	// A third rule on another project overlaps no rule of payments. A PUT with
	// a position moves it to the front, and the list shows the new order.
	other := placementRuleOf(t, placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"billing","stack":"*","collection":"payments"}`,
		http.StatusCreated,
	}, `"position":3`))

	moved := placementCall(ctx, t, env, principalStep{
		http.MethodPut, placementRules + "/" + other.ID,
		`{"tool":"pulumi","project":"billing","stack":"*","collection":"payments","position":1}`,
		http.StatusOK,
	}, `"position":1`)
	if got := placementRuleOf(t, moved); got.ID != other.ID {
		t.Fatalf("the move answers rule %s, want %s", got.ID, other.ID)
	}

	rules = placementList(ctx, t, env)
	if len(rules) != 3 || rules[0].ID != other.ID || rules[1].ID != narrow || rules[2].ID != wide {
		t.Fatalf("after the move the list is %+v, want billing, narrow, wide", rules)
	}

	placementCall(ctx, t, env, principalStep{
		http.MethodDelete, placementRules + "/" + other.ID, "", http.StatusNoContent,
	})

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
		got, resp, callErr := env.ServerAPIStatusAs(ctx, token.Secret, s.method, s.path, s.body)
		if callErr != nil || got != s.want {
			t.Fatalf("maintainer %s %s = %d, %v (%s); want %d", s.method, s.path, got, callErr, resp, s.want)
		}
	}

	// A request with no credentials is refused before the route runs.
	status, body, err = env.ServerAPIStatusBare(ctx, http.MethodGet, placementRules)
	if err != nil || status != http.StatusUnauthorized {
		t.Fatalf("GET the rules with no token = %d, %v (%s); want 401", status, err, body)
	}

	// The refused writes left the seed rule in place.
	if rules := placementList(ctx, t, env); len(rules) != 1 || rules[0].ID != seedID {
		t.Fatalf("refused writes changed the list: %+v, want the rule %s", rules, seedID)
	}

	placementCall(ctx, t, env, principalStep{
		http.MethodDelete, placementRules + "/" + seedID, "", http.StatusNoContent,
	})
}

// placementRulesCustomRole proves that a custom role with
// namespace:read-placement-rules and namespace:manage-placement-rules, and no
// collection:create-unit, writes the rules and cannot make a stack in the
// collection that a rule names. The role holds unit:manage, so that the stack
// request passes the unit check and reaches the check of the collection.
func placementRulesCustomRole(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var role e2eRole
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles",
		`{"name":"placements-rules-only","actions":[` +
			`"namespace:read-placement-rules","namespace:manage-placement-rules","unit:manage"]}`,
		http.StatusCreated,
	}, &role)

	var custom e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("placements-rules-only") + `","kind":"person"}`,
		http.StatusCreated,
	}, &custom)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + placementNamespace + "/grants",
		`{"subject":"principal:` + custom.ID + `","role":"` + role.ID + `"}`,
		http.StatusCreated,
	}, nil)

	token := env.PersonalToken(ctx, t, custom.ID, "placement-rules-only-check")

	project := e2e.Project
	rule := `{"tool":"pulumi","project":"` + project + `","stack":"*","collection":"payments"}`

	status, body, err := env.ServerAPIStatusAs(ctx, token.Secret, http.MethodPost, placementRules, rule)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("custom role writes a rule = %d, %v (%s); want %d", status, err, body, http.StatusCreated)
	}

	status, body, err = env.APIStatusAs(ctx, token.Secret, http.MethodPost,
		"/api/stacks/"+placementNamespace+"/"+project, `{"tags":{},"stackName":"dev"}`)
	// The control below shows that the refusal comes from the collection check:
	// the same request by a role that also holds collection:create-unit makes
	// the stack. The body does not name the action.
	if err != nil || status != http.StatusForbidden {
		t.Fatalf("custom role makes a stack without collection:create-unit = %d, %v (%s); want %d",
			status, err, body, http.StatusForbidden)
	}

	if got, found := placementCollectionOf(ctx, t, env, "dev"); found {
		t.Fatalf("the refused stack exists in collection %q, want no unit", got)
	}

	// The control: a role that also holds collection:create-unit makes the
	// stack.
	var createRole e2eRole
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles",
		`{"name":"placements-create-unit","actions":[` +
			`"namespace:read-placement-rules","namespace:manage-placement-rules","unit:manage",` +
			`"collection:create-unit"]}`,
		http.StatusCreated,
	}, &createRole)

	var creator e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("placements-create-unit") + `","kind":"person"}`,
		http.StatusCreated,
	}, &creator)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + placementNamespace + "/grants",
		`{"subject":"principal:` + creator.ID + `","role":"` + createRole.ID + `"}`,
		http.StatusCreated,
	}, nil)

	controlToken := env.PersonalToken(ctx, t, creator.ID, "placements-create-unit-check")

	status, body, err = env.APIStatusAs(ctx, controlToken.Secret, http.MethodPost,
		"/api/stacks/"+placementNamespace+"/"+project, `{"tags":{},"stackName":"control"}`)
	if err != nil || status != http.StatusOK {
		t.Fatalf("the control role makes a stack with collection:create-unit = %d, %v (%s); want %d",
			status, err, body, http.StatusOK)
	}

	if got, found := placementCollectionOf(ctx, t, env, "control"); !found || got != "payments" {
		t.Fatalf("the control stack is in collection %q (found %t), want %q", got, found, "payments")
	}
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

// TestPlacement proves that the server puts a new Pulumi stack in the
// collection of the first placement rule that matches it, and refuses a stack
// that no rule matches, with the real Pulumi CLI on each store. The e2e
// namespace of the harness holds a catch-all rule for the other scenarios; the
// steps here use a namespace with no rule but their own.
func TestPlacement(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

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

	scenarios := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, env *e2e.Env)
	}{
		{"pulumi", placementPulumiPlaces},
		{"refused", placementPulumiRefused},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()

					if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
						t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
					}

					ctx := t.Context()
					env := e2e.NewEnv(ctx, t, cli, db)

					checkReady(ctx, t, env)
					sc.run(ctx, t, env)
				})
			}
		})
	}
}

// placementSetup makes the namespace of the placement steps with the
// collections in collections.
func placementSetup(ctx context.Context, t *testing.T, env *e2e.Env, collections ...string) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + placementNamespace + `"}`, http.StatusCreated,
	}, nil)

	for _, name := range collections {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, placementBase + "/collections", collectionBody(t, name), http.StatusCreated,
		}, nil)
	}
}

// placementUnitList is the part of the unit list that the placement steps read.
type placementUnitList struct {
	Units []placementUnit `json:"units"`
}

// placementUnit is one unit of the unit list.
type placementUnit struct {
	Reference  string `json:"reference"`
	Collection string `json:"collection"`
}

// placementCollectionOf returns the collection of the Pulumi stack stackName
// of the e2e project in the placement namespace, and false when the namespace
// holds no such unit.
func placementCollectionOf(ctx context.Context, t *testing.T, env *e2e.Env, stackName string) (string, bool) {
	t.Helper()

	var doc placementUnitList

	body := placementCall(ctx, t, env, principalStep{http.MethodGet, placementBase + "/units", "", http.StatusOK})
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the unit list %s: %v", body, err)
	}

	want := "pulumi:" + placementNamespace + "/" + e2e.Project + "/" + stackName

	for _, u := range doc.Units {
		if u.Reference == want {
			return u.Collection, true
		}
	}

	return "", false
}

// placementPulumiPlaces makes two rules, runs "pulumi stack init" for a stack
// of each rule, and reads the collection of each new unit from the unit list.
func placementPulumiPlaces(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	placementSetup(ctx, t, env, "payments", "payments-prod")

	for _, rule := range []string{
		`{"tool":"pulumi","project":"` + e2e.Project + `","stack":"prod*","collection":"payments-prod"}`,
		`{"tool":"pulumi","project":"` + e2e.Project + `","stack":"*","collection":"payments"}`,
	} {
		placementCall(ctx, t, env, principalStep{http.MethodPost, placementRules, rule, http.StatusCreated})
	}

	env.Step(ctx, t, step("login", contains(env.URL), "login"))

	for _, c := range []struct{ stack, collection string }{
		{"prod-eu", "payments-prod"},
		{"dev", "payments"},
	} {
		env.Step(ctx, t, step("stack init "+c.stack, nil,
			"stack", "init", placementNamespace+"/"+e2e.Project+"/"+c.stack, "--no-select"))

		got, found := placementCollectionOf(ctx, t, env, c.stack)
		if !found || got != c.collection {
			t.Fatalf("stack %s is in collection %q (found %t), want %q", c.stack, got, found, c.collection)
		}
	}
}

// placementPulumiRefused runs "pulumi stack init" for a stack that no rule
// matches. The CLI fails with the error that names the rules, and the server
// makes no unit.
func placementPulumiRefused(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	placementSetup(ctx, t, env, "payments")

	placementCall(ctx, t, env, principalStep{
		http.MethodPost, placementRules,
		`{"tool":"pulumi","project":"another-project","stack":"*","collection":"payments"}`,
		http.StatusCreated,
	})

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, failStep("stack init with no matching rule", outputContains("no placement rule matches"),
		"stack", "init", placementNamespace+"/"+e2e.Project+"/dev", "--no-select"))

	if got, found := placementCollectionOf(ctx, t, env, "dev"); found {
		t.Fatalf("the refused stack exists in collection %q, want no unit", got)
	}
}
