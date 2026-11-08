//go:build e2e

package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// sinkStoreOptions returns the option that gives the in-process server the
// notification sink store of st.
func sinkStoreOptions(st serverStore) []dustpan.Option {
	switch s := st.(type) {
	case *sqlite.Store:
		return []dustpan.Option{dustpan.WithSinkStore(s.NotificationSinks())}
	case *postgres.Store:
		return []dustpan.Option{dustpan.WithSinkStore(s.NotificationSinks())}
	default:
		return nil
	}
}

// errDeliverySeam reports a delivery that the harness cannot store.
var errDeliverySeam = errors.New("a seeded delivery needs the in-process server")

// DeliverySeed describes a delivery to store. Principal is the creator of the
// subscription.
type DeliverySeed struct {
	Subscription string
	Sink         string
	Principal    string
	State        notify.DeliveryState
}

// seedBatch is the most deliveries that one claim of SeedDelivery takes.
const seedBatch = 100

// SeedDelivery stores one delivery of a subscription of the scenario
// namespace, as the dispatcher does, and stops it in the state of seed. The
// server has no dispatcher yet, so no request makes a delivery: a scenario that
// reads the history of a subscription writes the delivery through the
// notification store of the in-process server. It returns the ID of the
// delivery. The binary server holds its database open, so the step fails there.
func (e *Env) SeedDelivery(ctx context.Context, tb testing.TB, seed DeliverySeed) string {
	tb.Helper()

	if e.binPath != "" || e.inprocess == nil {
		tb.Fatalf("%v", errDeliverySeam)
	}

	switch st := e.inprocess.(type) {
	case *sqlite.Store:
		return e.storeDelivery(ctx, tb, st.Notifications(), seed)
	case *postgres.Store:
		return e.storeDelivery(ctx, tb, st.Notifications(), seed)
	default:
		tb.Fatalf("%v: the store %T keeps no deliveries", errDeliverySeam, e.inprocess)

		return ""
	}
}

// storeDelivery stores the delivery of seed in store.
func (e *Env) storeDelivery(
	ctx context.Context,
	tb testing.TB,
	store notificationDeliveries,
	seed DeliverySeed,
) string {
	tb.Helper()

	ns := namespace.Name(Namespace)

	n, err := e.inprocess.GetNamespace(ctx, ns)
	if err != nil {
		tb.Fatalf("namespace %s: %v", ns, err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	d := notify.Delivery{
		ID: ident.NewID(notify.DeliveryIDPrefix), Notification: ident.NewID(notify.IDPrefix), Scope: ns,
		Subscription: seed.Subscription, Sink: seed.Sink, Contact: "", Principal: seed.Principal,
		State: notify.DeliveryPending, Attempts: 0, Redeliveries: 0, NextAt: now.Add(-time.Minute), LastError: "",
		DeliveredAt: time.Time{}, Owner: "", LeaseUntil: time.Time{}, Version: 0,
	}

	if err = store.Enqueue(ctx, n.Fence(), []notify.Delivery{d}); err != nil {
		tb.Fatalf("enqueue the delivery: %v", err)
	}

	if seed.State != notify.DeliveryPending {
		stopDelivery(ctx, tb, store, deliveryStop{fence: n.Fence(), id: d.ID, state: seed.State})
	}

	return d.ID
}

// deliveryStop names the delivery that stopDelivery ends and the state that it ends in.
type deliveryStop struct {
	fence namespace.Fence
	id    string
	state notify.DeliveryState
}

// stopDelivery claims the pending delivery id and records an attempt that ends
// in state.
func stopDelivery(ctx context.Context, tb testing.TB, store notificationDeliveries, stop deliveryStop) {
	tb.Helper()

	fence, id, state := stop.fence, stop.id, stop.state

	now := time.Now().UTC().Truncate(time.Millisecond)

	claimed, err := store.Claim(ctx, fence, notify.ClaimRequest{
		Scope: namespace.Name(Namespace), Owner: "e2e", Now: now, Lease: time.Minute, Limit: seedBatch,
	})
	if err != nil {
		tb.Fatalf("claim the delivery: %v", err)
	}

	for i := range claimed {
		if claimed[i].ID != id {
			continue
		}

		err = store.Record(ctx, fence, notify.Attempt{
			ID: id, Owner: "e2e", Version: claimed[i].Version, Now: now, State: state, Attempts: 1, NextAt: now,
			LastError: "", DeliveredAt: now,
		})
		if err != nil {
			tb.Fatalf("record the attempt: %v", err)
		}

		return
	}

	tb.Fatalf("the claim did not return the delivery %s", id)
}

// notificationDeliveries is the part of notify.Deliveries that SeedDelivery
// uses.
type notificationDeliveries interface {
	Enqueue(ctx context.Context, fence namespace.Fence, ds []notify.Delivery) error
	Claim(ctx context.Context, fence namespace.Fence, req notify.ClaimRequest) ([]notify.Delivery, error)
	Record(ctx context.Context, fence namespace.Fence, a notify.Attempt) error
}
