package apidesc

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// marshalerTypes returns the interfaces that give a type its own JSON form.
func marshalerTypes() []reflect.Type {
	return []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[encoding.TextMarshaler]()}
}

// timeType returns the type of time.Time, which the reflector knows.
func timeType() reflect.Type { return reflect.TypeFor[time.Time]() }

// Schema members and types that the reflector writes more than once.
const (
	keyProperties = "properties"
	keyItems      = "items"
	keyFormat     = "format"
	keyOneOf      = "oneOf"
	keyAnyOf      = "anyOf"
	keyEnum       = "enum"
	keyAdditional = "additionalProperties"
	typeInteger   = "integer"
	typeNumber    = "number"
	typeBoolean   = "boolean"
	typeArray     = "array"
	typeNull      = "null"
	formatInt32   = "int32"
	formatInt64   = "int64"
	formatUint64  = "uint64"
)

// reflector turns Go types into the schemas of components.schemas. It follows
// the rules of encoding/json for the names and the presence of fields, and it
// refuses what the rules cannot tell: a type with its own JSON form that no
// declaration covers.
type reflector struct {
	// schemas holds the finished schemas by name.
	schemas object
	// names maps a type to its schema name.
	names map[reflect.Type]string
	// owners maps a schema name to the type that owns it.
	owners   map[string]reflect.Type
	declared map[reflect.Type]Declaration
	// described maps a struct type to the descriptions of its fields, by the
	// wire name of each field.
	described map[reflect.Type]map[string]string
	// badNames lists every wire name that breaks the camelCase rule, so that
	// one run names all of them.
	badNames []string
}

func newReflector() *reflector {
	return &reflector{
		schemas:   object{},
		names:     map[reflect.Type]string{},
		owners:    map[string]reflect.Type{},
		declared:  map[reflect.Type]Declaration{},
		described: map[reflect.Type]map[string]string{},
		badNames:  nil,
	}
}

// declare records a declaration. Two different declarations of one type are an
// error, so that the schema of a type never depends on the order of the
// operations.
func (r *reflector) declare(d Declaration) error {
	if d.describe != nil {
		return r.describeField(d)
	}

	if prev, ok := r.declared[d.typ]; ok {
		if !reflect.DeepEqual(prev, d) {
			return fmt.Errorf("%w: type %s is declared twice with different schemas", ErrInvalidBody, d.typ)
		}

		return nil
	}

	// A declaration with a name only keeps the derived schema, and a field
	// description of the type stays valid. Every other declaration replaces
	// the derived schema, and a field description would be lost.
	if err := r.refuseOwnSchema(d); err != nil {
		return err
	}

	switch {
	case d.as != nil:
		// The wire type has to be plain: a marshaler on it would only move the
		// problem.
		if err := refuseMarshaler(d.as); err != nil {
			return err
		}
	case d.name != "":
		// A name only: the type keeps its derived schema.
		return r.reserve(d.name, d.typ)
	case d.enum != nil:
		if err := r.declareEnum(d); err != nil {
			return err
		}
	default:
		if err := checkDeclared(d.schema); err != nil {
			return fmt.Errorf("%w: declaration of %s: %w", ErrInvalidBody, d.typ, err)
		}
	}

	r.declared[d.typ] = d

	return nil
}

// refuseOwnSchema returns an error when the type has a field description and
// the declaration gives the type a schema of its own: the description would
// be lost, because the derived struct schema is never built.
func (r *reflector) refuseOwnSchema(d Declaration) error {
	if d.name == "" && len(r.described[d.typ]) > 0 {
		return fmt.Errorf(
			"%w: type %s has a field description and a declaration of its own schema",
			ErrInvalidBody, d.typ,
		)
	}

	return nil
}

