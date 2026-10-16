package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var (
	_ session.Store       = (*Store)(nil)
	_ session.PolicyStore = (*Store)(nil)
)

// CreateSession implements session.Store.
func (s *Store) CreateSession(ctx context.Context, ss session.Session, d session.Digest, e audit.Entry) error {
	if err := ss.Validate(); err != nil {
		return err
	}

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.CreateSession(ctx, db.CreateSessionParams{
			ID:          ss.ID,
			Digest:      d.String(),
			Provider:    ss.Provider,
			CreatedAtMs: codec.Millis(ss.CreatedAt),
			AuthTimeMs:  codec.Millis(ss.AuthTime),
			SourceIp:    ss.SourceIP,
			UserAgent:   ss.UserAgent,
			PrincipalID: ss.PrincipalID,
		})
		if err != nil {
			return fmt.Errorf("insert session: %w", err)
		}

		if n != 1 {
			return refusedSession(ctx, q, ss.PrincipalID)
		}

		last := db.SetLastSignInParams{AtMs: codec.Millis(ss.CreatedAt), ID: ss.PrincipalID}
		if err := q.SetLastSignIn(ctx, last); err != nil {
			return fmt.Errorf("set last sign-in: %w", err)
		}

		return nil
	})
}

// refusedSession names why the insert of a session of the principal id
// inserted nothing: the principal, or a taken ID or digest.
func refusedSession(ctx context.Context, q *db.Queries, id string) error {
	p, err := q.GetSessionPrincipal(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.ErrPrincipalNotFound
	}

	if err != nil {
		return fmt.Errorf("select principal: %w", err)
	}

	switch {
	case p.Kind != string(principal.KindPerson):
		return fmt.Errorf("%w: the principal is a %s", session.ErrPrincipalNotFound, p.Kind)
	case p.DisabledAtMs.Valid:
		return session.ErrPrincipalDisabled
	default:
		return session.ErrAlreadyExists
	}
}

// GetSession implements session.Store.
func (s *Store) GetSession(ctx context.Context, id string) (session.Session, error) {
	row, err := s.queries.GetSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, session.ErrNotFound
	}

	if err != nil {
		return session.Session{}, fmt.Errorf("select session: %w", err)
	}

	return session.Session{
		CreatedAt:   codec.FromMillis(row.CreatedAtMs),
		LastSeenAt:  codec.FromMillis(row.LastSeenAtMs),
		AuthTime:    codec.FromMillis(row.AuthTimeMs),
		EndedAt:     codec.FromOptionalMillis(row.EndedAtMs.Int64),
		ID:          row.ID,
		PrincipalID: row.PrincipalID,
		Provider:    row.Provider,
		EndReason:   session.EndReason(row.EndReason),
		SourceIP:    row.SourceIp,
		UserAgent:   row.UserAgent,
	}, nil
}

// GetSessionByDigest implements session.Store.
func (s *Store) GetSessionByDigest(ctx context.Context, d session.Digest) (session.Match, error) {
	row, err := s.queries.GetSessionByDigest(ctx, d.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Match{}, session.ErrNotFound
	}

	if err != nil {
		return session.Match{}, fmt.Errorf("select session by digest: %w", err)
	}

	stored, err := session.ParseDigest(row.Digest)
	if err != nil {
		return session.Match{}, fmt.Errorf("session %q: stored digest: %w", row.ID, err)
	}

	return session.Match{
		Session: session.Session{
			CreatedAt:   codec.FromMillis(row.CreatedAtMs),
			LastSeenAt:  codec.FromMillis(row.LastSeenAtMs),
			AuthTime:    codec.FromMillis(row.AuthTimeMs),
			EndedAt:     codec.FromOptionalMillis(row.EndedAtMs.Int64),
			ID:          row.ID,
			PrincipalID: row.PrincipalID,
			Provider:    row.Provider,
			EndReason:   session.EndReason(row.EndReason),
			SourceIP:    row.SourceIp,
			UserAgent:   row.UserAgent,
		},
		Owner: session.Owner{
			ActiveAt:    codec.FromMillis(row.PrincipalActiveAtMs),
			DisabledAt:  codec.FromOptionalMillis(row.PrincipalDisabledAtMs.Int64),
			ID:          row.PrincipalID,
			Subject:     row.Subject,
			DisplayName: row.DisplayName,
			Kind:        principal.Kind(row.PrincipalKind),
			BreakGlass:  row.PrincipalBreakGlass,
		},
		Digest: stored,
	}, nil
}

// TouchSession implements session.Store.
func (s *Store) TouchSession(
	ctx context.Context, id string, at time.Time, every time.Duration, l session.Limits,
) error {
	bounds := l.ActiveAfter(at)

	err := s.queries.TouchSession(ctx, db.TouchSessionParams{
		AtMs: codec.Millis(at), ID: id, BeforeMs: codec.Millis(at.Add(-every)),
		SeenAfterMs: boundMillis(bounds.LastSeen), CreatedAfterMs: boundMillis(bounds.Created),
	})
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}

	return nil
}

// EndSession implements session.Store.
func (s *Store) EndSession(ctx context.Context, id string, reason session.EndReason, e audit.Entry) error {
	if err := session.ValidEndReason(reason); err != nil {
		return err
	}

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.EndSession(ctx, db.EndSessionParams{AtMs: codec.Millis(e.At), EndReason: string(reason), ID: id})
		if err != nil {
			return fmt.Errorf("end session: %w", err)
		}

		if n == 1 {
			return nil
		}

		count, err := q.CountSession(ctx, id)
		if err != nil {
			return fmt.Errorf("select session: %w", err)
		}

		if count == 0 {
			return session.ErrNotFound
		}

		return session.ErrEnded
	})
}

