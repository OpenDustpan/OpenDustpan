package stack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// rootStackType is the type of the automatic stack resource. A deployment
// with only this resource holds no managed resources.
const rootStackType = "pulumi:pulumi:Stack"

// errNotDeployment reports text that is not a deployment object with a
// resources array. The count maps every decode failure to a count, so this
// error never escapes the package.
var errNotDeployment = errors.New("not a deployment object with a resources array")

// CountManagedResources counts the resources in deployment beyond the
// automatic stack resource. A deployment that does not parse counts as one
// managed resource, so a delete without force cannot drop state the server
// cannot read.
//
// It decodes the resources array as a stream and keeps one entry at a time,
// so the save path holds no second copy of the deployment: the input stays
// the only decompressed copy.
func CountManagedResources(deployment []byte) int64 {
	n, err := countTopResources(json.NewDecoder(bytes.NewReader(deployment)))
	if err != nil {
		return 1
	}

	return n
}

// countTopResources counts the managed resources of the deployment dec reads:
// the entries of its resources array beyond the automatic stack resource. A
// missing array counts as empty, as encoding/json does; anything else that is
// not an object with an array fails.
func countTopResources(dec *json.Decoder) (int64, error) {
	tok, err := nextToken(dec)
	if err != nil {
		return 0, err
	}

	if tok == nil {
		return 0, nil
	}

	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return 0, errNotDeployment
	}

	return countTopFields(dec)
}

// countTopFields counts the managed resources of the object dec points at.
// The last resources array wins, as encoding/json overwrites duplicates, and
// trailing text after the object fails, as encoding/json rejects it.
func countTopFields(dec *json.Decoder) (int64, error) {
	var n int64

	for dec.More() {
		key, keyErr := objectKey(dec)
		if keyErr != nil {
			return 0, keyErr
		}

		if !strings.EqualFold(key, "resources") {
			if skipErr := skipJSONValue(dec); skipErr != nil {
				return 0, skipErr
			}

			continue
		}

		counted, countErr := countResourcesArray(dec)
		if countErr != nil {
			return 0, countErr
		}

		n = counted
	}

	if _, err := nextToken(dec); err != nil {
		return 0, err
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return 0, errNotDeployment
	}

	return n, nil
}

// countResourcesArray counts the managed resources of the array dec points
// at: every entry whose type differs from the automatic stack resource. A
// null array counts as empty, as encoding/json does.
func countResourcesArray(dec *json.Decoder) (int64, error) {
	tok, err := nextToken(dec)
	if err != nil {
		return 0, err
	}

	if tok == nil {
		return 0, nil
	}

	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return 0, errNotDeployment
	}

	var n int64

	for dec.More() {
		managed, err := isManagedResource(dec)
		if err != nil {
			return 0, err
		}

		if managed {
			n++
		}
	}

	if _, err := nextToken(dec); err != nil {
		return 0, err
	}

	return n, nil
}

// isManagedResource reports whether the object dec points at is a managed
// resource: an entry whose type differs from the automatic stack resource. An
// entry without a type counts as managed, as encoding/json leaves it empty.
func isManagedResource(dec *json.Decoder) (bool, error) {
	tok, err := nextToken(dec)
	if err != nil {
		return false, err
	}

	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return false, errNotDeployment
	}

	typ := ""

	for dec.More() {
		key, keyErr := objectKey(dec)
		if keyErr != nil {
			return false, keyErr
		}

		if !strings.EqualFold(key, "type") {
			if skipErr := skipJSONValue(dec); skipErr != nil {
				return false, skipErr
			}

			continue
		}

		// The token carries the string directly. A Decode call here would
		// rebuild the decoder state per resource and hold a second copy of
		// the deployment on the heap; the token keeps the save path to one
		// decompressed copy.
		text, textErr := resourceType(dec)
		if textErr != nil {
			return false, textErr
		}

		typ = text
	}

	if _, err := nextToken(dec); err != nil {
		return false, err
	}

	return typ != rootStackType, nil
}

// resourceType reads the value of the type key dec points at: the text, or
// empty when the value is null, which counts as no type. A value of another
// shape fails, as decoding it into a string fails too.
func resourceType(dec *json.Decoder) (string, error) {
	value, err := nextToken(dec)
	if err != nil {
		return "", err
	}

	switch text, ok := value.(string); {
	case ok:
		return text, nil
	case value == nil:
		return "", nil
	default:
		return "", fmt.Errorf("decode deployment: %w", errNotDeployment)
	}
}

// objectKey reads one object key of dec.
func objectKey(dec *json.Decoder) (string, error) {
	tok, err := nextToken(dec)
	if err != nil {
		return "", err
	}

	key, ok := tok.(string)
	if !ok {
		return "", errNotDeployment
	}

	return key, nil
}

// nextToken reads one token of dec, wrapping a decode failure.
func nextToken(dec *json.Decoder) (json.Token, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("decode deployment: %w", err)
	}

	return tok, nil
}

// skipJSONValue skips one value of dec without keeping it: a scalar reads one
// token, while an array or object reads through its closing delimiter. It
// keeps no decoded value, so skipping a large subtree stays small.
func skipJSONValue(dec *json.Decoder) error {
	tok, err := nextToken(dec)
	if err != nil {
		return err
	}

	if _, ok := tok.(json.Delim); !ok {
		return nil
	}

	depth := 1

	for depth > 0 {
		tok, err := nextToken(dec)
		if err != nil {
			return err
		}

		// The only delimiters are the brackets and braces: an opener nests
		// deeper, any other closes one level.
		if delim, ok := tok.(json.Delim); ok {
			if delim == '[' || delim == '{' {
				depth++
			} else {
				depth--
			}
		}
	}

	return nil
}
