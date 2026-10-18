// Package breakglass holds the review after use of each break-glass: a
// second person reads what was done and records a verdict, by a deadline
// on the business calendar. Reminders and escalations go out on the same
// calendar until the review is done.
//
// Two kinds of use need a review: a job that started through an active
// change freeze, and a session of a break-glass account. The review is the
// control after the fact that takes the place of an approval before use,
// because an approval cannot wait in an emergency.
//
// The functions of this package are pure: they read a review, the
// settings, a calendar and a time. The Store port keeps reviews.
package breakglass

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrInvalid reports a use, a review or settings that fail validation.
	ErrInvalid = errors.New("invalid break-glass review")
	// ErrNotFound reports an unknown review ID.
	ErrNotFound = errors.New("break-glass review not found")
	// ErrConflict reports a write that names an older version.
	ErrConflict = errors.New("break-glass review changed since it was read")
	// ErrReviewed reports a verdict on a review that already has one.
	ErrReviewed = errors.New("break-glass review already has a verdict")
	// ErrSelfReview reports a verdict by the person who used break-glass.
	ErrSelfReview = errors.New("the person who used break-glass cannot review it")
	// ErrAgentDenied reports a verdict that an agent records.
	ErrAgentDenied = errors.New("an agent cannot review a break-glass")
	// ErrAuthStale reports a verdict with no fresh authentication.
	ErrAuthStale = errors.New("a review needs a fresh authentication")
)

// Bounds of a review.
const (
	// IDPrefix starts every review ID.
	IDPrefix = "bgr-"
	// MaxNoteLen caps the note of a verdict, in bytes.
	MaxNoteLen = 4096
	// Freshness is the maximum age of the authentication of a verdict. It
	// is the same number as for the confirmation of a job.
	Freshness = 15 * time.Minute
)

// Kind is the kind of a break-glass use.
type Kind string

// Kinds of use.
const (
	// KindFreezeBreak is a job that started through an active freeze. Ref
	// is the job ID.
	KindFreezeBreak Kind = "freeze-break"
	// KindAccountSession is a session of a break-glass account. Ref is the
	// session ID. The review covers each action of the session.
	KindAccountSession Kind = "account-session"
)

// Use is one break-glass that needs a review.
type Use struct {
	Kind Kind
	Ref  string
	// Scope is the namespace of the job, or the server scope of the audit
	// log for a session of a break-glass account.
	Scope namespace.Name
	// Object is the unit reference of the job, or empty for a session.
	Object string
	// Subject is the principal that used break-glass. Agent is empty: an
	// agent can never use break-glass.
	Subject string
	// Reason is the text that the person typed.
	Reason string
	// At is the time of the use: the start of the job, or the end of the
	// session. The deadline counts from it.
	At time.Time
}

// Validate returns ErrInvalid for a use with a missing field or an unknown
// kind.
func (u Use) Validate() error {
	switch {
	case u.Kind != KindFreezeBreak && u.Kind != KindAccountSession:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, u.Kind)
	case u.Ref == "" || u.Scope == "" || u.Subject == "" || u.At.IsZero():
		return fmt.Errorf("%w: a use needs a reference, a scope, a subject and a time", ErrInvalid)
	case u.Reason == "":
		return fmt.Errorf("%w: a use needs a reason", ErrInvalid)
	case u.Kind == KindFreezeBreak && u.Object == "":
		return fmt.Errorf("%w: a freeze break needs the unit of its job", ErrInvalid)
	default:
		return nil
	}
}

// State is the state of a review.
type State string

// States of a review. A review starts pending and ends with a verdict. A
// verdict is final.
const (
	// StatePending waits for a verdict. A pending review after its
	// deadline is overdue (see Review.Overdue).
	StatePending State = "pending"
	// StateAccepted records that the reviewer found the use justified.
	StateAccepted State = "accepted"
	// StateDisputed records that the reviewer did not find the use
	// justified. The note says why. The server sends an event, and the
	// follow-up is outside the review.
	StateDisputed State = "disputed"
)

// AuthKind names how the reviewer authenticated for the verdict.
type AuthKind string

