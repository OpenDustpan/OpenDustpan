package audit_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// errMarkStore is the failure that memMarks injects.
var errMarkStore = errors.New("mark store failed")

// memMarks is an in-memory audit.CheckStore that two instances share. The
// flags make the matching call fail.
type memMarks struct {
	state    audit.CheckState
	failRead bool
	failSave bool
	mu       sync.Mutex
}

func (m *memMarks) CheckState(context.Context) (audit.CheckState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failRead {
		return audit.CheckState{CheckedAt: time.Time{}, Mark: nil, Invalid: 0}, errMarkStore
	}

	return m.state, nil
}

func (m *memMarks) SaveCheckResult(_ context.Context, invalid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Invalid = invalid

	return nil
}

func (m *memMarks) SaveCheckMark(_ context.Context, mark audit.CheckMark) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failSave {
		return errMarkStore
	}

	if m.state.Mark == nil || m.state.Mark.Epoch < mark.Epoch {
		m.state.Mark = &mark
	}

	return nil
}

func (m *memMarks) mark() *audit.CheckMark {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.state.Mark
}

func (m *memMarks) setFailSave(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failSave = fail
}

// withMarks returns checkpoints over the blobs and entries of f that keep
// their mark in marks: one instance of two that share the store.
func (f checkpointFixture) withMarks(t *testing.T, marks audit.CheckStore) *audit.Checkpoints {
	t.Helper()

	return audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: f.blobs, Entries: f.entries, Signer: newSigner(t, 7), Marks: marks, Now: nil,
	})
}

// changeHead changes the newest entry of f and rechains its hash, so the
// stored checkpoint of it does not match.
func (f checkpointFixture) changeHead() {
	last := len(f.entries.entries) - 1
	e := f.entries.entries[last]
	e.Subject = "mallory"
	e.Hash = audit.ComputeHash(e, e.PrevHash)
	f.entries.entries[last] = e
}

// writeAt writes a checkpoint of acme at the Unix second at.
func writeAt(t *testing.T, cps *audit.Checkpoints, at int64) (bool, error) {
	t.Helper()

	return cps.Write(t.Context(), "acme", time.Unix(at, 0))
}

// checkpointCount counts every stored checkpoint and epoch object.
func checkpointCount(t *testing.T, f checkpointFixture) int {
	t.Helper()

	objects, err := f.blobs.List(t.Context(), audit.CheckpointPrefix+"/")
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}

	return len(objects)
}

// markedByA returns two instances over one store, after instance A found a
// changed checkpoint of acme at sequence 3 and stored the mark.
func markedByA(t *testing.T) (checkpointFixture, *audit.Checkpoints) {
	t.Helper()

	f := newCheckpointFixture(t, 3)
	marks := &memMarks{
		state:    audit.CheckState{CheckedAt: time.Time{}, Mark: nil, Invalid: 0},
		failRead: false,
		failSave: false,
		mu:       sync.Mutex{},
	}
	a, b := f.withMarks(t, marks), f.withMarks(t, marks)

	if wrote, err := writeAt(t, a, 1_700_000_100); err != nil || !wrote {
		t.Fatalf("write of A: %v, %v", wrote, err)
	}

	f.changeHead()

	if got, err := a.Verify(t.Context(), "acme"); err != nil || got.Valid {
		t.Fatalf("verify of A: got %+v, %v, want invalid", got, err)
	}

	if mark := marks.mark(); mark == nil || mark.Sequence != 3 || mark.Scope != "acme" || mark.Epoch != 0 {
		t.Fatalf("stored mark: got %+v, want acme at 3 in epoch 0", mark)
	}

	return f, b
}

