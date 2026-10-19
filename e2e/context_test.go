//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// contextAgent is the User-Agent of the context requests. The route coverage
// check counts requests that do not carry the harness agent.
const contextAgent = "opendustpan-e2e-context"

// contextNamespace is the path of the e2e namespace in the tool-neutral API.
const contextNamespace = "/api/dustpan/v1/namespaces/" + e2e.Namespace

// contextBase is the context route of the e2e namespace.
const contextBase = contextNamespace + "/context"

// contextCall is one context request of a scenario step.
type contextCall struct {
	check  func(string) error
	name   string
	method string
	path   string
	body   string
	want   int
}

// contextStep runs one context request as principal A.
func contextStep(t *testing.T, env *e2e.Env, c contextCall) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: contextAgent,
		Name:      c.name,
		Method:    c.method,
		Path:      c.path,
		Body:      c.body,
		Want:      c.want,
		Check:     c.check,
		Scheme:    "",
	})
}

// contextFromWire is the provenance of a resolved value.
type contextFromWire struct {
	Object string `json:"object"`
}

// contextOwnersWire is the resolved owners of a context document.
type contextOwnersWire struct {
	From  contextFromWire `json:"from"`
	Value []string        `json:"value"`
}

// contextResolvedWire is the resolved context of a context document.
type contextResolvedWire struct {
	Owners contextOwnersWire `json:"owners"`
}

// contextDataWire is the data of the context section.
type contextDataWire struct {
	Resolved contextResolvedWire `json:"resolved"`
}

// contextSectionWire is the context section of a document.
type contextSectionWire struct {
	Data contextDataWire `json:"data"`
}

// contextSectionsWire holds the sections that the scenario reads.
type contextSectionsWire struct {
	Context contextSectionWire `json:"context"`
}

// contextDocWire is the part of a context document that the scenario reads.
type contextDocWire struct {
	Sections contextSectionsWire `json:"sections"`
}

// contextOwners checks that the context document of a unit resolves the
// owners to owners, from the object of the kind from.
func contextOwners(owners []string, from string) func(string) error {
	return func(body string) error {
		var doc contextDocWire
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			return fmt.Errorf("decode %s: %w", body, err)
		}

		got := doc.Sections.Context.Data.Resolved.Owners
		if !slices.Equal(got.Value, owners) || (len(owners) > 0 && got.From.Object != from) {
			return fmt.Errorf("%w: owners = %v from %q, want %v from %q in %s",
				errAnswerLacksFragment, got.Value, got.From.Object, owners, from, body)
		}

		return nil
	}
}

// contextScenario drives the context routes of the e2e namespace over the
// API as the platform engineer, principal A. It sets the owners and the
// on-call text on the namespace, and checks that the unit made by the tool
// reads them back from its own context route. Then the unit sets its own
// owners, and a clear brings the inherited owners back. tool is the stored
// code of the tool and unitID the stable ID of its unit.
func contextScenario(t *testing.T, env *e2e.Env, tool, unitID string) {
	t.Helper()

	unit := contextNamespace + "/units/" + unitID + "/context"
	inherited := []string{"group:platform"}

	calls := []contextCall{
		{
			name: "set owners and on-call on the namespace", method: http.MethodPatch, path: contextBase,
			body: `{"owners":["group:platform"],"onCall":"pager:platform"}`, want: http.StatusOK,
			check: collectionHolds(`"onCall":"pager:platform"`),
		},
		{
			name: "read the namespace context", method: http.MethodGet, path: contextBase, body: "",
			want: http.StatusOK, check: contextOwners(inherited, "namespace"),
		},
		{
			name: "the " + tool + " unit inherits the owners of the namespace", method: http.MethodGet, path: unit,
			body: "", want: http.StatusOK, check: contextOwners(inherited, "namespace"),
		},
		{
			name: "the unit sets its own owners", method: http.MethodPatch, path: unit,
			body: `{"owners":["group:team"]}`, want: http.StatusOK, check: nil,
		},
		{
			name: "the owners of the unit win", method: http.MethodGet, path: unit, body: "",
			want: http.StatusOK, check: contextOwners([]string{"group:team"}, "unit"),
		},
		{
			name: "clear the context of the unit", method: http.MethodDelete, path: unit, body: "",
			want: http.StatusNoContent, check: nil,
		},
		{
			name: "the inherited owners come back", method: http.MethodGet, path: unit, body: "",
			want: http.StatusOK, check: contextOwners(inherited, "namespace"),
		},
		{
			name: "a second clear finds nothing", method: http.MethodDelete, path: unit, body: "",
			want: http.StatusNotFound, check: nil,
		},
		{
			name:   "an unknown unit is not found",
			method: http.MethodGet,
			path:   contextNamespace + "/units/absent/context",
			body:   "",
			want:   http.StatusNotFound,
			check:  nil,
		},
		{
			name:   "an unknown namespace is not found",
			method: http.MethodGet,
			path:   "/api/dustpan/v1/namespaces/absent/context",
			body:   "",
			want:   http.StatusNotFound,
			check:  nil,
		},
		{
			name: "a body with an unknown field is refused", method: http.MethodPatch, path: unit,
			body: `{"owner":["group:team"]}`, want: http.StatusBadRequest, check: nil,
		},
		{
			name: "clear the context of the namespace", method: http.MethodDelete, path: contextBase, body: "",
			want: http.StatusNoContent, check: nil,
		},
		{
			name: "the unit has no owners left", method: http.MethodGet, path: unit, body: "",
			want: http.StatusOK, check: contextOwners(nil, ""),
		},
	}

	for _, c := range calls {
		contextStep(t, env, c)
	}

	profileScenario(t, env, tool)
}
