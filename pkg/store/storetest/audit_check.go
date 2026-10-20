package storetest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// AuditCheckHarness opens handles on one store that keeps the check state of
// the audit chain. Two handles act as two instances.
type AuditCheckHarness struct {
	// Open returns a new handle on the store of the harness.
	Open func(t *testing.T) audit.CheckStore
}

// AuditCheckFactory returns a fresh harness for one case.
type AuditCheckFactory func(t *testing.T) AuditCheckHarness

// auditCheckClockSlack is how far the clock of the store may differ from the
// clock of the test: both run on one host.
const auditCheckClockSlack = time.Minute

// auditCheckWriters is the count of concurrent writers.
const auditCheckWriters = 16

// RunAuditCheck runs the conformance suite of audit.CheckStore against
// harnesses from newHarness.
func RunAuditCheck(t *testing.T, newHarness AuditCheckFactory) {
	t.Helper()

	tests := []struct {
		test func(t *testing.T, h AuditCheckHarness)
		name string
	}{
		{name: "a database without a row holds the zero state", test: testCheckZero},
		{name: "a saved result reads back with the time of the store", test: testCheckResult},
		{name: "a later result replaces the count", test: testCheckResultReplaces},
		{name: "a saved mark reads back and leaves the result", test: testCheckMark},
		{name: "a result keeps the mark", test: testCheckResultKeepsMark},
		{name: "the first mark of a scope in an epoch stays", test: testCheckFirstMarkStays},
		{name: "marks of two scopes in one epoch both stand", test: testCheckScopeMarksStand},
		{name: "a mark of an older epoch changes no mark", test: testCheckOlderMark},
		{name: "a mark of a newer epoch replaces the older mark of its scope", test: testCheckNewerMark},
		{name: "two handles share one row", test: testCheckShared},
		{name: "concurrent writes leave whole marks", test: testCheckConcurrent},
		{name: "a state that no store keeps is refused", test: testCheckInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.test(t, newHarness(t))
		})
	}
}

// checkState reads the state of s and fails t on an error.
func checkState(t *testing.T, s audit.CheckStore) audit.CheckState {
	t.Helper()

	got, err := s.CheckState(t.Context())
	if err != nil {
		t.Fatalf("read check state: %v", err)
	}

	return got
}

// wantNear fails t unless at is within the clock slack of now.
func wantNear(t *testing.T, what string, at time.Time) {
	t.Helper()

	if d := time.Since(at); d < -auditCheckClockSlack || d > auditCheckClockSlack {
		t.Fatalf("%s = %v, want a time within %v of now", what, at, auditCheckClockSlack)
	}
}

// saveMark stores a mark of scope and fails t on an error.
func saveMark(t *testing.T, s audit.CheckStore, epoch int64, scope namespace.Name, seq int64) {
	t.Helper()

	err := s.SaveCheckMark(t.Context(), audit.CheckMark{At: time.Time{}, Scope: scope, Sequence: seq, Epoch: epoch})
	if err != nil {
		t.Fatalf("save mark of %s in epoch %d: %v", scope, epoch, err)
	}
}

// wantMark fails t unless state holds one mark of scope, at seq in epoch.
func wantMark(t *testing.T, state audit.CheckState, epoch int64, scope namespace.Name, seq int64) {
	t.Helper()

	var found []audit.CheckMark

	for _, m := range state.Marks {
		if m.Scope == scope {
			found = append(found, m)
		}
	}

	if len(found) != 1 || found[0].Epoch != epoch || found[0].Sequence != seq {
		t.Fatalf("marks of %s = %+v, want one at %d in epoch %d", scope, found, seq, epoch)
	}

	wantNear(t, "mark time", found[0].At)
}

// wantMarkCount fails t unless state holds n marks.
func wantMarkCount(t *testing.T, state audit.CheckState, n int) {
	t.Helper()

	if len(state.Marks) != n {
		t.Fatalf("marks = %+v, want %d", state.Marks, n)
	}
}

func testCheckZero(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	got := checkState(t, h.Open(t))

	if !got.CheckedAt.IsZero() || got.Invalid != 0 || len(got.Marks) != 0 {
		t.Fatalf("state of a new database = %+v, want the zero state", got)
	}
}

func testCheckResult(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	if err := s.SaveCheckResult(t.Context(), 3); err != nil {
		t.Fatalf("save result: %v", err)
	}

	got := checkState(t, s)

	if got.Invalid != 3 || len(got.Marks) != 0 {
		t.Fatalf("state = %+v, want 3 invalid scopes and no mark", got)
	}

	wantNear(t, "checked at", got.CheckedAt)
}

func testCheckResultReplaces(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	for _, invalid := range []int{2, 0, 5} {
		if err := s.SaveCheckResult(t.Context(), invalid); err != nil {
			t.Fatalf("save result %d: %v", invalid, err)
		}

		if got := checkState(t, s); got.Invalid != invalid {
			t.Fatalf("invalid after saving %d = %d", invalid, got.Invalid)
		}
	}
}

