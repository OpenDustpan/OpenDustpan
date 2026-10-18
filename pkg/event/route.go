package event

import (
	"fmt"
	"strings"
	"time"

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
	// SinkWebhook posts the event as JSON to the https URL Target, signed
	// with the secret of the sink (see Sign).
	SinkWebhook SinkKind = "webhook"
)

// Sink is a destination of events in a scope. Target is an email address
// or an https URL. SecretRef names the stored secret of the sink: the key
// of the signature of a webhook, or the token of a chat webhook. The API
// never answers the secret, and the dispatcher reads it only to deliver.
type Sink struct {
	ID        string
	Scope     namespace.Name
	Kind      SinkKind
	Name      string
	Target    string
	SecretRef string
	CreatedBy string
	Version   int64
}

// Validate returns ErrInvalid for a sink that breaks a bound, for a
// webhook with no secret, and for a target that does not fit the kind.
func (s Sink) Validate() error {
	switch {
	case !strings.HasPrefix(s.ID, SinkIDPrefix):
		return fmt.Errorf("%w: a sink ID starts with %q", ErrInvalid, SinkIDPrefix)
	case s.Scope == "" || s.Name == "" || len(s.Name) > MaxNameLen:
		return fmt.Errorf("%w: a sink needs a scope and a name of 1 to %d bytes", ErrInvalid, MaxNameLen)
	}

	switch s.Kind {
	case SinkEmail:
		if !strings.Contains(s.Target, "@") {
			return fmt.Errorf("%w: an email sink needs an address", ErrInvalid)
		}
	case SinkChat, SinkWebhook:
		if !strings.HasPrefix(s.Target, "https://") {
			return fmt.Errorf("%w: a %s sink needs an https URL", ErrInvalid, s.Kind)
		}

		if s.Kind == SinkWebhook && s.SecretRef == "" {
			return fmt.Errorf("%w: a webhook sink needs a signing secret", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown sink kind %q", ErrInvalid, s.Kind)
	}

	return nil
}

// Filter selects the events of a subscription. A type entry is a type, or
// a prefix that ends in ".*", for example "job.*". An object entry is a
// unit reference, or a prefix of references that ends in "/*", for
// example "opentofu:shop/*". An empty list selects every type or object.
// MinSeverity drops the events below it.
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
			return fmt.Errorf("%w: filter entry %q names no event type", ErrInvalid, t)
		}
	}

	return nil
}

// Match reports whether f selects e.
func (f Filter) Match(e Event) bool {
	if e.Severity < f.MinSeverity {
		return false
	}

	return matchList(f.Types, string(e.Type), ".*") && matchList(f.Objects, e.Object, "/*")
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
	// SubscriptionActive delivers the events that match.
	SubscriptionActive SubscriptionState = "active"
	// SubscriptionSuspended delivers nothing. The dispatcher suspends a
	// subscription when its sink failed for SuspendAfter, or when its
	// creator lost the action to manage notifications. A person resumes
	// it.
	SubscriptionSuspended SubscriptionState = "suspended"
)

// Subscription sends the events of its scope that match Filter to Sink.
// A subscription of the server scope receives the events of the server
// scope only. CreatedBy is the principal whose actions the dispatcher
// checks before each delivery.
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

// Route returns the active subscriptions of subs that receive e: those of
// the scope of e whose filter matches.
func Route(e Event, subs []Subscription) []Subscription {
	var out []Subscription

	for i := range subs {
		if s := subs[i]; s.State == SubscriptionActive && s.Scope == e.Scope && s.Filter.Match(e) {
			out = append(out, s)
		}
	}

	return out
}

// Retry policy of a delivery.
const (
	// FirstRetry is the wait before the second attempt.
	FirstRetry = 30 * time.Second
	// MaxRetryWait caps the wait between two attempts.
	MaxRetryWait = time.Hour
	// MaxAttempts caps the attempts of a delivery. After the last one,
	// the delivery is abandoned, and a person can deliver it again.
	MaxAttempts = 15
	// SuspendAfter is how long the deliveries of a subscription can fail
	// with no success before the dispatcher suspends it.
	SuspendAfter = 24 * time.Hour
)

// RetryAt returns the time of the attempt after attempt n, which ended at
// at, and false when no attempt remains. The wait doubles from FirstRetry
// up to MaxRetryWait.
func RetryAt(n int, at time.Time) (time.Time, bool) {
	if n >= MaxAttempts {
		return time.Time{}, false
	}

	wait := FirstRetry
	for i := 1; i < n && wait < MaxRetryWait; i++ {
		wait *= 2
	}

	return at.Add(min(wait, MaxRetryWait)), true
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
