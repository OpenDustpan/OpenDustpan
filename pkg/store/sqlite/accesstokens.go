package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ accesstoken.Store = (*Store)(nil)

// CreateToken implements accesstoken.Store.
func (s *Store) CreateToken(ctx context.Context, t accesstoken.Token, d accesstoken.Digest, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return insertToken(ctx, q, t, d)
	})
}

// insertToken inserts t in the transaction of q. It tells a missing
// principal from a taken ID or digest.
func insertToken(ctx context.Context, q *db.Queries, t accesstoken.Token, d accesstoken.Digest) error {
	n, err := q.CreateAccessToken(ctx, db.CreateAccessTokenParams{
		ID:          t.ID,
		Name:        t.Name,
		Kind:        string(t.Kind),
		Digest:      d.String(),
		CreatedAtMs: codec.Millis(t.CreatedAt),
		ExpiresAtMs: codec.Millis(t.ExpiresAt),
		PrincipalID: t.PrincipalID,
	})
	if err != nil {
		return fmt.Errorf("insert access token: %w", err)
	}

	if n == 1 {
		return nil
	}

	live, err := q.CountLivePrincipal(ctx, t.PrincipalID)
	if err != nil {
		return fmt.Errorf("select principal: %w", err)
	}

	if live == 0 {
		return accesstoken.ErrPrincipalNotFound
	}

	return accesstoken.ErrAlreadyExists
}

// GetToken implements accesstoken.Store.
func (s *Store) GetToken(ctx context.Context, id string) (accesstoken.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetAccessToken(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return accesstoken.Token{}, accesstoken.ErrNotFound
	}

	if err != nil {
		return accesstoken.Token{}, fmt.Errorf("select access token: %w", err)
	}

	return codec.FromAccessTokenRow(tokenRow(db.ListAccessTokensRow(row))), nil
}

// GetTokenByDigest implements accesstoken.Store.
func (s *Store) GetTokenByDigest(ctx context.Context, d accesstoken.Digest) (accesstoken.Match, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetAccessTokenByDigest(ctx, d.String())
	if errors.Is(err, sql.ErrNoRows) {
		return accesstoken.Match{}, accesstoken.ErrNotFound
	}

	if err != nil {
		return accesstoken.Match{}, fmt.Errorf("select access token by digest: %w", err)
	}

	stored, err := accesstoken.ParseDigest(row.Digest)
	if err != nil {
		return accesstoken.Match{}, fmt.Errorf("access token %q: stored digest: %w", row.ID, err)
	}

	return accesstoken.Match{
		Token: codec.FromAccessTokenRow(tokenRow(db.ListAccessTokensRow{
			ID: row.ID, PrincipalID: row.PrincipalID, Name: row.Name, Kind: row.Kind,
			CreatedAtMs: row.CreatedAtMs, ExpiresAtMs: row.ExpiresAtMs, LastUsedAtMs: row.LastUsedAtMs,
			RevokedAtMs: row.RevokedAtMs, ReplacedBy: row.ReplacedBy,
		})),
		Owner: codec.FromAccessTokenOwnerRow(codec.AccessTokenOwnerRow{
			ID: row.PrincipalID, Subject: row.Subject, DisplayName: row.DisplayName, Kind: row.PrincipalKind,
		}),
		Digest: stored,
	}, nil
}

// ListTokens implements accesstoken.Store. It reads the page and the total
// in one transaction.
func (s *Store) ListTokens(ctx context.Context, tq accesstoken.Query) ([]accesstoken.Token, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cols := codec.ToAccessTokenListQuery(tq)

	var hasAfter int64
	if cols.HasAfter {
		hasAfter = 1
	}

	var (
		out   []accesstoken.Token
		total int64
	)

	err := s.inTx(ctx, func(q *db.Queries) error {
		rows, err := q.ListAccessTokens(ctx, db.ListAccessTokensParams{
			PrincipalID: cols.PrincipalID, ActiveAtMs: cols.ActiveAtMs, HasAfter: hasAfter,
			AfterMs: cols.AfterMs, AfterID: cols.AfterID, RowLimit: int64(tq.Limit),
		})
		if err != nil {
			return fmt.Errorf("select access tokens: %w", err)
		}

		out = mapRows(rows, func(r db.ListAccessTokensRow) accesstoken.Token {
			return codec.FromAccessTokenRow(tokenRow(r))
		})

		total, err = q.CountAccessTokens(ctx, db.CountAccessTokensParams{
			PrincipalID: cols.PrincipalID, ActiveAtMs: cols.ActiveAtMs,
		})
		if err != nil {
			return fmt.Errorf("count access tokens: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// RevokeToken implements accesstoken.Store.
func (s *Store) RevokeToken(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.RevokeAccessToken(ctx, db.RevokeAccessTokenParams{RevokedAtMs: codec.Millis(e.At), ID: id})
		if err != nil {
			return fmt.Errorf("revoke access token: %w", err)
		}

		if n == 1 {
			return nil
		}

		return endedOrMissing(ctx, q, id)
	})
}

// RotateToken implements accesstoken.Store.
func (s *Store) RotateToken(ctx context.Context, r accesstoken.Replacement, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.EndRotatedAccessToken(ctx, db.EndRotatedAccessTokenParams{
			EndsAtMs: codec.Millis(r.OldEndsAt), ReplacedBy: r.Next.ID, ID: r.OldID, NowMs: codec.Millis(e.At),
		})
		if err != nil {
			return fmt.Errorf("end rotated access token: %w", err)
		}

		if n != 1 {
			return endedOrMissing(ctx, q, r.OldID)
		}

		return insertToken(ctx, q, r.Next, r.Digest)
	})
}

// endedOrMissing returns ErrEnded when the token id exists, and
// ErrNotFound when it does not.
func endedOrMissing(ctx context.Context, q *db.Queries, id string) error {
	count, err := q.CountAccessToken(ctx, id)
	if err != nil {
		return fmt.Errorf("select access token: %w", err)
	}

	if count == 0 {
		return accesstoken.ErrNotFound
	}

	return accesstoken.ErrEnded
}

// TouchToken implements accesstoken.Store.
func (s *Store) TouchToken(ctx context.Context, id string, at time.Time, every time.Duration) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err := s.queries.TouchAccessToken(ctx, db.TouchAccessTokenParams{
		AtMs: codec.Millis(at), ID: id, BeforeMs: codec.Millis(at.Add(-every)),
	})
	if err != nil {
		return fmt.Errorf("touch access token: %w", err)
	}

	return nil
}

// tokenRow maps a generated token row to its codec row.
func tokenRow(r db.ListAccessTokensRow) codec.AccessTokenRow {
	return codec.AccessTokenRow{
		ID:           r.ID,
		PrincipalID:  r.PrincipalID,
		Name:         r.Name,
		Kind:         r.Kind,
		ReplacedBy:   r.ReplacedBy.String,
		CreatedAtMs:  r.CreatedAtMs,
		ExpiresAtMs:  r.ExpiresAtMs,
		LastUsedAtMs: r.LastUsedAtMs.Int64,
		RevokedAtMs:  r.RevokedAtMs.Int64,
	}
}
