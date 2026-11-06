package job_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// TestStartSourceOfAnAgent proves that the start of an agent has the source
// kind agent, also when the caller gives that kind.
func TestStartSourceOfAnAgent(t *testing.T) {
	t.Parallel()

	for name, source := range map[string]job.SourceKind{"no source": "", "the source agent": job.SourceAgent} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			a := actorOf("ada")
			a.Agent = "dusty"

			req := r.plan("")
			req.Source = source

			j, err := r.svc.Start(t.Context(), a, req)
			if err != nil {
				t.Fatalf("Start = %v", err)
			}

			if j.Source != job.SourceAgent {
				t.Fatalf("source = %q, want %q", j.Source, job.SourceAgent)
			}
		})
	}
}

// TestStartSourceOfAnAgentRefused proves that the start of an agent with any
// source kind but agent is refused with ErrInvalid, before a policy sees it.
func TestStartSourceOfAnAgentRefused(t *testing.T) {
	t.Parallel()

	for _, source := range []job.SourceKind{
		job.SourceAPI, job.SourceSchedule, job.SourceTrigger, job.SourceForge, job.SourceSelfService, "cron",
	} {
		t.Run(string(source), func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			a := actorOf("ada")
			a.Agent = "dusty"

			req := r.plan("")
			req.Source = source

			if _, err := r.svc.Start(t.Context(), a, req); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("Start = %v, want %v", err, job.ErrInvalid)
			}

			if len(r.decider.inputs) != 0 {
				t.Fatalf("decisions = %d, want 0: a refused source reaches no policy", len(r.decider.inputs))
			}
		})
	}
}
