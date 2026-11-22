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
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	_ workspace.ClaimStore   = (*Store)(nil)
	_ workspace.ClaimDeleted = (*Store)(nil)
)

// WithClaims implements workspace.ClaimStore. The transaction takes the
// namespace row FOR UPDATE (the fence, and the order of the audit appends of
// the namespace) and then the workspace row FOR UPDATE, so two registrations on
// one workspace run one after the other and the second reads the claims that
// the first committed.
//
// The namespace row lock also orders the audit appends: every registration of
// the namespace, on any of its workspaces, waits for the one before it, so each
// entry reads the last entry of the chain that the one before it committed. The
// claims of two workspaces of one namespace therefore run one after the other
// too, and the chain stays valid.
//
// Defense in depth: each of the two locks orders the registrations of one
// workspace alone. The namespace row lock comes from the audited write, and the
// workspace row lock stays so that the claims of one workspace stay ordered if
// the helper of the audited write changes its lock. Neither lock relies on the
// other, and the unique key of the claim sequence refuses a second insert with
// the same number.
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

// Workspace implements workspace.ClaimTx. It reads the row in the
// transaction, after the claim lock, so a lock that committed before the claim
// lock is in it.
func (t *workspaceClaimTx) Workspace(ctx context.Context) (workspace.Workspace, error) {
	row, err := t.q.ReadWorkspaceByID(ctx, t.ws.String())
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("select workspace: %w", err)
	}

	w := workspaceFromRow(row.Workspace, row.NamespaceID)
	w.InCollection = row.InCollection

	return w, nil
}

// ManualLocks implements workspace.ClaimTx. It reads the locks through the
// queries of the transaction, never through the lock service. A workspace that
// the inventory does not hold is an error, so the check fails closed.
func (t *workspaceClaimTx) ManualLocks(ctx context.Context) ([]manuallock.Lock, error) {
	locks, err := coveringLocksOfUnitID(ctx, t.q, t.fence.Namespace, t.ws.String())
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil, nil
	}

	return locks, err
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
		ID:            c.ID,
		WorkspaceID:   c.WorkspaceID.String(),
		Base:          c.Base.String(),
		Addresses:     addresses,
		Principal:     c.Principal,
		JobID:         c.JobID,
		TriggerName:   c.Trigger,
		StartedAtMs:   codec.Millis(c.StartedAt),
		LeaseEndsAtMs: codec.LeaseMillis(c.LeaseEndsAt),
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

// EndClaimsByJob implements workspace.ClaimStore. Each ended claim writes an
// audit entry in the same transaction.
func (s *Store) EndClaimsByJob(
	ctx context.Context, fence namespace.Fence, jobID string, at time.Time,
) (int, error) {
	if jobID == "" {
		return 0, nil
	}

	var n int

	err := s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		var endErr error

		n, endErr = endJobClaims(ctx, q, fence.Namespace, jobID, at)

		return endErr
	})
	if err != nil {
		return 0, fmt.Errorf("end claims of job: %w", err)
	}

	return n, nil
}

// ExpireClaims implements workspace.ClaimStore. Each ended claim writes an
// audit entry in the same transaction.
func (s *Store) ExpireClaims(
	ctx context.Context, fence namespace.Fence, now time.Time, limit int,
) (int, error) {
	if limit < 1 {
		return 0, nil
	}

	var n int

	err := s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		rows, listErr := q.ListExpiredWorkspaceClaims(ctx, db.ListExpiredWorkspaceClaimsParams{
			NowMs: codec.Millis(now), Namespace: fence.Namespace.String(), MaxRows: int64(limit),
		})
		if listErr != nil {
			return fmt.Errorf("select expired claims: %w", listErr)
		}

		ending := make([]endingClaim, len(rows))
		for i := range rows {
			ending[i] = endingClaim(rows[i])
		}

		var endErr error

		n, endErr = endClaims(ctx, q, fence.Namespace, ending, claimEnd{at: now, cause: workspace.ClaimEndedByExpiry})

		return endErr
	})
	if err != nil {
		return 0, fmt.Errorf("expire claims: %w", err)
	}

	return n, nil
}

// RenewClaim implements workspace.ClaimStore. It runs under the claim lock of
// the workspace, so a renewal and an expiry of one claim run one after the
// other. The update statement holds the rules itself (the workspace, the
// principal, the state and a lease that did not end) and returns the claim, so
// a claim that the rules refuse is not changed; the check of the rules only
// chooses the error.
func (s *Store) RenewClaim(
	ctx context.Context, fence namespace.Fence, r workspace.ClaimRenewal,
) (workspace.Claim, error) {
	var out workspace.Claim

	err := s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		if lockErr := lockWorkspaceForClaims(ctx, q, r.Workspace, fence); lockErr != nil {
			return lockErr
		}

		claim, updErr := renewClaimRow(ctx, q, r)
		if updErr == nil {
			out = claim

			return nil
		}

		if !errors.Is(updErr, sql.ErrNoRows) {
			return updErr
		}

		return explainRefusedRenewal(ctx, q, r)
	})
	if err != nil {
		return workspace.Claim{}, err
	}

	return out, nil
}

// claimRenewer is the part of the queries that a renewal uses. A test gives
// it a layer that returns a row that the statement would not.
type claimRenewer interface {
	RenewWorkspaceClaim(ctx context.Context, arg db.RenewWorkspaceClaimParams) (db.RenewWorkspaceClaimRow, error)
}

