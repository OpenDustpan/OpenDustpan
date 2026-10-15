package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

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

		n, err := q.CreateTempAdmin(ctx, db.CreateTempAdminParams{
			PrincipalID: r.PrincipalID,
			Subject:     r.Subject,
			ExpiresAtMs: codec.Millis(r.ExpiresAt),
			CreatedAtMs: codec.Millis(r.CreatedAt),
			CreatedBy:   r.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("insert temporary administrator: %w", err)
		}

		if n == 0 {
			return tempadmin.ErrAlreadyExists
		}

		return nil
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

	return tempAdminOf(row.PrincipalID, row.Subject, row.CreatedBy, row.CreatedAtMs, row.ExpiresAtMs), nil
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

	return tempAdminOf(row.PrincipalID, row.Subject, row.CreatedBy, row.CreatedAtMs, row.ExpiresAtMs), nil
}

// ListTempAdmins implements tempadmin.Store.
func (s *Store) ListTempAdmins(ctx context.Context) ([]tempadmin.TempAdmin, error) {
	rows, err := s.queries.ListTempAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("list temporary administrators: %w", err)
	}

	out := make([]tempadmin.TempAdmin, 0, len(rows))

	for _, row := range rows {
		out = append(out, tempAdminOf(row.PrincipalID, row.Subject, row.CreatedBy, row.CreatedAtMs, row.ExpiresAtMs))
	}

	return out, nil
}

// ExpireTempAdmin implements tempadmin.Store: it disables the principal at
// e.At and drops its record, with the entry e in the same transaction.
func (s *Store) ExpireTempAdmin(ctx context.Context, principalID string, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		if _, err := q.GetTempAdmin(ctx, principalID); errors.Is(err, pgx.ErrNoRows) {
			return tempadmin.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("read temporary administrator: %w", err)
		}

		disable := db.DisablePrincipalForExpiryParams{DisabledAtMs: codec.Millis(e.At), ID: principalID}

		if _, err := q.DisablePrincipalForExpiry(ctx, disable); err != nil {
			return fmt.Errorf("disable principal %q: %w", principalID, err)
		}

		n, err := q.DeleteTempAdmin(ctx, principalID)
		if err != nil {
			return fmt.Errorf("delete temporary administrator: %w", err)
		}

		if n == 0 {
			return tempadmin.ErrNotFound
		}

		return nil
	})
}

// tempAdminOf converts a row into its domain record.
func tempAdminOf(id, subject, createdBy string, createdAtMs, expiresAtMs int64) tempadmin.TempAdmin {
	return tempadmin.TempAdmin{
		CreatedAt:   codec.FromMillis(createdAtMs),
		ExpiresAt:   codec.FromMillis(expiresAtMs),
		PrincipalID: id,
		Subject:     subject,
		CreatedBy:   createdBy,
	}
}
