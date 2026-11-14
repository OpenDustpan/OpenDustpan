package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// TestTransactionTimeoutMsFloor checks the floor of the value of
// transaction_timeout: 0 turns the timeout off, so a deadline that is now or
// in the past gives 1.
func TestTransactionTimeoutMsFloor(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000)

	for _, tc := range []struct {
		name     string
		deadline time.Time
		want     string
	}{
		{"the deadline is now", now, "1"},
		{"the deadline is less than 1 ms away", now.Add(500 * time.Microsecond), "1"},
		{"the deadline passed", now.Add(-time.Hour), "1"},
		{"the deadline is 1.5 s away", now.Add(1500 * time.Millisecond), "1500"},
	} {
		if got := transactionTimeoutMs(tc.deadline, now); got != tc.want {
			t.Errorf("%s: transactionTimeoutMs = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAttemptErrorMapsTheTimeout sends errors straight to attemptError: the
// SQLSTATE of transaction_timeout, and any error after the deadline of a
// transaction that wrote a record, become ErrItemKeyAttemptExpired with no
// text of the driver. Other errors pass.
func TestAttemptErrorMapsTheTimeout(t *testing.T) {
	t.Parallel()

	timeoutErr, otherErr := new(pgconn.PgError), new(pgconn.PgError)
	timeoutErr.Code, timeoutErr.Message = transactionTimeoutCode, "driver text"
	otherErr.Code, otherErr.Message = "23505", "driver text"
	timeout := fmt.Errorf("transaction: %w", timeoutErr)
	other := fmt.Errorf("transaction: %w", otherErr)
	plain := errPlain
	now := time.UnixMilli(1_700_000_000_000)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	atNow := func() time.Time { return now }

	for _, tc := range []struct {
		name    string
		attempt *itemKeyAttempt
		err     error
		expired bool
	}{
		{"the timeout of the database", &itemKeyAttempt{deadline: future, wrote: false, now: atNow}, timeout, true},
		{"another SQLSTATE before the deadline", &itemKeyAttempt{deadline: future, wrote: true, now: atNow}, other, false},
		{"an error after the deadline of a write", &itemKeyAttempt{deadline: past, wrote: true, now: atNow}, plain, true},
		{"an error after the deadline with no write", &itemKeyAttempt{deadline: past, wrote: false, now: atNow}, plain, false},
		{"no error", &itemKeyAttempt{deadline: past, wrote: true, now: atNow}, nil, false},
	} {
		got := tc.attempt.attemptError(tc.err)
		if errors.Is(got, secret.ErrItemKeyAttemptExpired) != tc.expired {
			t.Errorf("%s: attemptError = %v, want expired %v", tc.name, got, tc.expired)
		}

		if tc.expired && errors.As(got, new(*pgconn.PgError)) {
			t.Errorf("%s: attemptError keeps the error of the driver: %v", tc.name, got)
		}
	}
}

// errPlain is an error that is not an error of the deadline.
var errPlain = errors.New("plain")

// TestBeforeCommitRefusesALateWrite checks the check before the commit alone.
func TestBeforeCommitRefusesALateWrite(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	atNow := func() time.Time { return now }

	var none *itemKeyAttempt

	if err := (&itemKeyAttempt{deadline: past, wrote: true, now: atNow}).beforeCommit(); !errors.Is(
		err, secret.ErrItemKeyAttemptExpired,
	) {
		t.Errorf("a late write: %v, want ErrItemKeyAttemptExpired", err)
	}

	for name, a := range map[string]*itemKeyAttempt{
		"a write in time": {deadline: future, wrote: true, now: atNow},
		"no write":        {deadline: past, wrote: false, now: atNow},
		"none":            none,
	} {
		if err := a.beforeCommit(); err != nil {
			t.Errorf("%s: %v, want nil", name, err)
		}
	}
}

// TestTheDatabaseEndsALateCommit sends a transaction that wrote a record in
// time and commits after the deadline, with no check before the commit, as if
// that check had failed. transaction_timeout alone must end it, and the error
// maps to ErrItemKeyAttemptExpired.
func TestTheDatabaseEndsALateCommit(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	ref := seedItemKey(t, s)
	late := lateItemKey(t, ref)
	fence := namespace.Fence{Namespace: ref.Scope.Namespace, FailoverVersion: 1}
	now := time.UnixMilli(1_700_000_000_000)
	SetNowForTest(s, func() time.Time { return now })
	s.attemptDeadline = func(_ context.Context, start time.Time) time.Time {
		return start.Add(300 * time.Millisecond)
	}
	attempt := s.newAttempt(t.Context())

	err := s.inFencedTx(t.Context(), fence, func(q *db.Queries) error {
		if _, err := (itemKeyTx{q: q, fence: fence, attempt: attempt}).CreateItemKey(t.Context(), late); err != nil {
			return err
		}

		waitPast(900 * time.Millisecond)

		return nil
	})

	if got := attempt.attemptError(err); !errors.Is(got, secret.ErrItemKeyAttemptExpired) {
		t.Fatalf("a late commit = %v, want ErrItemKeyAttemptExpired", got)
	}

	if _, getErr := s.GetItemKey(t.Context(), late.Ref); !errors.Is(getErr, secret.ErrKeyNotFound) {
		t.Errorf("a late commit left a record: err = %v", getErr)
	}
}

// TestCreateItemKeyNeedsAnAttempt sends a write through an itemKeyTx with no
// attempt, as a read transaction makes it. It writes no record.
func TestCreateItemKeyNeedsAnAttempt(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	ref := seedItemKey(t, s)
	late := lateItemKey(t, ref)
	fence := namespace.Fence{Namespace: ref.Scope.Namespace, FailoverVersion: 1}

	err := s.inFencedTx(context.WithoutCancel(t.Context()), fence, func(q *db.Queries) error {
		_, err := (itemKeyTx{q: q, fence: fence, attempt: nil}).CreateItemKey(t.Context(), late)

		return err
	})
	if !errors.Is(err, secret.ErrItemKeyAttemptExpired) {
		t.Fatalf("a write with no attempt = %v, want ErrItemKeyAttemptExpired", err)
	}
}
