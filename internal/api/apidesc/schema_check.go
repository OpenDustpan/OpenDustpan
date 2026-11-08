package apidesc

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// errDeclared means that a declared schema breaks the subset.
var errDeclared = errors.New("declared schema is not valid")

// subsetSchemaKeys is the allowlist of the members of a schema in the subset
// of the description. The reflector writes no other member, and it refuses a
// declaration with another one.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant sets.
var subsetSchemaKeys = []string{
	keyRef, keyType, keyProperties, keyRequired, keyAdditional, keyItems, keyEnum, keyFormat, keyDescription,
	"minLength", "maxLength",
}

// isSubsetType reports whether name is a type name of the subset: a name that
// the reflector writes.
func isSubsetType(name string) bool {
	switch name {
	case typeObject, typeArray, typeString, typeInteger, typeNumber, typeBoolean, typeNull:
		return true
	default:
		return false
	}
}

// checkDeclared refuses a member outside the subset in a declared schema, at
// any depth. It refuses anyOf and oneOf too: the reflector writes the only two
// forms of them that the subset holds, the open enum and the nullable reference,
// and a declaration has no way to write them.
func checkDeclared(schema any) error {
	members, ok := schema.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: a schema is not an object", errDeclared)
	}

	for key, child := range members {
		if !slices.Contains(subsetSchemaKeys, key) {
			return fmt.Errorf("%w: member %q is not in the subset", errDeclared, key)
		}

		if err := checkDeclaredMember(key, child); err != nil {
			return err
		}
	}

	return nil
}

// checkDeclaredMember checks the schemas that a member of a schema holds, and
// the values of the members with a value of their own.
func checkDeclaredMember(key string, child any) error {
	switch key {
	case keyProperties:
		return checkDeclaredProperties(child)
	case keyItems:
		return checkDeclared(child)
	case keyAdditional:
		if _, isBool := child.(bool); isBool {
			return nil
		}

		return checkDeclared(child)
	case keyType:
		return checkDeclaredType(child)
	case keyRequired:
		return checkDeclaredRequired(child)
	default:
		return nil
	}
}

// checkDeclaredType checks the value of a type member: one type name of the
// subset, or a non-empty list of them, as the reflector writes for a value
// that can also be null.
func checkDeclaredType(child any) error {
	if name, isString := child.(string); isString {
		if isSubsetType(name) {
			return nil
		}

		return fmt.Errorf("%w: member %q is the type name %q, which is not in the subset", errDeclared, keyType, name)
	}

	names, isList := child.([]any)
	if !isList || len(names) == 0 {
		return fmt.Errorf("%w: member %q is %v, which is not a type name or a non-empty list of type names",
			errDeclared, keyType, child)
	}

	for _, name := range names {
		text, isString := name.(string)
		if !isString || !isSubsetType(text) {
			return fmt.Errorf("%w: member %q holds %v, which is not a type name of the subset", errDeclared,
				keyType, name)
		}
	}

	return nil
}

// checkDeclaredRequired checks the value of a required member: a list of
// strings, as the reflector writes.
func checkDeclaredRequired(child any) error {
	names, isList := child.([]any)
	if !isList {
		return fmt.Errorf("%w: member %q is %v, which is not a list of strings", errDeclared, keyRequired, child)
	}

	for _, name := range names {
		if _, isString := name.(string); !isString {
			return fmt.Errorf("%w: member %q holds the value %v, which is not a string", errDeclared, keyRequired,
				name)
		}
	}

	return nil
}

// checkDeclaredProperties checks the schemas of the members of an object.
func checkDeclaredProperties(child any) error {
	props, ok := child.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s is not an object", errDeclared, keyProperties)
	}

	for _, prop := range props {
		if err := checkDeclared(prop); err != nil {
			return err
		}
	}

	return nil
}

// checkRefs returns ErrInvalidBody when a schema refers to a name that has no
// schema. It is a check of the reflector itself: a client generator would fail
// on a dangling reference, and a dangling reference never ships.
func (r *reflector) checkRefs() error {
	return eachMember(r.schemas, func(key string, value any) error {
		ref, isString := value.(string)
		if key != keyRef || !isString {
			return nil
		}

		name := strings.TrimPrefix(ref, refSchemas)
		if _, known := r.schemas[name]; !known && name != errorSchema {
			return fmt.Errorf("%w: reference %q has no schema", ErrInvalidBody, ref)
		}

		return nil
	})
}

