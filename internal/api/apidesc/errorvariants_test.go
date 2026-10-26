package apidesc_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// TestBuildRefusesBadErrorVariants proves that the writer refuses a variant
// that it cannot write as a schema, and writes nothing then.
func TestBuildRefusesBadErrorVariants(t *testing.T) {
	t.Parallel()

	codes := []string{"a.b", "c.d"}
	arg := apidesc.ErrorArg{Name: "who", Type: apidesc.ArgString}

	tests := []struct {
		name     string
		codes    []string
		variants []apidesc.ErrorVariant
	}{
		{"no codes", nil, []apidesc.ErrorVariant{{Code: "a.b", Args: []apidesc.ErrorArg{arg}}}},
		{"code not in the list", codes, []apidesc.ErrorVariant{{Code: "x.y", Args: []apidesc.ErrorArg{arg}}}},
		{"code twice", codes, []apidesc.ErrorVariant{
			{Code: "a.b", Args: []apidesc.ErrorArg{arg}}, {Code: "a.b", Args: []apidesc.ErrorArg{arg}},
		}},
		{"no argument", codes, []apidesc.ErrorVariant{{Code: "a.b", Args: nil}}},
		{"argument twice", codes, []apidesc.ErrorVariant{{Code: "a.b", Args: []apidesc.ErrorArg{arg, arg}}}},
		{
			"unknown type",
			codes,
			[]apidesc.ErrorVariant{{Code: "a.b", Args: []apidesc.ErrorArg{{Name: "who", Type: 0}}}},
		},
		{"name with a capital first", codes, []apidesc.ErrorVariant{
			{Code: "a.b", Args: []apidesc.ErrorArg{{Name: "Who", Type: apidesc.ArgString}}},
		}},
		{"name with a dot", codes, []apidesc.ErrorVariant{
			{Code: "a.b", Args: []apidesc.ErrorArg{{Name: "w.ho", Type: apidesc.ArgString}}},
		}},
		{"name with a newline", codes, []apidesc.ErrorVariant{
			{Code: "a.b", Args: []apidesc.ErrorArg{{Name: "who\n", Type: apidesc.ArgString}}},
		}},
		{
			"empty name",
			codes,
			[]apidesc.ErrorVariant{{Code: "a.b", Args: []apidesc.ErrorArg{{Name: "", Type: apidesc.ArgString}}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc, err := apidesc.Build(nil, apidesc.WithErrorCodes(tt.codes), apidesc.WithErrorVariants(tt.variants))
			if !errors.Is(err, apidesc.ErrInvalidBody) || doc != nil {
				t.Errorf("Build() = %d bytes, %v; want ErrInvalidBody and no bytes", len(doc), err)
			}
		})
	}
}

// TestBuildWritesErrorVariants proves the shape of a variant: one schema for
// the code, one for its arguments, the types of the three kinds, and a union
// that ends with the base schema.
func TestBuildWritesErrorVariants(t *testing.T) {
	t.Parallel()

	doc, err := apidesc.Build(
		nil,
		apidesc.WithErrorCodes([]string{"a.b", "c-d.e"}),
		apidesc.WithErrorVariants([]apidesc.ErrorVariant{{Code: "c-d.e", Args: []apidesc.ErrorArg{
			{Name: "count", Type: apidesc.ArgInteger},
			{Name: "since", Type: apidesc.ArgTime},
			{Name: "who", Type: apidesc.ArgString},
		}}}),
	)
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	var parsed variantDocument

	if err = json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	schemas := parsed.Components.Schemas

	if _, has := schemas["ABError"]; has {
		t.Error("a code with no arguments must have no variant")
	}

	args, ok := schemas["CDEErrorArgs"]["properties"].(map[string]any)
	if !ok {
		t.Fatalf("CDEErrorArgs = %v, want an object with properties", schemas["CDEErrorArgs"])
	}
	want := map[string]any{
		"count": map[string]any{"type": "integer"},
		"since": map[string]any{"type": "string", "format": "date-time"},
		"who":   map[string]any{"type": "string"},
	}

	if !reflect.DeepEqual(args, want) {
		t.Errorf("arguments = %v, want %v", args, want)
	}

	answer := schemas[apidesc.ErrorAnswerSchema]
	branches, ok := answer["oneOf"].([]any)
	if !ok {
		t.Fatalf("%s = %v, want a oneOf", apidesc.ErrorAnswerSchema, answer)
	}

	if len(branches) != 2 ||
		!reflect.DeepEqual(branches[1], map[string]any{"$ref": "#/components/schemas/ErrorResponse"}) {
		t.Errorf("oneOf = %v, want the variant, then the base", branches)
	}
}

// TestBuildWritesNoUnionWithoutVariants proves that a description with codes
// and with no variant has no union, so that the shape is the same as before.
func TestBuildWritesNoUnionWithoutVariants(t *testing.T) {
	t.Parallel()

	doc, err := apidesc.Build(nil, apidesc.WithErrorCodes([]string{"a.b"}))
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	var parsed variantDocument

	if err = json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if _, has := parsed.Components.Schemas[apidesc.ErrorAnswerSchema]; has {
		t.Error("a description with no variant must have no union")
	}
}

// variantDocument is the part of the description that these tests read.
type variantDocument struct {
	Components variantComponents `json:"components"`
}

type variantComponents struct {
	Schemas map[string]map[string]any `json:"schemas"`
}
