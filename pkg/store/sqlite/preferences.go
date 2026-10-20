package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ preferences.Store = (*Store)(nil)

// preferencesTx is the SQL of an update of preferences inside a transaction.
// The rules are in preferences.Change.
type preferencesTx struct{ q *db.Queries }

var _ preferences.Tx = preferencesTx{q: nil}

// Load implements preferences.Tx. The write lock of the transaction orders
// two updates of one person.
func (t preferencesTx) Load(ctx context.Context, personID string) (string, int64, error) {
	row, err := t.q.GetPersonPreferences(ctx, personID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, preferences.ErrPersonNotFound
	}

	if err != nil {
		return "", 0, fmt.Errorf("select preferences: %w", err)
	}

	return row.Document.String, row.UpdatedAtMs.Int64, nil
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	document, updatedAtMs, err := preferencesTx{q: s.queries}.Load(ctx, personID)
	if err != nil {
		return preferences.Default(), err
	}

	settings, updatedAt := preferences.Load(document, updatedAtMs, s.logger)

	return settings.DocumentAt(updatedAt), nil
}

// Update implements preferences.Store. It runs preferences.Change in one
// transaction with the audit entry.
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

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result preferences.Result

	err := s.inTx(ctx, func(q *db.Queries) error {
		res, err := preferences.Change(ctx, preferencesTx{q: q}, s.logger, personID, patch, e, ifMatch)
		result = res

		return err
	})
	if err != nil {
		return preferences.Result{Document: preferences.Default(), Changed: nil}, err
	}

	return result, nil
}
