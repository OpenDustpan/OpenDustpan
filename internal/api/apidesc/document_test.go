package apidesc_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// semver is the SemVer 2.0.0 core, which a version of the description must
// match.
var semver = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// pep440 is the release segment of PEP 440 in its canonical form.
var pep440 = regexp.MustCompile(`^(0|[1-9]\d*)(\.(0|[1-9]\d*))*$`)

// sampleOperations returns a list that uses every feature of the document.
func sampleOperations() []apidesc.Operation {
	list := op("GET", "/api/dustpan/v1/namespaces/{namespace}/things", "thing:read")
	list.ID, list.Tags = "listThings", []string{"things"}

	create := op("POST", "/api/dustpan/v1/namespaces/{namespace}/things", "thing:write")
	create.ID, create.Tags, create.IdempotencyKey, create.ETag = "createThing", []string{"things"}, true, true

	update := op("PATCH", "/api/dustpan/v1/namespaces/{namespace}/things/{thing_id}", "thing:write")
	update.ID, update.Tags, update.IfMatch, update.ETag = "updateThing", []string{"things", "extra"}, true, true

	remove := op("DELETE", "/api/dustpan/v1/namespaces/{namespace}/things/{thing_id}", "thing:delete")
	remove.ID, remove.Tags = "deleteThing", []string{"things"}

	open := publicOp("/api/dustpan/v1/openapi.json")
	open.ID, open.Tags = "getApiDescription", []string{"description"}

	return []apidesc.Operation{list, create, update, remove, open}
}

func TestBuildIsDeterministic(t *testing.T) {
	t.Parallel()

	want, err := apidesc.Build(sampleOperations())
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	if !bytes.HasSuffix(want, []byte("}\n")) {
		t.Error("the document must end with one line feed")
	}

	// Every rotation of the list, and the list reversed, must give the same bytes.
	orders := make([][]apidesc.Operation, 0, len(sampleOperations())+1)

	for shift := range sampleOperations() {
		ops := sampleOperations()
		orders = append(orders, append(ops[shift:], ops[:shift]...))
	}

	reversed := sampleOperations()
	slices.Reverse(reversed)

	for _, ops := range append(orders, reversed) {
		got, buildErr := apidesc.Build(ops)
		if buildErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("the order of the operations changed the bytes (err %v)", buildErr)
		}
	}
}

func TestBuildSortsKeys(t *testing.T) {
	t.Parallel()

	doc, err := apidesc.Build(sampleOperations())
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	var generic any
	if unmarshalErr := json.Unmarshal(doc, &generic); unmarshalErr != nil {
		t.Fatalf("the document is not JSON: %v", unmarshalErr)
	}

	// encoding/json writes the keys of a map in sorted order, so a document that
	// goes through a map and back must give the same bytes as Build.
	var again bytes.Buffer

	enc := json.NewEncoder(&again)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if encErr := enc.Encode(generic); encErr != nil {
		t.Fatalf("encode: %v", encErr)
	}

	if !bytes.Equal(again.Bytes(), doc) {
		t.Fatal("the keys of the document are not in sorted order")
	}
}

func TestBuildRoundTrips(t *testing.T) {
	t.Parallel()

	want := sampleOperations()

	doc, err := apidesc.Build(want)
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	got, err := apidesc.Read(doc)
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	drift, err := apidesc.Diff(want, got)
	if err != nil || !drift.Empty() {
		t.Fatalf("Diff() = %+v, %v; want the same operations", drift, err)
	}
}

func TestBuildRefusesWhatValidateRefuses(t *testing.T) {
	t.Parallel()

	twice := sampleOperations()
	twice[1].ID = twice[0].ID

	rest := op("GET", "/api/dustpan/v1/files/{path...}", "thing:read")
	rest.ID = "getFile"

	end := op("GET", "/api/dustpan/v1/{$}", "thing:read")
	end.ID = "getRoot"

	for name, ops := range map[string][]apidesc.Operation{
		"a repeated ID":   twice,
		"a rest wildcard": {rest},
		"an end anchor":   {end},
	} {
		doc, err := apidesc.Build(ops)
		if !errors.Is(err, apidesc.ErrInvalidOperation) || doc != nil {
			t.Errorf("%s: Build() = %d bytes, %v; want ErrInvalidOperation", name, len(doc), err)
		}
	}
}

