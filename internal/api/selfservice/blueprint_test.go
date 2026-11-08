package selfservice_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/selfservice"
)

// blueprintPair names one schema of the description and the Go type that it
// describes.
type blueprintPair struct {
	name string
	typ  reflect.Type
}

// blueprintPairs lists the schemas of the blueprint document.
func blueprintPairs() []blueprintPair {
	return []blueprintPair{
		{"SelfServiceBlueprint", reflect.TypeFor[selfservice.Blueprint]()},
		{"SelfServiceProgram", reflect.TypeFor[selfservice.Program]()},
		{"SelfServiceModule", reflect.TypeFor[selfservice.Module]()},
		{"SelfServiceComponent", reflect.TypeFor[selfservice.Component]()},
		{"SelfServicePlatform", reflect.TypeFor[selfservice.Platform]()},
		{"SelfServiceDelivery", reflect.TypeFor[selfservice.Delivery]()},
		{"SelfServiceGitDelivery", reflect.TypeFor[selfservice.GitDelivery]()},
		{"SelfServiceInput", reflect.TypeFor[selfservice.Input]()},
		{"SelfServiceChoice", reflect.TypeFor[selfservice.Choice]()},
		{"SelfServiceSource", reflect.TypeFor[selfservice.Source]()},
		{"SelfServiceOutputRef", reflect.TypeFor[selfservice.OutputRef]()},
		{"SelfServiceFilter", reflect.TypeFor[selfservice.Filter]()},
		{"SelfServiceOperand", reflect.TypeFor[selfservice.Operand]()},
		{"SelfServiceRule", reflect.TypeFor[selfservice.Rule]()},
		{"SelfServicePolicyRule", reflect.TypeFor[selfservice.PolicyRule]()},
		{"SelfServiceQuotaRule", reflect.TypeFor[selfservice.QuotaRule]()},
	}
}

// jsonFieldSet holds the JSON members of a struct and the ones that a document
// must hold.
type jsonFieldSet struct {
	members  []string
	required []string
}

// jsonFields returns the JSON member names of t, and the ones that a document
// must hold: a member without omitempty.
func jsonFields(t reflect.Type) jsonFieldSet {
	var names, required []string

	for field := range t.Fields() {
		if !field.IsExported() {
			continue
		}

		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}

		name, options, _ := strings.Cut(tag, ",")
		if name == "" {
			name = field.Name
		}

		names = append(names, name)
		if !strings.Contains(options, "omitempty") {
			required = append(required, name)
		}
	}

	slices.Sort(names)
	slices.Sort(required)

	return jsonFieldSet{members: names, required: required}
}

// describedProperty is the part of a property schema that the drift test
// compares.
type describedProperty struct {
	Type json.RawMessage `json:"type"`
}

// describedSchema is the part of a schema of the description that the drift
// test compares.
type describedSchema struct {
	Properties map[string]describedProperty `json:"properties"`
	Required   []string                     `json:"required"`
}

// describedDocument is the part of the description that the drift test reads.
type describedDocument struct {
	Components describedComponents `json:"components"`
}

// describedComponents holds the named schemas of the description.
type describedComponents struct {
	Schemas map[string]describedSchema `json:"schemas"`
}

// blueprintTypeList names a property of a blueprint schema whose type is a
// list that the declaration states, because the Go type alone does not give
// it.
type blueprintTypeList struct {
	schema   string
	property string
	types    []string
}

// blueprintTypeLists lists the type lists of the blueprint schemas.
func blueprintTypeLists() []blueprintTypeList {
	return []blueprintTypeList{
		{"SelfServiceOperand", "value", []string{"string", "number", "boolean"}},
	}
}

// TestBlueprintSchemaMatchesTheStructs compares the members and the required
// members of each schema of the committed description with the JSON tags of
// the Go type that it describes. A field that is added to a struct, or renamed
// in it, fails the test until the description is regenerated. It compares the
// type lists too, so that a declaration without one fails the test.
func TestBlueprintSchemaMatchesTheStructs(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../apidesc/openapi.json")
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	var doc describedDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode the description: %v", err)
	}

	for _, pair := range blueprintPairs() {
		t.Run(pair.name, func(t *testing.T) {
			t.Parallel()

			schema, ok := doc.Components.Schemas[pair.name]
			if !ok {
				t.Fatalf("the description has no schema %s", pair.name)
			}

			fields := jsonFields(pair.typ)
			names, required := fields.members, fields.required

			described := make([]string, 0, len(schema.Properties))
			for name := range schema.Properties {
				described = append(described, name)
			}

			slices.Sort(described)
			if !slices.Equal(names, described) {
				t.Errorf("members = %v, the description has %v", names, described)
			}

			describedRequired := slices.Clone(schema.Required)
			slices.Sort(describedRequired)

			if !slices.Equal(required, describedRequired) {
				t.Errorf("required = %v, the description has %v", required, describedRequired)
			}

			checkTypeLists(t, pair.name, schema)
		})
	}
}

