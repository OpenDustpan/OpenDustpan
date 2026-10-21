//go:build e2e

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// apiDescriptionAgent is the User-Agent of the description requests. The route
// coverage check counts requests that do not carry the harness agent.
const apiDescriptionAgent = "opendustpan-e2e-api-description"

// apiDescriptionPath is the route that serves the description of the API.
const apiDescriptionPath = "/api/dustpan/v1/openapi.json"

var errAPIDescription = errors.New("the description of the API is not the expected one")

// descriptionDoc is the part of the description that the steps read.
type descriptionDoc struct {
	OpenAPI string                                 `json:"openapi"`
	Info    descriptionInfo                        `json:"info"`
	Paths   map[string]map[string]descriptionRoute `json:"paths"`
}

// descriptionInfo is the info object of the description.
type descriptionInfo struct {
	Version string `json:"version"`
}

// descriptionRoute is one operation of the description.
//
//nolint:tagliatelle // OpenAPI fixes the name of this member.
type descriptionRoute struct {
	OperationID string `json:"operationId"`
}

// TestAPIDescription reads the description of the API on a server of each
// store. No CLI reads it: the generators of the SDKs and the clients do. The
// steps read it with no credential, with a wrong credential and with the
// token, and check one known path and that the three answers hold the same
// bytes.
func TestAPIDescription(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnv(t.Context(), t, e2e.NoCLI(), store)

			apiDescriptionScenario(t, env)
		})
	}
}

func apiDescriptionScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	var first string

	check := func(body string) error {
		if first == "" {
			first = body
		} else if body != first {
			return fmt.Errorf("%w: the bytes differ between two requests", errAPIDescription)
		}

		return checkDescription(body)
	}

	// An empty Authorization header sends no credential. The route is public.
	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: apiDescriptionAgent, Name: "read the description with no credential", Method: http.MethodGet,
		Path: apiDescriptionPath, Body: "", Want: http.StatusOK, Scheme: "token", Check: check,
	}, map[string]string{"Authorization": ""})

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: apiDescriptionAgent, Name: "read the description with a wrong credential", Method: http.MethodGet,
		Path: apiDescriptionPath, Body: "", Want: http.StatusOK, Scheme: "token", Check: check,
	}, map[string]string{"Authorization": "token not-a-token"})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: apiDescriptionAgent, Name: "read the description with the token", Method: http.MethodGet,
		Path: apiDescriptionPath, Body: "", Want: http.StatusOK, Scheme: "token", Check: check,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: apiDescriptionAgent, Name: "a write on the description route", Method: http.MethodPut,
		Path: apiDescriptionPath, Body: "", Want: http.StatusMethodNotAllowed, Scheme: "token", Check: nil,
	})
}

// checkDescription checks the format, the version and one known path of the
// description.
func checkDescription(body string) error {
	var doc descriptionDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: not JSON: %w", errAPIDescription, err)
	}

	if doc.OpenAPI != "3.1.0" || doc.Info.Version == "" {
		return fmt.Errorf("%w: openapi %q, version %q", errAPIDescription, doc.OpenAPI, doc.Info.Version)
	}

	const known = "/api/dustpan/v1/capabilities"

	if got := doc.Paths[known]["get"].OperationID; got != "getCapabilities" {
		return fmt.Errorf("%w: GET %s has operationId %q", errAPIDescription, known, got)
	}

	return nil
}
