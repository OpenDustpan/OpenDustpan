// Package subscriptionsvc implements the use cases of notification
// subscriptions and of their deliveries for the HTTP adapter: create,
// replace, delete, read, read the history and send again. It adds the
// fence, the time, the IDs and the audit entry to the calls of
// notify.Subscriptions and notify.Deliveries.
package subscriptionsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// ErrSinkNotInNamespace reports a subscription that names a sink which the
// namespace does not hold. The text never says whether another namespace
// holds a sink of that ID.
var ErrSinkNotInNamespace = errors.New("the namespace holds no notification destination with this ID")

// ErrNotRedeliverable reports a delivery that cannot be sent again now: it is
// pending, or its subscription no longer sends it.
var ErrNotRedeliverable = errors.New("the delivery cannot be sent again")

// MaxHistory is the most deliveries that one read of the history reaches. A
// page that would end after this count has no next page.
const MaxHistory = 1000

// ReasonPerson is the reason of a subscription that a person suspended.
const ReasonPerson = "a person suspended the subscription"

// Action names of the audit entries.
const (
	actionCreate    = "subscription:create"
	actionUpdate    = "subscription:update"
	actionDelete    = "subscription:delete"
	actionRedeliver = "delivery:redeliver"
)

// Namespaces reads the namespace that a write fences. namespace.Service
// implements it.
type Namespaces interface {
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Config holds the dependencies of a Service. All fields except Now are
// required.
type Config struct {
	Subscriptions notify.Subscriptions
	Deliveries    notify.Deliveries
	Namespaces    Namespaces
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Service implements the use cases of subscriptions and deliveries.
type Service struct {
	subs       notify.Subscriptions
	deliveries notify.Deliveries
	namespaces Namespaces
	now        func() time.Time
}

// NewService returns a Service. It returns ErrMissingDependency when a port is
// missing.
func NewService(cfg Config) (*Service, error) {
	if cfg.Subscriptions == nil || cfg.Deliveries == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: subscriptions, deliveries and namespaces", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{subs: cfg.Subscriptions, deliveries: cfg.Deliveries, namespaces: cfg.Namespaces, now: now}, nil
}

// Caller is the principal who calls a use case. By is the template of the
// audit entry: the caller fills where the request came from, and the service
// fills the action, the object and the time.
type Caller struct {
	Principal identity.Principal
	By        audit.Entry
}

// New describes a subscription to make. It starts active.
type New struct {
	Sink   string
	Filter notify.Filter
}

// Change describes a replace. Version is the version that the caller read. A
// suspended subscription becomes active when State is active: this is the
// resume of a person.
type Change struct {
	Version int64
	Sink    string
	Filter  notify.Filter
	State   notify.SubscriptionState
}

// Create makes a subscription in ns. It returns notify.ErrInvalid,
// ErrSinkNotInNamespace, namespace.ErrNotFound or namespace.ErrFenced.
func (s *Service) Create(ctx context.Context, c *Caller, ns namespace.Name, in New) (notify.Subscription, error) {
	sub := notify.Subscription{
		ID: ident.NewID(notify.SubscriptionIDPrefix), Scope: ns, Sink: in.Sink, Filter: in.Filter,
		State: notify.SubscriptionStateActive, Reason: "", CreatedBy: c.Principal.Subject, Version: 0,
	}

	// Defense in depth: the store validates the subscription again, and the
	// handler checks the body. This check stays so that a refused write costs no
	// read of the namespace and no read of the sinks.
	if err := sub.Validate(); err != nil {
		return notify.Subscription{}, fmt.Errorf("create subscription: %w", err)
	}

	if err := s.checkSink(ctx, ns, in.Sink); err != nil {
		return notify.Subscription{}, err
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return notify.Subscription{}, err
	}

	if err = s.subs.PutSubscription(ctx, sub, fence, s.entry(c, ns, actionCreate, sub.ID)); err != nil {
		// A new subscription has no stored row, so a store that answers
		// notify.ErrNotFound says that the sink left the namespace after the check.
		if errors.Is(err, notify.ErrNotFound) {
			return notify.Subscription{}, fmt.Errorf("create subscription: %w", ErrSinkNotInNamespace)
		}

		return notify.Subscription{}, fmt.Errorf("create subscription: %w", err)
	}

	sub.Version = 1

	return sub, nil
}

// Replace changes the subscription id in ns. It returns notify.ErrNotFound,
// notify.ErrInvalid, notify.ErrConflict, ErrSinkNotInNamespace,
// namespace.ErrNotFound or namespace.ErrFenced.
func (s *Service) Replace(
	ctx context.Context, c *Caller, ns namespace.Name, id string, ch Change,
) (notify.Subscription, error) {
	old, err := s.Get(ctx, ns, id)
	if err != nil {
		return notify.Subscription{}, err
	}

	next := old
	next.Version, next.Sink, next.Filter, next.State = ch.Version, ch.Sink, ch.Filter, ch.State
	next.CreatedBy = c.Principal.Subject

	switch ch.State {
	case notify.SubscriptionStateActive:
		next.Reason = ""
	case notify.SubscriptionStateSuspended:
		next.Reason = old.Reason
		if old.State == notify.SubscriptionStateActive {
			next.Reason = ReasonPerson
		}
	default:
		return notify.Subscription{}, fmt.Errorf(
			"replace subscription: %w: unknown state %q",
			notify.ErrInvalid,
			ch.State,
		)
	}

	// Defense in depth: see Create.
	if err = next.Validate(); err != nil {
		return notify.Subscription{}, fmt.Errorf("replace subscription: %w", err)
	}

	if sinkErr := s.checkSink(ctx, ns, next.Sink); sinkErr != nil {
		return notify.Subscription{}, sinkErr
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return notify.Subscription{}, err
	}

	if err = s.subs.PutSubscription(ctx, next, fence, s.entry(c, ns, actionUpdate, id)); err != nil {
		return notify.Subscription{}, fmt.Errorf("replace subscription: %w", err)
	}

	next.Version++

	return next, nil
}

// Delete removes the subscription id in ns with its pending deliveries. It
// returns notify.ErrNotFound.
func (s *Service) Delete(ctx context.Context, c *Caller, ns namespace.Name, id string) error {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return err
	}

	if err = s.subs.DeleteSubscription(ctx, id, fence, s.entry(c, ns, actionDelete, id)); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}

	return nil
}

