package clientgen_test

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// baseDocument is a small description that the parser reads. The tests change
// one part of it with a text replacement.
// baseOperation is the one path item of the base document.
const baseOperation = `"/api/dustpan/v1/things/{thing_id}": {
   "get": {
    "operationId": "getThing",
    "tags": ["things"],
    "parameters": [{"in": "path", "name": "thing_id", "required": true, "schema": {"type": "string"}}],
    "responses": {
     "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}},
     "default": {"description": "err", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorResponse"}}}}
    },
    "x-dustpan-actions": [{"action": "unit:read", "object": "unit"}]
   }
  }`

// baseDocument is a small description that the parser reads. The tests change
// one part of it with a text replacement.
const baseDocument = `{
 "openapi": "3.1.0",
 "info": {"title": "T", "version": "0.1.0"},
 "tags": [{"name": "things", "description": "d"}],
 "paths": {
  ` + baseOperation + `
 },
 "components": {
  "schemas": {
   "Thing": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}},
   "ErrorResponse": {"type": "object", "required": ["code", "message"],
    "properties": {"code": {"type": "integer"}, "message": {"type": "string"}, "errorCode": {"type": "string"},
     "errorArgs": {"type": "object", "additionalProperties": true}}}
  }
 }
}`

// change returns the base document with one text replaced. It fails the test
// when the text is not in the document, so a change cannot do nothing.
func change(t *testing.T, old, replacement string) []byte {
	t.Helper()

	if !strings.Contains(baseDocument, old) {
		t.Fatalf("the base document has no %q", old)
	}

	return []byte(strings.Replace(baseDocument, old, replacement, 1))
}

// parseErr parses a document and returns the error.
func parseErr(doc string, ids ...string) error {
	_, err := clientgen.NewParser(ids...).Parse([]byte(doc))

	return err
}

func TestParseBaseDocument(t *testing.T) {
	t.Parallel()

	api, err := clientgen.NewParser().Parse([]byte(baseDocument))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if api.Title != "T" || api.Version != "0.1.0" || api.Error != "ErrorResponse" || len(api.Operations) != 1 {
		t.Fatalf("document: %+v", api)
	}

	checkBaseOperation(t, api.Operations[0])

	names := make([]string, 0, len(api.Schemas))
	for _, s := range api.Schemas {
		names = append(names, s.Name)
	}

	if !reflect.DeepEqual(names, []string{"ErrorResponse", "Thing"}) {
		t.Errorf("schemas are not in the order of their names: %v", names)
	}
}

func checkBaseOperation(t *testing.T, op model.Operation) {
	t.Helper()

	if op.ID != "getThing" || op.Method != model.MethodGet || op.Public ||
		!reflect.DeepEqual(op.Actions, []string{"unit:read"}) {
		t.Errorf("operation: %+v", op)
	}

	if len(op.Parameters) != 1 || op.Parameters[0].Name != "thing_id" || op.Parameters[0].In != model.InPath {
		t.Errorf("parameters: %+v", op.Parameters)
	}

	if op.Result.Type == nil || op.Result.Type.Ref != "Thing" || !slices.Equal(op.Result.Statuses, []int{200}) {
		t.Errorf("result: %+v", op.Result)
	}
}

// TestParseReadsTheExactSuccessStatus checks that an exact success key gives
// its status, and that two keys with the same answer give both statuses in
// ascending order.
func TestParseReadsTheExactSuccessStatus(t *testing.T) {
	t.Parallel()

	for key, want := range map[string][]int{"200": {200}, "201": {201}, "202": {202}, "204": {204}} {
		doc := change(t, `"200": {"description": "ok", "content"`, `"`+key+`": {"description": "ok", "content"`)

		api, err := clientgen.NewParser().Parse(doc)
		if err != nil {
			t.Fatalf("%s: parse: %v", key, err)
		}

		if got := api.Operations[0].Result.Statuses; !slices.Equal(got, want) {
			t.Errorf("%s: statuses = %v, want %v", key, got, want)
		}
	}

	both := change(
		t,
		`"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}},`,
		`"201": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}},
     "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}},`,
	)

	api, err := clientgen.NewParser().Parse(both)
	if err != nil {
		t.Fatalf("two keys: parse: %v", err)
	}

	if got := api.Operations[0].Result.Statuses; !slices.Equal(got, []int{200, 201}) {
		t.Errorf("two keys: statuses = %v, want [200 201]", got)
	}
}

