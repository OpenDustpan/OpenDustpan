package dustpan_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/preferences"
)

// TestEveryOperationMarksItsBodies proves that each operation of the server
// names the Go type of its request and of its answer, or says that it has
// none. A route with no mark fails here, and the server refuses to start with
// it.
func TestEveryOperationMarksItsBodies(t *testing.T) {
	t.Parallel()

	ops := newEveryModuleServer(t).Operations()
	if len(ops) == 0 {
		t.Fatal("the server lists no operation")
	}

	for _, op := range ops {
		if err := op.ValidateBodies(); err != nil {
			t.Errorf("%s %s: %v", op.Method, op.Path, err)
		}

		for _, action := range op.Actions {
			if op.ActionObjects[action] == "" {
				t.Errorf("%s %s: the action %s names no object kind", op.Method, op.Path, action)
			}
		}
	}
}

// descriptionSchemas is the part of the description that the body tests read.
type descriptionSchemas struct {
	Paths      map[string]map[string]descriptionOperation `json:"paths"`
	Components descriptionComponents                      `json:"components"`
}

type descriptionComponents struct {
	Schemas map[string]json.RawMessage `json:"schemas"`
}

//nolint:tagliatelle // OpenAPI fixes the names of these members.
type descriptionOperation struct {
	OperationID string                     `json:"operationId"`
	RequestBody *descriptionBody           `json:"requestBody"`
	Responses   map[string]descriptionBody `json:"responses"`
}

// descriptionBody is a request body or an answer: its content by media type.
type descriptionBody struct {
	Content map[string]descriptionMedia `json:"content"`
}

type descriptionMedia struct {
	Schema json.RawMessage `json:"schema"`
}

func readDescription(t *testing.T) descriptionSchemas {
	t.Helper()

	var doc descriptionSchemas
	if err := json.Unmarshal(apidesc.Document(), &doc); err != nil {
		t.Fatalf("the checked-in description is not JSON: %v", err)
	}

	return doc
}

// refsOf returns every reference to a schema in the JSON value.
func refsOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()

	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("not JSON: %v", err)
	}

	var out []string

	collectRefs(tree, &out)

	return out
}

func collectRefs(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if ref, ok := child.(string); ok && k == "$ref" {
				*out = append(*out, ref)

				continue
			}

			collectRefs(child, out)
		}
	case []any:
		for _, child := range x {
			collectRefs(child, out)
		}
	default:
		// A string, a number, a boolean or null holds no reference.
	}
}

const schemaRef = "#/components/schemas/"

// operationRefs returns every reference to a schema in the operations.
func operationRefs(t *testing.T, doc descriptionSchemas) []string {
	t.Helper()

	var out []string

	for _, item := range doc.Paths {
		for _, op := range item {
			raw, err := json.Marshal(op)
			if err != nil {
				t.Fatal(err)
			}

			out = append(out, refsOf(t, raw)...)
		}
	}

	return out
}

// reachableSchemas returns the names of the schemas that an operation uses,
// directly or through another schema. It reports a reference with no schema.
func reachableSchemas(t *testing.T, doc descriptionSchemas) map[string]bool {
	t.Helper()

	reached := map[string]bool{"ErrorResponse": true}

	var queue []string

	visit := func(ref string) {
		name, ok := strings.CutPrefix(ref, schemaRef)
		if !ok {
			return
		}

		if _, known := doc.Components.Schemas[name]; !known {
			t.Errorf("the reference %s has no schema", ref)

			return
		}

		if !reached[name] {
			reached[name] = true

			queue = append(queue, name)
		}
	}

	for _, ref := range operationRefs(t, doc) {
		visit(ref)
	}

	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]

		for _, ref := range refsOf(t, doc.Components.Schemas[next]) {
			visit(ref)
		}
	}

	return reached
}

// TestDescriptionSchemasAreConsistent proves that each reference of the
// checked-in description names a schema, that each schema except the error
// shape is reachable from an operation, and that each name follows the rule
// for schema names.
func TestDescriptionSchemasAreConsistent(t *testing.T) {
	t.Parallel()

	doc := readDescription(t)
	name := regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	reached := reachableSchemas(t, doc)

	for schema := range doc.Components.Schemas {
		if !name.MatchString(schema) {
			t.Errorf("the schema name %q is not UpperCamelCase", schema)
		}

		if !reached[schema] {
			t.Errorf("the schema %s is not reachable from any operation", schema)
		}
	}
}

// TestDescriptionBodiesMatchTheMarks proves that each operation holds a body
// schema exactly when the route names a type: a typed answer has content with
// a reference, a NoBody answer has none, and a typed request has a request body
// of the media type of its method.
func TestDescriptionBodiesMatchTheMarks(t *testing.T) {
	t.Parallel()

	doc := readDescription(t)

	for _, op := range newEveryModuleServer(t).Operations() {
		described := doc.Paths[op.Path][strings.ToLower(op.Method)]
		if described.OperationID != op.ID {
			t.Errorf("%s %s: the description holds %q, want %q", op.Method, op.Path, described.OperationID, op.ID)

			continue
		}

		if op.Bodies == nil {
			t.Errorf("%s %s: the operation names no bodies", op.Method, op.Path)

			continue
		}

		checkOperationBodies(t, op, described)
	}
}

