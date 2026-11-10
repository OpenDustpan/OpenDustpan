// Package notify is the one notification model of OpenDustpan. A change
// writes its notifications in the transaction of the change, next to its
// audit entries, so a notification never exists for a change that rolled
// back, and a committed change never loses its notification. A dispatcher
// reads the notifications of each scope in order, matches them with
// subscriptions, and delivers each one to the sinks of the matching
// subscriptions: email, a chat webhook, or a signed generic webhook.
// Reminders, escalations and integrations all read this one stream.
//
// A notification is a fact for people and tools to act on. It is not the
// audit log: the audit log is the record, and a notification can be
// missing from a sink that was down for long enough. A notification never
// holds a secret value, a credential, state or a plan.
package notify

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by the ports.
var (
	// ErrInvalid reports a notification, a sink, a subscription or a
	// delivery that fails validation, or a write whose fence does not
	// belong to the scope of a new row.
	ErrInvalid = errors.New("invalid notification data")
	// ErrNotFound reports an unknown sink, subscription or delivery, or a
	// stored row in a scope that the fence of the write does not name.
	ErrNotFound = errors.New("notification data not found")
	// ErrConflict reports a write that names an older version or cursor.
	ErrConflict = errors.New("notification data changed since it was read")
	// ErrInUse reports a delete of a sink that a subscription uses.
	ErrInUse = errors.New("sink is in use")
	// ErrNoURL reports a sink that has no URL the server can send to: the
	// URL item is missing, it does not open, or it holds a URL that does not
	// fit the sink. A send to such a sink fails closed and sends nothing. A
	// person sets the URL of the sink again.
	ErrNoURL = errors.New("the sink has no URL that the server can use: set the URL of the sink again")
	// ErrPermanent reports a delivery that the receiver refused for good,
	// for example an unknown address or a removed webhook. The dispatcher
	// does not try it again.
	ErrPermanent = errors.New("delivery refused for good")
	// ErrTemporary reports a delivery that may work later, for example a
	// receiver that does not answer. The dispatcher tries it again.
	ErrTemporary = errors.New("delivery failed for now")
	// ErrNoAccess reports a delivery whose principal can no longer read
	// the notification, or whose contact is gone. The dispatcher skips it.
	ErrNoAccess = errors.New("the principal of the delivery can no longer read the notification")
)

// Bounds of a notification.
const (
	// IDPrefix starts every notification ID.
	IDPrefix = "ntf-"
	// MaxSummaryLen caps the summary of a notification, in bytes.
	MaxSummaryLen = 500
	// MaxLinks caps the links of a notification.
	MaxLinks = 10
	// MaxLinkURLLen caps the URL of a link, in bytes.
	MaxLinkURLLen = 2048
	// MaxLinkTitleLen caps the title of a link, in bytes.
	MaxLinkTitleLen = 200
	// MaxAttributes caps the attributes of a notification.
	MaxAttributes = 20
	// MaxAttributeLen caps the key and the value of an attribute, and the
	// dedupe key, in bytes.
	MaxAttributeLen = 200
	// MaxRecipients caps the recipients of a notification.
	MaxRecipients = 50
)

// Type names what happened, as "<object>.<what>".
type Type string

// The catalog of notification types. A new type is a change of this list
// and of the reference documentation.
const (
	JobStarted            Type = "job.started"
	JobAwaitingApproval   Type = "job.awaiting-approval"
	JobSucceeded          Type = "job.succeeded"
	JobFailed             Type = "job.failed"
	JobCanceled           Type = "job.canceled"
	JobRejected           Type = "job.rejected"
	JobExpired            Type = "job.expired"
	FreezeUpcoming        Type = "freeze.upcoming"
	FreezeStarted         Type = "freeze.started"
	FreezeEnded           Type = "freeze.ended"
	BreakGlassUsed        Type = "break-glass.used"
	BreakGlassNotice      Type = "break-glass.review-notice"
	BreakGlassReviewed    Type = "break-glass.reviewed"
	OnCallHandoff         Type = "on-call.handoff"
	OnCallSyncFailed      Type = "on-call.sync-failed"
	CostEstimated         Type = "cost.estimated"
	BudgetThreshold       Type = "budget.threshold"
	IncidentOpened        Type = "incident.opened"
	IncidentChanged       Type = "incident.changed"
	IncidentResolved      Type = "incident.resolved"
	LockHeldLong          Type = "lock.held-long"
	SubscriptionSuspended Type = "subscription.suspended"
	// IssuerKeyNext reports a new next key of the OIDC issuer of a
	// namespace, with the time when it becomes active.
	IssuerKeyNext Type = "issuer.key-next"
	// IssuerKeyRevoked reports an emergency rotation of the signing keys of
	// the issuer of a namespace.
	IssuerKeyRevoked Type = "issuer.key-revoked"
	// IssuerKeyCopyMissing reports a copy check at the issuerURL of a
	// namespace that failed.
	IssuerKeyCopyMissing Type = "issuer.key-copy-missing"
	// SignInLocked reports a lock of the password sign-in by name, or of the
	// second factors of a person, after failed attempts. It goes to the
	// administrators of the installation.
	SignInLocked Type = "sign-in.locked"
	// MembershipMassRemoval reports that one source removed members from
	// groups at a rate that crossed the removal alert limits. It goes to
	// the administrators of the installation.
	MembershipMassRemoval Type = "membership.mass-removal"
	// SinkTest is the fixed message of the test route of a sink. A person
	// sends it to check a sink. It reports no event.
	SinkTest Type = "sink.test"
)

