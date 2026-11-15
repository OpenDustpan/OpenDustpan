//go:build e2e

package e2e_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// errBlueprintRouteNotServed reports an answer of the route-not-found code: the
// server serves no blueprint route at the path.
var errBlueprintRouteNotServed = errors.New("the server serves no blueprint route")

// blueprintAgent is the User-Agent of the blueprint requests.
const blueprintAgent = "opendustpan-e2e-self-service-blueprints"

// blueprintListPath is the list route of the self-service blueprint API of the
// e2e namespace.
const blueprintListPath = "/api/dustpan/v1/self-service/blueprints/" + e2e.Namespace

// blueprintDraftPath is the draft route of the blueprint database of the e2e
// namespace, the blueprint that docs/guides/publish-a-blueprint.md reads.
const blueprintDraftPath = blueprintListPath + "/database/draft"

// TestSelfServiceBlueprintRoutes proves that the server of every store serves
// the blueprint routes of the self-service API: a namespace without a
// blueprint answers the list with 200 and an empty list, and a draft that does
// not exist answers 404 with the blueprint code, not the route-not-found code.
// The steps send the bootstrap token of the harness. No CLI calls these routes.
func TestSelfServiceBlueprintRoutes(t *testing.T) {
	t.Parallel()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			skipWithoutStore(t, db)
			runBlueprintRoutesOnStore(t, db)
		})
	}
}

// runBlueprintRoutesOnStore runs the blueprint steps on one store.
func runBlueprintRoutesOnStore(t *testing.T, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, e2e.NoCLI(), store)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: blueprintAgent,
		Name:      "list the blueprints of a namespace with no blueprint",
		Method:    http.MethodGet, Path: blueprintListPath, Body: "",
		Want: http.StatusOK, Scheme: "token", Check: blueprintAnswer(`{"versions":[]}`),
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: blueprintAgent,
		Name:      "read the draft of a blueprint that does not exist",
		Method:    http.MethodGet, Path: blueprintDraftPath, Body: "",
		Want: http.StatusNotFound, Scheme: "token", Check: blueprintAnswer("self-service.blueprint-not-found"),
	})
}

// blueprintAnswer returns a check that body holds fragment and not the
// route-not-found code. The server answers that code when it serves no route at
// the path, so a step cannot pass on a 404 alone.
func blueprintAnswer(fragment string) func(string) error {
	return func(body string) error {
		if strings.Contains(body, "server.route-not-found") {
			return fmt.Errorf("%w: %s", errBlueprintRouteNotServed, body)
		}

		if !strings.Contains(body, fragment) {
			return fmt.Errorf("%w: %q not in %s", errAnswerLacksFragment, fragment, body)
		}

		return nil
	}
}
