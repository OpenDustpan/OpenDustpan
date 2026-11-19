package stack_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

const (
	depthTestURN = "urn:pulumi:dev::web::t:m:T::r"
	// maxWalkAllocBytes is the ceiling of the allocation that one refused read
	// of the worst document under the 5 MB version cap adds to the decode.
	maxWalkAllocBytes = 64 << 20
	versionCapBytes   = 5 << 20
)

// deploymentWithInputs wraps inputs in a deployment of one resource.
func deploymentWithInputs(inputs string) []byte {
	return []byte(`{"resources":[{"urn":"` + depthTestURN + `","type":"t:m:T","inputs":` + inputs + `}]}`)
}

// nestedSecret builds an object nested depth levels with one secret at the
// bottom.
func nestedSecret(depth int) string {
	secret := `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"x"}` //nolint:gosec // A fake secret signature of a test document, not a credential.

	return strings.Repeat(`{"a":`, depth) + secret + strings.Repeat(`}`, depth)
}

// deepLeaves builds nested arrays depth levels deep, with leaves zeros at the
// bottom. It stays under the version cap for the sizes that the tests use.
func deepLeaves(depth, leaves int) string {
	return strings.Repeat(
		`[`,
		depth,
	) + strings.TrimSuffix(
		strings.Repeat(`0,`, leaves),
		",",
	) + strings.Repeat(
		`]`,
		depth,
	)
}

// TestReadStateDecoderLayerRefusesDeepDocument is the hostile table of the
// decoder layer: a document nested past the bound is refused, whatever its
// size, before any walk.
func TestReadStateDecoderLayerRefusesDeepDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		depth   int
		wantErr bool
	}{
		{name: "shallow document reads", depth: 10, wantErr: false},
		{name: "depth under the bound reads", depth: stack.MaxStateDepth - 10, wantErr: false},
		{name: "depth past the bound is refused", depth: stack.MaxStateDepth + 50, wantErr: true},
		{name: "decoder limit depth is refused", depth: 9000, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := stack.ReadStateResource(t.Context(), deploymentWithInputs(nestedSecret(tt.depth)), depthTestURN)
			if got := errors.Is(err, stack.ErrStateBound); got != tt.wantErr {
				t.Fatalf("depth %d: ErrStateBound = %v, want %v (err %v)", tt.depth, got, tt.wantErr, err)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("depth %d: unexpected error %v", tt.depth, err)
			}
		})
	}
}

// TestReadStateOutputsDecoderLayerRefusesDeepDocument is the same table for
// the outputs read.
func TestReadStateOutputsDecoderLayerRefusesDeepDocument(t *testing.T) {
	t.Parallel()

	doc := []byte(`{"resources":[{"urn":"u","type":"pulumi:pulumi:Stack","outputs":` + nestedSecret(9000) + `}]}`)

	if _, err := stack.ReadStateOutputs(t.Context(), doc); !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}
}