func TestDocumentVersions(t *testing.T) {
	t.Parallel()

	if !semver.MatchString(apidesc.DocumentVersion) {
		t.Errorf("DocumentVersion %q is not SemVer", apidesc.DocumentVersion)
	}

	if !pep440.MatchString(apidesc.DocumentVersion) {
		t.Errorf("DocumentVersion %q is not PEP 440", apidesc.DocumentVersion)
	}

	if apidesc.OpenAPIVersion != "3.1.0" {
		t.Errorf("OpenAPIVersion = %q, want 3.1.0", apidesc.OpenAPIVersion)
	}
}

// decoded returns the checked-in document as a generic value.
func decoded(t *testing.T) map[string]any {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(apidesc.Document(), &doc); err != nil {
		t.Fatalf("the checked-in document is not JSON: %v", err)
	}

	return doc
}

// member returns the value at the path of keys, as the type T. It fails the
// test when a key is missing or the value has another type.
func member[T any](t *testing.T, v any, keys ...string) T {
	t.Helper()

	for _, key := range keys {
		obj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("the value before %q is not an object", key)
		}

		v, ok = obj[key]
		if !ok {
			t.Fatalf("no member %q", key)
		}
	}

	out, ok := v.(T)
	if !ok {
		t.Fatalf("the value at %v is %T", keys, v)
	}

	return out
}

// optional returns the member key of obj as the type T, or the zero value
// when the member is absent or has another type.
func optional[T any](obj map[string]any, key string) (T, bool) {
	out, ok := obj[key].(T)

	return out, ok
}

func TestCheckedInDocumentHasTheRequirementsOfTheGeneratorTrial(t *testing.T) {
	t.Parallel()

	doc := decoded(t)

	if got := member[string](t, doc, "openapi"); got != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", got)
	}

	if got := member[string](t, doc, "info", "version"); got != apidesc.DocumentVersion {
		t.Errorf("info.version = %v, want %s", got, apidesc.DocumentVersion)
	}

	schemas := member[map[string]any](t, doc, "components", "schemas")
	if _, ok := schemas["ErrorResponse"]; !ok {
		t.Error("components.schemas has no ErrorResponse")
	}

	if _, ok := schemas["Error"]; ok {
		t.Error("components.schemas has Error, which collides in TypeScript generators")
	}

	params := member[map[string]any](t, doc, "components", "parameters")
	for _, name := range []string{"IdempotencyKey", "IfMatch"} {
		if _, ok := params[name]; !ok {
			t.Errorf("components.parameters has no %s", name)
		}
	}

	if _, ok := member[map[string]any](t, doc, "components", "headers")["ETag"]; !ok {
		t.Error("components.headers has no ETag")
	}

	if len(member[map[string]any](t, doc, "components", "securitySchemes")) != 1 {
		t.Error("the document must hold one security scheme")
	}
}

func TestCheckedInOperationsHaveNamesTagsAndSecurity(t *testing.T) {
	t.Parallel()

	ids := map[string]bool{}

	for path, item := range member[map[string]any](t, decoded(t), "paths") {
		methods, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("path %s is not an object", path)
		}

		for method, raw := range methods {
			op, isObject := raw.(map[string]any)
			if !isObject {
				t.Fatalf("%s %s is not an object", method, path)
			}

			checkOperation(t, method+" "+path, op, ids)
		}
	}
}

