package incident_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/incident"
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func sample() incident.Incident {
	var in incident.Incident

	in.ID, in.Namespace, in.Title, in.Severity, in.State = "inc-1", "shop", "Checkout errors", 2, incident.StateOpen
	in.DetectedAt, in.OpenedAt, in.OpenedBy = now().Add(-10*time.Minute), now(), "alice"

	return in
}

func TestApply(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		from  incident.State
		step  incident.Step
		to    incident.State
		ready bool
		err   error
	}{
		{
			"acknowledge an open incident",
			incident.StateOpen,
			incident.StepAcknowledge,
			incident.StateAcknowledged,
			false,
			nil,
		},
		{"mitigate", incident.StateAcknowledged, incident.StepMitigate, incident.StateMitigated, false, nil},
		{"resolve from mitigated", incident.StateMitigated, incident.StepResolve, incident.StateResolved, false, nil},
		{"reopen a resolved incident", incident.StateResolved, incident.StepReopen, incident.StateOpen, false, nil},
		{"close after the review", incident.StateResolved, incident.StepClose, incident.StateClosed, true, nil},
		{"close with no review", incident.StateResolved, incident.StepClose, "", false, incident.ErrInvalid},
		{"close an open incident", incident.StateOpen, incident.StepClose, "", true, incident.ErrTransition},
		{"reopen a closed incident", incident.StateClosed, incident.StepReopen, "", true, incident.ErrTransition},
		{"acknowledge twice", incident.StateAcknowledged, incident.StepAcknowledge, "", false, incident.ErrTransition},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := sample()
			in.State = tc.from

			if tc.ready {
				in.RootCause, in.Lessons = "A bad route table", "Plan route changes in a freeze-free window"
			}

			got, err := incident.Apply(in, tc.step, "bob", now())
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("Apply: err = %v, want %v", err, tc.err)
				}

				return
			}

			if err != nil || got.State != tc.to {
				t.Fatalf("Apply = %s (%v), want %s", got.State, err, tc.to)
			}
		})
	}
}

func TestAcknowledgeSetsTheCommander(t *testing.T) {
	t.Parallel()

	got, err := incident.Apply(sample(), incident.StepAcknowledge, "bob", now())
	if err != nil || got.Commander != "bob" || !got.AcknowledgedAt.Equal(now()) {
		t.Fatalf("Apply = %+v (%v), want bob as commander", got, err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	if err := sample().Validate(); err != nil {
		t.Fatalf("sample: %v", err)
	}

	bad := map[string]func(in *incident.Incident){
		"detected after it opens": func(in *incident.Incident) { in.DetectedAt = in.OpenedAt.Add(time.Minute) },
		"a severity of 0":         func(in *incident.Incident) { in.Severity = 0 },
		"a job link with no role": func(in *incident.Incident) {
			in.Jobs = []incident.JobLink{{Job: "job-1", Role: ""}}
		},
		"no title": func(in *incident.Incident) { in.Title = "" },
	}

	for name, change := range bad {
		in := sample()
		change(&in)

		if err := in.Validate(); !errors.Is(err, incident.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}