func testCheckMark(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	saveMark(t, s, 4, "acme", 17)

	got := checkState(t, s)

	wantMark(t, got, 4, "acme", 17)

	if got.Invalid != 0 || !got.CheckedAt.IsZero() {
		t.Fatalf("state = %+v, want a mark and no result", got)
	}
}

func testCheckResultKeepsMark(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	saveMark(t, s, 1, "acme", 9)

	if err := s.SaveCheckResult(t.Context(), 1); err != nil {
		t.Fatalf("save result: %v", err)
	}

	got := checkState(t, s)

	wantMark(t, got, 1, "acme", 9)

	if got.Invalid != 1 {
		t.Fatalf("invalid = %d, want 1", got.Invalid)
	}

	saveMark(t, s, 1, "other", 2)

	after := checkState(t, s)

	if after.Invalid != 1 || after.CheckedAt.IsZero() {
		t.Fatalf("state after a second mark = %+v, want the result kept", after)
	}

	wantMarkCount(t, after, 2)
}

func testCheckFirstMarkStays(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	saveMark(t, s, 2, "acme", 5)
	saveMark(t, s, 2, "acme", 1)

	got := checkState(t, s)

	wantMark(t, got, 2, "acme", 5)
	wantMarkCount(t, got, 1)
}

func testCheckScopeMarksStand(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	saveMark(t, s, 2, "acme", 5)
	saveMark(t, s, 2, "beta", 8)

	got := checkState(t, s)

	wantMark(t, got, 2, "acme", 5)
	wantMark(t, got, 2, "beta", 8)
	wantMarkCount(t, got, 2)
}

func testCheckOlderMark(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	saveMark(t, s, 3, "acme", 5)
	saveMark(t, s, 3, "beta", 6)
	saveMark(t, s, 2, "acme", 9)
	saveMark(t, s, 1, "gamma", 8)

	got := checkState(t, s)

	wantMark(t, got, 3, "acme", 5)
	wantMark(t, got, 3, "beta", 6)
	wantMark(t, got, 1, "gamma", 8)
	wantMarkCount(t, got, 3)
}

func testCheckNewerMark(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	saveMark(t, s, 1, "acme", 5)
	saveMark(t, s, 1, "beta", 6)
	saveMark(t, s, 2, "acme", 8)

	got := checkState(t, s)

	wantMark(t, got, 2, "acme", 8)
	wantMark(t, got, 1, "beta", 6)
	wantMarkCount(t, got, 2)
}

func testCheckShared(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	a, b := h.Open(t), h.Open(t)

	if err := a.SaveCheckResult(t.Context(), 2); err != nil {
		t.Fatalf("save result on A: %v", err)
	}

	saveMark(t, a, 0, "acme", 3)

	got := checkState(t, b)

	if got.Invalid != 2 {
		t.Fatalf("invalid on B = %d, want 2", got.Invalid)
	}

	wantMark(t, got, 0, "acme", 3)
}

func testCheckConcurrent(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	var wg sync.WaitGroup

	for i := range auditCheckWriters {
		wg.Go(func() {
			scope := namespace.Name(fmt.Sprintf("scope-%d", i))

			mark := audit.CheckMark{At: time.Time{}, Scope: scope, Sequence: int64(i + 1), Epoch: 1}

			if err := s.SaveCheckMark(t.Context(), mark); err != nil {
				t.Errorf("save mark %d: %v", i, err)
			}

			if err := s.SaveCheckResult(t.Context(), i); err != nil {
				t.Errorf("save result %d: %v", i, err)
			}
		})
	}

	wg.Wait()

	got := checkState(t, s)

	wantMarkCount(t, got, auditCheckWriters)

	for i := range auditCheckWriters {
		wantMark(t, got, 1, namespace.Name(fmt.Sprintf("scope-%d", i)), int64(i+1))
	}
}

func testCheckInvalid(t *testing.T, h AuditCheckHarness) {
	t.Helper()

	s := h.Open(t)

	if err := s.SaveCheckResult(t.Context(), -1); !errors.Is(err, audit.ErrInvalidCheckState) {
		t.Fatalf("save a negative count: got %v, want ErrInvalidCheckState", err)
	}

	for _, mark := range []audit.CheckMark{
		{At: time.Time{}, Scope: "", Sequence: 1, Epoch: 0},
		{At: time.Time{}, Scope: "acme", Sequence: -1, Epoch: 0},
		{At: time.Time{}, Scope: "acme", Sequence: 1, Epoch: -1},
	} {
		if err := s.SaveCheckMark(t.Context(), mark); !errors.Is(err, audit.ErrInvalidCheckState) {
			t.Fatalf("save mark %+v: got %v, want ErrInvalidCheckState", mark, err)
		}
	}

	if got := checkState(t, s); len(got.Marks) != 0 || got.Invalid != 0 {
		t.Fatalf("state after refused writes = %+v, want the zero state", got)
	}
}
