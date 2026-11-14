package apidesc

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestCheckFormsAllowsTwoForms sends schemas straight to the check that runs
// on the finished schemas, as if the declaration check and nullable had
// failed. It allows the open enum and the nullable reference only.
func TestCheckFormsAllowsTwoForms(t *testing.T) {
	t.Parallel()

	const (
		enum = `"Kind":{"type":"string","enum":["a"]},`
		ref  = `{"$ref":"#/components/schemas/Kind"}`
	)

	tests := []struct {
		name   string
		schema string
		ok     bool
	}{
		{"open enum", `{"anyOf":[` + ref + `,{"type":"string"}]}`, true},
		{"open enum or null", `{"anyOf":[` + ref + `,{"type":"string"},{"type":"null"}]}`, true},
		{"nullable reference", `{"oneOf":[` + ref + `,{"type":"null"}]}`, true},
		{"no composition", `{"type":"string"}`, true},
		{"a property named oneOf", `{"properties":{"oneOf":{"type":"string"},"anyOf":{"type":"string"}}}`, true},
		{"oneOf of two types", `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, false},
		{"oneOf of two references", `{"oneOf":[` + ref + `,` + ref + `]}`, false},
		{"oneOf of an inline schema and null", `{"oneOf":[{"type":"object"},{"type":"null"}]}`, false},
		{"oneOf with null first", `{"oneOf":[{"type":"null"},` + ref + `]}`, false},
		{"oneOf of three", `{"oneOf":[` + ref + `,` + ref + `,{"type":"null"}]}`, false},
		{"oneOf with one branch", `{"oneOf":[` + ref + `]}`, false},
		{
			"oneOf with a reference that has members",
			`{"oneOf":[{"$ref":"#/components/schemas/Kind","type":"object"},{"type":"null"}]}`,
			false,
		},
		{"anyOf of two types", `{"anyOf":[{"type":"string"},{"type":"integer"}]}`, false},
		{"anyOf with the string first", `{"anyOf":[{"type":"string"},` + ref + `]}`, false},
		{"anyOf with an inline enum", `{"anyOf":[{"type":"string","enum":["a"]},{"type":"string"}]}`, false},
		{
			"anyOf with a string that has members",
			`{"anyOf":[` + ref + `,{"type":"string","format":"date-time"}]}`,
			false,
		},
		{
			"anyOf with a reference to an object",
			`{"anyOf":[{"$ref":"#/components/schemas/Shape"},{"type":"string"}]}`,
			false,
		},
		{
			"anyOf with a reference to nothing",
			`{"anyOf":[{"$ref":"#/components/schemas/Gone"},{"type":"string"}]}`,
			false,
		},
		{"anyOf of three with no null", `{"anyOf":[` + ref + `,{"type":"string"},{"type":"integer"}]}`, false},
		{"anyOf with two nulls", `{"anyOf":[` + ref + `,{"type":"string"},{"type":"null"},{"type":"null"}]}`, false},
		{"anyOf with one branch", `{"anyOf":[` + ref + `]}`, false},
		{"anyOf that is empty", `{"anyOf":[]}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text := `{` + enum + `"Shape":{"type":"object"},"Holder":{"type":"object","properties":{"v":` + tt.schema + `}}}`

			var schemas object
			if err := json.Unmarshal([]byte(text), &schemas); err != nil {
				t.Fatalf("bad test schema: %v", err)
			}

			r := newReflector()
			r.schemas = schemas

			err := r.checkForms()
			if tt.ok && err != nil {
				t.Errorf("checkForms() = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, ErrInvalidBody) {
				t.Errorf("checkForms() = %v, want ErrInvalidBody", err)
			}
		})
	}
}

