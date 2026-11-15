package apidesc

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"slices"
)

// ErrInvalidBody means that an operation names no body type where it needs
// one, names a body that its method cannot send, or names a Go type that the
// reflector cannot turn into a schema of the subset.
var ErrInvalidBody = errors.New("apidesc: body is not valid")

// Object kinds of the actions of an operation. A kind is the kind of the
// object that the route authorizes its action on. The set is the one of the
// objects of the authorization core: a namespace, a unit, a collection, the
// caller and the installation.
const (
	KindNamespace  = "namespace"
	KindUnit       = "unit"
	KindCollection = "collection"
	KindCaller     = "caller"
	KindServer     = "server"
)

// validObjectKind reports whether the kind is on the allowlist of object kinds.
func validObjectKind(kind string) bool {
	switch kind {
	case KindNamespace, KindUnit, KindCollection, KindCaller, KindServer:
		return true
	default:
		return false
	}
}

// bodyKind tells the three states of a Body.
type bodyKind int

const (
	// bodyUnset is the zero value: the route names nothing. Build refuses it.
	bodyUnset bodyKind = iota
	// bodyNone means that the route has no body, and says so.
	bodyNone
	// bodyTyped means that the route names a Go type.
	bodyTyped
)

// Body names the Go type of a request or of a response, or says that the
// route has no body. The zero value says nothing, and Build refuses it, so a
// route cannot leave its body out by mistake.
type Body struct {
	kind bodyKind
	// name is the name of the schema in components.schemas: UpperCamelCase.
	name string
	typ  reflect.Type
	// declared holds the schemas that the reflector may not derive: the types
	// with their own JSON form.
	declared []Declaration
}

// Bodies holds the body of the request and the body of the success answer of
// a route.
type Bodies struct {
	Request  Body
	Response Body
}

// bodies returns the bodies of the operation, with both unset for an operation
// that names none.
func (o Operation) bodies() Bodies {
	var b Bodies
	if o.Bodies != nil {
		b = *o.Bodies
	}

	return b
}

// NoBody is the explicit mark of a route with no request body or with no
// response body.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var NoBody = Body{kind: bodyNone, name: "", typ: nil, declared: nil}

// Typed returns the body of a route whose JSON is the encoding of T. The name
// is the name of the schema, for example CreateCollectionRequest. A schema
// has an explicit name, so that a rename of a Go type never renames a type
// in a client. The declarations cover each type in T with its own JSON form.
func Typed[T any](name string, declared ...Declaration) Body {
	return Body{kind: bodyTyped, name: name, typ: reflect.TypeFor[T](), declared: declared}
}

// Free returns the body of a route whose JSON object has members that the
// description does not list, for example the document of the description
// itself. The schema is an object with any member.
func Free(name string) Body {
	return Body{kind: bodyTyped, name: name, typ: reflect.TypeFor[freeObject](), declared: nil}
}

// freeObject is the type of Free: a JSON object with any member.
type freeObject map[string]any

// IsNone reports whether the body is the explicit mark of no body.
func (b Body) IsNone() bool { return b.kind == bodyNone }

// IsSet reports whether the route said anything about this body.
func (b Body) IsSet() bool { return b.kind != bodyUnset }

// Name returns the name of the schema, or "" for a body with no type.
func (b Body) Name() string { return b.name }

// Declaration changes the schema of a Go type: it states the schema of a type
// with its own JSON form (a type with MarshalJSON or MarshalText, or an
// interface), gives a type the name of its schema, or states the description
// of one field of a struct.
type Declaration struct {
	typ    reflect.Type
	schema object
	// enum is the name and the values of an open enum, or nil.
	enum *openEnum
	// name is the schema name that Named gives, and empty for the other
	// kinds.
	name string
	// as is the type that stands for this type on the wire, or nil.
	as reflect.Type
	// describe is the description of one field of a struct, or nil.
	describe *fieldDescription
}

// fieldDescription names one field of a struct type and the text of its
// description.
type fieldDescription struct {
	name string
	text string
}

// openEnum is the declared value set of a named string type.
type openEnum struct {
	name   string
	values []string
}

// Named gives the struct type T the name of its schema, in place of its Go
// name. A body names its own type. Use Named for a type that sits inside a
// body, when its Go name is not the name that a client should show.
func Named[T any](name string) Declaration {
	return Declaration{typ: reflect.TypeFor[T](), schema: nil, enum: nil, name: name, as: nil, describe: nil}
}

// Declare states the schema of the type T. The schema is a map in the form of
// the subset, and Build checks it: only the members of the subset are allowed.
func Declare[T any](schema map[string]any) Declaration {
	return Declaration{typ: reflect.TypeFor[T](), schema: schema, enum: nil, name: "", as: nil, describe: nil}
}

