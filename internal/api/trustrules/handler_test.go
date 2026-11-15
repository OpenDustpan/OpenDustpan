package trustrules_test

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/api/trustrules"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestTrustRuleRoundTrip creates two rules, reads them one by one and as a
// list in the order of their names, deletes one, and reads it as gone.
func TestTrustRuleRoundTrip(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	created := e.mustCreate("deploy")
	checkCreated(t, e, &created)

	e.mustCreate("audit")

	if got := e.mustGet(created.ID); got.ID != created.ID || got.Name != "deploy" || got.Issuer != created.Issuer {
		t.Fatalf("get = %+v, want %+v", got, created)
	}

	if list := e.mustList(); len(list) != 2 || list[0].Name != "audit" || list[1].Name != "deploy" {
		t.Fatalf("list = %+v, want audit, then deploy", list)
	}

	if status, body := e.admin(http.MethodDelete, rulesURL+"/"+created.ID, ""); status != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", status, body)
	}

	status, body := e.admin(http.MethodGet, rulesURL+"/"+created.ID, "")
	if status != http.StatusNotFound || errorCode(t, body) != "trust-rule.not-found" {
		t.Fatalf("get after delete = %d %s, want 404 trust-rule.not-found", status, body)
	}
}

// checkCreated fails the test when the created rule does not hold the
// fields of the request, the caller, and the conditions sorted by their claim
// names.
func checkCreated(t *testing.T, e *env, created *rule) {
	t.Helper()

	if created.Namespace != "acme" || created.Principal != e.worker || created.CreatedBy != "ada" ||
		created.KeySource != "discovery" || created.StaticKeys != "" || created.CreatedAt == "" {
		t.Fatalf("created rule = %+v", created)
	}

	if len(created.Conditions) != 2 || created.Conditions[0].Claim != "repository" ||
		created.Conditions[1].Claim != "sub" {
		t.Fatalf("conditions = %+v, want repository, then sub", created.Conditions)
	}
}

// TestTrustRuleChangesWriteAuditEntries proves that a create and a delete
// each write one audit entry in the namespace, and that the entry of the
// create names the issuer, the audience, the principal and the conditions.
func TestTrustRuleChangesWriteAuditEntries(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	status, body := e.admin(http.MethodPost, rulesURL, ruleBody("deploy", e.worker))
	if status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, body)
	}

	var created rule
	decode(t, body, &created)

	if status, body = e.admin(http.MethodDelete, rulesURL+"/"+created.ID, ""); status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", status, body)
	}

	entries := e.ruleEntries()
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d, want 2", len(entries))
	}

	create, del := entries[0], entries[1]
	if create.Action != trustrulesvc.ActionCreate || create.ObjectID != created.ID || create.Subject != "ada" {
		t.Errorf("create entry = %s %s by %s", create.Action, create.ObjectID, create.Subject)
	}

	changed := map[string]string{}
	for _, c := range create.Changes {
		changed[c.Key] = c.New
	}

	for key, want := range map[string]string{
		"issuer": created.Issuer, "audience": created.Audience, "principal": e.worker, "keySource": "discovery",
		"conditions": `repository in ["acme/infra"]; sub in ["repo:acme/infra:ref:refs/heads/main"]`,
	} {
		if changed[key] != want {
			t.Errorf("change %s = %q, want %q", key, changed[key], want)
		}
	}

	if del.Action != trustrulesvc.ActionDelete || del.ObjectID != created.ID {
		t.Errorf("delete entry = %s %s", del.Action, del.ObjectID)
	}
}

// TestTrustRuleCreateRefusals sends creates that the service or the store
// refuses, and checks the status, the code, and that no rule and no entry
// remain.
func TestTrustRuleCreateRefusals(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	person := e.principal("", principal.KindPerson)
	installationService := e.principal("", principal.KindService)

	if status, body := e.admin(http.MethodPost, rulesURL, ruleBody("taken", e.worker)); status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, body)
	}

	cases := []struct {
		name, body, token string
		status            int
		code              string
	}{
		{"missing field", `{"name":"x"}`, adminToken, http.StatusBadRequest, "request.body-invalid"},
		{"unknown field", `{"name":"x","jku":"https://x"}`, adminToken, http.StatusBadRequest, "request.body-invalid"},
		{
			"no subject condition",
			`{"name":"x","issuer":"https://token.actions.example.com","audience":"a","keySource":"discovery",` +
				`"principal":` + quote(e.worker) + `,"conditions":[{"claim":"repository","values":["acme/infra"]}]}`,
			adminToken, http.StatusBadRequest, "trust-rule.invalid",
		},
		{
			"issuer of this installation",
			`{"name":"x","issuer":"https://DUSTPAN.example.com/oidc/ns-1","audience":"a","keySource":"discovery",` +
				`"principal":` + quote(e.worker) + `,"conditions":[{"claim":"sub","values":["s"]}]}`,
			adminToken, http.StatusBadRequest, "trust-rule.issuer-refused",
		},
		{"a person", ruleBody("x", person), adminToken, http.StatusBadRequest, "trust-rule.principal-refused"},
		{
			"a principal of the installation", ruleBody("x", installationService), adminToken,
			http.StatusBadRequest, "trust-rule.principal-refused",
		},
		{
			"a principal of another namespace", ruleBody("x", e.stranger), adminToken,
			http.StatusBadRequest, "trust-rule.principal-refused",
		},
		{"a taken name", ruleBody("taken", e.worker), adminToken, http.StatusConflict, "trust-rule.already-exists"},
		{
			"a stored access token", ruleBody("x", e.worker), storedToken,
			http.StatusForbidden, "trust-rule.sign-in-required",
		},
	}

	for _, tc := range cases {
		status, body := e.do(http.MethodPost, rulesURL, tc.token, tc.body)
		if status != tc.status || errorCode(t, body) != tc.code {
			t.Errorf("%s: create = %d %s, want %d %s", tc.name, status, body, tc.status, tc.code)
		}
	}

	if got := len(e.ruleEntries()); got != 1 {
		t.Errorf("audit entries = %d, want only the first create", got)
	}
}

