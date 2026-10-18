package notify

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
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
	// MaxRefLen caps a secret ref of a sink, in bytes.
	MaxRefLen = 100
)

// SinkKind names the kind of a sink.
type SinkKind string

// Kinds of a sink. An adapter of the Sender port serves each kind.
const (
	// SinkEmail sends an email to the address Target, or to the email
	// contacts of the recipients of a notice.
	SinkEmail SinkKind = "email"
	// SinkChat posts a message to the incoming webhook URL of a chat tool.
	// The URL holds the token of the webhook, so it is the one secret item
	// of the sink. Target holds only the host of the URL (see ChatHost), so
	// a read of the sink shows the host and never the URL.
	SinkChat SinkKind = "chat"
	// SinkWebhook posts the notification as JSON to the https URL Target,
	// signed with each active secret of the sink (see Sign).
	SinkWebhook SinkKind = "webhook"
)

// Sink is a destination of notifications in a scope. Target is an email
// address, the https URL of a webhook, or the host of the URL of a chat
// webhook. SecretRefs name the stored secrets of the sink: the one or two
// active signing keys of a webhook, or the URL of a chat webhook. The API
// never answers a secret, and the dispatcher reads them only to deliver.
// A ref names one value for good: the API stores each new secret value
// under a new ref. It never writes a new value under an existing ref
// (see Subscriptions.PutSink). The URL of a chat webhook is a secret
// item, and Target holds only its host, so a new value under the same
// ref could send to another webhook of the same host. With one value for
// each ref, a sink whose kind, target and refs did not change sends to
// the same place.
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
// webhook with no secret or more than MaxSigningSecrets secrets, for a
// chat sink that has not exactly one secret, and for a target that does
// not fit the kind. The URL of a webhook sink, and the host of a chat
// sink, must pass egress.CheckURL for https only: the error then also
// matches egress.ErrRefused.
func (s Sink) Validate() error {
	switch {
	case !strings.HasPrefix(s.ID, SinkIDPrefix):
		return fmt.Errorf("%w: a sink ID starts with %q", ErrInvalid, SinkIDPrefix)
	case s.Scope == "" || s.Name == "" || len(s.Name) > MaxNameLen:
		return fmt.Errorf("%w: a sink needs a scope and a name of 1 to %d bytes", ErrInvalid, MaxNameLen)
	case len(s.SecretRefs) > MaxSigningSecrets || slices.ContainsFunc(s.SecretRefs, badRef):
		return fmt.Errorf("%w: a sink has at most %d secrets, each an identifier of at most %d bytes", ErrInvalid,
			MaxSigningSecrets, MaxRefLen)
	}

	switch s.Kind {
	case SinkEmail:
		return validAddress(s.Target, len(s.SecretRefs))
	case SinkChat:
		return s.validChat()
	case SinkWebhook:
		return s.validURL()
	default:
		return fmt.Errorf("%w: unknown sink kind %q", ErrInvalid, s.Kind)
	}
}

// badRef reports whether ref is not a valid secret ref of a sink.
func badRef(ref string) bool {
	return !ident.Valid(ref, MaxRefLen)
}

// MaxSecretLen caps the value of a secret item of a sink, in bytes.
const MaxSecretLen = 4096

// SinkSecret is a new secret item of a sink in plaintext: its ref and its
// value. A value is a signing key of a webhook, or the URL of a chat
// webhook. Only the API holds it, to seal it with SinkSealer.Seal. No
// port carries it.
type SinkSecret struct {
	Ref   string
	Value []byte
}

// ValidateSecrets returns ErrInvalid for new secret items of s with an
// empty ref, a ref that s.SecretRefs does not hold, a ref that two items
// share, or a value that is empty or longer than MaxSecretLen.
func (s Sink) ValidateSecrets(secrets []SinkSecret) error {
	seen := make([]string, 0, len(secrets))

	for _, sec := range secrets {
		switch {
		case sec.Ref == "" || !slices.Contains(s.SecretRefs, sec.Ref) || slices.Contains(seen, sec.Ref):
			return fmt.Errorf("%w: each new secret of a sink has its own ref of the sink", ErrInvalid)
		case len(sec.Value) == 0 || len(sec.Value) > MaxSecretLen:
			return fmt.Errorf("%w: a secret of a sink has 1 to %d bytes", ErrInvalid, MaxSecretLen)
		}

		seen = append(seen, sec.Ref)
	}

	return nil
}

