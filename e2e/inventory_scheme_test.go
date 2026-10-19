//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// inventorySchemeScenario reads the unit list of the e2e namespace with the
// console Authorization scheme ("token"), where the other steps send "Bearer".
// The inventory API accepts both schemes, so the list answers 200 (ADR 13).
func inventorySchemeScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: "opendustpan-e2e-console",
		Name:      "unit list with the console token scheme",
		Method:    http.MethodGet,
		Path:      "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units",
		Body:      "",
		Want:      http.StatusOK,
		Check:     nil,
		Scheme:    "token",
	})
}