// Kinds of fresh authentication.
const (
	// AuthMFA is a new step of the built-in MFA.
	AuthMFA AuthKind = "mfa"
	// AuthSSO is a new authentication at the identity provider, asked with
	// a maximum age.
	AuthSSO AuthKind = "sso"
)

// Review is the review of one use. DueAt is zero when the deadlines of the
// installation are off. Calendar names the calendar that gave DueAt, and
// CalendarFrom the scope that gave the calendar, so that a reader can see
// why the deadline falls where it does.
type Review struct {
	ID           string
	Use          Use
	State        State
	CreatedAt    time.Time
	DueAt        time.Time
	Calendar     calendar.ID
	CalendarFrom calendar.Scope
	// Step is the count of notice steps already sent.
	Step int
	// NextNoticeAt is the time of the next notice step, or zero when no
	// step remains or the review has a verdict.
	NextNoticeAt time.Time
	Verdict      Verdict
	Version      int64
}

// Verdict is the result of a review. It is empty while the review is
// pending.
type Verdict struct {
	Reviewer   string
	Note       string
	Auth       AuthKind
	AuthAt     time.Time
	ReviewedAt time.Time
}

// Overdue reports whether r is pending after its deadline at now.
func (r Review) Overdue(now time.Time) bool {
	return r.State == StatePending && !r.DueAt.IsZero() && !now.Before(r.DueAt)
}

// Reviewer is the person who records a verdict, and the authentication
// of the request.
type Reviewer struct {
	Subject string
	// Agent is the agent that acts for the subject, or empty.
	Agent  string
	Auth   AuthKind
	AuthAt time.Time
}

// CheckReviewer returns nil when rev can record a verdict on r at now. It
// returns ErrReviewed for a review with a verdict, ErrAgentDenied when an
// agent acts, ErrSelfReview when the reviewer used the break-glass, and
// ErrAuthStale for an authentication that is missing, of an unknown kind,
// in the future, or older than Freshness. The caller checks the action of
// the reviewer first.
func CheckReviewer(r Review, rev Reviewer, now time.Time) error {
	switch {
	case r.State != StatePending:
		return fmt.Errorf("%w: %s is %s", ErrReviewed, r.ID, r.State)
	case rev.Agent != "":
		return fmt.Errorf("%w: agent %s", ErrAgentDenied, rev.Agent)
	case strings.EqualFold(rev.Subject, r.Use.Subject):
		return ErrSelfReview
	case rev.Auth != AuthMFA && rev.Auth != AuthSSO,
		rev.AuthAt.After(now),
		now.Sub(rev.AuthAt) > Freshness:
		return fmt.Errorf("%w: authenticate again within %s", ErrAuthStale, Freshness)
	default:
		return nil
	}
}

// Accept returns r with the verdict of rev at now: accepted, with an
// optional note. It returns the errors of CheckReviewer, and ErrInvalid for
// a note above MaxNoteLen.
func Accept(r Review, rev Reviewer, note string, now time.Time) (Review, error) {
	return record(r, rev, StateAccepted, note, now)
}

// Dispute returns r with the verdict of rev at now: disputed, with a note.
// It returns the errors of CheckReviewer, and ErrInvalid for a dispute with
// no note or a note above MaxNoteLen.
func Dispute(r Review, rev Reviewer, note string, now time.Time) (Review, error) {
	return record(r, rev, StateDisputed, note, now)
}

// record returns r with the verdict state of rev at now. A dispute needs a
// note.
func record(r Review, rev Reviewer, state State, note string, now time.Time) (Review, error) {
	if err := CheckReviewer(r, rev, now); err != nil {
		return Review{}, err
	}

	if len(note) > MaxNoteLen || (state == StateDisputed && strings.TrimSpace(note) == "") {
		return Review{}, fmt.Errorf("%w: a dispute needs a note of at most %d bytes", ErrInvalid, MaxNoteLen)
	}

	r.State = state
	r.Verdict = Verdict{Reviewer: rev.Subject, Note: note, Auth: rev.Auth, AuthAt: rev.AuthAt, ReviewedAt: now}
	r.NextNoticeAt = time.Time{}

	return r, nil
}
