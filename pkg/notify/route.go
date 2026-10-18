package notify

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Bounds of sinks and subscriptions.
const (
	// SinkIDPrefix starts every sink ID.
	SinkIDPrefix = "snk-"
	// SubscriptionIDPrefix starts every subscription ID.
	SubscriptionIDPrefix = "sub-"
	// MaxFilterEntries caps the types and the objects of a filter.
	MaxFilterEntries = 50
	// MaxNameLen caps the name of a sink, in bytes.
	MaxNameLen = 100
	// MaxAddressLen caps the email address of a sink, in bytes.
	MaxAddressLen = 254
	// MaxSigningSecrets caps the active signing secrets of a webhook sink.
	// Two let an operator add a new secret, move the receiver to it, and
	// then remove the old one, with no delivery that the receiver refuses.
	MaxSigningSecrets = 2
)

// SinkKind names the kind of a sink.
type SinkKind string

// Kinds of a sink. An adapter of the Sender port serves each kind.
const (
	// SinkEmail sends an email to the address Target, or to the email
	// contacts of the recipients of a notice.
	SinkEmail SinkKind = "email"
	// SinkChat posts a message to the incoming webhook URL Target of a
	// chat tool. The secret, when set, is the token of the webhook.
	SinkChat SinkKind = "chat"
	// SinkWebhook posts the notification as JSON to the https URL Target,
	// signed with each active secret of the sink (see Sign).
	SinkWebhook SinkKind = "webhook"
)

// Sink is a destination of notifications in a scope. Target is an email
// address or an https URL. SecretRefs name the stored secrets of the sink:
// the one or two active signing keys of a webhook, or the token of a chat
// webhook. The API never answers a secret, and the dispatcher reads them
// only to deliver.
type Sink struct {
	ID         string
	Scope      namespace.Name
	Kind       SinkKind
	Name       string
	Target     string
	SecretRefs []string
	CreatedBy  string
	Version    int64
}

// Validate returns ErrInvalid for a sink that breaks a bound, for a
// webhook with no secret or more than MaxSigningSecrets secrets, and for a
// target that does not fit the kind. The URL of a chat or webhook sink
// must pass egress.CheckURL for https only: the error then also matches
// egress.ErrRefused.
func (s Sink) Validate() error {
	switch {
	case !strings.HasPrefix(s.ID, SinkIDPrefix):
		return fmt.Errorf("%w: a sink ID starts with %q", ErrInvalid, SinkIDPrefix)
	case s.Scope == "" || s.Name == "" || len(s.Name) > MaxNameLen:
		return fmt.Errorf("%w: a sink needs a scope and a name of 1 to %d bytes", ErrInvalid, MaxNameLen)
	case len(s.SecretRefs) > MaxSigningSecrets || slices.Contains(s.SecretRefs, ""):
		return fmt.Errorf("%w: a sink has at most %d secrets, none empty", ErrInvalid, MaxSigningSecrets)
	}

	switch s.Kind {
	case SinkEmail:
		return validAddress(s.Target, len(s.SecretRefs))
	case SinkChat, SinkWebhook:
		return s.validURL()
	default:
		return fmt.Errorf("%w: unknown sink kind %q", ErrInvalid, s.Kind)
	}
}

// validURL returns ErrInvalid for a chat or webhook sink whose target the
// egress rules refuse, for a chat sink with more than one token, and for a
// webhook sink with no signing secret.
func (s Sink) validURL() error {
	if err := egress.CheckURL(s.Target, egress.HTTPSOnly); err != nil {
		return fmt.Errorf("%w: a %s sink: %w", ErrInvalid, s.Kind, err)
	}

	if s.Kind == SinkChat && len(s.SecretRefs) > 1 {
		return fmt.Errorf("%w: a chat sink has at most one token", ErrInvalid)
	}

	if s.Kind == SinkWebhook && len(s.SecretRefs) == 0 {
		return fmt.Errorf("%w: a webhook sink needs a signing secret", ErrInvalid)
	}

	return nil
}

