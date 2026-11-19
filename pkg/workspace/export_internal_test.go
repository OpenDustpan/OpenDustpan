package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// RevealValueFromOpenerContext is revealFromOpener with the context of the
// test.
func RevealValueFromOpenerContext(
	ctx context.Context, open StateOpener, marks SensitiveMarks, path string,
) (any, []string, error) {
	return revealFromOpener(ctx, open, marks, path)
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
	doc, err := decodeBoundedState(state)
	if err != nil {
		return nil, err
	}

	entries, _ := tofuStateResources(doc)

	var masked []string

	work := newMaskWork(nil)
	ctx := context.Background()

	for _, entry := range entries {
		for i, instance := range tofuInstances(entry.obj) {
			site := instanceSite{resource: entry.index, position: i, address: tofuInstanceAddress(entry.obj, instance)}

			_, paths, err := maskTofuInstance(ctx, work, instance, site, marks)
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
	return checkInstanceWork(context.Background(), newMaskWork(nil), instance, resource, position)
}

// InstancesWorkBound runs the work scan over each instance with one budget,
// as a masked read of a resource does.
func InstancesWorkBound(instances []map[string]any, resource int) error {
	ctx := context.Background()
	work := newMaskWork(nil)

	for position, instance := range instances {
		if err := checkInstanceWork(ctx, work, instance, resource, position); err != nil {
			return err
		}
	}

	return nil
}

// MaskInstanceForTest runs maskTofuInstance for the tests.
func MaskInstanceForTest(
	ctx context.Context, instance map[string]any, address string, marks SensitiveMarks,
) (any, []string, error) {
	site := instanceSite{resource: 0, position: 0, address: address}

	return maskTofuInstance(ctx, newMaskWork(nil), instance, site, marks)
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

// MaskWork is the count of work units of one maskTofuInstance call.
type MaskWork struct {
	// Pointers is the count of sensitive pointers that the mask pass visited.
	Pointers int
	// RouteSteps is the count of tokens that the applied-route lookups compared.
	RouteSteps int
	// ResolveSteps is the count of tokens that the pointer resolution walked.
	ResolveSteps int
	// Digests is the count of values that the secret index digested.
	Digests int
	// Comparisons is the count of deep equality checks of the duplicate pass.
	Comparisons int
}

// MaskResult is the result of MaskInstanceWorkForTest.
type MaskResult struct {
	// Attributes are the masked attributes.
	Attributes any
	// Masked holds each marker path.
	Masked []string
	// Work holds the work counts of the call.
	Work MaskWork
}

// MaskInstanceWorkForTest runs maskTofuInstance and returns its work counts.
func MaskInstanceWorkForTest(
	ctx context.Context, instance map[string]any, address string, marks SensitiveMarks,
) (MaskResult, error) {
	return maskWorkProbed(ctx, instance, address, marks, false)
}

// MaskInstanceWithOneDigestForTest runs maskTofuInstance with an index that
// gives every value one digest, so only the equality check can tell a
// duplicate from a collision.
func MaskInstanceWithOneDigestForTest(
	ctx context.Context, instance map[string]any, address string, marks SensitiveMarks,
) (any, []string, error) {
	result, err := maskWorkProbed(ctx, instance, address, marks, true)

	return result.Attributes, result.Masked, err
}

func maskWorkProbed(
	ctx context.Context, instance map[string]any, address string, marks SensitiveMarks, oneDigest bool,
) (MaskResult, error) {
	probe := &maskProbe{oneDigest: oneDigest, pointers: 0, routeSteps: 0, resolveSteps: 0, digests: 0, comparisons: 0}

	attributes, masked, err := maskTofuInstance(
		ctx, newMaskWork(probe), instance, instanceSite{resource: 0, position: 0, address: address}, marks,
	)

	return MaskResult{
		Attributes: attributes,
		Masked:     masked,
		Work: MaskWork{
			Pointers:     probe.pointers,
			RouteSteps:   probe.routeSteps,
			ResolveSteps: probe.resolveSteps,
			Digests:      probe.digests,
			Comparisons:  probe.comparisons,
		},
	}, err
}

// MaxMaskWorkBytesForTest is the budget of one masked read.
const MaxMaskWorkBytesForTest = maxMaskWorkBytes

// MaskStepCostForTest is the fixed cost of one resolved token, besides its length.
const MaskStepCostForTest = maskStepCost

// ResolvePointerWithSpentForTest resolves tokens under node with a budget that
// has already spent the given bytes, and returns the error of the resolution.
func ResolvePointerWithSpentForTest(node any, tokens []string, spent int) error {
	work := newMaskWork(nil)
	work.budget.spent = spent

	_, err := routeTokens(work, node, tokens)

	return err
}

// IsMarkBoundForTest reports whether err is a refusal of a sensitive mark that
// the server cannot find with one rule, or of a mark longer than the depth
// limit. A depth or size refusal is not.
func IsMarkBoundForTest(err error) bool {
	var bound *stateBoundError

	return errors.As(err, &bound)
}
