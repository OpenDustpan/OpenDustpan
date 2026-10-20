package audit_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// afterFirstRead runs a hook once after the first of the two reads of Write:
// the list of the stored checkpoints of the scope, or the read of the last
// entry. The hook is the other holder: another instance appends an entry and
// stores the checkpoint of it between the two reads.
type afterFirstRead struct {
	hook func()
	once sync.Once
}

func (a *afterFirstRead) fire() { a.once.Do(a.hook) }

// listHook wraps a blob store: a list of the checkpoints of a scope (not of
// the epoch objects) fires the hook after it answered.
type listHook struct {
	blob.Store
	after *afterFirstRead
}

func (l listHook) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	out, err := l.Store.List(ctx, prefix)

	if !strings.Contains(prefix, "/epochs/") {
		l.after.fire()
	}

	return out, err
}

// lastEntryHook wraps an entry reader: a read of the last entry fires the
// hook after it answered.
type lastEntryHook struct {
	audit.EntryReader
	after *afterFirstRead
}

func (l lastEntryHook) LastEntry(ctx context.Context, scope namespace.Name) (audit.Entry, bool, error) {
	e, found, err := l.EntryReader.LastEntry(ctx, scope)
	l.after.fire()

	return e, found, err
}

// TestCheckpointsWriteWithACheckpointOfAnotherHolderBetweenTheReads proves
// that Write gives no ErrCheckpointMismatch when another holder stores a
// checkpoint above the last entry that Write read, and still gives it for a
// log that a person cut.
func TestCheckpointsWriteWithACheckpointOfAnotherHolderBetweenTheReads(t *testing.T) {
	t.Parallel()

	const stored = 3

	tests := []struct {
		name string
		// interleave is true when the other holder writes between the reads.
		interleave bool
		// cut is the count of entries that a person removes from the log
		// before Write.
		cut     int
		wantErr bool
	}{
		{name: "the other holder writes between the reads", interleave: true, cut: 0, wantErr: false},
		{name: "no other holder and a log that is whole", interleave: false, cut: 0, wantErr: false},
		{name: "a log cut below the newest checkpoint", interleave: false, cut: 1, wantErr: true},
		{name: "a log cut below the newest checkpoint with a holder in between", interleave: true, cut: 2, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newCheckpointFixture(t, 0)

			for range stored {
				f.entries.add()
				f.write(t)
			}

			f.entries.entries = f.entries.entries[:len(f.entries.entries)-tt.cut]

			other := f.with(newSigner(t, 7))
			after := &afterFirstRead{hook: func() {}, once: sync.Once{}}

			if tt.interleave {
				after.hook = func() {
					f.entries.add()

					if _, err := other.Write(t.Context(), "acme", time.Unix(1_700_000_050, 0)); err != nil && !tt.wantErr {
						t.Errorf("write of the other holder: %v", err)
					}
				}
			}

			cps := audit.NewCheckpoints(audit.CheckpointsConfig{
				Blobs:   listHook{Store: f.blobs, after: after},
				Entries: lastEntryHook{EntryReader: f.entries, after: after},
				Signer:  newSigner(t, 7), Marks: nil, Now: nil,
			})

			_, err := cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))

			if got := errors.Is(err, audit.ErrCheckpointMismatch); got != tt.wantErr {
				t.Fatalf("write: got %v, want mismatch %t", err, tt.wantErr)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("write: %v", err)
			}
		})
	}
}
