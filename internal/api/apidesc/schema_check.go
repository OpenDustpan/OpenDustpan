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
	"minLength", "maxLength", keyAnyOf, keyOneOf,
}

// checkDeclared refuses a member outside the subset in a declared schema, at
// any depth.
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

// checkDeclaredMember checks the schemas that a member of a schema holds.
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
	case keyAnyOf, keyOneOf:
		return checkDeclaredList(key, child)
	default:
		return nil
	}
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

// checkDeclaredList checks the branches of an anyOf or a oneOf.
func checkDeclaredList(key string, child any) error {
	list, ok := child.([]any)
	if !ok {
		return fmt.Errorf("%w: %s is not a list", errDeclared, key)
	}

	for _, branch := range list {
		if err := checkDeclared(branch); err != nil {
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
