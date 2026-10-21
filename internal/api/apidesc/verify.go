package apidesc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// errNotSameJSON means that a type and its wire type do not write the same JSON.
var errNotSameJSON = errors.New("the type and its wire type write different JSON")

// VerifyAs returns an error when the JSON of the sample is not the JSON that
// the type W reads and writes. It proves a declaration made with DeclareAs:
// the JSON of T decodes into W with no unknown member and no loss, and W
// writes the same JSON again. A test calls it with a sample that fills each
// member.
func VerifyAs[T, W any](sample T) error {
	first, err := json.Marshal(sample)
	if err != nil {
		return fmt.Errorf("encode %T: %w", sample, err)
	}

	var wire W

	dec := json.NewDecoder(bytes.NewReader(first))
	dec.DisallowUnknownFields()

	if err = dec.Decode(&wire); err != nil {
		return fmt.Errorf("decode the JSON of %T into %T: %w", sample, wire, err)
	}

	second, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("encode %T: %w", wire, err)
	}

	var a, b any
	if err = json.Unmarshal(first, &a); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	if err = json.Unmarshal(second, &b); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("%w: %T writes %s and %T writes %s", errNotSameJSON, sample, first, wire, second)
	}

	return nil
}
