package breakglass

import (
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
)

// Bounds of the settings.
const (
	// MaxSteps caps the notice steps of the settings.
	MaxSteps = 10
	// MaxStepNameLen caps the name of a step, in bytes.
	MaxStepNameLen = 50
	// MinRepeat is the shortest repeat of the last step, in business time.
	MinRepeat = time.Hour
)

// Audience names who receives a notice.
type Audience string

// Audiences of a notice. The notification model resolves each one to
// people and to the sinks of their subscriptions.
const (
	// AudienceReviewers is each principal that can review the use, except
	// the people of the use (Use.People).
	AudienceReviewers Audience = "reviewers"
	// AudienceUser is the person who used break-glass.
	AudienceUser Audience = "user"
	// AudienceOwners is the owners of the namespace, from its context.
	AudienceOwners Audience = "owners"
	// AudienceOnCall is the person on call for the namespace, from its
	// context.
	AudienceOnCall Audience = "on-call"
	// AudienceAdmins is the administrators of the installation.
	AudienceAdmins Audience = "admins"
)

// Base names the time that a step counts from.
type Base string

// Bases of a step.
const (
	// BaseCreated is the time when the server opened the review.
	BaseCreated Base = "created"
	// BaseDueDay is the start of the working hours on the date of the
	// deadline.
	BaseDueDay Base = "due-day"
	// BaseDue is the deadline.
	BaseDue Base = "due"
)

// Step is one notice of the settings. It goes out at After of business
// time after Base, to Audience.
type Step struct {
	Name     string
	Base     Base
	After    time.Duration
	Audience []Audience
}

// Settings are the review settings of the installation. Enabled is the
// opt-in of the installation: when it is false, a job through a freeze
// needs no review, and the review of a break-glass account session has no
// deadline and no notices. Steps go out in order. When Repeat is above 0,
// the last step repeats each Repeat of business time until the verdict.
type Settings struct {
	Enabled bool
	Steps   []Step
	Repeat  time.Duration
}

// DefaultSettings returns the settings of an installation that turns the
// opt-in on and changes nothing else.
func DefaultSettings() Settings {
	const (
		businessDay = 8 * time.Hour
		// The escalation goes out half a business day after the deadline.
		escalation = businessDay / 2
	)

	return Settings{
		Enabled: true,
		Steps: []Step{
			{Name: "review-needed", Base: BaseCreated, After: 0, Audience: []Audience{AudienceReviewers}},
			{Name: "reminder", Base: BaseDueDay, After: 0, Audience: []Audience{AudienceReviewers, AudienceUser}},
			{
				Name: "overdue", Base: BaseDue, After: 0,
				Audience: []Audience{AudienceReviewers, AudienceUser, AudienceOwners},
			},
			{
				Name: "escalation", Base: BaseDue, After: escalation,
				Audience: []Audience{AudienceAdmins, AudienceOnCall},
			},
		},
		Repeat: businessDay,
	}
}

// Validate returns ErrInvalid for settings that break a bound.
func (s Settings) Validate() error {
	if len(s.Steps) > MaxSteps {
		return fmt.Errorf("%w: at most %d steps", ErrInvalid, MaxSteps)
	}

	if s.Repeat != 0 && s.Repeat < MinRepeat {
		return fmt.Errorf("%w: a repeat is 0 or at least %s", ErrInvalid, MinRepeat)
	}

	for i := range s.Steps {
		if err := s.Steps[i].validate(); err != nil {
			return err
		}
	}

	return nil
}

// Notice is a step that goes out for a review.
type Notice struct {
	Review string
	Step   string
	// Audience joins the audiences of each step that came due, when the
	// server missed some of them.
	Audience []Audience
	Overdue  bool
	At       time.Time
}

// Open returns a new pending review of u with the ID id, opened at now. It
// takes the calendar that applies to u.Scope. With the opt-in on, DueAt is
// the end of the next business day after the end of the use (u.Until for
// a break-glass session, u.At for a freeze break), and NextNoticeAt is the
// time of the first step. The server opens the review at the use, so a
// session that ends by its time limit has a review too. It returns ErrInvalid for a use or settings that
// fail validation, and the errors of the calendar.
func Open(id string, u Use, s Settings, cal calendar.Resolved, now time.Time) (Review, error) {
	if err := u.Validate(); err != nil {
		return Review{}, err
	}

	if err := s.Validate(); err != nil {
		return Review{}, err
	}

	r := Review{
		ID: id, Use: u, State: StatePending, CreatedAt: now,
		DueAt: time.Time{}, Calendar: cal.Calendar.ID, CalendarFrom: cal.From,
		Step: 0, NextNoticeAt: time.Time{}, Verdict: noVerdict(), Version: 0,
	}

	if !s.Enabled {
		return r, nil
	}

	end := u.At
	if !u.Until.IsZero() {
		end = u.Until
	}

	due, err := cal.Calendar.EndOfNextBusinessDay(end)
	if err != nil {
		return Review{}, fmt.Errorf("deadline of %s: %w", id, err)
	}

	r.DueAt = due

	r.NextNoticeAt, err = s.stepTime(r, cal.Calendar, 0)
	if err != nil {
		return Review{}, err
	}

	return r, nil
}

