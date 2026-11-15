package clientgen_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// unionSchemas are the schemas of an error union with one variant. The tests
// put them in front of ErrorResponse in the base document.
const unionSchemas = `"ErrorAnswer": {
  "description": "d",
  "oneOf": [{"$ref": "#/components/schemas/LockError"}, {"$ref": "#/components/schemas/ErrorResponse"}],
  "discriminator": {"propertyName": "errorCode", "mapping": {"lock.locked": "#/components/schemas/LockError"}}},
 "LockError": {"type": "object", "required": ["code", "message", "errorCode", "errorArgs"],
  "properties": {"code": {"type": "integer"}, "message": {"type": "string"},
   "errorCode": {"type": "string", "enum": ["lock.locked"]},
   "errorArgs": {"$ref": "#/components/schemas/LockErrorArgs"}}},
 "LockErrorArgs": {"type": "object", "required": ["holder", "since"],
  "properties": {"holder": {"type": "string"}, "since": {"type": "string", "format": "date-time"}}},
 `

// unionDocument returns the base document with the union schemas, and with
// one text of them replaced.
func unionDocument(t *testing.T, old, replacement string) string {
	t.Helper()

	schemas := unionSchemas
	if old != "" {
		if !strings.Contains(schemas, old) {
			t.Fatalf("the union schemas have no %q", old)
		}

		schemas = strings.Replace(schemas, old, replacement, 1)
	}

	return string(change(t, `"ErrorResponse": {"type": "object"`, schemas+`"ErrorResponse": {"type": "object"`))
}