// checkOperationBodies compares the bodies that a route names with the ones in
// the description.
func checkOperationBodies(t *testing.T, op apidesc.Operation, described descriptionOperation) {
	t.Helper()

	where := op.Method + " " + op.Path

	var keys []string
	for key := range described.Responses {
		if key != "default" {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)

	want := make([]string, 0, len(op.Statuses))
	for _, status := range op.Statuses {
		want = append(want, strconv.Itoa(status))
	}

	if !slices.Equal(keys, want) {
		t.Errorf("%s: the description has the success keys %v, want %v", where, keys, want)

		return
	}

	response := described.Responses[want[0]].Content[mediaJSON]
	if hasSchema := len(response.Schema) > 0; hasSchema == op.Bodies.Response.IsNone() {
		t.Errorf("%s: answer schema present %v, NoBody %v", where, hasSchema, op.Bodies.Response.IsNone())
	}

	hasRequest := described.RequestBody != nil
	if hasRequest == op.Bodies.Request.IsNone() {
		t.Errorf("%s: request body present %v, NoBody %v", where, hasRequest, op.Bodies.Request.IsNone())

		return
	}

	if !hasRequest {
		return
	}

	media := mediaJSON
	if op.Method == http.MethodPatch {
		media = "application/merge-patch+json"
	}

	if _, ok := described.RequestBody.Content[media]; !ok {
		t.Errorf("%s: the request body lacks the media type %s", where, media)
	}
}

const mediaJSON = "application/json"

// camelCase is the rule for the name of a member: an acronym is one word, so
// unitId and never unitID.
var camelCase = regexp.MustCompile(`^[a-z][a-z0-9]*([A-Z][a-z0-9]+)*$`)

// TestFieldNamesAreCamelCaseExceptTheKnownOnes proves that each member name of
// each schema is camelCase, except the names on the list of the legacy ones. The
// list is the report of the fields that break the rule: each name leaves it
// when its field is renamed, and the test fails when a name stays on the list
// after the last schema that used it is gone.
func TestFieldNamesAreCamelCaseExceptTheKnownOnes(t *testing.T) {
	t.Parallel()

	doc := readDescription(t)
	legacy := apidesc.LegacyFieldNames()
	used := map[string]bool{}

	for schema, raw := range doc.Components.Schemas {
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}

		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("schema %s: %v", schema, err)
		}

		for field := range s.Properties {
			if camelCase.MatchString(field) {
				continue
			}

			if _, known := legacy[field]; !known {
				t.Errorf("schema %s: the member %q is not camelCase", schema, field)

				continue
			}

			used[field] = true
		}
	}

	for _, field := range slices.Sorted(maps.Keys(legacy)) {
		if !used[field] {
			t.Errorf("the legacy name %q is on the list and no schema uses it", field)
		}
	}
}

// schemaProperties returns the names of the members of a schema of the
// description.
func schemaProperties(t *testing.T, doc descriptionSchemas, name string) []string {
	t.Helper()

	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}

	raw, ok := doc.Components.Schemas[name]
	if !ok {
		t.Fatalf("the description has no schema %s", name)
	}

	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("schema %s: %v", name, err)
	}

	return slices.Sorted(maps.Keys(s.Properties))
}

// TestUpdatePreferencesRequestIsWhatTheParserReads proves that the schema of
// the preferences patch lists the members that preferences.ParsePatch accepts
// and no other, and that it lists every member of the document that a person
// can change. The handler reads the body with the parser, so no Go type of the
// request exists; this test keeps the schema from drifting.
func TestUpdatePreferencesRequestIsWhatTheParserReads(t *testing.T) {
	t.Parallel()

	doc := readDescription(t)
	request := schemaProperties(t, doc, "UpdatePreferencesRequest")

	for _, member := range request {
		body := `{"` + member + `":null}`
		if _, err := preferences.ParsePatch([]byte(body)); err != nil {
			t.Errorf("ParsePatch(%s) = %v, but the schema lists %s", body, err, member)
		}
	}

	// Every member of the answer is a member of the patch, except the ones the
	// parser refuses.
	refused := []string{preferences.FieldUpdatedAt}

	for _, member := range schemaProperties(t, doc, "Preferences") {
		if slices.Contains(refused, member) {
			if _, err := preferences.ParsePatch([]byte(`{"` + member + `":null}`)); err == nil {
				t.Errorf("ParsePatch accepts %s, but the schema leaves it out on purpose", member)
			}

			continue
		}

		if !slices.Contains(request, member) {
			t.Errorf("the answer has the member %s and the patch schema does not", member)
		}
	}

	schemes := schemaProperties(t, doc, "TerminalSchemesPatch")
	for _, member := range schemes {
		body := `{"terminalSchemes":{"` + member + `":null}}`
		if _, err := preferences.ParsePatch([]byte(body)); err != nil {
			t.Errorf("ParsePatch(%s) = %v, but the schema lists %s", body, err, member)
		}
	}

	if want := schemaProperties(t, doc, "TerminalSchemes"); !slices.Equal(schemes, want) {
		t.Errorf("TerminalSchemesPatch lists %v, TerminalSchemes lists %v", schemes, want)
	}
}
