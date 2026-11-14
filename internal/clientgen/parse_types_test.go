package clientgen_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// typesDocument covers the types of the subset: scalars, lists, maps, closed
// and open enums, nullable types and a list that pages with a cursor.
const typesDocument = `{
 "openapi": "3.1.0",
 "info": {"title": "T", "version": "0.2.0"},
 "paths": {
  "/api/dustpan/v1/widgets": {
   "get": {
    "operationId": "listWidgets",
    "parameters": [
     {"in": "query", "name": "pageToken", "schema": {"type": "string"}},
     {"in": "query", "name": "pageSize", "schema": {"type": "integer", "format": "int64"}},
     {"in": "query", "name": "include", "schema": {"type": "array", "items": {"type": "string"}}}
    ],
    "responses": {
     "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ListWidgetsResponse"}}}},
     "default": {"description": "err", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorResponse"}}}}
    },
    "x-dustpan-actions": [{"action": "unit:read", "object": "unit"}]
   }
  }
 },
 "components": {
  "schemas": {
   "ListWidgetsResponse": {"type": "object", "required": ["widgets"], "properties": {
    "widgets": {"type": "array", "items": {"$ref": "#/components/schemas/Widget"}},
    "nextPageToken": {"type": "string"}}},
   "Widget": {"type": "object", "required": ["id", "state"], "properties": {
    "id": {"type": "string"},
    "count": {"type": "integer", "format": "int64"},
    "ratio": {"type": "number"},
    "flag": {"type": "boolean"},
    "tags": {"type": "array", "items": {"type": "string"}},
    "labels": {"type": "object", "additionalProperties": {"type": "string"}},
    "anything": {"type": "object", "additionalProperties": true},
    "value": {"description": "Any JSON value."},
    "bare": {},
    "status": {"anyOf": [{"$ref": "#/components/schemas/Status"}, {"type": "string"}]},
    "maybeStatus": {"anyOf": [{"$ref": "#/components/schemas/Status"}, {"type": "string"}, {"type": "null"}]},
    "state": {"$ref": "#/components/schemas/State"},
    "describedState": {"$ref": "#/components/schemas/State", "description": "The described state."},
    "parent": {"oneOf": [{"$ref": "#/components/schemas/Widget"}, {"type": "null"}]},
    "mixed": {"type": ["string", "number", "boolean"], "description": "A mixed value."},
    "note": {"type": ["string", "null"], "description": "A note.", "minLength": 1, "maxLength": 10}}},
   "Status": {"type": "string", "enum": ["onCall", "awaiting-approval"]},
   "State": {"type": "string", "enum": ["open", "closed"]},
   "ErrorResponse": {"type": "object", "required": ["code", "message"],
    "properties": {"code": {"type": "integer"}, "message": {"type": "string"}, "errorCode": {"type": "string"},
     "errorArgs": {"type": "object", "additionalProperties": true}}}
  }
 }
}`

// typ makes a type with the fields that the test does not care about empty.
func typ(kind model.TypeKind) model.Type {
	return model.Type{Kind: kind, Format: "", Elem: nil, Ref: "", Const: "", Nullable: false, Members: nil}
}

func nullableOf(t model.Type) model.Type {
	t.Nullable = true

	return t
}

// unionOf makes the type of a list of primitive type names.
func unionOf(kinds ...model.TypeKind) model.Type {
	t := typ(model.TypeUnion)
	t.Members = kinds

	return t
}

func listOf(kind model.TypeKind, elem model.Type) model.Type {
	t := typ(kind)
	t.Elem = &elem

	return t
}

func refTo(name string, nullable bool) model.Type {
	t := typ(model.TypeRef)
	t.Ref, t.Nullable = name, nullable

	return t
}

func described(f model.Field, description string) model.Field {
	f.Description = description

	return f
}

func fieldOf(name string, t model.Type, required bool) model.Field {
	return model.Field{Name: name, Type: t, Required: required, Description: ""}
}

