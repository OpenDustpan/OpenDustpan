package clientgen

import (
	"fmt"
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
		keyDescription, "minLength", "maxLength", "anyOf", "oneOf", "discriminator",
	}
}

// The type names of the primitive values of JSON.
const (
	jsonString  = "string"
	jsonInteger = "integer"
	jsonNumber  = "number"
	jsonBoolean = "boolean"
)

// typeNull is the type name of JSON null in a type list.
const typeNull = "null"

// The numbers of names of a type list. The shortest list holds two names, and
// the nullable form [X, "null"] holds two as well.
const (
	typeListMinNames  = 2
	typeListNullNames = 2
)

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
	case isUnion(o):
		return st.unionSchema(ptr, name, description, o)
	case isString && kind == "object" && o["enum"] == nil:
		return st.objectSchema(ptr, name, description, o)
	case isString && kind == jsonString && o["enum"] != nil:
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
		return model.Field{}, invalidName(ptr, "field name", name)
	}

	f.Name = name

	t, err := st.typeOf(ptr, node)
	if err != nil {
		return model.Field{}, err
	}

	f.Type = t

	if o, ok := node.(object); ok {
		f.Description, err = optionalString(o, ptr, keyDescription)
	}

	return f, err
}

// anyType is the type of any JSON value.
func anyType() model.Type {
	return model.Type{Kind: model.TypeAny, Format: "", Elem: nil, Ref: "", Const: "", Nullable: false, Members: nil}
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

// refType reads a schema that is a $ref, with a description of the field
// beside it and nothing else. The description wins over the description of the
// type of the field.
func (st *parseState) refType(ptr string, o object) (model.Type, error) {
	var zero model.Type

	for _, k := range sortedKeys(o) {
		if k != "$ref" && k != keyDescription {
			return zero, unsupported(ptrJoin(ptr, k), "key beside $ref")
		}
	}

	if _, err := optionalString(o, ptr, keyDescription); err != nil {
		return zero, err
	}

	return st.namedType(ptr, o["$ref"], useClosed, false)
}

// refUse says how a field refers to a schema: as itself, or as an open enum.
type refUse int

const (
	_ refUse = iota // Zero means that nobody chose.
	// useClosed is a plain $ref. A $ref to an enum is a closed enum then.
	useClosed
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

	return model.Type{
		Kind:     model.TypeRef,
		Format:   "",
		Elem:     nil,
		Ref:      name,
		Const:    "",
		Nullable: nullable,
		Members:  nil,
	}, nil
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

// plainType reads a schema with a type: a scalar, an array, a map or a type
// list.
func (st *parseState) plainType(ptr string, o object) (model.Type, error) {
	var zero model.Type

	if isAnyValue(o) {
		// A field checks its own description after its type, but a result
		// or a request body has no such step, so this check covers them.
		if _, err := optionalString(o, ptr, keyDescription); err != nil {
			return zero, err
		}

		return anyType(), nil
	}

	names, isList, err := typeNames(ptr, o)
	if err != nil {
		return zero, err
	}

	if isList && !isNullableList(names) {
		return typeListType(ptr, o, names)
	}

	var t model.Type

	// The only list that reaches this point is [X, "null"], the nullable
	// form of the type X.
	t.Nullable = isList

	switch base := names[0]; base {
	case jsonString, jsonInteger, jsonNumber, jsonBoolean:
		if o["enum"] != nil {
			t.Const, err = constValue(ptr, base, o)
			if err != nil {
				return zero, err
			}
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

// constValue reads a string that has one allowed value and no name. It refuses
// any other enum that has no name.
func constValue(ptr, base string, o object) (string, error) {
	eptr := ptrJoin(ptr, "enum")

	if base != jsonString {
		return "", unsupported(eptr, "enum that has no name")
	}

	values, err := stringList(o, ptr, "enum")
	if err != nil {
		return "", err
	}

	if len(values) != 1 {
		return "", unsupported(eptr, "enum that has no name and not one value")
	}

	if !enumValueName.MatchString(values[0]) {
		return "", invalidName(ptrJoin(eptr, "0"), "enum value", values[0])
	}

	return values[0], nil
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
	case jsonInteger:
		return model.TypeInteger
	case jsonNumber:
		return model.TypeNumber
	case jsonBoolean:
		return model.TypeBoolean
	default:
		return model.TypeString
	}
}

// typeNames returns the names of the type of a schema: one name, or the names
// of a type list. The second result is true for a type list.
func typeNames(ptr string, o object) ([]string, bool, error) {
	tptr := ptrJoin(ptr, "type")

	switch v := o["type"].(type) {
	case string:
		return []string{v}, false, nil
	case []any:
		names := make([]string, 0, len(v))

		for i, item := range v {
			name, isString := item.(string)
			if !isString {
				return nil, false, unsupported(
					ptrJoin(tptr, strconv.Itoa(i)),
					"type name "+valueText(item)+" that is not a string",
				)
			}

			names = append(names, name)
		}

		return names, true, nil
	case nil:
		return nil, false, unsupported(ptr, "schema with no type")
	default:
		return nil, false, unsupported(tptr, "type "+valueText(v))
	}
}

// typeListType reads a schema whose type is a type list. A type list carries
// no enum and no format: a client that drops them would accept a value that
// the server refuses.
func typeListType(ptr string, o object, names []string) (model.Type, error) {
	var zero model.Type

	for _, k := range []string{"enum", "format"} {
		if o[k] != nil {
			return zero, unsupported(ptrJoin(ptr, k), k+" beside a type list")
		}
	}

	return primitiveType(ptr, names)
}

// isNullableList reports whether the names are the nullable form [X, "null"].
func isNullableList(names []string) bool {
	return len(names) == typeListNullNames && names[1] == typeNull
}

// primitiveType returns the type of a type list: two or more primitive type
// names, with no repeat.
func primitiveType(ptr string, names []string) (model.Type, error) {
	var zero model.Type

	tptr := ptrJoin(ptr, "type")

	if len(names) < typeListMinNames {
		return zero, unsupported(tptr, "type list "+nameListText(names)+" that holds fewer than two names")
	}

	members := make([]model.TypeKind, 0, len(names))
	seen := make(map[string]bool, len(names))

	for i, name := range names {
		nptr := ptrJoin(tptr, strconv.Itoa(i))

		if !isPrimitiveTypeName(name) {
			return zero, unsupported(nptr, "type name "+strconv.Quote(name)+" that is not a primitive type name")
		}

		if seen[name] {
			return zero, unsupported(nptr, "type name "+strconv.Quote(name)+" that is in the list two times")
		}

		seen[name] = true
		members = append(members, scalarKind(name))
	}

	var t model.Type

	t.Kind = model.TypeUnion
	t.Members = members

	return t, nil
}

// isPrimitiveTypeName reports whether the name is the type name of a primitive
// value: string, integer, number or boolean.
func isPrimitiveTypeName(name string) bool {
	switch name {
	case jsonString, jsonInteger, jsonNumber, jsonBoolean:
		return true
	default:
		return false
	}
}

// valueText writes a JSON value the way the description writes it, for an
// error message.
func valueText(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case nil:
		return typeNull
	default:
		return fmt.Sprintf("%v", x)
	}
}

// nameListText writes a list of type names the way the description writes it,
// for an error message.
func nameListText(names []string) string {
	out := make([]string, 0, len(names))

	for _, name := range names {
		out = append(out, strconv.Quote(name))
	}

	return "[" + strings.Join(out, ", ") + "]"
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
