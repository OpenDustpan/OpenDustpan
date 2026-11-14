package apidesc_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// schemasOf builds a document with one POST operation whose request is the
// body, and returns components.schemas of the document.
func schemasOf(t *testing.T, body apidesc.Body) (map[string]any, error) {
	t.Helper()

	o := withBodies(op("POST", "/api/dustpan/v1/things", "thing:write"), body, apidesc.NoBody)

	doc, err := apidesc.Build([]apidesc.Operation{o})
	if err != nil {
		return nil, err
	}

	var parsed componentsDoc
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}

	delete(parsed.Components.Schemas, "ErrorResponse")

	return parsed.Components.Schemas, nil
}

// componentsDoc is the components object of a document.
type componentsDoc struct {
	Components schemaSet `json:"components"`
}

type schemaSet struct {
	Schemas map[string]any `json:"schemas"`
}

// asMap returns the value as a JSON object, and fails the test when it is not
// one.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()

	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%v is not a JSON object", v)
	}

	return m
}

// wantSchemas decodes an expected schema map.
func wantSchemas(t *testing.T, text string) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("bad expectation: %v", err)
	}

	return out
}

type scalars struct {
	Text    string  `json:"text"`
	Flag    bool    `json:"flag"`
	Small   int32   `json:"small"`
	Big     int64   `json:"big"`
	Count   int     `json:"count"`
	Ratio   float64 `json:"ratio"`
	Single  float32 `json:"single"`
	Unsized uint64  `json:"unsized"`
}

type inner struct {
	Name string `json:"name"`
}

type pointers struct {
	Text  *string `json:"text"`
	Count *int64  `json:"count,omitempty"`
	Inner *inner  `json:"inner"`
	Plain inner   `json:"plain"`
}

type collections struct {
	Names  []string          `json:"names"`
	Inners []inner           `json:"inners"`
	Labels map[string]string `json:"labels"`
	Nested map[string]inner  `json:"nested"`
	Free   map[string]any    `json:"free"`
	Raw    []byte            `json:"raw"`
	Fixed  [2]int32          `json:"fixed"`
}

// nilMaps holds the map shapes that the JSON tags alone decide: a nil map
// without omitempty is written as null, and a nil map with omitempty is left
// out.
type nilMaps struct {
	Plain   map[string]string `json:"plain"`
	Omitted map[string]string `json:"omitted,omitempty"`
}

// taggedMaps holds the map shapes that the dustpan tags decide: the required
// and optional tags promise a value that is never null, and the nullable tag
// lets the value be null.
type taggedMaps struct {
	Required map[string]string `dustpan:"required" json:"required"`
	Optional map[string]string `dustpan:"optional" json:"optional"`
	Nullable map[string]string `dustpan:"nullable" json:"nullable"`
}

type omitting struct {
	Always  string    `json:"always"`
	Empty   string    `json:"empty,omitempty"`
	Zero    time.Time `json:"zero,omitzero"`
	Skipped string    `json:"-"`
	hidden  string    //nolint:unused // The test proves that an unexported field is left out.
}

type times struct {
	At *time.Time `json:"at"`
	On time.Time  `json:"on"`
}

type base struct {
	ID    string `json:"id"`
	Shown string `json:"shown"`
}

type embedding struct {
	base

	Own string `json:"own"`
}

type embeddingPointer struct {
	*base

	Own string `json:"own"`
}

type embeddingTagged struct {
	base `json:"base"`
}

// requiredInner holds a required field that the two embeddings below promote.
type requiredInner struct {
	Name string `dustpan:"required" json:"name"`
}

// requiredThroughPointer promotes a required field through an embedded pointer.
// A nil pointer leaves the field out, so the tag cannot hold.
type requiredThroughPointer struct {
	*requiredInner
}

// embeddedRequired promotes the required field through an embedded struct,
// which cannot be nil, so the tag holds.
type embeddedRequired struct {
	requiredInner

	Own string `dustpan:"required" json:"own"`
}

type shallow struct {
	base

	Shown string `json:"shown"`
}

type left struct{ Twin string }

type right struct{ Twin string }

type bothSides struct {
	left
	right

	Other string `json:"other"`
}

type tree struct {
	Name     string  `json:"name"`
	Children []*tree `json:"children"`
}

type request struct {
	Name   *string `dustpan:"required" json:"name"`
	Reason *string `dustpan:"required" json:"reason,omitempty"`
	Query  *inner  `dustpan:"optional" json:"query"`
	Reset  *string `dustpan:"nullable" json:"reset,omitempty"`
	Plain  *string `dustpan:"optional" json:"plain,omitempty"`
}

type twoWords struct {
	Name *string `dustpan:"required,nullable" json:"name"`
}

type badDustpanTag struct {
	Name *string `dustpan:"requird" json:"name"`
}

type shout struct {
	UnitID string `json:"unitID"`
}

// The types of the probes below. Each has a shape where encoding/json decides
// what the wire holds, and the schema has to agree.

type diamondD struct {
	Shared string `json:"shared"`
}

type diamondB struct{ diamondD }

type diamondC struct{ diamondD }

type diamond struct {
	diamondB
	diamondC

	Other string `json:"other"`
}

// diamondShallow reaches the shared type twice, but it has a field of that name
// at a lesser depth, which wins.
type diamondShallow struct {
	diamondB
	diamondC

	Shared string `json:"shared"`
}

// diamondTriple reaches the shared type three times.
type diamondTriple struct {
	diamondB
	diamondC
	diamondA
}

type diamondA struct{ diamondD }