// Advance returns r after the steps that are due at now, and the notice to
// send. When the server missed some steps, one notice goes out for all of
// them, with their audiences joined. It returns no notice for a review
// with a verdict, or with no step due. It returns the errors of the
// calendar.
func Advance(r Review, s Settings, cal calendar.Calendar, now time.Time) (Review, *Notice, error) {
	if r.State != StatePending || r.NextNoticeAt.IsZero() || now.Before(r.NextNoticeAt) {
		return r, nil, nil
	}

	n := Notice{Review: r.ID, Step: "", Audience: nil, Overdue: r.Overdue(now), At: now}

	for {
		st, ok := s.step(r.Step)
		if !ok {
			break
		}

		at, err := s.stepTime(r, cal, r.Step)
		if err != nil {
			return r, nil, err
		}

		if at.IsZero() || now.Before(at) {
			r.NextNoticeAt = at

			break
		}

		n.join(st)

		r.Step++
		r.NextNoticeAt = time.Time{}
	}

	if n.Step == "" {
		return r, nil, nil
	}

	return r, &n, nil
}

// step returns the step of index n. An index past the last step gives the
// last step when the settings repeat it.
func (s Settings) step(n int) (Step, bool) {
	if len(s.Steps) == 0 || (n >= len(s.Steps) && s.Repeat == 0) {
		var none Step

		return none, false
	}

	return s.Steps[min(n, len(s.Steps)-1)], true
}

// stepTime returns the time of the step of index n of r, or the zero time
// when the settings have no such step.
func (s Settings) stepTime(r Review, cal calendar.Calendar, n int) (time.Time, error) {
	st, ok := s.step(n)
	if !ok {
		return time.Time{}, nil
	}

	after := st.After
	if extra := n - (len(s.Steps) - 1); extra > 0 {
		after += time.Duration(extra) * s.Repeat
	}

	var base time.Time

	switch st.Base {
	case BaseCreated:
		base = r.CreatedAt
	case BaseDue:
		base = r.DueAt
	case BaseDueDay:
		loc, err := cal.Location()
		if err != nil {
			return time.Time{}, fmt.Errorf("step %s: %w", st.Name, err)
		}

		if base, err = cal.WorkStart(calendar.DateOf(r.DueAt, loc)); err != nil {
			return time.Time{}, fmt.Errorf("step %s: %w", st.Name, err)
		}
	default:
		// Validate refuses every other base.
	}

	// The deadline and the start of the due day are times of the calendar
	// already. The deadline is the end of the working hours, which the
	// calendar counts as outside them, so it must not move.
	if after == 0 && st.Base != BaseCreated {
		return base, nil
	}

	at, err := cal.AddBusinessTime(base, after)
	if err != nil {
		return time.Time{}, fmt.Errorf("step %s: %w", st.Name, err)
	}

	return at, nil
}

// validate returns ErrInvalid for a step that breaks a bound.
func (st Step) validate() error {
	known := []Audience{AudienceReviewers, AudienceUser, AudienceOwners, AudienceOnCall, AudienceAdmins}

	switch {
	case st.Name == "" || len(st.Name) > MaxStepNameLen:
		return fmt.Errorf("%w: a step name has 1 to %d bytes", ErrInvalid, MaxStepNameLen)
	case st.Base != BaseCreated && st.Base != BaseDueDay && st.Base != BaseDue:
		return fmt.Errorf("%w: step %s has an unknown base %q", ErrInvalid, st.Name, st.Base)
	case st.After < 0:
		return fmt.Errorf("%w: step %s counts back", ErrInvalid, st.Name)
	case len(st.Audience) == 0:
		return fmt.Errorf("%w: step %s has no audience", ErrInvalid, st.Name)
	}

	for _, a := range st.Audience {
		if !slices.Contains(known, a) {
			return fmt.Errorf("%w: step %s has an unknown audience %q", ErrInvalid, st.Name, a)
		}
	}

	return nil
}

// join adds the name and the audiences of st to n.
func (n *Notice) join(st Step) {
	n.Step = st.Name

	for _, a := range st.Audience {
		if !slices.Contains(n.Audience, a) {
			n.Audience = append(n.Audience, a)
		}
	}
}

// noVerdict returns the verdict of a review that is pending.
func noVerdict() Verdict {
	return Verdict{Reviewer: "", Note: "", Auth: "", AuthAt: time.Time{}, ReviewedAt: time.Time{}}
}
