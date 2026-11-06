package apidesc

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// argName is the form of the name of an argument: camelCase, with a capital
// letter allowed after any letter, because the catalog holds the name that is
// on the wire, for example objectID.
var argName = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,63}$`)

// ErrorAnswerSchema is the name of the schema that selects the shape of an
// error answer by its errorCode.
const ErrorAnswerSchema = "ErrorAnswer"

// ArgType is the type of an argument of an error code in the description.
type ArgType int

// The types of an argument.
const (
	// ArgString is a string.
	ArgString ArgType = iota + 1
	// ArgInteger is an integer.
	ArgInteger
	// ArgTime is a string that holds an instant in the form date-time.
	ArgTime
)

// ErrorArg is one argument of an error code.
type ErrorArg struct {
	Name string
	Type ArgType
}

// ErrorVariant is a code that carries arguments, with the arguments.
type ErrorVariant struct {
	Code string
	Args []ErrorArg
}

// WithErrorVariants states the codes that carry arguments. The description
// then holds one schema for each of these codes, with the typed arguments in
// errorArgs, and the schema ErrorAnswer. ErrorAnswer is a oneOf of these
// schemas and of ErrorResponse, with a discriminator on errorCode. A code with
// no arguments, and a code that a client does not know, has the shape of
// ErrorResponse. The caller passes the variants because this package cannot
// import the catalog of codes. The option needs WithErrorCodes, and each
// variant needs a code of that list.
func WithErrorVariants(variants []ErrorVariant) Option {
	return func(c *buildConfig) { c.errorVariants = slices.Clone(variants) }
}

// variantSchemaName returns the name of the schema of a code, for example
// LockLockedError for lock.locked.
func variantSchemaName(code string) string {
	var b strings.Builder

	for _, part := range strings.FieldsFunc(code, func(r rune) bool { return r == '.' || r == '-' }) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}

	b.WriteString("Error")

	return b.String()
}

// variantArgsName returns the name of the schema of the arguments of a code,
// for example LockLockedErrorArgs. The description names it because a client
// generator needs a name for each object.
func variantArgsName(code string) string { return variantSchemaName(code) + "Args" }

// checkedErrorVariants returns the variants sorted by code. It refuses a
// variant whose code is not in codes, a code listed twice, a variant with no
// argument, an argument that is invalid or listed twice, and a schema name
// that a schema of a body holds.
func checkedErrorVariants(variants []ErrorVariant, codes []string, bodies object) ([]ErrorVariant, error) {
	if len(variants) > 0 && len(codes) == 0 {
		return nil, fmt.Errorf("%w: error variants need error codes", ErrInvalidBody)
	}

	out := slices.Clone(variants)
	slices.SortFunc(out, func(a, b ErrorVariant) int { return strings.Compare(a.Code, b.Code) })

	// schemaCodes maps each generated schema name to the code that gave it.
	// Two codes can give one name, for example a.b-c and a-b.c.
	schemaCodes := map[string]string{}

	for i, v := range out {
		if i > 0 && out[i-1].Code == v.Code {
			return nil, fmt.Errorf("%w: error variant %q is listed twice", ErrInvalidBody, v.Code)
		}

		if err := checkedVariant(v, codes, bodies); err != nil {
			return nil, err
		}

		name := variantSchemaName(v.Code)

		if other, clash := schemaCodes[name]; clash {
			return nil, fmt.Errorf(
				"%w: error variants %q and %q give the same schema name", ErrInvalidBody, other, v.Code,
			)
		}

		schemaCodes[name] = v.Code
	}

	return out, nil
}

// checkedVariant refuses one variant that the writer cannot write.
func checkedVariant(v ErrorVariant, codes []string, bodies object) error {
	if !slices.Contains(codes, v.Code) {
		return fmt.Errorf("%w: error variant %q is not an error code", ErrInvalidBody, v.Code)
	}

	if len(v.Args) == 0 {
		return fmt.Errorf("%w: error variant %q has no argument", ErrInvalidBody, v.Code)
	}

	for _, name := range []string{variantSchemaName(v.Code), variantArgsName(v.Code)} {
		_, taken := bodies[name]

		if taken || !schemaName.MatchString(name) || name == errorSchema || name == errorCodeSchema ||
			name == ErrorAnswerSchema {
			return fmt.Errorf(
				"%w: error variant %q has the schema name %q, which is not free", ErrInvalidBody, v.Code, name,
			)
		}
	}

	return checkedVariantArgs(v)
}

// checkedVariantArgs refuses an argument name that is not in the form of a
// property name, a type that is not known, and a name that is listed twice.
func checkedVariantArgs(v ErrorVariant) error {
	seen := map[string]bool{}

	for _, a := range v.Args {
		if !argName.MatchString(a.Name) || seen[a.Name] {
			return fmt.Errorf("%w: error variant %q has the argument name %q, which is not valid or is listed twice",
				ErrInvalidBody, v.Code, a.Name)
		}

		seen[a.Name] = true

		if a.Type != ArgString && a.Type != ArgInteger && a.Type != ArgTime {
			return fmt.Errorf("%w: error variant %q, argument %q has an unknown type", ErrInvalidBody, v.Code, a.Name)
		}
	}

	return nil
}

// errorVariantSchemas returns the schema of each variant and ErrorAnswer. Each
// variant repeats the members of ErrorResponse, so that a client needs no
// allOf.
func errorVariantSchemas(variants []ErrorVariant) object {
	out := object{}
	if len(variants) == 0 {
		return out
	}

	branches := make([]any, 0, len(variants)+1)
	mapping := object{}

	for _, v := range variants {
		name := variantSchemaName(v.Code)
		ref := refSchemas + name
		out[name] = variantSchema(v)
		out[variantArgsName(v.Code)] = variantArgsSchema(v)
		branches = append(branches, object{keyRef: ref})
		mapping[v.Code] = ref
	}

	branches = append(branches, object{keyRef: refSchemas + errorSchema})

	out[ErrorAnswerSchema] = object{
		keyDescription: "An error answer, by errorCode. A code that carries arguments has its own schema, " +
			"with typed errorArgs. Any other code, also a code that a client does not know, has the shape " +
			"of ErrorResponse.",
		"oneOf":         branches,
		"discriminator": object{"propertyName": keyErrorCode, "mapping": mapping},
	}

	return out
}

// variantSchema returns the schema of one code.
func variantSchema(v ErrorVariant) object {
	return object{
		keyType:        typeObject,
		keyDescription: "The error answer with the code " + v.Code + ".",
		keyRequired:    []any{keyCode, keyMessage, keyErrorCode, keyErrorArgs},
		keyProperties: object{
			keyCode:      object{keyType: typeInteger, keyDescription: "The HTTP status."},
			keyMessage:   object{keyType: typeString, keyDescription: "A text for a person."},
			keyErrorCode: object{keyType: typeString, keyEnum: []any{v.Code}},
			keyErrorArgs: object{keyRef: refSchemas + variantArgsName(v.Code)},
		},
	}
}

// variantArgsSchema returns the schema of the arguments of one code. The
// catalog gives every argument to every answer of the code, so all are required.
func variantArgsSchema(v ErrorVariant) object {
	props := object{}
	names := make([]string, 0, len(v.Args))

	for _, a := range v.Args {
		names = append(names, a.Name)

		props[a.Name] = argSchema(a.Type)
	}

	slices.Sort(names)

	return object{
		keyType:        typeObject,
		keyDescription: "The arguments of the text of the error with the code " + v.Code + ".",
		keyProperties:  props,
		keyRequired:    stringsAsAny(names),
	}
}

// argSchema returns the schema of an argument type. It returns nil for a type
// that is not known; checkedVariantArgs refuses such a type before the writer
// gets here, and a nil schema would make the document invalid, not wide.
func argSchema(t ArgType) object {
	return map[ArgType]object{
		ArgString:  {keyType: typeString},
		ArgInteger: {keyType: typeInteger},
		ArgTime:    {keyType: typeString, "format": "date-time"},
	}[t]
}
