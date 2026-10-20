//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2eViewLabel, e2eViewQuery and e2eView decode a saved view.
type e2eViewLabel struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type e2eViewQuery struct {
	Labels     []e2eViewLabel `json:"labels"`
	References []string       `json:"references"`
}

type e2eView struct {
	ID        string       `json:"id"`
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
	Query     e2eViewQuery `json:"query"`
	CreatedAt string       `json:"createdAt"`
}

// e2eViewList decodes the list of saved views.
type e2eViewList struct {
	Views []e2eView `json:"views"`
}

// e2eUnitRow and e2eUnitPage decode one page of the unit list.
type e2eUnitRow struct {
	Reference string `json:"reference"`
}

type e2eUnitPage struct {
	Units []e2eUnitRow `json:"units"`
}

// TestSavedViewsAPI drives the saved view routes with the real server, on each
// store. No CLI calls these routes. The steps make stacks with tags through
// the API, sign two persons in with personal tokens, and prove that a person
// makes, lists, reads, opens and deletes a view, that the units of a view are
// the units of the same query in the unit list, that the bootstrap token and
// another person get no view, and that a deleted view is gone.
func TestSavedViewsAPI(t *testing.T) {
	t.Parallel()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runSavedViewsOnStore(t, db)
		})
	}
}

// viewScenario holds what the steps share: the server, the path of the views,
// and the tokens of the two persons.
type viewScenario struct {
	env  *e2e.Env
	path string
	ana  string
	bob  string
}

func runSavedViewsOnStore(t *testing.T, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, e2e.NoCLI(), store)

	checkReady(ctx, t, env)
	createTaggedStacks(ctx, t, env)

	s := &viewScenario{
		env:  env,
		path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/views",
		ana:  savedViewPerson(ctx, t, env, "ana@example.com"),
		bob:  savedViewPerson(ctx, t, env, "bob@example.com"),
	}

	created := s.create(ctx, t)

	s.reads(ctx, t, &created)
	s.units(ctx, t, &created)
	s.refusals(ctx, t, &created)
	s.delete(ctx, t, &created)
}

// createTaggedStacks makes three stacks with tags through the API.
func createTaggedStacks(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, body := range []struct{ project, stack, tags string }{
		{"web", "dev", `{"team":"payments"}`},
		{"web", "prod", `{"team":"payments","tier":"prod"}`},
		{"api", "prod", `{"team":"platform"}`},
	} {
		resp, err := env.APIStatusHeaders(ctx, http.MethodPost, "/api/stacks/"+e2e.Namespace+"/"+body.project,
			`{"stackName":"`+body.stack+`","tags":`+body.tags+`}`)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("create stack %s/%s = %d, %v (%s)", body.project, body.stack, resp.Status, err, resp.Body)
		}
	}
}