// TestCheckMaskWorkWalkLayer is the hostile table of the walk layer. Each
// input reaches the scan alone, as if the decoder had let it pass.
func TestCheckMaskWorkWalkLayer(t *testing.T) {
	t.Parallel()

	longKey := strings.Repeat("k", 1<<20)

	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{name: "small document", doc: `{"a":[1,2,{"b":3}]}`, wantErr: false},
		{name: "depth past the bound", doc: nestedSecret(stack.MaxStateDepth + 5), wantErr: true},
		{
			name:    "tiny leaves at the depth bound",
			doc:     deepLeaves(stack.MaxStateDepth-1, 400000),
			wantErr: true,
		},
		{
			name:    "one long key over many leaves",
			doc:     `{"` + longKey + `":` + deepLeaves(1, 100) + `}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc, err := stack.DecodeForTest(tt.doc)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			err = stack.MaskWorkBound(doc, len("/resources/0/inputs"))
			if got := errors.Is(err, stack.ErrStateBound); got != tt.wantErr {
				t.Fatalf("ErrStateBound = %v, want %v (err %v)", got, tt.wantErr, err)
			}
		})
	}
}

// TestReadStateResourceWorstCaseAllocation measures one read of the worst
// document under the version cap that the decoder lets pass: tiny leaves at
// the depth bound. The read refuses it, and allocates less than the ceiling.
// The test reads the global allocation counter, so it does not run in
// parallel.
//
//nolint:paralleltest // The test measures the allocation of the whole process.
func TestReadStateResourceWorstCaseAllocation(t *testing.T) {
	inputs := deepLeaves(stack.MaxStateDepth-10, versionCapBytes/2-2*stack.MaxStateDepth)
	deployment := deploymentWithInputs(inputs)

	if len(deployment) >= versionCapBytes {
		t.Fatalf("test document has %d bytes, want under %d", len(deployment), versionCapBytes)
	}

	// The decode of the document costs the same for every read. The ceiling
	// bounds what the read adds to it: no path is built.
	decodeBytes := allocatedBy(t, func() {
		if _, err := stack.DecodeForTest(string(deployment)); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})

	var err error

	readBytes := allocatedBy(t, func() {
		_, err = stack.ReadStateResource(t.Context(), deployment, depthTestURN)
	})

	if !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}

	if readBytes > decodeBytes+maxWalkAllocBytes {
		t.Fatalf(
			"read allocated %d bytes, decode alone %d, want at most %d more",
			readBytes,
			decodeBytes,
			maxWalkAllocBytes,
		)
	}
}

// cancelAfter is a context whose Err answers nil for the first limit calls
// and context.Canceled after that.
type cancelAfter struct {
	context.Context //nolint:containedctx // The test double wraps a context to change its Err.

	calls int
	limit int
}

func (c *cancelAfter) Err() error {
	c.calls++
	if c.calls > c.limit {
		return context.Canceled
	}

	return nil
}

// TestReadStateHonorsCancellation proves that a read stops when the context
// ends after the decode, and again after the walk.
func TestReadStateHonorsCancellation(t *testing.T) {
	t.Parallel()

	deployment := deploymentWithInputs(`{"a":1}`)
	outputs := []byte(`{"resources":[{"urn":"u","type":"pulumi:pulumi:Stack","outputs":{"a":1}}]}`)

	tests := []struct {
		name  string
		limit int
		read  func(ctx context.Context) error
	}{
		{"resource, canceled before the decode ends", 0, func(ctx context.Context) error {
			_, err := stack.ReadStateResource(ctx, deployment, depthTestURN)

			return err
		}},
		{"resource, canceled after the walk", 1, func(ctx context.Context) error {
			_, err := stack.ReadStateResource(ctx, deployment, depthTestURN)

			return err
		}},
		{"outputs, canceled before the decode ends", 0, func(ctx context.Context) error {
			_, err := stack.ReadStateOutputs(ctx, outputs)

			return err
		}},
		{"outputs, canceled after the walk", 1, func(ctx context.Context) error {
			_, err := stack.ReadStateOutputs(ctx, outputs)

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := &cancelAfter{Context: t.Context(), calls: 0, limit: tt.limit}

			if err := tt.read(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}
}

// allocatedBy returns the bytes that run allocates.
func allocatedBy(t *testing.T, run func()) uint64 {
	t.Helper()

	var before, after runtime.MemStats

	runtime.GC() //nolint:revive // The test reads the allocation counter and needs a settled heap.
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)

	return after.TotalAlloc - before.TotalAlloc
}

// TestListStateResourcesDecoderLayerOnly proves the decoder bound alone: the
// list never walks a value, so only the decoder stops a deep document.
func TestListStateResourcesDecoderLayerOnly(t *testing.T) {
	t.Parallel()

	deep := deploymentWithInputs(nestedSecret(9000))

	if _, err := stack.ListStateResources(deep, nil, "", 10, ""); !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}
}
