package workspace_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// workInstances is the count of instances that each hold a little less
	// than the work budget of one read, but more than it together.
	workInstances = 8
	// workLeavesPerInstance is the count of leaves of one such instance. One
	// leaf charges about 300 bytes of the budget of 128 MiB.
	workLeavesPerInstance = 100_000
	// maxRefusedReadBytes is the ceiling of the allocation that a refused
	// read adds to the decode of the same state.
	maxRefusedReadBytes = 64 << 20
	// duplicateDeadline ends a duplicate pass that grows with the product of
	// the secrets and the values. The linear pass ends in well under a second.
	duplicateDeadline = 30 * time.Second
)

// leafObject returns a JSON object of count leaves with distinct keys.
func leafObject(count int) string {
	var out strings.Builder

	out.WriteString("{")

	for i := range count {
		if i > 0 {
			out.WriteString(",")
		}

		out.WriteString(`"k` + strconv.Itoa(i) + `":0`)
	}

	out.WriteString("}")

	return out.String()
}

// manyInstancesState returns a state of one resource with instances that each
// hold leaves leaves.
func manyInstancesState(instances, leaves int) []byte {
	body := leafObject(leaves)
	list := make([]string, instances)

	for i := range list {
		list[i] = `{"index_key":` + strconv.Itoa(i) + `,"attributes":` + body + `}`
	}

	return []byte(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x",` +
		`"instances":[` + strings.Join(list, ",") + `]}]}`)
}

