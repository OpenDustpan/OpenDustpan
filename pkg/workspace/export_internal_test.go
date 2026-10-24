package workspace

import (
	"bytes"
	"io"
)

// The reveal lookup over bytes and over an opener, and the masked set, for
// the tests of this package. Production code reveals through RevealVersion
// and reads the masked set through the masked read.

// StateOpener is stateOpener for the tests.
type StateOpener = stateOpener

// RevealValueFromOpener is revealFromOpener for the tests.
func RevealValueFromOpener(open StateOpener, marks SensitiveMarks, path string) (any, []string, error) {
	return revealFromOpener(open, marks, path)
}

// RevealValue returns the plain value at path of state, and the masked set
// holding the path. The path must be in the masked set, else it fails with
// ErrResourceNotFound, so a caller cannot probe paths. A state the client
// encrypted fails with ErrClientEncrypted. It streams the state to the path
// and keeps no more than one resource instance in memory.
func RevealValue(state []byte, marks SensitiveMarks, path string) (any, []string, error) {
	return revealFromOpener(func() (io.ReadCloser, error) {
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
