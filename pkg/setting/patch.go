package setting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Edit is the change of one key: a new database value, or the delete of the
// database value.
type Edit struct {
	Key string
	// Value is the text form of the new value. It is empty for a delete.
	Value  string
	Delete bool
}

// Patch is the edits of one change, in key order, with one edit for each
// key.
type Patch []Edit

// ParsePatch reads the body of a change: a JSON object, as a JSON merge
// patch, with one member for each key. A string, a bool or a whole number
// sets the database value of the key, and null deletes it. A key that is not
// declared at level, a value that fails the kind or the check of its
// declaration, a duplicate key, a nested value and trailing data are
// refused, and nothing is applied.
//
// An error never holds a value. It holds a key only when the key has the
// shape of a key, so a hostile body cannot put text in a log line.
func ParsePatch(body []byte, reg *Registry, level Level) (Patch, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%w: the body is not one JSON object", ErrInvalidPatch)
	}

	var patch Patch

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: the body is not one JSON object", ErrInvalidPatch)
		}

		key, isString := keyTok.(string)
		if !isString {
			return nil, fmt.Errorf("%w: the body is not one JSON object", ErrInvalidPatch)
		}

		edit, err := readEdit(dec, reg, level, key)
		if err != nil {
			return nil, err
		}

		if slices.ContainsFunc(patch, func(e Edit) bool { return e.Key == key }) {
			return nil, fmt.Errorf("%w: the key %s is twice in the body", ErrInvalidPatch, shown(key))
		}

		patch = append(patch, edit)
	}

	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, fmt.Errorf("%w: the body is not one JSON object", ErrInvalidPatch)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the object", ErrInvalidPatch)
	}

	slices.SortFunc(patch, func(a, b Edit) int { return strings.Compare(a.Key, b.Key) })

	return patch, nil
}

// readEdit reads the value of key and returns its edit.
func readEdit(dec *json.Decoder, reg *Registry, level Level, key string) (Edit, error) {
	decl, ok := reg.Lookup(level, key)

	tok, err := dec.Token()
	if err != nil {
		return Edit{}, fmt.Errorf("%w: the body is not one JSON object", ErrInvalidPatch)
	}

	if !ok {
		return Edit{}, fmt.Errorf("%w: %s", ErrUnknownKey, shown(key))
	}

	if tok == nil {
		return Edit{Key: key, Value: "", Delete: true}, nil
	}

	text, err := textOf(decl, tok)
	if err != nil {
		return Edit{}, err
	}

	// Defense in depth: the store checks the value again when it applies the
	// edit. This check stays so that a caller of the service never sees a
	// value that the declaration refuses.
	if err := decl.CheckValue(text); err != nil {
		return Edit{}, err
	}

	return Edit{Key: key, Value: text, Delete: false}, nil
}

// textOf converts the JSON token of a value to the text form of the kind of
// decl.
func textOf(decl Declaration, tok json.Token) (string, error) {
	switch value := tok.(type) {
	case string:
		if decl.Kind == KindString {
			return value, nil
		}
	case bool:
		if decl.Kind == KindBool {
			return strconv.FormatBool(value), nil
		}
	case json.Number:
		if decl.Kind == KindInt {
			return value.String(), nil
		}
	default:
	}

	return "", keyError(ErrInvalidValue, decl.Key, "the value needs the kind "+string(decl.Kind))
}

// shown returns key when it has the shape of a key, and a fixed word
// otherwise.
func shown(key string) string {
	if len(key) <= MaxKeyBytes && keyPattern.MatchString(key) {
		return key
	}

	return "(a key that is not valid)"
}

// Keys returns the keys of the patch in order.
func (p Patch) Keys() []string {
	out := make([]string, 0, len(p))
	for _, e := range p {
		out = append(out, e.Key)
	}

	return out
}
