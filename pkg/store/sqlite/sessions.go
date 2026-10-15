package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ session.Store = (*Store)(nil)

// CreateSession implements session.Store.
func (s *Store) CreateSession(ctx context.Context, ss session.Session, d session.Digest, e audit.Entry) error {
	if err := ss.Validate(); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

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
	if errors.Is(err, sql.ErrNoRows) {
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetSessionByDigest(ctx, d.String())
	if errors.Is(err, sql.ErrNoRows) {
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
			DisabledAt:  codec.FromOptionalMillis(row.PrincipalDisabledAtMs.Int64),
			ID:          row.PrincipalID,
			Subject:     row.Subject,
			DisplayName: row.DisplayName,
			Kind:        principal.Kind(row.PrincipalKind),
		},
		Digest: stored,
	}, nil
}

// TouchSession implements session.Store.
func (s *Store) TouchSession(ctx context.Context, id string, at time.Time, every time.Duration) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err := s.queries.TouchSession(ctx, db.TouchSessionParams{
		AtMs: codec.Millis(at), ID: id, BeforeMs: codec.Millis(at.Add(-every)),
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

	s.mu.RLock()
	defer s.mu.RUnlock()

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

	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.CreateSignInRequest(ctx, db.CreateSignInRequestParams{
		Digest:      d.String(),
		Provider:    r.Provider,
		Nonce:       r.Nonce,
		Verifier:    r.Verifier,
		ReturnPath:  r.ReturnPath,
		CreatedAtMs: codec.Millis(r.CreatedAt),
		ExpiresAtMs: codec.Millis(r.ExpiresAt),
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.TakeSignInRequest(ctx, d.String())
	if errors.Is(err, sql.ErrNoRows) {
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
	}, nil
}

// PruneRequests implements session.Store.
func (s *Store) PruneRequests(ctx context.Context, at time.Time) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.PruneSignInRequests(ctx, codec.Millis(at))
	if err != nil {
		return 0, fmt.Errorf("prune sign-in requests: %w", err)
	}

	return n, nil
}
