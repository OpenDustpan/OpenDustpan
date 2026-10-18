package schema

import (
	"fmt"
	"strings"
)

// Kind is the kind of a type.
type Kind uint8

// The kinds of a type. The first version of the declaration format has no
// object kind: a nested object is a map of strings or a separate resource.
const (
	// KindInvalid is the zero value. A type with it does not validate.
	KindInvalid Kind = iota
	// KindString is a UTF-8 string.
	KindString
	// KindNumber is a decimal number of arbitrary precision.
	KindNumber
	// KindBool is true or false.
	KindBool
	// KindList is an ordered list of values of one element type.
	KindList
	// KindMap is a map from string keys to values of one element type.
	KindMap
)

// Type is the type of a value: a primitive, or a list or map of an element
// type.
type Type struct {
	// Kind is the kind of the type.
	Kind Kind
	// Elem is the element type of a list or a map, and nil for a primitive.
	Elem *Type
}

// String returns the string type.
func String() Type { return Type{Kind: KindString, Elem: nil} }

// Number returns the number type.
func Number() Type { return Type{Kind: KindNumber, Elem: nil} }

// Bool returns the bool type.
func Bool() Type { return Type{Kind: KindBool, Elem: nil} }

// ListOf returns the type of a list of elem.
func ListOf(elem Type) Type {
	return Type{Kind: KindList, Elem: &elem}
}

// MapOf returns the type of a map from strings to elem.
func MapOf(elem Type) Type {
	return Type{Kind: KindMap, Elem: &elem}
}

// Validate returns an error that wraps ErrInvalid when the type has no kind,
// when a list or map has no element type, or when a primitive has one.
func (t Type) Validate() error {
	switch t.Kind {
	case KindString, KindNumber, KindBool:
		if t.Elem != nil {
			return fmt.Errorf("%w: a primitive type has no element type", ErrInvalid)
		}

		return nil
	case KindList, KindMap:
		if t.Elem == nil {
			return fmt.Errorf("%w: a %s type needs an element type", ErrInvalid, t)
		}

		return t.Elem.Validate()
	case KindInvalid:
		return fmt.Errorf("%w: type has no kind", ErrInvalid)
	default:
		return fmt.Errorf("%w: unknown type kind %d", ErrInvalid, uint8(t.Kind))
	}
}

// Equal reports whether t and u are the same type.
func (t Type) Equal(u Type) bool {
	if t.Kind != u.Kind {
		return false
	}

	if t.Elem == nil || u.Elem == nil {
		return t.Elem == nil && u.Elem == nil
	}

	return t.Elem.Equal(*u.Elem)
}

// String returns the type as the declaration file spells it, for example
// "list(string)".
func (t Type) String() string {
	var b strings.Builder

	t.write(&b)

	return b.String()
}

func (t Type) write(b *strings.Builder) {
	switch t.Kind {
	case KindString:
		b.WriteString("string")
	case KindNumber:
		b.WriteString("number")
	case KindBool:
		b.WriteString("bool")
	case KindList, KindMap:
		if t.Kind == KindList {
			b.WriteString("list(")
		} else {
			b.WriteString("map(")
		}

		if t.Elem != nil {
			t.Elem.write(b)
		}

		b.WriteString(")")
	case KindInvalid:
		b.WriteString("invalid")
	default:
		fmt.Fprintf(b, "kind(%d)", uint8(t.Kind))
	}
}

// ParseType reads a type as the declaration file spells it: "string",
// "number", "bool", "list(T)" or "map(T)".
func ParseType(s string) (Type, error) {
	switch s {
	case "string":
		return String(), nil
	case "number":
		return Number(), nil
	case "bool":
		return Bool(), nil
	default:
	}

	for _, wrap := range []struct {
		prefix string
		of     func(Type) Type
	}{{prefix: "list(", of: ListOf}, {prefix: "map(", of: MapOf}} {
		inner, ok := strings.CutPrefix(s, wrap.prefix)
		if !ok {
			continue
		}

		inner, ok = strings.CutSuffix(inner, ")")
		if !ok {
			break
		}

		elem, err := ParseType(inner)
		if err != nil {
			var zero Type

			return zero, err
		}

		return wrap.of(elem), nil
	}

	var zero Type

	return zero, fmt.Errorf("%w: unknown type %q", ErrInvalid, s)
}