// TestTrustRuleAccess checks the action of each route, and that a create
// also needs principal:manage-tokens on the principal of the rule.
func TestTrustRuleAccess(t *testing.T) {
	t.Parallel()

	read := identity.ActionNamespaceRead

	cases := []struct {
		name    string
		holds   []identity.Action
		method  string
		path    string
		body    bool
		status  int
		created bool
	}{
		{
			"list needs read-access",
			[]identity.Action{read},
			http.MethodGet,
			rulesURL,
			false,
			http.StatusForbidden,
			false,
		},
		{
			"list with read-access",
			[]identity.Action{read, identity.ActionNamespaceReadAccess},
			http.MethodGet, rulesURL, false, http.StatusOK, false,
		},
		{
			"create needs manage-trust-rules",
			[]identity.Action{read, identity.ActionPrincipalManageTokens},
			http.MethodPost, rulesURL, true, http.StatusForbidden, false,
		},
		{
			"create needs manage-tokens on the principal",
			[]identity.Action{read, identity.ActionNamespaceManageTrustRules},
			http.MethodPost, rulesURL, true, http.StatusForbidden, false,
		},
		{
			"create with both",
			[]identity.Action{
				read, identity.ActionNamespaceManageTrustRules, identity.ActionPrincipalManageTokens,
			},
			http.MethodPost, rulesURL, true, http.StatusCreated, true,
		},
		{
			"no read of the namespace",
			[]identity.Action{
				identity.ActionNamespaceReadAccess, identity.ActionNamespaceManageTrustRules,
			},
			http.MethodGet, rulesURL, false, http.StatusNotFound, false,
		},
		{
			"an unknown namespace",
			[]identity.Action{read, identity.ActionNamespaceReadAccess},
			http.MethodGet, "/api/dustpan/v1/namespaces/absent/trust-rules", false, http.StatusNotFound, false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, holdsOnly{actions: tc.holds})

			body := ""
			if tc.body {
				body = ruleBody("deploy", e.worker)
			}

			status, answer := e.admin(tc.method, tc.path, body)
			if status != tc.status {
				t.Fatalf("%s %s = %d %s, want %d", tc.method, tc.path, status, answer, tc.status)
			}

			rules, err := e.store.TrustRules().List(t.Context(), "acme")
			if err != nil || (len(rules) == 1) != tc.created {
				t.Fatalf("rules after the request = %v, %v; want created %v", rules, err, tc.created)
			}
		})
	}
}

// TestTrustRuleReadsAnswer404 proves that an ID that is not a rule ID, an
// unknown rule, and a rule of another namespace each answer the same 404.
func TestTrustRuleReadsAnswer404(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	status, body := e.admin(http.MethodPost, "/api/dustpan/v1/namespaces/other/trust-rules",
		ruleBody("theirs", e.stranger))
	if status != http.StatusCreated {
		t.Fatalf("create in other = %d %s", status, body)
	}

	var theirs rule
	decode(t, body, &theirs)

	for _, id := range []string{"not-a-rule", "trr-00000000000000000000000000000000", theirs.ID} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			status, body := e.admin(method, rulesURL+"/"+id, "")
			if status != http.StatusNotFound || errorCode(t, body) != "trust-rule.not-found" {
				t.Errorf("%s %s = %d %s, want 404 trust-rule.not-found", method, id, status, body)
			}
		}
	}
}

// TestTrustRuleRefusesTheCredential proves that a request with no valid
// credential answers 401 and changes nothing.
func TestTrustRuleRefusesTheCredential(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	for _, token := range []string{"", "wrong"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			status, body := e.do(method, rulesURL, token, ruleBody("x", e.worker))
			if status != http.StatusUnauthorized {
				t.Errorf("%s with %q = %d %s, want 401", method, token, status, body)
			}
		}
	}

	if got := len(e.ruleEntries()); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

