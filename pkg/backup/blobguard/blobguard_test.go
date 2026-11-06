package blobguard_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
)

// fakeWatch answers a Watch with a function.
type fakeWatch func(ctx context.Context) (blobguard.Work, bool, error)

func (f fakeWatch) Running(ctx context.Context) (blobguard.Work, bool, error) {
	return f(ctx)
}

// answer returns a fakeWatch with a fixed answer.
func answer(work blobguard.Work, ok bool, err error) fakeWatch {
	return func(context.Context) (blobguard.Work, bool, error) { return work, ok, err }
}

var errRead = errors.New("read failed")

func TestHold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		watch blobguard.Watch
		want  error
	}{
		{name: "nothing runs", watch: answer(blobguard.Work{Kind: "", ID: ""}, false, nil), want: nil},
		{
			name:  "a backup runs",
			watch: answer(blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"}, true, nil),
			want:  blobguard.ErrWait,
		},
		{
			name:  "a restore runs",
			watch: answer(blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"}, true, nil),
			want:  blobguard.ErrWait,
		},
		{
			name:  "a failed read",
			watch: answer(blobguard.Work{Kind: "", ID: ""}, false, errRead),
			want:  errRead,
		},
		{
			name:  "a failed read hides an answer",
			watch: answer(blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"}, true, errRead),
			want:  errRead,
		},
		{
			name:  "an unknown kind",
			watch: answer(blobguard.Work{Kind: "other", ID: "x-1"}, true, nil),
			want:  blobguard.ErrUnknown,
		},
		{
			name:  "an empty ID",
			watch: answer(blobguard.Work{Kind: blobguard.Backup, ID: ""}, true, nil),
			want:  blobguard.ErrUnknown,
		},
		{name: "no guard", watch: nil, want: blobguard.ErrNoWatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := blobguard.Hold(t.Context(), tc.watch)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Hold err = %v, want %v", err, tc.want)
			}

			if errors.Is(tc.want, blobguard.ErrWait) {
				return
			}

			if wait, isWait := errors.AsType[*blobguard.WaitError](err); isWait {
				t.Fatalf("Hold err = %v, want no WaitError, got one for %v", err, wait.Work)
			}
		})
	}
}

func TestHoldNamesTheWork(t *testing.T) {
	t.Parallel()

	work := blobguard.Work{Kind: blobguard.Restore, ID: "rst-9"}

	wait, isWait := errors.AsType[*blobguard.WaitError](blobguard.Hold(t.Context(), answer(work, true, nil)))
	if !isWait || wait.Work != work {
		t.Fatalf("Hold: want a WaitError for %v, got %v", work, wait)
	}
}
