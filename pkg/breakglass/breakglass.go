// Package breakglass holds the review after use of each break-glass: a
// second person reads what was done and records a verdict, by a deadline
// on the business calendar. Reminders and escalations go out on the same
// calendar until the review is done.
//
// Three kinds of use need a review: a job that started through an active
// change freeze, a session of a break-glass account, and a temporary
// administrator that the host command made for recovery. The review is the
// control after the fact that takes the place of an approval before use,
// because an approval cannot wait in an emergency.
//
// The functions of this package are pure: they read a review, the
// settings, a calendar and a time. The Store port keeps reviews.
package breakglass

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/calendar"
	"opendustpan.org/opendustpan/pkg/audit"
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
	// ErrSelfReview reports a verdict by a person of Use.People: the
	// person who used break-glass, the owner of the break-glass account,
	// or a principal that could act as the person at the time of the use.
	ErrSelfReview = errors.New("a person of the break-glass use cannot review it")
	// ErrAgentDenied reports a verdict that an agent records.
	ErrAgentDenied = errors.New("an agent cannot review a break-glass")
	// ErrNeedsSession reports a verdict that does not come from a console
	// session, for example a request with an access token.
	ErrNeedsSession = errors.New("a review needs a console session, not an access token")
	// ErrAuthStale reports a verdict with no fresh authentication.
	ErrAuthStale = errors.New("a review needs a fresh authentication")
	// ErrInUse reports a verdict on the review of a break-glass account
	// session before the time limit of the session.
	ErrInUse = errors.New("the break-glass session is still in its time limit")
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
	// MaxClockSkew is the time that the ReviewedAt of a verdict can be
	// after the time of the transaction of the store that records it.
	MaxClockSkew = time.Minute
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
	// KindTempAdmin is a temporary administrator that the host command
	// made for recovery. Ref is the principal ID. The review covers each
	// action of the principal from its create to its end.
	KindTempAdmin Kind = "temp-admin"
)

// Use is one break-glass that needs a review.
type Use struct {
	Kind Kind
	Ref  string
	// Scope is the namespace of the job, or audit.ServerScope for a session
	// of a break-glass account.
	Scope namespace.Name
	// Object is the unit reference of the job, or empty for a session.
	Object string
	// Subject is the principal that used break-glass. Agent is empty: an
	// agent can never use break-glass.
	Subject string
	// People are the principals that cannot review the use, as the server
	// knows them at the time of the use: the subject, the named owner of a
	// break-glass account, and each principal that can act as the subject.
	// It holds the subject.
	People []string
	// Reason is the text that the person typed.
	Reason string
	// At is the time of the use: the start of the job, or the sign-in of
	// the break-glass account.
	At time.Time
	// Until is the time limit of a session of a break-glass account, set
	// at the sign-in, the end of a temporary administrator (its expiry or
	// an earlier end), or zero for a freeze break. The server revokes the
	// session at Until, and a verdict waits until then, so that the review
	// covers each action of the session. A sign-out before Until writes
	// nothing to the review.
	Until time.Time
}

// Validate returns ErrInvalid for a use with a missing field, an unknown
// kind, or a scope that does not match the kind: a session of a
// break-glass account is in audit.ServerScope, and a freeze break is in
// the namespace of its job.
func (u Use) Validate() error {
	switch {
	case u.Ref == "" || u.Scope == "" || u.Subject == "" || u.At.IsZero():
		return fmt.Errorf("%w: a use needs a reference, a scope, a subject and a time", ErrInvalid)
	case u.Reason == "":
		return fmt.Errorf("%w: a use needs a reason", ErrInvalid)
	case !hasPerson(u.People, u.Subject) || hasPerson(u.People, ""):
		return fmt.Errorf("%w: the people of a use hold the subject and no empty name", ErrInvalid)
	default:
		return u.validateKind()
	}
}

// validateKind returns ErrInvalid for an unknown kind, or for a scope, a
// unit or a time limit that does not match the kind of u.
func (u Use) validateKind() error {
	switch u.Kind {
	case KindFreezeBreak:
		if u.Object == "" || u.Scope == audit.ServerScope || !u.Until.IsZero() {
			return fmt.Errorf(
				"%w: a freeze break needs the unit and the namespace of its job, and no time limit", ErrInvalid,
			)
		}
	case KindAccountSession, KindTempAdmin:
		if u.Object != "" || u.Scope != audit.ServerScope || !u.Until.After(u.At) {
			return fmt.Errorf(
				"%w: a break-glass session is in the scope %s, with no unit and a time limit after its sign-in",
				ErrInvalid, audit.ServerScope,
			)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, u.Kind)
	}

	return nil
}

