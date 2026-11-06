package errcode

import (
	"errors"
	"fmt"
	"maps"
	"time"
	"unicode"
	"unicode/utf8"
)

// The limits of the arguments of one answer.
const (
	// MaxArgs is the largest number of arguments of a code.
	MaxArgs = 8
	// MaxIdentifier is the largest length of an identifier, in characters.
	MaxIdentifier = 256
	// MaxQuantity is the largest magnitude of a quantity: a client that reads
	// JSON numbers as IEEE 754 doubles keeps every integer up to it.
	MaxQuantity = 1<<53 - 1
)

// The errors of New. Each one is a bug in the adapter that
// builds the answer, not in the request.
var (
	ErrUnknownCode   = errors.New("the code is not in the catalog")
	ErrTooManyArgs   = errors.New("the code has more arguments than the limit")
	ErrArgUndeclared = errors.New("the code does not declare the argument")
	ErrArgRepeated   = errors.New("the argument is given two times")
	ErrArgMissing    = errors.New("a declared argument is missing")
	ErrArgKind       = errors.New("the argument has another kind than the code declares")
	ErrArgValue      = errors.New("the value of the argument is not valid")
)

// Value is one error argument of an answer. Make it with Identifier, Quantity
// or Time.
type Value struct {
	name   string
	kind   Kind
	text   string
	number int64
	at     time.Time
	limit  int
}

// Identifier returns the argument name with a name as its value, for example
// the name of a stack. It is never a sentence and never a value of the
// request body.
func Identifier(name, value string) Value {
	return Value{name: name, kind: KindIdentifier, text: value, number: 0, at: time.Time{}, limit: MaxIdentifier}
}

// IdentifierUpTo is Identifier with its own length limit, in characters, for
// an argument that holds a qualified name longer than MaxIdentifier. A limit
// below MaxIdentifier is MaxIdentifier. The limit belongs to this one value, so
// every other identifier keeps MaxIdentifier.
func IdentifierUpTo(name, value string, limit int) Value {
	return Value{
		name: name, kind: KindIdentifier, text: value, number: 0, at: time.Time{},
		limit: max(limit, MaxIdentifier),
	}
}

// Quantity returns the argument name with an integer as its value, for
// example a count or a limit.
func Quantity(name string, value int64) Value {
	return Value{name: name, kind: KindQuantity, text: "", number: value, at: time.Time{}, limit: 0}
}

// Time returns the argument name with an instant as its value.
func Time(name string, value time.Time) Value {
	return Value{name: name, kind: KindTime, text: "", number: 0, at: value, limit: 0}
}

// Answer is the code and the arguments of one error answer. New is the only
// way to make an answer with a code, so an answer always holds a code of the
// catalog and the arguments that the code declares. The adapter copies Code
// to the code field of its shape and Args to its arguments field. Answer has
// no JSON form of its own: the names of these fields differ in each shape.
type Answer struct {
	code string
	args map[string]any
}

// Code returns the code of the answer.
func (a Answer) Code() string { return a.code }

// Args returns a copy of the arguments of the answer: a string for an
// identifier and a time, an int64 for a quantity. It returns nil for a code
// without arguments.
func (a Answer) Args() map[string]any {
	if a.args == nil {
		return nil
	}

	return maps.Clone(a.args)
}

// New returns the answer of the catalog code with values. It fails when the
// catalog does not have the code, and unless values give each declared
// argument exactly once, with its kind and a valid value.
func New(code string, values ...Value) (Answer, error) {
	spec, ok := Lookup(code)
	if !ok {
		return Answer{code: "", args: nil}, fmt.Errorf("%w: %q", ErrUnknownCode, code)
	}

	return spec.answer(values...)
}

// answer returns the answer of s with values.
func (s Spec) answer(values ...Value) (Answer, error) {
	none := Answer{code: "", args: nil}

	if len(s.Args) > MaxArgs {
		return none, fmt.Errorf("%w: %s has %d", ErrTooManyArgs, s.Code, len(s.Args))
	}

	if len(s.Args) == 0 && len(values) == 0 {
		return Answer{code: s.Code, args: nil}, nil
	}

	args := make(map[string]any, len(values))

	for _, v := range values {
		arg, ok := s.arg(v.name)
		if !ok {
			return none, fmt.Errorf("%w: %s has no argument %q", ErrArgUndeclared, s.Code, v.name)
		}

		if _, seen := args[v.name]; seen {
			return none, fmt.Errorf("%w: %s, %q", ErrArgRepeated, s.Code, v.name)
		}

		if arg.Kind != v.kind {
			return none, fmt.Errorf("%w: %s, %q", ErrArgKind, s.Code, v.name)
		}

		wire, err := v.wire()
		if err != nil {
			return none, fmt.Errorf("%s, %q: %w", s.Code, v.name, err)
		}

		args[v.name] = wire
	}

	for _, arg := range s.Args {
		if _, ok := args[arg.Name]; !ok {
			return none, fmt.Errorf("%w: %s, %q", ErrArgMissing, s.Code, arg.Name)
		}
	}

	return Answer{code: s.Code, args: args}, nil
}

// arg returns the declared argument name.
func (s Spec) arg(name string) (Arg, bool) {
	for _, a := range s.Args {
		if a.Name == name && name != "" {
			return a, true
		}
	}

	return Arg{Name: "", Kind: 0}, false
}

// wire returns the JSON value of v, or ErrArgValue.
func (v Value) wire() (any, error) {
	switch v.kind {
	case KindIdentifier:
		if !validIdentifier(v.text, v.limit) {
			return nil, ErrArgValue
		}

		return v.text, nil
	case KindQuantity:
		if v.number > MaxQuantity || v.number < -MaxQuantity {
			return nil, ErrArgValue
		}

		return v.number, nil
	case KindTime:
		if v.at.IsZero() {
			return nil, ErrArgValue
		}

		return v.at.UTC().Format(time.RFC3339Nano), nil
	default:
		return nil, ErrArgValue
	}
}

// validIdentifier reports whether s is 1 to limit characters of
// valid UTF-8 with no control character. A line break or a control character
// can only come from text that is not a name.
func validIdentifier(s string, limit int) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit {
		return false
	}

	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}

	return true
}