// TestParseRefuses checks that a construct outside the subset, a bad name or a
// broken rule gives the right error and names the place.
func TestParseRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		old     string
		replace string
		want    error
		at      string
	}{
		{
			"unknown key in an operation",
			`"operationId": "getThing",`,
			`"operationId": "getThing", "deprecated": true,`,
			clientgen.ErrUnsupported,
			"deprecated",
		},
		{
			"external ref",
			`"$ref": "#/components/schemas/Thing"`,
			`"$ref": "other.json#/Thing"`,
			clientgen.ErrUnsupported,
			"$ref",
		},
		{
			"allOf in a schema",
			`"name": {"type": "string"}}}`,
			`"name": {"allOf": [{"type": "string"}]}}}`,
			clientgen.ErrUnsupported,
			"allOf",
		},
		{
			"schema with no type",
			`"name": {"type": "string"}}}`,
			`"name": {"format": "any"}}}`,
			clientgen.ErrUnsupported,
			"properties/name",
		},
		{
			"schema with no type and an enum",
			`"name": {"type": "string"}}}`,
			`"name": {"description": "any", "enum": ["a"]}}}`,
			clientgen.ErrUnsupported,
			"properties/name",
		},
		{
			"schema with no type and fields",
			`"name": {"type": "string"}}}`,
			`"name": {"properties": {"a": {"type": "string"}}}}}`,
			clientgen.ErrUnsupported,
			"properties/name",
		},
		{
			"parameter with an any-value schema",
			`"schema": {"type": "string"}}],`,
			`"schema": {"description": "any"}}],`,
			clientgen.ErrUnsupported,
			"parameters/0/schema",
		},
		{
			"any-value result with a description that is not a string",
			`{"schema": {"$ref": "#/components/schemas/Thing"}}}},`,
			`{"schema": {"description": 5}}}},`,
			clientgen.ErrInvalidDescription,
			"schema/description",
		},
		{"wrong openapi version", `"openapi": "3.1.0"`, `"openapi": "3.0.3"`, clientgen.ErrInvalidDescription, ""},
		{
			"operation ID with an underscore",
			`"operationId": "getThing"`,
			`"operationId": "get_thing"`,
			clientgen.ErrInvalidName,
			"operationId",
		},
		{
			"operation ID with a capital first letter",
			`"operationId": "getThing"`,
			`"operationId": "GetThing"`,
			clientgen.ErrInvalidName,
			"operationId",
		},
		{
			"field name that breaks the allowlist",
			`"name": {"type": "string"}}}`,
			`"Name": {"type": "string"}, "name": {"type": "string"}}}`,
			clientgen.ErrInvalidName,
			"Name",
		},
		{
			"path with a dot-dot segment",
			`/api/dustpan/v1/things/{thing_id}`,
			`/api/dustpan/v1/../{thing_id}`,
			clientgen.ErrInvalidName,
			"",
		},
		{
			"path outside the prefix",
			`/api/dustpan/v1/things/{thing_id}`,
			`/api/v2/things/{thing_id}`,
			clientgen.ErrInvalidDescription,
			"",
		},
		{
			"catch-all wildcard",
			`/api/dustpan/v1/things/{thing_id}`,
			`/api/dustpan/v1/things/{thing_id...}`,
			clientgen.ErrUnsupported,
			"",
		},
		{
			"wildcard with no parameter",
			`/api/dustpan/v1/things/{thing_id}`,
			`/api/dustpan/v1/things/{thing_id}/{other}`,
			clientgen.ErrInvalidDescription,
			"parameters",
		},
		{
			"header parameter",
			`"in": "path", "name": "thing_id"`,
			`"in": "header", "name": "X-Thing"`,
			clientgen.ErrUnsupported,
			"",
		},
		{
			"required field that is not a property",
			`"required": ["name"]`,
			`"required": ["nope"]`,
			clientgen.ErrInvalidDescription,
			"required",
		},
		{
			"action in the wrong shape",
			`"action": "unit:read"`,
			`"action": "UNIT READ"`,
			clientgen.ErrInvalidName,
			"action",
		},
		{
			"object kind that is not in the list",
			`"object": "unit"`,
			`"object": "planet"`,
			clientgen.ErrInvalidDescription,
			"object",
		},
		{
			"public operation with an action",
			`"x-dustpan-actions"`,
			`"security": [], "x-dustpan-actions"`,
			clientgen.ErrInvalidDescription,
			"x-dustpan-actions",
		},
		{
			"security on an operation",
			`"x-dustpan-actions"`,
			`"security": [{"bearer": []}], "x-dustpan-actions"`,
			clientgen.ErrUnsupported,
			"security",
		},
		{
			"success answers with different bodies",
			`"default": {`,
			`"201": {"description": "x"}, "default": {`,
			clientgen.ErrInvalidDescription,
			"201",
		},
		{
			"success key 2XX",
			`"200": {"description": "ok", "content"`,
			`"2XX": {"description": "ok", "content"`,
			clientgen.ErrUnsupported,
			"2XX",
		},
		{
			"success key outside 2xx",
			`"default": {`,
			`"302": {"description": "x"}, "default": {`,
			clientgen.ErrUnsupported,
			"302",
		},
		{
			"default answer that is not the error schema",
			`"$ref": "#/components/schemas/ErrorResponse"}}}}`,
			`"$ref": "#/components/schemas/Thing"}}}}`,
			clientgen.ErrInvalidDescription,
			"default",
		},
		{
			"response header other than ETag",
			`"200": {"description": "ok",`,
			`"200": {"description": "ok", "headers": {"X-Other": {"description": "x"}},`,
			clientgen.ErrUnsupported,
			"X-Other",
		},
		{
			"media type outside the subset",
			`"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}`,
			`"text/plain": {"schema": {"$ref": "#/components/schemas/Thing"}}`,
			clientgen.ErrUnsupported,
			"text~1plain",
		},
		{
			"schema name in lowercase",
			`"Thing": {"type": "object"`,
			`"thing": {"type": "object"`,
			clientgen.ErrInvalidDescription,
			"",
		},
		{
			"enum value with a space",
			`"name": {"type": "string"}}},`,
			`"name": {"type": "string"}, "kind": {"$ref": "#/components/schemas/Kind"}}},
   "Kind": {"type": "string", "enum": ["a b"]},`,
			clientgen.ErrInvalidName,
			"enum",
		},
		{"data after the document", ``, ``, clientgen.ErrInvalidDescription, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := []byte(baseDocument + " {}")
			if tt.old != "" {
				doc = change(t, tt.old, tt.replace)
			}

			_, err := clientgen.NewParser().Parse(doc)
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}

			if tt.at != "" && !strings.Contains(err.Error(), tt.at) {
				t.Errorf("the error does not name %q: %v", tt.at, err)
			}
		})
	}
}

