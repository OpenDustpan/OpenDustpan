package sdk

import (
	"fmt"
	"math"
	"math/big"
	"reflect"

	"opendustpan.org/providerkit/lifecycle"
	"opendustpan.org/providerkit/schema"
)

// Decode returns the model M that holds vs. A null or unknown value leaves
// its pointer field nil. A value that is partly unknown, for example a list
// with an unknown element, is an error. A number becomes an integer field only when it is a
// whole number in the range of the field: 1.5 or 300 for an int8 would lose
// a part of the value. A number becomes a float field rounded to the nearest
// float of the field. Errors: lifecycle.ErrInvalidConfig for a value that the field cannot
// hold, or a null required value; ErrInvalidModel for a struct that SchemaOf
// refuses.
func Decode[M any](vs schema.Values) (M, error) {
	var m M

	s, err := SchemaOf[M]()
	if err != nil {
		return m, err
	}

	rv := reflect.ValueOf(&m).Elem()

	for i, a := range taggedFields(rv.Type()) {
		v := vs.Get(s.Attributes[i])
		f := rv.Field(a)

		if v.IsNull() || !v.IsKnown() {
			if f.Kind() != reflect.Pointer {
				return m, fmt.Errorf("%w: attribute %q is required", lifecycle.ErrInvalidConfig, s.Attributes[i].Name)
			}

			continue
		}

		if !v.Type().Equal(s.Attributes[i].Type) || !v.WhollyKnown() {
			return m, fmt.Errorf("%w: attribute %q is not a known %s", lifecycle.ErrInvalidConfig,
				s.Attributes[i].Name, s.Attributes[i].Type)
		}

		if f.Kind() == reflect.Pointer {
			f.Set(reflect.New(f.Type().Elem()))
			f = f.Elem()
		}

		if err := decodeValue(v, f); err != nil {
			return m, fmt.Errorf("attribute %q: %w", s.Attributes[i].Name, err)
		}
	}

	return m, nil
}

// taggedFields returns the indexes of the tagged fields of t, in the order of
// SchemaOf.
func taggedFields(t reflect.Type) []int {
	var out []int

	for i := range t.NumField() {
		if _, ok := t.Field(i).Tag.Lookup(TagName); ok {
			out = append(out, i)
		}
	}

	return out
}

// decodeValue sets f, which is not a pointer, to the known, non-null value v.
func decodeValue(v schema.Value, f reflect.Value) error {
	switch f.Kind() {
	case reflect.String:
		s, _ := v.AsString()
		f.SetString(s)
	case reflect.Bool:
		f.SetBool(v.IsTrue())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return decodeInt(v, f)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return decodeUint(v, f)
	case reflect.Float32, reflect.Float64:
		return decodeFloat(v, f)
	case reflect.Slice:
		return decodeSlice(v, f)
	case reflect.Map:
		return decodeMap(v, f)
	case reflect.Invalid, reflect.Uintptr, reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan,
		reflect.Func, reflect.Interface, reflect.Pointer, reflect.Struct, reflect.UnsafePointer:
		return fmt.Errorf("%w: Go type %s has no attribute type", ErrInvalidModel, f.Type())
	default:
		return fmt.Errorf("%w: Go type %s is of an unknown kind", ErrInvalidModel, f.Type())
	}

	return nil
}

// decodeFloat rounds the number to the nearest value of the float field f.
// A float32 field rounds once, from the number, not through a float64.
func decodeFloat(v schema.Value, f reflect.Value) error {
	n, _ := v.AsNumber()

	var x float64
	if f.Kind() == reflect.Float32 {
		x32, _ := n.Float32()
		x = float64(x32)
	} else {
		x, _ = n.Float64()
	}

	if math.IsInf(x, 0) || f.OverflowFloat(x) {
		return fmt.Errorf("%w: %s is out of the range of %s", lifecycle.ErrInvalidConfig, n.Text('g', -1), f.Type())
	}

	f.SetFloat(x)

	return nil
}

func decodeInt(v schema.Value, f reflect.Value) error {
	n, _ := v.AsNumber()

	i, acc := n.Int64()
	if !n.IsInt() || acc != big.Exact || f.OverflowInt(i) {
		return fmt.Errorf("%w: %s is not a whole number in the range of %s",
			lifecycle.ErrInvalidConfig, n.Text('g', -1), f.Type())
	}

	f.SetInt(i)

	return nil
}

func decodeUint(v schema.Value, f reflect.Value) error {
	n, _ := v.AsNumber()

	u, acc := n.Uint64()
	if !n.IsInt() || acc != big.Exact || f.OverflowUint(u) {
		return fmt.Errorf("%w: %s is not a whole number in the range of %s",
			lifecycle.ErrInvalidConfig, n.Text('g', -1), f.Type())
	}

	f.SetUint(u)

	return nil
}

