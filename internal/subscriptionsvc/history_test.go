package subscriptionsvc_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// fakeDeliveries is the history of one subscription in memory. History
// orders the rows by next time, newest first, and then by ID, greatest first,
// and starts after the cursor, as the port says.
type fakeDeliveries struct {
	notify.Deliveries

	rows []notify.Delivery

	// countErr, when set, is the error of each CountBefore.
	countErr error
}

func (f fakeDeliveries) History(
	_ context.Context, _ namespace.Name, _ string, after *notify.DeliveryCursor, limit int,
) ([]notify.Delivery, error) {
	out := make([]notify.Delivery, 0, len(f.rows))
	for i := range f.rows {
		if after != nil && !afterCursor(f.rows[i], *after) {
			continue
		}

		out = append(out, f.rows[i])
	}

	slices.SortFunc(out, func(a, b notify.Delivery) int {
		return cmp.Or(b.NextAt.Compare(a.NextAt), strings.Compare(b.ID, a.ID))
	})

	return out[:min(limit, len(out))], nil
}

// afterCursor reports whether d comes after the cursor c in the history
// order: an older next time, or the same next time and a smaller ID.
func afterCursor(d notify.Delivery, c notify.DeliveryCursor) bool {
	if order := d.NextAt.Compare(c.NextAt); order != 0 {
		return order < 0
	}

	return d.ID < c.ID
}

// CountBefore counts the rows of the fake at or before the cursor, capped at
// maxCount, as the port says.
func (f fakeDeliveries) CountBefore(
	_ context.Context, _ namespace.Name, _ string, after *notify.DeliveryCursor, maxCount int,
) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}

	if after == nil || maxCount <= 0 {
		return 0, nil
	}

	count := 0
	for i := range f.rows {
		if !afterCursor(f.rows[i], *after) {
			count++
		}
	}

	return min(count, maxCount), nil
}

// at is the fixed clock of the tests.
func at() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