func TestParseRefusesDocumentThatIsNotJSON(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{"", "[]", `"text"`, "{", `{"openapi": 3}`} {
		if _, err := clientgen.NewParser().Parse([]byte(doc)); !errors.Is(err, clientgen.ErrInvalidDescription) {
			t.Errorf("document %q: want ErrInvalidDescription, got %v", doc, err)
		}
	}
}

func TestParseSizeLimit(t *testing.T) {
	t.Parallel()

	big := make([]byte, clientgen.MaxDescriptionBytes+1)
	if _, err := clientgen.NewParser().Parse(big); !errors.Is(err, clientgen.ErrInvalidDescription) {
		t.Fatalf("want ErrInvalidDescription for a document of %d bytes, got %v", len(big), err)
	}
}

func TestParseSelection(t *testing.T) {
	t.Parallel()

	if err := parseErr(baseDocument, "getThing"); err != nil {
		t.Errorf("select a known operation: %v", err)
	}

	if err := parseErr(baseDocument, "noSuchOperation"); !errors.Is(err, clientgen.ErrInvalidDescription) {
		t.Errorf("select an unknown operation: want ErrInvalidDescription, got %v", err)
	}

	// An operation that is not selected is not read, so a construct in it does
	// not stop the run.
	other := `"/api/dustpan/v1/other": {"get": {"operationId": "getOther", "x": 1}},`

	unread := strings.Replace(baseDocument, `"paths": {`, `"paths": {`+other, 1)
	if err := parseErr(unread, "getThing"); err != nil {
		t.Errorf("a construct in an operation that is not selected: %v", err)
	}

	// The rule on duplicate IDs holds for every operation.
	twin := strings.Replace(baseOperation, "/things/", "/twins/", 1)

	twice := strings.Replace(baseDocument, `"paths": {`, `"paths": {`+twin+`,`, 1)
	if err := parseErr(twice, "getThing"); !errors.Is(err, clientgen.ErrInvalidDescription) {
		t.Errorf("two operations with one ID: want ErrInvalidDescription, got %v", err)
	}
}