// hasPerson reports whether people holds name, with no case.
func hasPerson(people []string, name string) bool {
	for _, p := range people {
		if strings.EqualFold(p, name) {
			return true
		}
	}

	return false
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
	// justified. The note says why. The server sends a notification, and
	// the follow-up is outside the review.
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
	// ActAs is Reviewer.ActAs at the verdict, so that the store checks the
	// verdict again against it.
	ActAs []string
}

// empty reports whether v is the verdict of a pending review.
func (v Verdict) empty() bool {
	return v.Reviewer == "" && v.Note == "" && v.Auth == "" && v.AuthAt.IsZero() &&
		v.ReviewedAt.IsZero() && len(v.ActAs) == 0
}

// ValidateOpen returns ErrInvalid for a review that a store cannot open:
// a use that fails Validate, an ID without IDPrefix, a state that is not
// pending, a verdict, a notice step already sent, or times that do not
// agree. The times agree when CreatedAt is set and not before Use.At, and
// DueAt and NextNoticeAt are each zero or not before CreatedAt.
func (r Review) ValidateOpen() error {
	if err := r.Use.Validate(); err != nil {
		return err
	}

	switch {
	case !strings.HasPrefix(r.ID, IDPrefix) || len(r.ID) == len(IDPrefix):
		return fmt.Errorf("%w: a review ID starts with %s", ErrInvalid, IDPrefix)
	case r.State != StatePending || !r.Verdict.empty() || r.Step != 0:
		return fmt.Errorf("%w: a new review is pending, with no verdict and no notice sent", ErrInvalid)
	default:
		return validateOpenTimes(r)
	}
}

// validateOpenTimes returns ErrInvalid when the times of the new review r
// do not agree.
func validateOpenTimes(r Review) error {
	switch {
	case r.CreatedAt.IsZero() || r.CreatedAt.Before(r.Use.At):
		return fmt.Errorf("%w: a review opens at or after its use", ErrInvalid)
	case (!r.DueAt.IsZero() && r.DueAt.Before(r.CreatedAt)) ||
		(!r.NextNoticeAt.IsZero() && r.NextNoticeAt.Before(r.CreatedAt)):
		return fmt.Errorf("%w: the deadline and the next notice of a review are not before its opening", ErrInvalid)
	default:
		return nil
	}
}

// Overdue reports whether r is pending at or after its deadline at now. A
// Filter with DueBefore set to now selects the same reviews.
func (r Review) Overdue(now time.Time) bool {
	return r.State == StatePending && !r.DueAt.IsZero() && !now.Before(r.DueAt)
}

// Reviewer is the person who records a verdict, and the authentication
// of the request. The server fills each field from the request: Session,
// Auth and AuthAt come from the console session of the reviewer, never
// from the body of the request.
type Reviewer struct {
	Subject string
	// Agent is the agent that acts for the subject, or empty.
	Agent string
	// Session is the ID of the console session of the request, or empty
	// when the request has no console session.
	Session string
	// TokenID is the ID of the access token of the request, or empty.
	TokenID string
	Auth    AuthKind
	AuthAt  time.Time
	// ActAs are the principals that held an act-as edge to the subject of
	// the use at a time from Use.At to the end of the use (Use.Until for a
	// session, the verdict for a freeze break). The server fills it from
	// the grant history, because a principal that gets an edge during the
	// use is not in Use.People. For example, a break-glass account can
	// grant act-as to a colleague during its session.
	ActAs []string
}

