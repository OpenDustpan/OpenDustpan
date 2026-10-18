package event

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Outbox is the persistence port for events. The store of each domain
// appends the events of a change in the transaction of the change, as it
// appends the audit entries, through its own write methods. Append is for
// an event that no domain write carries, for example a notice of a
// schedule.
type Outbox interface {
	// Append stores events in one transaction, each with the next
	// sequence of the installation. It checks fence when it names a
	// namespace, and returns namespace.ErrFenced for a stale one, and
	// ErrInvalid for an event that fails Validate.
	Append(ctx context.Context, fence namespace.Fence, events []Event) error
	// After returns at most limit events with a sequence above seq, in
	// order of sequence. A sequence never comes back after a later one
	// is visible: an adapter that cannot promise this for transactions
	// that commit out of order waits for them, as the audit shipper does.
	After(ctx context.Context, seq int64, limit int) ([]Event, error)
	// Cursor returns the last sequence that the dispatcher name handled,
	// or 0.
	Cursor(ctx context.Context, name string) (int64, error)
	// MoveCursor moves the cursor name from the sequence from to the
	// sequence to. It returns
	// ErrConflict when the cursor is not at from, so two instances never
	// both route one event.
	MoveCursor(ctx context.Context, name string, from, to int64) error
}

// Subscriptions is the persistence port for sinks and subscriptions. A
// sink and a subscription are writes in their scope: each write carries
// the fence of the namespace, or a zero fence in the server scope, and
// appends the audit entry e in its transaction.
type Subscriptions interface {
	// PutSink creates s, or replaces it when s.Version is the stored
	// version. It returns ErrInvalid, ErrConflict and namespace.ErrFenced.
	PutSink(ctx context.Context, s Sink, fence namespace.Fence, e audit.Entry) error
	// DeleteSink deletes the sink id. It returns ErrNotFound, ErrInUse
	// while a subscription names it, and namespace.ErrFenced.
	DeleteSink(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// Sinks returns the sinks of scope, ordered by ID.
	Sinks(ctx context.Context, scope namespace.Name) ([]Sink, error)
	// PutSubscription creates s, or replaces it when s.Version is the
	// stored version. It returns ErrNotFound for a sink that is not in the
	// scope of s, ErrInvalid, ErrConflict and namespace.ErrFenced.
	PutSubscription(ctx context.Context, s Subscription, fence namespace.Fence, e audit.Entry) error
	// DeleteSubscription deletes the subscription id and its pending
	// deliveries. It returns ErrNotFound and namespace.ErrFenced.
	DeleteSubscription(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// Subscriptions returns the subscriptions of scope, ordered by ID.
	Subscriptions(ctx context.Context, scope namespace.Name) ([]Subscription, error)
}

// DeliveryState is the state of a delivery.
type DeliveryState string

// States of a delivery.
const (
	DeliveryPending   DeliveryState = "pending"
	DeliveryDone      DeliveryState = "delivered"
	DeliveryAbandoned DeliveryState = "abandoned"
)

// Delivery is the delivery of one event to one sink, or to one personal
// contact of a recipient. Contact is empty for a delivery to the target of
// the sink. LastError holds the error text of the adapter, with no secret
// and no body of the answer.
type Delivery struct {
	ID           string
	Event        string
	Scope        namespace.Name
	Subscription string
	Sink         string
	Contact      string
	State        DeliveryState
	Attempts     int
	NextAt       time.Time
	LastError    string
	DeliveredAt  time.Time
	Version      int64
}

// Deliveries is the persistence port for deliveries and their history.
type Deliveries interface {
	// Enqueue stores ds as pending. A delivery for the same event,
	// subscription and contact as a stored one is skipped, so a dispatcher
	// that routes an event again makes no second delivery.
	Enqueue(ctx context.Context, ds []Delivery) error
	// Due returns at most limit pending deliveries whose NextAt is at or
	// before now, oldest first.
	Due(ctx context.Context, now time.Time, limit int) ([]Delivery, error)
	// Record stores the result of an attempt of d when d.Version is the
	// stored version. It returns ErrNotFound and ErrConflict.
	Record(ctx context.Context, d Delivery) error
	// History returns the deliveries of the subscription id, newest first,
	// at most limit.
	History(ctx context.Context, id string, limit int) ([]Delivery, error)
	// Redeliver makes the delivery id pending again with no attempts, and
	// appends e. It returns ErrNotFound, and namespace.ErrFenced.
	Redeliver(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
}

// Message is what a Sender delivers: the event, the sink, its secret and
// the address. Secret is nil for a sink with no secret. To is the target
// of the sink, or the personal contact of a recipient.
type Message struct {
	Event  Event
	Sink   Sink
	Secret []byte
	To     string
	// Actions are the chat actions that a chat sink offers with the
	// event, each as a signed URL of the server.
	Actions []ActionLink
}

// ActionLink is a button of a chat message.
type ActionLink struct {
	Title string
	URL   string
}

// Sender delivers messages of one sink kind. The email adapter talks SMTP
// to the relay of the installation. The chat and webhook adapters post
// over https through the egress rules of the installation, refuse a
// redirect to another host, and never follow one with the body.
type Sender interface {
	// Kind returns the sink kind that the adapter serves.
	Kind() SinkKind
	// Send delivers m once. It returns ErrPermanent for an answer that
	// will not change, for example an unknown address or a 4xx other than
	// 408 and 429, and ErrTemporary for every other failure.
	Send(ctx context.Context, m Message) error
}

// Contact is a personal address of a person for notices.
type Contact struct {
	Kind    SinkKind
	Address string
}

// Contacts finds the personal contacts of a person: the verified email
// address, and the chat identity that the person linked. The identity
// package implements it.
type Contacts interface {
	// ContactsOf returns the contacts of the principal id, or none for a
	// principal that is disabled or has none.
	ContactsOf(ctx context.Context, id string) ([]Contact, error)
	// Members returns the principal IDs of the group name.
	Members(ctx context.Context, name string) ([]string, error)
}
