package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var _ workspace.ClaimStore = (*Store)(nil)

// WithClaims implements workspace.ClaimStore. The transaction takes the
// namespace row FOR UPDATE (the fence and the audit chain) and then the
// workspace row FOR UPDATE, so two registrations on one workspace run one
// after the other and the second reads the claims that the first committed.
//
// Defense in depth: each of the two locks orders the registrations alone. The
// namespace row lock comes from the audit append, and the workspace row lock
// stays so that the claims of one workspace stay ordered if the helper of the
// audited write changes its lock. Neither lock relies on the other, and the
// unique key of the claim sequence refuses a second insert with the same
// number.
func (s *Store) WithClaims(
	ctx context.Context, ws workspace.ID, fence namespace.Fence, fn func(tx workspace.ClaimTx) error,
) error {
	return s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		if err := lockWorkspaceForClaims(ctx, q, ws, fence); err != nil {
			return err
		}

		return fn(&workspaceClaimTx{q: q, ws: ws, fence: fence})
	})
}

// lockWorkspaceForClaims checks that the workspace is a live workspace of the
// namespace of the fence. A workspace of another namespace reads as missing.
func lockWorkspaceForClaims(ctx context.Context, q *db.Queries, ws workspace.ID, fence namespace.Fence) error {
	_, err := q.LockWorkspaceForClaims(ctx, db.LockWorkspaceForClaimsParams{
		ID: ws.String(), Namespace: fence.Namespace.String(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("lock workspace for claims: %w", err)
	}

	return nil
}

// workspaceClaimTx is the view of one workspace inside WithClaims.
type workspaceClaimTx struct {
	q     *db.Queries
	ws    workspace.ID
	fence namespace.Fence
}

// Claims implements workspace.ClaimTx.
func (t *workspaceClaimTx) Claims(ctx context.Context) ([]workspace.Claim, error) {
	return readWorkspaceClaims(ctx, t.q, t.ws)
}

// Touches implements workspace.ClaimTx.
func (t *workspaceClaimTx) Touches(ctx context.Context) ([]workspace.ClaimTouch, error) {
	rows, err := t.q.ListWorkspaceClaimTouches(ctx, t.ws.String())
	if err != nil {
		return nil, fmt.Errorf("select claim touches: %w", err)
	}

	out := make([]workspace.ClaimTouch, 0, len(rows))

	for i := range rows {
		addresses, parseErr := codec.ParseClaimAddresses(rows[i].Addresses)
		if parseErr != nil {
			return nil, parseErr
		}

		out = append(
			out,
			workspace.ClaimTouch{Version: workspace.StateVersionID(rows[i].VersionID), Addresses: addresses},
		)
	}

	return out, nil
}

// Insert implements workspace.ClaimTx. The claim and its audit entry commit
// together, or neither does.
func (t *workspaceClaimTx) Insert(ctx context.Context, c workspace.Claim, entry audit.Entry) error {
	// Defense in depth: the registry validates the claim and the entry too.
	// These checks stay so that a new caller of the store cannot write a claim
	// that the registry refuses, or a claim with no valid audit entry.
	if err := workspace.ValidateNewClaim(c); err != nil {
		return err
	}

	if c.WorkspaceID != t.ws {
		return fmt.Errorf("%w: claim names workspace %s, transaction holds %s",
			workspace.ErrClaimInvalid, c.WorkspaceID, t.ws)
	}

	if err := workspace.CheckClaimEntry(t.fence, entry); err != nil {
		return err
	}

	addresses, err := codec.ClaimAddressesText(c.Addresses)
	if err != nil {
		return err
	}

	err = t.q.InsertWorkspaceClaim(ctx, db.InsertWorkspaceClaimParams{
		ID:          c.ID,
		WorkspaceID: c.WorkspaceID.String(),
		Base:        c.Base.String(),
		Addresses:   addresses,
		Principal:   c.Principal,
		JobID:       c.JobID,
		TriggerName: c.Trigger,
		StartedAtMs: codec.Millis(c.StartedAt),
	})
	if err != nil {
		return fmt.Errorf("insert claim: %w", err)
	}

	if _, err = appendAuditEntry(ctx, t.q, entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// readWorkspaceClaims reads the claims of a workspace in accept order.
func readWorkspaceClaims(ctx context.Context, q *db.Queries, ws workspace.ID) ([]workspace.Claim, error) {
	rows, err := q.ListWorkspaceClaims(ctx, ws.String())
	if err != nil {
		return nil, fmt.Errorf("select claims: %w", err)
	}

	out := make([]workspace.Claim, 0, len(rows))

	for i := range rows {
		claim, convErr := codec.ClaimFromRow(codec.ClaimRow(rows[i]))
		if convErr != nil {
			return nil, convErr
		}

		out = append(out, claim)
	}

	return out, nil
}

// ListClaims implements workspace.ClaimStore.
func (s *Store) ListClaims(ctx context.Context, ws workspace.ID) ([]workspace.Claim, error) {
	return readWorkspaceClaims(ctx, s.queries, ws)
}

// EndClaimsByJob implements workspace.ClaimStore.
func (s *Store) EndClaimsByJob(
	ctx context.Context, fence namespace.Fence, jobID string, at time.Time,
) (int, error) {
	if jobID == "" {
		return 0, nil
	}

	var n int64

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		var execErr error

		n, execErr = q.EndWorkspaceClaimsByJob(ctx, db.EndWorkspaceClaimsByJobParams{
			EndedAtMs: codec.Millis(at), JobID: jobID, Namespace: fence.Namespace.String(),
		})

		return execErr
	})
	if err != nil {
		return 0, fmt.Errorf("end claims of job: %w", err)
	}

	return int(n), nil
}

// ExpireClaims implements workspace.ClaimStore.
func (s *Store) ExpireClaims(ctx context.Context, fence namespace.Fence, cutoff time.Time) (int, error) {
	var n int64

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		var execErr error

		n, execErr = q.ExpireWorkspaceClaims(ctx, db.ExpireWorkspaceClaimsParams{
			EndedAtMs: codec.Millis(cutoff), CutoffMs: codec.Millis(cutoff), Namespace: fence.Namespace.String(),
		})

		return execErr
	})
	if err != nil {
		return 0, fmt.Errorf("expire claims: %w", err)
	}

	return int(n), nil
}

// AddClaimTouch implements workspace.ClaimStore.
func (s *Store) AddClaimTouch(
	ctx context.Context, fence namespace.Fence, ws workspace.ID, version workspace.StateVersionID, addresses []string,
) error {
	if version == "" {
		return fmt.Errorf("%w: a touch names its version", workspace.ErrClaimInvalid)
	}

	text, err := codec.ClaimAddressesText(workspace.NormalizeClaimAddresses(addresses))
	if err != nil {
		return err
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if lockErr := lockWorkspaceForClaims(ctx, q, ws, fence); lockErr != nil {
			return lockErr
		}

		return q.InsertWorkspaceClaimTouch(ctx, db.InsertWorkspaceClaimTouchParams{
			WorkspaceID: ws.String(), VersionID: version.String(), Addresses: text,
		})
	})
}
