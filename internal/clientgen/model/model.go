// Package model holds the model of an API description that the client
// generator reads: operations, parameters, bodies and schemas.
//
// The model holds only what a template needs. It holds no OpenAPI syntax, so
// a template does not parse JSON pointers or $ref strings. Every list has a
// fixed order and the model holds no Go map, so a template that ranges over
// the model gives the same bytes on each run.
package model

// API is one API description.
type API struct {
	// Title is the title of the description.
	Title string
	// Version is the version of the description, for example 0.1.0. It is
	// not the version of the server.
	Version string
	// Tags are the groups of operations, in the order of their names.
	Tags []Tag
	// Operations are in the order of their IDs.
	Operations []Operation
	// Schemas are the named schemas, in the order of their names. The error
	// schema is one of them.
	Schemas []Schema
	// Error is the name of the schema of each error answer.
	Error string
}

// Tag is one group of operations.
type Tag struct {
	Name        string
	Description string
}

// Method is an HTTP method in capital letters.
type Method string

// The methods that the description allows.
const (
	MethodGet    Method = "GET"
	MethodHead   Method = "HEAD"
	MethodPost   Method = "POST"
	MethodPut    Method = "PUT"
	MethodPatch  Method = "PATCH"
	MethodDelete Method = "DELETE"
)

// IfMatch says how an operation takes the If-Match header.
type IfMatch int

// The ways an operation takes If-Match.
const (
	// IfMatchNone means that the operation does not take If-Match.
	IfMatchNone IfMatch = iota
	// IfMatchOptional means that the operation takes If-Match and works
	// without it.
	IfMatchOptional
	// IfMatchRequired means that the operation answers 428 without
	// If-Match.
	IfMatchRequired
)

// Operation is one operation of the description.
type Operation struct {
	// ID is the operationId in lowerCamelCase. A template names the method
	// of the client from it.
	ID string
	// Method is the HTTP method.
	Method Method
	// Path is the path template, for example
	// /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}.
	Path string
	// Tags are the tags of the operation. The first tag names its group.
	Tags []string
	// Summary and Description are text for the documentation comment.
	Summary     string
	Description string
	// Actions are the authorization actions that the operation needs.
	Actions []string
	// Public is true for an operation that needs no sign-in.
	Public bool
	// Parameters are the path, query and header parameters, in the order
	// of the path, then of their wire names. The shared Idempotency-Key and
	// If-Match parameters are not in this list. The flags below hold them.
	Parameters []Parameter
	// Body is the request body, or nil for an operation with no body.
	Body *Body
	// Result is the success answer.
	Result Response
	// IdempotencyKey is true for an operation that takes the
	// Idempotency-Key header.
	IdempotencyKey bool
	// IfMatch says how the operation takes If-Match.
	IfMatch IfMatch
	// Page is set for a list operation that pages with a cursor, else nil.
	Page *Page
}

// In is the place of a parameter in a request.
type In string

// The places of a parameter.
const (
	InPath   In = "path"
	InQuery  In = "query"
	InHeader In = "header"
)

// Parameter is one parameter of an operation.
type Parameter struct {
	// Name is the name on the wire, for example unit_id or pageToken.
	Name string
	// In is the place of the parameter.
	In In
	// Required is true for a parameter that the request must hold. A path
	// parameter is always required.
	Required bool
	// Type is the type of the value. A parameter has a scalar type or an
	// array of a scalar type.
	Type Type
	// Description is text for the documentation comment.
	Description string
}

// MediaType is the media type of a request body.
type MediaType string

// The media types of a request body.
const (
	MediaJSON       MediaType = "application/json"
	MediaMergePatch MediaType = "application/merge-patch+json"
)

// Body is the request body of an operation.
type Body struct {
	MediaType MediaType
	Required  bool
	Type      Type
}

