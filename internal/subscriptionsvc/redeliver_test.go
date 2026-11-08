package subscriptionsvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// redeliverDeliveries returns err from Redeliver.
type redeliverDeliveries struct {
	notify.Deliveries

	err error
}

func (f redeliverDeliveries) Redeliver(
	context.Context, string, time.Time, namespace.Fence, audit.Entry,
) error {
	return f.err
}

func TestRedeliverErrors(t *testing.T) {
	t.Parallel()

	wait := 1500 * time.Millisecond
	cases := []struct {
		name     string
		err      error
		want     error
		tooSoon  bool
		wantWait time.Duration
	}{
		{"too soon", &notify.TooSoonError{Wait: wait}, subscriptionsvc.ErrRedeliverTooSoon, true, wait},
		{"pending", notify.ErrConflict, subscriptionsvc.ErrNotRedeliverable, false, 0},
		{"unknown", notify.ErrNotFound, notify.ErrNotFound, false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := redeliverWith(t, tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("Redeliver = %v, want %v", got, tc.want)
			}

			if tc.tooSoon {
				checkTooSoonMapping(t, got, tc.wantWait)
			}
		})
	}
}

// redeliverWith returns the error of a redeliver over a store that returns in.
func redeliverWith(t *testing.T, in error) error {
	t.Helper()

	svc, err := subscriptionsvc.NewService(subscriptionsvc.Config{
		Subscriptions: fakeSubs{nil}, Deliveries: redeliverDeliveries{Deliveries: nil, err: in},
		Namespaces: fakeNamespaces{}, Now: at,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	var principal identity.Principal

	principal.Subject = "alice"

	var by audit.Entry

	caller := &subscriptionsvc.Caller{Principal: principal, By: by}

	return svc.Redeliver(t.Context(), caller, "acme", "dlv-1")
}

// checkTooSoonMapping checks that err keeps the wait and the service sentinel.
func checkTooSoonMapping(t *testing.T, err error, wait time.Duration) {
	t.Helper()

	if !errors.Is(err, notify.ErrTooSoon) || errors.Is(err, subscriptionsvc.ErrNotRedeliverable) {
		t.Fatalf("Redeliver = %v, want ErrTooSoon and not ErrNotRedeliverable", err)
	}

	tooSoon, ok := errors.AsType[*notify.TooSoonError](err)
	if !ok || tooSoon.Wait != wait {
		t.Fatalf("Redeliver = %v, want a TooSoonError with a wait of %s", err, wait)
	}
}
