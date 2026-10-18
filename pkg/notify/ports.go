package notify

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The fence of a write. Every write of the ports below carries a fence
// that belongs to the scope of its row: the fence of the namespace, at
// the current failover version, for a row in a namespace, and the zero
// fence for a row in audit.ServerScope. The store checks the fence in the
// transaction of the write. It returns namespace.ErrFenced for a stale
// fence, and namespace.ErrNotFound for a fence of an unknown namespace.
// For a fence that does not belong to the scope of the row (a zero fence
// for a row in a namespace, a fence for a row in the server scope, or the
// fence of another namespace), it returns ErrInvalid for a new row whose
// scope the caller gives, and ErrNotFound for a stored row that the
// caller names by its ID, so a caller learns nothing of the rows of
// another scope.

// Outbox is the persistence port for notifications. The store of each
// domain appends the notifications of a change in the transaction of the
// change, as it appends the audit entries, through its own write methods.
// Append is for a notification that no domain write carries, for example
// a notice of a schedule.
//
// Each scope has its own sequence, as the audit log of a scope has. A
// dispatcher keeps one cursor for each scope.
type Outbox interface {
	// Append stores notifications in one transaction, each with the next
	// sequence of its scope. All notifications of one call have the scope
	// of fence. A notification with the scope and dedupe key of a stored
	// one, or of an earlier one of the call, is skipped and gets no
	// sequence. It returns ErrInvalid for a notification that fails
	// Validate, for notifications of two scopes, and for a fence of
	// another scope; and namespace.ErrFenced and namespace.ErrNotFound.
	Append(ctx context.Context, fence namespace.Fence, ns []Notification) error
	// Scopes returns the scopes that hold a notification above the cursor
	// name of that scope, ordered by name.
	Scopes(ctx context.Context, name string) ([]namespace.Name, error)
	// After returns at most limit notifications of scope with a sequence
	// above seq, in order of sequence. A sequence never comes back after a
	// later one of its scope is visible: an adapter that cannot promise
	// this for transactions that commit out of order waits for them, as
	// the audit shipper does.
	After(ctx context.Context, scope namespace.Name, seq int64, limit int) ([]Notification, error)
	// Cursor returns the last sequence of scope that the dispatcher name
	// handled, or 0.
	Cursor(ctx context.Context, name string, scope namespace.Name) (int64, error)
	// MoveCursor moves the cursor m.Name of m.Scope from the sequence
	// m.From to the sequence m.To. It returns ErrConflict when the cursor
	// is not at m.From, so two instances never both route one
	// notification. It returns ErrInvalid for a fence of another scope,
	// and namespace.ErrFenced and namespace.ErrNotFound.
	MoveCursor(ctx context.Context, fence namespace.Fence, m CursorMove) error
}

// CursorMove names a move of the cursor Name of Scope from the sequence
// From to the sequence To.
type CursorMove struct {
	Name     string
	Scope    namespace.Name
	From, To int64
}

