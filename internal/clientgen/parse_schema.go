package clientgen

import (
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// keyDescription is the key of the description in the nodes of a schema.
const keyDescription = "description"

// schemaKeys returns the keys that a schema holds in the subset.
func schemaKeys() []string {
	return []string{
		"$ref", "type", "properties", "required", "additionalProperties", "items", "enum", "format",
		keyDescription, "minLength", "maxLength", "anyOf", "oneOf",
	}
}

// schemaRefPrefix starts the one kind of $ref that the parser reads.
const schemaRefPrefix = "#/components/schemas/"

// useSchema makes sure that the named schema is in the model.
func (st *parseState) useSchema(ptr, name string) error {
	if !schemaName.MatchString(name) {
		return invalidName(ptr, "schema name", name)
	}

	if _, ok := st.schemas[name]; ok || st.visiting[name] {
		return nil
	}

	return st.namedSchema(name)
}

// namedSchema reads one schema of the components into the model.
func (st *parseState) namedSchema(name string) error {
	ptr := ptrJoin("/components/schemas", name)

	if !schemaName.MatchString(name) {
		return invalidName(ptr, "schema name", name)
	}

	comps, err := objectAt(st.doc, "", "components")
	if err != nil {
		return err
	}

	all, err := objectAt(comps, "/components", "schemas")
	if err != nil {
		return err
	}

	node, ok := all[name]
	if !ok {
		return invalid(ptr, "the schema is not defined")
	}

	o, err := asObject(ptr, node)
	if err != nil {
		return err
	}

	st.visiting[name] = true
	defer delete(st.visiting, name)

	err = checkKeys(ptr, o, schemaKeys()...)
	if err != nil {
		return err
	}

	s, err := st.schemaOfKind(ptr, name, o)
	if err != nil {
		return err
	}

	st.schemas[name] = s

	return nil
}

// schemaOfKind reads a named schema as an object or as an enum.
func (st *parseState) schemaOfKind(ptr, name string, o object) (*model.Schema, error) {
	description, err := optionalString(o, ptr, keyDescription)
	if err != nil {
		return nil, err
	}

	// A type that is not a string is not "object" or "string", so it is refused
	// below with the other schemas that are not in the subset.
	kind, isString := o["type"].(string)

	switch {
	case isString && kind == "object" && o["enum"] == nil:
		return st.objectSchema(ptr, name, description, o)
	case isString && kind == "string" && o["enum"] != nil:
		return enumSchema(ptr, name, description, o)
	default:
		return nil, unsupported(ptr, "named schema that is neither an object nor a string enum")
	}
}

// enumSchema reads a string with a list of known values.
func enumSchema(ptr, name, description string, o object) (*model.Schema, error) {
	values, err := stringList(o, ptr, "enum")
	if err != nil {
		return nil, err
	}

	if len(values) == 0 {
		return nil, invalid(ptrJoin(ptr, "enum"), "an enum needs a value")
	}

	for i, v := range values {
		vptr := ptrJoin(ptrJoin(ptr, "enum"), strconv.Itoa(i))

		if !enumValueName.MatchString(v) {
			return nil, invalidName(vptr, "enum value", v)
		}

		if slices.Contains(values[:i], v) {
			return nil, invalid(vptr, "the enum value is in the list two times")
		}
	}

	s := new(model.Schema)
	s.Name, s.Kind, s.Description, s.Values = name, model.KindEnum, description, values

	return s, nil
}

// objectSchema reads an object with named fields.
func (st *parseState) objectSchema(ptr, name, description string, o object) (*model.Schema, error) {
	s := new(model.Schema)
	s.Name, s.Kind, s.Description = name, model.KindObject, description

	required, err := stringList(o, ptr, "required")
	if err != nil {
		return nil, err
	}

	props, err := optionalObjectAt(o, ptr, "properties")
	if err != nil {
		return nil, err
	}

	for _, r := range required {
		if _, ok := props[r]; !ok {
			return nil, invalid(ptrJoin(ptr, "required"), "required field "+r+" is not a property")
		}
	}

	for _, field := range sortedKeys(props) {
		fptr := ptrJoin(ptrJoin(ptr, "properties"), field)

		f, fErr := st.field(fptr, field, props[field])
		if fErr != nil {
			return nil, fErr
		}

		f.Required = slices.Contains(required, field)
		s.Fields = append(s.Fields, f)
	}

	if s.Extra, err = st.extraType(ptr, o); err != nil {
		return nil, err
	}

	return s, nil
}

// field reads one property.
func (st *parseState) field(ptr, name string, node any) (model.Field, error) {
	var f model.Field

	if !fieldName.MatchString(name) {
		return f, invalidName(ptr, "field name", name)
	}

	f.Name = name

	t, err := st.typeOf(ptr, node)
	if err != nil {
		return f, err
	}

	f.Type = t

	if o, ok := node.(object); ok {
		f.Description, err = optionalString(o, ptr, keyDescription)
	}

	return f, err
}

// anyType is the type of any JSON value.
func anyType() model.Type {
	return model.Type{Kind: model.TypeAny, Format: "", Elem: nil, Ref: "", Nullable: false}
}

// extraType reads additionalProperties of an object: true for any value, or a
// schema for the values. It returns nil when the object declares none.
func (st *parseState) extraType(ptr string, o object) (*model.Type, error) {
	extra, ok := o["additionalProperties"]
	if !ok {
		return nil, nil //nolint:nilnil // No additional properties is a valid answer, not an error.
	}

	eptr := ptrJoin(ptr, "additionalProperties")

	if b, isBool := extra.(bool); isBool {
		if !b {
			return nil, nil //nolint:nilnil // False says the same as no key.
		}

		t := anyType()

		return &t, nil
	}

	t, err := st.typeOf(eptr, extra)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// typeOf reads the schema of a field, a parameter or a body as a type.
func (st *parseState) typeOf(ptr string, node any) (model.Type, error) {
	var zero model.Type

	o, err := asObject(ptr, node)
	if err != nil {
		return zero, err
	}

	if err := checkKeys(ptr, o, schemaKeys()...); err != nil {
		return zero, err
	}

	if err := checkLengthKeys(ptr, o); err != nil {
		return zero, err
	}

	switch {
	case o["$ref"] != nil:
		return st.refType(ptr, o)
	case o["anyOf"] != nil:
		return st.openEnumType(ptr, o)
	case o["oneOf"] != nil:
		return st.nullableRefType(ptr, o)
	default:
		return st.plainType(ptr, o)
	}
}

// checkLengthKeys checks the shape of minLength and maxLength. The model does
// not hold them, because the server checks the length of a string.
func checkLengthKeys(ptr string, o object) error {
	for _, k := range []string{"minLength", "maxLength"} {
		v, ok := o[k]
		if !ok {
			continue
		}

		n, isNumber := v.(interface{ Int64() (int64, error) })
		if !isNumber {
			return invalid(ptrJoin(ptr, k), "an integer is needed")
		}

		if i, err := n.Int64(); err != nil || i < 0 {
			return invalid(ptrJoin(ptr, k), "a whole number that is 0 or more is needed")
		}
	}

	return nil
}

// localRef returns the schema name of a $ref in the components.
func localRef(ptr string, v any) (string, error) {
	ref, ok := v.(string)
	if !ok {
		return "", invalid(ptr, "a string is needed")
	}

	name, found := strings.CutPrefix(ref, schemaRefPrefix)
	if !found {
		return "", unsupported(ptr, "$ref outside the schemas of this document")
	}

	return name, nil
}

// refType reads a schema that is a $ref and nothing else.
func (st *parseState) refType(ptr string, o object) (model.Type, error) {
	var zero model.Type

	if len(o) > 1 {
		return zero, unsupported(ptr, "$ref beside other keys")
	}

	return st.namedType(ptr, o["$ref"], useClosed, false)
}

// refUse says how a field refers to a schema: as itself, or as an open enum.
type refUse int

const (
	// useClosed is a plain $ref. A $ref to an enum is a closed enum then.
	useClosed refUse = iota
	// useOpen is a $ref in anyOf with a string, an open enum.
	useOpen
)

// namedType returns a TypeRef after it makes sure that the schema is read.
func (st *parseState) namedType(ptr string, ref any, use refUse, nullable bool) (model.Type, error) {
	var zero model.Type

	rptr := ptrJoin(ptr, "$ref")

	name, err := localRef(rptr, ref)
	if err != nil {
		return zero, err
	}

	if err := st.useSchema(rptr, name); err != nil {
		return zero, err
	}

	// A schema that is still being read is an object that refers to itself.
	if s := st.schemas[name]; s != nil && s.Kind == model.KindEnum {
		st.recordEnumUse(name, use)
	}

	return model.Type{Kind: model.TypeRef, Format: "", Elem: nil, Ref: name, Nullable: nullable}, nil
}

// recordEnumUse remembers how a field uses an enum, as an open enum or as a
// closed one.
func (st *parseState) recordEnumUse(name string, use refUse) {
	if use == useOpen {
		st.openUse[name] = true
	} else {
		st.closedUse[name] = true
	}
}

// The numbers of branches of an open enum and of a nullable reference. The
// last branch of a nullable one is {type: null}.
const (
	withoutNull = 2
	withNull    = 3
)

// branches returns the items of an anyOf or a oneOf, which must have a given
// number of items.
func branches(ptr string, o object, key string, counts ...int) ([]object, error) {
	kptr := ptrJoin(ptr, key)

	if len(o) > 1 {
		return nil, unsupported(kptr, key+" beside other keys")
	}

	items, err := asList(kptr, o[key])
	if err != nil {
		return nil, err
	}

	if !slices.Contains(counts, len(items)) {
		return nil, unsupported(kptr, key+" that is not in the subset")
	}

	out := make([]object, 0, len(items))

	for i, item := range items {
		b, bErr := asObject(ptrJoin(kptr, strconv.Itoa(i)), item)
		if bErr != nil {
			return nil, bErr
		}

		out = append(out, b)
	}

	return out, nil
}

// isTypeOnly says that a branch is {"type": want}.
func isTypeOnly(b object, want string) bool {
	v, ok := b["type"].(string)

	return ok && len(b) == 1 && v == want
}

// openEnumType reads anyOf: [{$ref: enum}, {type: string}], with a third
// branch {type: null} for a nullable open enum.
func (st *parseState) openEnumType(ptr string, o object) (model.Type, error) {
	var zero model.Type

	bs, err := branches(ptr, o, "anyOf", withoutNull, withNull)
	if err != nil {
		return zero, err
	}

	kptr := ptrJoin(ptr, "anyOf")
	ok := len(bs[0]) == 1 && bs[0]["$ref"] != nil && isTypeOnly(bs[1], "string")
	nullable := len(bs) == withNull

	if nullable {
		ok = ok && isTypeOnly(bs[2], "null")
	}

	if !ok {
		return zero, unsupported(kptr, "anyOf that is not an open enum")
	}

	t, err := st.namedType(ptrJoin(kptr, "0"), bs[0]["$ref"], useOpen, nullable)
	if err != nil {
		return zero, err
	}

	if s := st.schemas[t.Ref]; s == nil || s.Kind != model.KindEnum {
		return zero, invalid(kptr, "an open enum needs the $ref of an enum")
	}

	return t, nil
}

// nullableRefType reads oneOf: [{$ref}, {type: null}].
func (st *parseState) nullableRefType(ptr string, o object) (model.Type, error) {
	var zero model.Type

	bs, err := branches(ptr, o, "oneOf", withoutNull)
	if err != nil {
		return zero, err
	}

	kptr := ptrJoin(ptr, "oneOf")

	if len(bs[0]) != 1 || bs[0]["$ref"] == nil || !isTypeOnly(bs[1], "null") {
		return zero, unsupported(kptr, "oneOf that is not a nullable reference")
	}

	return st.namedType(ptrJoin(kptr, "0"), bs[0]["$ref"], useClosed, true)
}

// plainType reads a schema with a type: a scalar, an array or a map.
func (st *parseState) plainType(ptr string, o object) (model.Type, error) {
	var zero model.Type

	if isAnyValue(o) {
		return anyType(), nil
	}

	base, nullable, err := baseType(ptr, o)
	if err != nil {
		return zero, err
	}

	var t model.Type

	t.Nullable = nullable

	switch base {
	case "string", "integer", "number", "boolean":
		if o["enum"] != nil {
			return zero, unsupported(ptrJoin(ptr, "enum"), "enum that has no name")
		}

		t.Kind = scalarKind(base)
		t.Format, err = optionalString(o, ptr, "format")
	case "array":
		t.Kind = model.TypeArray
		t.Elem, err = st.elemType(ptr, o)
	case "object":
		t.Kind = model.TypeMap
		t.Elem, err = st.mapElem(ptr, o)
	default:
		err = unsupported(ptrJoin(ptr, "type"), "type "+base)
	}

	return t, err
}

// isAnyValue says that a schema holds any JSON value: it has no type and no
// key but a description. The document size limit bounds such a schema, and the
// client keeps the value as raw JSON.
func isAnyValue(o object) bool {
	if o["type"] != nil {
		return false
	}

	for k := range o {
		if k != keyDescription {
			return false
		}
	}

	return true
}

// scalarKind maps the JSON type of a scalar to its kind.
func scalarKind(base string) model.TypeKind {
	switch base {
	case "integer":
		return model.TypeInteger
	case "number":
		return model.TypeNumber
	case "boolean":
		return model.TypeBoolean
	default:
		return model.TypeString
	}
}

// baseType returns the type of a schema, and whether it is nullable. The type
// is a name, or a list of a name and "null".
func baseType(ptr string, o object) (string, bool, error) {
	tptr := ptrJoin(ptr, "type")

	switch v := o["type"].(type) {
	case string:
		return v, false, nil
	case []any:
		if len(v) == 2 && v[1] == "null" {
			if name, ok := v[0].(string); ok && name != "null" {
				return name, true, nil
			}
		}

		return "", false, unsupported(tptr, "type list that is not [X, \"null\"]")
	default:
		return "", false, unsupported(ptr, "schema with no type")
	}
}

// elemType reads the items of an array.
func (st *parseState) elemType(ptr string, o object) (*model.Type, error) {
	items, ok := o["items"]
	if !ok {
		return nil, invalid(ptr, "an array needs items")
	}

	t, err := st.typeOf(ptrJoin(ptr, "items"), items)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// mapElem reads an object with free keys: additionalProperties and no
// properties.
func (st *parseState) mapElem(ptr string, o object) (*model.Type, error) {
	if o["properties"] != nil {
		return nil, unsupported(ptrJoin(ptr, "properties"), "object with fields that has no name")
	}

	elem, err := st.extraType(ptr, o)
	if err != nil {
		return nil, err
	}

	if elem == nil {
		return nil, unsupported(ptr, "object that declares no member")
	}

	return elem, nil
}
