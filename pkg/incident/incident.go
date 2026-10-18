// Package incident holds incident records: what broke, when the team saw
// it, who led the response, which units and jobs took part, how it was
// classified, and what the team learned. The record links the change
// records that already exist (jobs, approvals, freezes, break-glass
// reviews), so one incident shows its whole trail.
//
// The server records the classification that a person gives. It never
// decides by itself that an incident is major, and it never reports an
// incident to an authority: it keeps the facts that a report needs.
package incident

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrInvalid reports an incident or a note that fails validation.
	ErrInvalid = errors.New("invalid incident")
	// ErrNotFound reports an unknown incident.
	ErrNotFound = errors.New("incident not found")
	// ErrExists reports a create with an ID that an incident already has.
	ErrExists = errors.New("incident already exists")
	// ErrConflict reports a write that names an older version.
	ErrConflict = errors.New("incident changed since it was read")
	// ErrTransition reports a step that the state of the incident does not
	// allow.
	ErrTransition = errors.New("incident state does not allow this step")
)

// Bounds of an incident.
const (
	// IDPrefix starts every incident ID.
	IDPrefix = "inc-"
	// MaxTitleLen caps the title, in bytes.
	MaxTitleLen = 200
	// MaxTextLen caps the impact, the root cause and the lessons, in
	// bytes each.
	MaxTextLen = 64 << 10
	// MaxLinks caps the units, the jobs and the reviews of an incident,
	// each.
	MaxLinks = 200
	// MaxNoteLen caps a note of the timeline, in bytes.
	MaxNoteLen = 8192
	// MaxRecords caps the classifications and the reports of an incident,
	// each.
	MaxRecords = 100
	// MaxCriteria caps the criteria of a classification.
	MaxCriteria = 32
	// MaxNameLen caps a criterion and the kind of a report, in bytes.
	MaxNameLen = 64
	// MaxReferenceLen caps the reference of a report, in bytes.
	MaxReferenceLen = 200
)

// State is the state of an incident.
type State string

// States of an incident.
const (
	StateOpen         State = "open"
	StateAcknowledged State = "acknowledged"
	StateMitigated    State = "mitigated"
	StateResolved     State = "resolved"
	// StateClosed is final for the step and the fields of the incident.
	// An incident closes after its review after the incident: the root
	// cause and the lessons are set. A closed incident still takes notes,
	// classifications and reports, because a final report and a late
	// classification can come after the close.
	StateClosed State = "closed"
)

// Severity is the severity that the team gives, from 1, the highest, to 4.
type Severity int

// Bounds of a severity.
const (
	SeverityHighest Severity = 1
	SeverityLowest  Severity = 4
)

// Classification is the classification of an incident by a person, with
// the criteria that the person found met. The criteria are names that the
// installation lists, for example "clients-affected" or "data-loss". The
// server checks no criterion.
type Classification struct {
	Major        bool
	Criteria     []string
	ClassifiedBy string
	ClassifiedAt time.Time
}

// Validate returns ErrInvalid for a classification with no person, no
// time, or criteria that break a bound.
func (c Classification) Validate() error {
	switch {
	case c.ClassifiedBy == "" || c.ClassifiedAt.IsZero():
		return fmt.Errorf("%w: a classification needs a person and a time", ErrInvalid)
	case len(c.Criteria) > MaxCriteria:
		return fmt.Errorf("%w: a classification has at most %d criteria", ErrInvalid, MaxCriteria)
	}

	for _, name := range c.Criteria {
		if name == "" || len(name) > MaxNameLen {
			return fmt.Errorf("%w: a criterion has 1 to %d bytes", ErrInvalid, MaxNameLen)
		}
	}

	return nil
}

// Report records a report about the incident that a person made outside
// OpenDustpan, for example to a supervisor, with its reference there.
type Report struct {
	Kind      string
	Reference string
	SentAt    time.Time
	By        string
}

// Validate returns ErrInvalid for a report with no person or no time, or
// with a kind or a reference that breaks a bound.
func (r Report) Validate() error {
	switch {
	case r.By == "" || r.SentAt.IsZero():
		return fmt.Errorf("%w: a report needs a person and a time", ErrInvalid)
	case r.Kind == "" || len(r.Kind) > MaxNameLen:
		return fmt.Errorf("%w: the kind of a report has 1 to %d bytes", ErrInvalid, MaxNameLen)
	case r.Reference == "" || len(r.Reference) > MaxReferenceLen:
		return fmt.Errorf("%w: the reference of a report has 1 to %d bytes", ErrInvalid, MaxReferenceLen)
	default:
		return nil
	}
}

