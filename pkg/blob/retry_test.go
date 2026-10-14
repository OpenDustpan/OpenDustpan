package blob_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// errDown is the failure of a delete in the retry tests.
var errDown = errors.New("object store unavailable")

// failingDeletes fails the first fails deletes. Only Delete is used.
type failingDeletes struct {
	blob.Store

	mu    sync.Mutex
	fails int
	calls int
}

func (f *failingDeletes) Delete(_ context.Context, _ blob.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	if f.fails > 0 {
		f.fails--

		return errDown
	}

	return nil
}

// TestDeleteRetriedBacksOff proves that a delete gets four attempts with a
// backoff between them: three failures still delete the object, four fail
// with the last error.
func TestDeleteRetriedBacksOff(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fails   int
		wantErr bool
	}{
		{fails: 0, wantErr: false},
		{fails: 3, wantErr: false},
		{fails: 4, wantErr: true},
	}

	for _, tc := range cases {
		s := &failingDeletes{Store: nil, mu: sync.Mutex{}, fails: tc.fails, calls: 0}
		start := time.Now()

		err := blob.DeleteRetried(t.Context(), s, "a/b")
		if tc.wantErr != errors.Is(err, errDown) {
			t.Fatalf("%d failures: err = %v, want error %v", tc.fails, err, tc.wantErr)
		}

		if tc.fails >= 1 && time.Since(start) < 100*time.Millisecond {
			t.Fatalf("%d failures took %s, want a backoff of at least 100ms", tc.fails, time.Since(start))
		}
	}
}

// TestDeleteRetriedStopsWithTheContext proves that an ended context stops
// the backoff at once.
func TestDeleteRetriedStopsWithTheContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	s := &failingDeletes{Store: nil, mu: sync.Mutex{}, fails: 10, calls: 0}

	if err := blob.DeleteRetried(ctx, s, "a/b"); !errors.Is(err, errDown) {
		t.Fatalf("err = %v, want the delete error", err)
	}

	if s.calls != 1 {
		t.Fatalf("calls = %d, want 1 after the context ended", s.calls)
	}
}
