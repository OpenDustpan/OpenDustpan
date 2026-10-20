package preferences

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
)

// Patch is a JSON merge patch of the preferences, by flat field name. A nil
// value sets the field back to its default. A non-nil value sets the field. A
// field that the map does not hold keeps its value.
type Patch map[string]*string

// Text returns a pointer to s, to set a field in a Patch.
func Text(s string) *string { return new(s) }

// ParsePatch reads the body of a PATCH request. It accepts one JSON object
// that names each field at most one time, and refuses updatedAt, a field that
// the document does not have, and a value that is not a string or null. A
// terminalSchemes member is null (both schemes back to the default) or an
// object with the members light and dark. It also checks each value, so the
// patch that it returns passes Validate.
func ParsePatch(body []byte) (Patch, error) {
	if len(body) > MaxBodyBytes {
		return nil, ErrBodyTooLarge
	}

	members, err := objectMembers(body)
	if err != nil {
		return nil, err
	}

	patch := Patch{}

	// First the structure of every member, then the values, so that the
	// answer for a body does not depend on the order of its members.
	if _, readOnly := members[FieldUpdatedAt]; readOnly {
		return nil, ErrReadOnlyField
	}

	for _, name := range sortedKeys(members) {
		if err := addMember(patch, name, members[name]); err != nil {
			return nil, err
		}
	}

	if err := patch.Validate(); err != nil {
		return nil, err
	}

	return patch, nil
}

// addMember adds the member name of a body to patch.
func addMember(patch Patch, name string, raw json.RawMessage) error {
	if name == FieldTerminalSchemes {
		return addSchemes(patch, raw)
	}

	// A flat name such as terminalSchemes.light is not a name on the wire: the
	// object of terminalSchemes holds it.
	if _, known := specOf(name); !known || strings.Contains(name, ".") {
		return ErrUnknownField
	}

	v, err := textOrNull(name, raw)
	if err != nil {
		return err
	}

	patch[name] = v

	return nil
}

// addSchemes adds the terminalSchemes member of a body: null resets both
// schemes, and an object changes the schemes that it names.
func addSchemes(patch Patch, raw json.RawMessage) error {
	if isNull(raw) {
		patch[FieldTerminalSchemeLight] = nil
		patch[FieldTerminalSchemeDark] = nil

		return nil
	}

	notObject := &FieldError{Field: FieldTerminalSchemes, Err: reason("terminalSchemes must be an object or null")}

	if first := firstByte(raw); first != '{' {
		return notObject
	}

	inner, err := objectMembers(raw)
	if err != nil {
		return notObject
	}

	for _, name := range sortedKeys(inner) {
		flat := FieldTerminalSchemes + "." + name
		if name != "light" && name != "dark" {
			return ErrUnknownField
		}

		v, err := textOrNull(flat, inner[name])
		if err != nil {
			return err
		}

		patch[flat] = v
	}

	return nil
}

// textOrNull reads the value of a field: a JSON string, or null.
func textOrNull(field string, raw json.RawMessage) (*string, error) {
	if isNull(raw) {
		return nil, nil //nolint:nilnil // A nil value is the null of a merge patch.
	}

	if firstByte(raw) != '"' {
		return nil, &FieldError{Field: field, Err: reason(field + " must be a string or null")}
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, &FieldError{Field: field, Err: reason(field + " must be a string or null")}
	}

	return &s, nil
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func firstByte(raw json.RawMessage) byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0
	}

	return raw[0]
}

// objectMembers reads body as one JSON object and returns its members, with
// the raw value of each. It returns ErrInvalidBody for any other JSON value,
// for text after the object, and for a member that appears two times: with
// two members of one name, parsers disagree about the value.
func objectMembers(body []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(body))

	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, ErrInvalidBody
	}

	members := map[string]json.RawMessage{}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, ErrInvalidBody
		}

		key, isText := keyTok.(string)
		if !isText {
			return nil, ErrInvalidBody
		}

		if _, dup := members[key]; dup {
			return nil, ErrInvalidBody
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, ErrInvalidBody
		}

		members[key] = raw
	}

	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, ErrInvalidBody
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidBody
	}

	return members, nil
}

// Validate checks every field of the patch. It returns ErrUnknownField for a
// name that is not a flat field name (updatedAt and terminalSchemes included),
// and a *FieldError for a value that is not allowed. The check runs in order
// of the field names, so the same patch always gets the same error.
//
// Defense in depth: ParsePatch checks the values too. Validate stays so that
// a caller that builds a Patch in code, and each layer below the route, still
// checks every value.
func (p Patch) Validate() error {
	for _, name := range sortedKeys(p) {
		if _, known := specOf(name); !known {
			return ErrUnknownField
		}

		if v := p[name]; v != nil {
			if err := checkValue(name, *v); err != nil {
				return err
			}
		}
	}

	return nil
}