func decodeSlice(v schema.Value, f reflect.Value) error {
	elems := v.Elements()
	out := reflect.MakeSlice(f.Type(), len(elems), len(elems))

	for i, e := range elems {
		if e.IsNull() {
			return fmt.Errorf("%w: element %d is null", lifecycle.ErrInvalidConfig, i)
		}

		if err := decodeValue(e, out.Index(i)); err != nil {
			return fmt.Errorf("element %d: %w", i, err)
		}
	}

	f.Set(out)

	return nil
}

func decodeMap(v schema.Value, f reflect.Value) error {
	entries := v.Entries()
	out := reflect.MakeMapWithSize(f.Type(), len(entries))

	for k, e := range entries {
		if e.IsNull() {
			return fmt.Errorf("%w: entry %q is null", lifecycle.ErrInvalidConfig, k)
		}

		ev := reflect.New(f.Type().Elem()).Elem()
		if err := decodeValue(e, ev); err != nil {
			return fmt.Errorf("entry %q: %w", k, err)
		}

		out.SetMapIndex(reflect.ValueOf(k).Convert(f.Type().Key()), ev)
	}

	f.Set(out)

	return nil
}

// Encode returns the values of the model m. A nil pointer field is null.
// Errors: ErrInvalidModel for a struct that SchemaOf refuses, or for a float
// that is not a number or is infinite.
func Encode[M any](m M) (schema.Values, error) {
	s, err := SchemaOf[M]()
	if err != nil {
		return nil, err
	}

	rv := reflect.ValueOf(m)
	vs := make(schema.Values, len(s.Attributes))

	for i, a := range taggedFields(rv.Type()) {
		attr := s.Attributes[i]
		f := rv.Field(a)

		if f.Kind() == reflect.Pointer {
			if f.IsNil() {
				vs[attr.Name] = schema.NullOf(attr.Type)

				continue
			}

			f = f.Elem()
		}

		v, err := encodeValue(attr.Type, f)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", attr.Name, err)
		}

		vs[attr.Name] = v
	}

	return vs, nil
}

// encodeValue returns the value of f, which is not a pointer, as a value of t.
func encodeValue(t schema.Type, f reflect.Value) (schema.Value, error) {
	switch f.Kind() {
	case reflect.String:
		return schema.StringValue(f.String()), nil
	case reflect.Bool:
		return schema.BoolValue(f.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return schema.IntValue(f.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schema.NumberValue(new(big.Float).SetUint64(f.Uint())), nil
	case reflect.Float32, reflect.Float64:
		v, ok := schema.FloatValue(f.Float(), f.Type().Bits())
		if !ok {
			return schema.NullOf(t), fmt.Errorf("%w: %v is not a number value", ErrInvalidModel, f.Float())
		}

		return v, nil
	case reflect.Slice:
		return encodeSlice(t, f)
	case reflect.Map:
		return encodeMap(t, f)
	case reflect.Invalid, reflect.Uintptr, reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan,
		reflect.Func, reflect.Interface, reflect.Pointer, reflect.Struct, reflect.UnsafePointer:
		return schema.NullOf(t), fmt.Errorf("%w: Go type %s has no attribute type", ErrInvalidModel, f.Type())
	default:
		return schema.NullOf(t), fmt.Errorf("%w: Go type %s is of an unknown kind", ErrInvalidModel, f.Type())
	}
}

func encodeSlice(t schema.Type, f reflect.Value) (schema.Value, error) {
	if t.Elem == nil {
		return schema.NullOf(t), fmt.Errorf("%w: list type %s has no element type", ErrInvalidModel, t)
	}

	elems := make([]schema.Value, f.Len())

	for i := range f.Len() {
		e, err := encodeValue(*t.Elem, f.Index(i))
		if err != nil {
			return schema.NullOf(t), fmt.Errorf("element %d: %w", i, err)
		}

		elems[i] = e
	}

	return schema.ListValue(*t.Elem, elems...), nil
}

func encodeMap(t schema.Type, f reflect.Value) (schema.Value, error) {
	if t.Elem == nil {
		return schema.NullOf(t), fmt.Errorf("%w: map type %s has no element type", ErrInvalidModel, t)
	}

	entries := make(map[string]schema.Value, f.Len())

	for it := f.MapRange(); it.Next(); {
		e, err := encodeValue(*t.Elem, it.Value())
		if err != nil {
			return schema.NullOf(t), fmt.Errorf("entry %q: %w", it.Key().String(), err)
		}

		entries[it.Key().String()] = e
	}

	return schema.MapValue(*t.Elem, entries), nil
}
