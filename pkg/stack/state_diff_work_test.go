package stack_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// diffOnly wraps one resource with the given inputs and empty outputs, under
// the shared test URN.
func diffOnly(inputs string) []byte {
	return resourceDeployment(inputs, `{}`)
}

// TestDiffStatesSharesOneBudgetAcrossBothSides is the hostile case of the
// diff scan: each side is under the work budget alone, and both sides are over
// it together. Only one budget across both sides refuses the pair.
func TestDiffStatesSharesOneBudgetAcrossBothSides(t *testing.T) {
	t.Parallel()

	side := underBoundLeaves()

	for name, prefix := range map[string]int{"inputs": len("/inputs"), "outputs": len("/outputs")} {
		doc, err := stack.DecodeForTest(side)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		if err := stack.MaskWorkBound(doc, prefix); err != nil {
			t.Fatalf("%s of one side alone: err = %v, want under the budget", name, err)
		}
	}

	one := diffOnly(side)

	if _, err := stack.DiffStates(t.Context(), one, diffOnly(`{}`), "", 100); err != nil {
		t.Fatalf("one heavy side: err = %v, want none", err)
	}

	_, err := stack.DiffStates(t.Context(), one, one, "", 100)
	if !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("both heavy sides: err = %v, want ErrStateBound", err)
	}
}

// hostileDiffSide builds the probe document of one diff side: outputs nested
// 20 levels with 10,000-byte keys and 5,000 leaves at the bottom. The leaf
// values start at base, so two sides with another base differ at each leaf.
func hostileDiffSide(base int) string {
	var out strings.Builder

	out.WriteString(`{"urn":"` + depthTestURN + `","type":"t:m:T","outputs":`)

	for level := range 20 {
		out.WriteString(`{"` + strings.Repeat(string(rune('a'+level)), 10000) + `":`)
	}

	out.WriteString(`[`)

	for i := range 5000 {
		if i > 0 {
			out.WriteString(`,`)
		}

		out.WriteString(strconv.Itoa(base + i))
	}

	out.WriteString(`]` + strings.Repeat(`}`, 20) + `}`)

	return `{"resources":[` + out.String() + `]}`
}

// TestDiffStatesRefusesLongPathProbe is the probe of the hostile review: two
// states of about 249 KB each, whose diff would build 5,000 paths of about
// 200 KB. The diff answers ErrStateBound and no path.
func TestDiffStatesRefusesLongPathProbe(t *testing.T) {
	t.Parallel()

	from := []byte(hostileDiffSide(0))
	to := []byte(hostileDiffSide(100000))

	if len(from) > 300<<10 {
		t.Fatalf("probe has %d bytes, want about 249 KB", len(from))
	}

	diff, err := stack.DiffStates(t.Context(), from, to, "", 100)
	if !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}

	if len(diff.Changed) != 0 || len(diff.Added) != 0 || len(diff.Removed) != 0 {
		t.Fatalf("diff = %+v, want an empty diff with the refusal", diff)
	}
}

// TestDiffStatesChecksContext proves that a canceled context stops the diff
// after the decode and after the walk, and that the walk itself runs for a
// live context.
func TestDiffStatesChecksContext(t *testing.T) {
	t.Parallel()

	from := diffOnly(`{"a":1}`)
	to := diffOnly(`{"a":2}`)

	live, err := stack.DiffStates(t.Context(), from, to, "", 100)
	if err != nil || len(live.Changed) != 1 {
		t.Fatalf("live context: diff = %+v, err = %v, want one changed resource", live, err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := stack.DiffStates(canceled, from, to, "", 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: err = %v, want context.Canceled", err)
	}
}

// callContext is a context whose Err answers by the number of the call: it
// reports cancellation only on call number cancelOn. It lets a test cancel at
// one check of the diff, so that each ctx check has its own test.
type callContext struct {
	context.Context //nolint:containedctx // The test context wraps the test context to change only Err.

	cancelOn int
	calls    int
}

func (c *callContext) Err() error {
	c.calls++
	if c.calls == c.cancelOn {
		return context.Canceled
	}

	return nil
}

// TestDiffStatesChecksContextAtEachStep proves that the check after the
// decode and the check after the walk each stop the diff alone. The first case
// uses a pair past the work budget: when the check after the decode stops the
// diff, the scan does not run, so the answer is the cancellation and not the
// bound. The second case cancels on the second check, which only the check
// after the walk makes.
func TestDiffStatesChecksContextAtEachStep(t *testing.T) {
	t.Parallel()

	small := [2][]byte{diffOnly(`{"a":1}`), diffOnly(`{"a":2}`)}
	heavy := [2][]byte{[]byte(hostileDiffSide(0)), []byte(hostileDiffSide(100000))}

	for _, tt := range []struct {
		name     string
		pair     [2][]byte
		cancelOn int
	}{
		{"after the decode", heavy, 1},
		{"after the walk", small, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := &callContext{Context: t.Context(), cancelOn: tt.cancelOn, calls: 0}

			_, err := stack.DiffStates(ctx, tt.pair[0], tt.pair[1], "", 100)
			if !errors.Is(err, context.Canceled) || errors.Is(err, stack.ErrStateBound) {
				t.Fatalf("cancel on call %d: err = %v, want context.Canceled", tt.cancelOn, err)
			}
		})
	}
}