// Apply applies patch to current and returns the new settings and the flat
// names of the fields whose value changed, in order. It checks every value of
// patch and every value of the new settings, so a store that calls it never
// writes a value that failed a check, also when the stored document came from
// an older version or from a restore. It checks the new settings and not
// current, so a patch that sets a stored field that fails a check back to its
// default always works. It changes neither argument. A value equal to
// the default of its field is stored as no value, so two Settings are equal
// when they give the same document.
func Apply(current Settings, patch Patch) (Settings, []string, error) {
	if err := patch.Validate(); err != nil {
		return nil, nil, err
	}

	next := make(Settings, len(current)+len(patch))
	maps.Copy(next, current)

	for name, v := range patch {
		spec, _ := specOf(name)

		if v == nil || *v == spec.def {
			delete(next, name)

			continue
		}

		next[name] = *v
	}

	if err := next.Validate(); err != nil {
		return nil, nil, err
	}

	changed := []string{}

	for _, spec := range fieldSpecs {
		if current[spec.name] != next[spec.name] {
			changed = append(changed, spec.name)
		}
	}

	return next, changed, nil
}

// Validate checks every stored value. It returns ErrInvalidStored for a name
// that is not a field and for a value that is not allowed.
func (s Settings) Validate() error {
	for _, name := range sortedKeys(s) {
		if err := checkValue(name, s[name]); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidStored, err)
		}
	}

	return nil
}

// Encode returns the stored text of s: one JSON object with the flat field
// names as keys, in order.
func (s Settings) Encode() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}

	data, err := json.Marshal(map[string]string(s))
	if err != nil {
		return "", fmt.Errorf("encode preferences: %w", err)
	}

	return string(data), nil
}

// ParseSettings reads the stored text of a row. It returns ErrInvalidStored
// for text that is not one JSON object of strings with no repeated key, for a
// name that is not a field, and for a value that is not allowed. A value that
// equals the default of its field is valid and is dropped.
func ParseSettings(text string) (Settings, error) {
	if len(text) > MaxBodyBytes {
		return nil, fmt.Errorf("%w: longer than %d bytes", ErrInvalidStored, MaxBodyBytes)
	}

	members, err := objectMembers([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidStored, err)
	}

	out := Settings{}

	for _, name := range sortedKeys(members) {
		v, err := textOrNull(name, members[name])
		if err != nil || v == nil {
			return nil, fmt.Errorf("%w: a stored value is not a string", ErrInvalidStored)
		}

		spec, known := specOf(name)
		if !known {
			return nil, fmt.Errorf("%w: %w", ErrInvalidStored, ErrUnknownField)
		}

		if cerr := checkValue(name, *v); cerr != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidStored, cerr)
		}

		if *v != spec.def {
			out[name] = *v
		}
	}

	return out, nil
}

// ParseStored reads the stored text of a row for a read. It drops each field
// that fails its check, and each member that is not a field, and returns the
// names of the dropped fields, in order. For a member that is not a field it
// returns the fixed word "unknown-field" and never its name, because the name
// is text of a row that anyone who wrote the row chose: it can hold a line
// break, a control character or a forged log line, and the caller logs it. It
// never returns the value of a dropped member. Text that is not one JSON object of strings with no
// repeated key gives no settings, and the one name "document". So a stored
// value that fails a check never stops a person from reading or changing
// the preferences. ParseSettings is the strict form.
func ParseStored(text string) (Settings, []string) {
	if len(text) > MaxBodyBytes {
		return Settings{}, []string{storedDocument}
	}

	members, err := objectMembers([]byte(text))
	if err != nil {
		return Settings{}, []string{storedDocument}
	}

	out := Settings{}
	dropped := []string{}

	for _, name := range sortedKeys(members) {
		v, err := textOrNull(name, members[name])
		if err != nil || v == nil || checkValue(name, *v) != nil {
			dropped = append(dropped, droppedName(name))

			continue
		}

		if spec, _ := specOf(name); *v != spec.def {
			out[name] = *v
		}
	}

	return out, dropped
}

// storedDocument names a stored text that is not a document of members.
const storedDocument = "document"

// storedUnknown stands for the name of a stored member that is not a field.
const storedUnknown = "unknown-field"

// droppedName returns name when it is the name of a field, and else the fixed
// word storedUnknown.
//
// Defense in depth: the log of Load takes only what this function gives. The
// caller does not clean the name again, and this function does not rely on
// the log handler to escape it.
func droppedName(name string) string {
	if _, ok := specOf(name); ok {
		return name
	}

	return storedUnknown
}
