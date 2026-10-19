package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

var _ tempadmin.Store = (*Store)(nil)

// CreateTempAdmin implements tempadmin.Store.
func (s *Store) CreateTempAdmin(ctx context.Context, r tempadmin.TempAdmin, e audit.Entry) error {
	if err := r.Validate(); err != nil {
		return err
	}

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		if _, err := q.GetPrincipalNamespace(ctx, r.PrincipalID); errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("principal %q: %w", r.PrincipalID, principal.ErrNotFound)
		} else if err != nil {
			return fmt.Errorf("read principal %q: %w", r.PrincipalID, err)
		}

		return insertTempAdmin(ctx, q, r)
	})
}

// GetTempAdmin implements tempadmin.Store.
func (s *Store) GetTempAdmin(ctx context.Context, principalID string) (tempadmin.TempAdmin, error) {
	row, err := s.queries.GetTempAdmin(ctx, principalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
	}

	if err != nil {
		return tempadmin.TempAdmin{}, fmt.Errorf("select temporary administrator: %w", err)
	}

	return tempAdminOf(row), nil
}

// GetTempAdminBySubject implements tempadmin.Store.
func (s *Store) GetTempAdminBySubject(ctx context.Context, subject string) (tempadmin.TempAdmin, error) {
	row, subjectErr := s.queries.GetTempAdminBySubject(ctx, subject)
	if errors.Is(subjectErr, pgx.ErrNoRows) {
		return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
	}

	if subjectErr != nil {
		return tempadmin.TempAdmin{}, fmt.Errorf("select temporary administrator: %w", subjectErr)
	}

	return tempAdminOf(row), nil
}

// ListTempAdmins implements tempadmin.Store.
func (s *Store) ListTempAdmins(ctx context.Context) ([]tempadmin.TempAdmin, error) {
	rows, err := s.queries.ListTempAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("list temporary administrators: %w", err)
	}

	out := make([]tempadmin.TempAdmin, 0, len(rows))

	for _, row := range rows {
		out = append(
			out,
			tempAdminOf(row),
		)
	}

	return out, nil
}

// ExpireTempAdmin implements tempadmin.Store: at e.At it marks the record
// ended, disables the principal, ends every grant and every group
// membership of the principal and ends its live tokens, with the entry e in
// the same transaction. The record stays as a tombstone, so a later use is
// still denied. A record that already ended answers tempadmin.ErrExpired
// and writes no second entry.
func (s *Store) ExpireTempAdmin(ctx context.Context, principalID string, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return expireTempAdminTx(ctx, q, principalID, e)
	})
}

// expireTempAdminTx ends one temporary administrator in the transaction of
// q. Of two concurrent expiries, the update of the record lets one end it,
// and the other answers ErrExpired.
func expireTempAdminTx(ctx context.Context, q *db.Queries, principalID string, e audit.Entry) error {
	if _, err := q.GetTempAdmin(ctx, principalID); errors.Is(err, pgx.ErrNoRows) {
		return tempadmin.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read temporary administrator: %w", err)
	}

	at := codec.Millis(e.At)

	ended, err := q.EndTempAdmin(ctx, db.EndTempAdminParams{EndedAtMs: nullMillis(at), PrincipalID: principalID})
	if err != nil {
		return fmt.Errorf("end temporary administrator: %w", err)
	}

	if ended == 0 {
		return tempadmin.ErrExpired
	}

	disable := db.DisablePrincipalForExpiryParams{DisabledAtMs: at, ID: principalID}
	if _, err := q.DisablePrincipalForExpiry(ctx, disable); err != nil {
		return fmt.Errorf("disable principal %q: %w", principalID, err)
	}

	if err := revokeGrantsOf(ctx, q, access.Subject{Kind: access.SubjectPrincipal, ID: principalID}, e); err != nil {
		return err
	}

	if err := endPrincipalMemberships(ctx, q, principalID, at); err != nil {
		return fmt.Errorf("end group memberships of %q: %w", principalID, err)
	}

	tokens := db.RevokeTempAdminTokensParams{RevokedAtMs: nullMillis(at), PrincipalID: principalID, ExpiresAtMs: at}
	if _, err := q.RevokeTempAdminTokens(ctx, tokens); err != nil {
		return fmt.Errorf("end tokens of %q: %w", principalID, err)
	}

	return nil
}

// tempAdminOf converts a row into its domain record.
func tempAdminOf(row db.TempAdmin) tempadmin.TempAdmin {
	return tempadmin.TempAdmin{
		CreatedAt:   codec.FromMillis(row.CreatedAtMs),
		ExpiresAt:   codec.FromMillis(row.ExpiresAtMs),
		PrincipalID: row.PrincipalID,
		Subject:     row.Subject,
		CreatedBy:   row.CreatedBy,
		Reason:      row.Reason,
		EndedAt:     endedAt(row.EndedAtMs),
	}
}

// endedAt answers the end of a record, or the zero time when the record
// did not end.
func endedAt(ms pgtype.Int8) time.Time {
	if !ms.Valid {
		return time.Time{}
	}

	return codec.FromMillis(ms.Int64)
}

// IsTempAdmin implements principal.TempAdmins: a record of the principal,
// that works or that ended, makes it a temporary administrator.
func (s *Store) IsTempAdmin(ctx context.Context, id string) (bool, error) {
	_, err := s.GetTempAdmin(ctx, id)
	if errors.Is(err, tempadmin.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}
