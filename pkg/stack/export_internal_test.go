package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/outputshare"
)

// MaskedPaths returns the masked set of deployment: the value path of each
// secret in the inputs and outputs of every resource, with the same maskValue
// as the masked read. The tests hold the reveal lookup to this set.
func MaskedPaths(deployment []byte) ([]string, error) {
	doc, err := decodeDeployment(deployment)
	if err != nil {
		return nil, err
	}

	list := resourceElements(doc)

	var masked []string

	for index, elem := range list {
		obj, ok := elem.(map[string]any)
		if !ok || resourceField(obj, "urn") == "" {
			continue
		}

		inputs := obj["inputs"]
		if inputs == nil {
			inputs = map[string]any{}
		}

		outputs := obj["outputs"]
		if outputs == nil {
			outputs = map[string]any{}
		}

		base := "/resources/" + strconv.Itoa(index)

		maskValue(inputs, base+"/inputs", &masked)
		maskValue(outputs, base+"/outputs", &masked)
	}

	if masked == nil {
		masked = []string{}
	}

	return masked, nil
}

// resourceElements returns the elements of the resources array of doc, or nil
// when doc has no such array. The positions of the elements are the indices
// the reveal path names.
func resourceElements(doc any) []any {
	top, ok := doc.(map[string]any)
	if !ok {
		return nil
	}

	list, ok := top["resources"].([]any)
	if !ok {
		return nil
	}

	return list
}

// ParseRootOutputs returns the root outputs of deployment as the Outputs port
// reads them, so the external tests reach the parse without a store.
func ParseRootOutputs(deployment []byte) ([]outputshare.Output, error) {
	return rootOutputs(context.Background(), deployment)
}

// MapReplayBound returns the error that the build of update id gives for the
// replay error err.
func MapReplayBound(id UpdateID, err error) error {
	return new(Updates).mapReplayBound(id, err)
}

// MaskWorkBound runs the work scan of a masked read over v, whose own path
// has prefix bytes, so the external tests reach it without a deployment.
func MaskWorkBound(v any, prefix int) error {
	var budget maskBudget

	return checkMaskWork(&budget, v, prefix)
}

// DecodeForTest decodes a JSON text with no bound check, so a test hands the
// walk a document that the decoder of the reads would refuse.
func DecodeForTest(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()

	var doc any

	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return doc, nil
}

// MaskedPageAt answers the page of paths that starts at offset, as the masked
// read does after the cursor check. The tests call it with an offset that a
// signed cursor never holds, to reach the guard against a negative offset.
func MaskedPageAt(key []byte, paths []string, offset int) (MaskedPage, error) {
	bind := cursorPayload{Unit: "", Version: "1", Subject: "", Offset: 0, Expiry: 0}

	return maskedPageOf(key, bind, paths, offset, time.Unix(0, 0))
}

// CursorCheck names what a test cursor must bind: the unit, the version and
// the caller, and the time of the check.
type CursorCheck struct {
	Unit    string
	Version string
	Subject string
	Now     time.Time
}

// VerifyCursorOffset checks raw as the masked read does before it pages. The
// tests call it alone, so the check of the cursor answers without the guard of
// the page behind it.
func VerifyCursorOffset(key []byte, raw string, want CursorCheck) (int, error) {
	bind := cursorPayload{Unit: want.Unit, Version: want.Version, Subject: want.Subject, Offset: 0, Expiry: 0}

	return verifyCursor(key, raw, bind, want.Now)
}