// Incident is one incident in a namespace. DetectedAt is when the team
// found the problem, which can be before OpenedAt. Commander is the
// principal who leads the response. OnCall is the on-call field that
// paged, for example a schedule reference. Classifications and Reports
// are append-only and oldest first: the last classification is the
// current one.
type Incident struct {
	ID              string
	Namespace       namespace.Name
	Title           string
	Severity        Severity
	State           State
	DetectedAt      time.Time
	OpenedAt        time.Time
	AcknowledgedAt  time.Time
	MitigatedAt     time.Time
	ResolvedAt      time.Time
	ClosedAt        time.Time
	OpenedBy        string
	Commander       string
	OnCall          string
	Units           []string
	Jobs            []JobLink
	Reviews         []string
	Impact          string
	Classifications []Classification
	RootCause       string
	Lessons         string
	Reports         []Report
	Version         int64
}

// Classification returns the current classification of the incident, and
// false when no person classified it.
func (in Incident) Classification() (Classification, bool) {
	if len(in.Classifications) == 0 {
		return Classification{Major: false, Criteria: nil, ClassifiedBy: "", ClassifiedAt: time.Time{}}, false
	}

	return in.Classifications[len(in.Classifications)-1], true
}

// Validate returns ErrInvalid for an incident that breaks a bound.
func (in Incident) Validate() error {
	if err := in.validateFields(); err != nil {
		return err
	}

	return in.validateBounds()
}

// ValidateNew returns ErrInvalid for an incident that fails Validate, or
// that a create cannot store: an incident starts in the state open, with
// no time of a later step.
func (in Incident) ValidateNew() error {
	if err := in.Validate(); err != nil {
		return err
	}

	switch {
	case in.State != StateOpen:
		return fmt.Errorf("%w: a new incident is in the state %s", ErrInvalid, StateOpen)
	case !in.AcknowledgedAt.IsZero() || !in.MitigatedAt.IsZero() || !in.ResolvedAt.IsZero() || !in.ClosedAt.IsZero():
		return fmt.Errorf("%w: a new incident has no time of a later step", ErrInvalid)
	default:
		return nil
	}
}

// validateFields returns ErrInvalid for an ID, a namespace, a title, a
// severity or a time of the incident that breaks a bound.
func (in Incident) validateFields() error {
	switch {
	case !strings.HasPrefix(in.ID, IDPrefix):
		return fmt.Errorf("%w: an incident ID starts with %q", ErrInvalid, IDPrefix)
	case !validNamespace(in.Namespace):
		return fmt.Errorf("%w: an incident needs a namespace", ErrInvalid)
	case in.Title == "" || len(in.Title) > MaxTitleLen:
		return fmt.Errorf("%w: a title has 1 to %d bytes", ErrInvalid, MaxTitleLen)
	case in.Severity < SeverityHighest || in.Severity > SeverityLowest:
		return fmt.Errorf("%w: a severity is %d to %d", ErrInvalid, SeverityHighest, SeverityLowest)
	case in.OpenedAt.IsZero() || in.DetectedAt.IsZero() || in.DetectedAt.After(in.OpenedAt):
		return fmt.Errorf("%w: an incident is detected at or before it opens", ErrInvalid)
	default:
		return nil
	}
}

// validateBounds returns ErrInvalid for links or texts of the incident
// above their bounds.
func (in Incident) validateBounds() error {
	switch {
	case len(in.Units) > MaxLinks || len(in.Jobs) > MaxLinks || len(in.Reviews) > MaxLinks:
		return fmt.Errorf("%w: at most %d units, jobs and reviews each", ErrInvalid, MaxLinks)
	case len(in.Impact) > MaxTextLen || len(in.RootCause) > MaxTextLen || len(in.Lessons) > MaxTextLen:
		return fmt.Errorf("%w: a text has at most %d bytes", ErrInvalid, MaxTextLen)
	case len(in.Classifications) > MaxRecords || len(in.Reports) > MaxRecords:
		return fmt.Errorf("%w: at most %d classifications and reports each", ErrInvalid, MaxRecords)
	default:
		return in.validateRecords()
	}
}

