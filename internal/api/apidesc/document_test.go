package apidesc_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
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
	list.ID, list.Tags, list.Statuses = "listThings", []string{"things"}, []int{http.StatusOK}

	create := op("POST", "/api/dustpan/v1/namespaces/{namespace}/things", "thing:write")
	create.ID, create.Tags, create.IdempotencyKey, create.ETag = "createThing", []string{"things"}, true, true
	create.Statuses = []int{http.StatusCreated}

	update := op("PATCH", "/api/dustpan/v1/namespaces/{namespace}/things/{thing_id}", "thing:write")
	update.ID, update.Tags, update.IfMatch, update.ETag = "updateThing", []string{"things", "extra"}, true, true
	update.Statuses = []int{http.StatusOK, http.StatusCreated}

	remove := op("DELETE", "/api/dustpan/v1/namespaces/{namespace}/things/{thing_id}", "thing:delete")
	remove.ID, remove.Tags, remove.IfMatchRequired = "deleteThing", []string{"things"}, true
	remove.Statuses = []int{http.StatusNoContent}

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

func TestBuildWritesTheExactSuccessKey(t *testing.T) {
	t.Parallel()

	doc, err := apidesc.Build(sampleOperations())
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	built := decodedBytes(t, doc)
	want := map[string][]string{
		"/api/dustpan/v1/namespaces/{namespace}/things get":               {"200"},
		"/api/dustpan/v1/namespaces/{namespace}/things post":              {"201"},
		"/api/dustpan/v1/namespaces/{namespace}/things/{thing_id} patch":  {"200", "201"},
		"/api/dustpan/v1/namespaces/{namespace}/things/{thing_id} delete": {"204"},
	}

	for key, wantKeys := range want {
		path, method, _ := strings.Cut(key, " ")

		responses := member[map[string]any](t, built, "paths", path, method, "responses")

		got := slices.Sorted(maps.Keys(responses))
		if !slices.Equal(got, append(slices.Clone(wantKeys), "default")) {
			t.Errorf("%s: response keys = %v, want %v and default", key, got, wantKeys)
		}
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

	return decodedBytes(t, apidesc.Document())
}

// decodedBytes returns the document in raw as a JSON value.
func decodedBytes(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
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
	for _, name := range []string{"IdempotencyKey", "IfMatch", "IfMatchRequired"} {
		if _, ok := params[name]; !ok {
			t.Errorf("components.parameters has no %s", name)
		}
	}

	if got := member[bool](t, params, "IfMatch", "required"); got {
		t.Error("components.parameters.IfMatch is required, want optional")
	}

	if got := member[bool](t, params, "IfMatchRequired", "required"); !got {
		t.Error("components.parameters.IfMatchRequired is optional, want required")
	}

	if _, ok := member[map[string]any](t, doc, "components", "headers")["ETag"]; !ok {
		t.Error("components.headers has no ETag")
	}

	checkSecuritySchemes(t, doc)
}

// checkSecuritySchemes checks that the document holds the three schemes of the
// API: the access token, the session cookie and the CSRF header.
func checkSecuritySchemes(t *testing.T, doc map[string]any) {
	t.Helper()

	schemes := member[map[string]any](t, doc, "components", "securitySchemes")
	for _, name := range []string{"bearerAuth", "sessionCookie", "csrfHeader"} {
		if _, ok := schemes[name]; !ok {
			t.Errorf("components.securitySchemes has no %s", name)
		}
	}

	if len(schemes) != 3 {
		t.Errorf("the document holds %d security schemes, want 3", len(schemes))
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

	for _, problem := range subsetFindings(decoded(t)) {
		t.Error(problem)
	}
}

// subsetFindings returns the subset problems of a document, except the two
// closed enums that the document may hold. The values of an open enum sit in a
// schema of their own, and the use is anyOf [ref, string]: that schema is not a
// closed enum. The discriminator member of a branch of a union has one value,
// the constant of the branch: it is the form of OpenAPI 3.0.3 for a constant.
// A one-value enum anywhere else stays a problem.
func subsetFindings(doc map[string]any) []string {
	allowed := openEnumComponents(doc)
	constants := unionConstants(doc)

	var out []string

	for _, problem := range subsetProblems("$", doc, walkState{inProperties: false, openEnum: false}) {
		if name, ok := closedEnumComponent(problem); ok && (allowed[name] || constants[name]) {
			continue
		}

		out = append(out, problem)
	}

	return out
}

// unionConstants returns the paths schema.properties.member, as closedEnumComponent
// reads them, of the discriminator member of each branch that the mapping of a
// discriminator names, when the member is a string with exactly one value.
func unionConstants(doc map[string]any) map[string]bool {
	out := map[string]bool{}

	components, _ := optional[map[string]any](doc, "components")
	schemas, _ := optional[map[string]any](components, "schemas")

	for _, node := range schemas {
		union := map[string]any{"node": node}
		union, _ = optional[map[string]any](union, "node")
		disc, _ := optional[map[string]any](union, "discriminator")
		member, _ := optional[string](disc, "propertyName")
		mapping, _ := optional[map[string]any](disc, "mapping")

		for _, target := range mapping {
			ref, _ := optional[string](map[string]any{"ref": target}, "ref")
			name := strings.TrimPrefix(ref, "#/components/schemas/")

			branch, _ := optional[map[string]any](schemas, name)
			props, _ := optional[map[string]any](branch, "properties")
			prop, _ := optional[map[string]any](props, member)
			values, _ := optional[[]any](prop, "enum")

			if member != "" && prop["type"] == "string" && len(values) == 1 {
				out[name+".properties."+member] = true
			}
		}
	}

	return out
}

// closedEnumComponent returns the name of the component schema that a
// closed-enum problem is about.
func closedEnumComponent(problem string) (string, bool) {
	rest, ok := strings.CutPrefix(problem, "$.components.schemas.")
	if !ok {
		return "", false
	}

	name, _, found := strings.Cut(rest, " has a closed enum")

	return name, found
}

// openEnumComponents returns the names of the component schemas that an anyOf
// refers to, next to a plain string. Each is the list of values of an open
// enum.
func openEnumComponents(doc map[string]any) map[string]bool {
	found := map[string]bool{}

	collectOpenEnumRefs(doc, found)

	return found
}

// collectOpenEnumRefs adds the references of each open anyOf below the value.
func collectOpenEnumRefs(v any, found map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if anyOf, ok := x["anyOf"].([]any); ok && hasPlainString(anyOf) {
			for _, ref := range refsOfAlternatives(anyOf) {
				found[strings.TrimPrefix(ref, "#/components/schemas/")] = true
			}
		}

		for _, child := range x {
			collectOpenEnumRefs(child, found)
		}
	case []any:
		for _, child := range x {
			collectOpenEnumRefs(child, found)
		}
	default:
		// A string, a number, a boolean or null holds no schema.
	}
}

// refsOfAlternatives returns the references among the alternatives of an anyOf.
func refsOfAlternatives(alts []any) []string {
	var refs []string

	for _, alt := range alts {
		m, isObject := alt.(map[string]any)
		if !isObject {
			continue
		}

		if ref, isString := m["$ref"].(string); isString {
			refs = append(refs, ref)
		}
	}

	return refs
}

// TestSubsetCheckSeesTheForbiddenForms proves that the walk finds each form
// that the subset refuses, so that a pass of the test above means something.
func TestSubsetCheckSeesTheForbiddenForms(t *testing.T) {
	t.Parallel()

	bad := map[string]string{
		"type list with an object":           `{"type":["object","string"]}`,
		"type list with an enum":             `{"type":["string","number"],"enum":["a"]}`,
		"type list with a format":            `{"type":["string","number"],"format":"date"}`,
		"type list with a name twice":        `{"type":["string","string"]}`,
		"type list of one name":              `{"type":["string"]}`,
		"null type alone":                    `{"type":["null"]}`,
		"closed enum":                        `{"type":"string","enum":["a","b"]}`,
		"const":                              `{"const":"a"}`,
		"oneOf of three":                     `{"oneOf":[{"$ref":"#/components/schemas/a"},{"$ref":"#/components/schemas/b"},{"type":"null"}]}`,
		"oneOf with no null":                 `{"oneOf":[{"$ref":"#/components/schemas/a"},{"$ref":"#/components/schemas/b"}]}`,
		"anyOf with no string":               `{"anyOf":[{"enum":["a"]},{"type":"integer"}]}`,
		"oneOf of two types":                 `{"oneOf":[{"type":"string"},{"type":"integer"}]}`,
		"oneOf inline and null":              `{"oneOf":[{"type":"object"},{"type":"null"}]}`,
		"oneOf with null first":              `{"oneOf":[{"type":"null"},{"$ref":"#/components/schemas/a"}]}`,
		"oneOf with a sibling":               `{"oneOf":[{"$ref":"#/components/schemas/a"},{"type":"null"}],"description":"x"}`,
		"anyOf of two types":                 `{"anyOf":[{"type":"string"},{"type":"integer"}]}`,
		"anyOf with inline enum":             `{"anyOf":[{"type":"string","enum":["a","b"]},{"type":"string"}]}`,
		"anyOf with string first":            `{"anyOf":[{"type":"string"},{"$ref":"#/components/schemas/a"}]}`,
		"anyOf with a third type":            `{"anyOf":[{"$ref":"#/components/schemas/a"},{"type":"string"},{"type":"integer"}]}`,
		"anyOf with two nulls":               `{"anyOf":[{"$ref":"#/components/schemas/a"},{"type":"string"},{"type":"null"},{"type":"null"}]}`,
		"anyOf with a sibling":               `{"anyOf":[{"$ref":"#/components/schemas/a"},{"type":"string"}],"description":"x"}`,
		"anyOf with a typed string":          `{"anyOf":[{"$ref":"#/components/schemas/a"},{"type":"string","format":"uuid"}]}`,
		"one value enum":                     `{"type":"string","enum":["a"]}`,
		"empty enum":                         `{"type":"string","enum":[]}`,
		"one integer enum":                   `{"type":"integer","enum":[1]}`,
		"union with no discriminator":        `{"oneOf":[{"$ref":"#/components/schemas/a"},{"$ref":"#/components/schemas/b"}],"description":"x"}`,
		"union with an inline branch":        `{"oneOf":[{"$ref":"#/components/schemas/a"},{"type":"object"}],"discriminator":{"propertyName":"k","mapping":{"x":"#/components/schemas/a"}}}`,
		"union with a mapping to a stranger": `{"oneOf":[{"$ref":"#/components/schemas/a"},{"$ref":"#/components/schemas/b"}],"discriminator":{"propertyName":"k","mapping":{"x":"#/components/schemas/c"}}}`,
		"union with a sibling":               `{"oneOf":[{"$ref":"#/components/schemas/a"},{"$ref":"#/components/schemas/b"}],"type":"object","discriminator":{"propertyName":"k","mapping":{"x":"#/components/schemas/a"}}}`,
	}

	good := map[string]string{
		"nullable":             `{"type":["string","null"]}`,
		"list of primitives":   `{"type":["string","number","boolean"]}`,
		"nullable ref":         `{"oneOf":[{"$ref":"#/components/schemas/a"},{"type":"null"}]}`,
		"open enum":            `{"anyOf":[{"$ref":"#/components/schemas/a"},{"type":"string"}]}`,
		"open enum or null":    `{"anyOf":[{"$ref":"#/components/schemas/a"},{"type":"string"},{"type":"null"}]}`,
		"property named const": `{"properties":{"const":{"type":"string"}}}`,
		"union":                `{"oneOf":[{"$ref":"#/components/schemas/a"},{"$ref":"#/components/schemas/b"}],"discriminator":{"propertyName":"k","mapping":{"x":"#/components/schemas/a"}},"description":"x"}`,
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

	if types, ok := obj["type"].([]any); ok {
		out = append(out, typeListProblems(where, obj, types)...)
	}

	out = append(out, oneOfAndEnumProblems(where, obj, state)...)

	if anyOf, ok := obj["anyOf"].([]any); ok && (!isOpenEnum(anyOf) || len(obj) != 1) {
		out = append(out, where+" has an anyOf that is not [ref, string] with an optional null: it is not an open enum")
	}

	return out
}

// typeListProblems checks a type member that is a list: the nullable form
// [X, "null"], or a list of two or more primitive names. A list without null
// carries no enum and no format.
func typeListProblems(where string, obj map[string]any, types []any) []string {
	names, seen, problem := typeListNames(types)
	if problem != "" {
		return []string{where + problem}
	}

	if seen["null"] {
		if problem := nullableTypeList(names); problem != "" {
			return []string{where + problem}
		}

		return nil
	}

	if problem := plainTypeList(obj, names); problem != "" {
		return []string{where + problem}
	}

	return nil
}

// typeListNames reads the names of a type list and reports a repeat or a name
// that the subset does not know.
func typeListNames(types []any) ([]string, map[string]bool, string) {
	names := make([]string, 0, len(types))
	seen := make(map[string]bool, len(types))

	for _, item := range types {
		name, ok := item.(string)
		if !ok || !subsetTypeName(name) {
			return nil, nil, " has a type list with a name that is not a type name"
		}

		if seen[name] {
			return nil, nil, " has a type list with a name twice"
		}

		seen[name] = true
		names = append(names, name)
	}

	return names, seen, ""
}

// nullableTypeList checks the nullable form [X, "null"].
func nullableTypeList(names []string) string {
	if len(names) != 2 || names[1] != "null" {
		return ` has the type name "null" outside the second place of a list of two names`
	}

	return ""
}

// plainTypeList checks a type list without null: at least two primitive names,
// with no enum and no format.
func plainTypeList(obj map[string]any, names []string) string {
	if len(names) < 2 {
		return " has a type list with fewer than two names"
	}

	for _, name := range names {
		if !primitiveTypeName(name) {
			return " has a type list with a name that is not a primitive type name"
		}
	}

	for _, key := range []string{"enum", "format"} {
		if obj[key] != nil {
			return " has " + key + " beside a type list"
		}
	}

	return ""
}

// subsetTypeName reports whether the name is a type name of the subset.
func subsetTypeName(name string) bool {
	switch name {
	case "string", "number", "integer", "boolean", "object", "array", "null":
		return true
	default:
		return false
	}
}

// primitiveTypeName reports whether the name is a primitive type name of JSON.
func primitiveTypeName(name string) bool {
	switch name {
	case "string", "number", "integer", "boolean":
		return true
	default:
		return false
	}
}

// oneOfAndEnumProblems returns the problems of oneOf and enum in a schema. A
// oneOf is [ref, null] or a union with a discriminator. An enum is the schema
// of an open enum. The one-value enum of the discriminator of a union branch is
// allowed by subsetFindings, which knows the unions of the document.
func oneOfAndEnumProblems(where string, obj map[string]any, state walkState) []string {
	var out []string

	if one, ok := obj["oneOf"].([]any); ok {
		nullableRef := isRefOrNull(one) && len(obj) == 1

		if !nullableRef && !isDiscriminatedUnion(obj, one) {
			out = append(out, where+" has a oneOf that is not [ref, null] and not a union with a discriminator")
		}
	}

	if _, ok := obj["enum"]; ok && !state.openEnum {
		out = append(out, where+" has a closed enum: an enum that can grow is anyOf [enum, string]")
	}

	return out
}

// isDiscriminatedUnion reports whether a schema is a oneOf of references with
// a discriminator that names a property and a mapping, and with no other key
// than a description. Each entry of the mapping must be a branch.
func isDiscriminatedUnion(obj map[string]any, alts []any) bool {
	disc, ok := obj["discriminator"].(map[string]any)
	if !ok || len(disc) != 2 || len(alts) < 2 {
		return false
	}

	if !hasOnlyKeys(obj, "oneOf", "discriminator", "description") {
		return false
	}

	name, isString := disc["propertyName"].(string)
	mapping, isMap := disc["mapping"].(map[string]any)

	if !isString || name == "" || !isMap || len(mapping) != len(alts)-1 {
		return false
	}

	branches, ok := refsOf(alts)

	return ok && mappingNamesBranches(mapping, branches)
}

// hasOnlyKeys reports whether an object has no key but the listed ones.
func hasOnlyKeys(obj map[string]any, keys ...string) bool {
	for key := range obj {
		if !slices.Contains(keys, key) {
			return false
		}
	}

	return true
}

// refsOf returns the references of the alternatives, or false when an
// alternative is more than a reference.
func refsOf(alts []any) (map[string]bool, bool) {
	refs := map[string]bool{}

	for _, alt := range alts {
		m, isMap := alt.(map[string]any)
		if !isMap || !isOnlyRef(m) {
			return nil, false
		}

		if ref, isString := m["$ref"].(string); isString {
			refs[ref] = true
		}
	}

	return refs, true
}

// mappingNamesBranches reports whether each entry of a discriminator mapping
// is the reference of a branch.
func mappingNamesBranches(mapping map[string]any, branches map[string]bool) bool {
	for _, target := range mapping {
		ref, isString := target.(string)
		if !isString || !branches[ref] {
			return false
		}
	}

	return true
}

// isRefOrNull reports whether a oneOf list is [{$ref}, {type: null}]. Another
// order, another number of members and any other member are not the form.
func isRefOrNull(alts []any) bool {
	return len(alts) == 2 && isOnlyRef(alts[0]) && isOnlyType(alts[1], "null")
}

// isOpenEnum reports whether an anyOf list is [{$ref}, {type: string}], with
// {type: null} after them.
func isOpenEnum(alts []any) bool {
	if len(alts) == 3 && !isOnlyType(alts[2], "null") {
		return false
	}

	return (len(alts) == 2 || len(alts) == 3) && isOnlyRef(alts[0]) && isOnlyType(alts[1], "string")
}

// isOnlyRef reports whether the schema holds a reference and nothing else.
func isOnlyRef(alt any) bool {
	m, ok := alt.(map[string]any)
	if !ok || len(m) != 1 {
		return false
	}

	ref, isString := m["$ref"].(string)

	return isString && strings.HasPrefix(ref, "#/components/schemas/")
}

// isOnlyType reports whether the schema holds the one type and nothing else.
func isOnlyType(alt any, typ string) bool {
	m, ok := alt.(map[string]any)

	return ok && len(m) == 1 && m["type"] == typ
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
		"empty":                      nil,
		"not JSON":                   []byte("<xml/>"),
		"truncate":                   good[:len(good)/2],
		"other format version":       bytes.Replace(good, []byte(`"3.1.0"`), []byte(`"3.0.3"`), 1),
		"other description version":  bytes.Replace(good, []byte(`"`+apidesc.DocumentVersion+`"`), []byte(`"9.9.9"`), 1),
		"success status outside 2xx": bytes.Replace(good, []byte(`"200": {`), []byte(`"302": {`), 1),
		"key 2XX":                    bytes.Replace(good, []byte(`"200": {`), []byte(`"2XX": {`), 1),
		"range key":                  bytes.Replace(good, []byte(`"200": {`), []byte(`"20X": {`), 1),
		"signed key":                 bytes.Replace(good, []byte(`"200": {`), []byte(`"+200": {`), 1),
		"padded key":                 bytes.Replace(good, []byte(`"200": {`), []byte(`"0200": {`), 1),
		"error status key":           bytes.Replace(good, []byte(`"default": {`), []byte(`"404": {`), 1),
		"no success answer":          bytes.Replace(good, []byte(`"200": {`), []byte(`"default": {`), 1),
	} {
		ops, readErr := apidesc.Read(doc)
		if !errors.Is(readErr, apidesc.ErrInvalidDocument) || ops != nil {
			t.Errorf("%s: Read() = %d operations, %v; want ErrInvalidDocument", name, len(ops), readErr)
		}
	}
}

// TestReadRefusesSuccessAnswersThatDisagreeOnTheETag proves that Read refuses
// an operation whose success answers differ in the ETag header, because a
// client would then read one flag from two answers.
func TestReadRefusesSuccessAnswersThatDisagreeOnTheETag(t *testing.T) {
	t.Parallel()

	good, err := apidesc.Build(sampleOperations())
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	var doc map[string]any
	if err = json.Unmarshal(good, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := "/api/dustpan/v1/namespaces/{namespace}/things/{thing_id}"
	responses := member[map[string]any](t, doc, "paths", path, "patch", "responses")
	responses["201"] = map[string]any{"description": "The request succeeded."}

	changed, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if _, err = apidesc.Read(changed); !errors.Is(err, apidesc.ErrInvalidDocument) {
		t.Errorf("Read() = %v, want ErrInvalidDocument", err)
	}
}

// TestOpenEnumComponentNeedsAnOpenUse proves that a schema with an enum passes
// the subset check when an anyOf refers to it next to a plain string, and fails
// when no such anyOf does.
func TestOpenEnumComponentNeedsAnOpenUse(t *testing.T) {
	t.Parallel()

	var doc map[string]any

	text := `{"components":{"schemas":{
	"Open":{"type":"string","enum":["a"]},
	"Closed":{"type":"string","enum":["b"]},
	"Holder":{"type":"object","properties":{
	"kind":{"anyOf":[{"$ref":"#/components/schemas/Open"},{"type":"string"}]}}}}}}`

	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}

	allowed := openEnumComponents(doc)
	if !allowed["Open"] || allowed["Closed"] || allowed["Holder"] {
		t.Fatalf("openEnumComponents() = %v, want only Open", allowed)
	}

	var flagged []string

	for _, problem := range subsetProblems("$", doc, walkState{inProperties: false, openEnum: false}) {
		if name, ok := closedEnumComponent(problem); ok && !allowed[name] {
			flagged = append(flagged, name)
		}
	}

	if !slices.Equal(flagged, []string{"Closed"}) {
		t.Errorf("closed enums = %v, want [Closed]", flagged)
	}
}

// TestOneValueEnumIsAllowedOnlyForTheDiscriminator proves that the exemption
// of the subset check covers the discriminator member of a branch of a union
// and nothing else: a one-value enum in another member of the branch, in a
// schema that no mapping names, in a member with a value that is not a string,
// and in a branch whose member has two values stays a problem.
func TestOneValueEnumIsAllowedOnlyForTheDiscriminator(t *testing.T) {
	t.Parallel()

	const (
		union  = `"U":{"oneOf":[{"$ref":"#/components/schemas/A"},{"$ref":"#/components/schemas/B"}],"discriminator":{"propertyName":"kind","mapping":{"a":"#/components/schemas/A"}}}`
		branch = `"A":{"type":"object","properties":{"kind":{"type":"string","enum":["a"]}%s}}`
	)

	tests := []struct {
		name, schemas string
		want          int
	}{
		{"the discriminator of a branch", union + `,"B":{"type":"object"},` + fmt.Sprintf(branch, ""), 0},
		{
			"another member of the branch",
			union + `,"B":{"type":"object"},` + fmt.Sprintf(branch, `,"other":{"type":"string","enum":["x"]}`),
			1,
		},
		{
			"a schema that no mapping names",
			union + `,"B":{"type":"object"},` + fmt.Sprintf(
				branch,
				"",
			) + `,"C":{"type":"object","properties":{"kind":{"type":"string","enum":["a"]}}}`,
			1,
		},
		{
			"a branch member with two values",
			union + `,"B":{"type":"object"},"A":{"type":"object","properties":{"kind":{"type":"string","enum":["a","b"]}}}`,
			1,
		},
		{
			"a branch member that is not a string",
			union + `,"B":{"type":"object"},"A":{"type":"object","properties":{"kind":{"type":"integer","enum":[1]}}}`,
			1,
		},
		{
			"a one-value enum and no union",
			`"A":{"type":"object","properties":{"kind":{"type":"string","enum":["a"]}}}`,
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var doc map[string]any

			text := `{"components":{"schemas":{` + tt.schemas + `}}}`
			if err := json.Unmarshal([]byte(text), &doc); err != nil {
				t.Fatal(err)
			}

			if got := subsetFindings(doc); len(got) != tt.want {
				t.Errorf("subsetFindings() = %q, want %d problems", got, tt.want)
			}
		})
	}
}
