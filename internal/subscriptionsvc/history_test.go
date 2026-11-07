package subscriptionsvc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// subscriptionPort names notify.Subscriptions for an embedded field: the port
// has a method with the name of its type.
type subscriptionPort = notify.Subscriptions

// fakeSubs holds one subscription in the namespace acme.
type fakeSubs struct{ subscriptionPort }

func (fakeSubs) Subscriptions(context.Context, namespace.Name) ([]notify.Subscription, error) {
	return []notify.Subscription{{
		ID: "sub-1", Scope: "acme", Sink: "snk-1", Filter: notify.Filter{Types: nil, Objects: nil, MinSeverity: 0},
		State: notify.SubscriptionStateActive, Reason: "", CreatedBy: "alice", Version: 1,
	}}, nil
}

// fakeDeliveries answers History with up to limit deliveries of a store that
// holds total of them.
type fakeDeliveries struct {
	notify.Deliveries

	total int
}

func (f fakeDeliveries) History(_ context.Context, _ namespace.Name, _ string, limit int) ([]notify.Delivery, error) {
	out := make([]notify.Delivery, 0, min(limit, f.total))
	for i := range min(limit, f.total) {
		out = append(out, notify.Delivery{
			ID: fmt.Sprintf("dlv-%d", i), Notification: "", Scope: "acme", Subscription: "sub-1", Sink: "", Contact: "",
			Principal: "", State: notify.DeliveryDone, Attempts: 0, NextAt: time.Time{}, LastError: "",
			DeliveredAt: time.Time{}, Owner: "", LeaseUntil: time.Time{}, Version: 0,
		})
	}

	return out, nil
}

func emptyNamespace() namespace.Namespace {
	return namespace.Namespace{
		CreatedAt:           time.Time{},
		ID:                  "",
		Name:                "acme",
		PrimaryInstallation: "",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     0,
	}
}

type fakeNamespaces struct{}

func (fakeNamespaces) Get(context.Context, namespace.Name) (namespace.Namespace, error) {
	return emptyNamespace(), nil
}

func (fakeNamespaces) WritableHere(context.Context, namespace.Name) (namespace.Namespace, error) {
	return emptyNamespace(), nil
}

func TestHistoryWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		total        int
		offset, size int
		wantLen      int
		wantMore     bool
	}{
		{"first page with more", 5, 0, 2, 2, true},
		{"last page", 5, 4, 2, 1, false},
		{"page that ends at the last row", 4, 2, 2, 2, false},
		{"offset past the end", 3, 10, 2, 0, false},
		{"negative offset", 3, -1, 2, 0, false},
		{"size zero", 3, 0, 0, 0, false},
		{"page cut at the window", 5000, 998, 10, 2, false},
		{"offset at the window", 5000, subscriptionsvc.MaxHistory, 10, 0, false},
		{
			"full window with more rows behind it",
			5000,
			0,
			subscriptionsvc.MaxHistory,
			subscriptionsvc.MaxHistory,
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, err := subscriptionsvc.NewService(subscriptionsvc.Config{
				Subscriptions: fakeSubs{nil}, Deliveries: fakeDeliveries{Deliveries: nil, total: tc.total},
				Namespaces: fakeNamespaces{}, Now: nil,
			})
			if err != nil {
				t.Fatalf("new service: %v", err)
			}

			page, err := svc.History(t.Context(), "acme", "sub-1", tc.offset, tc.size)
			if err != nil {
				t.Fatalf("history: %v", err)
			}

			if len(page.Deliveries) != tc.wantLen || page.More != tc.wantMore {
				t.Errorf("page = %d deliveries, more = %t; want %d, %t",
					len(page.Deliveries), page.More, tc.wantLen, tc.wantMore)
			}
		})
	}
}

func TestHistoryOfUnknownSubscription(t *testing.T) {
	t.Parallel()

	svc, err := subscriptionsvc.NewService(subscriptionsvc.Config{
		Subscriptions: fakeSubs{nil}, Deliveries: fakeDeliveries{Deliveries: nil, total: 1},
		Namespaces: fakeNamespaces{}, Now: nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	if _, err = svc.History(t.Context(), "acme", "sub-2", 0, 10); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("history = %v, want notify.ErrNotFound", err)
	}
}

func TestNewServiceNeedsItsPorts(t *testing.T) {
	t.Parallel()

	_, err := subscriptionsvc.NewService(subscriptionsvc.Config{
		Subscriptions: nil, Deliveries: nil, Namespaces: nil, Now: nil,
	})
	if !errors.Is(err, subscriptionsvc.ErrMissingDependency) {
		t.Fatalf("NewService = %v, want ErrMissingDependency", err)
	}
}
