package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// sharedInstances and sharedMarks make a read whose work is the product of
	// the two counts: each instance walks every base mark. One instance spends
	// about 1.5 MB of the budget, so only the budget of the whole read, at
	// 128 MiB, refuses the product. The product is 600,000 pointers.
	sharedInstances = 120
	sharedMarks     = 5000
)

// sharedBaseState returns a state of one resource with count instances that
// hold one plain attribute each.
func sharedBaseState(count int) []byte {
	var b strings.Builder

	b.WriteString(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x","instances":[`)

	for i := range count {
		if i > 0 {
			b.WriteString(",")
		}

		b.WriteString(`{"index_key":` + strconv.Itoa(i) + `,"attributes":{"id":"a"}}`)
	}

	b.WriteString(`]}]}`)

	return []byte(b.String())
}

// sharedBaseMarks returns count marks of the resource base that name no value.
// Each mark applies to every instance of the resource.
func sharedBaseMarks(count int) workspace.SensitiveMarks {
	pointers := make([]string, 0, count)
	for i := range count {
		pointers = append(pointers, "/p"+strconv.Itoa(i))
	}

	return workspace.SensitiveMarks{"terraform_data.x": pointers}
}

// TestReadStateResourceSharesBudgetAcrossInstances proves that one budget
// covers the whole read: instances that share base marks spend from one
// bound, so the product of the instance count and the mark count ends with
// ErrStateBound. A read with the same marks and one instance still answers.
func TestReadStateResourceSharesBudgetAcrossInstances(t *testing.T) {
	t.Parallel()

	marks := sharedBaseMarks(sharedMarks)

	_, err := workspace.ReadStateResource(t.Context(), sharedBaseState(1), marks, "terraform_data.x")
	if err != nil {
		t.Fatalf("one instance: err = %v, want nil", err)
	}

	_, err = workspace.ReadStateResource(
		t.Context(), sharedBaseState(sharedInstances), marks, "terraform_data.x",
	)
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("%d instances: err = %v, want ErrStateBound", sharedInstances, err)
	}
}

// TestDiffStatesSharesBudgetAcrossInstances proves the same bound for the
// diff: both sides and every instance spend from one budget.
func TestDiffStatesSharesBudgetAcrossInstances(t *testing.T) {
	t.Parallel()

	marks := sharedBaseMarks(sharedMarks)
	from := workspace.StateWithMarks{State: sharedBaseState(1), Marks: marks}
	to := workspace.StateWithMarks{State: sharedBaseState(1), Marks: marks}

	if _, err := workspace.DiffStates(t.Context(), from, to, "", 10); err != nil {
		t.Fatalf("one instance: err = %v, want nil", err)
	}

	// Each side holds 70 instances, so the diff walks 140 instances: the same
	// product as a read of 120, and no side alone passes the bound.
	from = workspace.StateWithMarks{State: sharedBaseState(70), Marks: marks}
	to = workspace.StateWithMarks{State: sharedBaseState(70), Marks: marks}

	_, err := workspace.DiffStates(t.Context(), from, to, "", 10)
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("diff of 70 instances: err = %v, want ErrStateBound", err)
	}
}

// nestedWrapper builds a dynamic value of depth levels: each level is an
// object with a value member that holds the next level and a type member.
func nestedWrapper(depth int) string {
	return strings.Repeat(`{"value":`, depth) + `"leaf"` + strings.Repeat(`,"type":"x"}`, depth)
}