func TestParseIsDeterministic(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(descriptionPath)
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	p := clientgen.NewParser(coveredOperations()...)

	first, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	second, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("parse again: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatal("two parses of one description differ")
	}
}

// coveredOperations are the operations of the first slice of the Go client.
func coveredOperations() []string {
	return []string{
		"createProfile", "createProfileRevision", "getCapabilities", "getNotificationSink",
		"getPreferences", "getProfile", "listNotificationSinks",
	}
}

// TestParsePublishedDescription reads the published description for the
// operations of the first slice, and checks the parts that the Go client uses.
func TestParsePublishedDescription(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(descriptionPath)
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	api, err := clientgen.NewParser(coveredOperations()...).Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	byID := make(map[string]model.Operation)
	ids := make([]string, 0, len(api.Operations))

	for _, op := range api.Operations {
		byID[op.ID] = op
		ids = append(ids, op.ID)
	}

	if !reflect.DeepEqual(ids, coveredOperations()) {
		t.Fatalf("operations: want %v, got %v", coveredOperations(), ids)
	}

	for _, c := range publishedChecks() {
		if !c.check(byID[c.id]) {
			t.Errorf("%s: it should have a property: %s: %+v", c.id, c.what, byID[c.id])
		}
	}
}

type publishedCheck struct {
	id    string
	what  string
	check func(model.Operation) bool
}

func publishedChecks() []publishedCheck {
	return append([]publishedCheck{
		{"getCapabilities", "is public with no action", func(o model.Operation) bool {
			return o.Public && len(o.Actions) == 0
		}},
		{"getNotificationSink", "has its path parameters in the order of the path", func(o model.Operation) bool {
			return len(o.Parameters) == 2 && o.Parameters[0].Name == "namespace" && o.Parameters[1].Name == "sink_id"
		}},
	}, profileChecks()...)
}

func profileChecks() []publishedCheck {
	return []publishedCheck{
		{"getProfile", "answers with an ETag", func(o model.Operation) bool {
			return o.Result.ETag && !o.IdempotencyKey
		}},
		{"createProfile", "takes Idempotency-Key, no If-Match, and has a required body", func(o model.Operation) bool {
			return o.IdempotencyKey && o.IfMatch == model.IfMatchNone && o.Body != nil && o.Body.Required &&
				o.Result.ETag
		}},
		{"createProfileRevision", "takes Idempotency-Key and a required If-Match", func(o model.Operation) bool {
			return o.IdempotencyKey && o.IfMatch == model.IfMatchRequired && o.Body != nil && o.Body.Required &&
				o.Result.ETag
		}},
	}
}

