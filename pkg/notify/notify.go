// Package event is the one event model of OpenDustpan. A change writes its
// events in the transaction of the change, next to its audit entries, so
// an event never exists for a change that rolled back, and a committed
// change never loses its event. A dispatcher reads the events in order,
// matches them with subscriptions, and delivers each one to the sinks of
// the matching subscriptions: email, a chat webhook, or a signed generic
// webhook. Notifications, reminders, escalations and integrations all read
// this one stream.
//
// An event is a fact for people and tools to act on. It is not the audit
// log: the audit log is the record, and an event can be missing from a
// sink that was down for long enough. An event never holds a secret value,
// a credential, state or a plan.
package event

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by the ports.
var (
	// ErrInvalid reports an event, a sink, a subscription or a delivery
	// that fails validation.
	ErrInvalid = errors.New("invalid event data")
	// ErrNotFound reports an unknown sink, subscription or delivery.
	ErrNotFound = errors.New("event data not found")
	// ErrExists reports a create with an ID that already exists.
	ErrExists = errors.New("event data already exists")
	// ErrConflict reports a write that names an older version or cursor.
	ErrConflict = errors.New("event data changed since it was read")
	// ErrInUse reports a delete of a sink that a subscription uses.
	ErrInUse = errors.New("sink is in use")
	// ErrPermanent reports a delivery that the receiver refused for good,
	// for example an unknown address or a removed webhook. The dispatcher
	// does not try it again.
	ErrPermanent = errors.New("delivery refused for good")
	// ErrTemporary reports a delivery that may work later, for example a
	// receiver that does not answer. The dispatcher tries it again.
	ErrTemporary = errors.New("delivery failed for now")
)

// Bounds of an event.
const (
	// IDPrefix starts every event ID.
	IDPrefix = "evt-"
	// MaxSummaryLen caps the summary of an event, in bytes.
	MaxSummaryLen = 500
	// MaxLinks caps the links of an event.
	MaxLinks = 10
	// MaxAttributes caps the attributes of an event.
	MaxAttributes = 20
	// MaxAttributeLen caps the key and the value of an attribute, in
	// bytes.
	MaxAttributeLen = 200
	// MaxRecipients caps the recipients of an event.
	MaxRecipients = 50
)

// Type names what happened, as "<object>.<what>".
type Type string

// The catalog of event types. A new type is a change of this list and of
// the reference documentation.
const (
	JobStarted          Type = "job.started"
	JobAwaitingApproval Type = "job.awaiting-approval"
	JobSucceeded        Type = "job.succeeded"
	JobFailed           Type = "job.failed"
	JobCanceled         Type = "job.canceled"
	FreezeUpcoming      Type = "freeze.upcoming"
	FreezeStarted       Type = "freeze.started"
	FreezeEnded         Type = "freeze.ended"
	BreakGlassUsed      Type = "break-glass.used"
	BreakGlassNotice    Type = "break-glass.review-notice"
	BreakGlassReviewed  Type = "break-glass.reviewed"
	OnCallHandoff       Type = "on-call.handoff"
	OnCallSyncFailed    Type = "on-call.sync-failed"
	CostEstimated       Type = "cost.estimated"
	BudgetThreshold     Type = "budget.threshold"
	IncidentOpened      Type = "incident.opened"
	IncidentChanged     Type = "incident.changed"
	IncidentResolved    Type = "incident.resolved"
	LockHeldLong        Type = "lock.held-long"
	SubscriptionStopped Type = "subscription.suspended"
)

// Types returns the catalog of event types, in order.
func Types() []Type {
	return []Type{
		JobStarted, JobAwaitingApproval, JobSucceeded, JobFailed, JobCanceled,
		FreezeUpcoming, FreezeStarted, FreezeEnded,
		BreakGlassUsed, BreakGlassNotice, BreakGlassReviewed,
		OnCallHandoff, OnCallSyncFailed, CostEstimated, BudgetThreshold,
		IncidentOpened, IncidentChanged, IncidentResolved, LockHeldLong, SubscriptionStopped,
	}
}

