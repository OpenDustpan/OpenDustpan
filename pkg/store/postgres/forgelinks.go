package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/store/internal/forgelinkstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// ForgeLinks returns the store of the links from accounts of a Git host to
// principals, and of the requests that make them. The rows belong to the
// installation, so a write takes no fence. A write that changes access appends
// its audit entry in the scope @server in its own transaction.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) ForgeLinks() forgelink.Store {
	return forgeLinks{s: s}
}

// forgeLinks is forgelink.Store over the database of a Store.
type forgeLinks struct {
	s *Store
}

var _ forgelink.Store = forgeLinks{s: nil}

// CreateRequest implements forgelink.Store.
func (f forgeLinks) CreateRequest(ctx context.Context, r forgelink.Request) error {
	if err := forgelinkstore.CheckNewRequest(r); err != nil {
		return err
	}

	return f.s.inTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteExpiredForgeLinkRequestsOf(ctx, db.DeleteExpiredForgeLinkRequestsOfParams{
			PrincipalID: r.Principal, BeforeMs: r.CreatedAt.UnixMilli(),
		}); err != nil {
			return fmt.Errorf("delete expired link requests: %w", err)
		}

		open, err := q.CountOpenForgeLinkRequests(ctx, db.CountOpenForgeLinkRequestsParams{
			PrincipalID: r.Principal, NowMs: r.CreatedAt.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("count open link requests: %w", err)
		}

		if open >= forgelink.MaxOpenRequests {
			return forgelink.ErrLimit
		}

		row := forgelinkstore.RequestRowOf(r)

		err = q.InsertForgeLinkRequest(ctx, db.InsertForgeLinkRequestParams{
			TokenSha256: row.TokenSha256, PrincipalID: row.PrincipalID, CreatedAtMs: row.CreatedAtMs,
			ExpiresAtMs: row.ExpiresAtMs,
		})
		if isUniqueViolation(err) {
			return forgelink.ErrConflict
		}

		if err != nil {
			return fmt.Errorf("insert link request: %w", err)
		}

		return nil
	})
}

// GetRequest implements forgelink.Store.
func (f forgeLinks) GetRequest(ctx context.Context, hash string) (forgelink.Request, error) {
	row, err := f.s.queries.GetForgeLinkRequest(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return forgelink.Request{}, forgelink.ErrNotFound
	}

	if err != nil {
		return forgelink.Request{}, fmt.Errorf("select link request: %w", err)
	}

	return forgelinkstore.Request(forgelinkstore.RequestRow(row))
}

// ProveRequest implements forgelink.Store.
func (f forgeLinks) ProveRequest(
	ctx context.Context, hash string, p forgelink.Proof, now time.Time,
) (forgelink.Request, error) {
	if err := forgelinkstore.CheckProof(p); err != nil {
		return forgelink.Request{}, err
	}

	var out forgelink.Request

	err := f.s.inTx(ctx, func(q *db.Queries) error {
		var proveErr error

		out, proveErr = forgelinkstore.ApplyProof(ctx, requestTx{q: q}, hash, p, now)

		return proveErr
	})
	if err != nil {
		return forgelink.Request{}, err
	}

	return out, nil
}

// AcceptRequest implements forgelink.Store.
func (f forgeLinks) AcceptRequest(
	ctx context.Context, hash, principal, previous string, now time.Time, e audit.Entry,
) (forgelink.Link, string, error) {
	var (
		link     forgelink.Link
		replaced string
	)

	err := f.s.inAuditedTxOf(ctx, &e, func(q *db.Queries) error {
		var acceptErr error

		link, replaced, acceptErr = forgelinkstore.ApplyAccept(
			ctx,
			linkTx{requestTx: requestTx{q: q}},
			hash,
			principal,
			previous,
			now,
		)

		return acceptErr
	})
	if err != nil {
		return forgelink.Link{}, "", err
	}

	return link, replaced, nil
}

// DeleteLink implements forgelink.Store.
func (f forgeLinks) DeleteLink(ctx context.Context, a forgelink.Account, e audit.Entry) error {
	if err := forgelinkstore.CheckAccount(a); err != nil {
		return err
	}

	return f.s.inAuditedTxOf(ctx, &e, func(q *db.Queries) error {
		n, err := q.DeleteForgeAccountLink(ctx, db.DeleteForgeAccountLinkParams{
			Kind: string(a.Kind), Host: a.Host, AccountID: a.ID,
		})
		if err != nil {
			return fmt.Errorf("delete link: %w", err)
		}

		if n == 0 {
			return forgelink.ErrNotFound
		}

		return nil
	})
}

