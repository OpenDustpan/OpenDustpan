package clientgen

import "fmt"

// NameKind is the kind of a name that comes from the description.
type NameKind int

// The kinds of names, each with its own allowlist.
const (
	NameOperation NameKind = iota + 1
	NameSchema
	NameField
	NamePathParameter
	NameQueryParameter
	NameEnumValue
)

// CheckName checks a name against the allowlist of its kind. It returns
// ErrInvalidName, wrapped, for a name that is not on it. The parser uses the
// same allowlists, and a template set calls CheckName again for the names of a
// model that it gets, so that a model built by hand is checked too.
func CheckName(kind NameKind, name string) error {
	var ok bool

	switch kind {
	case NameOperation:
		ok = operationIDName.MatchString(name)
	case NameSchema:
		ok = schemaName.MatchString(name)
	case NameField, NameQueryParameter:
		ok = fieldName.MatchString(name)
	case NamePathParameter:
		ok = pathParamName.MatchString(name)
	case NameEnumValue:
		ok = enumValueName.MatchString(name)
	default:
	}

	if !ok {
		return fmt.Errorf("%w: %q (kind %d)", ErrInvalidName, name, kind)
	}

	return nil
}