// validAddress returns ErrInvalid for a target of an email sink that is
// not one bare address, or for an email sink with a secret.
func validAddress(target string, secrets int) error {
	a, err := mail.ParseAddress(target)
	if err != nil || a.Name != "" || a.Address != target || len(target) > MaxAddressLen {
		return fmt.Errorf("%w: an email sink needs one address of at most %d bytes", ErrInvalid, MaxAddressLen)
	}

	if secrets != 0 {
		return fmt.Errorf("%w: an email sink has no secret", ErrInvalid)
	}

	return nil
}

// Filter selects the notifications of a subscription. A type entry is a
// type, or a prefix that ends in ".*", for example "job.*". An object
// entry is a unit reference, or a prefix of references that ends in "/*",
// for example "opentofu:shop/*". An empty list selects every type or
// object. MinSeverity drops the notifications below it.
type Filter struct {
	Types       []string
	Objects     []string
	MinSeverity Severity
}

// Validate returns ErrInvalid for a filter with too many entries, or with
// a type entry that names no type of the catalog.
func (f Filter) Validate() error {
	if len(f.Types) > MaxFilterEntries || len(f.Objects) > MaxFilterEntries {
		return fmt.Errorf("%w: a filter has at most %d types and %d objects", ErrInvalid, MaxFilterEntries,
			MaxFilterEntries)
	}

	for _, t := range f.Types {
		if !matchesAnyType(t) {
			return fmt.Errorf("%w: filter entry %q names no notification type", ErrInvalid, t)
		}
	}

	return nil
}

// Match reports whether f selects n.
func (f Filter) Match(n Notification) bool {
	if n.Severity < f.MinSeverity {
		return false
	}

	return matchList(f.Types, string(n.Type), ".*") && matchList(f.Objects, n.Object, "/*")
}

func matchList(list []string, v, wildcard string) bool {
	if len(list) == 0 {
		return true
	}

	for _, p := range list {
		if prefix, ok := strings.CutSuffix(p, wildcard); ok {
			if strings.HasPrefix(v, prefix+wildcard[:1]) {
				return true
			}

			continue
		}

		if p == v {
			return true
		}
	}

	return false
}

func matchesAnyType(entry string) bool {
	for _, t := range Types() {
		if matchList([]string{entry}, string(t), ".*") {
			return true
		}
	}

	return false
}

// SubscriptionState is the state of a subscription.
type SubscriptionState string

// States of a subscription.
const (
	// SubscriptionStateActive delivers the notifications that match.
	SubscriptionStateActive SubscriptionState = "active"
	// SubscriptionStateSuspended delivers nothing. The dispatcher
	// suspends a subscription when its sink failed for SuspendAfter, or
	// when its creator lost the action to manage notifications or to read
	// the scope. A person resumes it.
	SubscriptionStateSuspended SubscriptionState = "suspended"
)

// Subscription sends the notifications of its scope that match Filter to
// Sink. A subscription of the server scope receives the notifications of
// the server scope only. CreatedBy is the principal whose actions the
// dispatcher checks before each delivery.
type Subscription struct {
	ID        string
	Scope     namespace.Name
	Sink      string
	Filter    Filter
	State     SubscriptionState
	Reason    string
	CreatedBy string
	Version   int64
}

// Reader decides whether a principal can read the object of a
// notification. The identity package implements it.
type Reader interface {
	// CanRead reports whether the principal id can read the object of n:
	// for a unit, the read action of the unit, through the grants on the
	// unit and on its collections; for a notification with no object, the
	// read action on the scope (namespace:read, or server:observe in the
	// server scope). It returns false and no error for an unknown or
	// disabled principal. It returns an error only when it cannot decide.
	CanRead(ctx context.Context, id string, n Notification) (bool, error)
}

