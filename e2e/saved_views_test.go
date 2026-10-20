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

// e2eView decodes a saved view.
type e2eView struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Query     struct {
		Labels []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"labels"`
		References []string `json:"references"`
	} `json:"query"`
	CreatedAt string `json:"createdAt"`
}

// e2eViewList decodes the list of saved views.
type e2eViewList struct {
	Views []e2eView `json:"views"`
}

// e2eUnitPage decodes one page of the unit list.
type e2eUnitPage struct {
	Units []struct {
		Reference string `json:"reference"`
	} `json:"units"`
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

func runSavedViewsOnStore(t *testing.T, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, e2e.NoCLI(), store)

	checkReady(ctx, t, env)

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

	ana, anaToken := savedViewPerson(ctx, t, env, "ana@example.com")
	_, bobToken := savedViewPerson(ctx, t, env, "bob@example.com")
	_ = ana

	path := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/views"
	created := checkViewCreate(ctx, t, env, anaToken, path)

	checkViewReads(ctx, t, env, anaToken, path, created)
	checkViewUnits(ctx, t, env, anaToken, path, created)
	checkViewRefusals(ctx, t, env, bobToken, path, created)
	checkViewDelete(ctx, t, env, anaToken, path, created)
}

// savedViewPerson makes a person with a viewer grant on the scenario
// namespace and returns the person and a personal token.
func savedViewPerson(ctx context.Context, t *testing.T, env *e2e.Env, idpSubject string) (e2ePrincipal, string) {
	t.Helper()

	var person e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"` + e2e.SignInSubject(idpSubject) + `","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/grants",
		`{"subject":"principal:` + person.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)

	return person, env.PersonalToken(ctx, t, person.ID, "views").Secret
}

// viewCall sends one request with token and checks the status.
func viewCall(ctx context.Context, t *testing.T, env *e2e.Env, token, method, path, body string, want int) string {
	t.Helper()

	status, got, err := env.ServerAPIStatusAs(ctx, token, method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	if status != want {
		t.Fatalf("%s %s = %d (%s), want %d", method, path, status, got, want)
	}

	return got
}

func checkViewCreate(ctx context.Context, t *testing.T, env *e2e.Env, token, path string) e2eView {
	t.Helper()

	body := `{"name":"Payments","query":{"labels":[{"key":"team","value":"payments"}],"references":[]}}`

	var created e2eView
	if err := json.Unmarshal([]byte(viewCall(ctx, t, env, token, http.MethodPost, path, body, http.StatusCreated)),
		&created); err != nil {
		t.Fatalf("decode the created view: %v", err)
	}

	if !strings.HasPrefix(created.ID, "view-") || created.Name != "Payments" || created.Namespace != e2e.Namespace ||
		len(created.Query.Labels) != 1 || created.CreatedAt == "" {
		t.Fatalf("created view = %+v", created)
	}

	// The same name for the same person is a conflict, and a bad body is a 400.
	viewCall(ctx, t, env, token, http.MethodPost, path, body, http.StatusConflict)
	viewCall(ctx, t, env, token, http.MethodPost, path, `{"name":""}`, http.StatusBadRequest)
	viewCall(ctx, t, env, token, http.MethodPost, path, `{"name":"x","extra":1}`, http.StatusBadRequest)

	return created
}

func checkViewReads(ctx context.Context, t *testing.T, env *e2e.Env, token, path string, created e2eView) {
	t.Helper()

	var list e2eViewList
	if err := json.Unmarshal([]byte(viewCall(ctx, t, env, token, http.MethodGet, path, "", http.StatusOK)),
		&list); err != nil {
		t.Fatalf("decode the view list: %v", err)
	}

	if len(list.Views) != 1 || list.Views[0].ID != created.ID {
		t.Fatalf("view list = %+v, want the created view", list)
	}

	var got e2eView
	if err := json.Unmarshal([]byte(viewCall(ctx, t, env, token, http.MethodGet, path+"/"+created.ID, "",
		http.StatusOK)), &got); err != nil {
		t.Fatalf("decode the view: %v", err)
	}

	if got.ID != created.ID || got.Name != created.Name {
		t.Fatalf("view = %+v, want %+v", got, created)
	}
}

// checkViewUnits opens the view and compares its page with the page of the same
// query in the unit list.
func checkViewUnits(ctx context.Context, t *testing.T, env *e2e.Env, token, path string, created e2eView) {
	t.Helper()

	opened := viewCall(ctx, t, env, token, http.MethodGet, path+"/"+created.ID+"/units", "", http.StatusOK)
	listed := viewCall(ctx, t, env, token, http.MethodGet,
		"/api/dustpan/v1/namespaces/"+e2e.Namespace+"/units?label=team%3Dpayments", "", http.StatusOK)

	if opened != listed {
		t.Fatalf("units of the view = %s, units of the same query = %s", opened, listed)
	}

	var page e2eUnitPage
	if err := json.Unmarshal([]byte(opened), &page); err != nil || len(page.Units) != 2 {
		t.Fatalf("units of the view = %s, want the two stacks with the tag team=payments (%v)", opened, err)
	}

	// A filter in the request would change the query of the view.
	viewCall(ctx, t, env, token, http.MethodGet, path+"/"+created.ID+"/units?label=team%3Dplatform", "",
		http.StatusBadRequest)
}

// checkViewRefusals proves that the bootstrap token and another person read
// no view of the first person.
func checkViewRefusals(ctx context.Context, t *testing.T, env *e2e.Env, otherToken, path string, created e2eView) {
	t.Helper()

	// The bootstrap token is not a person: every view route answers 403.
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, path, ""},
		{http.MethodGet, path + "/" + created.ID, ""},
		{http.MethodGet, path + "/" + created.ID + "/units", ""},
		{http.MethodPost, path, `{"name":"Admin"}`},
		{http.MethodDelete, path + "/" + created.ID, ""},
	} {
		status, body, err := env.APIStatus(ctx, call.method, call.path, call.body)
		if err != nil || status != http.StatusForbidden || !strings.Contains(body, "sign in as a person") {
			t.Fatalf("%s %s as the bootstrap token = %d, %v (%s), want 403 and the person message",
				call.method, call.path, status, err, body)
		}
	}

	// Another person: the same 404 as an unknown view, and an empty list.
	viewCall(ctx, t, env, otherToken, http.MethodGet, path+"/"+created.ID, "", http.StatusNotFound)
	viewCall(ctx, t, env, otherToken, http.MethodGet, path+"/"+created.ID+"/units", "", http.StatusNotFound)
	viewCall(ctx, t, env, otherToken, http.MethodDelete, path+"/"+created.ID, "", http.StatusNotFound)

	var list e2eViewList
	if err := json.Unmarshal([]byte(viewCall(ctx, t, env, otherToken, http.MethodGet, path, "", http.StatusOK)),
		&list); err != nil || len(list.Views) != 0 {
		t.Fatalf("view list of another person = %+v, %v; want none", list, err)
	}
}

func checkViewDelete(ctx context.Context, t *testing.T, env *e2e.Env, token, path string, created e2eView) {
	t.Helper()

	viewCall(ctx, t, env, token, http.MethodDelete, path+"/"+created.ID, "", http.StatusNoContent)
	viewCall(ctx, t, env, token, http.MethodGet, path+"/"+created.ID, "", http.StatusNotFound)
	viewCall(ctx, t, env, token, http.MethodDelete, path+"/"+created.ID, "", http.StatusNotFound)
	viewCall(ctx, t, env, token, http.MethodGet, path+"/"+created.ID+"/units", "", http.StatusNotFound)
}
