package provider

import (
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

// Type is the type of one field of a spec.
type Type string

// The types of a field. A duration is a string that time.ParseDuration reads,
// above zero.
const (
	TypeString   Type = "string"
	TypeNumber   Type = "number"
	TypeBoolean  Type = "boolean"
	TypeList     Type = "list"
	TypeMap      Type = "map"
	TypeDuration Type = "duration"
)

// Valid reports whether t is a type.
func (t Type) Valid() bool {
	switch t {
	case TypeString, TypeNumber, TypeBoolean, TypeList, TypeMap, TypeDuration:
		return true
	default:
		return false
	}
}

// Field describes one input or one output of an adapter.
type Field struct {
	Name        string
	Type        Type
	Description string
	Required    bool
	// Secret marks a field that holds a secret. An input of this field must
	// come from a secret. An output of this field must have the secret mark.
	Secret bool
}

// Spec describes the inputs and the outputs of an adapter. The save check,
// the console and the docs read it.
type Spec struct {
	Inputs  []Field
	Outputs []Field
	// MintsTokens says that the adapter asks the TokenSource for tokens of
	// the issuer. Run gives a TokenSource only to such an adapter.
	MintsTokens bool
}

// CheckSpec validates a spec: each field has a valid name, a valid type and a
// description, and no name repeats in the inputs or in the outputs. A login
// spec names at least one secret output.
func CheckSpec(k Kind, s *Spec) error {
	if err := checkFields("input", s.Inputs); err != nil {
		return err
	}

	if err := checkFields("output", s.Outputs); err != nil {
		return err
	}

	if k == KindLogin && !slices.ContainsFunc(s.Outputs, func(f Field) bool { return f.Secret }) {
		return fmt.Errorf("%w: a login spec needs a secret output", profile.ErrInvalid)
	}

	return nil
}

func checkFields(group string, fields []Field) error {
	seen := make(map[string]bool, len(fields))

	for i := range fields {
		f := &fields[i]
		if err := checkName(f.Name); err != nil {
			return fmt.Errorf("%s %d: %w", group, i, err)
		}

		if !f.Type.Valid() {
			return fmt.Errorf("%w: %s %s has an unknown type", profile.ErrInvalid, group, f.Name)
		}

		if f.Description == "" {
			return fmt.Errorf("%w: %s %s has no description", profile.ErrInvalid, group, f.Name)
		}

		if seen[f.Name] {
			return fmt.Errorf("%w: the %s %s repeats", profile.ErrInvalid, group, f.Name)
		}

		seen[f.Name] = true
	}

	return nil
}

// CheckInputs validates in against the inputs of s. It refuses an input that
// the spec does not name, a required input that is missing, a value of
// another type, and a plain value for a secret input. It calls no network.
// The errors wrap profile.ErrInvalid and name the input, never its value.
func CheckInputs(s *Spec, in Value) error {
	for name := range in {
		if !slices.ContainsFunc(s.Inputs, func(f Field) bool { return f.Name == name }) {
			return fmt.Errorf("%w: the input %s is not an input of the adapter", profile.ErrInvalid, safeName(name))
		}
	}

	for i := range s.Inputs {
		f := &s.Inputs[i]

		leaf, ok := in[f.Name]
		if !ok {
			if f.Required {
				return fmt.Errorf("%w: the input %s is required", profile.ErrInvalid, f.Name)
			}

			continue
		}

		if err := checkInput(f, leaf); err != nil {
			return err
		}
	}

	return nil
}

// MaxDepth is the deepest nesting of lists and maps in one value.
const MaxDepth = 32

// CheckTree checks that v and every value inside it is a string, a bool, a
// number (float64, int or int64), nil, a []any or a map[string]any, with at
// most MaxDepth levels. It returns profile.ErrInvalid for any other value, so
// that a value that a copy would share, for example a []string or a pointer,
// never reaches an adapter, a result or the caller.
func CheckTree(v any) error {
	return checkTreeAt(v, 0)
}

func checkTreeAt(v any, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: a value is nested deeper than %d levels", profile.ErrInvalid, MaxDepth)
	}

	switch x := v.(type) {
	case nil, string, bool, float64, int, int64:
		return nil
	case []any:
		for _, e := range x {
			if err := checkTreeAt(e, depth+1); err != nil {
				return err
			}
		}

		return nil
	case map[string]any:
		for _, e := range x {
			if err := checkTreeAt(e, depth+1); err != nil {
				return err
			}
		}

		return nil
	default:
		return fmt.Errorf("%w: a value holds a type other than string, bool, number, list or map",
			profile.ErrInvalid)
	}
}

// checkType returns profile.ErrInvalid when v is not of the type t.
// checkInput checks one input against its field.
func checkInput(f *Field, leaf Leaf) error {
	if f.Secret && !leaf.Secret {
		return fmt.Errorf("%w: the input %s must come from a secret", profile.ErrInvalid, f.Name)
	}

	if err := checkType(f.Type, leaf.Value); err != nil {
		return fmt.Errorf("the input %s: %w", f.Name, err)
	}

	if err := CheckTree(leaf.Value); err != nil {
		return fmt.Errorf("the input %s: %w", f.Name, err)
	}

	return nil
}

func checkType(t Type, v any) error {
	ok := false

	switch t {
	case TypeString:
		_, ok = v.(string)
	case TypeNumber:
		switch v.(type) {
		case float64, int, int64:
			ok = true
		default:
		}
	case TypeBoolean:
		_, ok = v.(bool)
	case TypeList:
		_, ok = v.([]any)
	case TypeMap:
		_, ok = v.(map[string]any)
	case TypeDuration:
		return checkDuration(v)
	default:
	}

	if !ok {
		return fmt.Errorf("%w: the value is not of the type %s", profile.ErrInvalid, t)
	}

	return nil
}

func checkDuration(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%w: the value is not of the type %s", profile.ErrInvalid, TypeDuration)
	}

	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fmt.Errorf("%w: the value is not a duration above zero, for example 15m", profile.ErrInvalid)
	}

	return nil
}

// checkName validates the name of a block, an adapter or a field: 1 to
// MaxNameBytes bytes of ASCII letters, digits, "-" and "_", that start with a
// letter. It returns profile.ErrInvalid, and never echoes a name that is not
// valid.
func checkName(name string) error {
	if name == "" || len(name) > MaxNameBytes {
		return fmt.Errorf("%w: a name has 1 to %d bytes", profile.ErrInvalid, MaxNameBytes)
	}

	if !isLetter(name[0]) {
		return fmt.Errorf("%w: a name starts with a letter", profile.ErrInvalid)
	}

	for i := range len(name) {
		if !isNameByte(name[i]) {
			return fmt.Errorf("%w: a name holds only letters, digits, - and _", profile.ErrInvalid)
		}
	}

	return nil
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isNameByte(c byte) bool { return isLetter(c) || c >= '0' && c <= '9' || c == '-' || c == '_' }

// safeName returns name for an error message when it is a valid name, and a
// fixed text when it is not. So a message never echoes an input that a
// caller controls and that can hold a secret or a control character.
func safeName(name string) string {
	if checkName(name) != nil {
		return "(a name that is not valid)"
	}

	return name
}