// validURL returns ErrInvalid for a webhook sink whose target the egress
// rules refuse, and for a webhook sink with no signing secret.
func (s Sink) validURL() error {
	if err := egress.CheckURL(s.Target, egress.HTTPSOnly); err != nil {
		return fmt.Errorf("%w: a %s sink: %w", ErrInvalid, s.Kind, err)
	}

	if len(s.SecretRefs) == 0 {
		return fmt.Errorf("%w: a webhook sink needs a signing secret", ErrInvalid)
	}

	return nil
}

// validChat returns ErrInvalid for a chat sink whose target is not a host
// that the egress rules accept, or that has not exactly one secret item,
// which holds the URL of the webhook.
func (s Sink) validChat() error {
	if len(s.SecretRefs) != 1 {
		return fmt.Errorf("%w: a chat sink has one secret item, which holds the URL of the webhook", ErrInvalid)
	}

	host, err := ChatHost("https://" + s.Target + "/")
	if err != nil {
		return fmt.Errorf("%w: a chat sink: %w", ErrInvalid, err)
	}

	if host != s.Target {
		return fmt.Errorf("%w: the target of a chat sink is a host, not a URL", ErrInvalid)
	}

	return nil
}

// ChatHost returns the host of the URL of a chat webhook, in lower case,
// with its port when the URL has one. It returns an error that matches
// egress.ErrRefused for a URL that egress.CheckURL refuses for https only.
// The API stores the URL as the secret item of the sink and the host as
// its Target. The chat adapter sends only when the host of the URL in the
// item is the Target.
func ChatHost(raw string) (string, error) {
	if err := egress.CheckURL(raw, egress.HTTPSOnly); err != nil {
		return "", fmt.Errorf("chat webhook: %w", err)
	}

	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return "", fmt.Errorf("chat webhook: %w: the URL does not parse", egress.ErrRefused)
	}

	return strings.ToLower(u.Host), nil
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

// ReasonSinkChanged is the reason of a subscription that the store
// suspended because a person changed the kind, the target or the secrets
// of its sink. The creator approved the old destination, not the new one,
// so the subscription delivers nothing until a person resumes it.
const ReasonSinkChanged = "the destination of the sink changed"

// SinkChangedNotice returns the notification subscription.suspended for
// the subscription sub, which a change of its sink suspends; e is the
// audit entry of the change. sub is the subscription before the suspend,
// and version is the version of the sink that the change stores. The notice goes to the administrators of the scope and
// to sub.CreatedBy, the person who approved the old destination. Its
// object is NotificationsObject of the scope, so only a principal that can
// manage the notifications of the scope reads it, and a subscription
// sends it only for such a creator. Its ID is new and random. Its dedupe
// key is "<sub.ID>/sink-changed/<version>", so each suspend has its own
// notice, and a store that repeats the append adds none. The store of
// Subscriptions.PutSink appends it in the transaction of the change.
func SinkChangedNotice(sub Subscription, version int64, e audit.Entry) Notification {
	return Notification{
		ID:       ident.NewID(IDPrefix),
		Type:     SubscriptionSuspended,
		Scope:    sub.Scope,
		Object:   NotificationsObject(sub.Scope),
		ObjectID: sub.ID,
		Subject:  e.Subject,
		Agent:    "",
		Severity: SeverityWarning,
		Summary: fmt.Sprintf("Subscription %s is suspended: the destination of sink %s changed. "+
			"Resume it to send to the new destination.", sub.ID, sub.Sink),
		Links: nil,
		Attributes: map[string]string{
			"subscription": sub.ID, "sink": sub.Sink, "reason": "sink-changed",
		},
		Recipients: []Recipient{
			{Principal: "", Group: "", Admins: true},
			{Principal: sub.CreatedBy, Group: "", Admins: false},
		},
		DedupeKey: sub.ID + "/sink-changed/" + strconv.FormatInt(version, 10),
		At:        e.At,
		Sequence:  0,
	}.Fit()
}

