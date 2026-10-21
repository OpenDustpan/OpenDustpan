package clientgen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// object is a JSON object as the decoder gives it.
type object = map[string]any

// ptrJoin appends a key to a JSON pointer, with the escapes of RFC 6901.
func ptrJoin(base, key string) string {
	key = strings.ReplaceAll(key, "~", "~0")
	key = strings.ReplaceAll(key, "/", "~1")

	return base + "/" + key
}

// unsupported returns ErrUnsupported with the pointer of the construct.
func unsupported(ptr, what string) error {
	return fmt.Errorf("%w: %s at %s", ErrUnsupported, what, pointerText(ptr))
}

// invalid returns ErrInvalidDescription with the pointer of the construct.
func invalid(ptr, what string) error {
	return fmt.Errorf("%w: %s at %s", ErrInvalidDescription, what, pointerText(ptr))
}

// invalidName returns ErrInvalidName with the pointer of the name.
func invalidName(ptr, what, name string) error {
	return fmt.Errorf("%w: %s %q at %s", ErrInvalidName, what, name, pointerText(ptr))
}

// pointerText shows the root pointer, which is empty, as "/".
func pointerText(ptr string) string {
	if ptr == "" {
		return "/"
	}

	return ptr
}

// sortedKeys returns the keys of an object in order, so that a walk does not
// depend on the order of a Go map.
func sortedKeys(o object) []string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// checkKeys fails with ErrUnsupported on the first key, in order, that is not
// in the allowlist.
func checkKeys(ptr string, o object, allowed ...string) error {
	for _, k := range sortedKeys(o) {
		if !slices.Contains(allowed, k) {
			return unsupported(ptrJoin(ptr, k), "key")
		}
	}

	return nil
}

func asObject(ptr string, v any) (object, error) {
	o, ok := v.(object)
	if !ok {
		return nil, invalid(ptr, "an object is needed")
	}

	return o, nil
}

func asList(ptr string, v any) ([]any, error) {
	l, ok := v.([]any)
	if !ok {
		return nil, invalid(ptr, "a list is needed")
	}

	return l, nil
}

// objectAt returns the object at a key, which must exist.
func objectAt(o object, ptr, key string) (object, error) {
	v, ok := o[key]
	if !ok {
		return nil, invalid(ptrJoin(ptr, key), "the key is needed")
	}

	return asObject(ptrJoin(ptr, key), v)
}

// optionalObjectAt returns the object at a key, or nil when the key is absent.
func optionalObjectAt(o object, ptr, key string) (object, error) {
	if _, ok := o[key]; !ok {
		return nil, nil //nolint:nilnil // An absent object and an empty object read the same.
	}

	return objectAt(o, ptr, key)
}

// stringAt returns the string at a key, which must exist.
func stringAt(o object, ptr, key string) (string, error) {
	v, ok := o[key]
	if !ok {
		return "", invalid(ptrJoin(ptr, key), "the key is needed")
	}

	s, ok := v.(string)
	if !ok {
		return "", invalid(ptrJoin(ptr, key), "a string is needed")
	}

	return s, nil
}

// optionalString returns the string at a key, or "" when the key is absent.
func optionalString(o object, ptr, key string) (string, error) {
	if _, ok := o[key]; !ok {
		return "", nil
	}

	return stringAt(o, ptr, key)
}

// optionalBool returns the boolean at a key, or false when the key is absent.
func optionalBool(o object, ptr, key string) (bool, error) {
	v, ok := o[key]
	if !ok {
		return false, nil
	}

	b, ok := v.(bool)
	if !ok {
		return false, invalid(ptrJoin(ptr, key), "a boolean is needed")
	}

	return b, nil
}

// stringList returns a list of strings at a key, or nil when the key is
// absent.
func stringList(o object, ptr, key string) ([]string, error) {
	v, ok := o[key]
	if !ok {
		return nil, nil
	}

	items, err := asList(ptrJoin(ptr, key), v)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(items))

	for i, item := range items {
		s, isString := item.(string)
		if !isString {
			return nil, invalid(ptrJoin(ptrJoin(ptr, key), strconv.Itoa(i)), "a string is needed")
		}

		out = append(out, s)
	}

	return out, nil
}
