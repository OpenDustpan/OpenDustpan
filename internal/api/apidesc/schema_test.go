package apidesc_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
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
			"labels":{"type":"object","additionalProperties":{"type":"string"}},
			"nested":{"type":"object","additionalProperties":{"$ref":"#/components/schemas/Inner"}},
			"free":{"type":"object","additionalProperties":true},
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
			"an embedded pointer to a struct is flat",
			apidesc.Typed[embeddingPointer]("EmbeddingPointer"),
			`{"EmbeddingPointer":{"type":"object","required":["id","own","shown"],"properties":{
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