// CheckReviewer returns nil when rev can record a verdict on r at now. It
// returns ErrReviewed for a review with a verdict, ErrInvalid for a
// reviewer with no subject, ErrAgentDenied when an agent acts,
// ErrNeedsSession for a request with no console session or with an access
// token, ErrSelfReview when the reviewer is one of r.Use.People, of
// rev.ActAs, or the subject of the use, ErrInUse for a break-glass session before its time
// limit, and ErrAuthStale for an authentication that is missing, of an
// unknown kind, in the future, or older than Freshness. The caller checks
// the action of the reviewer first.
func CheckReviewer(r Review, rev Reviewer, now time.Time) error {
	switch {
	case r.State != StatePending:
		return fmt.Errorf("%w: %s is %s", ErrReviewed, r.ID, r.State)
	case strings.TrimSpace(rev.Subject) == "":
		return fmt.Errorf("%w: a verdict needs a reviewer", ErrInvalid)
	case rev.Agent != "":
		return fmt.Errorf("%w: agent %s", ErrAgentDenied, rev.Agent)
	case rev.Session == "" || rev.TokenID != "":
		return ErrNeedsSession
	case excluded(r.Use, rev.ActAs, rev.Subject):
		return ErrSelfReview
	case now.Before(r.Use.Until):
		return fmt.Errorf("%w: review it after %s", ErrInUse, r.Use.Until.UTC().Format(time.RFC3339))
	case !fresh(rev.Auth, rev.AuthAt, now):
		return fmt.Errorf("%w: authenticate again within %s", ErrAuthStale, Freshness)
	default:
		return nil
	}
}

// excluded reports whether reviewer is the subject of u, one of u.People,
// or one of actAs, with no case.
func excluded(u Use, actAs []string, reviewer string) bool {
	return strings.EqualFold(reviewer, u.Subject) || hasPerson(u.People, reviewer) || hasPerson(actAs, reviewer)
}

// fresh reports whether an authentication of the kind kind, at the time
// at, is fresh at now.
func fresh(kind AuthKind, at, now time.Time) bool {
	return (kind == AuthMFA || kind == AuthSSO) && !at.IsZero() && !at.After(now) && now.Sub(at) <= Freshness
}

// CheckVerdict returns nil when v, with the state state, is a verdict
// that a store can record on the stored review r. A store calls it with
// the review that it holds, not with the review of the caller. It returns
// ErrReviewed for a stored review with a verdict, ErrSelfReview for a
// reviewer that is one of r.Use.People, of v.ActAs, or the subject of the
// use, ErrInUse for a verdict before the time limit of a session, and
// ErrInvalid for a state that is not a verdict, a verdict with no
// reviewer, a ReviewedAt before Use.At or before CreatedAt, a note above
// MaxNoteLen, a dispute with no note, or an authentication that was not
// fresh at ReviewedAt. It cannot check a time against the clock: a store
// also refuses a ReviewedAt after its own time plus MaxClockSkew.
func CheckVerdict(r Review, state State, v Verdict) error {
	switch {
	case r.State != StatePending:
		return fmt.Errorf("%w: %s is %s", ErrReviewed, r.ID, r.State)
	case state != StateAccepted && state != StateDisputed:
		return fmt.Errorf("%w: unknown verdict %q", ErrInvalid, state)
	case strings.TrimSpace(v.Reviewer) == "" || v.ReviewedAt.IsZero():
		return fmt.Errorf("%w: a verdict needs a reviewer and a time", ErrInvalid)
	case excluded(r.Use, v.ActAs, v.Reviewer):
		return ErrSelfReview
	case v.ReviewedAt.Before(r.Use.Until):
		return fmt.Errorf("%w: review it after %s", ErrInUse, r.Use.Until.UTC().Format(time.RFC3339))
	case v.ReviewedAt.Before(r.Use.At) || v.ReviewedAt.Before(r.CreatedAt):
		return fmt.Errorf("%w: a verdict is not before its use or its review", ErrInvalid)
	default:
		return checkNoteAndAuth(state, v)
	}
}

// checkNoteAndAuth returns ErrInvalid for a note above MaxNoteLen, a
// dispute with no note, or an authentication that was not fresh at the
// time of the verdict.
func checkNoteAndAuth(state State, v Verdict) error {
	if len(v.Note) > MaxNoteLen || (state == StateDisputed && strings.TrimSpace(v.Note) == "") {
		return fmt.Errorf("%w: a dispute needs a note of at most %d bytes", ErrInvalid, MaxNoteLen)
	}

	if !fresh(v.Auth, v.AuthAt, v.ReviewedAt) {
		return fmt.Errorf("%w: the authentication of a verdict is at most %s old", ErrInvalid, Freshness)
	}

	return nil
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

	v := Verdict{
		Reviewer: rev.Subject, Note: note, Auth: rev.Auth, AuthAt: rev.AuthAt, ReviewedAt: now,
		ActAs: slices.Clone(rev.ActAs),
	}
	if err := CheckVerdict(r, state, v); err != nil {
		return Review{}, err
	}

	r.State = state
	r.Verdict = v
	r.NextNoticeAt = time.Time{}

	return r, nil
}