// checkOperation checks one operation of the document for a name that no other
// operation has, for tags, and for a security entry that fits its actions.
func checkOperation(t *testing.T, where string, op map[string]any, ids map[string]bool) {
	t.Helper()

	id, _ := optional[string](op, "operationId")
	if id == "" || ids[id] {
		t.Errorf("%s has operationId %q, want one that no other operation has", where, id)
	}

	ids[id] = true

	if tags, _ := optional[[]any](op, "tags"); len(tags) == 0 {
		t.Errorf("%s has no tags", where)
	}

	actions, _ := optional[[]any](op, "x-dustpan-actions")
	security, hasSecurity := optional[[]any](op, "security")
	public := hasSecurity && len(security) == 0

	if len(actions) == 0 && !public {
		t.Errorf("%s needs no action, so it must have security: []", where)
	}

	if len(actions) > 0 && hasSecurity {
		t.Errorf("%s needs an action, so it must use the default security", where)
	}
}

// forbiddenKeywords returns the members of OpenAPI 3.1 and of its JSON Schema
// that have no form in 3.0.3. The document must not use them.
func forbiddenKeywords() []string {
	return []string{
		"const", "$defs", "prefixItems", "if", "then", "else", "unevaluatedProperties", "unevaluatedItems",
		"contains", "propertyNames", "dependentSchemas", "dependentRequired", "$schema", "$id", "webhooks",
		"jsonSchemaDialect", "identifier", "contentMediaType", "contentEncoding", "patternProperties",
	}
}

// walkState carries what a child of a schema needs to know about its parent.
type walkState struct {
	// inProperties is true for the properties map of a schema, whose keys are
	// names of properties and not keywords.
	inProperties bool
	// openEnum is true for an alternative of an anyOf that has an alternative
	// of the plain type string, which makes an enum open.
	openEnum bool
}

func TestCheckedInDocumentUsesTheSubsetOf30(t *testing.T) {
	t.Parallel()

	for _, problem := range subsetProblems("$", decoded(t), walkState{inProperties: false, openEnum: false}) {
		t.Error(problem)
	}
}

// TestSubsetCheckSeesTheForbiddenForms proves that the walk finds each form
// that the subset refuses, so that a pass of the test above means something.
func TestSubsetCheckSeesTheForbiddenForms(t *testing.T) {
	t.Parallel()

	bad := map[string]string{
		"multi type":           `{"type":["string","integer"]}`,
		"null type alone":      `{"type":["null"]}`,
		"closed enum":          `{"type":"string","enum":["a","b"]}`,
		"const":                `{"const":"a"}`,
		"oneOf of three":       `{"oneOf":[{"$ref":"#/a"},{"$ref":"#/b"},{"type":"null"}]}`,
		"oneOf with no null":   `{"oneOf":[{"$ref":"#/a"},{"$ref":"#/b"}]}`,
		"anyOf with no string": `{"anyOf":[{"enum":["a"]},{"type":"integer"}]}`,
	}

	good := map[string]string{
		"nullable":             `{"type":["string","null"]}`,
		"nullable ref":         `{"oneOf":[{"$ref":"#/a"},{"type":"null"}]}`,
		"open enum":            `{"anyOf":[{"type":"string","enum":["a","b"]},{"type":"string"}]}`,
		"property named const": `{"properties":{"const":{"type":"string"}}}`,
	}

	for name, text := range bad {
		if problems := problemsOf(t, text); len(problems) == 0 {
			t.Errorf("%s: no problem found", name)
		}
	}

	for name, text := range good {
		if problems := problemsOf(t, text); len(problems) != 0 {
			t.Errorf("%s: problems %v", name, problems)
		}
	}
}

// problemsOf returns the subset problems of one JSON text.
func problemsOf(t *testing.T, text string) []string {
	t.Helper()

	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%s: %v", text, err)
	}

	return subsetProblems("$", v, walkState{inProperties: false, openEnum: false})
}

