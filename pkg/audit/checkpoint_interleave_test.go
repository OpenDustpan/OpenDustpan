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

// listHook wraps a blob store: the last page of a list of the checkpoints of a scope (not of
// the epoch objects) fires the hook after it answered.
type listHook struct {
	blob.Store

	after *afterFirstRead
}

func (l listHook) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	out, err := l.Store.ListPage(ctx, req)

	if !strings.Contains(req.Prefix, "/epochs/") && out.Next == "" {
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
		{
			name:       "a log cut below the newest checkpoint with a holder in between",
			interleave: true,
			cut:        2,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var holder func(f checkpointFixture, other *audit.Checkpoints)

			if tt.interleave {
				onErr := func(err error) { t.Errorf("write of the other holder: %v", err) }
				if tt.wantErr {
					onErr = func(error) {}
				}

				holder = otherHolder(t, onErr)
			}

			err := writeBetweenReads(t, stored, tt.cut, holder)

			if got := errors.Is(err, audit.ErrCheckpointMismatch); got != tt.wantErr {
				t.Fatalf("write: got %v, want mismatch %t", err, tt.wantErr)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("write: %v", err)
			}
		})
	}
}

// writeBetweenReads stores stored checkpoints, cuts the log by cut entries,
// and writes a checkpoint. When holder is not nil, it runs once between the
// two reads of the write: the other holder.
func writeBetweenReads(
	t *testing.T, stored, cut int, holder func(f checkpointFixture, other *audit.Checkpoints),
) error {
	t.Helper()

	f := newCheckpointFixture(t, 0)

	for range stored {
		f.entries.add()
		f.write(t)
	}

	f.entries.entries = f.entries.entries[:len(f.entries.entries)-cut]

	other := f.with(newSigner(t, 7))
	after := &afterFirstRead{hook: func() {}, once: sync.Once{}}

	if holder != nil {
		after.hook = func() { holder(f, other) }
	}

	cps := audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs:   listHook{Store: f.blobs, after: after},
		Entries: lastEntryHook{EntryReader: f.entries, after: after},
		Signer:  newSigner(t, 7), Marks: nil, Now: nil,
	})

	_, err := cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))

	return err
}

// otherHolder returns the hook of the other holder: it appends an entry and
// writes the checkpoint of it. A cut log makes that write fail too, so a test
// of a cut log passes an onErr that ignores the failure.
func otherHolder(t *testing.T, onErr func(error)) func(f checkpointFixture, other *audit.Checkpoints) {
	t.Helper()

	return func(f checkpointFixture, other *audit.Checkpoints) {
		f.entries.add()

		if _, err := other.Write(t.Context(), "acme", time.Unix(1_700_000_050, 0)); err != nil {
			onErr(err)
		}
	}
}