// Subscriptions is the persistence port for sinks and subscriptions. A
// sink and a subscription are writes in their scope: each write carries
// the fence of its scope and appends the audit entry e in its
// transaction.
type Subscriptions interface {
	// PutSink creates s, or replaces it when s.Version is the stored
	// version. It returns ErrInvalid for a sink that fails Validate or for
	// a fence that is not of s.Scope, ErrNotFound when the stored sink
	// s.ID is in another scope, ErrConflict, namespace.ErrFenced and
	// namespace.ErrNotFound.
	PutSink(ctx context.Context, s Sink, fence namespace.Fence, e audit.Entry) error
	// DeleteSink deletes the sink id. It returns ErrNotFound for an
	// unknown sink or a sink of another scope than fence, ErrInUse while a
	// subscription names it, namespace.ErrFenced and namespace.ErrNotFound.
	DeleteSink(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// Sinks returns the sinks of scope, ordered by ID.
	Sinks(ctx context.Context, scope namespace.Name) ([]Sink, error)
	// PutSubscription creates s, or replaces it when s.Version is the
	// stored version. It returns ErrInvalid for a filter that fails
	// Validate or for a fence that is not of s.Scope, ErrNotFound for a
	// sink that is not in the scope of s or when the stored subscription
	// s.ID is in another scope, ErrConflict, namespace.ErrFenced and
	// namespace.ErrNotFound.
	PutSubscription(ctx context.Context, s Subscription, fence namespace.Fence, e audit.Entry) error
	// DeleteSubscription deletes the subscription id and its pending
	// deliveries. It returns ErrNotFound for an unknown subscription or a
	// subscription of another scope than fence, namespace.ErrFenced and
	// namespace.ErrNotFound.
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

// Delivery is the delivery of one notification to one sink, or to one
// personal contact of a recipient. Contact is empty for a delivery to the
// target of the sink. LastError holds the Class of the error of the last
// attempt, and nothing else. Owner and LeaseUntil are the claim of the
// delivery: the store sets them in Claim and clears them in Record.
// Deliveries are not ordered: a receiver orders notifications by their
// scope and sequence.
type Delivery struct {
	ID           string
	Notification string
	Scope        namespace.Name
	Subscription string
	Sink         string
	Contact      string
	State        DeliveryState
	Attempts     int
	NextAt       time.Time
	LastError    string
	DeliveredAt  time.Time
	Owner        string
	LeaseUntil   time.Time
	Version      int64
}

// Deliveries is the persistence port for deliveries and their history.
type Deliveries interface {
	// Enqueue stores ds as pending, in one transaction. All deliveries of
	// one call have the scope of fence. A delivery for the same
	// notification, subscription and contact as a stored one is skipped,
	// so a dispatcher that routes a notification again makes no second
	// delivery. It returns ErrInvalid for deliveries of two scopes or a
	// fence of another scope, namespace.ErrFenced and
	// namespace.ErrNotFound.
	Enqueue(ctx context.Context, fence namespace.Fence, ds []Delivery) error
	// DueScopes returns the scopes that hold a delivery that Claim can
	// take at now, ordered by name.
	DueScopes(ctx context.Context, now time.Time) ([]namespace.Name, error)
	// Claim takes, in one transaction, at most req.Limit pending
	// deliveries of req.Scope whose NextAt is at or before req.Now and
	// that no live claim holds, oldest NextAt first. It sets Owner to
	// req.Owner, LeaseUntil to req.Now plus req.Lease, and a new Version
	// on each, and returns them. A claim whose LeaseUntil passed is not
	// live, so another instance can take the delivery when its holder
	// stopped. Two calls never return one delivery under two live claims.
	// It returns ErrInvalid for a request that fails Validate or a fence
	// of another scope, namespace.ErrFenced and namespace.ErrNotFound.
	Claim(ctx context.Context, fence namespace.Fence, req ClaimRequest) ([]Delivery, error)
	// Record stores the result of an attempt of d when d.Version is the
	// version of the claim that returned d, and clears the claim. A later
	// claim changes the version, so a holder that lost its claim gets
	// ErrConflict. It returns ErrNotFound for an unknown delivery or a
	// delivery of another scope than fence, ErrConflict,
	// namespace.ErrFenced and namespace.ErrNotFound.
	Record(ctx context.Context, fence namespace.Fence, d Delivery) error
	// History returns the deliveries of the subscription id, newest first,
	// at most limit.
	History(ctx context.Context, id string, limit int) ([]Delivery, error)
	// Redeliver makes the delivery id pending again with no attempts and
	// no claim, and appends e. It returns ErrNotFound for an unknown
	// delivery or a delivery of another scope than fence,
	// namespace.ErrFenced and namespace.ErrNotFound.
	Redeliver(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
}

// ClaimRequest asks Claim for the due deliveries of Scope at Now. Lease is
// the time from the claim to its expiry.
type ClaimRequest struct {
	Scope namespace.Name
	Owner string
	Now   time.Time
	Lease time.Duration
	Limit int
}

// Validate returns ErrInvalid for a request with no scope, no owner, or a
// lease or a limit that is not positive.
func (r ClaimRequest) Validate() error {
	if r.Scope == "" || r.Owner == "" || r.Lease <= 0 || r.Limit <= 0 {
		return fmt.Errorf("%w: a claim needs a scope, an owner, a positive lease and a positive limit", ErrInvalid)
	}

	return nil
}

// Message is what a Sender delivers: the notification, the sink, its
// active secrets and the address. Secrets is empty for a sink with no
// secret, and holds one or two signing keys for a webhook. To is the
// target of the sink, or the personal contact of a recipient.
type Message struct {
	Notification Notification
	Sink         Sink
	Secrets      [][]byte
	To           string
	// Actions are the chat actions that a chat sink offers with the
	// notification, each as a signed URL of the server.
	Actions []ActionLink
}

// ActionLink is a button of a chat message.
type ActionLink struct {
	Title string
	URL   string
}

// Sender delivers messages of one sink kind. The email adapter talks SMTP
// to the relay of the installation. The chat and webhook adapters post
// over https with the client of egresshttp.Client(egress.NoRedirect,
// egress.HTTPSOnly): its dial guard checks the resolved address of each
// connection with egress.CheckAddr, so a private, loopback or link-local
// address fails, also after a DNS answer that changed. The adapters follow
// no redirect, send no user information, and read at most
// egress.MaxBodyBytes of an answer with egresshttp.ReadBody. Sink.Validate
// runs egress.CheckURL when a sink is created or changed, and the test
// route of a sink runs it again before it sends. A delivery stores only
// the Class of the error of an attempt, never the address, the network
// error, the body of the answer or a secret.
type Sender interface {
	// Kind returns the sink kind that the adapter serves.
	Kind() SinkKind
	// Send delivers m once. It returns an error that matches
	// egress.ErrRefused for an address that the egress rules refuse;
	// ErrPermanent for an answer that will not change, for example an
	// unknown address or a 4xx other than 408 and 429; a RetryAfterError
	// for an answer with a Retry-After header; and ErrTemporary for every
	// other failure.
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