// subsetProblems returns one text for each place where a value uses a form
// that has no counterpart in OpenAPI 3.0.3, or a closed enum.
func subsetProblems(where string, v any, state walkState) []string {
	switch value := v.(type) {
	case map[string]any:
		return objectProblems(where, value, state)
	case []any:
		var out []string

		for _, child := range value {
			out = append(out, subsetProblems(where+"[]", child, walkState{inProperties: false, openEnum: false})...)
		}

		return out
	default:
		return nil
	}
}

// objectProblems returns the problems of an object and of what it holds.
func objectProblems(where string, obj map[string]any, state walkState) []string {
	out := schemaProblems(where, obj, state)

	for key, child := range obj {
		if !state.inProperties && slices.Contains(forbiddenKeywords(), key) {
			out = append(out, where+" uses "+key+", which has no form in OpenAPI 3.0.3")
		}

		next := walkState{inProperties: key == "properties" && !state.inProperties, openEnum: false}

		list, isList := child.([]any)
		if !isList {
			out = append(out, subsetProblems(where+"."+key, child, next)...)

			continue
		}

		next.openEnum = key == "anyOf" && hasPlainString(list)

		for _, alt := range list {
			out = append(out, subsetProblems(where+"."+key+"[]", alt, next)...)
		}
	}

	return out
}

// hasPlainString reports whether a list of alternatives holds the type string
// with no enum.
func hasPlainString(alts []any) bool {
	for _, alt := range alts {
		m, ok := alt.(map[string]any)
		if !ok {
			continue
		}

		if _, hasEnum := m["enum"]; m["type"] == "string" && !hasEnum && len(m) == 1 {
			return true
		}
	}

	return false
}

// schemaProblems checks the keywords of a schema that decide the subset: the
// type, oneOf, anyOf and enum.
func schemaProblems(where string, obj map[string]any, state walkState) []string {
	var out []string

	if types, ok := obj["type"].([]any); ok && (len(types) != 2 || !slices.Contains(types, any("null"))) {
		out = append(out, where+` has a type list that is not [X, "null"]`)
	}

	if one, ok := obj["oneOf"].([]any); ok && !isRefOrNull(one) {
		out = append(out, where+" has a oneOf that is not [ref, null]")
	}

	if _, ok := obj["enum"]; ok && !state.openEnum {
		out = append(out, where+" has a closed enum: an enum that can grow is anyOf [enum, string]")
	}

	if anyOf, ok := obj["anyOf"].([]any); ok && !hasPlainString(anyOf) {
		out = append(out, where+" has an anyOf with no plain string: it is not an open enum")
	}

	return out
}

// isRefOrNull reports whether a oneOf list has two members and one of them is
// the null type.
func isRefOrNull(alts []any) bool {
	nulls := 0

	for _, alt := range alts {
		if m, ok := alt.(map[string]any); ok && m["type"] == "null" {
			nulls++
		}
	}

	return len(alts) == 2 && nulls == 1
}

func TestDocumentReturnsACopy(t *testing.T) {
	t.Parallel()

	first := apidesc.Document()
	if len(first) == 0 {
		t.Fatal("the checked-in document is empty")
	}

	first[0] = 'X'

	if apidesc.Document()[0] == 'X' {
		t.Fatal("Document() shares its bytes with the caller")
	}
}

func TestReadFailsClosed(t *testing.T) {
	t.Parallel()

	good, err := apidesc.Build(sampleOperations())
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	for name, doc := range map[string][]byte{
		"empty":                     nil,
		"not JSON":                  []byte("<xml/>"),
		"truncate":                  good[:len(good)/2],
		"other format version":      bytes.Replace(good, []byte(`"3.1.0"`), []byte(`"3.0.3"`), 1),
		"other description version": bytes.Replace(good, []byte(`"`+apidesc.DocumentVersion+`"`), []byte(`"9.9.9"`), 1),
	} {
		ops, readErr := apidesc.Read(doc)
		if !errors.Is(readErr, apidesc.ErrInvalidDocument) || ops != nil {
			t.Errorf("%s: Read() = %d operations, %v; want ErrInvalidDocument", name, len(ops), readErr)
		}
	}
}
