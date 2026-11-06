package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/generationstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ session.PrincipalStateStore = (*Store)(nil)

// errNoBumpEntry reports a bump of the local user store with no audit entry
// of the change: the bump fails closed.
var errNoBumpEntry = errors.New("a bump of the session generation needs the audit entry of its change")

// generationTx is the generationstore.Tx of one transaction.
type generationTx struct {
	q *db.Queries
}

func (t generationTx) PrincipalKind(ctx context.Context, id string) (string, error) {
	p, err := t.q.GetSessionPrincipal(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", generationstore.ErrNoRow
	}

	if err != nil {
		return "", fmt.Errorf("select principal: %w", err)
	}

	return p.Kind, nil
}

// BumpGeneration adds 1 with no condition for an expected generation of 0.
// For another expected generation it updates the row only while it holds
// that generation, and for the expected generation 1 it writes the first row
// when the principal has none.
func (t generationTx) BumpGeneration(ctx context.Context, id string, expected int64) (int64, error) {
	if expected == 0 {
		n, err := t.q.BumpSessionGeneration(ctx, id)
		if err != nil {
			return 0, fmt.Errorf("bump session generation: %w", err)
		}

		return n, nil
	}

	n, err := t.q.BumpSessionGenerationFrom(ctx, db.BumpSessionGenerationFromParams{
		PrincipalID: id, Expected: expected,
	})
	if err != nil {
		return 0, fmt.Errorf("bump session generation: %w", err)
	}

	// A principal with no row has the generation 1: only the expected
	// generation 1 writes its first row.
	if n == 1 || expected != 1 {
		return n, nil
	}

	n, err = t.q.InsertFirstSessionGeneration(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("write first session generation: %w", err)
	}

	return n, nil
}

func (t generationTx) Generation(ctx context.Context, id string) (int64, error) {
	g, err := t.q.GetSessionGeneration(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("select session generation: %w", err)
	}

	return g, nil
}

func (t generationTx) Append(ctx context.Context, e audit.Entry) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	if _, err := appendAuditEntry(ctx, t.q, e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// PrincipalState implements session.PrincipalStateStore.
func (s *Store) PrincipalState(ctx context.Context, id string) (session.PrincipalState, error) {
	row, err := s.queries.GetPrincipalState(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.PrincipalState{}, fmt.Errorf("%w: %s", session.ErrPrincipalNotFound, id)
	}

	if err != nil {
		return session.PrincipalState{}, fmt.Errorf("select principal state: %w", err)
	}

	if row.PrincipalKind != string(principal.KindPerson) {
		return session.PrincipalState{}, fmt.Errorf("%w: the principal is a %s", session.ErrPrincipalNotFound,
			row.PrincipalKind)
	}

	g, err := generationstore.FromRow(row.PrincipalGeneration)
	if err != nil {
		return session.PrincipalState{}, err
	}

	return session.PrincipalState{
		Owner: session.Owner{
			ActiveAt:    codec.FromMillis(row.PrincipalActiveAtMs),
			DisabledAt:  codec.FromOptionalMillis(row.PrincipalDisabledAtMs.Int64),
			ID:          row.ID,
			Subject:     row.Subject,
			DisplayName: row.DisplayName,
			Kind:        principal.Kind(row.PrincipalKind),
			BreakGlass:  row.PrincipalBreakGlass,
		},
		Policy: codec.FromSessionPolicyRow(codec.SessionPolicyRow{
			IdleMs: row.PolicyIdleMs, MaxLengthMs: row.PolicyMaxLengthMs, UpdatedAtMs: row.PolicyUpdatedAtMs,
			TokenFreshnessMs: row.PolicyTokenFreshnessMs.Int64, TokenFreshnessSet: row.PolicyTokenFreshnessMs.Valid,
		}),
		Accounts:   codec.FromSessionAccountsRow(row.AccountsInactivityMs, row.AccountsDisable),
		Generation: g,
	}, nil
}

// BumpGeneration implements session.PrincipalStateStore. The bump, its
// checks and the append of e run in one transaction.
func (s *Store) BumpGeneration(
	ctx context.Context, id string, cause session.BumpCause, expected uint64, e audit.Entry,
) (uint64, error) {
	if err := session.ValidBumpCause(cause); err != nil {
		return 0, err
	}

	if err := validServerEntry(e); err != nil {
		return 0, err
	}

	var g uint64

	err := s.inTx(ctx, func(q *db.Queries) error {
		next, bumpErr := generationstore.Bump(
			ctx,
			generationTx{q: q},
			generationstore.Request{ID: id, Cause: cause, Expected: expected},
			e,
		)
		g = next

		return bumpErr
	})
	if err != nil {
		return 0, err
	}

	return g, nil
}

// bumpFor runs the bump r in the transaction of q, with the entry
// session:generation-bumped of the change with the entry change, and returns
// the new generation. A nil change fails closed.
func bumpFor(ctx context.Context, q *db.Queries, r generationstore.Request, change *audit.Entry) (uint64, error) {
	if change == nil {
		return 0, errNoBumpEntry
	}

	return generationstore.Bump(ctx, generationTx{q: q}, r, generationstore.EntryOf(*change, r.ID))
}
