package clientgen

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// errorAnswerName is the union of the error schemas by errorCode. A
// description may hold it. The parser reads it whatever the operations reach.
const errorAnswerName = "ErrorAnswer"

// isUnion says that a schema is a oneOf with a discriminator.
func isUnion(o object) bool {
	return o["oneOf"] != nil && o["discriminator"] != nil
}

// unionSchema reads a oneOf of references with a discriminator. The mapping
// names the variants. The one branch that the mapping does not name is the
// base: a client uses it for a value of the discriminator that no variant
// names, so a value that the description does not know yet still parses.
func (st *parseState) unionSchema(ptr, name, description string, o object) (*model.Schema, error) {
	for key := range o {
		if key != "oneOf" && key != "discriminator" && key != keyDescription {
			return nil, unsupported(ptrJoin(ptr, key), "key beside oneOf and discriminator")
		}
	}

	property, mapping, err := discriminatorOf(ptrJoin(ptr, "discriminator"), o["discriminator"])
	if err != nil {
		return nil, err
	}

	branches, err := unionBranches(ptrJoin(ptr, "oneOf"), o["oneOf"])
	if err != nil {
		return nil, err
	}

	variants, base, err := unionParts(ptr, mapping, branches)
	if err != nil {
		return nil, err
	}

	s := new(model.Schema)
	s.Name, s.Kind, s.Description, s.Discriminator = name, model.KindUnion, description, property
	s.Variants, s.Base = variants, base

	if err := st.checkUnionMembers(ptr, s); err != nil {
		return nil, err
	}

	return s, nil
}

// unionParts matches the mapping with the branches. Each mapping entry must
// name a branch, no branch may be named two times, and one branch must stay:
// the base.
func unionParts(ptr string, mapping map[string]string, branches []string) ([]model.Variant, string, error) {
	mptr := ptrJoin(ptrJoin(ptr, "discriminator"), "mapping")
	variants := make([]model.Variant, 0, len(mapping))

	for _, value := range slices.Sorted(maps.Keys(mapping)) {
		target := mapping[value]
		if !slices.Contains(branches, target) {
			return nil, "", invalid(mptr, "the mapping names "+target+", which is not a oneOf branch")
		}

		if slices.ContainsFunc(variants, func(v model.Variant) bool { return v.Schema == target }) {
			return nil, "", invalid(mptr, "the mapping names "+target+" two times")
		}

		variants = append(variants, model.Variant{Value: value, Schema: target})
	}

	if len(branches) != len(variants)+1 {
		return nil, "", invalid(
			ptrJoin(ptr, "oneOf"),
			"oneOf needs one branch for each mapping entry and one base branch",
		)
	}

	for _, b := range branches {
		if !slices.ContainsFunc(variants, func(v model.Variant) bool { return v.Schema == b }) {
			return variants, b, nil
		}
	}

	return nil, "", invalid(ptrJoin(ptr, "oneOf"), "no base branch is left")
}

// discriminatorOf reads the discriminator: the property name and the mapping
// from a value to the name of a schema.
func discriminatorOf(ptr string, node any) (string, map[string]string, error) {
	o, err := asObject(ptr, node)
	if err != nil {
		return "", nil, err
	}

	for key := range o {
		if key != "propertyName" && key != "mapping" {
			return "", nil, unsupported(ptrJoin(ptr, key), "discriminator key that is not propertyName or mapping")
		}
	}

	property, ok := o["propertyName"].(string)
	if !ok || !fieldName.MatchString(property) {
		return "", nil, invalid(ptrJoin(ptr, "propertyName"), "the property name must be a field name")
	}

	mapping, err := mappingOf(ptrJoin(ptr, "mapping"), o["mapping"])
	if err != nil {
		return "", nil, err
	}

	return property, mapping, nil
}

