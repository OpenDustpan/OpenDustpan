package apidesc

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Types of a query parameter. A parameter holds one scalar of the type, or a
// list of them when it repeats.
const (
	QueryString  = "string"
	QueryInteger = "integer"
	QueryBoolean = "boolean"
)

// QueryParam is one query parameter that a route reads. The label of the
// route declares it, the writer puts it in the description, and QueryOf gives
// the handler the value of a declared name only.
type QueryParam struct {
	// Name is the name in the query, in camelCase, for example pageSize.
	Name string
	// Type is one of the constants Query...
	Type string
	// Repeated is true for a parameter that the query can hold more than
	// once. The handler reads all its values.
	Repeated bool
	// Required is true for a parameter that a request must send.
	Required bool
	// Description says what the parameter does, for a person.
	Description string
}

// StringParam returns a query parameter that holds a string.
func StringParam(name, description string) QueryParam {
	return QueryParam{Name: name, Type: QueryString, Repeated: false, Required: false, Description: description}
}

// IntegerParam returns a query parameter that holds an integer.
func IntegerParam(name, description string) QueryParam {
	return QueryParam{Name: name, Type: QueryInteger, Repeated: false, Required: false, Description: description}
}

// BooleanParam returns a query parameter that holds true or false.
func BooleanParam(name, description string) QueryParam {
	return QueryParam{Name: name, Type: QueryBoolean, Repeated: false, Required: false, Description: description}
}

// Repeat returns the parameter as one that the query can hold more than once.
func (p QueryParam) Repeat() QueryParam {
	p.Repeated = true

	return p
}

// Require returns the parameter as one that a request must send.
func (p QueryParam) Require() QueryParam {
	p.Required = true

	return p
}

// queryName is the grammar of a query parameter: camelCase, as the generated
// clients need it.
var queryName = regexp.MustCompile(`^[a-z][a-zA-Z0-9]{0,63}$`)

// maxQueryDescription is the longest description of a query parameter.
const maxQueryDescription = 300

// validateQuery refuses a query parameter that breaks the grammar of a name,
// that has a type outside the allowlist or a description that is empty, too
// long or holds a control character, and a name that the route declares twice.
func validateQuery(o Operation) error {
	for i, p := range o.Query {
		if !queryName.MatchString(p.Name) {
			return fmt.Errorf(
				"%w: query parameter %q of %s %s is not camelCase",
				ErrInvalidOperation,
				p.Name,
				o.Method,
				o.Path,
			)
		}

		switch p.Type {
		case QueryString, QueryInteger, QueryBoolean:
		default:
			return fmt.Errorf("%w: query parameter %q has the type %q", ErrInvalidOperation, p.Name, p.Type)
		}

		if p.Description == "" || len(p.Description) > maxQueryDescription ||
			strings.ContainsFunc(p.Description, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return fmt.Errorf("%w: query parameter %q has a description that is not valid", ErrInvalidOperation, p.Name)
		}

		if slices.ContainsFunc(o.Query[:i], func(q QueryParam) bool { return q.Name == p.Name }) {
			return fmt.Errorf("%w: query parameter %q appears twice", ErrInvalidOperation, p.Name)
		}
	}

	return nil
}

// sortedQuery returns a copy of the parameters, sorted by name: the order of
// the description.
func sortedQuery(params []QueryParam) []QueryParam {
	out := slices.Clone(params)
	slices.SortFunc(out, func(a, b QueryParam) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// queryParameter returns the parameter object of one query parameter.
func queryParameter(p QueryParam) object {
	schema := object{keyType: p.Type}
	if p.Type == QueryInteger {
		schema["format"] = "int64"
	}

	if p.Repeated {
		schema = object{keyType: "array", "items": schema}
	}

	return object{
		keyName: p.Name, "in": "query", keyRequired: p.Required, keyDescription: p.Description, keySchema: schema,
	}
}