// Response is the success answer of an operation.
type Response struct {
	// Statuses are the status codes that the success answer can have, in
	// ascending order, for example [204] or [200, 201]. A client accepts
	// these codes only.
	Statuses []int
	// Type is the type of the body, or nil for an answer with no body.
	Type *Type
	// ETag is true for an answer that holds the ETag header.
	ETag bool
}

// Page names the parts of a list operation that pages with a cursor.
type Page struct {
	// SizeParameter is the query parameter of the page size, for example
	// pageSize.
	SizeParameter string
	// TokenParameter is the query parameter of the cursor, for example
	// pageToken.
	TokenParameter string
	// ItemsField is the field of the answer that holds the items.
	ItemsField string
	// NextField is the field of the answer that holds the next cursor. It
	// is empty on the last page.
	NextField string
}

// SchemaKind is the kind of a named schema.
type SchemaKind int

// The kinds of a named schema.
const (
	// KindObject is an object with named fields.
	KindObject SchemaKind = iota + 1
	// KindEnum is a string with a list of known values.
	KindEnum
	// KindUnion is a choice between objects. The value of one member, the
	// discriminator, says which object it is. A value that no variant names
	// has the shape of the base.
	KindUnion
)

// Schema is one named schema of the description.
type Schema struct {
	// Name is the name of the schema in UpperCamelCase.
	Name string
	Kind SchemaKind
	// Description is text for the documentation comment.
	Description string
	// Fields are the fields of an object, in the order of their wire names.
	Fields []Field
	// Values are the known values of an enum, in the order of the
	// description.
	Values []string
	// Open is true for an enum that can get new values. A client keeps a
	// value that it does not know and does not fail.
	Open bool
	// Extra is the type of the additional properties of an object, or nil
	// for an object that declares none. A client ignores an unknown field in
	// each case.
	Extra *Type
	// Discriminator is the wire name of the member of a union that selects
	// the variant.
	Discriminator string
	// Variants are the choices of a union, in the order of their values.
	Variants []Variant
	// Base is the name of the schema that a union uses for a value that no
	// variant names.
	Base string
	// Union is the name of the union that the schema is a variant of, or
	// the arguments of a variant of. It is empty for any other schema. A
	// template set that writes no union skips these schemas.
	Union string
}

// Variant is one choice of a union.
type Variant struct {
	// Value is the value of the discriminator for this choice.
	Value string
	// Schema is the name of the object schema of this choice.
	Schema string
}

// Field is one field of an object.
type Field struct {
	// Name is the name on the wire, in camelCase.
	Name string
	Type Type
	// Required is true for a field that the object always holds.
	Required bool
	// Description is text for the documentation comment.
	Description string
}

// TypeKind is the kind of a type.
type TypeKind int

// The kinds of a type.
const (
	TypeString TypeKind = iota + 1
	TypeInteger
	TypeNumber
	TypeBoolean
	// TypeArray is a list. Elem holds the type of the items.
	TypeArray
	// TypeMap is an object with free keys. Elem holds the type of the
	// values.
	TypeMap
	// TypeRef is a named schema. Ref holds its name.
	TypeRef
	// TypeAny is any JSON value.
	TypeAny
	// TypeUnion is a choice between two or more primitive kinds, in the
	// order of the description. Members holds the kinds.
	TypeUnion
)

// Type is the type of a field, a parameter or a body.
type Type struct {
	Kind TypeKind
	// Format is the format of a string or a number, for example date-time
	// or int64, or empty.
	Format string
	// Elem is the type of the items of an array or of the values of a map.
	Elem *Type
	// Ref is the name of the schema of a TypeRef.
	Ref string
	// Const is the one value of a string that has only one allowed value, or
	// empty. A union variant holds its discriminator in it.
	Const string
	// Nullable is true for a value that can be JSON null. A client keeps
	// absent, null and a value apart where the body is a merge patch.
	Nullable bool
	// Members are the kinds of a TypeUnion, in the order of the
	// description. Each kind is a primitive kind and no kind repeats. It is
	// nil for any other kind.
	Members []TypeKind
}