// describeField records the description of one field of the struct type. The
// field is named by its wire name. The text must not be empty, the field must
// exist in the schema of the type, and two different texts for one field are
// refused. A type with a schema of its own has no derived fields, so it
// cannot take a field description.
func (r *reflector) describeField(d Declaration) error {
	fd := d.describe

	if fd.text == "" {
		return fmt.Errorf("%w: the description of the field %q of %s is empty", ErrInvalidBody, fd.name, d.typ)
	}

	if _, hasOwnSchema := r.declared[d.typ]; hasOwnSchema {
		return fmt.Errorf(
			"%w: type %s has a declaration of its own schema and a field description",
			ErrInvalidBody, d.typ,
		)
	}

	if d.typ.Kind() != reflect.Struct {
		return fmt.Errorf("%w: %s is not a struct, so it has no field %q", ErrInvalidBody, d.typ, fd.name)
	}

	fields, err := structFields(d.typ)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(fields, func(f field) bool { return f.name == fd.name }) {
		return fmt.Errorf("%w: type %s has no field %q", ErrInvalidBody, d.typ, fd.name)
	}

	if text, ok := r.described[d.typ][fd.name]; ok {
		if text != fd.text {
			return fmt.Errorf(
				"%w: the field %q of %s has the descriptions %q and %q",
				ErrInvalidBody, fd.name, d.typ, text, fd.text,
			)
		}

		return nil
	}

	if r.described[d.typ] == nil {
		r.described[d.typ] = map[string]string{}
	}

	r.described[d.typ][fd.name] = fd.text

	return nil
}

// declareEnum checks the values of an open enum and writes its schema.
func (r *reflector) declareEnum(d Declaration) error {
	e := d.enum
	if len(e.values) == 0 {
		return fmt.Errorf("%w: enum %s has no value", ErrInvalidBody, e.name)
	}

	values := make([]any, len(e.values))
	seen := make(map[string]bool, len(e.values))

	for i, v := range e.values {
		if !enumValue.MatchString(v) {
			return fmt.Errorf("%w: enum %s: value %q is not camelCase with separators", ErrInvalidBody, e.name, v)
		}

		if seen[v] {
			return fmt.Errorf("%w: enum %s: value %q is listed twice", ErrInvalidBody, e.name, v)
		}

		seen[v] = true
		values[i] = v
	}

	if err := r.reserve(e.name, d.typ); err != nil {
		return err
	}

	r.schemas[e.name] = object{keyType: typeString, keyEnum: values}

	return nil
}

// reserve gives the name to the type. It refuses a name that does not follow
// the rule, a name of another type, and a type with another name.
func (r *reflector) reserve(name string, t reflect.Type) error {
	if !schemaName.MatchString(name) {
		return fmt.Errorf("%w: schema name %q for %s is not UpperCamelCase", ErrInvalidBody, name, t)
	}

	if name == errorSchema || name == errorCodeSchema || name == ErrorAnswerSchema {
		return fmt.Errorf("%w: schema name %q belongs to the error shape", ErrInvalidBody, name)
	}

	if owner, ok := r.owners[name]; ok && owner != t {
		return fmt.Errorf("%w: schema name %q names the types %s and %s", ErrInvalidBody, name, owner, t)
	}

	if prev, ok := r.names[t]; ok && prev != name {
		return fmt.Errorf("%w: type %s has the schema names %q and %q", ErrInvalidBody, t, prev, name)
	}

	r.owners[name] = t
	r.names[t] = name

	return nil
}

// reflectBodies writes the schemas of the bodies. It gives every body its name
// first, so that a type that one body holds inside another keeps the name of
// its own body whatever the order is.
func (r *reflector) reflectBodies(bodies []Body) error {
	var typed []Body

	for _, b := range bodies {
		if b.kind != bodyTyped {
			continue
		}

		for _, d := range b.declared {
			if err := r.declare(d); err != nil {
				return err
			}
		}

		typed = append(typed, b)
	}

	for _, b := range typed {
		if err := r.reserve(b.name, b.typ); err != nil {
			return err
		}
	}

	for _, b := range typed {
		if err := r.defineOnce(b.name, b.typ); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidBody, err)
		}
	}

	if len(r.badNames) > 0 {
		slices.Sort(r.badNames)

		return fmt.Errorf("%w: these fields are not camelCase: %s", ErrInvalidBody, strings.Join(r.badNames, ", "))
	}

	return r.checkFinished()
}

