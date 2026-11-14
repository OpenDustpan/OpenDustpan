package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// TestNameLockAppendsWaitForTheServerAuditScopeLock proves that each
// name-lock path that appends to the server audit scope takes the advisory
// lock of the scope first. The test holds the lock by hand: a path that does
// not take the lock runs while the hold is open, and its append can then
// read the same last entry as another append. Each case opens a database of
// its own, because the key of the advisory lock is fixed.
func TestNameLockAppendsWaitForTheServerAuditScopeLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		start func(t *testing.T, s *Store) <-chan error
		name  string
	}{
		{name: "AddNameFailure appends the entry of a new lock", start: startAddNameFailure},
		{name: "ClearName appends the entry of a clear", start: startClearName},
		{name: "PruneNameLocks appends the entry of a prune", start: startPruneNameLocks},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, _ := createLockSetup(t, "acme")
			hold := holdScopeLock(t, s)

			done := tc.start(t, s)

			assertCreateBlocked(t, done, tc.name)
			hold.release(t)
			assertCreateDone(t, done, tc.name)
		})
	}
}

// scopeLockHold is a transaction that holds the advisory lock of the server
// audit scope.
type scopeLockHold struct {
	tx pgx.Tx
}

// holdScopeLock takes the advisory lock of the server audit scope in its own
// transaction, which stays open until release.
func holdScopeLock(t *testing.T, s *Store) scopeLockHold {
	t.Helper()

	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	hold := scopeLockHold{tx: tx}

	t.Cleanup(func() {
		// A release has ended the transaction already, which is not an error.
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("roll back the hold: %v", rollbackErr)
		}
	})

	if err := s.queries.WithTx(tx).LockServerAuditScope(t.Context()); err != nil {
		t.Fatalf("take the server audit scope lock: %v", err)
	}

	return hold
}

// release ends the transaction and so frees the lock.
func (h scopeLockHold) release(t *testing.T) {
	t.Helper()

	if err := h.tx.Rollback(t.Context()); err != nil {
		t.Fatalf("release the server audit scope lock: %v", err)
	}
}

// startAddNameFailure starts an add that reaches the limit of its rule, so
// the add appends the entry and the alert of a new lock to the server scope.
func startAddNameFailure(t *testing.T, s *Store) <-chan error {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		_, err := s.AddNameFailure(t.Context(), scopeLockFailure())
		done <- err
	}()

	return done
}

// startClearName starts a clear of a name, which appends the entry of the
// clear to the server scope.
func startClearName(t *testing.T, s *Store) <-chan error {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		_, err := s.ClearName(
			t.Context(),
			[]string{authgate.ScopePassword},
			"n1",
			serverScopeEntry("local-user.name-unlocked"),
		)
		done <- err
	}()

	return done
}

// startPruneNameLocks starts a prune of the name counts, which appends the
// entry of the prune to the server scope.
func startPruneNameLocks(t *testing.T, s *Store) <-chan error {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		_, err := s.PruneNameLocks(t.Context(), time.Hour, 1, 1, serverScopeEntry("sign-in.name-locks-pruned"))
		done <- err
	}()

	return done
}

// scopeLockFailure returns a failure that reaches the limit of its rule on
// its first add, so the add appends the records of a new lock.
func scopeLockFailure() authgate.NameFailure {
	return authgate.NameFailure{
		Events: scopeLockEvents(),
		Scope:  authgate.ScopePassword,
		Name:   "n1",
		Sub:    "s",
		Rule:   authgate.LockRule{Limit: 1, LockFor: time.Minute, MaxLockFor: time.Minute, ResetAfter: 0},
	}
}

// scopeLockEvents returns valid records for one add: the entries of a lock
// and of the end of a lock, and the alert of a lock.
func scopeLockEvents() *authgate.LockEvents {
	return &authgate.LockEvents{
		Locked: serverScopeEntry(authgate.ActionNameLocked),
		Ended:  serverScopeEntry(authgate.ActionNameLockEnded),
		Alert: notify.Notification{
			ID:         ident.NewID(notify.IDPrefix),
			Type:       notify.SignInLocked,
			Scope:      audit.ServerScope,
			Object:     notify.NotificationsObject(audit.ServerScope),
			ObjectID:   "",
			Subject:    "",
			Agent:      "",
			Severity:   notify.SeverityWarning,
			Summary:    "",
			Links:      nil,
			Attributes: nil,
			Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
			DedupeKey:  "",
			At:         time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC),
			Sequence:   0,
		},
	}
}

// serverScopeEntry returns a valid audit entry of the server scope with the
// action.
func serverScopeEntry(action string) audit.Entry {
	return audit.Entry{
		At:            time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC),
		Namespace:     audit.ServerScope,
		Subject:       "server",
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        "name-locks",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}
