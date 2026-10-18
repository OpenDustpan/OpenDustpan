package declaration

import (
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/providerkit/schema"
)

// RefKind is the kind of a reference in a template.
type RefKind uint8

// The kinds of a reference.
const (
	// RefNone marks a literal part.
	RefNone RefKind = iota
	// RefAttribute is "{name}": an attribute of the resource or data source.
	RefAttribute
	// RefConfig is "{config.name}": an attribute of the provider
	// configuration.
	RefConfig
	// RefFile is "{file:config.name}": the path of a file that holds the
	// value of a configuration attribute.
	RefFile
)

// Ref is one reference in a template.
type Ref struct {
	// Kind is the kind of the reference.
	Kind RefKind
	// Name is the attribute name.
	Name string
}

// String returns the reference as the declaration spells it.
func (r Ref) String() string {
	switch r.Kind {
	case RefAttribute:
		return "{" + r.Name + "}"
	case RefConfig:
		return "{config." + r.Name + "}"
	case RefFile:
		return "{file:config." + r.Name + "}"
	case RefNone:
		return ""
	}

	return ""
}

// Part is a literal or a reference.
type Part struct {
	// Literal is the text of a literal part.
	Literal string
	// Ref is the reference of a reference part. Its kind is RefNone for a
	// literal part.
	Ref Ref
}

// Template is a string of literal parts and references.
type Template []Part

// ParseTemplate reads a string with references in braces. A "{" with no
// matching "}" and a reference of an unknown form are errors that wrap
// ErrInvalid. A template has no escape for a literal brace.
func ParseTemplate(s string) (Template, error) {
	var t Template

	for s != "" {
		start := strings.IndexAny(s, "{}")
		if start < 0 {
			t = append(t, Part{Literal: s, Ref: Ref{Kind: RefNone, Name: ""}})

			break
		}

		if s[start] == '}' {
			return nil, fmt.Errorf("%w: %q has a %q with no %q", ErrInvalid, s, "}", "{")
		}

		if start > 0 {
			t = append(t, Part{Literal: s[:start], Ref: Ref{Kind: RefNone, Name: ""}})
		}

		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			return nil, fmt.Errorf("%w: %q has a %q with no %q", ErrInvalid, s, "{", "}")
		}

		ref, err := parseRef(s[start+1 : start+end])
		if err != nil {
			return nil, err
		}

		t = append(t, Part{Literal: "", Ref: ref})
		s = s[start+end+1:]
	}

	return t, nil
}

func parseRef(body string) (Ref, error) {
	kind := RefAttribute
	name := body

	if rest, ok := strings.CutPrefix(body, "file:config."); ok {
		kind, name = RefFile, rest
	} else if rest, ok := strings.CutPrefix(body, "config."); ok {
		kind, name = RefConfig, rest
	}

	if !schema.ValidName(name) {
		return Ref{Kind: RefNone, Name: ""}, fmt.Errorf("%w: reference {%s} is not of a known form", ErrInvalid, body)
	}

	return Ref{Kind: kind, Name: name}, nil
}

// Refs returns the references of the template, in order.
func (t Template) Refs() []Ref {
	var refs []Ref

	for _, p := range t {
		if p.Ref.Kind != RefNone {
			refs = append(refs, p.Ref)
		}
	}

	return refs
}

// Whole returns the reference and true when the template is exactly one
// reference with no literal text.
func (t Template) Whole() (Ref, bool) {
	if len(t) == 1 && t[0].Ref.Kind != RefNone {
		return t[0].Ref, true
	}

	return Ref{Kind: RefNone, Name: ""}, false
}

// ErrOptionValue reports a value that would start a command argument with
// "-". The program could read it as an option.
var ErrOptionValue = errors.New("value starts an argument with a hyphen")

// Expand returns the template with each reference replaced by the value that
// lookup gives. It returns the error of lookup.
func (t Template) Expand(lookup func(Ref) (string, error)) (string, error) {
	var b strings.Builder

	for _, p := range t {
		if p.Ref.Kind == RefNone {
			b.WriteString(p.Literal)

			continue
		}

		v, err := lookup(p.Ref)
		if err != nil {
			return "", err
		}

		b.WriteString(v)
	}

	return b.String(), nil
}

// ExpandArg expands one command argument. When a reference starts the
// argument and the whole expanded argument starts with "-", it returns
// ErrOptionValue: a value cannot become an option of the program. This also
// holds when the first value is empty and a later value or literal gives the
// "-", as in "{a}{b}" or "{a}-x". A literal "-" at the start of the
// declaration, as in "--name={name}", is the author's own option and is
// allowed.
func (t Template) ExpandArg(lookup func(Ref) (string, error)) (string, error) {
	arg, err := t.Expand(lookup)
	if err != nil {
		return "", err
	}

	if len(t) > 0 && t[0].Ref.Kind != RefNone && strings.HasPrefix(arg, "-") {
		return "", fmt.Errorf("%w: %s", ErrOptionValue, t[0].Ref)
	}

	return arg, nil
}
