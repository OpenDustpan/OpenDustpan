package workspace_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// maxWalkAllocBytes is the ceiling of the allocation that one refused read
	// of the worst state under the 5 MB version cap adds to the decode.
	maxWalkAllocBytes = 64 << 20
	versionCapBytes   = 5 << 20
	resourceAddress   = "terraform_data.x"
)

// stateWithAttributes wraps attributes in a state of one resource.
func stateWithAttributes(attributes string) []byte {
	return []byte(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x",` +
		`"instances":[{"attributes":` + attributes + `}]}]}`)
}

// nestedObject builds an object nested depth levels with one leaf at the
// bottom.
func nestedObject(depth int) string {
	return strings.Repeat(`{"a":`, depth) + `1` + strings.Repeat(`}`, depth)
}

// deepLeaves builds nested arrays depth levels deep, with leaves zeros at the
// bottom.
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

// TestReadStateResourceDecoderLayerRefusesDeepDocument is the hostile table of
// the decoder layer: a state nested past the bound is refused, whatever its
// size, before any walk.
func TestReadStateResourceDecoderLayerRefusesDeepDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		depth   int
		wantErr bool
	}{
		{name: "shallow document reads", depth: 10, wantErr: false},
		{name: "depth under the bound reads", depth: workspace.MaxStateDepth - 10, wantErr: false},
		{name: "depth past the bound is refused", depth: workspace.MaxStateDepth + 50, wantErr: true},
		{name: "decoder limit depth is refused", depth: 9000, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := workspace.ReadStateResource(
				t.Context(), stateWithAttributes(nestedObject(tt.depth)), nil, resourceAddress,
			)
			if got := errors.Is(err, workspace.ErrStateBound); got != tt.wantErr {
				t.Fatalf("depth %d: ErrStateBound = %v, want %v (err %v)", tt.depth, got, tt.wantErr, err)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("depth %d: unexpected error %v", tt.depth, err)
			}
		})
	}
}

// TestCheckInstanceWorkWalkLayer is the hostile table of the walk layer. Each
// input reaches the scan alone, as if the decoder had let it pass.
func TestCheckInstanceWorkWalkLayer(t *testing.T) {
	t.Parallel()

	longKey := strings.Repeat("k", 1<<20)

	tests := []struct {
		name       string
		attributes string
		wantErr    bool
	}{
		{name: "small instance", attributes: `{"a":[1,2,{"b":3}]}`, wantErr: false},
		{name: "depth past the bound", attributes: nestedObject(workspace.MaxStateDepth + 5), wantErr: true},
		{
			name:       "tiny leaves at the depth bound",
			attributes: `{"a":` + deepLeaves(workspace.MaxStateDepth-3, 400000) + `}`,
			wantErr:    true,
		},
		{
			name:       "one long key over many leaves",
			attributes: `{"` + longKey + `":` + deepLeaves(1, 200) + `}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			attributes, err := workspace.DecodeForTest(tt.attributes)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			err = workspace.InstanceWorkBound(map[string]any{"attributes": attributes}, 0, 0)
			if got := errors.Is(err, workspace.ErrStateBound); got != tt.wantErr {
				t.Fatalf("ErrStateBound = %v, want %v (err %v)", got, tt.wantErr, err)
			}
		})
	}
}

// TestStateReadersRefuseWorstCaseAtTheirBoundary sends the worst state under
// the depth bound to each reader that masks: the diff and the reveal.
func TestStateReadersRefuseWorstCaseAtTheirBoundary(t *testing.T) {
	t.Parallel()

	state := stateWithAttributes(`{"a":` + deepLeaves(workspace.MaxStateDepth-10, 400000) + `}`)
	path := "/resources/0/instances/0/attributes/a" + strings.Repeat("/0", workspace.MaxStateDepth-10)

	t.Run("diff", func(t *testing.T) {
		t.Parallel()

		side := workspace.StateWithMarks{State: state, Marks: nil}

		if _, err := workspace.DiffStates(t.Context(), side, side, "", 0); !errors.Is(err, workspace.ErrStateBound) {
			t.Fatalf("err = %v, want ErrStateBound", err)
		}
	})

	t.Run("reveal", func(t *testing.T) {
		t.Parallel()

		marks := workspace.SensitiveMarks{resourceAddress: {"/a", "/a/0"}}

		if _, _, err := workspace.RevealValue(state, marks, path); !errors.Is(err, workspace.ErrStateBound) {
			t.Fatalf("err = %v, want ErrStateBound", err)
		}
	})
}

// TestReadStateResourceWorstCaseAllocation measures one read of the worst
// state under the version cap that the decoder lets pass: tiny leaves at the
// depth bound. The read refuses it, and adds less than the ceiling to the
// decode. The test reads the global allocation counter, so it does not run in
// parallel.
func TestReadStateResourceWorstCaseAllocation(t *testing.T) {
	// The allocation counter belongs to the whole process, so this test must not
	// run in parallel. t.Setenv makes testing panic if t.Parallel is added.
	t.Setenv("OPENDUSTPAN_TEST_SERIAL_HEAP", "1")

	attributes := `{"a":` + deepLeaves(workspace.MaxStateDepth-10, versionCapBytes/2-2*workspace.MaxStateDepth) + `}`
	state := stateWithAttributes(attributes)

	if len(state) >= versionCapBytes {
		t.Fatalf("test state has %d bytes, want under %d", len(state), versionCapBytes)
	}

	decodeBytes := allocatedBy(t, func() {
		if _, err := workspace.DecodeForTest(string(state)); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})

	var err error

	readBytes := allocatedBy(t, func() {
		_, err = workspace.ReadStateResource(t.Context(), state, nil, resourceAddress)
	})

	if !errors.Is(err, workspace.ErrStateBound) {
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
// and context.Canceled after that. It has no deadline, no done channel and no
// values.
type cancelAfter struct {
	calls int
	limit int
}

func (c *cancelAfter) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelAfter) Done() <-chan struct{} { return nil }

func (c *cancelAfter) Value(any) any { return nil }

func (c *cancelAfter) Err() error {
	c.calls++
	if c.calls > c.limit {
		return context.Canceled
	}

	return nil
}

// TestReadStateResourceHonorsCancellation proves that a read stops when the
// context ends after the decode, and again after the walk.
func TestReadStateResourceHonorsCancellation(t *testing.T) {
	t.Parallel()

	state := stateWithAttributes(`{"a":1}`)

	tests := []struct {
		name  string
		limit int
	}{
		{name: "canceled before the decode ends", limit: 0},
		{name: "canceled after the walk", limit: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := &cancelAfter{calls: 0, limit: tt.limit}

			_, err := workspace.ReadStateResource(ctx, state, nil, resourceAddress)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}
}

// allocatedBy returns the bytes that run allocates.
func allocatedBy(t *testing.T, run func()) uint64 {
	t.Helper()

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)

	return after.TotalAlloc - before.TotalAlloc
}

// TestListStateResourcesDecoderLayerOnly proves the decoder bound alone: the
// list never walks a value, so only the decoder stops a deep document.
func TestListStateResourcesDecoderLayerOnly(t *testing.T) {
	t.Parallel()

	deep := stateWithAttributes(nestedObject(9000))

	if _, err := workspace.ListStateResources(deep, nil, "", 10, ""); !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}
}
