package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/principal"
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
		CreatedBy:   t.CreatedBy,
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
			RevokedAtMs: row.RevokedAtMs, ReplacedBy: row.ReplacedBy, CreatedBy: row.CreatedBy,
			DisabledAtMs: row.DisabledAtMs,
		})),
		Owner: codec.FromAccessTokenOwnerRow(codec.AccessTokenOwnerRow{
			ID: row.PrincipalID, Subject: row.Subject, DisplayName: row.DisplayName, Kind: row.PrincipalKind,
			DisabledAtMs: row.PrincipalDisabledAtMs.Int64, ActiveAtMs: row.PrincipalActiveAtMs,
			BreakGlass: row.PrincipalBreakGlass,
		}),
		Digest: stored,
		Policy: policyOf(db.GetAccessTokenPolicyRow{
			MaxLifetimeMs: row.PolicyMaxLifetimeMs, InactivityMs: row.PolicyInactivityMs,
			DisableTokens: row.PolicyDisableTokens, DisableAccounts: row.PolicyDisableAccounts,
			DisableBreakGlass: row.PolicyDisableBreakGlass, UpdatedAtMs: row.PolicyUpdatedAtMs,
		}),
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

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		rows, err := q.ListAccessTokens(ctx, db.ListAccessTokensParams{
			PrincipalID: cols.PrincipalID,
			Status:      cols.Status,
			AtMs:        cols.AtMs,
			SoonMs:      cols.SoonMs,
			HasAfter:    hasAfter,
			AfterMs:     cols.AfterMs,
			AfterID:     cols.AfterID,
			RowLimit:    int64(tq.Limit),
		})
		if err != nil {
			return fmt.Errorf("select access tokens: %w", err)
		}

		out = mapRows(rows, func(r db.ListAccessTokensRow) accesstoken.Token {
			return codec.FromAccessTokenRow(tokenRow(r))
		})

		total, err = q.CountAccessTokens(ctx, db.CountAccessTokensParams{
			PrincipalID: cols.PrincipalID, Status: cols.Status, AtMs: cols.AtMs, SoonMs: cols.SoonMs,
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

// RevokeToken implements accesstoken.Store. It only reduces access, so it runs
// in maintenance mode and the maintenance journal records it.
func (s *Store) RevokeToken(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rev := revocation{kind: maintenance.RevokedToken, target: id, endReason: ""}

	return s.inRevokingTx(ctx, e, rev, func(q *db.Queries) error {
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

	err := s.inTx(ctx, func(q *db.Queries) error {
		return q.TouchAccessToken(ctx, db.TouchAccessTokenParams{
			AtMs: codec.Millis(at), ID: id, BeforeMs: codec.Millis(at.Add(-every)),
		})
	})
	if err != nil {
		return fmt.Errorf("touch access token: %w", err)
	}

	return nil
}

// GetPolicy implements accesstoken.Store.
func (s *Store) GetPolicy(ctx context.Context) (accesstoken.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetAccessTokenPolicy(ctx)
	if err != nil {
		return accesstoken.Policy{}, fmt.Errorf("select access token policy: %w", err)
	}

	return policyOf(row), nil
}

// UpdatePolicy implements accesstoken.Store. The write lock of the transaction orders it with every other change of
// @server.
func (s *Store) UpdatePolicy(ctx context.Context, update accesstoken.PolicyUpdate) (accesstoken.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return updateServerSetting(ctx, s, func(q *db.Queries) (accesstoken.Policy, error) {
		row, err := q.GetAccessTokenPolicy(ctx)
		if err != nil {
			return accesstoken.Policy{}, fmt.Errorf("select access token policy: %w", err)
		}

		return policyOf(row), nil
	}, update, func(q *db.Queries, p accesstoken.Policy) error {
		return setPolicy(ctx, q, p)
	})
}

// policyOf maps the generated policy row to the policy.
func policyOf(row db.GetAccessTokenPolicyRow) accesstoken.Policy {
	return codec.FromAccessTokenPolicyRow(codec.AccessTokenPolicyRow{
		MaxLifetimeMs:     row.MaxLifetimeMs,
		InactivityMs:      row.InactivityMs,
		UpdatedAtMs:       row.UpdatedAtMs,
		DisableTokens:     row.DisableTokens,
		DisableAccounts:   row.DisableAccounts,
		DisableBreakGlass: row.DisableBreakGlass,
	})
}

// setPolicy writes p in the transaction of q.
func setPolicy(ctx context.Context, q *db.Queries, p accesstoken.Policy) error {
	row := codec.ToAccessTokenPolicyRow(p)

	n, err := q.SetAccessTokenPolicy(ctx, db.SetAccessTokenPolicyParams{
		MaxLifetimeMs:     row.MaxLifetimeMs,
		InactivityMs:      row.InactivityMs,
		DisableTokens:     row.DisableTokens,
		DisableAccounts:   row.DisableAccounts,
		DisableBreakGlass: row.DisableBreakGlass,
		UpdatedAtMs:       row.UpdatedAtMs,
	})
	if err != nil {
		return fmt.Errorf("update access token policy: %w", err)
	}

	if n != 1 {
		return errNoPolicyRow
	}

	return nil
}

// SweepInactive implements accesstoken.Store. The write lock of the transaction orders it with every other change of
// @server. The time of the run
// comes from the database clock.
func (s *Store) SweepInactive(
	ctx context.Context, sw accesstoken.Sweep, entry accesstoken.SweepEntryFunc,
) (accesstoken.SweepResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var res accesstoken.SweepResult

	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error

		res, err = sweepInactive(ctx, q, sw)
		if err != nil {
			return err
		}

		return s.appendSweepEntries(ctx, q, res, entry)
	})
	if err != nil {
		return accesstoken.SweepResult{}, err
	}

	return res, nil
}

// sweepInactive disables the inactive tokens and accounts of sw in the
// transaction of q, at the time of the database clock. The run keeps the
// temporary administrators that still work, and only their fixed expiry
// ends them. An expired record is a tombstone, not an exemption: it never
// keeps a token or a principal.
func sweepInactive(ctx context.Context, q *db.Queries, sw accesstoken.Sweep) (accesstoken.SweepResult, error) {
	nowMs, err := q.DatabaseNowMs(ctx)
	if err != nil {
		return accesstoken.SweepResult{}, fmt.Errorf("read database clock: %w", err)
	}

	res := accesstoken.SweepResult{
		At: codec.FromMillis(nowMs), Tokens: []accesstoken.SweptToken{}, Principals: []string{},
	}
	params := db.DisableInactiveAccessTokensParams{AtMs: nowMs, CutoffMs: nowMs - sw.Inactivity.Milliseconds()}

	if sw.Tokens {
		rows, err := q.DisableInactiveAccessTokens(ctx, params)
		if err != nil {
			return accesstoken.SweepResult{}, fmt.Errorf("disable inactive access tokens: %w", err)
		}

		for _, r := range rows {
			res.Tokens = append(res.Tokens, accesstoken.SweptToken{ID: r.ID, PrincipalID: r.PrincipalID})
		}
	}

	if sw.Accounts {
		ids, err := q.DisableInactivePrincipals(ctx, db.DisableInactivePrincipalsParams(params))
		if err != nil {
			return accesstoken.SweepResult{}, fmt.Errorf("disable inactive accounts: %w", err)
		}

		res.Principals = ids
	}

	return res, nil
}

// appendSweepEntries appends one entry for each record of res, in the
// transaction of q.
func (s *Store) appendSweepEntries(
	ctx context.Context, q *db.Queries, res accesstoken.SweepResult, entry accesstoken.SweepEntryFunc,
) error {
	objects := make([]string, 0, len(res.Tokens)+len(res.Principals))
	for _, t := range res.Tokens {
		objects = append(objects, accesstoken.TokenRef(t.PrincipalID, t.ID))
	}

	for _, id := range res.Principals {
		objects = append(objects, principal.Ref(id))
	}

	for _, object := range objects {
		e := entry(res.At, object)
		if err := validServerEntry(e); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}
	}

	return nil
}

// errNoPolicyRow reports a database without the seeded policy row, which
// migration 0054 writes.
var errNoPolicyRow = errors.New("access token policy row is missing")

// tokenRow maps a generated token row to its codec row.
func tokenRow(r db.ListAccessTokensRow) codec.AccessTokenRow {
	return codec.AccessTokenRow{
		ID:           r.ID,
		PrincipalID:  r.PrincipalID,
		Name:         r.Name,
		Kind:         r.Kind,
		ReplacedBy:   r.ReplacedBy.String,
		CreatedBy:    r.CreatedBy,
		CreatedAtMs:  r.CreatedAtMs,
		ExpiresAtMs:  r.ExpiresAtMs,
		LastUsedAtMs: r.LastUsedAtMs.Int64,
		RevokedAtMs:  r.RevokedAtMs.Int64,
		DisabledAtMs: r.DisabledAtMs.Int64,
	}
}