// ruleActions are the routes of the trust rules with the one action that each
// declares.
func ruleActions() map[string]identity.Action {
	return map[string]identity.Action{
		http.MethodGet + " " + trustrules.RulesPath:   identity.ActionNamespaceReadAccess,
		http.MethodPost + " " + trustrules.RulesPath:  identity.ActionNamespaceManageTrustRules,
		http.MethodGet + " " + trustrules.RulePath:    identity.ActionNamespaceReadAccess,
		http.MethodDelete + " " + trustrules.RulePath: identity.ActionNamespaceManageTrustRules,
		// The public list: the exchange needs no sign-in, and declares no
		// action. The token of the workload is its credential.
		http.MethodPost + " " + trustrules.ExchangePath: "",
	}
}

// TestRouteTable proves the rules of the route table: the table and the mux
// list the same patterns, no route is public, each route resolves its
// target, has a handler and declares exactly one action, and a route that
// changes data declares namespace:manage-trust-rules.
func TestRouteTable(t *testing.T) {
	t.Parallel()

	e := newEnv(t, identity.AdminOnly{})
	want := ruleActions()
	got := map[string]identity.Action{}
	patterns := make([]string, 0, len(want))

	for _, rt := range e.handler.Routes() {
		patterns = append(patterns, rt.Pattern())

		if action, ok := checkRoute(t, &rt); ok {
			got[rt.Pattern()] = action
		}
	}

	if len(got) != len(want) {
		t.Fatalf("route table lists %v, want %v", got, want)
	}

	for key, action := range want {
		if got[key] != action {
			t.Errorf("route %s declares %s, want %s", key, got[key], action)
		}
	}

	registered := e.handler.RegisteredPatterns()

	slices.Sort(patterns)
	slices.Sort(registered)

	if !slices.Equal(patterns, registered) {
		t.Errorf("table patterns = %v, registered = %v, want equal", patterns, registered)
	}

	if len(e.handler.Operations()) != len(want) {
		t.Errorf("operations = %d, want %d", len(e.handler.Operations()), len(want))
	}
}

// checkRoute fails the test for a route that is not complete, or that
// changes data without namespace:manage-trust-rules. It returns the one
// action of the route.
func checkRoute(t *testing.T, rt *trustrules.Route) (identity.Action, bool) {
	t.Helper()

	if rt.Scope == route.ScopePublic {
		if rt.Pattern() != http.MethodPost+" "+trustrules.ExchangePath || len(rt.Actions) != 0 || rt.Serve == nil {
			t.Errorf("route %s is public and not the exchange", rt.Pattern())
		}

		return "", true
	}

	if rt.Scope == 0 || rt.Resolve == nil || rt.Serve == nil || len(rt.Actions) != 1 {
		t.Errorf("route %s is not complete: %+v", rt.Pattern(), rt.Actions)

		return "", false
	}

	if rt.Method != http.MethodGet && rt.Actions[0] != identity.ActionNamespaceManageTrustRules {
		t.Errorf("route %s changes data without namespace:manage-trust-rules", rt.Pattern())
	}

	return rt.Actions[0], true
}

// TestNewHandlerNeedsItsDependencies proves that each required dependency is
// checked.
func TestNewHandlerNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	e := newEnv(t, identity.AdminOnly{})

	namespaces, err := namespace.NewService(namespace.Config{Store: e.store, Now: nil, Installation: installationName})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	rules, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: e.store.TrustRules(), Namespaces: namespaces, Principals: e.store, Authorizer: identity.AdminOnly{},
		Reach:            nil,
		InstallationURLs: nil, OwnIssuer: nil, CheckURL: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	full := func() trustrules.Config {
		return trustrules.Config{
			Exchanger:      nil,
			Namespaces:     namespaces,
			Rules:          rules,
			Authenticator:  tokenAuthenticator{},
			Authorizer:     identity.AdminOnly{},
			Audit:          newTestAudit(),
			OnAuditFailure: nil,
			DenialLimiter:  nil,
			Sessions:       nil,
			Logger:         slog.New(slog.DiscardHandler),
			Now:            nil,
		}
	}

	cases := []struct {
		name   string
		change func(c *trustrules.Config)
	}{
		{"no namespace service", func(c *trustrules.Config) { c.Namespaces = nil }},
		{"no rule service", func(c *trustrules.Config) { c.Rules = nil }},
		{"no authenticator", func(c *trustrules.Config) { c.Authenticator = nil }},
		{"no authorizer", func(c *trustrules.Config) { c.Authorizer = nil }},
		{"no audit store", func(c *trustrules.Config) { c.Audit = nil }},
		{"no logger", func(c *trustrules.Config) { c.Logger = nil }},
	}

	for _, tc := range cases {
		cfg := full()
		tc.change(&cfg)

		h, newErr := trustrules.NewHandler(cfg)
		if !errors.Is(newErr, trustrules.ErrMissingDependency) || h != nil {
			t.Errorf("%s: NewHandler = %v, %v, want ErrMissingDependency", tc.name, h, newErr)
		}
	}
}