// checkFinished checks the schemas when all of them are written: every
// reference has a schema, and every type list and every anyOf and oneOf has
// an allowed form.
func (r *reflector) checkFinished() error {
	if err := r.checkRefs(); err != nil {
		return err
	}

	if err := r.checkTypeLists(); err != nil {
		return err
	}

	return r.checkForms()
}

// ref returns the reference to the schema of the body.
func (b Body) ref() object { return object{keyRef: refSchemas + b.name} }

// schemaFor returns the schema that stands for the type at a place of use: a
// reference for a named struct, an inline schema for the rest.
func (r *reflector) schemaFor(t reflect.Type) (object, error) {
	if t.Kind() == reflect.Pointer {
		inner, err := r.schemaFor(t.Elem())
		if err != nil {
			return nil, err
		}

		out, err := nullable(inner)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidBody, t, err)
		}

		return out, nil
	}

	if d, ok := r.declared[t]; ok {
		if d.as != nil {
			return r.schemaFor(d.as)
		}

		return declaredUse(d), nil
	}

	if t == timeType() {
		return object{keyType: typeString, keyFormat: "date-time"}, nil
	}

	if t.Kind() == reflect.Struct && t.PkgPath() != "" && t.Name() != "" {
		return r.namedStructRef(t)
	}

	return r.define(t)
}

// namedStructRef defines the schema of a named struct type, unless it is
// there, and returns the reference to it.
func (r *reflector) namedStructRef(t reflect.Type) (object, error) {
	name, err := r.structName(t)
	if err != nil {
		return nil, err
	}

	if defineErr := r.defineOnce(name, t); defineErr != nil {
		return nil, defineErr
	}

	return object{keyRef: refSchemas + name}, nil
}

// declaredUse returns the schema for a use of a declared type: the declared
// schema, or for an open enum the open form.
func declaredUse(d Declaration) object {
	if d.enum == nil {
		return cloneSchema(d.schema)
	}

	return object{keyAnyOf: []any{object{keyRef: refSchemas + d.enum.name}, object{keyType: typeString}}}
}

// structName returns the schema name of a named struct type: the name that a
// body or Named gave, or the Go name with a capital first letter.
func (r *reflector) structName(t reflect.Type) (string, error) {
	if name, ok := r.names[t]; ok {
		return name, nil
	}

	goName := t.Name()
	if strings.ContainsAny(goName, "[]") {
		return "", fmt.Errorf("%w: type %s has no usable name; give it a name with Named", ErrInvalidBody, t)
	}

	name := strings.ToUpper(goName[:1]) + goName[1:]
	if err := r.reserve(name, t); err != nil {
		return "", err
	}

	return name, nil
}

// defineOnce writes the schema of the struct type under its name, unless it
// is there. The name is stored first, so that a type that refers to itself
// ends.
func (r *reflector) defineOnce(name string, t reflect.Type) error {
	if _, done := r.schemas[name]; done {
		return nil
	}

	// A placeholder keeps a type that refers to itself from running again.
	r.schemas[name] = object{}

	schema, err := r.define(t)
	if err != nil {
		return fmt.Errorf("schema %s: %w", name, err)
	}

	r.schemas[name] = schema

	return nil
}

// define returns the schema of the type itself, never a reference to it.
func (r *reflector) define(t reflect.Type) (object, error) {
	if t.Kind() == reflect.Pointer || t == timeType() {
		return r.schemaFor(t)
	}

	if d, ok := r.declared[t]; ok {
		return r.defineDeclared(d)
	}

	if err := refuseMarshaler(t); err != nil {
		return nil, err
	}

	if scalar, ok := scalarSchema(t.Kind()); ok {
		return scalar, nil
	}

	//nolint:exhaustive // The default case refuses every other kind.
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return r.array(t)
	case reflect.Map:
		return r.mapSchema(t)
	case reflect.Struct:
		return r.structSchema(t)
	case reflect.Interface:
		return nil, fmt.Errorf("%w: %s holds any JSON; declare its schema", ErrInvalidBody, t)
	default:
		return nil, fmt.Errorf("%w: %s has no JSON form", ErrInvalidBody, t)
	}
}

