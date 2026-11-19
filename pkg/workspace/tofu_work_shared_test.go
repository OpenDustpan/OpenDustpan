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

// wrapperCase is one shape of the table of wrapper tests: the attributes, the
// kept mark, and either the marker paths that the read answers or the refusal.
type wrapperCase struct {
	name       string
	attributes string
	mark       string
	want       []string
	wantErr    bool
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

	tests := []wrapperCase{
		{
			name:       "type member of a wrapper whose value holds a type key",
			attributes: `{"input":{"value":{"type":"hunter2"},"type":["object",{"type":"string"}]}}`,
			mark:       "/input/type",
			want:       []string{base + "/input/type"},
			wantErr:    false,
		},
		{
			name:       "wrapper in a wrapper with a type key in the value",
			attributes: `{"input":{"value":{"type":"hunter2","value":"v"},"type":"x"}}`,
			mark:       "/input/type",
			want:       []string{base + "/input/type"},
			wantErr:    false,
		},
		{
			name:       "value member of a wrapper whose value holds a value key and a sibling",
			attributes: `{"input":{"value":{"value":"hunter2","other":"hunter3"},"type":"x"}}`,
			mark:       "/input/value",
			want:       []string{base + "/input/value"},
			wantErr:    false,
		},
		{
			name:       "pointer through the type member to a second wrapper",
			attributes: `{"w":{"value":{},"type":{"a":{"value":{"k":"hunter2"},"type":"x"}}}}`,
			mark:       "/w/type/a/k",
			want:       []string{base + "/w/type/a/k"},
			wantErr:    false,
		},
		{
			name:       "attributes that are a wrapper with a type key in the value",
			attributes: `{"value":{"type":"hunter2"},"type":"x"}`,
			mark:       "/type",
			want:       nil,
			wantErr:    true,
		},
		{
			name:       "second wrapper below the member of a wrapper",
			attributes: `{"in":{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}}`,
			mark:       "/in/k",
			want:       []string{base + "/in/k"},
			wantErr:    false,
		},
		{
			name:       "second wrapper below a key of a wrapper",
			attributes: `{"a":{"value":{"x":{"value":{"k":"hunter2"},"type":"x"}},"type":"x"}}`,
			mark:       "/a/x/k",
			want:       []string{base + "/a/x/k"},
			wantErr:    false,
		},
		{
			name:       "second wrapper below an index of a wrapper",
			attributes: `{"a":{"value":{"l":[{"value":{"k":"hunter2"},"type":"x"}]},"type":"x"}}`,
			mark:       "/a/l/0/k",
			want:       []string{base + "/a/l/0/k"},
			wantErr:    false,
		},
		{
			name:       "wrapper as a list element resolves",
			attributes: `{"a":[{"value":{"k":"hunter2"},"type":"x"}]}`,
			mark:       "/a/0/k",
			want:       []string{base + "/a/0/k"},
			wantErr:    false,
		},
		{
			name:       "two wrappers as a list element",
			attributes: `{"a":[{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}]}`,
			mark:       "/a/0/k",
			want:       []string{base + "/a/0/k"},
			wantErr:    false,
		},
		{
			name:       "attributes that are a wrapper resolve",
			attributes: `{"value":{"k":"hunter2"},"type":"x"}`,
			mark:       "/k",
			want:       []string{base + "/k"},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := maskAttributesErrForTest(t, tt.attributes, tt.mark)
			assertMaskOutcome(t, tt, result, err)
		})
	}
}

// assertMaskOutcome checks the result of a masked read of one wrapper shape:
// no clear secret, and either the refusal of the read or the marker paths.
func assertMaskOutcome(t *testing.T, tt wrapperCase, result workspace.MaskResult, err error) {
	t.Helper()

	mark, want := tt.mark, tt.want

	if tt.wantErr {
		if !errors.Is(err, workspace.ErrStateBound) {
			t.Fatalf("err = %v, want ErrStateBound", err)
		}

		assertNoPointerInBoundText(t, err, mark)
	} else if err != nil {
		t.Fatalf("mask: %v", err)
	}

	text := fmt.Sprint(result.Attributes)
	if strings.Contains(text, "hunter2") || strings.Contains(text, "hunter3") {
		t.Fatalf("attributes = %v, want no clear secret", result.Attributes)
	}

	if tt.wantErr {
		return
	}

	if got := slices.Sorted(slices.Values(result.Masked)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Fatalf("masked = %v, want %v", got, want)
	}
}

// maskAttributesErrForTest masks the attributes of one terraform_data instance
// with one kept mark, and returns the error of the read.
func maskAttributesErrForTest(t *testing.T, attributes, mark string) (workspace.MaskResult, error) {
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

	return workspace.MaskInstanceWorkForTest(t.Context(), object, "terraform_data.x", marks)
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

	assertNoPointerInBoundText(t, err, "/k")

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

	if strings.Contains(err.Error(), "too deep") || strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %q, want the text of a mark that is too long, not a state that is too deep", err)
	}
}

