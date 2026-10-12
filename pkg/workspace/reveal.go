package workspace

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Reveal of one secret value (ADR 41 decisions 4 and 5): the caller names a
// value path of a state file, the server answers the plain value only when
// the path is in the masked set of the masked state read. The masked set
// here is the union of the masked paths of every resource, built with the
// same maskTofuInstance as the read, so no second masking path exists.

// MaskedPaths returns the masked set of state: the value path of each marker
// of every resource instance, masked with marks. With no kept marks for the
// version, every attribute value is a marker.
func MaskedPaths(state []byte, marks SensitiveMarks) ([]string, error) {
	doc, err := decodeState(state)
	if err != nil {
		return nil, err
	}

	entries, _ := tofuStateResources(doc)

	var masked []string

	for _, entry := range entries {
		for i, instance := range tofuInstances(entry.obj) {
			_, paths := maskTofuInstance(
				instance, entry.index, i, tofuInstanceAddress(entry.obj, instance), marks,
			)
			masked = append(masked, paths...)
		}
	}

	if masked == nil {
		masked = []string{}
	}

	return masked, nil
}

// lookupStatePointer returns the value at the absolute JSON Pointer path in
// the state document. Array indices are decimal, with RFC 6901 escaping.
func lookupStatePointer(doc any, path string) (any, bool) {
	if path == "" || path[0] != '/' {
		return nil, false
	}

	tokens := strings.Split(path[1:], "/")
	for i := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(tokens[i], "~1", "/"), "~0", "~")
	}

	node := doc

	for _, token := range tokens {
		switch typed := node.(type) {
		case map[string]any:
			next, ok := typed[token]
			if !ok {
				return nil, false
			}

			node = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}

			node = typed[index]
		default:
			return nil, false
		}
	}

	return node, true
}

// RevealValue returns the plain value at path of state, and the masked set
// of the state. The path must be in the masked set, else it fails with
// ErrResourceNotFound, so a caller cannot probe paths. A state the client
// encrypted fails with ErrClientEncrypted.
func RevealValue(state []byte, marks SensitiveMarks, path string) (any, []string, error) {
	if encrypted, err := IsClientEncrypted(state); err != nil {
		return nil, nil, err
	} else if encrypted {
		return nil, nil, fmt.Errorf("reveal of a client-encrypted state: %w", ErrClientEncrypted)
	}

	masked, err := MaskedPaths(state, marks)
	if err != nil {
		return nil, nil, err
	}

	if !slices.Contains(masked, path) {
		return nil, masked, fmt.Errorf("%w: %q", ErrResourceNotFound, path)
	}

	doc, err := decodeState(state)
	if err != nil {
		return nil, masked, err
	}

	value, ok := lookupStatePointer(doc, path)
	if !ok {
		return nil, masked, fmt.Errorf("%w: %q", ErrResourceNotFound, path)
	}

	// A marker never answers: the plain state holds the plain value at a
	// masked path. A marker-shaped real value is still a real value.
	if isStateMarker(value) {
		return nil, masked, fmt.Errorf("%w: %q", ErrResourceNotFound, path)
	}

	// The value travels through JSON, so numbers keep their exact encoding.
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, masked, fmt.Errorf("encode revealed value: %w", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()

	var out any

	if err := dec.Decode(&out); err != nil {
		return nil, masked, fmt.Errorf("decode revealed value: %w", err)
	}

	return out, masked, nil
}

// isStateMarker reports whether v is a secret marker of the masked read: an
// object with the secret flag. The plain state never holds one at a masked
// path; a real value with the same shape but no entry in the masked set is a
// real value and never reaches this check.
func isStateMarker(v any) bool {
	obj, ok := v.(map[string]any)
	if !ok {
		return false
	}

	flag, ok := obj[markerSecretKey].(bool)

	return ok && flag
}
