package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// TestAttemptErrorMapsTheDeadline sends errors straight to attemptError: the
// deadline of the attempt, and any error after the deadline of a transaction
// that wrote a record, become ErrItemKeyAttemptExpired with no text of the
// driver. Other errors, and a cancel of the caller, pass.
func TestAttemptErrorMapsTheDeadline(t *testing.T) {
	t.Parallel()

	plain := errPlain
	now := time.UnixMilli(1_700_000_000_000)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	atNow := func() time.Time { return now }

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	for _, tc := range []struct {
		name     string
		canceled bool
		attempt  *itemKeyAttempt
		err      error
		expired  bool
	}{
		{
			"the deadline of the attempt", false, &itemKeyAttempt{deadline: past, wrote: false, now: atNow},
			context.DeadlineExceeded, true,
		},
		{"an error after the deadline of a write", false, &itemKeyAttempt{deadline: past, wrote: true, now: atNow}, plain, true},
		{"an error before the deadline", false, &itemKeyAttempt{deadline: future, wrote: true, now: atNow}, plain, false},
		{"a cancel of the caller", true, &itemKeyAttempt{deadline: past, wrote: true, now: atNow}, context.Canceled, false},
		{"no error", false, &itemKeyAttempt{deadline: past, wrote: true, now: atNow}, nil, false},
	} {
		parent := t.Context()
		if tc.canceled {
			parent = canceled
		}

		got := tc.attempt.attemptError(parent, tc.err)
		if errors.Is(got, secret.ErrItemKeyAttemptExpired) != tc.expired {
			t.Errorf("%s: attemptError = %v, want expired %v", tc.name, got, tc.expired)
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

// TestTheContextEndsALateCommit sends a transaction that wrote a record in
// time and commits after the deadline, with no check before the commit, as if
// that check had failed. The deadline of the context of the attempt alone
// must end it, and the error maps to ErrItemKeyAttemptExpired.
// Real time: it proves that a real context deadline ends a late commit.
func TestTheContextEndsALateCommit(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	s.attemptDeadline = func(_ context.Context, start time.Time) time.Time {
		return start.Add(300 * time.Millisecond)
	}

	ref := seedItemKey(t, s)
	late := lateItemKey(t, ref)
	fence := namespace.Fence{Namespace: ref.Scope.Namespace, FailoverVersion: 1}

	attempt, txCtx, cancel := s.newAttempt(t.Context())
	defer cancel()

	err := s.inFencedTx(txCtx, fence, func(q *db.Queries) error {
		if _, err := (itemKeyTx{q: q, fence: fence, attempt: attempt}).CreateItemKey(txCtx, late); err != nil {
			return err
		}

		waitPast(900 * time.Millisecond)

		return nil
	})

	if got := attempt.attemptError(t.Context(), err); !errors.Is(got, secret.ErrItemKeyAttemptExpired) {
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

	s := openInternalTemplated(t)
	ref := seedItemKey(t, s)
	late := lateItemKey(t, ref)
	fence := namespace.Fence{Namespace: ref.Scope.Namespace, FailoverVersion: 1}

	err := s.inFencedTx(t.Context(), fence, func(q *db.Queries) error {
		_, err := (itemKeyTx{q: q, fence: fence, attempt: nil}).CreateItemKey(t.Context(), late)

		return err
	})
	if !errors.Is(err, secret.ErrItemKeyAttemptExpired) {
		t.Fatalf("a write with no attempt = %v, want ErrItemKeyAttemptExpired", err)
	}
}