// wantWidgetFields are the fields of Widget in typesDocument, by wire name.
func wantWidgetFields() map[string]model.Field {
	str := typ(model.TypeString)
	int64Type := typ(model.TypeInteger)
	int64Type.Format = "int64"

	note := fieldOf("note", nullableOf(str), false)
	note.Description = "A note."

	mixed := fieldOf("mixed", unionOf(model.TypeString, model.TypeNumber, model.TypeBoolean), false)
	mixed.Description = "A mixed value."

	return map[string]model.Field{
		"id":             fieldOf("id", str, true),
		"count":          fieldOf("count", int64Type, false),
		"ratio":          fieldOf("ratio", typ(model.TypeNumber), false),
		"flag":           fieldOf("flag", typ(model.TypeBoolean), false),
		"tags":           fieldOf("tags", listOf(model.TypeArray, str), false),
		"labels":         fieldOf("labels", listOf(model.TypeMap, str), false),
		"anything":       fieldOf("anything", listOf(model.TypeMap, typ(model.TypeAny)), false),
		"value":          described(fieldOf("value", typ(model.TypeAny), false), "Any JSON value."),
		"bare":           fieldOf("bare", typ(model.TypeAny), false),
		"status":         fieldOf("status", refTo("Status", false), false),
		"maybeStatus":    fieldOf("maybeStatus", refTo("Status", true), false),
		"state":          fieldOf("state", refTo("State", false), true),
		"describedState": described(fieldOf("describedState", refTo("State", false), false), "The described state."),
		"parent":         fieldOf("parent", refTo("Widget", true), false),
		"mixed":          mixed,
		"note":           note,
	}
}

func schemaNamed(api *model.API, name string) *model.Schema {
	for i := range api.Schemas {
		if api.Schemas[i].Name == name {
			return &api.Schemas[i]
		}
	}

	return nil
}

func TestParseTypes(t *testing.T) {
	t.Parallel()

	api, err := clientgen.NewParser().Parse([]byte(typesDocument))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	widget, status, state := schemaNamed(api, "Widget"), schemaNamed(api, "Status"), schemaNamed(api, "State")
	if widget == nil || status == nil || state == nil {
		t.Fatalf("the schemas are missing: %+v", api.Schemas)
	}

	want := wantWidgetFields()
	if len(widget.Fields) != len(want) {
		t.Fatalf("Widget has %d fields, want %d", len(widget.Fields), len(want))
	}

	prev := ""

	for _, f := range widget.Fields {
		if f.Name <= prev {
			t.Errorf("the fields are not in the order of their wire names: %s after %s", f.Name, prev)
		}

		prev = f.Name

		if !reflect.DeepEqual(f, want[f.Name]) {
			t.Errorf("field %s: want %+v, got %+v", f.Name, want[f.Name], f)
		}
	}

	if !status.Open || state.Open {
		t.Errorf("Status should be open and State closed: %v %v", status.Open, state.Open)
	}

	if !reflect.DeepEqual(status.Values, []string{"onCall", "awaiting-approval"}) {
		t.Errorf("enum values keep the order of the description: %v", status.Values)
	}
}

func TestParsePage(t *testing.T) {
	t.Parallel()

	api, err := clientgen.NewParser().Parse([]byte(typesDocument))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	op := api.Operations[0]
	want := &model.Page{
		SizeParameter: "pageSize", TokenParameter: "pageToken", ItemsField: "widgets", NextField: "nextPageToken",
	}

	if !reflect.DeepEqual(op.Page, want) {
		t.Errorf("page: want %+v, got %+v", want, op.Page)
	}

	names := make([]string, 0, len(op.Parameters))
	for _, p := range op.Parameters {
		names = append(names, p.Name)
	}

	if !reflect.DeepEqual(names, []string{"include", "pageSize", "pageToken"}) {
		t.Errorf("query parameters are not in the order of their names: %v", names)
	}
}

