package workspace_test

// Hostile tests for the work of the masking walks of a masked read: the bound
// on the nodes times the secrets of one instance, and the check of the context
// inside the walks. Each case sends its input directly to one layer, as if the
// other layer were absent, and fails when its layer is removed.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// expiringCtx is a context that answers the deadline from the call of Err
// after the first after calls. It holds no parent and no deadline of its own.
type expiringCtx struct {
	calls *atomic.Int64
	after int64
}

// Deadline implements context.Context.
func (c expiringCtx) Deadline() (time.Time, bool) { return time.Time{}, false }

// Done implements context.Context.
func (c expiringCtx) Done() <-chan struct{} { return nil }

// Value implements context.Context.
func (c expiringCtx) Value(any) any { return nil }

// Err implements context.Context.
func (c expiringCtx) Err() error {
	if c.calls.Add(1) > c.after {
		return context.DeadlineExceeded
	}

	return nil
}

// secretsAndFiller returns attributes with secrets objects that kept marks
// name, and filler small objects that no mark names, with the marks for t.n.
func secretsAndFiller(secrets, filler int) (string, workspace.SensitiveMarks) {
	var b strings.Builder

	marks := make([]string, 0, secrets)

	b.WriteString(`{"p":{`)

	for i := range secrets {
		if i > 0 {
			b.WriteString(",")
		}

		fmt.Fprintf(&b, `"s%d":{"a":"s%d"}`, i, i)

		marks = append(marks, fmt.Sprintf("/p/s%d", i))
	}

	b.WriteString(`},"f":[`)

	for i := range filler {
		if i > 0 {
			b.WriteString(",")
		}

		fmt.Fprintf(&b, `{"x":"%d"}`, i)
	}

	b.WriteString(`]}`)

	return b.String(), workspace.SensitiveMarks{"t.n": marks}
}

// TestMaskedPathsDuplicateWorkBound proves the bound on the nodes times the
// secrets of one instance: kept marks that name distinct objects and 40000
// small filler objects, under 1 MB, are refused with ErrTooLarge before any
// masking. The context expires after a few thousand values, so a read without
// the bound ends with the deadline error and fails this case fast. The case with 4000
// secrets stays under the bound of the markers, so only the work bound can
// refuse it.
func TestMaskedPathsDuplicateWorkBound(t *testing.T) {
	t.Parallel()

	for _, secrets := range []int{4000, 10000} {
		t.Run(fmt.Sprintf("%d secrets", secrets), func(t *testing.T) {
			t.Parallel()

			attrs, marks := secretsAndFiller(secrets, 40000)
			state := stateWithAttributes(attrs)

			if len(state) > 1<<20 {
				t.Fatalf("the test state has %d bytes, want at most 1 MiB", len(state))
			}

			// The context expires at the third check, a few thousand values into
			// the masking, so a read without the bound ends fast with the deadline.
			ctx := expiringCtx{calls: &atomic.Int64{}, after: 2}

			_, err := workspace.MaskedPathsContext(ctx, []byte(state), marks)
			if !errors.Is(err, workspace.ErrTooLarge) {
				t.Fatalf("err = %v, want ErrTooLarge", err)
			}
		})
	}
}

// TestMaskedPathsDuplicateWorkBoundAllowsSmallStates proves the bound refuses
// only the large product: a state with 100 secrets and 1000 filler objects is
// read.
func TestMaskedPathsDuplicateWorkBoundAllowsSmallStates(t *testing.T) {
	t.Parallel()

	attrs, marks := secretsAndFiller(100, 1000)

	if _, err := workspace.MaskedPaths([]byte(stateWithAttributes(attrs)), marks); err != nil {
		t.Fatalf("err = %v, want a read", err)
	}
}

// TestMaskedPathsWalksCheckTheContext proves the masking walks check the
// context inside the walk, with the work bound out of the way: the state has
// one secret, so the product is small. The context passes the check after the
// parse and expires at the next call, so only the check inside the walk can
// refuse. Without that check the read answers a result.
func TestMaskedPathsWalksCheckTheContext(t *testing.T) {
	t.Parallel()

	oneSecret, marks := secretsAndFiller(1, 4000)

	tests := []struct {
		name  string
		attrs string
		marks workspace.SensitiveMarks
	}{
		{name: "duplicate pass", attrs: oneSecret, marks: marks},
		{name: "all leaves", attrs: oneSecret, marks: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := expiringCtx{calls: &atomic.Int64{}, after: 1}

			_, err := workspace.MaskedPathsContext(ctx, []byte(stateWithAttributes(tc.attrs)), tc.marks)
			if !errors.Is(err, workspace.ErrReadDeadline) {
				t.Fatalf("err = %v, want ErrReadDeadline", err)
			}
		})
	}
}