// validateRecords returns ErrInvalid for a job link, a classification or a
// report that fails its validation.
func (in Incident) validateRecords() error {
	if err := in.validateJobLinks(); err != nil {
		return err
	}

	for i := range in.Classifications {
		if err := in.Classifications[i].Validate(); err != nil {
			return err
		}
	}

	for i := range in.Reports {
		if err := in.Reports[i].Validate(); err != nil {
			return err
		}
	}

	return nil
}

// validNamespace reports whether ns is the name of a namespace. The
// server scope is not a namespace, so it holds no incident.
func validNamespace(ns namespace.Name) bool {
	_, err := namespace.ParseName(ns.String())

	return err == nil
}

// validateJobLinks returns ErrInvalid for a job link with no job or with
// an unknown role.
func (in Incident) validateJobLinks() error {
	for i := range in.Jobs {
		l := in.Jobs[i]
		if l.Job == "" || (l.Role != RoleCause && l.Role != RoleFix && l.Role != RoleRelated) {
			return fmt.Errorf("%w: a job link needs a job and the role cause, fix or related", ErrInvalid)
		}
	}

	return nil
}

// Step is a change of the state of an incident.
type Step string

// Steps of an incident.
const (
	StepAcknowledge Step = "acknowledge"
	StepMitigate    Step = "mitigate"
	StepResolve     Step = "resolve"
	StepReopen      Step = "reopen"
	StepClose       Step = "close"
)

// Apply returns in after step by the principal by at at. It returns
// ErrTransition for a step that the state does not allow, and ErrInvalid
// for a close with no root cause or no lessons. Acknowledge makes by the
// commander when the incident has none.
func Apply(in Incident, step Step, by string, at time.Time) (Incident, error) {
	if !allowed(step, in.State) {
		return Incident{}, fmt.Errorf("%w: %s from %s", ErrTransition, step, in.State)
	}

	switch step {
	case StepAcknowledge:
		in.State, in.AcknowledgedAt = StateAcknowledged, at
		if in.Commander == "" {
			in.Commander = by
		}
	case StepMitigate:
		in.State, in.MitigatedAt = StateMitigated, at
	case StepResolve:
		in.State, in.ResolvedAt = StateResolved, at
	case StepReopen:
		in.State, in.ResolvedAt = StateOpen, time.Time{}
	case StepClose:
		if strings.TrimSpace(in.RootCause) == "" || strings.TrimSpace(in.Lessons) == "" {
			return Incident{}, fmt.Errorf("%w: write the root cause and the lessons before the close", ErrInvalid)
		}

		in.State, in.ClosedAt = StateClosed, at
	default:
		// allowed refuses every other step.
	}

	return in, nil
}

// allowed reports whether the state from allows step.
func allowed(step Step, from State) bool {
	switch step {
	case StepAcknowledge:
		return from == StateOpen
	case StepMitigate:
		return from == StateOpen || from == StateAcknowledged
	case StepResolve:
		return from == StateOpen || from == StateAcknowledged || from == StateMitigated
	case StepReopen, StepClose:
		return from == StateResolved
	default:
		return false
	}
}

// LinkRole tells how a job relates to an incident.
type LinkRole string

// Roles of a job link. The roles let the delivery metrics read which
// change caused an incident and which change fixed it.
const (
	RoleCause   LinkRole = "cause"
	RoleFix     LinkRole = "fix"
	RoleRelated LinkRole = "related"
)

// JobLink links a job to an incident, with its role.
type JobLink struct {
	Job  string
	Role LinkRole
}

// Note is one line of the timeline of an incident. Notes are append-only.
type Note struct {
	Incident string
	At       time.Time
	By       string
	Text     string
}

// Validate returns ErrInvalid for a note with no incident, no person, no
// time, or a text that is empty or above MaxNoteLen.
func (n Note) Validate() error {
	switch {
	case n.Incident == "" || n.By == "" || n.At.IsZero():
		return fmt.Errorf("%w: a note needs an incident, a person and a time", ErrInvalid)
	case strings.TrimSpace(n.Text) == "" || len(n.Text) > MaxNoteLen:
		return fmt.Errorf("%w: a note has 1 to %d bytes", ErrInvalid, MaxNoteLen)
	default:
		return nil
	}
}
