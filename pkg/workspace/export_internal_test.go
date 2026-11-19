package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// The reveal lookup over bytes and over an opener, and the masked set, for
// the tests of this package. Production code reveals through RevealVersion
// and reads the masked set through the masked read.

// StateOpener is stateOpener for the tests.
type StateOpener = stateOpener

// RevealValueFromOpener is revealFromOpener for the tests.
func RevealValueFromOpener(open StateOpener, marks SensitiveMarks, path string) (any, []string, error) {
	return revealFromOpener(context.Background(), open, marks, path)
}

// RevealValue returns the plain value at path of state, and the masked set
// holding the path. The path must be in the masked set, else it fails with
// ErrResourceNotFound, so a caller cannot probe paths. A state the client
// encrypted fails with ErrClientEncrypted. It streams the state to the path
// and keeps no more than one resource instance in memory.
func RevealValue(state []byte, marks SensitiveMarks, path string) (any, []string, error) {
	return revealFromOpener(context.Background(), func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(state)), nil
	}, marks, path)
}

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
			site := instanceSite{resource: entry.index, position: i, address: tofuInstanceAddress(entry.obj, instance)}

			_, paths, err := maskTofuInstance(context.Background(), instance, site, marks)
			if err != nil {
				return nil, err
			}

			masked = append(masked, paths...)
		}
	}

	if masked == nil {
		masked = []string{}
	}

	return masked, nil
}

// InstanceWorkBound runs the work scan of a masked read over one instance, so
// the external tests reach it without a state.
func InstanceWorkBound(instance map[string]any, resource, position int) error {
	return checkInstanceWork(newMaskBudget(), instance, resource, position)
}

// InstancesWorkBound runs the work scan over each instance with one budget,
// as a masked read of a resource does.
func InstancesWorkBound(instances []map[string]any, resource int) error {
	budget := newMaskBudget()

	for position, instance := range instances {
		if err := checkInstanceWork(budget, instance, resource, position); err != nil {
			return err
		}
	}

	return nil
}

// MaskInstanceForTest runs maskTofuInstance for the tests.
func MaskInstanceForTest(
	ctx context.Context, instance map[string]any, address string, marks SensitiveMarks,
) (any, []string, error) {
	return maskTofuInstance(ctx, instance, instanceSite{resource: 0, position: 0, address: address}, marks)
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

// WithOneDigest returns ctx with the hook that gives every value of the
// secret index one digest.
func WithOneDigest(ctx context.Context) context.Context {
	return context.WithValue(ctx, oneDigestKey{}, true)
}
