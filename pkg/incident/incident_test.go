package incident_test

import (
	"errors"
	"slices"
	"strings"
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

	cases := []struct {
		name   string
		change func(in *incident.Incident)
	}{
		{"detected after it opens", func(in *incident.Incident) { in.DetectedAt = in.OpenedAt.Add(time.Minute) }},
		{"a severity of 0", func(in *incident.Incident) { in.Severity = 0 }},
		{"a job link with no role", func(in *incident.Incident) {
			in.Jobs = []incident.JobLink{{Job: "job-1", Role: ""}}
		}},
		{"no title", func(in *incident.Incident) { in.Title = "" }},
		{"the server scope", func(in *incident.Incident) { in.Namespace = "@server" }},
		{"a classification with no person", func(in *incident.Incident) {
			in.Classifications = []incident.Classification{classification("")}
		}},
		{"a report with no kind", func(in *incident.Incident) {
			in.Reports = []incident.Report{{Kind: "", Reference: "REF-1", SentAt: now(), By: "carol"}}
		}},
		{"too many reports", func(in *incident.Incident) {
			r := incident.Report{Kind: "initial", Reference: "REF-1", SentAt: now(), By: "carol"}
			in.Reports = slices.Repeat([]incident.Report{r}, incident.MaxRecords+1)
		}},
		{"no opener", func(in *incident.Incident) { in.OpenedBy = "" }},
		{"an unknown state", func(in *incident.Incident) { in.State = "bogus" }},
		{"acknowledged with no time", func(in *incident.Incident) { in.State = incident.StateAcknowledged }},
		{"mitigated with no time", func(in *incident.Incident) { in.State = incident.StateMitigated }},
		{"resolved with no time", func(in *incident.Incident) { in.State = incident.StateResolved }},
		{"open with a time of resolve", func(in *incident.Incident) { in.ResolvedAt = now() }},
		{"acknowledged with a time of close", func(in *incident.Incident) {
			in.State, in.AcknowledgedAt, in.ClosedAt = incident.StateAcknowledged, now(), now()
		}},
		{"resolved with a time of close", func(in *incident.Incident) {
			in.State, in.ResolvedAt, in.ClosedAt = incident.StateResolved, now(), now()
		}},
		{"a step before the open", func(in *incident.Incident) {
			in.State, in.AcknowledgedAt = incident.StateAcknowledged, now().Add(-time.Minute)
		}},
		{"closed with no time of close", func(in *incident.Incident) {
			closed(in)
			in.ClosedAt = time.Time{}
		}},
		{"closed with no time of resolve", func(in *incident.Incident) {
			closed(in)
			in.ResolvedAt = time.Time{}
		}},
		{"closed with no root cause", func(in *incident.Incident) {
			closed(in)
			in.RootCause = " "
		}},
		{"closed with no lessons", func(in *incident.Incident) {
			closed(in)
			in.Lessons = ""
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := sample()
			tc.change(&in)

			if err := in.Validate(); !errors.Is(err, incident.ErrInvalid) {
				t.Fatalf("Validate: err = %v, want ErrInvalid", err)
			}
		})
	}
}

// closed makes in a closed incident that passes Validate.
func closed(in *incident.Incident) {
	in.State, in.ResolvedAt, in.ClosedAt = incident.StateClosed, now(), now()
	in.RootCause, in.Lessons = "A bad route table", "Plan route changes in a freeze-free window"
}

func TestValidateStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(in *incident.Incident)
	}{
		{"open", func(*incident.Incident) {}},
		{"open again after a reopen", func(in *incident.Incident) { in.AcknowledgedAt = now() }},
		{"acknowledged", func(in *incident.Incident) {
			in.State, in.AcknowledgedAt = incident.StateAcknowledged, now()
		}},
		{"mitigated with no acknowledge", func(in *incident.Incident) {
			in.State, in.MitigatedAt = incident.StateMitigated, now()
		}},
		{"resolved", func(in *incident.Incident) { in.State, in.ResolvedAt = incident.StateResolved, now() }},
		{"closed", closed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := sample()
			tc.change(&in)

			if err := in.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidateNew(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(in *incident.Incident)
		err    error
	}{
		{"an open incident", func(*incident.Incident) {}, nil},
		{"acknowledged", func(in *incident.Incident) {
			in.State, in.AcknowledgedAt = incident.StateAcknowledged, now()
		}, incident.ErrInvalid},
		{
			"closed",
			func(in *incident.Incident) { in.State, in.ClosedAt = incident.StateClosed, now() },
			incident.ErrInvalid,
		},
		{"open with a time of close", func(in *incident.Incident) { in.ClosedAt = now() }, incident.ErrInvalid},
		{"open with no title", func(in *incident.Incident) { in.Title = "" }, incident.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := sample()
			tc.change(&in)

			if err := in.ValidateNew(); !errors.Is(err, tc.err) {
				t.Fatalf("ValidateNew: err = %v, want %v", err, tc.err)
			}
		})
	}
}

func classification(by string) incident.Classification {
	return incident.Classification{Major: true, Criteria: []string{"data-loss"}, ClassifiedBy: by, ClassifiedAt: now()}
}

func TestRecordsValidate(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", incident.MaxNameLen+1)
	report := incident.Report{Kind: "final", Reference: "REF-1", SentAt: now(), By: "carol"}
	note := incident.Note{Incident: "inc-1", At: now(), By: "bob", Text: "Rolled back"}

	cases := []struct {
		name string
		err  error
		got  func() error
	}{
		{"a classification", nil, classification("carol").Validate},
		{"a classification with no person", incident.ErrInvalid, classification("").Validate},
		{"a classification with no time", incident.ErrInvalid, func() error {
			c := classification("carol")
			c.ClassifiedAt = time.Time{}

			return c.Validate()
		}},
		{"a criterion above the bound", incident.ErrInvalid, func() error {
			c := classification("carol")
			c.Criteria = []string{long}

			return c.Validate()
		}},
		{"too many criteria", incident.ErrInvalid, func() error {
			c := classification("carol")
			c.Criteria = slices.Repeat([]string{"data-loss"}, incident.MaxCriteria+1)

			return c.Validate()
		}},
		{"a report", nil, report.Validate},
		{"a report with no reference", incident.ErrInvalid, func() error {
			r := report
			r.Reference = ""

			return r.Validate()
		}},
		{"a report with no time", incident.ErrInvalid, func() error {
			r := report
			r.SentAt = time.Time{}

			return r.Validate()
		}},
		{"a note", nil, note.Validate},
		{"a note of spaces", incident.ErrInvalid, func() error {
			n := note
			n.Text = "   "

			return n.Validate()
		}},
		{"a note above the bound", incident.ErrInvalid, func() error {
			n := note
			n.Text = strings.Repeat("x", incident.MaxNoteLen+1)

			return n.Validate()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.got(); !errors.Is(err, tc.err) {
				t.Fatalf("Validate: err = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestCurrentClassification(t *testing.T) {
	t.Parallel()

	in := sample()
	if _, ok := in.Classification(); ok {
		t.Fatal("Classification of a new incident: ok = true, want false")
	}

	in.Classifications = []incident.Classification{classification("carol"), classification("dave")}
	if c, ok := in.Classification(); !ok || c.ClassifiedBy != "dave" {
		t.Fatalf("Classification = %+v (%t), want the last one, by dave", c, ok)
	}
}
