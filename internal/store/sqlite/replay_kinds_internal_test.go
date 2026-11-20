package sqlite

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// TestReplayPlanCoversEveryKind proves that the replay has a plan for each
// journal kind, and none for a kind that is not one. A kind that
// maintenance.Kinds names and the replay lacks would stay in the journal and
// never apply after a restore.
func TestReplayPlanCoversEveryKind(t *testing.T) {
	t.Parallel()

	for _, kind := range maintenance.Kinds() {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			steps, ok := replayPlan(kind)
			if !ok || len(steps) == 0 {
				t.Fatalf("replayPlan has no plan for the kind %q", kind)
			}
		})
	}

	if _, ok := replayPlan("unknown"); ok {
		t.Fatal("replayPlan has a plan for a kind that is not a journal kind")
	}
}
