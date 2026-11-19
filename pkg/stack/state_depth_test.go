package stack_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

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
	secret := `{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"x"}`

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
			doc:     `{"` + longKey + `":` + deepLeaves(1, 200) + `}`,
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
func TestReadStateResourceWorstCaseAllocation(t *testing.T) {
	// The allocation counter is global, so a parallel test would add its own
	// allocation to the count. The testing package panics on t.Setenv in a
	// parallel test, so the call keeps this test serial.
	t.Setenv("OPENDUSTPAN_TEST_SERIAL_HEAP", "1")

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
// and context.Canceled after that. It is never done and holds no value, so
// only the reads of Err change the result of a read.
type cancelAfter struct {
	calls int
	limit int
}

func (c *cancelAfter) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelAfter) Done() <-chan struct{} { return nil }

func (c *cancelAfter) Err() error {
	c.calls++
	if c.calls > c.limit {
		return context.Canceled
	}

	return nil
}

func (c *cancelAfter) Value(any) any { return nil }

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

			ctx := &cancelAfter{calls: 0, limit: tt.limit}

			if err := tt.read(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}
}

// allocatedBy returns the bytes that run allocates. It reads the cumulative
// allocation counter of the process, so the caller must run serial.
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

	deep := deploymentWithInputs(nestedSecret(9000))

	if _, err := stack.ListStateResources(deep, nil, "", 10, ""); !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}
}

// pastBoundLeaves builds a document that passes the decoder depth bound and
// is past the work bound of one masked read on its own.
func pastBoundLeaves() string {
	return deepLeaves(stack.MaxStateDepth-10, 400000)
}

// underBoundLeaves builds a document whose scan stays under the work bound of
// one read alone, but spends most of it.
func underBoundLeaves() string {
	return deepLeaves(stack.MaxStateDepth-10, 250000)
}

// resourceDeployment wraps inputs and outputs in a deployment of one resource.
func resourceDeployment(inputs, outputs string) []byte {
	return []byte(`{"resources":[{"urn":"` + depthTestURN + `","type":"t:m:T","inputs":` + inputs +
		`,"outputs":` + outputs + `}]}`)
}

// stackOutputsDeployment wraps outputs in a deployment of one stack resource.
func stackOutputsDeployment(outputs string) []byte {
	return []byte(`{"resources":[{"urn":"u","type":"pulumi:pulumi:Stack","outputs":` + outputs + `}]}`)
}

// TestReadStateScansOutputsSide is the hostile table of the outputs scan: a
// resource whose outputs are past the work bound, with inputs that are fine,
// is refused by each read. Only the scan of the outputs stops it, because the
// decoder lets the depth pass.
func TestReadStateScansOutputsSide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		read func() error
	}{
		{"resource read, outputs past the bound", func() error {
			deployment := resourceDeployment(`{"a":1}`, pastBoundLeaves())
			_, err := stack.ReadStateResource(t.Context(), deployment, depthTestURN)

			return err
		}},
		{"outputs read, outputs past the bound", func() error {
			_, err := stack.ReadStateOutputs(t.Context(), stackOutputsDeployment(pastBoundLeaves()))

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.read(); !errors.Is(err, stack.ErrStateBound) {
				t.Fatalf("err = %v, want ErrStateBound", err)
			}
		})
	}
}

// TestReadStateChecksContextBeforeScan pins the order of a read: the check of
// the context comes before the work scan, so a canceled read of a document
// past the work bound answers context.Canceled and spends no scan.
func TestReadStateChecksContextBeforeScan(t *testing.T) {
	t.Parallel()

	resource := resourceDeployment(pastBoundLeaves(), `{}`)
	outputs := stackOutputsDeployment(pastBoundLeaves())

	tests := []struct {
		name string
		read func(ctx context.Context) error
	}{
		{"resource read", func(ctx context.Context) error {
			_, err := stack.ReadStateResource(ctx, resource, depthTestURN)

			return err
		}},
		{"outputs read", func(ctx context.Context) error {
			_, err := stack.ReadStateOutputs(ctx, outputs)

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Control: the document is past the bound when the context lives.
			if err := tt.read(t.Context()); !errors.Is(err, stack.ErrStateBound) {
				t.Fatalf("live context: err = %v, want ErrStateBound", err)
			}

			ctx := &cancelAfter{calls: 0, limit: 0}

			err := tt.read(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled context: err = %v, want context.Canceled", err)
			}

			if errors.Is(err, stack.ErrStateBound) {
				t.Fatalf("canceled context: err = %v, want no ErrStateBound: the scan ran", err)
			}
		})
	}
}

// TestReadStateResourceSharesOneBudget proves that the inputs and the outputs
// of one resource draw from one work budget. Each side alone stays under the
// bound, and the two together are past it, so only a shared budget refuses the
// read. The refusal builds no marker: its allocation stays under the decode
// plus the ceiling.
func TestReadStateResourceSharesOneBudget(t *testing.T) {
	// The allocation counter is global, so a parallel test would add its own
	// allocation to the count. The testing package panics on t.Setenv in a
	// parallel test, so the call keeps this test serial.
	t.Setenv("OPENDUSTPAN_TEST_SERIAL_HEAP", "1")

	side := underBoundLeaves()

	for name, prefix := range map[string]int{"inputs": len("/resources/0/inputs"), "outputs": len("/resources/0/outputs")} {
		doc, err := stack.DecodeForTest(side)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		if err := stack.MaskWorkBound(doc, prefix); err != nil {
			t.Fatalf("%s alone: err = %v, want under the bound", name, err)
		}
	}

	deployment := resourceDeployment(side, side)

	if len(deployment) >= versionCapBytes {
		t.Fatalf("test document has %d bytes, want under %d", len(deployment), versionCapBytes)
	}

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

// TestListStateResourcesRefusesDeepPreviousState proves that a previous state
// past the decoder bound makes the list answer the bound. The current state is
// small, so only the read of the previous state can refuse it.
func TestListStateResourcesRefusesDeepPreviousState(t *testing.T) {
	t.Parallel()

	current := deploymentWithInputs(`{"a":1}`)

	_, err := stack.ListStateResources(current, deploymentWithInputs(nestedSecret(9000)), "", 10, "")
	if !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}
}

// TestListStateResourcesListsAllAsChangedOnBrokenPreviousState keeps the
// behavior for a previous state that does not decode: every resource counts
// as changed.
func TestListStateResourcesListsAllAsChangedOnBrokenPreviousState(t *testing.T) {
	t.Parallel()

	page, err := stack.ListStateResources(deploymentWithInputs(`{"a":1}`), []byte(`{broken`), "", 10, "")
	if err != nil {
		t.Fatalf("err = %v, want none", err)
	}

	if len(page.Resources) != 1 || !page.Resources[0].Changed {
		t.Fatalf("resources = %+v, want one changed resource", page.Resources)
	}
}
