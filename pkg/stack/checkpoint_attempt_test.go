package stack_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/stack"
)

// replacingBlob is a blob store that ignores If-None-Match: a second Put of a
// key replaces the object and returns no error, as a store that does not
// enforce conditional writes does. It records each key that a Put wrote, so a
// test can show that no key got two writes.
type replacingBlob struct {
	*memBlob

	written []blob.Key
	wmu     sync.Mutex
}

func newReplacingBlob(clk *clock) *replacingBlob {
	return &replacingBlob{memBlob: newMemBlob(clk), written: nil, wmu: sync.Mutex{}}
}

func (r *replacingBlob) Put(_ context.Context, key blob.Key, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read blob: %w", err)
	}

	r.mu.Lock()
	r.objects[key] = memObject{data: data, modifiedAt: r.clock.Now()}
	r.mu.Unlock()

	r.wmu.Lock()
	r.written = append(r.written, key)
	r.wmu.Unlock()

	return nil
}

// writes returns how many times a Put wrote each key.
func (r *replacingBlob) writes() map[blob.Key]int {
	r.wmu.Lock()
	defer r.wmu.Unlock()

	counts := map[blob.Key]int{}
	for _, key := range r.written {
		counts[key]++
	}

	return counts
}

// staleRowStore hides the row of an update from the first reads of
// CheckpointByUpdate, as a save that read the row before a concurrent save
// committed. The service then writes its blob, and the store decides.
type staleRowStore struct {
	*memUpdates

	hide atomic.Int32
}

func (s *staleRowStore) CheckpointByUpdate(
	ctx context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Checkpoint, error) {
	if s.hide.Add(-1) >= 0 {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	return s.memUpdates.CheckpointByUpdate(ctx, ref, id)
}

// raceCase is one save over a committed row that the save did not see.
type raceCase struct {
	name   string
	first  stack.CheckpointInput
	second stack.CheckpointInput
	want   error
}

// TestCheckpointRaceKeepsTheCommittedBlob saves over a row that the save did
// not see, on a store that replaces objects. Each case commits one save, then
// runs a second save whose read of the row is stale. The row decides: the
// second save deletes only its own blob, and the committed blob stays under
// its key with its content.
func TestCheckpointRaceKeepsTheCommittedBlob(t *testing.T) {
	t.Parallel()

	tests := []raceCase{
		{
			name:   "a replay of the same sequence and digest",
			first:  checkpoint(2, envelope(`{"n":2}`)),
			second: checkpoint(2, envelope(`{"n":2}`)),
			want:   nil,
		},
		{
			name:   "the same sequence with another digest",
			first:  checkpoint(2, envelope(`{"n":2}`)),
			second: checkpoint(2, envelope(`{"n":"other"}`)),
			want:   stack.ErrCheckpointConflict,
		},
		{
			name:   "an earlier sequence",
			first:  checkpoint(2, envelope(`{"n":2}`)),
			second: checkpoint(1, envelope(`{"n":1}`)),
			want:   stack.ErrStaleCheckpoint,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runRaceCase(t, tc)
		})
	}
}

// runRaceCase commits tc.first, then runs tc.second with a stale read of the
// row, and checks the answer, the blobs and the export.
func runRaceCase(t *testing.T, tc raceCase) {
	t.Helper()

	clk := newClock()
	blobs := newReplacingBlob(clk)

	stale := &staleRowStore{memUpdates: nil, hide: atomic.Int32{}}

	f := newUpdatesWrapped(t, clk, blobs, nil, func(m *memUpdates) stack.CheckpointStore {
		stale.memUpdates = m

		return stale
	})
	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, tc.first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	committed := blobKeysOf(t, blobs)
	if len(committed) != 1 {
		t.Fatalf("blob keys after the first save = %v, want one", committed)
	}

	stale.hide.Store(1)

	if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, tc.second); !errors.Is(err, tc.want) {
		t.Fatalf("second save: err = %v, want %v", err, tc.want)
	}

	if got := blobKeysOf(t, blobs); len(got) != 1 || got[0] != committed[0] {
		t.Fatalf("blob keys after the second save = %v, want only the committed key", got)
	}

	blobs.wantEachKeyWrittenOnce(t, f.prefix)

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	if want := []byte(`{"n":2}`); !bytes.Equal(cp.Deployment, want) {
		t.Fatalf("export = %s, want the committed %s", cp.Deployment, want)
	}
}

// wantEachKeyWrittenOnce fails the test when a Put wrote a key twice.
func (r *replacingBlob) wantEachKeyWrittenOnce(t *testing.T, prefix string) {
	t.Helper()

	for key, n := range r.writes() {
		if n != 1 {
			t.Fatalf("a key got %d writes, want 1: %s", n, strings.TrimPrefix(key.String(), prefix))
		}
	}
}

// TestCheckpointSavesWriteEachKeyOnce saves many times without a sequence
// number and at the same sequence on a store that replaces objects. No key
// gets two writes, so a replace cannot change a committed blob, and the
// export still reads the state.
func TestCheckpointSavesWriteEachKeyOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seq  int64
		body func(i int) string
	}{
		{"saves without a sequence number", 0, distinctBody},
		{"saves of one sequence and one digest", 1, func(int) string { return "same" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			blobs := newReplacingBlob(clk)
			f := newUpdatesWith(t, clk, blobs, nil)
			s := f.started(t, stack.KindUpdate)

			const saves = 4

			var wg sync.WaitGroup

			errs := make(chan error, saves)

			for i := range saves {
				wg.Go(func() {
					in := checkpoint(tc.seq, envelope(`{"n":"`+tc.body(i)+`"}`))
					errs <- f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, in)
				})
			}

			wg.Wait()
			close(errs)

			for err := range errs {
				if err != nil {
					t.Fatalf("save: %v", err)
				}
			}

			blobs.wantEachKeyWrittenOnce(t, f.prefix)

			if _, err := f.svc.Export(t.Context(), f.ref); err != nil {
				t.Fatalf("export: %v", err)
			}
		})
	}
}

func distinctBody(i int) string {
	return strings.Repeat("x", i+1)
}