// TestMaskWrapperChainIsLinear proves that a pointer resolves once. A chain of
// dynamic wrappers with a missing leaf costs a number of steps that grows with
// the depth, not with a power of it: the old fallback at each level tried the
// direct key and then the wrapper again and again.
func TestMaskWrapperChainIsLinear(t *testing.T) {
	t.Parallel()

	const depth = 60

	instance, err := workspace.DecodeForTest(`{"attributes":` + nestedWrapper(depth) + `}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	object, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("instance is %T, want an object", instance)
	}

	pointer := strings.Repeat("/value", depth-1) + "/missing"
	marks := workspace.SensitiveMarks{"terraform_data.x": {pointer}}

	result, err := workspace.MaskInstanceWorkForTest(t.Context(), object, "terraform_data.x", marks)
	if err != nil {
		t.Fatalf("mask: %v", err)
	}

	// The leaf is missing below nested wrappers, so a second wrapper might
	// hide the value that the mark names: the read masks the first wrapper.
	wantMasked := "/resources/0/instances/0/attributes" + pointer
	if len(result.Masked) != 1 || result.Masked[0] != wantMasked {
		t.Fatalf("masked = %v, want the one path of the pointer (fail closed at the first wrapper)", result.Masked)
	}

	const stepsPerLevel = 4

	if result.Work.ResolveSteps > stepsPerLevel*depth {
		t.Fatalf("resolve steps = %d, want at most %d for depth %d",
			result.Work.ResolveSteps, stepsPerLevel*depth, depth)
	}
}

// TestMaskWrapperTakesOneTransparentLevel proves the rule that a pointer
// takes at most one wrapper. The wrapper holds its value under "value", and a
// mark below it names the path inside the wrapped value.
func TestMaskWrapperTakesOneTransparentLevel(t *testing.T) {
	t.Parallel()

	instance, err := workspace.DecodeForTest(
		`{"attributes":{"input":{"value":{"k":"s","value":{"q":"t"}},"type":"x"}}}`,
	)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	object, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("instance is %T, want an object", instance)
	}

	marks := workspace.SensitiveMarks{"terraform_data.x": {"/input/k", "/input/value/q"}}

	result, err := workspace.MaskInstanceWorkForTest(t.Context(), object, "terraform_data.x", marks)
	if err != nil {
		t.Fatalf("mask: %v", err)
	}

	want := []string{
		"/resources/0/instances/0/attributes/input/k",
		"/resources/0/instances/0/attributes/input/value/q",
	}

	if len(result.Masked) != len(want) || result.Masked[0] != want[0] || result.Masked[1] != want[1] {
		t.Fatalf("masked = %v, want %v", result.Masked, want)
	}
}

// TestMaskWrapperNeedingTwoWrappersFailsClosed proves that a pointer that only
// a second wrapper resolves never answers the marked value in clear. The
// value "hunter2" sits below two wrappers; the mark names it as if one wrapper
// stood there.
func TestMaskWrapperNeedingTwoWrappersFailsClosed(t *testing.T) {
	t.Parallel()

	instance, err := workspace.DecodeForTest(
		`{"attributes":{"in":{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}}}`,
	)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	object, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("instance is %T, want an object", instance)
	}

	marks := workspace.SensitiveMarks{"terraform_data.x": {"/in/k"}}

	result, err := workspace.MaskInstanceWorkForTest(t.Context(), object, "terraform_data.x", marks)
	if err != nil {
		t.Fatalf("mask: %v", err)
	}

	if strings.Contains(fmt.Sprint(result.Attributes), "hunter2") {
		t.Fatalf("attributes = %v, want no clear secret", result.Attributes)
	}

	// The marker path names the pointer; the marker itself replaces the whole
	// first wrapper.
	want := "/resources/0/instances/0/attributes/in/k"
	if len(result.Masked) != 1 || result.Masked[0] != want {
		t.Fatalf("masked = %v, want [%s]", result.Masked, want)
	}
}

// cancelOnCall is a context whose Err reports cancellation from call number
// from on, and counts the calls.
type cancelOnCall struct {
	from  int
	calls int
}

func (c *cancelOnCall) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelOnCall) Done() <-chan struct{} { return nil }

func (c *cancelOnCall) Value(any) any { return nil }

func (c *cancelOnCall) Err() error {
	c.calls++
	if c.from > 0 && c.calls >= c.from {
		return context.Canceled
	}

	return nil
}

// diffContextChecks is the count of context checks that the diff of the two
// states below makes, the last one after the walk. The test fixes the count,
// so that a removed check, among them the check after the walk, fails the
// test. Add or remove a check on purpose, and change the count with it.
const diffContextChecks = 18

// TestDiffStatesChecksContextAfterTheWalk proves that a context that ends
// during the walk never gives a diff. The state changes a value below a
// sensitive object, so a diff that ignored the end of the context would
// answer a changed path or a changed secret that the walk did not finish.
func TestDiffStatesChecksContextAfterTheWalk(t *testing.T) {
	t.Parallel()

	side := func(value string) workspace.StateWithMarks {
		return workspace.StateWithMarks{
			State: []byte(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x",` +
				`"instances":[{"attributes":{"tags":{"hunter2":"` + value + `"}}}]}]}`),
			Marks: workspace.SensitiveMarks{"terraform_data.x": {"/tags"}},
		}
	}

	live := &cancelOnCall{from: 0, calls: 0}

	diff, err := workspace.DiffStates(live, side("a"), side("b"), "", 10)
	if err != nil || len(diff.Changed) != 1 {
		t.Fatalf("live context: diff = %+v, err = %v, want one changed resource", diff, err)
	}

	if live.calls != diffContextChecks {
		t.Fatalf("the diff checked the context %d times, want %d", live.calls, diffContextChecks)
	}

	// A context that ends at the last check, the one after the walk, and at
	// each check before it, gives an error and no diff.
	for from := 1; from <= diffContextChecks; from++ {
		ctx := &cancelOnCall{from: from, calls: 0}

		got, err := workspace.DiffStates(ctx, side("a"), side("b"), "", 10)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel from check %d: err = %v, diff = %+v, want context.Canceled", from, err, got)
		}

		if len(got.Changed) != 0 || len(got.Added) != 0 || len(got.Removed) != 0 {
			t.Fatalf("cancel from check %d: diff = %+v, want no diff with the error", from, got)
		}
	}
}
