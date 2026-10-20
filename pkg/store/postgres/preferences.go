package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ preferences.Store = (*Store)(nil)

// preferencesTx is the SQL of an update of preferences inside a transaction.
// The rules are in preferences.Change. lock says whether Load locks the row
// of the person until the end of the transaction.
type preferencesTx struct {
	q    *db.Queries
	lock bool
}

var _ preferences.Tx = preferencesTx{q: nil, lock: false}

// Load implements preferences.Tx. With lock set it takes the row of the
// person FOR NO KEY UPDATE, so two updates of one person, and the end of the
// person, wait for each other and no update loses the change of another.
func (t preferencesTx) Load(ctx context.Context, personID string) (string, int64, error) {
	var (
		document string
		updated  int64
		err      error
	)

	if t.lock {
		var row db.LockPersonPreferencesRow

		row, err = t.q.LockPersonPreferences(ctx, personID)
		document, updated = row.Document.String, row.UpdatedAtMs.Int64
	} else {
		var row db.GetPersonPreferencesRow

		row, err = t.q.GetPersonPreferences(ctx, personID)
		document, updated = row.Document.String, row.UpdatedAtMs.Int64
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, preferences.ErrPersonNotFound
	}

	if err != nil {
		return "", 0, fmt.Errorf("select preferences: %w", err)
	}

	return document, updated, nil
}

// Save implements preferences.Tx.
func (t preferencesTx) Save(ctx context.Context, personID, document string, updatedAtMs int64) error {
	err := t.q.UpsertPersonPreferences(ctx, db.UpsertPersonPreferencesParams{
		PrincipalID: personID, Document: document, UpdatedAtMs: updatedAtMs,
	})
	if err != nil {
		return fmt.Errorf("upsert preferences: %w", err)
	}

	return nil
}

// Append implements preferences.Tx.
func (t preferencesTx) Append(ctx context.Context, e audit.Entry) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	if _, err := appendAuditEntry(ctx, t.q, e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// Get implements preferences.Store.
func (s *Store) Get(ctx context.Context, personID string) (preferences.Document, error) {
	document, updatedAtMs, err := preferencesTx{q: s.queries, lock: false}.Load(ctx, personID)
	if err != nil {
		return preferences.Default(), err
	}

	settings, updatedAt := preferences.Load(document, updatedAtMs, s.logger)

	return settings.DocumentAt(updatedAt), nil
}

// Update implements preferences.Store. It runs preferences.Change in one
// transaction with the audit entry. The transaction takes the lock of the
// server audit scope first, as every audited write does, and then the row of
// the person, so the lock order is the same everywhere.
func (s *Store) Update(
	ctx context.Context, personID string, patch preferences.Patch, e audit.Entry, ifMatch *int64,
) (preferences.Result, error) {
	// Defense in depth: the service and the route check the entry and the
	// patch too. The store checks them before it opens a transaction, so a
	// caller that skips them still writes nothing.
	if err := validServerEntry(e); err != nil {
		return preferences.Result{Document: preferences.Default(), Changed: nil}, err
	}

	if err := patch.Validate(); err != nil {
		return preferences.Result{Document: preferences.Default(), Changed: nil}, fmt.Errorf("check patch: %w", err)
	}

	var result preferences.Result

	err := s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
		}

		res, err := preferences.Change(ctx, preferencesTx{q: q, lock: true}, preferences.ChangeInput{
			Log: s.logger, PersonID: personID, Patch: patch, Entry: e, IfMatch: ifMatch,
		})
		result = res

		return err
	})
	if err != nil {
		return preferences.Result{Document: preferences.Default(), Changed: nil}, err
	}

	return result, nil
}
