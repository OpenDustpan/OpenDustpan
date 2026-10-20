package sqlite

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

var _ accesstoken.HostStore = (*Store)(nil)

// CreateHostRecovery implements accesstoken.HostStore: the person, the
// grant, the record and the token of r, and their four entries, in one
// transaction.
// The transaction holds the write lock of the database, so two recoveries
// run one after the other, and the second reads the rows of the first.
func (s *Store) CreateHostRecovery(ctx context.Context, r *accesstoken.HostRecovery) error {
	entries, err := hostRecoveryEntries(r)
	if err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := insertHostRecovery(ctx, q, r); err != nil {
			return err
		}

		for i := range entries {
			if _, err := appendAuditEntry(ctx, q, entries[i]); err != nil {
				return fmt.Errorf("append audit entry: %w", err)
			}
		}

		return nil
	})
}

// hostRecoveryEntries checks r and answers its four entries in the order
// of the writes.
func hostRecoveryEntries(r *accesstoken.HostRecovery) ([]audit.Entry, error) {
	if r.Person.Namespace != "" {
		return nil, fmt.Errorf("%w: a temporary administrator belongs to the installation", principal.ErrInvalid)
	}

	if err := r.Record.Validate(); err != nil {
		return nil, err
	}

	entries := []audit.Entry{r.PersonEntry, r.GrantEntry, r.RecordEntry, r.TokenEntry}

	for i := range entries {
		if err := validServerEntry(entries[i]); err != nil {
			return nil, err
		}
	}

	return entries, nil
}

// insertHostRecovery inserts the person, the grant, the record and the
// token of r in the transaction of q.
func insertHostRecovery(ctx context.Context, q *db.Queries, r *accesstoken.HostRecovery) error {
	if err := insertPrincipal(ctx, q, r.Person); err != nil {
		return err
	}

	if err := insertGrant(ctx, q, r.Grant); err != nil {
		return err
	}

	if err := insertTempAdmin(ctx, q, r.Record); err != nil {
		return err
	}

	return insertFirstHostToken(ctx, q, r.Token, r.Digest)
}

// insertFirstHostToken inserts t in the transaction of q when every token
// check of accesstoken.HostStore.CreateHostRecovery holds, or returns
// accesstoken.ErrNotOwnToken.
func insertFirstHostToken(ctx context.Context, q *db.Queries, t accesstoken.Token, d accesstoken.Digest) error {
	n, err := q.CreateFirstHostToken(ctx, db.CreateFirstHostTokenParams{
		ID:          t.ID,
		Name:        t.Name,
		Kind:        string(t.Kind),
		Digest:      d.String(),
		CreatedAtMs: codec.Millis(t.CreatedAt),
		ExpiresAtMs: codec.Millis(t.ExpiresAt),
		CreatedBy:   t.CreatedBy,
		PrincipalID: t.PrincipalID,
		WindowMs:    accesstoken.HostFirstTokenWindow.Milliseconds(),
	})
	if err != nil {
		return fmt.Errorf("insert host access token: %w", err)
	}

	if n == 0 {
		return accesstoken.ErrNotOwnToken
	}

	return nil
}

// insertTempAdmin inserts r in the transaction of q, or returns
// tempadmin.ErrAlreadyExists when the principal has a record.
func insertTempAdmin(ctx context.Context, q *db.Queries, r tempadmin.TempAdmin) error {
	n, err := q.CreateTempAdmin(ctx, db.CreateTempAdminParams{
		PrincipalID: r.PrincipalID,
		Subject:     r.Subject,
		ExpiresAtMs: codec.Millis(r.ExpiresAt),
		CreatedAtMs: codec.Millis(r.CreatedAt),
		CreatedBy:   r.CreatedBy,
		Reason:      r.Reason,
	})
	if err != nil {
		return fmt.Errorf("insert temporary administrator: %w", err)
	}

	if n == 0 {
		return tempadmin.ErrAlreadyExists
	}

	return nil
}