// TestParsePageNeedsAllNames checks that a part of the three names of a paged
// list is an error, not a guess.
func TestParsePageNeedsAllNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		old     string
		replace string
	}{
		{"no pageToken", `{"in": "query", "name": "pageToken", "schema": {"type": "string"}},`, ``},
		{"no pageSize", `{"in": "query", "name": "pageSize", "schema": {"type": "integer", "format": "int64"}},`, ``},
		{"no nextPageToken", `"nextPageToken": {"type": "string"}`, `"other": {"type": "string"}`},
		{
			"two arrays",
			`"nextPageToken": {"type": "string"}`,
			`"nextPageToken": {"type": "string"}, "more": {"type": "array", "items": {"type": "string"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(typesDocument, tt.old) {
				t.Fatalf("the document has no %q", tt.old)
			}

			doc := strings.Replace(typesDocument, tt.old, tt.replace, 1)
			if _, err := clientgen.NewParser().Parse([]byte(doc)); !errors.Is(err, clientgen.ErrInvalidDescription) {
				t.Fatalf("want ErrInvalidDescription, got %v", err)
			}
		})
	}
}

// TestParseRefusesBadTypeValue checks that a type value that the subset does
// not allow is an error that names the value, never a fallback type.
func TestParseRefusesBadTypeValue(t *testing.T) {
	t.Parallel()

	const old = `"mixed": {"type": ["string", "number", "boolean"]`

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			"an object name in a list without null",
			`["object", "string"]`,
			`type name "object" that is not a primitive type name`,
		},
		{
			"an array name in a list without null",
			`["array", "number"]`,
			`type name "array" that is not a primitive type name`,
		},
		{"an empty list", `[]`, `type list [] that holds fewer than two names`},
		{"a list with one name", `["string"]`, `type list ["string"] that holds fewer than two names`},
		{"a list with null alone", `["null"]`, `type list ["null"] that holds fewer than two names`},
		{"null before the name", `["null", "string"]`, `type name "null" that is not a primitive type name`},
		{"null before an integer", `["null", "integer"]`, `type name "null" that is not a primitive type name`},
		{"an unknown name", `["text", "number"]`, `type name "text" that is not a primitive type name`},
		{"a repeated name", `["string", "string"]`, `type name "string" that is in the list two times`},
		{"a non-string item", `["string", 5]`, `type name 5 that is not a string`},
		{"a type that is a number", `5`, `type 5`},
		{"a type that is a boolean", `true`, `type true`},
		{"an enum beside a type list", `["string", "number"], "enum": ["a"]`, `enum beside a type list`},
		{"a format beside a type list", `["string", "number"], "format": "date-time"`, `format beside a type list`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(typesDocument, old) {
				t.Fatalf("the document has no %q", old)
			}

			doc := strings.Replace(typesDocument, old, `"mixed": {"type": `+tt.value, 1)

			_, err := clientgen.NewParser().Parse([]byte(doc))
			if !errors.Is(err, clientgen.ErrUnsupported) {
				t.Fatalf("want ErrUnsupported, got %v", err)
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("the error does not name the value: want %q in %q", tt.want, err)
			}
		})
	}
}

// TestParseRefusesAKeyBesideARef checks that a $ref takes a description of the
// field and no other key.
func TestParseRefusesAKeyBesideARef(t *testing.T) {
	t.Parallel()

	const old = `"describedState": {"$ref": "#/components/schemas/State", "description": "The described state."}`

	if !strings.Contains(typesDocument, old) {
		t.Fatalf("the document has no %q", old)
	}

	doc := strings.Replace(typesDocument, old,
		`"describedState": {"$ref": "#/components/schemas/State", "format": "date"}`, 1)

	_, err := clientgen.NewParser().Parse([]byte(doc))
	if !errors.Is(err, clientgen.ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}

	if !strings.Contains(err.Error(), "key beside $ref") ||
		!strings.Contains(err.Error(), "/properties/describedState/format") {
		t.Fatalf("the error does not name the key: %v", err)
	}
}

// TestParseEnumUsedOpenAndClosed checks that one enum cannot be open in one
// field and closed in another.
func TestParseEnumUsedOpenAndClosed(t *testing.T) {
	t.Parallel()

	doc := strings.Replace(typesDocument, `"state": {"$ref": "#/components/schemas/State"},`,
		`"state": {"$ref": "#/components/schemas/State"}, "sameStatus": {"$ref": "#/components/schemas/Status"},`, 1)
	if _, err := clientgen.NewParser().Parse([]byte(doc)); !errors.Is(err, clientgen.ErrInvalidDescription) {
		t.Fatalf("want ErrInvalidDescription, got %v", err)
	}
}