// Severity orders events for filters and for quiet hours.
type Severity int

// Severities, in increasing order.
const (
	SeverityInfo Severity = iota + 1
	SeverityWarning
	SeverityCritical
)

// String returns "info", "warning" or "critical".
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// LinkKind names what a link of an event opens.
type LinkKind string

// Kinds of a link.
const (
	LinkConsole LinkKind = "console"
	LinkRunbook LinkKind = "runbook"
	LinkDocs    LinkKind = "docs"
)

// Link is a link that a sink shows with an event: the page of the object
// in the console, or the runbook of the unit from its context.
type Link struct {
	Title string
	URL   string
	Kind  LinkKind
}

// Recipient names a person or a group that a notice is for. Exactly one
// field is set. A subscription routes every event. A recipient routes a
// notice also to the personal contacts of each person.
type Recipient struct {
	Principal string
	Group     string
}

// Event is one fact. Scope is the namespace of the object, or the server
// scope of the audit log. Subject and Agent name who caused the event, and
// stay empty for an event of the server, for example a scheduled freeze.
// DedupeKey is the same for two events that tell the same thing, for
// example "bgr-1/overdue", so a receiver can drop the second. Sequence
// orders the events of the installation: the store sets it at append.
type Event struct {
	ID         string
	Type       Type
	Scope      namespace.Name
	Object     string
	ObjectID   string
	Subject    string
	Agent      string
	Severity   Severity
	Summary    string
	Links      []Link
	Attributes map[string]string
	Recipients []Recipient
	DedupeKey  string
	At         time.Time
	Sequence   int64
}

// Validate returns ErrInvalid for an event that breaks a bound, with an
// unknown type, or with a link that is not https.
func (e Event) Validate() error {
	switch {
	case !strings.HasPrefix(e.ID, IDPrefix):
		return fmt.Errorf("%w: an event ID starts with %q", ErrInvalid, IDPrefix)
	case !slices.Contains(Types(), e.Type):
		return fmt.Errorf("%w: unknown type %q", ErrInvalid, e.Type)
	case e.Scope == "" || e.At.IsZero():
		return fmt.Errorf("%w: an event needs a scope and a time", ErrInvalid)
	case e.Severity < SeverityInfo || e.Severity > SeverityCritical:
		return fmt.Errorf("%w: unknown severity %d", ErrInvalid, e.Severity)
	case e.Summary == "" || len(e.Summary) > MaxSummaryLen:
		return fmt.Errorf("%w: a summary has 1 to %d bytes", ErrInvalid, MaxSummaryLen)
	case len(e.Links) > MaxLinks || len(e.Attributes) > MaxAttributes || len(e.Recipients) > MaxRecipients:
		return fmt.Errorf("%w: too many links, attributes or recipients", ErrInvalid)
	}

	return e.validateParts()
}

// validateParts returns ErrInvalid for a link, an attribute or a recipient
// of the event that breaks a bound.
func (e Event) validateParts() error {
	for _, l := range e.Links {
		if !strings.HasPrefix(l.URL, "https://") || l.Title == "" {
			return fmt.Errorf("%w: a link needs a title and an https URL", ErrInvalid)
		}
	}

	for k, v := range e.Attributes {
		if !validKey(k) || len(v) > MaxAttributeLen {
			return fmt.Errorf("%w: attribute %q", ErrInvalid, k)
		}
	}

	for _, r := range e.Recipients {
		if (r.Principal == "") == (r.Group == "") {
			return fmt.Errorf("%w: a recipient names a principal or a group", ErrInvalid)
		}
	}

	return nil
}

// validKey reports whether k has 1 to MaxAttributeLen lowercase letters,
// digits, dots and dashes.
func validKey(k string) bool {
	if k == "" || len(k) > MaxAttributeLen {
		return false
	}

	for _, r := range k {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '-' {
			return false
		}
	}

	return true
}