// historyRows returns count deliveries of sub-1 with the next time next, with
// the IDs dlv-<first> to dlv-<first+count-1>, zero-padded so that their byte
// order is their number order.
func historyRows(next time.Time, first, count int) []notify.Delivery {
	out := make([]notify.Delivery, 0, count)
	for i := range count {
		out = append(out, notify.Delivery{
			ID: fmt.Sprintf("dlv-%04d", first+i), Notification: "ntf-1", Scope: "acme", Subscription: "sub-1",
			Sink: "snk-1", Contact: "", Principal: "alice", State: notify.DeliveryDone, Attempts: 0,
			NextAt: next, LastError: "", DeliveredAt: time.Time{}, Owner: "", LeaseUntil: time.Time{}, Version: 0,
		})
	}

	return out
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

// newService returns the service over rows, with the fixed clock injected.
func newService(t *testing.T, rows []notify.Delivery) *subscriptionsvc.Service {
	t.Helper()

	svc, err := subscriptionsvc.NewService(subscriptionsvc.Config{
		Subscriptions: fakeSubs{nil}, Deliveries: fakeDeliveries{Deliveries: nil, rows: rows, countErr: nil},
		Namespaces: fakeNamespaces{}, Now: at,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc
}

// TestHistorySameNextAtPages reads the history of many deliveries that share
// one next time, with a page edge between them. Every delivery comes back
// once, in the order of the history.
func TestHistorySameNextAtPages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rows []notify.Delivery
		size int
		want []string
	}{
		{
			"five rows of one next time across page edges",
			historyRows(at(), 1, 5),
			2,
			[]string{"dlv-0005", "dlv-0004", "dlv-0003", "dlv-0002", "dlv-0001"},
		},
		{
			"a page edge inside the rows of one next time",
			slices.Concat(historyRows(at(), 4, 3), historyRows(at().Add(-time.Minute), 1, 2)),
			2,
			[]string{"dlv-0006", "dlv-0005", "dlv-0004", "dlv-0002", "dlv-0001"},
		},
		{
			"a page edge between two next times",
			slices.Concat(historyRows(at(), 2, 1), historyRows(at().Add(-time.Minute), 1, 1)),
			1,
			[]string{"dlv-0002", "dlv-0001"},
		},
		{
			"one row",
			historyRows(at(), 1, 1),
			5,
			[]string{"dlv-0001"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := newService(t, tc.rows)

			seen := pageIDs(t, svc, len(tc.rows), tc.size)
			if !slices.Equal(seen, tc.want) {
				t.Fatalf("history = %v, want %v", seen, tc.want)
			}
		})
	}
}

// pageIDs reads the history of sub-1 in pages of size, follows the cursor of
// each page, and returns the IDs in order. rows is the number of deliveries
// in the history.
func pageIDs(t *testing.T, svc *subscriptionsvc.Service, rows, size int) []string {
	t.Helper()

	var (
		seen  []string
		after *notify.DeliveryCursor
	)

	for pages := 0; pages <= rows+1; pages++ {
		page, err := svc.History(t.Context(), "acme", "sub-1", subscriptionsvc.HistoryRequest{
			After: after, Size: size,
		})
		if err != nil {
			t.Fatalf("history: %v", err)
		}

		for i := range page.Deliveries {
			seen = append(seen, page.Deliveries[i].ID)
		}

		if !page.More {
			return seen
		}

		last := page.Deliveries[len(page.Deliveries)-1]
		after = &notify.DeliveryCursor{NextAt: last.NextAt, ID: last.ID}
	}

	t.Fatalf("the history does not end: %v", seen)

	return nil
}

func TestHistoryWindows(t *testing.T) {
	t.Parallel()

	next := at()
	cursor := func(id string) *notify.DeliveryCursor {
		return &notify.DeliveryCursor{NextAt: next, ID: id}
	}

	tests := []struct {
		name     string
		total    int
		after    *notify.DeliveryCursor
		size     int
		wantLen  int
		wantMore bool
	}{
		{"first page with more", 5, nil, 2, 2, true},
		{"last page", 5, cursor("dlv-0003"), 2, 2, false},
		{"cursor after the last row", 3, cursor("dlv-0000"), 2, 0, false},
		{"size zero", 3, nil, 0, 0, false},
		{"page cut at the window", 5000, cursor("dlv-4006"), 10, 5, false},
		{"cursor at the window", 5000, cursor("dlv-4001"), 10, 0, false},
		{
			"full window with more rows behind it",
			5000,
			nil,
			subscriptionsvc.MaxHistory,
			subscriptionsvc.MaxHistory,
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := newService(t, historyRows(next, 1, tc.total))

			page, err := svc.History(t.Context(), "acme", "sub-1", subscriptionsvc.HistoryRequest{
				After: tc.after, Size: tc.size,
			})
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

// TestHistoryBoundFromTheStore reads the history at a cursor whose position
// is past the window and checks that the service counts the deliveries at or
// before the cursor in the store, never in a count of the caller.
func TestHistoryBoundFromTheStore(t *testing.T) {
	t.Parallel()

	const total = 1500

	next := at()
	cursor := func(id string) *notify.DeliveryCursor {
		return &notify.DeliveryCursor{NextAt: next, ID: id}
	}

	tests := []struct {
		name     string
		after    *notify.DeliveryCursor
		wantLen  int
		wantMore bool
	}{
		{"a cursor at the 1001st delivery", cursor("dlv-0500"), 0, false},
		{"a cursor at the 990th delivery", cursor("dlv-0511"), 10, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := newService(t, historyRows(next, 1, total))

			page, err := svc.History(t.Context(), "acme", "sub-1", subscriptionsvc.HistoryRequest{
				After: tc.after, Size: 50,
			})
			if err != nil {
				t.Fatalf("history: %v", err)
			}

			if len(page.Deliveries) != tc.wantLen || page.More != tc.wantMore {
				t.Fatalf("page = %d deliveries, more = %t; want %d, %t",
					len(page.Deliveries), page.More, tc.wantLen, tc.wantMore)
			}
		})
	}
}

// errCount is the error of a store that cannot count the deliveries.
var errCount = errors.New("the deliveries cannot be counted")

// TestHistoryReturnsTheCountError reads a page after a cursor from a store
// whose count fails. The service returns the error and no page.
func TestHistoryReturnsTheCountError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		after *notify.DeliveryCursor
	}{
		{"a cursor inside the history", &notify.DeliveryCursor{NextAt: at(), ID: "dlv-0003"}},
		{"a cursor after the last row", &notify.DeliveryCursor{NextAt: at(), ID: "dlv-0000"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, err := subscriptionsvc.NewService(subscriptionsvc.Config{
				Subscriptions: fakeSubs{nil},
				Deliveries:    fakeDeliveries{Deliveries: nil, rows: historyRows(at(), 1, 5), countErr: errCount},
				Namespaces:    fakeNamespaces{}, Now: at,
			})
			if err != nil {
				t.Fatalf("new service: %v", err)
			}

			page, err := svc.History(t.Context(), "acme", "sub-1", subscriptionsvc.HistoryRequest{
				After: tc.after, Size: 2,
			})
			if !errors.Is(err, errCount) {
				t.Fatalf("history = %v, want the count error", err)
			}

			if len(page.Deliveries) != 0 || page.More {
				t.Fatalf("page = %d deliveries, more = %t; want no page", len(page.Deliveries), page.More)
			}
		})
	}
}

func TestHistoryOfUnknownSubscription(t *testing.T) {
	t.Parallel()

	svc := newService(t, historyRows(at(), 1, 1))

	_, err := svc.History(t.Context(), "acme", "sub-2",
		subscriptionsvc.HistoryRequest{After: nil, Size: 10})
	if !errors.Is(err, notify.ErrNotFound) {
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
