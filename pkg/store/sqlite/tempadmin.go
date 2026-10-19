package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

var _ tempadmin.Store = (*Store)(nil)

// CreateTempAdmin implements tempadmin.Store.
func (s *Store) CreateTempAdmin(ctx context.Context, r tempadmin.TempAdmin, e audit.Entry) error {
	if err := r.Validate(); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		if _, err := q.GetPrincipalNamespace(ctx, r.PrincipalID); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("principal %q: %w", r.PrincipalID, principal.ErrNotFound)
		} else if err != nil {
			return fmt.Errorf("read principal %q: %w", r.PrincipalID, err)
		}

		return insertTempAdmin(ctx, q, r)
	})
}

// GetTempAdmin implements tempadmin.Store.
func (s *Store) GetTempAdmin(ctx context.Context, principalID string) (tempadmin.TempAdmin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetTempAdmin(ctx, principalID)
	if errors.Is(err, sql.ErrNoRows) {
		return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
	}

	if err != nil {
		return tempadmin.TempAdmin{}, fmt.Errorf("select temporary administrator: %w", err)
	}

	return tempAdminOf(row), nil
}

// GetTempAdminBySubject implements tempadmin.Store.
func (s *Store) GetTempAdminBySubject(ctx context.Context, subject string) (tempadmin.TempAdmin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, subjectErr := s.queries.GetTempAdminBySubject(ctx, subject)
	if errors.Is(subjectErr, sql.ErrNoRows) {
		return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
	}

	if subjectErr != nil {
		return tempadmin.TempAdmin{}, fmt.Errorf("select temporary administrator: %w", subjectErr)
	}

	return tempAdminOf(row), nil
}

// ListTempAdmins implements tempadmin.Store.
func (s *Store) ListTempAdmins(ctx context.Context) ([]tempadmin.TempAdmin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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

// ExpireTempAdmin implements tempadmin.Store: at e.At it disables the
// principal, ends every site-administrator grant of the principal on the
// installation and ends the host-recovery tokens of the principal, with the
// entry e in the same transaction. The record stays as a tombstone, so a
// later use is still denied. When the principal is already disabled, the
// expiry still ends the grants and the tokens. A record that already ended
// answers tempadmin.ErrExpired and writes no second entry.
func (s *Store) ExpireTempAdmin(ctx context.Context, principalID string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return expireTempAdminTx(ctx, q, principalID, e)
	})
}

// expireTempAdminTx ends one temporary administrator in the transaction of
// q. It still ends the grants and the tokens when the principal is already
// disabled, and only answers ErrExpired when nothing changed.
func expireTempAdminTx(ctx context.Context, q *db.Queries, principalID string, e audit.Entry) error {
	if _, err := q.GetTempAdmin(ctx, principalID); errors.Is(err, sql.ErrNoRows) {
		return tempadmin.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read temporary administrator: %w", err)
	}

	disabled, err := disableTempAdminPrincipal(ctx, q, principalID, e)
	if err != nil {
		return err
	}

	grants, err := revokeTempAdminGrants(ctx, q, principalID, e)
	if err != nil {
		return err
	}

	tokens, err := revokeTempAdminTokens(ctx, q, principalID, e)
	if err != nil {
		return err
	}

	if disabled == 0 && grants == 0 && tokens == 0 {
		return tempadmin.ErrExpired
	}

	return nil
}

// disableTempAdminPrincipal disables the principal at the time of e.
func disableTempAdminPrincipal(
	ctx context.Context, q *db.Queries, principalID string, e audit.Entry,
) (int64, error) {
	disable := db.DisablePrincipalForExpiryParams{DisabledAtMs: codec.Millis(e.At), ID: principalID}

	n, err := q.DisablePrincipalForExpiry(ctx, disable)
	if err != nil {
		return 0, fmt.Errorf("disable principal %q: %w", principalID, err)
	}

	return n, nil
}

// revokeTempAdminGrants ends every live site-administrator grant of the
// principal on the installation at the time of e.
func revokeTempAdminGrants(
	ctx context.Context, q *db.Queries, principalID string, e audit.Entry,
) (int64, error) {
	grants := db.RevokeTempAdminSiteGrantsParams{
		RevokedAtMs: nullMillis(codec.Millis(e.At)),
		RevokedBy:   e.Subject,
		SubjectID:   principalID,
		Target:      identity.ServerObject().String(),
	}

	n, err := q.RevokeTempAdminSiteGrants(ctx, grants)
	if err != nil {
		return 0, fmt.Errorf("end site-administrator grants of %q: %w", principalID, err)
	}

	return n, nil
}

// revokeTempAdminTokens ends the live host-recovery tokens of the principal
// at the time of e.
func revokeTempAdminTokens(
	ctx context.Context, q *db.Queries, principalID string, e audit.Entry,
) (int64, error) {
	tokens := db.RevokeTempAdminHostTokensParams{
		RevokedAtMs: nullMillis(codec.Millis(e.At)),
		PrincipalID: principalID,
		Name:        tempadmin.HostTokenName,
		ExpiresAtMs: codec.Millis(e.At),
	}

	n, err := q.RevokeTempAdminHostTokens(ctx, tokens)
	if err != nil {
		return 0, fmt.Errorf("end host-recovery tokens of %q: %w", principalID, err)
	}

	return n, nil
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
	}
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