// LinkOf implements forgelink.Store.
func (f forgeLinks) LinkOf(ctx context.Context, a forgelink.Account) (forgelink.Link, error) {
	if err := forgelinkstore.CheckAccount(a); err != nil {
		return forgelink.Link{}, err
	}

	row, err := f.s.queries.GetForgeAccountLink(ctx, db.GetForgeAccountLinkParams{
		Kind: string(a.Kind), Host: a.Host, AccountID: a.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return forgelink.Link{}, forgelink.ErrNotFound
	}

	if err != nil {
		return forgelink.Link{}, fmt.Errorf("select link: %w", err)
	}

	return forgelinkstore.Link(forgelinkstore.LinkRow(row))
}

// LinksOf implements forgelink.Store.
func (f forgeLinks) LinksOf(ctx context.Context, principal string) ([]forgelink.Link, error) {
	rows, err := f.s.queries.ListForgeAccountLinksOf(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("select links of a principal: %w", err)
	}

	stored := make([]forgelinkstore.LinkRow, 0, len(rows))
	for i := range rows {
		stored = append(stored, forgelinkstore.LinkRow(rows[i]))
	}

	return forgelinkstore.Links(stored)
}

// requestTx is forgelinkstore.RequestTx over the queries of one transaction.
type requestTx struct {
	q *db.Queries
}

var _ forgelinkstore.RequestTx = requestTx{q: nil}

// Request implements forgelinkstore.RequestTx.
func (x requestTx) Request(ctx context.Context, hash string) (forgelinkstore.RequestRow, bool, error) {
	var none forgelinkstore.RequestRow

	row, err := x.q.GetForgeLinkRequest(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select link request: %w", err)
	}

	return forgelinkstore.RequestRow(row), true, nil
}

// ProveRequest implements forgelinkstore.RequestTx.
func (x requestTx) ProveRequest(
	ctx context.Context, hash string, p forgelinkstore.ProofRow, nowMs int64,
) (bool, error) {
	n, err := x.q.ProveForgeLinkRequest(ctx, db.ProveForgeLinkRequestParams{
		ProvedKind: p.Kind, ProvedHost: p.Host, ProvedAccountID: p.AccountID, ProvedLogin: p.Login,
		ProvedDeliveryID: p.DeliveryID, ProvedAtMs: nowMs, TokenSha256: hash, NowMs: nowMs,
	})
	if err != nil {
		return false, fmt.Errorf("prove link request: %w", err)
	}

	return n > 0, nil
}

// linkTx is forgelinkstore.Tx over the queries of one transaction.
type linkTx struct {
	requestTx
}

var _ forgelinkstore.Tx = linkTx{requestTx: requestTx{q: nil}}

// Link implements forgelinkstore.Tx.
func (x linkTx) Link(ctx context.Context, a forgelink.Account) (forgelinkstore.LinkRow, bool, error) {
	var none forgelinkstore.LinkRow

	row, err := x.q.GetForgeAccountLink(ctx, db.GetForgeAccountLinkParams{
		Kind: string(a.Kind), Host: a.Host, AccountID: a.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select link: %w", err)
	}

	return forgelinkstore.LinkRow(row), true, nil
}

// InsertLink implements forgelinkstore.Tx.
func (x linkTx) InsertLink(ctx context.Context, r forgelinkstore.LinkRow) error {
	err := x.q.InsertForgeAccountLink(ctx, db.InsertForgeAccountLinkParams{
		Kind: r.Kind, Host: r.Host, AccountID: r.AccountID, Login: r.Login, PrincipalID: r.PrincipalID,
		LinkedAtMs: r.LinkedAtMs,
	})
	if isUniqueViolation(err) {
		return forgelink.ErrConflict
	}

	if err != nil {
		return fmt.Errorf("insert link: %w", err)
	}

	return nil
}

// ReplaceLink implements forgelinkstore.Tx.
func (x linkTx) ReplaceLink(ctx context.Context, r forgelinkstore.LinkRow, previous string) (bool, error) {
	n, err := x.q.ReplaceForgeAccountLink(ctx, db.ReplaceForgeAccountLinkParams{
		Login: r.Login, PrincipalID: r.PrincipalID, LinkedAtMs: r.LinkedAtMs, Kind: r.Kind, Host: r.Host,
		AccountID: r.AccountID, PreviousPrincipalID: previous,
	})
	if err != nil {
		return false, fmt.Errorf("replace link: %w", err)
	}

	return n > 0, nil
}

// UseRequest implements forgelinkstore.Tx.
func (x linkTx) UseRequest(ctx context.Context, hash, principal string, atMs int64) (bool, error) {
	n, err := x.q.UseForgeLinkRequest(ctx, db.UseForgeLinkRequestParams{
		UsedAtMs: atMs, TokenSha256: hash, PrincipalID: principal,
	})
	if err != nil {
		return false, fmt.Errorf("use link request: %w", err)
	}

	return n > 0, nil
}
