// Package schema describes the attributes of a provider configuration, a
// resource and a data source. Both protocol adapters translate this one
// description to the schema of their tool, so a provider has one source of
// truth for names, types and secrecy.
package schema

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalid reports a schema that breaks a rule of this package. The
// wrapped message names the attribute and the rule.
var ErrInvalid = errors.New("invalid schema")

// IDAttribute is the name of the attribute that identifies a resource. Every
// resource schema has it, as a computed string, or as a required string that
// forces a replace.
const IDAttribute = "id"

// Mode says who sets an attribute.
type Mode uint8

// The modes of an attribute.
const (
	// ModeInvalid is the zero value. A schema with it does not validate.
	ModeInvalid Mode = iota
	// ModeRequired: the user sets it in the configuration.
	ModeRequired
	// ModeOptional: the user can set it. When the user does not, it is null.
	ModeOptional
	// ModeComputed: the provider sets it. The user cannot.
	ModeComputed
	// ModeOptionalComputed: the user can set it. When the user does not,
	// the provider sets it and the plan keeps the value of the prior state.
	ModeOptionalComputed
)

// String returns the name of the mode as the declaration file spells it.
func (m Mode) String() string {
	switch m {
	case ModeRequired:
		return "required"
	case ModeOptional:
		return "optional"
	case ModeComputed:
		return "computed"
	case ModeOptionalComputed:
		return "optionalComputed"
	case ModeInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("mode(%d)", uint8(m))
	}
}

// UserSets reports whether the configuration can hold a value for the
// attribute.
func (m Mode) UserSets() bool {
	return m == ModeRequired || m == ModeOptional || m == ModeOptionalComputed
}

// Attribute is one named, typed value of a schema.
type Attribute struct {
	// Name is snake_case: a lowercase letter, then lowercase letters, digits
	// and underscores, at most 64 characters.
	Name string
	// Type is the type of the value.
	Type Type
	// Mode says who sets the value.
	Mode Mode
	// Sensitive masks the value in plans and logs. The Pulumi adapter marks
	// it as a secret, so the Pulumi CLI encrypts it in state. OpenTofu stores
	// it in state as plain text, as it does for every provider.
	Sensitive bool
	// WriteOnly sends the value to the API and never stores it in state. A
	// change to it alone does not show in a plan. Only a user-set attribute
	// can be write-only. The engines mask a write-only value in diagnostics
	// and logs as they mask a sensitive value.
	WriteOnly bool
	// Replace makes a change to the value replace the resource, not update
	// it in place.
	Replace bool
	// Stable marks a computed or optional computed value that an update in
	// place never changes, for example a creation time. The plan of an
	// update keeps its prior value. Without the flag, the plan of an update
	// makes the value unknown, because an API can change it on any write,
	// as it does with an update time, an etag or a version.
	Stable bool
	// Description is the text that the tool's documentation shows.
	Description string
}

// Schema is the ordered list of attributes of one configuration, resource or
// data source.
type Schema struct {
	// Version is the version of the stored shape. A provider increments it
	// when a change needs an upgrade of the values in existing state.
	Version int
	// Attributes keep the order of the declaration, so generated
	// documentation keeps it too.
	Attributes []Attribute
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidName reports whether name is a valid attribute, resource or data
// source name.
func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

var providerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)

// ValidProviderName reports whether name is a valid provider type name:
// lowercase letters and digits. The name "dustpan" is not valid, because the
// file form of a secret then gives a variable DUSTPAN_<ATTR>_FILE, inside the
// DUSTPAN_ prefix that the launcher reserves.
func ValidProviderName(name string) bool {
	return name != "dustpan" && providerNamePattern.MatchString(name)
}

// Lookup returns the attribute with the name, and false when the schema has
// none.
func (s Schema) Lookup(name string) (Attribute, bool) {
	for _, a := range s.Attributes {
		if a.Name == name {
			return a, true
		}
	}

	var zero Attribute

	return zero, false
}

// Validate checks the rules that do not depend on the kind of schema. It
// returns an error that wraps ErrInvalid and names the first broken rule.
func (s Schema) Validate() error {
	if s.Version < 0 {
		return fmt.Errorf("%w: version %d is negative", ErrInvalid, s.Version)
	}

	seen := make(map[string]bool, len(s.Attributes))
	for _, a := range s.Attributes {
		if !ValidName(a.Name) {
			return fmt.Errorf("%w: attribute name %q is not snake_case", ErrInvalid, a.Name)
		}

		if seen[a.Name] {
			return fmt.Errorf("%w: attribute %q is declared twice", ErrInvalid, a.Name)
		}

		seen[a.Name] = true

		if err := a.validate(); err != nil {
			return err
		}
	}

	return nil
}

// ValidateResource checks Validate and the rules of a resource schema: it has
// an "id" string that the provider computes, or that the user sets and that
// forces a replace. An object cannot change its id in place.
func (s Schema) ValidateResource() error {
	if err := s.Validate(); err != nil {
		return err
	}

	id, ok := s.Lookup(IDAttribute)
	if !ok {
		return fmt.Errorf("%w: a resource needs the attribute %q", ErrInvalid, IDAttribute)
	}

	if id.Type.Kind != KindString {
		return fmt.Errorf("%w: attribute %q must be a string", ErrInvalid, IDAttribute)
	}

	if id.Sensitive || id.WriteOnly {
		return fmt.Errorf("%w: attribute %q cannot be sensitive or write-only", ErrInvalid, IDAttribute)
	}

	if id.Mode != ModeComputed && (id.Mode != ModeRequired || !id.Replace) {
		return fmt.Errorf("%w: attribute %q must be computed, or required with replace", ErrInvalid, IDAttribute)
	}

	return nil
}

// ValidateDataSource checks Validate and the rules of a data source schema:
// no attribute is write-only, forces a replace or is stable. A data source
// has no state to keep a value from, and no object to replace.
func (s Schema) ValidateDataSource() error {
	if err := s.Validate(); err != nil {
		return err
	}

	for _, a := range s.Attributes {
		if a.WriteOnly || a.Replace || a.Stable {
			return fmt.Errorf("%w: data source attribute %q cannot be write-only, replace or stable",
				ErrInvalid, a.Name)
		}
	}

	return nil
}

func (a Attribute) validate() error {
	if err := a.Type.Validate(); err != nil {
		return fmt.Errorf("attribute %q: %w", a.Name, err)
	}

	switch a.Mode {
	case ModeRequired, ModeOptional, ModeComputed, ModeOptionalComputed:
	case ModeInvalid:
		return fmt.Errorf("%w: attribute %q has no mode", ErrInvalid, a.Name)
	default:
		return fmt.Errorf("%w: attribute %q has an unknown mode", ErrInvalid, a.Name)
	}

	return a.validateFlags()
}

// validateFlags checks the flags against the mode.
func (a Attribute) validateFlags() error {
	if a.WriteOnly && a.Mode != ModeRequired && a.Mode != ModeOptional {
		return fmt.Errorf("%w: write-only attribute %q must be required or optional", ErrInvalid, a.Name)
	}

	if a.Replace && a.Mode == ModeComputed {
		return fmt.Errorf("%w: computed attribute %q cannot force a replace", ErrInvalid, a.Name)
	}

	if a.Replace && a.WriteOnly {
		return fmt.Errorf("%w: write-only attribute %q cannot force a replace", ErrInvalid, a.Name)
	}

	if a.Stable && a.Mode != ModeComputed && a.Mode != ModeOptionalComputed {
		return fmt.Errorf("%w: only a computed attribute can be stable, not %q", ErrInvalid, a.Name)
	}

	return nil
}