// Types returns the catalog of notification types, in order.
func Types() []Type {
	return []Type{
		JobStarted, JobAwaitingApproval, JobSucceeded, JobFailed, JobCanceled, JobRejected, JobExpired,
		FreezeUpcoming, FreezeStarted, FreezeEnded,
		BreakGlassUsed, BreakGlassNotice, BreakGlassReviewed,
		OnCallHandoff, OnCallSyncFailed, CostEstimated, BudgetThreshold,
		IncidentOpened, IncidentChanged, IncidentResolved, LockHeldLong, SubscriptionSuspended,
		IssuerKeyNext, IssuerKeyRevoked, IssuerKeyCopyMissing,
		SignInLocked,
		MembershipMassRemoval,
		SinkTest,
	}
}

// Severity orders notifications for filters and for quiet hours.
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

// LinkKind names what a link of a notification opens. The kinds other
// than LinkConsole are the kinds of a link in the context of an object.
type LinkKind string

// Kinds of a link.
const (
	LinkConsole    LinkKind = "console"
	LinkDocs       LinkKind = "docs"
	LinkRunbook    LinkKind = "runbook"
	LinkDashboard  LinkKind = "dashboard"
	LinkRepository LinkKind = "repository"
	LinkOther      LinkKind = "other"
)

// Link is a link that a sink shows with a notification: the page of the
// object in the console, or a link from the context of the unit, for
// example its runbook. The URL is http or https, as in the context of an
// object. The server never fetches it.
type Link struct {
	Title string
	URL   string
	Kind  LinkKind
}