// NotificationsObject returns the object reference of the notification
// settings of scope, its sinks and subscriptions, for example
// "notifications:shop". A notification of this object tells about the
// sinks and the subscriptions of the scope. Only a principal that holds
// the action to manage the notifications of the scope reads it (see
// ScopeActions).
func NotificationsObject(scope namespace.Name) string {
	return "notifications:" + scope.String()
}

// Subscription sends the notifications of its scope that match Filter to
// Sink. A subscription of the server scope receives the notifications of
// the server scope only. CreatedBy is the principal whose actions the
// dispatcher checks before each delivery, and the Principal of each of
// its deliveries. The store sets it to the principal of the last create
// or change.
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

// Validate returns ErrInvalid for a subscription with no ID of the prefix
// SubscriptionIDPrefix, no scope, no sink, no creator, an unknown state,
// or a filter that fails Filter.Validate.
func (s Subscription) Validate() error {
	switch {
	case !strings.HasPrefix(s.ID, SubscriptionIDPrefix):
		return fmt.Errorf("%w: a subscription ID starts with %q", ErrInvalid, SubscriptionIDPrefix)
	case s.Scope == "" || s.Sink == "" || s.CreatedBy == "":
		return fmt.Errorf("%w: a subscription needs a scope, a sink and a creator", ErrInvalid)
	case s.State != SubscriptionStateActive && s.State != SubscriptionStateSuspended:
		return fmt.Errorf("%w: unknown subscription state %q", ErrInvalid, s.State)
	default:
		return s.Filter.Validate()
	}
}

// Actions that decide who reads a notification.
const (
	// ActionRead is the read action on a namespace.
	ActionRead = "namespace:read"
	// ActionReadCost is the action to read the costs and budgets of a
	// namespace.
	ActionReadCost = "namespace:read-cost"
	// ActionObserve is the read action on the server scope.
	ActionObserve = "server:observe"
	// ActionManage is the action to manage the notifications of a
	// namespace.
	ActionManage = "namespace:manage-notifications"
	// ActionManageServer is the action to manage the notifications of the
	// server scope.
	ActionManageServer = "server:manage-notifications"
)

// ScopeActions returns the actions on the scope of n that a principal
// needs to read n, in addition to the read action of its object: the read
// action of the scope; for a cost or budget notification also
// ActionReadCost; and for a notification of NotificationsObject of the
// scope also the action to manage the notifications of the scope. So a
// cost estimate or a budget threshold reaches no list, subscription or
// notice of a principal that cannot read costs, and a notice about the
// sinks and subscriptions of a scope reaches only a principal that can
// manage them.
func ScopeActions(n Notification) []string {
	settings := n.Object == NotificationsObject(n.Scope)

	switch {
	case n.Scope == audit.ServerScope && settings:
		return []string{ActionObserve, ActionManageServer}
	case n.Scope == audit.ServerScope:
		return []string{ActionObserve}
	case settings:
		return []string{ActionRead, ActionManage}
	case n.Type == CostEstimated || n.Type == BudgetThreshold:
		return []string{ActionRead, ActionReadCost}
	default:
		return []string{ActionRead}
	}
}

// Reader decides whether a principal can read a notification. The
// identity package implements it.
type Reader interface {
	// CanRead reports whether the principal id can read n: the principal
	// holds each action of ScopeActions(n) on the scope of n, and, for a
	// unit, the read action of the unit, through the grants on the unit
	// and on its collections. It returns false and no error for an
	// unknown or disabled principal. It returns an error only when it
	// cannot decide. The notification list of the API, Route, Readers and
	// BeforeAttempt all call it.
	CanRead(ctx context.Context, id string, n Notification) (bool, error)
}