// renewClaimRow runs the renew statement and returns the claim of its row. It
// returns sql.ErrNoRows when the statement changed no row. Defense in depth:
// the statement holds the rules of the renewal in its WHERE clause; this
// function checks the row that came back again, so that a row of another
// principal, a job claim or a claim of another workspace is refused here and
// not relied on from the statement. The error rolls the transaction back.
func renewClaimRow(ctx context.Context, q claimRenewer, r workspace.ClaimRenewal) (workspace.Claim, error) {
	row, err := q.RenewWorkspaceClaim(ctx, db.RenewWorkspaceClaimParams{
		LeaseEndsAtMs: codec.Millis(r.Now.Add(r.Lease)), ID: r.ClaimID, WorkspaceID: r.Workspace.String(),
		Principal: r.Principal, NowMs: codec.Millis(r.Now),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workspace.Claim{}, err
		}

		return workspace.Claim{}, fmt.Errorf("renew claim: %w", err)
	}

	claim, err := codec.ClaimFromRow(codec.ClaimRow(row))
	if err != nil {
		return workspace.Claim{}, err
	}

	if checkErr := workspace.CheckRenewed(&claim, r); checkErr != nil {
		return workspace.Claim{}, checkErr
	}

	return claim, nil
}

// explainRefusedRenewal returns the error for a renewal that changed no row:
// the claim is not on the workspace, or the check of the rules names why the
// statement refused it. It fails closed: a claim that the check accepts,
// although the statement did not renew it, is an ended claim.
func explainRefusedRenewal(ctx context.Context, q *db.Queries, r workspace.ClaimRenewal) error {
	claims, err := readWorkspaceClaims(ctx, q, r.Workspace)
	if err != nil {
		return err
	}

	for i := range claims {
		if claims[i].ID != r.ClaimID {
			continue
		}

		if checkErr := workspace.CheckRenewable(&claims[i], r); checkErr != nil {
			return checkErr
		}

		return fmt.Errorf("%w: claim %s", workspace.ErrClaimEnded, r.ClaimID)
	}

	return fmt.Errorf("%w: claim %s", workspace.ErrClaimNotFound, r.ClaimID)
}

// claimEnd is when and why claims end.
type claimEnd struct {
	at    time.Time
	cause string
}

// endingClaim is an accepted claim that an automatic end closes: its ID, its
// workspace, its job and the name of its workspace.
type endingClaim struct {
	ID          string
	WorkspaceID string
	JobID       string
	Name        string
}

// endJobClaims ends each accepted claim of the job in the namespace ns, in
// the transaction of q, with one audit entry for each. It returns the count.
// The store of jobs calls it in the transaction that moves a job to a
// terminal state, so a claim never outlives its job.
func endJobClaims(
	ctx context.Context, q *db.Queries, ns namespace.Name, jobID string, at time.Time,
) (int, error) {
	rows, err := q.ListJobWorkspaceClaims(ctx, db.ListJobWorkspaceClaimsParams{
		JobID: jobID, Namespace: ns.String(),
	})
	if err != nil {
		return 0, fmt.Errorf("select claims of job: %w", err)
	}

	ending := make([]endingClaim, len(rows))
	for i := range rows {
		ending[i] = endingClaim(rows[i])
	}

	return endClaims(ctx, q, ns, ending, claimEnd{at: at, cause: workspace.ClaimEndedByJob})
}

// endClaims ends each claim of ending that is still accepted and appends one
// audit entry for each end, in the transaction of q.
func endClaims(
	ctx context.Context, q *db.Queries, ns namespace.Name, ending []endingClaim, why claimEnd,
) (int, error) {
	ended := 0

	for i := range ending {
		c := &ending[i]

		n, err := q.EndWorkspaceClaim(ctx, db.EndWorkspaceClaimParams{EndedAtMs: codec.Millis(why.at), ID: c.ID})
		if err != nil {
			return 0, fmt.Errorf("end claim: %w", err)
		}

		if n == 0 {
			continue
		}

		entry, err := workspace.ClaimEndEntry(&workspace.ClaimEnd{
			Namespace: ns, Workspace: workspace.Name(c.Name), ClaimID: c.ID, JobID: c.JobID, Cause: why.cause,
			At: why.at,
		})
		if err != nil {
			return 0, err
		}

		if _, err = appendAuditEntry(ctx, q, entry); err != nil {
			return 0, fmt.Errorf("append audit entry: %w", err)
		}

		ended++
	}

	return ended, nil
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

// DeletedWorkspace implements workspace.ClaimDeleted: it returns the reference
// of the soft-deleted workspace id, with the name that it had when it was
// deleted. A workspace that does not exist, or is not deleted, is not found.
func (s *Store) DeletedWorkspace(ctx context.Context, id workspace.ID) (workspace.Workspace, bool, error) {
	row, err := s.queries.GetDeletedWorkspaceRefByID(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		var none workspace.Workspace

		return none, false, nil
	}

	if err != nil {
		var none workspace.Workspace

		return none, false, fmt.Errorf("select deleted workspace: %w", err)
	}

	name := row.Name
	if row.DeletedName.Valid {
		name = row.DeletedName.String
	}

	var gone workspace.Workspace

	gone.ID = id
	gone.Ref = workspace.Ref{Namespace: namespace.Name(row.Namespace), Name: workspace.Name(name)}

	return gone, true, nil
}
