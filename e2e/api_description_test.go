//go:build e2e

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
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
	OpenAPI    string                                 `json:"openapi"`
	Info       descriptionInfo                        `json:"info"`
	Paths      map[string]map[string]descriptionRoute `json:"paths"`
	Components descriptionComponents                  `json:"components"`
}

// descriptionComponents is the part of the components object that the steps
// read.
type descriptionComponents struct {
	Schemas map[string]descriptionSchema `json:"schemas"`
}

// descriptionSchema is the part of a schema that the steps read. The type is
// raw, because a type that can be null is a list.
type descriptionSchema struct {
	Type       json.RawMessage              `json:"type"`
	Required   []string                     `json:"required"`
	Properties map[string]descriptionSchema `json:"properties"`
}

// descriptionInfo is the info object of the description.
type descriptionInfo struct {
	Version string `json:"version"`
}

// descriptionRoute is one operation of the description.
//
//nolint:tagliatelle // OpenAPI fixes the name of this member.
type descriptionRoute struct {
	OperationID string                         `json:"operationId"`
	Responses   map[string]descriptionResponse `json:"responses"`
}

// descriptionResponse is one answer of an operation.
type descriptionResponse struct {
	Content map[string]descriptionMedia `json:"content"`
}

// descriptionMedia is the content of an answer for one media type.
type descriptionMedia struct {
	Schema descriptionRef `json:"schema"`
}

// descriptionRef is a reference to a schema.
type descriptionRef struct {
	Ref string `json:"$ref"`
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

// checkDescription checks the format, the version, one known path and the
// schema of its answer.
func checkDescription(body string) error {
	var doc descriptionDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: not JSON: %w", errAPIDescription, err)
	}

	if doc.OpenAPI != "3.1.0" || doc.Info.Version == "" {
		return fmt.Errorf("%w: openapi %q, version %q", errAPIDescription, doc.OpenAPI, doc.Info.Version)
	}

	const known = "/api/dustpan/v1/capabilities"

	route := doc.Paths[known]["get"]
	if route.OperationID != "getCapabilities" {
		return fmt.Errorf("%w: GET %s has operationId %q", errAPIDescription, known, route.OperationID)
	}

	return checkCapabilitiesSchema(doc, route)
}

// checkCapabilitiesSchema checks that the success answer of the capabilities
// route refers to the schema Capabilities, and that this schema holds the
// member apiVersion as a required string.
func checkCapabilitiesSchema(doc descriptionDoc, route descriptionRoute) error {
	const (
		wantRef = "#/components/schemas/Capabilities"
		member  = "apiVersion"
	)

	if got := route.Responses["2XX"].Content["application/json"].Schema.Ref; got != wantRef {
		return fmt.Errorf(
			"%w: the answer of the capabilities route refers to %q, want %q",
			errAPIDescription,
			got,
			wantRef,
		)
	}

	schema, ok := doc.Components.Schemas["Capabilities"]
	if !ok {
		return fmt.Errorf("%w: the schema Capabilities is missing", errAPIDescription)
	}

	isObject := string(schema.Type) == `"object"`
	isString := string(schema.Properties[member].Type) == `"string"`

	if !isObject || !isString || !slices.Contains(schema.Required, member) {
		return fmt.Errorf("%w: the schema Capabilities has no required string %s", errAPIDescription, member)
	}

	return nil
}