// eachMember calls visit for each member of each object in the JSON value, at
// any depth, and stops at the first error.
func eachMember(value any, visit func(key string, member any) error) error {
	switch x := value.(type) {
	case map[string]any:
		return eachObjectMember(x, visit)
	case []any:
		for _, member := range x {
			if err := eachMember(member, visit); err != nil {
				return err
			}
		}

		return nil
	default:
		// A string, a number, a boolean or null holds no member.
		return nil
	}
}

// eachObjectMember calls visit for each member of the object and of the values
// below it.
func eachObjectMember(object map[string]any, visit func(key string, member any) error) error {
	for key, member := range object {
		if err := visit(key, member); err != nil {
			return err
		}

		if err := eachMember(member, visit); err != nil {
			return err
		}
	}

	return nil
}

// openEnumBranches is the number of branches of an open enum without null: the
// reference to the values and the plain string.
const openEnumBranches = 2

// checkForms returns ErrInvalidBody when a schema holds an anyOf or a oneOf in
// a form other than the two that the subset allows:
//
//   - the open enum: anyOf [{$ref: an enum of strings}, {type: string}], with
//     {type: null} after them when the value can be null;
//   - the nullable reference: oneOf [{$ref}, {type: null}].
//
// It runs on the finished schemas, so that no declaration and no reflected type
// can write another form. It is a layer of its own: the declaration check and
// nullable refuse the same forms earlier.
func (r *reflector) checkForms() error {
	return eachMember(r.schemas, func(key string, value any) error {
		branches, isList := value.([]any)
		if !isList {
			return nil
		}

		switch key {
		case keyAnyOf:
			return r.checkOpenEnum(branches)
		case keyOneOf:
			return checkNullableRef(branches)
		default:
			return nil
		}
	})
}

// checkOpenEnum checks the branches of an anyOf.
func (r *reflector) checkOpenEnum(branches []any) error {
	form := len(branches) == openEnumBranches ||
		(len(branches) == openEnumBranches+1 && isNullBranch(branches[openEnumBranches]))
	if !form || !isPlainString(branches[1]) {
		return fmt.Errorf("%w: an anyOf is not the open enum [enum, string] with an optional null", ErrInvalidBody)
	}

	name, ok := refName(branches[0])
	if !ok {
		return fmt.Errorf("%w: the first branch of an open enum is not a reference", ErrInvalidBody)
	}

	enum, isSchema := r.schemas[name].(object)
	if !isSchema || enum[keyType] != typeString || enum[keyEnum] == nil {
		return fmt.Errorf("%w: reference %q of an open enum is not an enum of strings", ErrInvalidBody, name)
	}

	return nil
}

// checkNullableRef checks the branches of a oneOf.
func checkNullableRef(branches []any) error {
	if len(branches) != 2 || !isNullBranch(branches[1]) {
		return fmt.Errorf("%w: a oneOf is not [reference, null]", ErrInvalidBody)
	}

	if _, ok := refName(branches[0]); !ok {
		return fmt.Errorf("%w: the first branch of a oneOf is not a reference", ErrInvalidBody)
	}

	return nil
}

// refName returns the schema name of a branch that is only a reference.
func refName(branch any) (string, bool) {
	m, ok := branch.(map[string]any)
	if !ok || len(m) != 1 {
		return "", false
	}

	ref, ok := m[keyRef].(string)
	if !ok || !strings.HasPrefix(ref, refSchemas) {
		return "", false
	}

	return strings.TrimPrefix(ref, refSchemas), true
}

// isNullBranch reports whether the branch is only the null type.
func isNullBranch(branch any) bool {
	m, ok := branch.(map[string]any)

	return ok && len(m) == 1 && m[keyType] == typeNull
}

// isPlainString reports whether the branch is only the type string.
func isPlainString(branch any) bool {
	m, ok := branch.(map[string]any)

	return ok && len(m) == 1 && m[keyType] == typeString
}