// Route returns the active subscriptions of subs that receive n: those of
// the scope of n whose filter matches, and whose creator can read the
// object of n. A subscription whose creator cannot read the object skips
// n: a grant on a unit or a collection that hides a unit also hides its
// notifications. The dispatcher calls Route when it routes n, and calls
// Reader again before each attempt of a delivery.
func Route(ctx context.Context, n Notification, subs []Subscription, r Reader) ([]Subscription, error) {
	var out []Subscription

	for i := range subs {
		s := subs[i]
		if s.State != SubscriptionStateActive || s.Scope != n.Scope || !s.Filter.Match(n) {
			continue
		}

		ok, err := r.CanRead(ctx, s.CreatedBy, n)
		if err != nil {
			return nil, fmt.Errorf("route %s to %s: %w", n.ID, s.ID, err)
		}

		if ok {
			out = append(out, s)
		}
	}

	return out, nil
}

// Readers returns the principals of ids that can read the object of n, in
// the order of ids. A notice goes only to them.
func Readers(ctx context.Context, n Notification, ids []string, r Reader) ([]string, error) {
	var out []string

	for _, id := range ids {
		ok, err := r.CanRead(ctx, id, n)
		if err != nil {
			return nil, fmt.Errorf("notice %s for %s: %w", n.ID, id, err)
		}

		if ok {
			out = append(out, id)
		}
	}

	return out, nil
}

// Retry policy of a delivery.
const (
	// FirstRetry is the wait before the second attempt.
	FirstRetry = 30 * time.Second
	// MaxRetryWait caps the wait between two attempts that the server
	// chooses.
	MaxRetryWait = time.Hour
	// MaxAttempts caps the attempts of a delivery. After the last one,
	// the delivery is abandoned, and a person can deliver it again.
	MaxAttempts = 15
	// SuspendAfter is how long the deliveries of a subscription can fail
	// with no success before the dispatcher suspends it. It also caps the
	// wait that a receiver asks for.
	SuspendAfter = 24 * time.Hour
)

// RetryAfterError is an ErrTemporary with the wait that the receiver asked
// for, for example in a Retry-After header. A Sender returns it, and
// RetryAt honors it.
type RetryAfterError struct {
	Wait time.Duration
}

// Error returns the text of the error.
func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("%s: the receiver asks to wait %s", ErrTemporary, e.Wait)
}

// Unwrap returns ErrTemporary.
func (e *RetryAfterError) Unwrap() error { return ErrTemporary }

// RetryAt returns the time of the attempt after attempt n, which ended
// at the time at with the error err, and false when no attempt remains: after
// MaxAttempts, or after an error that is not ErrTemporary. The wait
// doubles from FirstRetry up to MaxRetryWait. When err is a
// RetryAfterError, the wait is at least the wait it asks for, up to
// SuspendAfter.
func RetryAt(n int, at time.Time, err error) (time.Time, bool) {
	if n >= MaxAttempts || !errors.Is(err, ErrTemporary) {
		return time.Time{}, false
	}

	wait := FirstRetry
	for i := 1; i < n && wait < MaxRetryWait; i++ {
		wait *= 2
	}

	wait = min(wait, MaxRetryWait)

	if ra, ok := errors.AsType[*RetryAfterError](err); ok {
		wait = max(wait, min(ra.Wait, SuspendAfter))
	}

	return at.Add(wait), true
}

// Classes of the error of an attempt, as a delivery stores it.
const (
	ClassRefused   = "refused"
	ClassPermanent = "permanent"
	ClassTemporary = "temporary"
)

// Class returns the class of the error of an attempt, the only error text
// that a delivery stores: ClassRefused for egress.ErrRefused, ClassPermanent
// for ErrPermanent, and ClassTemporary for every other error. The class
// never holds an address, the body of an answer or a secret.
func Class(err error) string {
	switch {
	case errors.Is(err, egress.ErrRefused):
		return ClassRefused
	case errors.Is(err, ErrPermanent):
		return ClassPermanent
	default:
		return ClassTemporary
	}
}

// Quiet returns the time to send a personal notice of severity s: now for
// a critical notice, and otherwise the next business moment of the
// calendar of the person. next is the NextBusinessMoment method of that
// calendar.
func Quiet(s Severity, now time.Time, next func(time.Time) (time.Time, error)) (time.Time, error) {
	if s >= SeverityCritical {
		return now, nil
	}

	at, err := next(now)
	if err != nil {
		return time.Time{}, fmt.Errorf("quiet hours: %w", err)
	}

	return at, nil
}
