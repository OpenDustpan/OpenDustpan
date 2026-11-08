//go:build linux || darwin

package conformance_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
)

// TestProcessAdapterKillSkipsAnEndedGroup checks that kill sends no signal
// when the post-exit step has ended the group while done is still open. A
// signal then could reach a group ID that another process owns.
func TestProcessAdapterKillSkipsAnEndedGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		groupEnded bool
		wantKills  int64
	}{
		{name: "the group has ended", groupEnded: true, wantKills: 0},
		{name: "the group has not ended", groupEnded: false, wantKills: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, kills := conformance.NewKillAdapter(t, tt.groupEnded)

			conformance.Kill(a)

			if got := kills.Load(); got != tt.wantKills {
				t.Errorf("kill sent %d signals to the process group, want %d", got, tt.wantKills)
			}
		})
	}
}

// TestProcessAdapterCancelSkipsAnEndedGroup checks that the Cancel function
// that prepareGroup sets returns nil when the group has ended and the command
// has no process. Without the check, the call would dereference the nil
// process.
func TestProcessAdapterCancelSkipsAnEndedGroup(t *testing.T) {
	t.Parallel()

	cmd := conformance.NewEndedGroupCommand()

	if cmd.Process != nil {
		t.Fatalf("the command has process %v, want none", cmd.Process)
	}

	if cmd.Cancel == nil {
		t.Fatal("prepareGroup set no Cancel function")
	}

	if err := cmd.Cancel(); err != nil {
		t.Errorf("Cancel = %v, want nil: the group has ended", err)
	}
}

// TestProcessAdapterGroupEndedAfterClose checks that a clean Close of a real
// adapter marks its group as ended, so that a later kill sends no signal to a
// group ID that can belong to another process.
func TestProcessAdapterGroupEndedAfterClose(t *testing.T) {
	t.Parallel()

	a := startFake(t, "waitstdin", 0)

	if conformance.GroupEnded(a) {
		t.Fatal("the group ended before the adapter exited")
	}

	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !conformance.GroupEnded(a) {
		t.Error("the group has not ended after a clean Close")
	}
}