// mappingOf reads the mapping of a discriminator: a value of the member to a
// reference to a schema. It returns the names of the schemas.
func mappingOf(ptr string, node any) (map[string]string, error) {
	raw, err := asObject(ptr, node)
	if err != nil {
		return nil, err
	}

	if len(raw) == 0 {
		return nil, invalid(ptr, "the mapping needs an entry")
	}

	mapping := make(map[string]string, len(raw))

	for value, entry := range raw {
		vptr := ptrJoin(ptr, value)

		if !enumValueName.MatchString(value) {
			return nil, invalidName(vptr, "discriminator value", value)
		}

		target, isString := entry.(string)
		if !isString || !strings.HasPrefix(target, schemaRefPrefix) {
			return nil, invalid(vptr, "the mapping entry must be a reference to a schema")
		}

		mapping[value] = strings.TrimPrefix(target, schemaRefPrefix)
	}

	return mapping, nil
}

// unionBranches reads the branches of a oneOf: each is a reference to a
// schema and nothing else. It returns the names, in the order of the list.
func unionBranches(ptr string, node any) ([]string, error) {
	items, err := asList(ptr, node)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(items))

	for i, item := range items {
		bptr := ptrJoin(ptr, strconv.Itoa(i))

		b, bErr := asObject(bptr, item)
		if bErr != nil {
			return nil, bErr
		}

		ref, isString := b["$ref"].(string)
		if len(b) != 1 || !isString || !strings.HasPrefix(ref, schemaRefPrefix) {
			return nil, unsupported(bptr, "oneOf branch that is not a reference to a schema")
		}

		name := strings.TrimPrefix(ref, schemaRefPrefix)
		if slices.Contains(names, name) {
			return nil, invalid(bptr, "the oneOf branch is in the list two times")
		}

		names = append(names, name)
	}

	return names, nil
}

// checkUnionMembers reads the schemas that the union names and checks that
// each variant holds the discriminator with the value that the mapping gives,
// and that the base holds the discriminator too. It marks the variants, and
// the schemas that they refer to, as parts of the union.
func (st *parseState) checkUnionMembers(ptr string, u *model.Schema) error {
	optr := ptrJoin(ptr, "oneOf")

	if err := st.useSchema(optr, u.Base); err != nil {
		return err
	}

	base := st.schemas[u.Base]
	if base == nil || base.Kind != model.KindObject || fieldOf(base, u.Discriminator) == nil {
		return invalid(optr, "the base branch must be an object with the discriminator member")
	}

	for _, v := range u.Variants {
		if err := st.useSchema(optr, v.Schema); err != nil {
			return err
		}

		if err := st.checkVariant(optr, u, v); err != nil {
			return err
		}
	}

	return nil
}

// checkVariant checks that a variant is an object with no additional
// properties and with the discriminator as a required constant. It marks the
// variant, and the object schemas of its members, as parts of the union.
func (st *parseState) checkVariant(ptr string, u *model.Schema, v model.Variant) error {
	s := st.schemas[v.Schema]
	if s == nil || s.Kind != model.KindObject || s.Extra != nil {
		return invalid(ptr, "the variant "+v.Schema+" must be an object with no additional properties")
	}

	f := fieldOf(s, u.Discriminator)
	if f == nil || !f.Required || f.Type.Kind != model.TypeString || f.Type.Const != v.Value {
		return invalid(
			ptr,
			"the variant "+v.Schema+" must hold the member "+u.Discriminator+" with the one value "+v.Value,
		)
	}

	s.Union = u.Name
	st.markArguments(u.Name, s)

	return nil
}

// markArguments marks the object schemas that a variant refers to, such as
// its arguments, as parts of the union, when no union holds them yet.
func (st *parseState) markArguments(union string, variant *model.Schema) {
	for i := range variant.Fields {
		member := &variant.Fields[i]
		if member.Type.Kind != model.TypeRef {
			continue
		}

		if arg := st.schemas[member.Type.Ref]; arg != nil && arg.Kind == model.KindObject && arg.Union == "" {
			arg.Union = union
		}
	}
}

// fieldOf returns the field of an object schema with the wire name, or nil.
func fieldOf(s *model.Schema, name string) *model.Field {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return &s.Fields[i]
		}
	}

	return nil
}