// valid reports whether l has a title, a known kind and an http or https
// URL with a host, within the bounds.
func (l Link) valid() bool {
	if l.Title == "" || len(l.Title) > MaxLinkTitleLen || len(l.URL) > MaxLinkURLLen {
		return false
	}

	switch l.Kind {
	case LinkConsole, LinkDocs, LinkRunbook, LinkDashboard, LinkRepository, LinkOther:
	default:
		return false
	}

	u, err := url.Parse(l.URL)
	if err != nil || u == nil {
		return false
	}

	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

// Recipient names a person or a group that a notice is for. Exactly one
// field is set. Admins names the administrators of the scope of the
// notification: each principal that holds the action to manage the
// notifications of the scope (see Contacts.Admins). A subscription routes
// every notification. A recipient routes a notice also to the personal
// contacts of each person.
type Recipient struct {
	Principal string
	Group     string
	Admins    bool
}

// valid reports whether exactly one field of r is set.
func (r Recipient) valid() bool {
	set := 0

	for _, ok := range []bool{r.Principal != "", r.Group != "", r.Admins} {
		if ok {
			set++
		}
	}

	return set == 1
}

// Notification is one fact. Scope is the namespace of the object, or the
// server scope of the audit log. Object is the reference of the object,
// for example a unit reference, and decides who can read the notification.
// Subject and Agent name who caused the notification, and stay empty for
// a notification of the server, for example a scheduled freeze. DedupeKey
// is the same for two notifications that tell the same fact, for example
// "bgr-1/overdue": the outbox keeps one notification for each scope and
// dedupe key. Sequence orders the notifications of one scope: the store
// sets it at append.
type Notification struct {
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

// Validate returns ErrInvalid for a notification that breaks a bound, with
// an unknown type, with no dedupe key, or with a link that is not an http
// or https URL.
func (n Notification) Validate() error {
	if err := n.validateHead(); err != nil {
		return err
	}

	return n.validateParts()
}

// Fit returns n with its text cut to the bounds, so that a notification
// never fails the domain write that carries it. It cuts the summary, the
// link titles and the attribute values at a rune boundary, replaces a
// dedupe key above the bound with a prefix of it and the SHA-256 of the
// full key (see fitKey), uses the type as the summary and the ID as the
// dedupe key when they are empty, drops each link
// that is not valid and each attribute with a key that is not valid, and
// keeps the first MaxLinks links, the MaxAttributes attributes with the
// lowest keys, and the first MaxRecipients valid recipients. Fit does not
// change the ID, the type, the scope, the severity or the time: an error
// in those is an error of the code, and Validate still refuses it.
func (n Notification) Fit() Notification {
	n.Summary = cut(n.Summary, MaxSummaryLen)
	if n.Summary == "" {
		n.Summary = string(n.Type)
	}

	n.DedupeKey = fitKey(cmp.Or(n.DedupeKey, n.ID))

	links := make([]Link, 0, min(len(n.Links), MaxLinks))
	for _, l := range n.Links {
		l.Title = cut(l.Title, MaxLinkTitleLen)
		if l.valid() && len(links) < MaxLinks {
			links = append(links, l)
		}
	}

	n.Links = links

	attrs := make(map[string]string, min(len(n.Attributes), MaxAttributes))
	for _, k := range slices.Sorted(maps.Keys(n.Attributes)) {
		if validKey(k) && len(attrs) < MaxAttributes {
			attrs[k] = cut(n.Attributes[k], MaxAttributeLen)
		}
	}

	n.Attributes = attrs

	recipients := make([]Recipient, 0, min(len(n.Recipients), MaxRecipients))
	for _, r := range n.Recipients {
		if r.valid() && len(recipients) < MaxRecipients {
			recipients = append(recipients, r)
		}
	}

	n.Recipients = recipients

	return n
}

// fitKey returns a dedupe key of at most MaxAttributeLen bytes. A key
// above the bound becomes its first bytes, cut at a rune boundary, then
// "#" and the hex SHA-256 of the full key. A cut alone would make two keys
// with the same first bytes one key, and the outbox would then drop the
// second notification.
func fitKey(k string) string {
	if len(k) <= MaxAttributeLen {
		return k
	}

	sum := sha256.Sum256([]byte(k))
	tail := "#" + hex.EncodeToString(sum[:])

	return cut(k, MaxAttributeLen-len(tail)) + tail
}

// cut returns s cut to at most limit bytes, at a rune boundary.
func cut(s string, limit int) string {
	if len(s) <= limit {
		return s
	}

	s = s[:limit]
	for s != "" && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}

	return s
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

// validateHead returns ErrInvalid for an ID, a type, a scope, a time, a
// severity, a summary, a dedupe key or a count of parts that breaks a
// bound.
func (n Notification) validateHead() error {
	switch {
	case !strings.HasPrefix(n.ID, IDPrefix):
		return fmt.Errorf("%w: a notification ID starts with %q", ErrInvalid, IDPrefix)
	case !slices.Contains(Types(), n.Type):
		return fmt.Errorf("%w: unknown type %q", ErrInvalid, n.Type)
	case n.Scope == "" || n.At.IsZero():
		return fmt.Errorf("%w: a notification needs a scope and a time", ErrInvalid)
	case n.Severity.String() == "unknown":
		return fmt.Errorf("%w: unknown severity %d", ErrInvalid, n.Severity)
	case !between(len(n.Summary), 1, MaxSummaryLen):
		return fmt.Errorf("%w: a summary has 1 to %d bytes", ErrInvalid, MaxSummaryLen)
	case !between(len(n.DedupeKey), 1, MaxAttributeLen):
		return fmt.Errorf("%w: a dedupe key has 1 to %d bytes", ErrInvalid, MaxAttributeLen)
	case len(n.Links) > MaxLinks || len(n.Attributes) > MaxAttributes || len(n.Recipients) > MaxRecipients:
		return fmt.Errorf("%w: too many links, attributes or recipients", ErrInvalid)
	default:
		return nil
	}
}

// validateParts returns ErrInvalid for a link, an attribute or a recipient
// of the notification that breaks a bound.
func (n Notification) validateParts() error {
	for _, l := range n.Links {
		if !l.valid() {
			return fmt.Errorf("%w: a link needs a title, a kind and an http or https URL", ErrInvalid)
		}
	}

	for k, v := range n.Attributes {
		if !validKey(k) || len(v) > MaxAttributeLen {
			return fmt.Errorf("%w: attribute %q", ErrInvalid, k)
		}
	}

	for _, r := range n.Recipients {
		if !r.valid() {
			return fmt.Errorf("%w: a recipient names a principal, a group or the administrators", ErrInvalid)
		}
	}

	return nil
}

// between reports whether v is in the closed range from lo to hi.
func between(v, lo, hi int) bool { return v >= lo && v <= hi }