// defineDeclared returns the schema of a declared type.
func (r *reflector) defineDeclared(d Declaration) (object, error) {
	switch {
	case d.as != nil:
		return r.define(d.as)
	case d.enum != nil:
		return object{keyType: typeString}, nil
	default:
		return cloneSchema(d.schema), nil
	}
}

// scalarSchemas holds the schema of each kind of Go value that encoding/json
// writes as a JSON string, boolean or number.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var scalarSchemas = map[reflect.Kind]object{
	reflect.String:  {keyType: typeString},
	reflect.Bool:    {keyType: typeBoolean},
	reflect.Int8:    {keyType: typeInteger, keyFormat: formatInt32},
	reflect.Int16:   {keyType: typeInteger, keyFormat: formatInt32},
	reflect.Int32:   {keyType: typeInteger, keyFormat: formatInt32},
	reflect.Uint8:   {keyType: typeInteger, keyFormat: formatInt32},
	reflect.Uint16:  {keyType: typeInteger, keyFormat: formatInt32},
	reflect.Int:     {keyType: typeInteger, keyFormat: formatInt64},
	reflect.Int64:   {keyType: typeInteger, keyFormat: formatInt64},
	reflect.Uint32:  {keyType: typeInteger, keyFormat: formatInt64},
	reflect.Uint:    {keyType: typeInteger, keyFormat: formatUint64},
	reflect.Uint64:  {keyType: typeInteger, keyFormat: formatUint64},
	reflect.Uintptr: {keyType: typeInteger, keyFormat: formatUint64},
	reflect.Float32: {keyType: typeNumber, keyFormat: "float"},
	reflect.Float64: {keyType: typeNumber, keyFormat: "double"},
}

// scalarSchema returns a copy of the schema of a scalar kind.
func scalarSchema(kind reflect.Kind) (object, bool) {
	schema, ok := scalarSchemas[kind]
	if !ok {
		return nil, false
	}

	return cloneSchema(schema), true
}

// refuseMarshaler returns an error for a type with its own JSON form: the Go
// type does not tell the JSON that it writes. A pointer receiver counts too,
// because encoding/json uses it for a value it can address.
func refuseMarshaler(t reflect.Type) error {
	for _, c := range []reflect.Type{t, reflect.PointerTo(t)} {
		if slices.ContainsFunc(marshalerTypes(), c.Implements) {
			return fmt.Errorf(
				"%w: %s has its own JSON form (MarshalJSON or MarshalText); declare its schema", ErrInvalidBody, t,
			)
		}
	}

	return nil
}

// array returns the schema of a slice or an array. A slice of bytes is a
// string, as encoding/json writes it in base64. A slice is not nullable: the
// handlers answer an empty list, and a nil slice would be a bug.
func (r *reflector) array(t reflect.Type) (object, error) {
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 && refuseMarshaler(t.Elem()) == nil {
		return object{keyType: typeString, keyFormat: "byte"}, nil
	}

	items, err := r.schemaFor(t.Elem())
	if err != nil {
		return nil, err
	}

	return object{keyType: typeArray, keyItems: items}, nil
}

// mapSchema returns the schema of a map with string keys.
func (r *reflector) mapSchema(t reflect.Type) (object, error) {
	key := t.Key()
	if key.Kind() != reflect.String || refuseMarshaler(key) != nil {
		return nil, fmt.Errorf("%w: the keys of %s are not plain strings", ErrInvalidBody, t)
	}

	if _, declared := r.declared[t.Elem()]; t.Elem().Kind() == reflect.Interface && !declared {
		return object{keyType: typeObject, keyAdditional: true}, nil
	}

	value, err := r.schemaFor(t.Elem())
	if err != nil {
		return nil, err
	}

	return object{keyType: typeObject, keyAdditional: value}, nil
}

