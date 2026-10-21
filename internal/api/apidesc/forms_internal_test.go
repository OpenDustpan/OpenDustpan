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