// assertNoPointerInBoundText proves that the text of an ErrStateBound for a
// mark holds neither the pointer, which comes from the state, nor the claim
// that the state is too deep or too large.
func assertNoPointerInBoundText(t *testing.T, err error, pointer string) {
	t.Helper()

	text := err.Error()
	if strings.Contains(text, pointer) || strings.Contains(text, "too deep") || strings.Contains(text, "too large") {
		t.Fatalf("err text = %q, want no pointer %q and no claim of depth or size", text, pointer)
	}
}

// TestRevealRefusesFallbackRoute proves that a pointer that falls back to its
// wrapper never reveals the wrapper: the masked read masks the whole wrapper,
// and the reveal answers not found, so the audit names one value only. The
// rows hold the shapes of a second wrapper and the ambiguous shapes, where a
// member of a wrapper and the wrapped value both resolve the pointer.
func TestRevealRefusesFallbackRoute(t *testing.T) {
	t.Parallel()

	const base = "/resources/0/instances/0/attributes"

	tests := []struct {
		name       string
		attributes string
		mark       string
	}{
		{"second wrapper", `{"in":{"value":{"value":{"k":"hunter2"},"type":"x"},"type":"x"}}`, "/in/k"},
		{
			"type member and a type key",
			`{"input":{"value":{"type":"hunter2"},"type":["object",{"type":"string"}]}}`,
			"/input/type",
		},
		{
			"wrapper with a type key in the value",
			`{"input":{"value":{"type":"hunter2","value":"v"},"type":"x"}}`,
			"/input/type",
		},
		{
			"value member and a sibling",
			`{"input":{"value":{"value":"hunter2","other":"hunter3"},"type":"x"}}`,
			"/input/value",
		},
		{
			"through the type member",
			`{"w":{"value":{},"type":{"a":{"value":{"k":"hunter2"},"type":"x"}}}}`,
			"/w/type/a/k",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := []byte(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x",` +
				`"instances":[{"attributes":` + tt.attributes + `}]}]}`)
			marks := workspace.SensitiveMarks{"terraform_data.x": {tt.mark}}

			value, _, err := workspace.RevealValue(state, marks, base+tt.mark)
			if !errors.Is(err, workspace.ErrResourceNotFound) {
				t.Fatalf("value = %v, err = %v, want ErrResourceNotFound", value, err)
			}

			if value != nil {
				t.Fatalf("value = %v, want none", value)
			}
		})
	}
}

// TestRoutePhysicalChargesTheBudget proves that the walk below a wrapper spends
// from the budget: the budget runs out on the first step inside the wrapper,
// after the one step to the wrapper, and the read fails with ErrStateBound.
func TestRoutePhysicalChargesTheBudget(t *testing.T) {
	t.Parallel()

	node := map[string]any{"w": map[string]any{"value": map[string]any{"k": "v"}, "type": "x"}}
	tokens := []string{"w", "k"}
	before := len("w") + workspace.MaskStepCostForTest

	if err := workspace.ResolvePointerWithSpentForTest(node, tokens, 0); err != nil {
		t.Fatalf("fresh budget: err = %v, want nil", err)
	}

	err := workspace.ResolvePointerWithSpentForTest(node, tokens, workspace.MaxMaskWorkBytesForTest-before)
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("budget spent inside the wrapper: err = %v, want ErrStateBound", err)
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

// TestMaskEmptyPointerRefusesTheRead proves that a mark with an empty pointer,
// which names the whole attributes, ends the read with ErrStateBound, and that
// no secret stays in clear. The empty pointer comes from an empty cty path of
// sensitive_attributes or from a kept mark.
func TestMaskEmptyPointerRefusesTheRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		instance string
		marks    workspace.SensitiveMarks
	}{
		{
			name:     "empty path of sensitive_attributes",
			instance: `{"attributes":{"": "hunter2","k":"hunter2"},"sensitive_attributes":[[]]}`,
			marks:    workspace.SensitiveMarks{"terraform_data.x": {"/other"}},
		},
		{
			name:     "empty kept mark",
			instance: `{"attributes":{"k":"hunter2"}}`,
			marks:    workspace.SensitiveMarks{"terraform_data.x": {""}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			instance, err := workspace.DecodeForTest(tt.instance)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			object, ok := instance.(map[string]any)
			if !ok {
				t.Fatalf("instance is %T, want an object", instance)
			}

			result, err := workspace.MaskInstanceWorkForTest(t.Context(), object, "terraform_data.x", tt.marks)
			if !errors.Is(err, workspace.ErrStateBound) {
				t.Fatalf("err = %v, want ErrStateBound", err)
			}

			if strings.Contains(fmt.Sprint(result.Attributes), "hunter2") {
				t.Fatalf("attributes = %v, want no clear secret with the error", result.Attributes)
			}
		})
	}
}
