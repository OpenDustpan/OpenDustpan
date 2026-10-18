package schema

import (
	"maps"
	"math/big"
	"slices"
)

// Value is one typed value. It can be known, null, or unknown. A value is
// unknown in a plan when the provider sets it only during the apply, for
// example an ID that the API assigns. The zero value is an invalid value of
// no type.
type Value struct {
	typ     Type
	unknown bool
	null    bool
	str     string
	num     *big.Float
	boolean bool
	elems   []Value
	entries map[string]Value
}

// NumberPrecision is the precision in bits of every number value. Each
// conversion to a number value rounds to it, so two values that come from
// different sources compare by one rule. 512 bits hold every int64, uint64
// and float64 exactly, and decimal numbers of about 150 digits.
const NumberPrecision = 512

// Values is the set of attribute values of one configuration, resource or
// data source, by attribute name. A missing name is the same as null.
type Values map[string]Value

// known returns the known, non-null value of t with no content.
func known(t Type) Value {
	return Value{
		typ: t, unknown: false, null: false,
		str: "", num: nil, boolean: false, elems: nil, entries: nil,
	}
}

// NullOf returns the null value of t.
func NullOf(t Type) Value {
	v := known(t)
	v.null = true

	return v
}

// UnknownOf returns the unknown value of t.
func UnknownOf(t Type) Value {
	v := known(t)
	v.unknown = true

	return v
}

// StringValue returns a known string.
func StringValue(s string) Value {
	v := known(String())
	v.str = s

	return v
}

// NumberValue returns a known number. It copies n and rounds the copy to
// NumberPrecision.
func NumberValue(n *big.Float) Value {
	v := known(Number())
	v.num = newNumber().Set(n)

	return v
}

// IntValue returns a known number with the value of i.
func IntValue(i int64) Value {
	return NumberValue(newNumber().SetInt64(i))
}

// BoolValue returns a known bool.
func BoolValue(b bool) Value {
	v := known(Bool())
	v.boolean = b

	return v
}

// ListValue returns a known list of elem. Each element must have the type
// elem. It copies the slice.
func ListValue(elem Type, elems ...Value) Value {
	v := known(ListOf(elem))
	v.elems = slices.Clone(elems)

	return v
}

// MapValue returns a known map of elem. Each entry must have the type elem.
// It copies the map.
func MapValue(elem Type, entries map[string]Value) Value {
	v := known(MapOf(elem))
	v.entries = maps.Clone(entries)

	return v
}

// Type returns the type of the value.
func (v Value) Type() Type { return v.typ }

// IsNull reports whether the value is null.
func (v Value) IsNull() bool { return v.null }

// IsKnown reports whether the value is known. A null value is known.
func (v Value) IsKnown() bool { return !v.unknown && v.typ.Kind != KindInvalid }

// WhollyKnown reports whether the value and every element in it are known.
func (v Value) WhollyKnown() bool {
	if !v.IsKnown() {
		return false
	}

	for _, e := range v.elems {
		if !e.WhollyKnown() {
			return false
		}
	}

	for _, e := range v.entries {
		if !e.WhollyKnown() {
			return false
		}
	}

	return true
}

// AsString returns the string, and false when the value is not a known,
// non-null string.
func (v Value) AsString() (string, bool) {
	return v.str, v.typ.Kind == KindString && v.IsKnown() && !v.null
}

// AsNumber returns a copy of the number, of NumberPrecision, and false when
// the value is not a known, non-null number.
func (v Value) AsNumber() (*big.Float, bool) {
	if v.typ.Kind != KindNumber || !v.IsKnown() || v.null || v.num == nil {
		return nil, false
	}

	return newNumber().Set(v.num), true
}

// newNumber returns a zero number of NumberPrecision.
func newNumber() *big.Float {
	return new(big.Float).SetPrec(NumberPrecision)
}

// IsTrue reports whether the value is the known bool true.
func (v Value) IsTrue() bool {
	return v.typ.Kind == KindBool && v.IsKnown() && !v.null && v.boolean
}

// Elements returns a copy of the elements of a known list, and nil for any
// other value.
func (v Value) Elements() []Value {
	return slices.Clone(v.elems)
}

// Entries returns a copy of the entries of a known map, and nil for any other
// value.
func (v Value) Entries() map[string]Value {
	return maps.Clone(v.entries)
}

// Equal reports whether v and u have the same type and the same value. An
// unknown value is equal to no value, also not to another unknown value,
// because the two can resolve to different values.
func (v Value) Equal(u Value) bool {
	if !v.typ.Equal(u.typ) || v.unknown || u.unknown {
		return false
	}

	if v.null || u.null {
		return v.null == u.null
	}

	return v.equalContent(u)
}

// equalContent compares two known, non-null values of the same type.
func (v Value) equalContent(u Value) bool {
	switch v.typ.Kind {
	case KindString:
		return v.str == u.str
	case KindNumber:
		return v.num != nil && u.num != nil && v.num.Cmp(u.num) == 0
	case KindBool:
		return v.boolean == u.boolean
	case KindList:
		return slices.EqualFunc(v.elems, u.elems, Value.Equal)
	case KindMap:
		return maps.EqualFunc(v.entries, u.entries, Value.Equal)
	case KindInvalid:
		return false
	}

	return false
}

// Get returns the value of the attribute, and the null value of its type when
// vs has no entry for it.
func (vs Values) Get(a Attribute) Value {
	if v, ok := vs[a.Name]; ok {
		return v
	}

	return NullOf(a.Type)
}
