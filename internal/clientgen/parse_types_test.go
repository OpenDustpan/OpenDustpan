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
    "parent": {"oneOf": [{"$ref": "#/components/schemas/Widget"}, {"type": "null"}]},
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
	return model.Type{Kind: kind, Format: "", Elem: nil, Ref: "", Nullable: false}
}

func nullableOf(t model.Type) model.Type {
	t.Nullable = true

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

	return map[string]model.Field{
		"id":          fieldOf("id", str, true),
		"count":       fieldOf("count", int64Type, false),
		"ratio":       fieldOf("ratio", typ(model.TypeNumber), false),
		"flag":        fieldOf("flag", typ(model.TypeBoolean), false),
		"tags":        fieldOf("tags", listOf(model.TypeArray, str), false),
		"labels":      fieldOf("labels", listOf(model.TypeMap, str), false),
		"anything":    fieldOf("anything", listOf(model.TypeMap, typ(model.TypeAny)), false),
		"value":       described(fieldOf("value", typ(model.TypeAny), false), "Any JSON value."),
		"bare":        fieldOf("bare", typ(model.TypeAny), false),
		"status":      fieldOf("status", refTo("Status", false), false),
		"maybeStatus": fieldOf("maybeStatus", refTo("Status", true), false),
		"state":       fieldOf("state", refTo("State", false), true),
		"parent":      fieldOf("parent", refTo("Widget", true), false),
		"note":        note,
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
