//go:build e2e

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// collectionAgent is the User-Agent of the collection requests. The route
// coverage check counts requests that do not carry the harness agent.
const collectionAgent = "opendustpan-e2e-collections"

// collectionsBase is the collection list of the e2e namespace.
const collectionsBase = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/collections"

// collectionUnitPath returns the unit move route for the e2e workspace of
// OpenTofu. The unit reference names the tool, the namespace and the workspace.
func collectionUnitPath() string {
	return "/api/inventory/unit-collection?unit=" +
		url.QueryEscape("opentofu:"+e2e.Namespace+"/"+e2e.Workspace)
}

// errAnswerLacksFragment reports a collection answer without the fragment that
// the step checks for.
var errAnswerLacksFragment = errors.New("answer lacks the expected fragment")

// collectionCall is one collection request of a scenario step.
type collectionCall struct {
	check  func(string) error
	name   string
	method string
	path   string
	body   string
	want   int
}

// collectionBody returns the JSON body that names one collection.
func collectionBody(t *testing.T, name string) string {
	t.Helper()

	out, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		t.Fatalf("encode collection body: %v", err)
	}

	return string(out)
}

// moveBody returns the JSON body that names the target of a unit move.
func moveBody(t *testing.T, name string) string {
	t.Helper()

	out, err := json.Marshal(map[string]string{"collection": name})
	if err != nil {
		t.Fatalf("encode move body: %v", err)
	}

	return string(out)
}

// collectionStep runs one collection request as principal A.
func collectionStep(t *testing.T, env *e2e.Env, c collectionCall) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: collectionAgent,
		Name:      c.name,
		Method:    c.method,
		Path:      c.path,
		Body:      c.body,
		Want:      c.want,
		Check:     c.check,
		Scheme:    "",
	})
}

// collectionHolds checks that the body names the fragment.
func collectionHolds(fragment string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, fragment) {
			return fmt.Errorf("%w: %q in %s", errAnswerLacksFragment, fragment, body)
		}

		return nil
	}
}

// collectionsScenario drives the collection routes of the e2e namespace over
// the API as the admin principal (ADR 51, ADR 61 decision 5). It makes two
// collections, moves the e2e workspace between them, and checks the refusal of
// a delete while a unit is in the collection.
func collectionsScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	calls := []collectionCall{
		{
			name: "make a collection", method: http.MethodPost, path: collectionsBase,
			body: collectionBody(t, "payments"), want: http.StatusCreated,
			check: collectionHolds(`"name":"payments"`),
		},
		{
			name: "a name that differs only in case is taken", method: http.MethodPost, path: collectionsBase,
			body: collectionBody(t, "PAYMENTS"), want: http.StatusConflict, check: nil,
		},
		{
			name: "a name with a dot is refused", method: http.MethodPost, path: collectionsBase,
			body: collectionBody(t, "pay.ments"), want: http.StatusBadRequest, check: nil,
		},
		{
			name: "make the second collection", method: http.MethodPost, path: collectionsBase,
			body: collectionBody(t, "platform"), want: http.StatusCreated, check: nil,
		},
		{
			name: "list the collections", method: http.MethodGet, path: collectionsBase,
			body: "", want: http.StatusOK, check: collectionHolds(`"name":"platform"`),
		},
		{
			name: "read one collection", method: http.MethodGet, path: collectionsBase + "/payments",
			body: "", want: http.StatusOK, check: collectionHolds(`"name":"payments"`),
		},
		{
			name: "a missing collection is not found", method: http.MethodGet, path: collectionsBase + "/absent",
			body: "", want: http.StatusNotFound, check: nil,
		},
		{
			name: "move the workspace into payments", method: http.MethodPut, path: collectionUnitPath(),
			body: moveBody(t, "payments"), want: http.StatusOK,
			check: collectionHolds(`"previousCollection":"` + e2e.CatchAllCollection + `"`),
		},
		{
			name: "a collection with a unit cannot be deleted", method: http.MethodDelete,
			path: collectionsBase + "/payments", body: "", want: http.StatusConflict, check: nil,
		},
		{
			name: "move the workspace to platform", method: http.MethodPut, path: collectionUnitPath(),
			body: moveBody(t, "platform"), want: http.StatusOK,
			check: collectionHolds(`"previousCollection":"payments"`),
		},
		{
			name: "delete the emptied collection", method: http.MethodDelete,
			path: collectionsBase + "/payments", body: "", want: http.StatusNoContent, check: nil,
		},
		{
			name: "the inventory shows the new collection", method: http.MethodGet,
			path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units", body: "", want: http.StatusOK,
			check: collectionHolds(`"collection":"platform"`),
		},
	}

	for _, c := range calls {
		collectionStep(t, env, c)
	}
}