// nullable returns the schema of a value that can also be null. A reference
// becomes oneOf, an open enum gets a third branch, and any other schema takes
// the type list [X, "null"]. A type list that already holds null stays as it
// is, in the order of its declaration. It refuses a schema that none of these
// forms can make null without leaving the subset: a declared oneOf, a declared
// schema with no type, and a type list that holds no null and cannot take one.
func nullable(s object) (object, error) {
	null := object{keyType: typeNull}

	if s[keyRef] != nil {
		return object{keyOneOf: []any{s, null}}, nil
	}

	if branches, ok := s[keyAnyOf].([]any); ok {
		if endsWithNull(branches) {
			return s, nil
		}

		return object{keyAnyOf: append(slices.Clone(branches), null)}, nil
	}

	if branches, ok := s[keyOneOf].([]any); ok {
		// Only the nullable reference has a oneOf, and it holds null already.
		if endsWithNull(branches) {
			return s, nil
		}

		return nil, fmt.Errorf("%w: a oneOf with no null branch cannot be made nullable", errDeclared)
	}

	out := cloneSchema(s)

	switch typ := out[keyType].(type) {
	case string:
		out[keyType] = []any{typ, typeNull}
	case []any:
		if !slices.Contains(typ, any(typeNull)) {
			return nil, fmt.Errorf("%w: the type list %v cannot take null without leaving the subset", errDeclared, typ)
		}
	default:
		return nil, fmt.Errorf("%w: a schema with no type cannot be made nullable", errDeclared)
	}

	return out, nil
}

// endsWithNull reports whether the last branch of a list is the null type.
func endsWithNull(branches []any) bool {
	if len(branches) == 0 {
		return false
	}

	last, ok := branches[len(branches)-1].(object)

	return ok && len(last) == 1 && last[keyType] == typeNull
}

// structSchema returns the schema of a struct.
func (r *reflector) structSchema(t reflect.Type) (object, error) {
	fields, err := structFields(t)
	if err != nil {
		return nil, err
	}

	props := object{}

	var required []string

	for _, f := range fields {
		r.checkFieldName(t, f.name)

		schema, err := r.schemaFor(f.valueType())
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", f.name, err)
		}

		if f.isNullableMap() {
			schema, err = nullable(schema)
			if err != nil {
				return nil, fmt.Errorf("%w: field %q of %s: %w", ErrInvalidBody, f.name, t, err)
			}
		}

		if text, ok := r.described[t][f.name]; ok {
			schema = withDescription(schema, text)
		}

		props[f.name] = schema

		if f.isRequired() {
			required = append(required, f.name)
		}
	}

	out := object{keyType: typeObject}
	if len(props) > 0 {
		out[keyProperties] = props
	}

	if len(required) > 0 {
		slices.Sort(required)

		out[keyRequired] = stringsAsAny(required)
	}

	return out, nil
}

// checkFieldName notes a wire name that breaks the camelCase rule and that is
// not on the list of the legacy names.
func (r *reflector) checkFieldName(t reflect.Type, name string) {
	if wireField.MatchString(name) {
		return
	}

	if _, legacy := legacyFieldNames[name]; !legacy {
		r.badNames = append(r.badNames, fmt.Sprintf("%q of %s", name, t))
	}
}

// withDescription returns the schema of a field with the description of the
// field. The text stands next to a reference, as the only member beside it,
// and it wins over the description of the type of the field.
func withDescription(schema object, text string) object {
	out := cloneSchema(schema)
	out[keyDescription] = text

	return out
}

// cloneSchema returns a deep copy of a schema, so that a use of a declared
// schema never shares a map with the declaration.
func cloneSchema(s object) object {
	out := make(object, len(s))
	for k, v := range s {
		out[k] = cloneValue(v)
	}

	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneSchema(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}

		return out
	default:
		return v
	}
}