// TestInstanceWorkBoundSharesOneBudget is the hostile table of the scan layer:
// each instance is under the budget, all together are over it.
func TestInstanceWorkBoundSharesOneBudget(t *testing.T) {
	t.Parallel()

	doc, err := workspace.DecodeForTest(`{"attributes":` + leafObject(workLeavesPerInstance) + `}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	instance, isObject := doc.(map[string]any)
	if !isObject {
		t.Fatalf("instance has type %T, want an object", doc)
	}

	if err := workspace.InstanceWorkBound(instance, 0, 0); err != nil {
		t.Fatalf("one instance: %v, want nil", err)
	}

	many := make([]map[string]any, workInstances)
	for i := range many {
		many[i] = instance
	}

	if err := workspace.InstancesWorkBound(many, 0); !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("all instances: %v, want ErrStateBound", err)
	}
}

// TestReadStateResourceSharesOneBudgetAcrossInstances is the hostile table of
// the read layer: the read refuses before it builds a marker, and allocates
// little more than the decode.
func TestReadStateResourceSharesOneBudgetAcrossInstances(t *testing.T) {
	// The allocation counter belongs to the whole process, so this test must not
	// run in parallel. t.Setenv makes testing panic if t.Parallel is added.
	t.Setenv("OPENDUSTPAN_TEST_SERIAL_HEAP", "1")

	state := manyInstancesState(workInstances, workLeavesPerInstance)

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

	if readBytes > decodeBytes+maxRefusedReadBytes {
		t.Fatalf("read allocated %d bytes, decode alone %d, want at most %d more",
			readBytes, decodeBytes, maxRefusedReadBytes)
	}
}

// TestDiffStatesSharesOneBudgetAcrossBothSides proves that the diff charges
// both sides to one budget. Each side holds 3 instances of about 30 MB of
// work, about 90 MB, under the budget of 128 MiB. The two sides together are
// about 180 MB, over it. A budget that restarts for each side lets the pair
// pass.
func TestDiffStatesSharesOneBudgetAcrossBothSides(t *testing.T) {
	t.Parallel()

	const sideInstances = 3

	side := workspace.StateWithMarks{State: manyInstancesState(sideInstances, workLeavesPerInstance), Marks: nil}
	empty := workspace.StateWithMarks{State: manyInstancesState(0, 0), Marks: nil}

	// One side alone is under the budget, in either position.
	if _, err := workspace.DiffStates(t.Context(), side, empty, "", 0); err != nil {
		t.Fatalf("one heavy side as from: err = %v, want none", err)
	}

	if _, err := workspace.DiffStates(t.Context(), empty, side, "", 0); err != nil {
		t.Fatalf("one heavy side as to: err = %v, want none", err)
	}

	if _, err := workspace.DiffStates(t.Context(), side, side, "", 0); !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("both heavy sides: err = %v, want ErrStateBound", err)
	}
}

// duplicateInstance returns an instance with secrets distinct secret strings,
// values distinct plain strings, and one copy of the secret with index 5. The
// marks name each secret.
func duplicateInstance(
	t *testing.T, secrets, values int,
) (map[string]any, workspace.SensitiveMarks) {
	t.Helper()

	secretList := make([]string, secrets)
	marks := make([]string, secrets)

	for i := range secretList {
		secretList[i] = `"secret-` + strconv.Itoa(i) + `"`
		marks[i] = "/s/" + strconv.Itoa(i)
	}

	plain := make([]string, values)
	for i := range plain {
		plain[i] = `"plain-` + strconv.Itoa(i) + `"`
	}

	doc, err := workspace.DecodeForTest(`{"attributes":{"s":[` + strings.Join(secretList, ",") +
		`],"n":[` + strings.Join(plain, ",") + `],"copy":"secret-5"}}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	instance, isObject := doc.(map[string]any)
	if !isObject {
		t.Fatalf("instance has type %T, want an object", doc)
	}

	return instance, workspace.SensitiveMarks{resourceAddress: marks}
}

// TestMaskTofuInstanceDuplicatePassIsLinear is the hostile table of the
// duplicate pass: many secrets and many values that match none of them. The
// pass compares each value with an index of the secrets. A pass that compares
// each value with each secret does not end before the deadline of the context.
func TestMaskTofuInstanceDuplicatePassIsLinear(t *testing.T) {
	t.Parallel()

	instance, marks := duplicateInstance(t, 16_000, 160_000)

	ctx, cancel := context.WithTimeout(t.Context(), duplicateDeadline)
	defer cancel()

	attributes, masked, err := workspace.MaskInstanceForTest(ctx, instance, resourceAddress, marks)
	if err != nil {
		t.Fatalf("mask: %v", err)
	}

	if !slices.Contains(masked, "/resources/0/instances/0/attributes/copy") {
		t.Fatal("the copy of a secret is not masked: want its path in the masked set")
	}

	object, isObject := attributes.(map[string]any)
	if !isObject {
		t.Fatalf("attributes have type %T, want an object", attributes)
	}

	if _, isMarker := object["copy"].(map[string]any); !isMarker {
		t.Fatalf("copy = %v, want a marker", object["copy"])
	}
}

// TestDuplicatePassComparesValuesOnADigestCollision proves that the duplicate
// pass compares the values when two digests are equal. Every value has one
// digest here. The copy of a secret is masked, and a value that is not a copy
// stays, so only the equality check stops a collision from masking it.
func TestDuplicatePassComparesValuesOnADigestCollision(t *testing.T) {
	// The digest switch is global to the package, so this test must not run in
	// parallel. t.Setenv makes testing panic if t.Parallel is added.
	t.Setenv("OPENDUSTPAN_TEST_SERIAL_HEAP", "1")

	restore := workspace.ForceOneDigest()
	defer restore()

	instance, marks := duplicateInstance(t, 1, 3)

	attributes, masked, err := workspace.MaskInstanceForTest(t.Context(), instance, resourceAddress, marks)
	if err != nil {
		t.Fatalf("mask: %v", err)
	}

	object, isObject := attributes.(map[string]any)
	if !isObject {
		t.Fatalf("attributes have type %T, want an object", attributes)
	}

	if firstIsMarker(t, object, "n") {
		t.Fatalf("a plain value that collides with a secret is masked: %v", masked)
	}

	if !firstIsMarker(t, object, "s") {
		t.Fatalf("the secret is not masked: %v", object["s"])
	}
}

// firstIsMarker reports whether the first element of the list under key is a
// marker object.
func firstIsMarker(t *testing.T, object map[string]any, key string) bool {
	t.Helper()

	list, isList := object[key].([]any)
	if !isList || len(list) == 0 {
		t.Fatalf("%s = %v, want a list", key, object[key])
	}

	_, isMarker := list[0].(map[string]any)

	return isMarker
}

// TestMaskTofuInstanceDuplicatePassHonorsCancellation proves that the
// duplicate pass checks the context at each container.
func TestMaskTofuInstanceDuplicatePassHonorsCancellation(t *testing.T) {
	t.Parallel()

	instance, marks := duplicateInstance(t, 4, 4)

	// The context ends after the mark pass and before the first container of
	// the duplicate pass answers.
	_, _, err := workspace.MaskInstanceForTest(&cancelAfter{calls: 0, limit: 0}, instance, resourceAddress, marks)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestReadStateResourceChecksTheContextBeforeTheScan proves the order of the
// layers: a state over the work budget that is read with an ended context
// answers the end of the context, so the scan never runs for a caller that
// left. The state is under the depth bound, so the decoder passes it.
func TestReadStateResourceChecksTheContextBeforeTheScan(t *testing.T) {
	t.Parallel()

	state := manyInstancesState(workInstances, workLeavesPerInstance)

	_, err := workspace.ReadStateResource(&cancelAfter{calls: 0, limit: 0}, state, nil, resourceAddress)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	side := workspace.StateWithMarks{State: state, Marks: nil}

	_, err = workspace.DiffStates(&cancelAfter{calls: 0, limit: 0}, side, side, "", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("diff err = %v, want context.Canceled", err)
	}
}

// TestListStateResourcesRefusesADeepPreviousState proves that the list
// answers the bound when the previous state nests too deep, and does not read
// every resource as new.
func TestListStateResourcesRefusesADeepPreviousState(t *testing.T) {
	t.Parallel()

	current := stateWithAttributes(`{"a":1}`)
	deep := stateWithAttributes(nestedObject(workspace.MaxStateDepth + 5))

	_, err := workspace.ListStateResources(current, deep, "", 10, "")
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}

	// A previous state that is not JSON keeps the answer of a list with no
	// previous state.
	if _, err := workspace.ListStateResources(current, []byte("not json"), "", 10, ""); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