// DescribeField states the description of one field of the struct type T, in
// the schema of T. jsonName is the wire name of the field: the name in its
// json tag, or its Go name. The field must exist in the schema of T, and the
// text must not be empty. Two different texts for one field are refused.
//
// The text of the field wins over the description of the type of the field.
// The text of a field whose schema is a reference stands next to the $ref, as
// the only member beside it: {"$ref": "...", "description": "..."}. The
// reflector writes no described reference today. The subset holds description
// already, and the form allOf of the reference would add allOf to the subset,
// so the text beside the $ref is the form with the smaller change.
func DescribeField[T any](jsonName, text string) Declaration {
	return Declaration{
		typ:      reflect.TypeFor[T](),
		schema:   nil,
		enum:     nil,
		name:     "",
		as:       nil,
		describe: &fieldDescription{name: jsonName, text: text},
	}
}

// DeclareAs states that the JSON of T is the JSON of W, a type that is only a
// plain struct. It is the form for a type with MarshalJSON that writes a
// shape the description can show: W mirrors that shape. VerifyAs proves in a
// test that W and T agree.
func DeclareAs[T, W any]() Declaration {
	return Declaration{
		typ: reflect.TypeFor[T](), schema: nil, enum: nil, name: "", as: reflect.TypeFor[W](), describe: nil,
	}
}

// DeclareString states that T is a JSON string. It is the form of a type with
// MarshalText.
func DeclareString[T any]() Declaration {
	return Declare[T](object{keyType: typeString})
}

// DeclareOpenEnum states that T is a string whose value set can grow. The
// description holds the schema name with the values that exist today, and a
// use of T is anyOf the schema and any string, so that a client keeps a value
// it does not know. A value that is listed twice is refused.
func DeclareOpenEnum[T ~string](name string, values ...string) Declaration {
	return Declaration{
		typ:      reflect.TypeFor[T](),
		schema:   nil,
		enum:     &openEnum{name: name, values: slices.Clone(values)},
		name:     "",
		as:       nil,
		describe: nil,
	}
}

var (
	schemaName = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,63}$`)
	// wireField is camelCase with an acronym as one word, so
	// unitId and never unitID. A capital letter always has a small letter or a
	// digit after it.
	wireField = regexp.MustCompile(`^[a-z][a-z0-9]*([A-Z][a-z0-9]+)*$`)
	enumValue = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*([._:-][a-zA-Z0-9]+)*$`)
)

// legacyFieldNames lists the wire names of fields that break the camelCase
// rule and that a released console reads. The reflector accepts exactly these
// names and refuses any other name that breaks the rule. A name leaves the
// list in the change that renames the field.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant sets.
var legacyFieldNames = map[string]string{
	"unitID":    "The inventory, context and job APIs spell the stable ID of a unit unitID, and the console reads it.",
	"versionID": "The jobs API spells the ID of the closing state version versionID.",
	"poolID": "The jobs API spells the ID of the runner pool poolID, because the tagliatelle goCamel " +
		"rule of the linter demands it.",
	"appID": "The Git host API spells the ID of the GitHub App appID, because the tagliatelle goCamel " +
		"rule of the linter demands it.",
	"hookID": "The Git host API spells the ID of the webhook hookID, because the tagliatelle goCamel " +
		"rule of the linter demands it.",
	"installationID": "The Git host API spells the ID of a GitHub App installation installationID, " +
		"because the tagliatelle goCamel rule of the linter demands it.",
	"setID": "The Git host API spells the ID of a credential set setID, because the tagliatelle " +
		"goCamel rule of the linter demands it.",
	"issuerURL": "The issuer API spells the issuerURL setting of a namespace issuerURL, because the tagliatelle " +
		"goCamel rule of the linter demands it.",
	"effectiveIssuerURL": "The issuer API spells the issuer URL that a namespace uses effectiveIssuerURL, " +
		"because the tagliatelle goCamel rule of the linter demands it.",
	"objectID": "The error argument of the answer for a replayed secret spells the ID of the object objectID, " +
		"as the catalog of error codes declares it, and the console reads it.",
}

// LegacyFieldNames returns a copy of the list of wire names that break the
// camelCase rule but stay until their fields are renamed: the name and the
// reason.
func LegacyFieldNames() map[string]string {
	return maps.Clone(legacyFieldNames)
}

// ValidateBodies returns ErrInvalidBody, wrapped, when the operation leaves a
// body unmarked or gives a request body to a method that sends none. A body is
// marked when it names a Go type or says NoBody. It does not reflect the types:
// Build does, and it refuses a type that has no schema in the subset.
func (o Operation) ValidateBodies() error {
	where := o.Method + " " + o.Path
	b := o.bodies()

	if !b.Response.IsSet() {
		return fmt.Errorf(
			"%w: %s names no response type; use apidesc.NoBody for an answer with no body", ErrInvalidBody, where,
		)
	}

	sends := sendsBody(o.Method)

	switch {
	case sends && !b.Request.IsSet():
		return fmt.Errorf(
			"%w: %s names no request type; use apidesc.NoBody for a request with no body", ErrInvalidBody, where,
		)
	case !sends && b.Request.IsSet() && !b.Request.IsNone():
		return fmt.Errorf("%w: %s sends no request body, but names the type %s", ErrInvalidBody, where, b.Request.name)
	}

	return nil
}

// sendsBody reports whether a request of the method can hold a body.
func sendsBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}
