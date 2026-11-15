package subscriptionsvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// errAuditDown is the error of an audit store that does not answer.
var errAuditDown = errors.New("audit store is down")

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

// redeliverAudit keeps the entries that Append gets, or returns its error.
type redeliverAudit struct {
	audit.Store

	entries []audit.Entry
	err     error
}

func (a *redeliverAudit) Append(
	_ context.Context, _ namespace.Fence, e audit.Entry,
) (audit.Entry, error) {
	if a.err != nil {
		return audit.Entry{}, a.err
	}

	a.entries = append(a.entries, e)

	return e, nil
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

			auditor, got := redeliverWith(t, tc.err, nil)
			if !errors.Is(got, tc.want) {
				t.Fatalf("Redeliver = %v, want %v", got, tc.want)
			}

			if tc.tooSoon {
				checkTooSoonMapping(t, got, tc.wantWait)
			}

			if len(auditor.entries) != 0 {
				t.Fatalf("audit entries = %d, want none", len(auditor.entries))
			}
		})
	}
}

func TestRedeliverLimitRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		auditErr    error
		wantEntries int
	}{
		{"no redeliveries left", nil, 1},
		{"no redeliveries left with an audit store that fails", errAuditDown, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			auditor, got := redeliverWith(t, notify.ErrRedeliverLimit, tc.auditErr)
			checkLimitRefusal(t, got, auditor, tc.auditErr, tc.wantEntries)
		})
	}
}

// checkLimitRefusal checks the refusal of a redeliver at the limit.
func checkLimitRefusal(
	t *testing.T, got error, auditor *redeliverAudit, auditErr error, wantEntries int,
) {
	t.Helper()

	if !errors.Is(got, subscriptionsvc.ErrRedeliverLimit) {
		t.Fatalf("Redeliver = %v, want ErrRedeliverLimit", got)
	}

	if !errors.Is(got, notify.ErrRedeliverLimit) {
		t.Fatalf("Redeliver = %v, want notify.ErrRedeliverLimit in the chain", got)
	}

	if auditErr != nil && !errors.Is(got, auditErr) {
		t.Fatalf("Redeliver = %v, want the audit error in the chain", got)
	}

	if len(auditor.entries) != wantEntries {
		t.Fatalf("audit entries = %d, want %d", len(auditor.entries), wantEntries)
	}

	if wantEntries == 1 {
		checkDeniedRedeliverEntry(t, auditor.entries[0])
	}
}

// redeliverWith returns the audit store of a redeliver over a store that
// returns in, and the error of the redeliver. The audit store returns
// auditErr.
func redeliverWith(t *testing.T, in, auditErr error) (*redeliverAudit, error) {
	t.Helper()

	auditor := &redeliverAudit{Store: nil, entries: nil, err: auditErr}

	svc, err := subscriptionsvc.NewService(subscriptionsvc.Config{
		Subscriptions: fakeSubs{nil}, Deliveries: redeliverDeliveries{Deliveries: nil, err: in},
		Namespaces: fakeNamespaces{}, Audit: auditor, Now: at,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	var principal identity.Principal

	principal.Subject = "alice"

	var by audit.Entry

	caller := &subscriptionsvc.Caller{Principal: principal, By: by}

	return auditor, svc.Redeliver(t.Context(), caller, "acme", "dlv-1")
}

// checkDeniedRedeliverEntry checks the entry of a redeliver at the limit.
func checkDeniedRedeliverEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	switch {
	case e.Namespace != "acme":
		t.Fatalf("audit namespace = %q, want acme", e.Namespace)
	case e.Subject != "alice":
		t.Fatalf("audit subject = %q, want alice", e.Subject)
	case e.Action != "delivery:redeliver":
		t.Fatalf("audit action = %q, want delivery:redeliver", e.Action)
	case e.Object != "acme", e.ObjectID != "dlv-1":
		t.Fatalf("audit object = %q/%q, want acme/dlv-1", e.Object, e.ObjectID)
	case e.Outcome != audit.OutcomeDenied:
		t.Fatalf("audit outcome = %q, want denied", e.Outcome)
	case e.Kind != audit.KindChange:
		t.Fatalf("audit kind = %q, want change", e.Kind)
	case e.Reason != errcode.SinkDeliveryRedeliverLimit:
		t.Fatalf("audit reason = %q, want %q", e.Reason, errcode.SinkDeliveryRedeliverLimit)
	default:
	}
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