// TestParseRefusesAnErrorSchemaOfAnotherShape checks that the parser holds the
// shape of the error answer that every transport decodes.
func TestParseRefusesAnErrorSchemaOfAnotherShape(t *testing.T) {
	t.Parallel()

	const good = `"properties": {"code": {"type": "integer"}, "message": {"type": "string"}, "errorCode": {"type": "string"},
     "errorArgs": {"type": "object", "additionalProperties": true}}}`

	tests := []struct{ name, replacement string }{
		{"errorCode is missing", `"properties": {"code": {"type": "integer"}, "message": {"type": "string"},
     "errorArgs": {"type": "object", "additionalProperties": true}}}`},
		{"errorArgs is missing", `"properties": {"code": {"type": "integer"}, "message": {"type": "string"},
     "errorCode": {"type": "string"}}}`},
		{"code is a string", `"properties": {"code": {"type": "string"}, "message": {"type": "string"},
     "errorCode": {"type": "string"}, "errorArgs": {"type": "object", "additionalProperties": true}}}`},
		{"message is an integer", `"properties": {"code": {"type": "integer"}, "message": {"type": "integer"},
     "errorCode": {"type": "string"}, "errorArgs": {"type": "object", "additionalProperties": true}}}`},
		{"errorCode is an integer", `"properties": {"code": {"type": "integer"}, "message": {"type": "string"},
     "errorCode": {"type": "integer"}, "errorArgs": {"type": "object", "additionalProperties": true}}}`},
		{"errorCode can be null", `"properties": {"code": {"type": "integer"}, "message": {"type": "string"},
     "errorCode": {"type": ["string", "null"]}, "errorArgs": {"type": "object", "additionalProperties": true}}}`},
		{"errorArgs is a string", `"properties": {"code": {"type": "integer"}, "message": {"type": "string"},
     "errorCode": {"type": "string"}, "errorArgs": {"type": "string"}}}`},
		{"errorArgs has typed values", `"properties": {"code": {"type": "integer"}, "message": {"type": "string"},
     "errorCode": {"type": "string"}, "errorArgs": {"type": "object", "additionalProperties": {"type": "string"}}}}`},
		{"a member that is not known", `"properties": {"code": {"type": "integer"}, "message": {"type": "string"},
     "errorCode": {"type": "string"}, "errorArgs": {"type": "object", "additionalProperties": true},
     "extra": {"type": "string"}}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := change(t, good, tt.replacement)
			if err := parseErr(string(doc)); !errors.Is(err, clientgen.ErrInvalidDescription) {
				t.Fatalf("want ErrInvalidDescription, got %v", err)
			}
		})
	}

	// errorCode and errorArgs required: the transport reads a pointer and a
	// map, so a required member is another shape.
	for _, member := range []string{"errorCode", "errorArgs"} {
		doc := change(t, `"required": ["code", "message"]`, `"required": ["code", "message", "`+member+`"]`)
		if err := parseErr(string(doc)); !errors.Is(err, clientgen.ErrInvalidDescription) {
			t.Errorf("%s required: want ErrInvalidDescription, got %v", member, err)
		}
	}

	// code optional.
	doc := change(t, `"required": ["code", "message"]`, `"required": ["message"]`)
	if err := parseErr(string(doc)); !errors.Is(err, clientgen.ErrInvalidDescription) {
		t.Errorf("code optional: want ErrInvalidDescription, got %v", err)
	}
}

// errorCodeDocument returns the base document with an ErrorCode schema and the
// given form of the errorCode member.
func errorCodeDocument(t *testing.T, codeSchema, member string) string {
	t.Helper()

	doc := change(t, `"ErrorResponse": {"type": "object"`, codeSchema+`, "ErrorResponse": {"type": "object"`)

	return strings.Replace(string(doc), `"errorCode": {"type": "string"}`, `"errorCode": `+member, 1)
}

// TestParseReadsErrorCodeAsAnOpenEnum checks that errorCode may be an open enum
// of strings, and that no other reference is the error shape.
func TestParseReadsErrorCodeAsAnOpenEnum(t *testing.T) {
	t.Parallel()

	const (
		enum    = `"ErrorCode": {"type": "string", "enum": ["a.b", "c.d"]}`
		ref     = `{"$ref": "#/components/schemas/ErrorCode"}`
		openRef = `{"anyOf": [` + ref + `, {"type": "string"}]}`
	)

	tests := []struct {
		name, codeSchema, member string
		valid                    bool
	}{
		{"open enum", enum, openRef, true},
		{"closed enum", enum, ref, false},
		{"nullable open enum", enum, `{"anyOf": [` + ref + `, {"type": "string"}, {"type": "null"}]}`, false},
		{"reference to an object", `"ErrorCode": {"type": "object", "properties": {}}`, openRef, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := parseErr(errorCodeDocument(t, tt.codeSchema, tt.member))

			switch {
			case tt.valid && err != nil:
				t.Fatalf("want no error, got %v", err)
			case !tt.valid && err == nil:
				t.Fatal("want an error, got none")
			}
		})
	}
}