// checkTypeLists compares each type list that the declaration states with the
// one in the description of the schema.
func checkTypeLists(t *testing.T, schemaName string, schema describedSchema) {
	t.Helper()

	for _, want := range blueprintTypeLists() {
		if want.schema != schemaName {
			continue
		}

		property, ok := schema.Properties[want.property]
		if !ok {
			t.Errorf("the description has no property %s.%s", want.schema, want.property)

			continue
		}

		var got []string
		if err := json.Unmarshal(property.Type, &got); err != nil {
			t.Errorf("%s.%s: type = %s, want %v", want.schema, want.property, property.Type, want.types)

			continue
		}

		if !slices.Equal(got, want.types) {
			t.Errorf("%s.%s: type = %v, want %v", want.schema, want.property, got, want.types)
		}
	}
}

// draftWithFilter returns a draft whose choice keeps the units whose output
// equals the JSON value.
func draftWithFilter(value string) string {
	return `{
	"schemaVersion": 1, "name": "blueprint-a", "title": "Network", "tool": "opentofu",
	"program": {"module": {"source": "registry.example.com/platform/network/aws", "version": "1.2.3"}},
	"unitName": "'networks/' + request.name",
	"sourcePrincipal": "svc-network",
	"inputs": [
		{"name": "network", "type": "string", "maps": "network", "choice": {"source": {
			"collection": "networks",
			"value": {"output": "network_id"},
			"where": [{"equals": {"value": ` + value + `}, "output": "tier"}]
		}}}
	],
	"delivery": {"homes": ["opendustpan"], "default": "opendustpan"}
}`
}

// TestOperandValueKinds sends each kind of fixed filter value to the server.
// The server accepts a string, a number and a boolean, and refuses an object,
// an array and null with the catalog code of an invalid blueprint.
func TestOperandValueKinds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		value  string
		status int
		code   string
	}{
		{name: "string", value: `"private"`, status: http.StatusOK, code: ""},
		{name: "number", value: `42`, status: http.StatusOK, code: ""},
		{name: "boolean", value: `true`, status: http.StatusOK, code: ""},
		{name: "object", value: `{}`, status: http.StatusBadRequest, code: errcode.SelfServiceBlueprintInvalid},
		{name: "array", value: `[1]`, status: http.StatusBadRequest, code: errcode.SelfServiceBlueprintInvalid},
		{name: "null", value: `null`, status: http.StatusBadRequest, code: errcode.SelfServiceBlueprintInvalid},
	}

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newEnv(t, allActions())
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, testDraftPath,
				strings.NewReader(draftWithFilter(tc.value)))
			r.Header.Set("Authorization", "Bearer token-a")
			r.Header.Set("If-Match", `"1"`)

			w := httptest.NewRecorder()
			env.handler.ServeHTTP(w, r)

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.status, w.Body.String())
			}

			if tc.code != "" {
				var result struct {
					ErrorCode string `json:"errorCode"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatalf("decode error response: %v", err)
				}

				if result.ErrorCode != tc.code {
					t.Errorf("errorCode = %q, want %q", result.ErrorCode, tc.code)
				}
			}
		})
	}
}

// TestDraftRefusesAClientExprVersion sends a draft that names an Expr version.
// The server sets that value at publish, so the draft is refused and nothing
// is stored.
func TestDraftRefusesAClientExprVersion(t *testing.T) {
	t.Parallel()

	draft := strings.Replace(draftJSON, `"schemaVersion": 1,`, `"schemaVersion": 1, "exprVersion": "v9.9.9",`, 1)

	env := newEnv(t, allActions())
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, testDraftPath, strings.NewReader(draft))
	r.Header.Set("Authorization", "Bearer token-a")
	r.Header.Set("If-Match", `"1"`)

	w := httptest.NewRecorder()
	env.handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	if env.store.callCount() != 0 {
		t.Error("a draft with a client Expr version reached the store")
	}
}

// TestInputTypeOutsideTheList sends an input type that the server does not
// accept. The open enum of the description lists the accepted values only.
func TestInputTypeOutsideTheList(t *testing.T) {
	t.Parallel()

	const draft = `{
	"schemaVersion": 1, "name": "blueprint-a", "title": "Database", "tool": "opentofu",
	"program": {"module": {"source": "registry.example.com/platform/database/aws", "version": "1.2.3"}},
	"unitName": "'databases/' + request.name",
	"inputs": [{"name": "size", "type": "bogus", "maps": "size"}],
	"delivery": {"homes": ["opendustpan"], "default": "opendustpan"}
}`

	env := newEnv(t, allActions())
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, testDraftPath, strings.NewReader(draft))
	r.Header.Set("Authorization", "Bearer token-a")
	r.Header.Set("If-Match", `"1"`)

	w := httptest.NewRecorder()
	env.handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var result struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if result.ErrorCode != errcode.SelfServiceBlueprintInvalid {
		t.Errorf("errorCode = %q, want %q", result.ErrorCode, errcode.SelfServiceBlueprintInvalid)
	}
}