// diamondDeep reaches the shared type twice through two levels of embedding.
type diamondDeep struct {
	diamondUp
	diamondDown
}

type diamondUp struct{ diamondB }

type diamondDown struct{ diamondC }

type pointerInner struct {
	Inner string  `json:"inner"`
	Opt   *string `json:"opt"`
}

type nilEmbed struct {
	*pointerInner

	Own string `json:"own"`
}

type pointerHolder struct{ *pointerInner }

// nilEmbedNested has the pointer one level below the embedded struct.
type nilEmbedNested struct {
	pointerHolder

	Own string `json:"own"`
}

// wireProbe proves a schema against encoding/json. The sparse value is the
// zero value, which has each pointer nil, and the full value has each pointer
// set. The schema has to require only members that the sparse value writes,
// and it has to list exactly the members that the full value writes.
func wireProbe[T any](t *testing.T, name string, full T) {
	t.Helper()

	var sparse T

	schemas, err := schemasOf(t, apidesc.Typed[T](name))
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	schema := asMap(t, schemas[name])
	properties, _ := optional[map[string]any](schema, "properties")
	required, _ := optional[[]any](schema, "required")

	sparseKeys := wireKeys(t, sparse)
	fullKeys := wireKeys(t, full)

	for _, item := range required {
		member, isString := item.(string)
		if !isString {
			t.Fatalf("%s: %v in required is not a string", name, item)
		}

		if _, ok := sparseKeys[member]; !ok {
			t.Errorf("%s: %q is required, but json.Marshal(%+v) leaves it out", name, member, sparse)
		}
	}

	for key := range fullKeys {
		if _, ok := properties[key]; !ok {
			t.Errorf("%s: json.Marshal writes %q, and the schema has no such property", name, key)
		}
	}

	for key := range properties {
		if _, ok := fullKeys[key]; !ok {
			t.Errorf("%s: the schema lists %q, and json.Marshal never writes it", name, key)
		}
	}
}

// wireKeys returns the members of the JSON object that json.Marshal writes.
func wireKeys(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%+v) = %v", v, err)
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("%s is not an object: %v", raw, err)
	}

	return keys
}

// TestReflectorAgreesWithTheEncoder compares the schema of the embedding
// shapes with the JSON that encoding/json writes for them.
func TestReflectorAgreesWithTheEncoder(t *testing.T) {
	t.Parallel()

	text := "text"
	d := diamondD{Shared: "x"}

	t.Run("a diamond writes nothing of the shared type", func(t *testing.T) {
		t.Parallel()

		full := diamond{diamondB: diamondB{d}, diamondC: diamondC{d}, Other: "x"}
		if _, leaked := wireKeys(t, full)["shared"]; leaked {
			t.Fatal("the probe is wrong: json.Marshal writes the shared member of a diamond")
		}

		wireProbe(t, "Diamond", full)
	})

	t.Run("a shallow field wins over a diamond", func(t *testing.T) {
		t.Parallel()

		wireProbe(t, "DiamondShallow", diamondShallow{diamondB: diamondB{d}, diamondC: diamondC{d}, Shared: "x"})
	})

	t.Run("three embeddings of one type write nothing of it", func(t *testing.T) {
		t.Parallel()

		wireProbe(t, "DiamondTriple", diamondTriple{
			diamondB: diamondB{d},
			diamondC: diamondC{d},
			diamondA: diamondA{d},
		})
	})

	t.Run("two levels of embedding on both sides", func(t *testing.T) {
		t.Parallel()

		wireProbe(t, "DiamondDeep", diamondDeep{
			diamondUp:   diamondUp{diamondB{d}},
			diamondDown: diamondDown{diamondC{d}},
		})
	})

	t.Run("a nil embedded pointer writes none of its fields", func(t *testing.T) {
		t.Parallel()

		var sparse nilEmbed
		if len(wireKeys(t, sparse)) != 1 {
			t.Fatal("the probe is wrong: json.Marshal writes the fields of a nil embedded pointer")
		}

		wireProbe(t, "NilEmbed", nilEmbed{pointerInner: &pointerInner{Inner: "x", Opt: &text}, Own: "x"})
	})

	t.Run("a nil pointer below an embedded struct", func(t *testing.T) {
		t.Parallel()

		wireProbe(t, "NilEmbedNested", nilEmbedNested{
			pointerHolder: pointerHolder{&pointerInner{Inner: "x", Opt: &text}},
			Own:           "x",
		})
	})
}

func TestReflectorShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body apidesc.Body
		want string
	}{
		{
			"scalars",
			apidesc.Typed[scalars]("Scalars"),
			`{"Scalars":{"type":"object","required":["big","count","flag","ratio","single","small","text","unsized"],
			"properties":{
			"text":{"type":"string"},
			"flag":{"type":"boolean"},
			"small":{"type":"integer","format":"int32"},
			"big":{"type":"integer","format":"int64"},
			"count":{"type":"integer","format":"int64"},
			"ratio":{"type":"number","format":"double"},
			"single":{"type":"number","format":"float"},
			"unsized":{"type":"integer","format":"uint64"}}}}`,
		},
		{
			"pointers are nullable, and a pointer to a struct is a nullable reference",
			apidesc.Typed[pointers]("Pointers"),
			`{"Pointers":{"type":"object","required":["inner","plain","text"],"properties":{
			"text":{"type":["string","null"]},
			"count":{"type":"integer","format":"int64"},
			"inner":{"oneOf":[{"$ref":"#/components/schemas/Inner"},{"type":"null"}]},
			"plain":{"$ref":"#/components/schemas/Inner"}}},
			"Inner":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}`,
		},
		{
			"slices, maps and bytes",
			apidesc.Typed[collections]("Collections"),
			`{"Collections":{"type":"object","required":["fixed","free","inners","labels","names","nested","raw"],"properties":{
			"names":{"type":"array","items":{"type":"string"}},
			"inners":{"type":"array","items":{"$ref":"#/components/schemas/Inner"}},
			"labels":{"type":["object","null"],"additionalProperties":{"type":"string"}},
			"nested":{"type":["object","null"],"additionalProperties":{"$ref":"#/components/schemas/Inner"}},
			"free":{"type":["object","null"],"additionalProperties":true},
			"raw":{"type":"string","format":"byte"},
			"fixed":{"type":"array","items":{"type":"integer","format":"int32"}}}},
			"Inner":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}`,
		},
		{
			"omitempty, omitzero, a dash and an unexported field",
			apidesc.Typed[omitting]("Omitting"),
			`{"Omitting":{"type":"object","required":["always"],"properties":{
			"always":{"type":"string"},
			"empty":{"type":"string"},
			"zero":{"type":"string","format":"date-time"}}}}`,
		},
		{
			"time.Time is a date-time string",
			apidesc.Typed[times]("Times"),
			`{"Times":{"type":"object","required":["at","on"],"properties":{
			"at":{"type":["string","null"],"format":"date-time"},
			"on":{"type":"string","format":"date-time"}}}}`,
		},
		{
			"an embedded struct is flat",
			apidesc.Typed[embedding]("Embedding"),
			`{"Embedding":{"type":"object","required":["id","own","shown"],"properties":{
			"id":{"type":"string"},"shown":{"type":"string"},"own":{"type":"string"}}}}`,
		},
		{
			"an embedded pointer to a struct is flat, and its fields are optional",
			apidesc.Typed[embeddingPointer]("EmbeddingPointer"),
			`{"EmbeddingPointer":{"type":"object","required":["own"],"properties":{
			"id":{"type":"string"},"shown":{"type":"string"},"own":{"type":"string"}}}}`,
		},
		{
			"an embedded struct with a tag is a field",
			apidesc.Typed[embeddingTagged]("EmbeddingTagged"),
			`{"EmbeddingTagged":{"type":"object","required":["base"],"properties":{
			"base":{"$ref":"#/components/schemas/Base"}}},
			"Base":{"type":"object","required":["id","shown"],"properties":{
			"id":{"type":"string"},"shown":{"type":"string"}}}}`,
		},
		{
			"the shallow field wins over the embedded one",
			apidesc.Typed[shallow]("Shallow"),
			`{"Shallow":{"type":"object","required":["id","shown"],"properties":{
			"shown":{"type":"string"},"id":{"type":"string"}}}}`,
		},
		{
			"two fields of one name at one depth are both dropped",
			apidesc.Typed[bothSides]("BothSides"),
			`{"BothSides":{"type":"object","required":["other"],"properties":{"other":{"type":"string"}}}}`,
		},
		{
			"a type that two embeddings reach at one depth is dropped",
			apidesc.Typed[diamond]("Diamond"),
			`{"Diamond":{"type":"object","required":["other"],"properties":{"other":{"type":"string"}}}}`,
		},
		{
			"a recursive type ends",
			apidesc.Typed[tree]("Tree"),
			`{"Tree":{"type":"object","required":["children","name"],"properties":{
			"name":{"type":"string"},
			"children":{"type":"array","items":{"oneOf":[{"$ref":"#/components/schemas/Tree"},{"type":"null"}]}}}}}`,
		},
		{
			"a free object",
			apidesc.Free("Anything"),
			`{"Anything":{"type":"object","additionalProperties":true}}`,
		},
		{
			"a required pointer is neither nullable nor optional, and a pointer that omitempty leaves out is never null",
			apidesc.Typed[request]("Request"),
			`{"Request":{"type":"object","required":["name","reason"],"properties":{
			"name":{"type":"string"},
			"reason":{"type":"string"},
			"query":{"$ref":"#/components/schemas/Inner"},
			"reset":{"type":["string","null"]},
			"plain":{"type":"string"}}},
			"Inner":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}`,
		},
		{
			"a legacy wire name stays until its field is renamed",
			apidesc.Typed[shout]("Shout"),
			`{"Shout":{"type":"object","required":["unitID"],"properties":{"unitID":{"type":"string"}}}}`,
		},
		{
			"the map tags decide the nullability: required and optional are never null, and nullable can be null",
			apidesc.Typed[taggedMaps]("TaggedMaps"),
			`{"TaggedMaps":{"type":"object","required":["required"],"properties":{
			"required":{"type":"object","additionalProperties":{"type":"string"}},
			"optional":{"type":"object","additionalProperties":{"type":"string"}},
			"nullable":{"type":["object","null"],"additionalProperties":{"type":"string"}}}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := schemasOf(t, tt.body)
			if err != nil {
				t.Fatalf("Build() = %v", err)
			}

			if want := wantSchemas(t, tt.want); !reflect.DeepEqual(got, want) {
				t.Errorf("schemas =\n%s\nwant\n%s", jsonOf(got), jsonOf(want))
			}
		})
	}
}

// TestNilMapFollowsTheNullabilityRule proves the rule for a map field in both
// places that matter: the JSON that encoding/json writes and the schema. A nil
// map without omitempty is null on the wire, so its schema holds null too. A
// nil map with omitempty is left out, so its schema does not hold null.
func TestNilMapFollowsTheNullabilityRule(t *testing.T) {
	t.Parallel()

	t.Run("a nil map without omitempty is null on the wire", func(t *testing.T) {
		t.Parallel()

		var zero nilMaps

		raw, err := json.Marshal(zero)
		if err != nil {
			t.Fatalf("json.Marshal = %v", err)
		}

		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("%s is not an object: %v", raw, err)
		}

		if got := string(wire["plain"]); got != "null" {
			t.Errorf("plain = %s, want null", got)
		}

		if _, ok := wire["omitted"]; ok {
			t.Errorf("%s holds omitted, and omitempty leaves a nil map out", raw)
		}
	})

	t.Run("the schema follows the same rule", func(t *testing.T) {
		t.Parallel()

		got, err := schemasOf(t, apidesc.Typed[nilMaps]("NilMaps"))
		if err != nil {
			t.Fatalf("Build() = %v", err)
		}

		want := wantSchemas(t, `{"NilMaps":{"type":"object","required":["plain"],"properties":{
		"plain":{"type":["object","null"],"additionalProperties":{"type":"string"}},
		"omitted":{"type":"object","additionalProperties":{"type":"string"}}}}}`)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("schemas =\n%s\nwant\n%s", jsonOf(got), jsonOf(want))
		}
	})
}

// own is a type with its own JSON form.
type own struct{ v string }

//nolint:unparam // The method implements json.Marshaler.
func (o own) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(o.v)), nil }

// ptrOwn has the method on the pointer, which encoding/json uses for a value
// that it can address.
type ptrOwn struct{ v string }

//nolint:revive,unparam // The test proves that a pointer receiver counts as a marshaler.
func (o *ptrOwn) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(o.v)), nil }

type texty string

//nolint:unparam // The method implements encoding.TextMarshaler.
func (o texty) MarshalText() ([]byte, error) { return []byte(o), nil }

type holdsOwn struct {
	Value own `json:"value"`
}

// holdsOwnPointer is a struct whose field is a pointer to a declared type.
type holdsOwnPointer struct {
	Value *own `json:"value"`
}

type holdsPtrOwn struct {
	Value ptrOwn `json:"value"`
}

type holdsTexty struct {
	Value texty `json:"value"`
}

type holdsRaw struct {
	Value json.RawMessage `json:"value"`
}

type holdsAny struct {
	Value any `json:"value"`
}

type promoted struct {
	own

	Other string `json:"other"`
}

type holdsFunc struct {
	Call func() `json:"call"`
}

type holdsIntKeys struct {
	Counts map[int]string `json:"counts"`
}

type snake struct {
	UnitName string `json:"unit_name"` //nolint:tagliatelle // The test needs a name that breaks the rule.
}

type kebab struct {
	UnitName string `json:"unit-name"` //nolint:tagliatelle // The test needs a name that breaks the rule.
}

type pascal struct {
	UnitName string
}

type acronym struct {
	URL string `json:"consoleURL"`
}

type stringOption struct {
	Count int64 `json:"count,string"`
}

type generic[T any] struct {
	Item T `json:"item"`
}

type holdsGeneric struct {
	Page generic[string] `json:"page"`
}

func TestReflectorRefusesWhatItCannotTell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body apidesc.Body
	}{
		{"MarshalJSON without a declaration", apidesc.Typed[holdsOwn]("HoldsOwn")},
		{"MarshalJSON on the root type", apidesc.Typed[own]("Own")},
		{"MarshalJSON on a pointer receiver", apidesc.Typed[holdsPtrOwn]("HoldsPtrOwn")},
		{"MarshalText without a declaration", apidesc.Typed[holdsTexty]("HoldsTexty")},
		{"json.RawMessage without a declaration", apidesc.Typed[holdsRaw]("HoldsRaw")},
		{"an interface without a declaration", apidesc.Typed[holdsAny]("HoldsAny")},
		{"a marshaler promoted by an embedded field", apidesc.Typed[promoted]("Promoted")},
		{"a func", apidesc.Typed[holdsFunc]("HoldsFunc")},
		{"a map with integer keys", apidesc.Typed[holdsIntKeys]("HoldsIntKeys")},
		{"a snake_case name", apidesc.Typed[snake]("Snake")},
		{"a kebab-case name", apidesc.Typed[kebab]("Kebab")},
		{"a PascalCase name", apidesc.Typed[pascal]("Pascal")},
		{"an acronym in a name", apidesc.Typed[acronym]("Acronym")},
		{"the string option", apidesc.Typed[stringOption]("StringOption")},
		{"a dustpan tag that is not required", apidesc.Typed[badDustpanTag]("BadDustpanTag")},
		{"a dustpan tag with two words", apidesc.Typed[twoWords]("TwoWords")},
		{"a generic type without a name", apidesc.Typed[holdsGeneric]("HoldsGeneric")},
		{"a schema name in lowerCamelCase", apidesc.Typed[inner]("inner")},
		{"a schema name with a separator", apidesc.Typed[inner]("Inner_Name")},
		{"the name of the error shape", apidesc.Typed[inner]("ErrorResponse")},
		{"an empty schema name", apidesc.Typed[inner]("")},
		{
			"a declaration outside the subset",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{"type": "string", "pattern": "x"})),
		},
		{
			"an open enum value outside the allowlist",
			apidesc.Typed[inner]("Inner", apidesc.DeclareOpenEnum[texty]("Kind", "Bad Value")),
		},
		{
			"an open enum with no value",
			apidesc.Typed[inner]("Inner", apidesc.DeclareOpenEnum[texty]("Kind")),
		},
		{
			"an open enum with a value twice",
			apidesc.Typed[withKind]("WithKind", apidesc.DeclareOpenEnum[kindName]("KindName", "onCall", "onCall")),
		},
		{
			"a type list with an object name",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"object", "string"},
			})),
		},
		{
			"a type list with an array name",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"array", "string"},
			})),
		},
		{
			"an empty type list",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{"type": []any{}})),
		},
		{
			"a type list with one name",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{"type": []any{"string"}})),
		},
		{
			"a type list with a name twice",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"string", "string"},
			})),
		},
		{
			"a type list with an unknown name",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"text", "number"},
			})),
		},
		{
			"a type list with a name that is not a string",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{1, "string"},
			})),
		},
		{
			"a type list with null beside two names",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"string", "number", "null"},
			})),
		},
		{
			"a type list with null first",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"null", "string"},
			})),
		},
		{
			"a type list with an unknown name and null",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"text", "null"},
			})),
		},
		{
			"a type list with an enum",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"string", "number"}, "enum": []any{"a"},
			})),
		},
		{
			"a type list with a format",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"string", "number"}, "format": "date-time",
			})),
		},
		{
			"a type that is a number",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{"type": 3})),
		},
		{
			"a type list with several names behind a pointer",
			apidesc.Typed[holdsOwnPointer]("HoldsOwnPointer", apidesc.Declare[own](map[string]any{
				"type": []any{"string", "number"},
			})),
		},
		{
			"a description of an unknown field",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.DescribeField[holdsOwn]("other", "Text.")),
		},
		{
			"a description that is empty",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.DescribeField[holdsOwn]("value", "")),
		},
		{
			"two descriptions of one field",
			apidesc.Typed[holdsOwn](
				"HoldsOwn",
				apidesc.DescribeField[holdsOwn]("value", "One."),
				apidesc.DescribeField[holdsOwn]("value", "Two."),
			),
		},
		{
			"a description of a type that is not a struct",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.DescribeField[string]("value", "Text.")),
		},
		{
			"a description of a type with its own schema",
			apidesc.Typed[inner](
				"Inner",
				apidesc.Declare[inner](map[string]any{"type": "string"}),
				apidesc.DescribeField[inner]("name", "Text."),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := schemasOf(t, tt.body); !errors.Is(err, apidesc.ErrInvalidBody) {
				t.Fatalf("Build() = %v, want ErrInvalidBody", err)
			}
		})
	}
}

// TestRequiredThroughAnEmbeddedPointer proves that field discovery refuses the
// tag required on a field that an embedded pointer promotes, because a nil
// pointer leaves the field out, and that an ordinary required field and a
// required field that an embedded struct promotes stay valid.
func TestRequiredThroughAnEmbeddedPointer(t *testing.T) {
	t.Parallel()

	t.Run("the tag required is refused with a reason", func(t *testing.T) {
		t.Parallel()

		_, err := schemasOf(t, apidesc.Typed[requiredThroughPointer]("RequiredThroughPointer"))
		if !errors.Is(err, apidesc.ErrInvalidBody) {
			t.Fatalf("Build() = %v, want ErrInvalidBody", err)
		}

		for _, want := range []string{"required", "embedded pointer"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Build() = %v, want an error that names %q", err, want)
			}
		}
	})

	t.Run("ordinary required fields and an embedded struct stay valid", func(t *testing.T) {
		t.Parallel()

		got, err := schemasOf(t, apidesc.Typed[embeddedRequired]("EmbeddedRequired"))
		if err != nil {
			t.Fatalf("Build() = %v", err)
		}

		want := wantSchemas(t, `{"EmbeddedRequired":{"type":"object","required":["name","own"],"properties":{
		"name":{"type":"string"},"own":{"type":"string"}}}}`)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("schemas =\n%s\nwant\n%s", jsonOf(got), jsonOf(want))
		}
	})
}

type choice string

type holdsChoice struct {
	Choice choice `json:"choice"`
}

type holdsChoicePointer struct {
	Choice *choice `json:"choice"`
}

type holdsChoiceList struct {
	Choices []*choice `json:"choices"`
}

type holdsUntyped struct {
	Value *own `json:"value"`
}

type doublePointers struct {
	Deep *map[string]string `json:"deep"`
	Ref  **inner            `json:"ref"`
	Kind **kindName         `json:"kind"`
}

// TestBuildRefusesFormsOutsideTheSubset proves that the only anyOf and oneOf
// in a description are the open enum and the nullable reference. A declaration
// cannot write another form, and a pointer cannot make a declared schema
// nullable in another form.
func TestBuildRefusesFormsOutsideTheSubset(t *testing.T) {
	t.Parallel()

	str := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer"}
	null := map[string]any{"type": "null"}
	ref := map[string]any{"$ref": "#/components/schemas/Inner"}

	declare := func(schema map[string]any) apidesc.Body {
		return apidesc.Typed[holdsChoice]("HoldsChoice", apidesc.Declare[choice](schema))
	}

	tests := []struct {
		name string
		body apidesc.Body
	}{
		{"a declared oneOf of two types", declare(map[string]any{"oneOf": []any{str, integer}})},
		{"a declared oneOf with a null", declare(map[string]any{"oneOf": []any{str, null}})},
		{"a declared oneOf of a reference and null", declare(map[string]any{"oneOf": []any{ref, null}})},
		{"a declared anyOf of two types", declare(map[string]any{"anyOf": []any{str, integer}})},
		{"a declared open enum form", declare(map[string]any{"anyOf": []any{ref, str}})},
		{"a declared anyOf with a type", declare(map[string]any{"type": "string", "anyOf": []any{str}})},
		{"a declared oneOf in a property", declare(map[string]any{
			"type":       "object",
			"properties": map[string]any{"a": map[string]any{"oneOf": []any{str, integer}}},
		})},
		{"a declared oneOf in the items", declare(map[string]any{
			"type": "array", "items": map[string]any{"oneOf": []any{str, integer}},
		})},
		{"a declared oneOf in the values of a map", declare(map[string]any{
			"type": "object", "additionalProperties": map[string]any{"anyOf": []any{str, integer}},
		})},
		{"a declared oneOf that is not a list", declare(map[string]any{"oneOf": "string"})},
		{
			"a pointer to a declared oneOf",
			apidesc.Typed[holdsChoicePointer]("HoldsChoicePointer", apidesc.Declare[choice](map[string]any{
				"oneOf": []any{str, integer},
			})),
		},
		{
			"a pointer to a declared schema with no type",
			apidesc.Typed[holdsChoicePointer]("HoldsChoicePointer", apidesc.Declare[choice](map[string]any{
				"description": "any value",
			})),
		},
		{
			"a list of pointers to a declared schema with no type",
			apidesc.Typed[holdsChoiceList]("HoldsChoiceList", apidesc.Declare[choice](map[string]any{
				"description": "any value",
			})),
		},
		{
			"a pointer to a type that declares no type",
			apidesc.Typed[holdsUntyped]("HoldsUntyped", apidesc.Declare[own](map[string]any{"description": "text"})),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := schemasOf(t, tt.body); !errors.Is(err, apidesc.ErrInvalidBody) {
				t.Fatalf("Build() = %v, want ErrInvalidBody", err)
			}
		})
	}
}

// TestBuildChecksDeclaredTypeAndRequired proves that the declared check reads
// the values of type and required: a type name of the subset, a list of them
// and a list of strings pass at the top and at each depth, and any other value
// is refused.
func TestBuildChecksDeclaredTypeAndRequired(t *testing.T) {
	t.Parallel()

	declare := func(schema map[string]any) apidesc.Body {
		return apidesc.Typed[holdsChoice]("HoldsChoice", apidesc.Declare[choice](schema))
	}
	property := func(child map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{"name": child}}
	}

	tests := []struct {
		name    string
		body    apidesc.Body
		wantErr bool
	}{
		{"an object type", declare(map[string]any{"type": "object"}), false},
		{"an array type", declare(map[string]any{"type": "array", "items": map[string]any{"type": "string"}}), false},
		{"a string type", declare(map[string]any{"type": "string"}), false},
		{"an integer type", declare(map[string]any{"type": "integer"}), false},
		{"a number type", declare(map[string]any{"type": "number"}), false},
		{"a boolean type", declare(map[string]any{"type": "boolean"}), false},
		{"a null type", declare(map[string]any{"type": "null"}), false},
		{"a type list", declare(map[string]any{"type": []any{"string", "null"}}), false},
		{
			"a required list",
			declare(map[string]any{
				"type": "object", "required": []any{"name"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			}),
			false,
		},
		{
			"a valid schema in a property",
			declare(property(map[string]any{
				"type": "object", "required": []any{"inner"},
				"properties": map[string]any{"inner": map[string]any{"type": "integer"}},
			})),
			false,
		},
		{
			"a valid type in the values of a map",
			declare(map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "boolean"}}),
			false,
		},
		{"a misspelled type", declare(map[string]any{"type": "strnig"}), true},
		{"a type that is not a string", declare(map[string]any{"type": 5}), true},
		{"an empty type list", declare(map[string]any{"type": []any{}}), true},
		{
			"a type list with a value that is not a string",
			declare(map[string]any{"type": []any{"string", 3}}),
			true,
		},
		{
			"a type list with a name that is not in the subset",
			declare(map[string]any{"type": []any{"string", "text"}}),
			true,
		},
		{"a misspelled type in a property", declare(property(map[string]any{"type": "strnig"})), true},
		{"a type that is not a string in a property", declare(property(map[string]any{"type": 5})), true},
		{
			"a misspelled type in the items",
			declare(map[string]any{"type": "array", "items": map[string]any{"type": "strnig"}}),
			true,
		},
		{
			"a misspelled type in the values of a map",
			declare(map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "strnig"}}),
			true,
		},
		{"a required that is not a list", declare(map[string]any{"required": "name"}), true},
		{"a required that is an object", declare(map[string]any{"required": map[string]any{"name": true}}), true},
		{"a required with a value that is not a string", declare(map[string]any{"required": []any{"name", 3}}), true},
		{"a required that is not a list in a property", declare(property(map[string]any{"required": "name"})), true},
		{
			"a required with a value that is not a string in a property",
			declare(property(map[string]any{"required": []any{"name", 3}})),
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := schemasOf(t, tt.body)

			if tt.wantErr {
				if !errors.Is(err, apidesc.ErrInvalidBody) {
					t.Fatalf("Build() = %v, want ErrInvalidBody", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Build() = %v, want no error", err)
			}
		})
	}
}

// TestBuildNamesTheBadTypeAndRequired proves that the refusal of a declared
// type or required value names the member and the value.
func TestBuildNamesTheBadTypeAndRequired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body apidesc.Body
		want string
	}{
		{
			"a misspelled type",
			apidesc.Typed[holdsChoice]("HoldsChoice", apidesc.Declare[choice](map[string]any{"type": "strnig"})),
			`member "type" is the type name "strnig"`,
		},
		{
			"a required with a value that is not a string",
			apidesc.Typed[holdsChoice]("HoldsChoice", apidesc.Declare[choice](
				map[string]any{"required": []any{"name", 3}},
			)),
			`member "required" holds the value 3`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := schemasOf(t, tt.body)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Build() = %v, want an error that holds %q", err, tt.want)
			}
		})
	}
}

// TestNullableKeepsNull proves that a pointer to a pointer is as nullable as a
// pointer: the second pointer never drops the null branch of a reference, of an
// open enum or of a type list.
func TestNullableKeepsNull(t *testing.T) {
	t.Parallel()

	got, err := schemasOf(t, apidesc.Typed[doublePointers](
		"DoublePointers", apidesc.DeclareOpenEnum[kindName]("KindName", "onCall"),
	))
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	want := wantSchemas(t, `{
	"KindName":{"type":"string","enum":["onCall"]},
	"Inner":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}},
	"DoublePointers":{"type":"object","required":["deep","kind","ref"],"properties":{
	"deep":{"type":["object","null"],"additionalProperties":{"type":"string"}},
	"ref":{"oneOf":[{"$ref":"#/components/schemas/Inner"},{"type":"null"}]},
	"kind":{"anyOf":[{"$ref":"#/components/schemas/KindName"},{"type":"string"},{"type":"null"}]}}}}`)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("schemas =\n%s\nwant\n%s", jsonOf(got), jsonOf(want))
	}
}

func TestReflectorAcceptsDeclaredTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body apidesc.Body
		want string
	}{
		{
			"a declared MarshalJSON type",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.DeclareString[own]()),
			`{"HoldsOwn":{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}}`,
		},
		{
			"a declared pointer receiver",
			apidesc.Typed[holdsPtrOwn]("HoldsPtrOwn", apidesc.DeclareString[ptrOwn]()),
			`{"HoldsPtrOwn":{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}}`,
		},
		{
			"a declared MarshalText type",
			apidesc.Typed[holdsTexty]("HoldsTexty", apidesc.DeclareString[texty]()),
			`{"HoldsTexty":{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}}`,
		},
		{
			"a raw message declared as an object",
			apidesc.Typed[holdsRaw]("HoldsRaw", apidesc.Declare[json.RawMessage](
				map[string]any{"type": "object", "additionalProperties": true},
			)),
			`{"HoldsRaw":{"type":"object","required":["value"],"properties":{
			"value":{"type":"object","additionalProperties":true}}}}`,
		},
		{
			"an interface declared as an object",
			apidesc.Typed[holdsAny]("HoldsAny", apidesc.Declare[any](
				map[string]any{"type": "object", "additionalProperties": true},
			)),
			`{"HoldsAny":{"type":"object","required":["value"],"properties":{
			"value":{"type":"object","additionalProperties":true}}}}`,
		},
		{
			"a declared object with properties",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": "object", "required": []any{"v"},
				"properties": map[string]any{"v": map[string]any{"type": "string"}},
			})),
			`{"HoldsOwn":{"type":"object","required":["value"],"properties":{"value":{
			"type":"object","required":["v"],"properties":{"v":{"type":"string"}}}}}}`,
		},
		{
			"a declared type list",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"string", "number", "boolean"},
			})),
			`{"HoldsOwn":{"type":"object","required":["value"],"properties":{
			"value":{"type":["string","number","boolean"]}}}}`,
		},
		{
			"a declared type list with null",
			apidesc.Typed[holdsOwn]("HoldsOwn", apidesc.Declare[own](map[string]any{
				"type": []any{"string", "null"},
			})),
			`{"HoldsOwn":{"type":"object","required":["value"],"properties":{
			"value":{"type":["string","null"]}}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := schemasOf(t, tt.body)
			if err != nil {
				t.Fatalf("Build() = %v", err)
			}

			if want := wantSchemas(t, tt.want); !reflect.DeepEqual(got, want) {
				t.Errorf("schemas =\n%s", jsonOf(got))
			}
		})
	}
}