// TestCheckTypeListsRefusesWhatTheDeclarationRefuses sends type members
// straight to the check that runs on the finished schemas, as if the
// declaration check had failed.
func TestCheckTypeListsRefusesWhatTheDeclarationRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema string
		ok     bool
	}{
		{"a type", `{"type":"string"}`, true},
		{"a nullable type", `{"type":["string","null"]}`, true},
		{"a nullable type with a format", `{"type":["string","null"],"format":"date-time"}`, true},
		{"a nullable object", `{"type":["object","null"]}`, true},
		{"a nullable array", `{"type":["array","null"]}`, true},
		{"a list of primitives", `{"type":["string","number","boolean"]}`, true},
		{"a list with null first", `{"type":["null","integer"]}`, false},
		{"a list with an unknown name and null", `{"type":["text","null"]}`, false},
		{"a list with null and an unknown name", `{"type":["null","text"]}`, false},
		{"a type list with an enum", `{"type":["string","number"],"enum":["a"]}`, false},
		{"a type list with a format", `{"type":["string","number"],"format":"date-time"}`, false},
		{"a list with an object", `{"type":["object","string"]}`, false},
		{"a list with an array", `{"type":["array","string"]}`, false},
		{"a list with an object and an array", `{"type":["object","array"]}`, false},
		{"an empty list", `{"type":[]}`, false},
		{"a list with one name", `{"type":["string"]}`, false},
		{"a list with a name twice", `{"type":["string","string"]}`, false},
		{"a list with an unknown name", `{"type":["text","number"]}`, false},
		{"a list with a name that is not a string", `{"type":[1,"string"]}`, false},
		{"a list with null beside two names", `{"type":["string","number","null"]}`, false},
		{"a type that is a number", `{"type":3}`, false},
		{
			"a property named type",
			`{"type":"object","properties":{"type":{"type":"string"}}}`,
			true,
		},
		{
			"a property named type with an open enum",
			`{"type":"object","properties":{"type":{"anyOf":[{"$ref":"#/components/schemas/Kind"},{"type":"string"}]}}}`,
			true,
		},
		{
			"a bad type below a property named items",
			`{"type":"object","properties":{"items":{"type":["object","string"]}}}`,
			false,
		},
		{
			"a bad type in the items of an array",
			`{"type":"array","items":{"type":["string","string"]}}`,
			false,
		},
		{
			"a bad type in a branch",
			`{"anyOf":[{"$ref":"#/components/schemas/Kind"},{"type":["text","number"]}]}`,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text := `{"Holder":{"type":"object","properties":{"v":` + tt.schema + `}}}`

			var schemas object
			if err := json.Unmarshal([]byte(text), &schemas); err != nil {
				t.Fatalf("bad test schema: %v", err)
			}

			r := newReflector()
			r.schemas = schemas

			err := r.checkTypeLists()
			if tt.ok && err != nil {
				t.Errorf("checkTypeLists() = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, ErrInvalidBody) {
				t.Errorf("checkTypeLists() = %v, want ErrInvalidBody", err)
			}
		})
	}
}

// TestNullableRefusesWhatItCannotMakeNull sends schemas straight to nullable.
func TestNullableRefusesWhatItCannotMakeNull(t *testing.T) {
	t.Parallel()

	null := map[string]any{"type": "null"}

	for name, in := range map[string]object{
		"a oneOf with no null":     {keyOneOf: []any{object{keyType: typeString}, object{keyType: typeInteger}}},
		"a schema with no type":    {keyDescription: "any"},
		"an empty oneOf":           {keyOneOf: []any{}},
		"a oneOf that starts null": {keyOneOf: []any{null, object{keyType: typeString}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, err := nullable(in); !errors.Is(err, errDeclared) {
				t.Errorf("nullable() = %v, %v; want errDeclared", got, err)
			}
		})
	}

	t.Run("a oneOf with a null keeps its null", func(t *testing.T) {
		t.Parallel()

		in := object{keyOneOf: []any{object{keyRef: refSchemas + "A"}, null}}

		got, err := nullable(in)
		if err != nil {
			t.Fatalf("nullable() = %v", err)
		}

		list, ok := got[keyOneOf].([]any)
		if !ok || len(list) != 2 || !isNullBranch(list[1]) {
			t.Errorf("nullable() = %v, want the oneOf with its null", got)
		}
	})
}