// savedViewPerson makes a person with a viewer grant on the scenario
// namespace and returns a personal token.
func savedViewPerson(ctx context.Context, t *testing.T, env *e2e.Env, idpSubject string) string {
	t.Helper()

	var person e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject(idpSubject) + `","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/grants",
		`{"subject":"principal:` + person.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)

	return env.PersonalToken(ctx, t, person.ID, "views").Secret
}

// e2eViewRequest is one request of a step.
type e2eViewRequest struct {
	token  string
	method string
	path   string
	body   string
	want   int
}

// call sends one request and checks the status.
func (s *viewScenario) call(ctx context.Context, t *testing.T, r e2eViewRequest) string {
	t.Helper()

	status, got, err := s.env.ServerAPIStatusAs(ctx, r.token, r.method, r.path, r.body)
	if err != nil {
		t.Fatalf("%s %s: %v", r.method, r.path, err)
	}

	if status != r.want {
		t.Fatalf("%s %s = %d (%s), want %d", r.method, r.path, status, got, r.want)
	}

	return got
}

func (s *viewScenario) create(ctx context.Context, t *testing.T) e2eView {
	t.Helper()

	body := `{"name":"Payments","query":{"labels":[{"key":"team","value":"payments"}],"references":[]}}`
	post := func(payload string, want int) string {
		return s.call(ctx, t, e2eViewRequest{s.ana, http.MethodPost, s.path, payload, want})
	}

	var created e2eView
	if err := json.Unmarshal([]byte(post(body, http.StatusCreated)), &created); err != nil {
		t.Fatalf("decode the created view: %v", err)
	}

	if !strings.HasPrefix(created.ID, "view-") || created.Name != "Payments" || created.Namespace != e2e.Namespace ||
		len(created.Query.Labels) != 1 || created.CreatedAt == "" {
		t.Fatalf("created view = %+v", created)
	}

	// The same name for the same person is a conflict, and a bad body is a 400.
	post(body, http.StatusConflict)
	post(`{"name":""}`, http.StatusBadRequest)
	post(`{"name":"x","extra":1}`, http.StatusBadRequest)

	return created
}

func (s *viewScenario) reads(ctx context.Context, t *testing.T, created *e2eView) {
	t.Helper()

	var list e2eViewList

	listed := s.call(ctx, t, e2eViewRequest{s.ana, http.MethodGet, s.path, "", http.StatusOK})
	if err := json.Unmarshal([]byte(listed), &list); err != nil {
		t.Fatalf("decode the view list: %v", err)
	}

	if len(list.Views) != 1 || list.Views[0].ID != created.ID {
		t.Fatalf("view list = %+v, want the created view", list)
	}

	var got e2eView

	one := s.call(ctx, t, e2eViewRequest{s.ana, http.MethodGet, s.path + "/" + created.ID, "", http.StatusOK})
	if err := json.Unmarshal([]byte(one), &got); err != nil {
		t.Fatalf("decode the view: %v", err)
	}

	if got.ID != created.ID || got.Name != created.Name {
		t.Fatalf("view = %+v, want %+v", got, created)
	}
}

// units opens the view and compares its page with the page of the same query in
// the unit list.
func (s *viewScenario) units(ctx context.Context, t *testing.T, created *e2eView) {
	t.Helper()

	opened := s.call(
		ctx,
		t,
		e2eViewRequest{s.ana, http.MethodGet, s.path + "/" + created.ID + "/units", "", http.StatusOK},
	)
	listed := s.call(ctx, t, e2eViewRequest{
		s.ana, http.MethodGet, "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units?label=team%3Dpayments", "",
		http.StatusOK,
	})

	if opened != listed {
		t.Fatalf("units of the view = %s, units of the same query = %s", opened, listed)
	}

	var page e2eUnitPage
	if err := json.Unmarshal([]byte(opened), &page); err != nil || len(page.Units) != 2 {
		t.Fatalf("units of the view = %s, want the two stacks with the tag team=payments (%v)", opened, err)
	}

	// A filter in the request would change the query of the view.
	s.call(ctx, t, e2eViewRequest{
		s.ana, http.MethodGet, s.path + "/" + created.ID + "/units?label=team%3Dplatform", "", http.StatusBadRequest,
	})
}

// refusals proves that the bootstrap token and another person read no view of
// the first person.
func (s *viewScenario) refusals(ctx context.Context, t *testing.T, created *e2eView) {
	t.Helper()

	// The bootstrap token is not a person: every view route answers 403.
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, s.path, ""},
		{http.MethodGet, s.path + "/" + created.ID, ""},
		{http.MethodGet, s.path + "/" + created.ID + "/units", ""},
		{http.MethodPost, s.path, `{"name":"Admin"}`},
		{http.MethodDelete, s.path + "/" + created.ID, ""},
	} {
		status, body, err := s.env.APIStatus(ctx, call.method, call.path, call.body)
		if err != nil || status != http.StatusForbidden || !strings.Contains(body, "sign in as a person") {
			t.Fatalf("%s %s as the bootstrap token = %d, %v (%s), want 403 and the person message",
				call.method, call.path, status, err, body)
		}
	}

	// Another person: the same 404 as an unknown view, and an empty list.
	for _, suffix := range []string{"", "/units"} {
		s.call(
			ctx,
			t,
			e2eViewRequest{s.bob, http.MethodGet, s.path + "/" + created.ID + suffix, "", http.StatusNotFound},
		)
	}

	s.call(ctx, t, e2eViewRequest{s.bob, http.MethodDelete, s.path + "/" + created.ID, "", http.StatusNotFound})

	var list e2eViewList

	listed := s.call(ctx, t, e2eViewRequest{s.bob, http.MethodGet, s.path, "", http.StatusOK})
	if err := json.Unmarshal([]byte(listed), &list); err != nil || len(list.Views) != 0 {
		t.Fatalf("view list of another person = %+v, %v; want none", list, err)
	}
}

func (s *viewScenario) delete(ctx context.Context, t *testing.T, created *e2eView) {
	t.Helper()

	target := s.path + "/" + created.ID

	s.call(ctx, t, e2eViewRequest{s.ana, http.MethodDelete, target, "", http.StatusNoContent})
	s.call(ctx, t, e2eViewRequest{s.ana, http.MethodGet, target, "", http.StatusNotFound})
	s.call(ctx, t, e2eViewRequest{s.ana, http.MethodDelete, target, "", http.StatusNotFound})
	s.call(ctx, t, e2eViewRequest{s.ana, http.MethodGet, target + "/units", "", http.StatusNotFound})
}
