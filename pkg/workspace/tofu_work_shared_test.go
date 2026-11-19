package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

	instance, err := workspace.DecodeForTest(`{"attributes":{"in":` + nestedWrapper(depth) + `}}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	object, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("instance is %T, want an object", instance)
	}

	pointer := "/in" + strings.Repeat("/value", depth-1) + "/missing"
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
// a second wrapper resolves never answers the marked value in clear, wherever
// the first wrapper stands: below an object member, as a list element, below a
// member or an index of another wrapper, and as the attributes themselves. The
// value "hunter2" sits where a mark names it; each row also fixes the marker
// paths that the read answers.
func TestMaskWrapperNeedingTwoWrappersFailsClosed(t *testing.T) {
	t.Parallel()

	const base = "/resources/0/instances/0/attributes"

	tests := []struct {
		name       string
		attributes string
		mark       string
		want       []string
	}{
		{
			name:       "second wrapper below the member of a wrapper",
			attributes: `{"in":{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}}`,
			mark:       "/in/k",
			want:       []string{base + "/in/k"},
		},
		{
			name:       "second wrapper below a key of a wrapper",
			attributes: `{"a":{"value":{"x":{"value":{"k":"hunter2"},"type":"x"}},"type":"x"}}`,
			mark:       "/a/x/k",
			want:       []string{base + "/a/x/k"},
		},
		{
			name:       "second wrapper below an index of a wrapper",
			attributes: `{"a":{"value":{"l":[{"value":{"k":"hunter2"},"type":"x"}]},"type":"x"}}`,
			mark:       "/a/l/0/k",
			want:       []string{base + "/a/l/0/k"},
		},
		{
			name:       "wrapper as a list element resolves",
			attributes: `{"a":[{"value":{"k":"hunter2"},"type":"x"}]}`,
			mark:       "/a/0/k",
			want:       []string{base + "/a/0/k"},
		},
		{
			name:       "two wrappers as a list element",
			attributes: `{"a":[{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}]}`,
			mark:       "/a/0/k",
			want:       []string{base + "/a/0/k"},
		},
		{
			name:       "attributes that are a wrapper resolve",
			attributes: `{"value":{"k":"hunter2"},"type":"x"}`,
			mark:       "/k",
			want:       []string{base + "/k"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := maskAttributesForTest(t, tt.attributes, tt.mark)

			if strings.Contains(fmt.Sprint(result.Attributes), "hunter2") {
				t.Fatalf("attributes = %v, want no clear secret", result.Attributes)
			}

			got := slices.Sorted(slices.Values(result.Masked))
			want := slices.Sorted(slices.Values(tt.want))

			if !slices.Equal(got, want) {
				t.Fatalf("masked = %v, want %v", got, want)
			}
		})
	}
}

// maskAttributesForTest masks the attributes of one terraform_data instance
// with one kept mark.
func maskAttributesForTest(t *testing.T, attributes, mark string) workspace.MaskResult {
	t.Helper()

	instance, err := workspace.DecodeForTest(`{"attributes":` + attributes + `}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	object, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("instance is %T, want an object", instance)
	}

	marks := workspace.SensitiveMarks{"terraform_data.x": {mark}}

	result, err := workspace.MaskInstanceWorkForTest(t.Context(), object, "terraform_data.x", marks)
	if err != nil {
		t.Fatalf("mask: %v", err)
	}

	return result
}

// TestMaskAttributesThatAreWrapperOfWrapperRefuseTheRead proves that a pointer
// that only a second wrapper resolves, below attributes that are themselves a
// wrapper, ends the read with ErrStateBound: no route names the attributes, so
// no marker can replace them, and the read never answers the secret.
func TestMaskAttributesThatAreWrapperOfWrapperRefuseTheRead(t *testing.T) {
	t.Parallel()

	instance, err := workspace.DecodeForTest(
		`{"attributes":{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}}`,
	)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	object, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("instance is %T, want an object", instance)
	}

	marks := workspace.SensitiveMarks{"terraform_data.x": {"/k"}}

	result, err := workspace.MaskInstanceWorkForTest(t.Context(), object, "terraform_data.x", marks)
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}

	if strings.Contains(fmt.Sprint(result.Attributes), "hunter2") {
		t.Fatalf("attributes = %v, want no clear secret with the error", result.Attributes)
	}
}

// TestRouteStepChargesTheBudget proves that each resolved step of a pointer
// spends from the budget of the read: a pointer that resolves with a fresh
// budget fails with ErrStateBound when the budget is almost spent.
func TestRouteStepChargesTheBudget(t *testing.T) {
	t.Parallel()

	node := map[string]any{"a": "v"}

	if err := workspace.ResolvePointerWithSpentForTest(node, []string{"a"}, 0); err != nil {
		t.Fatalf("fresh budget: err = %v, want nil", err)
	}

	err := workspace.ResolvePointerWithSpentForTest(node, []string{"a"}, workspace.MaxMaskWorkBytesForTest-1)
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("almost spent budget: err = %v, want ErrStateBound", err)
	}
}

// TestRoutePointerBeyondTheDepthBoundIsRefused proves that a pointer of more
// tokens than MaxStateDepth+1 fails with ErrStateBound before any step, so no
// state that the read accepts is walked by it.
func TestRoutePointerBeyondTheDepthBoundIsRefused(t *testing.T) {
	t.Parallel()

	tokens := make([]string, workspace.MaxStateDepth+2)
	for i := range tokens {
		tokens[i] = "a"
	}

	err := workspace.ResolvePointerWithSpentForTest(map[string]any{"a": "v"}, tokens, 0)
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}
}

// TestRevealRefusesFallbackRoute proves that a pointer that falls back to its
// wrapper never reveals the wrapper: the masked read masks the whole wrapper,
// and the reveal answers not found, so the audit names one value only.
func TestRevealRefusesFallbackRoute(t *testing.T) {
	t.Parallel()

	state := []byte(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x",` +
		`"instances":[{"attributes":{"in":{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}}}]}]}`)
	marks := workspace.SensitiveMarks{"terraform_data.x": {"/in/k"}}
	path := "/resources/0/instances/0/attributes/in/k"

	value, _, err := workspace.RevealValue(state, marks, path)
	if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Fatalf("value = %v, err = %v, want ErrResourceNotFound", value, err)
	}

	if value != nil {
		t.Fatalf("value = %v, want none", value)
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
