package run

import (
	"log/slog"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestRefuseStepAfterALostClaim sets the lost state of a run directly and
// calls refuseStep, with no fake server and no renewal loop, so the order of
// the events is fixed. A step whose claim is lost before its start is
// refused; the same step on a run that holds its claim is not.
func TestRefuseStepAfterALostClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lost    bool
		refused bool
		reason  string
	}{
		{name: "the claim is lost", lost: true, refused: true, reason: claimLostReason},
		{name: "the claim is held", lost: false, refused: false, reason: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				cfg  Config
				step protocol.Step
			)

			step.Name, step.Role, step.Args = "hook", string(job.RoleHook), []string{"true"}

			r := &phaseRun{
				cfg: &cfg, log: slog.New(slog.DiscardHandler), sess: nil, profile: "", mu: sync.Mutex{},
				lost: false, canceled: false, events: make(chan struct{}, 1), checkout: "", home: "", tmp: "",
			}

			if tt.lost {
				r.markLost()
			}

			res, refused := r.refuseStep(&step, nil)
			if refused != tt.refused || res.reason != tt.reason {
				t.Fatalf("refuseStep = %+v, %t; want refused %t with reason %q", res, refused, tt.refused, tt.reason)
			}

			if refused && res.exitCode != -1 {
				t.Fatalf("refuseStep exit code = %d, want -1 for a step that does not start", res.exitCode)
			}
		})
	}
}