// TestCheckpointsMarkStopsSigningOnEveryInstance proves that a mismatch that
// instance A finds stops signing of that scope on instance B and no other scope, and that a new
// epoch ends it.
func TestCheckpointsMarkStopsSigningOnEveryInstance(t *testing.T) {
	t.Parallel()

	t.Run("B signs nothing while the mark of the epoch stands", func(t *testing.T) {
		t.Parallel()

		f, b := markedByA(t)
		f.entries.add()

		before := checkpointCount(t, f)

		if wrote, err := writeAt(t, b, 1_700_000_200); !errors.Is(err, audit.ErrCheckpointMismatch) || wrote {
			t.Fatalf("write of B: got %v, %v, want ErrCheckpointMismatch", wrote, err)
		}

		if got := checkpointCount(t, f); got != before {
			t.Fatalf("objects while the mark stands: got %d, want %d", got, before)
		}
	})

	t.Run("B signs another scope while the mark of acme stands", testMarkOtherScope)

	t.Run("B fails the scope of the mark in a check", func(t *testing.T) {
		t.Parallel()

		_, b := markedByA(t)

		if got := verifyWith(t, b); got.Valid || got.BadSequence != 3 {
			t.Fatalf("verify of B: got %+v, want invalid at 3", got)
		}
	})

	t.Run("a new epoch ends the mark", func(t *testing.T) {
		t.Parallel()

		f, b := markedByA(t)

		if _, err := b.NewEpoch(t.Context(), time.Unix(1_700_000_150, 0)); err != nil {
			t.Fatalf("new epoch: %v", err)
		}

		f.entries.add()

		if wrote, err := writeAt(t, b, 1_700_000_200); err != nil || !wrote {
			t.Fatalf("write of B in the new epoch: got %v, %v, want a checkpoint", wrote, err)
		}
	})
}

func testMarkOtherScope(t *testing.T) {
	t.Parallel()

	_, b := markedByA(t)

	if wrote, err := b.Write(t.Context(), "beta", time.Unix(1_700_000_200, 0)); err != nil || !wrote {
		t.Fatalf("write of B for another scope: got %v, %v, want a checkpoint", wrote, err)
	}
}

// TestCheckpointsFailClosedWithoutTheMark proves that a mark that the store
// cannot read denies signing, and that a mark that the store cannot save is
// stored again before the next check.
func TestCheckpointsFailClosedWithoutTheMark(t *testing.T) {
	t.Parallel()

	t.Run("an unreadable mark denies a write and fails a check", testUnreadableMark)
	t.Run("a mark that the store did not take is stored again", testUnsavedMark)
}

func testUnreadableMark(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	marks := &memMarks{
		state:    audit.CheckState{CheckedAt: time.Time{}, Mark: nil, Invalid: 0},
		failRead: true,
		failSave: false,
		mu:       sync.Mutex{},
	}
	cps := f.withMarks(t, marks)
	before := checkpointCount(t, f)

	if wrote, err := writeAt(t, cps, 1_700_000_100); !errors.Is(err, audit.ErrCheckpointMismatch) || wrote {
		t.Fatalf("write: got %v, %v, want ErrCheckpointMismatch", wrote, err)
	}

	if got := checkpointCount(t, f); got != before {
		t.Fatalf("objects after a write with an unreadable mark: got %d, want %d", got, before)
	}

	if got, err := cps.Verify(t.Context(), "acme"); err != nil || got.Valid {
		t.Fatalf("verify: got %+v, %v, want invalid", got, err)
	}
}

func testUnsavedMark(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	marks := &memMarks{
		state:    audit.CheckState{CheckedAt: time.Time{}, Mark: nil, Invalid: 0},
		failRead: false,
		failSave: true,
		mu:       sync.Mutex{},
	}
	cps := f.withMarks(t, marks)

	if wrote, err := writeAt(t, cps, 1_700_000_100); err != nil || !wrote {
		t.Fatalf("write: %v, %v", wrote, err)
	}

	f.changeHead()

	if got, err := cps.Verify(t.Context(), "acme"); err != nil || got.Valid {
		t.Fatalf("verify: got %+v, %v, want invalid", got, err)
	}

	if mark := marks.mark(); mark != nil {
		t.Fatalf("mark stored although the store failed: %+v", mark)
	}

	f.entries.add()

	if wrote, err := writeAt(t, cps, 1_700_000_200); !errors.Is(err, audit.ErrCheckpointMismatch) || wrote {
		t.Fatalf("write while the mark is unsaved: got %v, %v, want ErrCheckpointMismatch", wrote, err)
	}

	marks.setFailSave(false)

	if _, err := writeAt(t, cps, 1_700_000_300); !errors.Is(err, audit.ErrCheckpointMismatch) {
		t.Fatalf("write after the store recovered: got %v, want ErrCheckpointMismatch", err)
	}

	if mark := marks.mark(); mark == nil || mark.Sequence != 3 {
		t.Fatalf("stored mark after the retry: got %+v, want sequence 3", mark)
	}
}
