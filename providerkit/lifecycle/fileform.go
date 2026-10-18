package lifecycle

import (
	"fmt"
	"strings"

	"opendustpan.org/providerkit/schema"
)

// FileSuffix names the file form of a sensitive configuration attribute. For
// each sensitive attribute "token", a provider also accepts "token_file", the
// path of a file that holds the value. A credential then need not appear in
// the configuration of the tool at all. This form is for local use: a saved
// plan and the state of the Pulumi CLI hold the path. On a runner, the
// variable of FileEnv names the file instead.
const FileSuffix = "_file"

// maxNameLength is the longest attribute name that schema.ValidName accepts.
const maxNameLength = 64

// WithFileForms returns the configuration schema that a provider serves for
// the schema base that its author declares. A declared provider and an SDK
// provider both call it, so both get the same file forms:
//   - Each sensitive attribute "token" gets an optional, not sensitive string
//     "token_file" after it.
//   - A required sensitive attribute becomes optional. CheckFileForms then
//     needs exactly one of "token" and "token_file".
//
// Errors: schema.ErrInvalid when base does not validate, when an attribute
// is not required or optional, when a sensitive attribute is not a string
// (a file holds text), when the name of a file form is longer than 64
// characters, or when base already has an attribute of that name.
func WithFileForms(base schema.Schema) (schema.Schema, error) {
	if err := base.Validate(); err != nil {
		return base, fmt.Errorf("configuration schema: %w", err)
	}

	out := schema.Schema{Version: base.Version, Attributes: make([]schema.Attribute, 0, len(base.Attributes))}

	for _, a := range base.Attributes {
		if a.Mode != schema.ModeRequired && a.Mode != schema.ModeOptional {
			return base, fmt.Errorf(
				"%w: configuration attribute %q must be required or optional",
				schema.ErrInvalid,
				a.Name,
			)
		}

		if !a.Sensitive {
			out.Attributes = append(out.Attributes, a)

			continue
		}

		if !a.Type.Equal(schema.String()) {
			return base, fmt.Errorf("%w: sensitive configuration attribute %q must be a string to have a file form",
				schema.ErrInvalid, a.Name)
		}

		name := a.Name + FileSuffix
		if len(name) > maxNameLength {
			return base, fmt.Errorf("%w: the file form %q of %q is longer than %d characters",
				schema.ErrInvalid, name, a.Name, maxNameLength)
		}

		if _, taken := base.Lookup(name); taken {
			return base, fmt.Errorf("%w: attribute %q is taken by the file form of %q", schema.ErrInvalid, name, a.Name)
		}

		if a.Mode == schema.ModeRequired {
			a.Mode = schema.ModeOptional
		}

		out.Attributes = append(out.Attributes, a, schema.Attribute{
			Name: name, Type: schema.String(), Mode: schema.ModeOptional,
			Sensitive: false, WriteOnly: false, Replace: false, Stable: false,
			Description: "The path of a file that holds the value of " + a.Name + ".",
		})
	}

	return out, nil
}

// FileEnv returns the name of the environment variable that holds the path
// of the file of the sensitive configuration attribute name of the provider:
// the provider name, "_", the attribute name and "_FILE", in upper case. For
// the attribute "token" of the provider "ipam" it is "IPAM_TOKEN_FILE". On a
// runner, the launcher sets it for the tool step, and the path then goes in
// no plan and in no state.
func FileEnv(provider, name string) string {
	return strings.ToUpper(provider + "_" + name + FileSuffix)
}

// CheckFileForms checks config against the schema base that the author
// declares, for each sensitive attribute and its file form: at most one of
// the two is set, and exactly one when base makes the attribute required.
// A required attribute with neither is valid when fromEnv reports that the
// variable of FileEnv for it names a file. Run it with CheckConfig on the
// schema of WithFileForms. Errors: ErrInvalidConfig.
func CheckFileForms(base schema.Schema, config schema.Values, fromEnv func(name string) bool) error {
	for _, a := range base.Attributes {
		if !a.Sensitive {
			continue
		}

		name := a.Name + FileSuffix
		value, file := !config.Get(a).IsNull(), isSet(config, name)

		switch {
		case value && file:
			return fmt.Errorf("%w: set %q or %q, not both", ErrInvalidConfig, a.Name, name)
		case !value && !file && a.Mode == schema.ModeRequired && !fromEnv(a.Name):
			return fmt.Errorf("%w: set %q or %q, or the variable of its file", ErrInvalidConfig, a.Name, name)
		default:
		}
	}

	return nil
}

func isSet(config schema.Values, name string) bool {
	v, ok := config[name]

	return ok && !v.IsNull()
}