// CreateRequest implements session.Store.
func (s *Store) CreateRequest(ctx context.Context, r session.Request, d session.Digest) error {
	if err := r.Validate(); err != nil {
		return err
	}

	n, err := s.queries.CreateSignInRequest(ctx, db.CreateSignInRequestParams{
		Digest:      d.String(),
		Provider:    r.Provider,
		Nonce:       r.Nonce,
		Verifier:    r.Verifier,
		ReturnPath:  r.ReturnPath,
		CreatedAtMs: codec.Millis(r.CreatedAt),
		ExpiresAtMs: codec.Millis(r.ExpiresAt),
		MaxAgeMs:    pgtype.Int8{Int64: r.MaxAge.Milliseconds(), Valid: r.HasMaxAge},
	})
	if err != nil {
		return fmt.Errorf("insert sign-in request: %w", err)
	}

	if n != 1 {
		return session.ErrAlreadyExists
	}

	return nil
}

// TakeRequest implements session.Store.
func (s *Store) TakeRequest(ctx context.Context, d session.Digest, at time.Time) (session.Request, error) {
	row, err := s.queries.TakeSignInRequest(ctx, d.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Request{}, session.ErrNotFound
	}

	if err != nil {
		return session.Request{}, fmt.Errorf("take sign-in request: %w", err)
	}

	if row.ExpiresAtMs <= codec.Millis(at) {
		return session.Request{}, session.ErrNotFound
	}

	return session.Request{
		CreatedAt:  codec.FromMillis(row.CreatedAtMs),
		ExpiresAt:  codec.FromMillis(row.ExpiresAtMs),
		Provider:   row.Provider,
		Nonce:      row.Nonce,
		Verifier:   row.Verifier,
		ReturnPath: row.ReturnPath,
		MaxAge:     time.Duration(row.MaxAgeMs.Int64) * time.Millisecond,
		HasMaxAge:  row.MaxAgeMs.Valid,
	}, nil
}

// PruneRequests implements session.Store.
func (s *Store) PruneRequests(ctx context.Context, at time.Time) (int64, error) {
	n, err := s.queries.PruneSignInRequests(ctx, codec.Millis(at))
	if err != nil {
		return 0, fmt.Errorf("prune sign-in requests: %w", err)
	}

	return n, nil
}

// GetSessionPolicy implements session.PolicyStore.
func (s *Store) GetSessionPolicy(ctx context.Context) (session.Policy, error) {
	row, err := s.queries.GetSessionPolicy(ctx)
	if err != nil {
		return session.Policy{}, fmt.Errorf("select session policy: %w", err)
	}

	return sessionPolicyOf(row), nil
}

// UpdateSessionPolicy implements session.PolicyStore. The transaction takes the
// advisory lock of @server first, as every audited change does.
func (s *Store) UpdateSessionPolicy(ctx context.Context, update session.PolicyUpdate) (session.Policy, error) {
	return updateServerSetting(ctx, s, func(q *db.Queries) (session.Policy, error) {
		row, err := q.GetSessionPolicy(ctx)
		if err != nil {
			return session.Policy{}, fmt.Errorf("select session policy: %w", err)
		}

		return sessionPolicyOf(row), nil
	}, update, func(q *db.Queries, p session.Policy) error {
		return setSessionPolicy(ctx, q, p)
	})
}

// sessionPolicyOf maps the generated policy row to the policy.
func sessionPolicyOf(row db.GetSessionPolicyRow) session.Policy {
	return codec.FromSessionPolicyRow(codec.SessionPolicyRow{
		IdleMs: row.IdleMs, MaxLengthMs: row.MaxLengthMs, UpdatedAtMs: row.UpdatedAtMs,
		TokenFreshnessMs: row.TokenFreshnessMs.Int64, TokenFreshnessSet: row.TokenFreshnessMs.Valid,
	})
}

// setSessionPolicy writes p in the transaction of q.
func setSessionPolicy(ctx context.Context, q *db.Queries, p session.Policy) error {
	row := codec.ToSessionPolicyRow(p)

	n, err := q.SetSessionPolicy(ctx, db.SetSessionPolicyParams{
		IdleMs: row.IdleMs, MaxLengthMs: row.MaxLengthMs, UpdatedAtMs: row.UpdatedAtMs,
		TokenFreshnessMs: pgtype.Int8{Int64: row.TokenFreshnessMs, Valid: row.TokenFreshnessSet},
	})
	if err != nil {
		return fmt.Errorf("update session policy: %w", err)
	}

	if n != 1 {
		return errNoSessionPolicyRow
	}

	return nil
}

// errNoSessionPolicyRow reports a database without the seeded session
// policy row.
var errNoSessionPolicyRow = errors.New("session policy row is missing")

// boundMillis returns t in Unix milliseconds, or the lowest value for the
// zero time: a bound that every stored time passes.
func boundMillis(t time.Time) int64 {
	if t.IsZero() {
		return math.MinInt64
	}

	return codec.Millis(t)
}

// BindProvider implements session.ProviderStore. The insert keeps an
// earlier record, so the read answers the issuer of the first start.
func (s *Store) BindProvider(ctx context.Context, id, issuer string, at time.Time) (string, error) {
	err := s.queries.InsertOIDCProvider(ctx, db.InsertOIDCProviderParams{
		ProviderID: id, Issuer: issuer, CreatedAtMs: codec.Millis(at),
	})
	if err != nil {
		return "", fmt.Errorf("insert sign-in provider: %w", err)
	}

	recorded, err := s.queries.GetOIDCProviderIssuer(ctx, id)
	if err != nil {
		return "", fmt.Errorf("select sign-in provider: %w", err)
	}

	return recorded, nil
}
