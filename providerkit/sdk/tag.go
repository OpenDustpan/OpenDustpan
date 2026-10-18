package sdk

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"opendustpan.org/opendustpan/providerkit/schema"
)

// ErrInvalidModel reports a model struct that the SDK cannot map to a
// schema. The wrapped message names the field and the rule.
var ErrInvalidModel = errors.New("invalid model")

// TagName is the struct tag that maps a field to an attribute.
const TagName = "provider"

// ParseTag reads a tag of the form "name,option,...". The options are one
// mode ("required", "optional", "computed", or "optional,computed") and any
// of "sensitive", "writeonly" and "replace". It returns an attribute with
// no type and no description.
func ParseTag(tag string) (schema.Attribute, error) {
	parts := strings.Split(tag, ",")
	a := schema.Attribute{
		Name: parts[0], Type: schema.Type{Kind: schema.KindInvalid, Elem: nil}, Mode: schema.ModeInvalid,
		Sensitive: false, WriteOnly: false, Replace: false, Description: "",
	}

	if !schema.ValidName(a.Name) {
		return a, fmt.Errorf("%w: tag %q: name %q is not snake_case", ErrInvalidModel, tag, a.Name)
	}

	var optional, computed, required bool

	for _, opt := range parts[1:] {
		var seen *bool

		switch opt {
		case "required":
			seen = &required
		case "optional":
			seen = &optional
		case "computed":
			seen = &computed
		case "sensitive":
			seen = &a.Sensitive
		case "writeonly":
			seen = &a.WriteOnly
		case "replace":
			seen = &a.Replace
		default:
			return a, fmt.Errorf("%w: tag %q: unknown option %q", ErrInvalidModel, tag, opt)
		}

		if *seen {
			return a, fmt.Errorf("%w: tag %q: option %q is there twice", ErrInvalidModel, tag, opt)
		}

		*seen = true
	}

	a.Mode = tagMode(required, optional, computed)
	if a.Mode == schema.ModeInvalid {
		return a, fmt.Errorf("%w: tag %q: give one mode", ErrInvalidModel, tag)
	}

	return a, nil
}

func tagMode(required, optional, computed bool) schema.Mode {
	switch [3]bool{required, optional, computed} {
	case [3]bool{true, false, false}:
		return schema.ModeRequired
	case [3]bool{false, true, false}:
		return schema.ModeOptional
	case [3]bool{false, false, true}:
		return schema.ModeComputed
	case [3]bool{false, true, true}:
		return schema.ModeOptionalComputed
	default:
		return schema.ModeInvalid
	}
}

// SchemaOf derives the schema from the exported, tagged fields of the struct
// M, in field order. A field without the tag is not an attribute. The Go
// types map as follows: string to string; the int, uint and float types to
// number; bool to bool; a slice to a list; a map with string keys to a map.
// A pointer to one of these makes no difference to the type.
func SchemaOf[M any]() (schema.Schema, error) {
	s := schema.Schema{Version: 0, Attributes: nil}

	t := reflect.TypeFor[M]()
	if t.Kind() != reflect.Struct {
		return s, fmt.Errorf("%w: %s is not a struct", ErrInvalidModel, t)
	}

	for f := range t.Fields() {
		tag, ok := f.Tag.Lookup(TagName)
		if !ok {
			continue
		}

		if !f.IsExported() {
			return s, fmt.Errorf("%w: field %s has a tag and is not exported", ErrInvalidModel, f.Name)
		}

		a, err := ParseTag(tag)
		if err != nil {
			return s, fmt.Errorf("field %s: %w", f.Name, err)
		}

		if a.Type, err = typeOf(f.Type); err != nil {
			return s, fmt.Errorf("field %s: %w", f.Name, err)
		}

		s.Attributes = append(s.Attributes, a)
	}

	if err := s.Validate(); err != nil {
		return s, fmt.Errorf("%w: %w", ErrInvalidModel, err)
	}

	return s, nil
}

func typeOf(t reflect.Type) (schema.Type, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	var none schema.Type

	switch t.Kind() {
	case reflect.String:
		return schema.String(), nil
	case reflect.Bool:
		return schema.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return schema.Number(), nil
	case reflect.Slice:
		elem, err := typeOf(t.Elem())

		return schema.ListOf(elem), err
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return none, fmt.Errorf("%w: map key %s is not a string", ErrInvalidModel, t.Key())
		}

		elem, err := typeOf(t.Elem())

		return schema.MapOf(elem), err
	case reflect.Invalid, reflect.Uintptr, reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan,
		reflect.Func, reflect.Interface, reflect.Pointer, reflect.Struct, reflect.UnsafePointer:
		return none, fmt.Errorf("%w: Go type %s has no attribute type", ErrInvalidModel, t)
	default:
		return none, fmt.Errorf("%w: Go type %s is of an unknown kind", ErrInvalidModel, t)
	}
}