// describedRef is a struct with a field that is a reference.
type describedRef struct {
	Inner inner `json:"inner"`
}

// describedList is a struct with a list field and a nullable field.
type describedList struct {
	Names []string `json:"names"`
	Maybe *string  `json:"maybe"`
}

// TestReflectorWritesFieldDescriptions proves that a field takes its own
// description, that the text of the field wins over the description of its
// type, and that the text of a reference stands next to it.
func TestReflectorWritesFieldDescriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body apidesc.Body
		want string
	}{
		{
			"the text of the field wins over the text of the type",
			apidesc.Typed[holdsOwn](
				"HoldsOwn",
				apidesc.Declare[own](map[string]any{"type": "string", "description": "The type."}),
				apidesc.DescribeField[holdsOwn]("value", "The field."),
			),
			`{"HoldsOwn":{"type":"object","required":["value"],"properties":{
			"value":{"type":"string","description":"The field."}}}}`,
		},
		{
			"the text of a reference stands next to the $ref",
			apidesc.Typed[describedRef](
				"DescribedRef",
				apidesc.DescribeField[describedRef]("inner", "The inner object."),
			),
			`{"DescribedRef":{"type":"object","required":["inner"],"properties":{
			"inner":{"$ref":"#/components/schemas/Inner","description":"The inner object."}}},
			"Inner":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}`,
		},
		{
			"the text of a field of a nested struct",
			apidesc.Typed[describedRef]("DescribedRef", apidesc.DescribeField[inner]("name", "The name.")),
			`{"DescribedRef":{"type":"object","required":["inner"],"properties":{
			"inner":{"$ref":"#/components/schemas/Inner"}}},
			"Inner":{"type":"object","required":["name"],"properties":{"name":{"type":"string","description":"The name."}}}}`,
		},
		{
			"the text of a list field and of a nullable field",
			apidesc.Typed[describedList](
				"DescribedList",
				apidesc.DescribeField[describedList]("names", "The names."),
				apidesc.DescribeField[describedList]("maybe", "The optional text."),
			),
			`{"DescribedList":{"type":"object","required":["maybe","names"],"properties":{
			"names":{"type":"array","items":{"type":"string"},"description":"The names."},
			"maybe":{"type":["string","null"],"description":"The optional text."}}}}`,
		},
		{
			"the same text twice stays one text",
			apidesc.Typed[describedRef](
				"DescribedRef",
				apidesc.DescribeField[describedRef]("inner", "The inner object."),
				apidesc.DescribeField[describedRef]("inner", "The inner object."),
			),
			`{"DescribedRef":{"type":"object","required":["inner"],"properties":{
			"inner":{"$ref":"#/components/schemas/Inner","description":"The inner object."}}},
			"Inner":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := schemasOf(t, tt.body)
			if err != nil {
				t.Fatalf("Build() = %v", err)
			}

			if want := wantSchemas(t, tt.want); !reflect.DeepEqual(got, want) {
				t.Errorf("schemas =\n%s\nwant\n%s", jsonOf(got), jsonOf(want))
			}
		})
	}
}

type kindName string

type withKind struct {
	Kind  kindName   `json:"kind"`
	Maybe *kindName  `json:"maybe"`
	Many  []kindName `json:"many"`
}

func TestReflectorWritesAnOpenEnum(t *testing.T) {
	t.Parallel()

	body := apidesc.Typed[withKind](
		"WithKind",
		apidesc.DeclareOpenEnum[kindName]("KindName", "onCall", "awaiting-approval"),
	)

	got, err := schemasOf(t, body)
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	want := wantSchemas(t, `{
	"KindName":{"type":"string","enum":["onCall","awaiting-approval"]},
	"WithKind":{"type":"object","required":["kind","many","maybe"],"properties":{
	"kind":{"anyOf":[{"$ref":"#/components/schemas/KindName"},{"type":"string"}]},
	"maybe":{"anyOf":[{"$ref":"#/components/schemas/KindName"},{"type":"string"},{"type":"null"}]},
	"many":{"type":"array","items":{"anyOf":[{"$ref":"#/components/schemas/KindName"},{"type":"string"}]}}}}}`)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("schemas =\n%s", jsonOf(got))
	}
}

type twin struct{ A string }

func (twin) Foo() {}

type sameNameA struct {
	Item inner `json:"item"`
}

func TestReflectorNames(t *testing.T) {
	t.Parallel()

	t.Run("a type under two names is refused", func(t *testing.T) {
		t.Parallel()

		o := withBodies(
			op("POST", "/api/dustpan/v1/things", "thing:write"),
			apidesc.Typed[inner]("First"),
			apidesc.Typed[inner]("Second"),
		)

		if _, err := apidesc.Build([]apidesc.Operation{o}); !errors.Is(err, apidesc.ErrInvalidBody) {
			t.Fatalf("Build() = %v, want ErrInvalidBody", err)
		}
	})

	t.Run("a name for two types is refused", func(t *testing.T) {
		t.Parallel()

		o := withBodies(
			op("POST", "/api/dustpan/v1/things", "thing:write"),
			apidesc.Typed[inner]("Same"),
			apidesc.Typed[twin]("Same"),
		)

		if _, err := apidesc.Build([]apidesc.Operation{o}); !errors.Is(err, apidesc.ErrInvalidBody) {
			t.Fatalf("Build() = %v, want ErrInvalidBody", err)
		}
	})

	t.Run("a type that two routes share keeps one schema", func(t *testing.T) {
		t.Parallel()

		a := withBodies(
			op("POST", "/api/dustpan/v1/things", "thing:write"),
			apidesc.Typed[inner]("Thing"),
			apidesc.Typed[inner]("Thing"),
		)
		b := withBodies(
			op("PUT", "/api/dustpan/v1/things/{thing_id}", "thing:write"),
			apidesc.Typed[inner]("Thing"),
			apidesc.NoBody,
		)

		if _, err := apidesc.Build([]apidesc.Operation{a, b}); err != nil {
			t.Fatalf("Build() = %v", err)
		}
	})

	t.Run("a nested type takes the name of its own body", func(t *testing.T) {
		t.Parallel()

		o := withBodies(
			op("POST", "/api/dustpan/v1/things", "thing:write"),
			apidesc.Typed[sameNameA]("Wrapper"),
			apidesc.Typed[inner]("Member"),
		)

		doc, err := apidesc.Build([]apidesc.Operation{o})
		if err != nil {
			t.Fatalf("Build() = %v", err)
		}

		var parsed componentsDoc
		if err := json.Unmarshal(doc, &parsed); err != nil {
			t.Fatal(err)
		}

		wrapper := asMap(t, parsed.Components.Schemas["Wrapper"])
		props := asMap(t, wrapper["properties"])
		item := asMap(t, props["item"])

		if got := item["$ref"]; got != "#/components/schemas/Member" {
			t.Errorf("item = %v, want a reference to Member", got)
		}

		if _, ok := parsed.Components.Schemas["Inner"]; ok {
			t.Error("the schema Inner exists next to Member for one type")
		}
	})
}