// Access is the Reader and the Contacts that BeforeAttempt checks. The
// identity package implements both.
type Access interface {
	Reader
	Contacts
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

// BeforeAttempt runs before each attempt of the delivery d of n. sub is
// the stored subscription of d, or the zero Subscription for a delivery
// with no subscription or a subscription that is gone. It returns
// ErrNoAccess:
//   - for a delivery of a subscription when sub is not that subscription,
//     is not active, sends to another sink than d, or has another
//     CreatedBy than the principal of d, for example after a change of
//     the sink, an edit of another person, or a suspend,
//   - when the principal of d can no longer read n, for example after the
//     principal lost a grant or was disabled,
//   - when d goes to a contact that is no longer a contact of the
//     principal, for example after the principal removed the address.
//
// The dispatcher then records d as DeliverySkipped and does not send it.
// It returns the error of a port that cannot decide; the dispatcher then
// tries d again later, as for ErrTemporary.
func BeforeAttempt(ctx context.Context, d Delivery, sub Subscription, n Notification, a Access) error {
	if d.Subscription != "" && !SendsFor(sub, d) {
		return fmt.Errorf("before attempt of %s: the subscription changed: %w", d.ID, ErrNoAccess)
	}

	ok, err := a.CanRead(ctx, d.Principal, n)
	if err != nil {
		return fmt.Errorf("before attempt of %s: %w", d.ID, err)
	}

	if !ok {
		return fmt.Errorf("before attempt of %s: %w", d.ID, ErrNoAccess)
	}

	if d.Contact == "" {
		return nil
	}

	contacts, err := a.ContactsOf(ctx, d.Principal)
	if err != nil {
		return fmt.Errorf("before attempt of %s: %w", d.ID, err)
	}

	if !slices.ContainsFunc(contacts, func(k Contact) bool { return k.Address == d.Contact }) {
		return fmt.Errorf("before attempt of %s: the contact is gone: %w", d.ID, ErrNoAccess)
	}

	return nil
}

// SendsFor reports whether the subscription sub still sends the delivery
// d: sub is the subscription of d, is active, sends to the sink of d, and
// has the principal of d as its CreatedBy. BeforeAttempt and
// Deliveries.Redeliver apply it.
func SendsFor(sub Subscription, d Delivery) bool {
	return sub.ID != "" && sub.ID == d.Subscription && sub.Scope == d.Scope &&
		sub.State == SubscriptionStateActive && sub.Sink == d.Sink && sub.CreatedBy == d.Principal
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

// ParseRetryAfter returns the wait that the Retry-After header value h
// asks for at now: a count of seconds, or an HTTP date. It caps the wait
// at SuspendAfter, and returns 0 for a date in the past. It returns false
// for a value that does not parse and for a negative count, so the
// dispatcher uses its own wait.
func ParseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}

	if h[0] >= '0' && h[0] <= '9' || h[0] == '-' || h[0] == '+' {
		return retrySeconds(h)
	}

	// The preferred layout of an HTTP date, and the two obsolete ones that
	// a receiver must accept.
	for _, layout := range []string{"Mon, 02 Jan 2006 15:04:05 GMT", time.RFC850, time.ANSIC} {
		if at, err := time.Parse(layout, h); err == nil {
			return min(max(at.Sub(now), 0), SuspendAfter), true
		}
	}

	return 0, false
}

// retrySeconds returns the wait of a Retry-After count of seconds h, up to
// SuspendAfter, and false for a sign or a value that does not parse.
func retrySeconds(h string) (time.Duration, bool) {
	if h[0] == '-' || h[0] == '+' {
		return 0, false
	}

	n, err := strconv.ParseUint(h, 10, 63)
	if errors.Is(err, strconv.ErrRange) {
		return SuspendAfter, true
	}

	if err != nil {
		return 0, false
	}

	if n > uint64(SuspendAfter/time.Second) {
		return SuspendAfter, true
	}

	return time.Duration(n) * time.Second, true
}

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