// TestParseReadsTheErrorUnion checks that the parser reads a union of the
// error schemas by errorCode: the variants, the base, the one-value errorCode
// of each variant, and the marks that let a template set skip the union.
func TestParseReadsTheErrorUnion(t *testing.T) {
	t.Parallel()

	api, err := clientgen.NewParser().Parse([]byte(unionDocument(t, "", "")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := map[string]model.Schema{}
	for _, s := range api.Schemas {
		got[s.Name] = s
	}

	checkUnion(t, got)
	checkVariantMarks(t, got)
}

// checkUnion checks the union itself.
func checkUnion(t *testing.T, got map[string]model.Schema) {
	t.Helper()

	union := got["ErrorAnswer"]
	if union.Kind != model.KindUnion || union.Discriminator != "errorCode" || union.Base != "ErrorResponse" ||
		!reflect.DeepEqual(union.Variants, []model.Variant{{Value: "lock.locked", Schema: "LockError"}}) {
		t.Errorf("union = %+v", union)
	}
}

// checkVariantMarks checks the marks of the variant, of its arguments and of
// the base, and the constant errorCode of the variant.
func checkVariantMarks(t *testing.T, got map[string]model.Schema) {
	t.Helper()

	if got["LockError"].Union != "ErrorAnswer" || got["LockErrorArgs"].Union != "ErrorAnswer" {
		t.Errorf("the variant and its arguments must belong to the union: %+v, %+v",
			got["LockError"], got["LockErrorArgs"])
	}

	if got["ErrorResponse"].Union != "" {
		t.Errorf("the base belongs to no union: %+v", got["ErrorResponse"])
	}

	fields := got["LockError"].Fields
	for i := range fields {
		f := &fields[i]
		if f.Name == "errorCode" && (f.Type.Kind != model.TypeString || f.Type.Const != "lock.locked") {
			t.Errorf("errorCode of the variant = %+v, want the string constant lock.locked", f.Type)
		}
	}
}

// TestParseRefusesAnErrorUnionOfAnotherShape checks each way in which a union
// can differ from the form that the parser reads, as hostile input.
func TestParseRefusesAnErrorUnionOfAnotherShape(t *testing.T) {
	t.Parallel()

	const (
		lockRef = `{"$ref": "#/components/schemas/LockError"}`
		baseRef = `{"$ref": "#/components/schemas/ErrorResponse"}`
	)

	tests := []struct {
		name, old, replacement string
	}{
		{
			"mapping names a stranger",
			`"lock.locked": "#/components/schemas/LockError"}}}`,
			`"lock.locked": "#/components/schemas/Thing"}}}`,
		},
		{"mapping is not a reference", `"lock.locked": "#/components/schemas/LockError"`, `"lock.locked": 1`},
		{"mapping is empty", `"mapping": {"lock.locked": "#/components/schemas/LockError"}`, `"mapping": {}`},
		{"unknown key in the discriminator", `"propertyName": "errorCode",`, `"propertyName": "errorCode", "x": 1,`},
		{"property name is not a field name", `"propertyName": "errorCode"`, `"propertyName": "Error Code"`},
		{
			"value is not an enum value",
			`"lock.locked": "#/components/schemas/LockError"}}}`,
			`"lock locked": "#/components/schemas/LockError"}}}`,
		},
		{"inline branch", lockRef + `, ` + baseRef, lockRef + `, {"type": "object"}`},
		{"branch in the list two times", lockRef + `, ` + baseRef, lockRef + `, ` + lockRef},
		{"no base branch", lockRef + `, ` + baseRef + `]`, lockRef + `]`},
		{"key beside the union", `"description": "d",`, `"description": "d", "type": "object",`},
		{"base is not the error schema", baseRef + `]`, `{"$ref": "#/components/schemas/Thing"}]`},
		{"variant without the discriminator", `"errorCode": {"type": "string", "enum": ["lock.locked"]},`, ``},
		{"variant with another value", `"enum": ["lock.locked"]`, `"enum": ["lock.other"]`},
		{
			"variant with a plain string",
			`"errorCode": {"type": "string", "enum": ["lock.locked"]}`,
			`"errorCode": {"type": "string"}`,
		},
		{"variant with two values", `"enum": ["lock.locked"]`, `"enum": ["lock.locked", "lock.other"]`},
		{
			"variant with an optional discriminator",
			`"required": ["code", "message", "errorCode", "errorArgs"]`,
			`"required": ["code", "message", "errorArgs"]`,
		},
		{
			"variant with additional properties",
			`"errorArgs": {"$ref": "#/components/schemas/LockErrorArgs"}}}`,
			`"errorArgs": {"$ref": "#/components/schemas/LockErrorArgs"}}, "additionalProperties": true}`,
		},
		{
			"enum of an integer with no name",
			`"holder": {"type": "string"}`,
			`"holder": {"type": "integer", "enum": [1]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := parseErr(unionDocument(t, tt.old, tt.replacement))
			if !errors.Is(err, clientgen.ErrInvalidDescription) && !errors.Is(err, clientgen.ErrUnsupported) &&
				!errors.Is(err, clientgen.ErrInvalidName) {
				t.Fatalf("parse = %v, want a refusal of the description", err)
			}
		})
	}
}

// TestParseReadsTheErrorUnionAsTheDefaultAnswer checks that the default
// answer of an operation may be the error union, that the error schema of the
// model stays the base, and that another schema is still refused.
func TestParseReadsTheErrorUnionAsTheDefaultAnswer(t *testing.T) {
	t.Parallel()

	const (
		base  = `"default": {"description": "err", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorResponse"}}}}`
		union = `"default": {"description": "err", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorAnswer"}}}}`
		thing = `"default": {"description": "err", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}}`
	)

	tests := []struct {
		name, answer string
		valid        bool
	}{
		{"the error schema", base, true},
		{"the error union", union, true},
		{"another schema", thing, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := strings.Replace(unionDocument(t, "", ""), base, tt.answer, 1)

			api, err := clientgen.NewParser().Parse([]byte(doc))
			if (err == nil) != tt.valid {
				t.Fatalf("parse = %v, want valid %v", err, tt.valid)
			}

			if tt.valid && api.Error != "ErrorResponse" {
				t.Errorf("the error schema = %q, want ErrorResponse", api.Error)
			}
		})
	}
}
