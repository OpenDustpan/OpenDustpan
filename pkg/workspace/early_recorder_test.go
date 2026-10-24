package workspace_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestStreamedOverrideNeedsARecorderBeforeTheBody proves that a streamed write
// that uses an override with a nil record callback is refused before the
// service reads the body. The service declares the placeholder before it reads
// the body, so a write refused before the read declares no placeholder.
func TestStreamedOverrideNeedsARecorderBeforeTheBody(t *testing.T) {
	t.Parallel()

	f := overrideFixture(t)

	if _, err := f.svc.Unlock(t.Context(), f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	var read atomic.Bool

	body := newFirstReadHook(strings.NewReader(streamDoc("lin-old", 2)), func() { read.Store(true) })

	_, err := f.svc.WriteState(
		t.Context(),
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "", MD5: ""},
		streamAuthority(namespace.Overrides{Unlocked: true, Forced: false}, nil),
		body,
	)
	if !errors.Is(err, workspace.ErrNoRecorder) {
		t.Fatalf("err = %v, want ErrNoRecorder", err)
	}

	if read.Load() {
		t.Error("the service read the body of a write that it must refuse before the read")
	}

	checkHistoryTotal(t, f, 1)
}