// List returns the subscriptions of ns, ordered by ID.
func (s *Service) List(ctx context.Context, ns namespace.Name) ([]notify.Subscription, error) {
	if _, err := s.namespaces.Get(ctx, ns); err != nil {
		return nil, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	subs, err := s.subs.Subscriptions(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}

	return subs, nil
}

// Get returns the subscription id of ns, or notify.ErrNotFound.
func (s *Service) Get(ctx context.Context, ns namespace.Name, id string) (notify.Subscription, error) {
	subs, err := s.List(ctx, ns)
	if err != nil {
		return notify.Subscription{}, err
	}

	for i := range subs {
		if subs[i].ID == id {
			return subs[i], nil
		}
	}

	return notify.Subscription{}, fmt.Errorf("get subscription %s: %w", id, notify.ErrNotFound)
}

// Page is one page of the history of a subscription. More says that a later
// page exists.
type Page struct {
	Deliveries []notify.Delivery
	More       bool
}

// HistoryRequest names one page of the history of a subscription. After is
// the last delivery of the page before, or nil for the first page. Size is
// the largest number of deliveries on the page.
type HistoryRequest struct {
	After *notify.DeliveryCursor
	Size  int
}

// History returns at most req.Size deliveries of the subscription id in ns,
// newest first, after req.After. A nil cursor starts at the newest delivery.
// It returns notify.ErrNotFound for a subscription that ns does not hold. A
// page that would reach past MaxHistory deliveries is cut at that count and
// has no next page. The position of the cursor comes from the store, never
// from the caller, so a cursor cannot reach past MaxHistory.
func (s *Service) History(
	ctx context.Context, ns namespace.Name, id string, req HistoryRequest,
) (Page, error) {
	if _, err := s.Get(ctx, ns, id); err != nil {
		return Page{}, err
	}

	if req.Size <= 0 {
		return Page{Deliveries: nil, More: false}, nil
	}

	read := 0

	if req.After != nil {
		count, err := s.deliveries.CountBefore(ctx, ns, id, req.After, MaxHistory)
		if err != nil {
			return Page{}, fmt.Errorf("history of %s: %w", id, err)
		}

		if count >= MaxHistory {
			return Page{Deliveries: nil, More: false}, nil
		}

		read = count
	}

	size := min(req.Size, MaxHistory-read)

	// One more row than the page says whether a next page exists.
	all, err := s.deliveries.History(ctx, ns, id, req.After, size+1)
	if err != nil {
		return Page{}, fmt.Errorf("history of %s: %w", id, err)
	}

	more := len(all) > size && read+size < MaxHistory

	return Page{Deliveries: all[:min(size, len(all))], More: more}, nil
}

// Redeliver makes the stopped delivery id pending again. It returns
// notify.ErrNotFound, ErrNotRedeliverable, namespace.ErrNotFound or
// namespace.ErrFenced.
func (s *Service) Redeliver(ctx context.Context, c *Caller, ns namespace.Name, id string) error {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return err
	}

	err = s.deliveries.Redeliver(ctx, id, fence, s.entry(c, ns, actionRedeliver, id))
	switch {
	case errors.Is(err, notify.ErrConflict):
		return fmt.Errorf("redeliver %s: %w", id, errors.Join(ErrNotRedeliverable, err))
	case err != nil:
		return fmt.Errorf("redeliver %s: %w", id, err)
	default:
		return nil
	}
}

// checkSink returns ErrSinkNotInNamespace when ns holds no sink with the ID.
//
// Defense in depth: the store checks the scope of the sink in the transaction
// of the write. This check stays so that the refusal has its own error, and so
// that a store that skips its check cannot take a sink of another namespace
// from this service.
func (s *Service) checkSink(ctx context.Context, ns namespace.Name, sink string) error {
	all, err := s.subs.Sinks(ctx, ns)
	if err != nil {
		return fmt.Errorf("list sinks: %w", err)
	}

	for i := range all {
		if all[i].ID == sink {
			return nil
		}
	}

	return fmt.Errorf("subscription sink %s: %w", sink, ErrSinkNotInNamespace)
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}

// entry makes the audit entry of one write. It names the object by ID and
// holds no filter text.
func (s *Service) entry(c *Caller, ns namespace.Name, action, id string) audit.Entry {
	e := c.By
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Subject = c.Principal.Subject
	e.TokenID = c.Principal.TokenID
	e.Action = action
	e.Object = identity.NamespaceObject(ns).String()
	e.ObjectID = id
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.PrincipalKind = auditKind(c.Principal)

	return e
}

// auditKind returns the audit principal kind of p, or no kind for a kind that
// the audit log does not know.
func auditKind(p identity.Principal) string {
	switch p.Kind {
	case identity.KindPerson:
		return audit.PrincipalKindPerson
	case identity.KindService:
		return audit.PrincipalKindService
	case identity.KindWorkload:
		return audit.PrincipalKindWorkload
	default:
		return ""
	}
}
