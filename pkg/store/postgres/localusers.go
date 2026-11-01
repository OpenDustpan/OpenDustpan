package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/localuserstore"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var (
	_ localuser.Store                  = (*Store)(nil)
	_ localuser.PasswordStore          = (*Store)(nil)
	_ localuser.ResetStore             = (*Store)(nil)
	_ principal.SessionGenerationStore = (*Store)(nil)
)

// CreateUser implements localuser.Store.
func (s *Store) CreateUser(ctx context.Context, u localuser.User, e audit.Entry) error {
	if err := u.Validate(); err != nil {
		return err
	}

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.CreateUser(ctx, localUserTx{q: q}, u)
	})
}

// UserByName implements localuser.Store.
func (s *Store) UserByName(ctx context.Context, name string) (localuser.Credential, error) {
	return localuserstore.ByName(ctx, localUserTx{q: s.queries}, name)
}

// UserByPrincipal implements localuser.Store.
func (s *Store) UserByPrincipal(ctx context.Context, principalID string) (localuser.User, error) {
	return localuserstore.ByPrincipal(ctx, localUserTx{q: s.queries}, principalID)
}

// RenameUser implements localuser.Store.
func (s *Store) RenameUser(ctx context.Context, userID, name string, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.Rename(ctx, localUserTx{q: q}, userID, name)
	})
}

// DeleteUser implements localuser.Store.
func (s *Store) DeleteUser(ctx context.Context, userID string, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.Delete(ctx, localUserTx{q: q}, userID)
	})
}

// SetPassword implements localuser.PasswordStore.
func (s *Store) SetPassword(
	ctx context.Context, userID string, h localuser.PasswordHash, c localuser.PasswordChange, e audit.Entry,
) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.SetPassword(ctx, localUserTx{q: q}, userID, localuserstore.PasswordWrite(h, c, e.At))
	})
}

// SetChangeRequired implements localuser.PasswordStore.
func (s *Store) SetChangeRequired(ctx context.Context, userID string, required bool, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.SetChangeRequired(ctx, localUserTx{q: q}, userID, required)
	})
}

// RehashPassword implements localuser.PasswordStore.
func (s *Store) RehashPassword(ctx context.Context, userID string, old, h localuser.PasswordHash, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.Rehash(ctx, localUserTx{q: q}, userID, old, h)
	})
}

// RemovePassword implements localuser.PasswordStore.
func (s *Store) RemovePassword(ctx context.Context, userID string, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.RemovePassword(ctx, localUserTx{q: q}, userID)
	})
}

// SeedDemoPassword implements localuser.PasswordStore.
func (s *Store) SeedDemoPassword(ctx context.Context, h localuser.PasswordHash, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return localuserstore.SeedDemoPassword(ctx, localUserTx{q: q}, h, e.At)
	})
}

// CreateReset implements localuser.ResetStore.
func (s *Store) CreateReset(ctx context.Context, r localuser.Reset, d localuser.Digest, e audit.Entry) error {
	if err := r.Validate(); err != nil {
		return err
	}

	return s.inAuditedTxOf(ctx, &e, func(q *db.Queries) error {
		return localuserstore.CreateReset(ctx, localUserTx{q: q}, r, d, &e)
	})
}

// RedeemReset implements localuser.ResetStore. A code that expired rolls
// back the transaction of the redeem, so a second transaction deletes it.
func (s *Store) RedeemReset(ctx context.Context, rd localuser.Redeem, e audit.Entry) (localuser.Reset, error) {
	var r localuser.Reset

	err := s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		var err error

		r, err = localuserstore.RedeemReset(ctx, localUserTx{q: q}, rd)

		return err
	})
	if localuserstore.Expired(err) {
		return localuser.Reset{}, errors.Join(err, s.deleteExpiredReset(ctx, rd.Digest, rd.At))
	}

	if err != nil {
		return localuser.Reset{}, err
	}

	return r, nil
}

// deleteExpiredReset deletes the code with the digest d when it expired at
// or before at.
func (s *Store) deleteExpiredReset(ctx context.Context, d localuser.Digest, at time.Time) error {
	err := s.inTx(ctx, func(q *db.Queries) error {
		_, err := q.DeleteExpiredLocalUserReset(ctx, db.DeleteExpiredLocalUserResetParams{
			Digest: d.String(), AtMs: codec.Millis(at),
		})

		return err
	})
	if err != nil {
		return fmt.Errorf("delete expired reset code: %w", storeerr.Sentinel(localuser.ErrUnavailable, err))
	}

	return nil
}

// PruneResets implements localuser.ResetStore.
func (s *Store) PruneResets(ctx context.Context, at time.Time) (int64, error) {
	var n int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error

		n, err = q.PruneLocalUserResets(ctx, codec.Millis(at))

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("prune reset codes: %w", storeerr.Sentinel(localuser.ErrUnavailable, err))
	}

	return n, nil
}

// SessionGeneration implements principal.SessionGenerationStore.
func (s *Store) SessionGeneration(ctx context.Context, id string) (int64, error) {
	g, err := s.queries.GetSessionGeneration(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", principal.ErrNotFound, id)
	}

	if err != nil {
		return 0, fmt.Errorf("select session generation: %w", err)
	}

	return g, nil
}

// localUserTx is the localuserstore.Tx of the queries q.
type localUserTx struct {
	q *db.Queries
}

// localUserReadErr maps the no-rows error of the driver to localuserstore.ErrNoRow and
// each other error to localuser.ErrUnavailable, without driver text.
func localUserReadErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return localuserstore.ErrNoRow
	}

	if err != nil {
		return storeerr.Sentinel(localuser.ErrUnavailable, err)
	}

	return nil
}

func (t localUserTx) LockPrincipal(ctx context.Context, id string) (localuserstore.Principal, error) {
	r, err := t.q.GetLocalUserPrincipal(ctx, id)
	if err != nil {
		return localuserstore.Principal{}, localUserReadErr(err)
	}

	return localuserstore.Principal{Kind: r.Kind, Subject: r.Subject, BreakGlass: r.BreakGlass}, nil
}

func (t localUserTx) InstallationKind(ctx context.Context) (string, error) {
	k, err := t.q.GetInstallationKind(ctx)
	if err != nil {
		return "", localUserReadErr(err)
	}

	return k.String, nil
}

func (t localUserTx) PrincipalBySubject(ctx context.Context, subject string) (string, error) {
	p, err := t.q.GetPrincipalBySubject(ctx, subject)
	if err != nil {
		return "", localUserReadErr(err)
	}

	return p.ID, nil
}

func (t localUserTx) CreateUser(ctx context.Context, u localuserstore.User) (int64, error) {
	n, err := t.q.CreateLocalUser(ctx, db.CreateLocalUserParams{
		ID: u.ID, PrincipalID: u.PrincipalID, UserName: u.UserName, CreatedBy: u.CreatedBy, CreatedAtMs: u.CreatedAtMs,
	})

	return n, localUserReadErr(err)
}

func (t localUserTx) CreateDemoUser(ctx context.Context, u localuserstore.User) (int64, error) {
	n, err := t.q.CreateDemoLocalUser(ctx, db.CreateDemoLocalUserParams{
		ID: u.ID, PrincipalID: u.PrincipalID, UserName: u.UserName, CreatedBy: u.CreatedBy, CreatedAtMs: u.CreatedAtMs,
	})

	return n, localUserReadErr(err)
}

func (t localUserTx) CountUserConflicts(ctx context.Context, id, principalID, userName string) (int64, error) {
	n, err := t.q.CountLocalUserConflicts(ctx, db.CountLocalUserConflictsParams{
		ID: id, PrincipalID: principalID, UserName: userName,
	})

	return n, localUserReadErr(err)
}

// userRow returns the shared row of a local user row.
func userRow(r db.LocalUser) localuserstore.User {
	return localuserstore.User{
		ID:                r.ID,
		PrincipalID:       r.PrincipalID,
		UserName:          r.UserName,
		PasswordHash:      r.PasswordHash,
		CreatedBy:         r.CreatedBy,
		PasswordSetAtMs:   r.PasswordSetAtMs,
		CreatedAtMs:       r.CreatedAtMs,
		ChangeRequired:    r.ChangeRequired,
		Demo:              r.Demo,
		SessionGeneration: 0,
	}
}

func (t localUserTx) UserByID(ctx context.Context, id string) (localuserstore.User, error) {
	r, err := t.q.GetLocalUser(ctx, id)
	if err != nil {
		return localuserstore.User{}, localUserReadErr(err)
	}

	return userRow(r), nil
}

func (t localUserTx) UserByName(ctx context.Context, name string) (localuserstore.User, error) {
	r, err := t.q.GetLocalUserByName(ctx, name)
	if err != nil {
		return localuserstore.User{}, localUserReadErr(err)
	}

	u := userRow(db.LocalUser{
		ID:              r.ID,
		PrincipalID:     r.PrincipalID,
		UserName:        r.UserName,
		PasswordHash:    r.PasswordHash,
		PasswordSetAtMs: r.PasswordSetAtMs,
		ChangeRequired:  r.ChangeRequired,
		Demo:            r.Demo,
		CreatedBy:       r.CreatedBy,
		CreatedAtMs:     r.CreatedAtMs,
	})
	u.SessionGeneration = r.SessionGeneration

	return u, nil
}

func (t localUserTx) UserByPrincipal(ctx context.Context, principalID string) (localuserstore.User, error) {
	r, err := t.q.GetLocalUserByPrincipal(ctx, principalID)
	if err != nil {
		return localuserstore.User{}, localUserReadErr(err)
	}

	return userRow(r), nil
}

func (t localUserTx) CountOtherUserName(ctx context.Context, id, userName string) (int64, error) {
	n, err := t.q.CountLocalUserName(ctx, db.CountLocalUserNameParams{UserName: userName, ID: id})

	return n, localUserReadErr(err)
}

func (t localUserTx) RenameUser(ctx context.Context, id, userName string) (int64, error) {
	n, err := t.q.RenameLocalUser(ctx, db.RenameLocalUserParams{UserName: userName, ID: id})

	return n, localUserReadErr(err)
}

func (t localUserTx) DeleteUser(ctx context.Context, id string) (int64, error) {
	n, err := t.q.DeleteLocalUser(ctx, id)

	return n, localUserReadErr(err)
}

func (t localUserTx) DeleteResetsOfUser(ctx context.Context, userID string) error {
	return localUserReadErr(t.q.DeleteLocalUserResets(ctx, userID))
}

func (t localUserTx) SetPassword(ctx context.Context, id string, w localuserstore.Write) (int64, error) {
	n, err := t.q.SetLocalUserPassword(ctx, db.SetLocalUserPasswordParams{
		PasswordHash: w.Hash, SetAtMs: w.SetAtMs, ChangeRequired: w.ChangeRequired, ID: id,
	})

	return n, localUserReadErr(err)
}

func (t localUserTx) SetChangeRequired(ctx context.Context, id string, required bool) (int64, error) {
	n, err := t.q.SetLocalUserChangeRequired(ctx, db.SetLocalUserChangeRequiredParams{
		ChangeRequired: required, ID: id,
	})

	return n, localUserReadErr(err)
}

func (t localUserTx) RehashPassword(ctx context.Context, id, oldHash, newHash string) (int64, error) {
	n, err := t.q.RehashLocalUserPassword(ctx, db.RehashLocalUserPasswordParams{
		NewHash: newHash, ID: id, OldHash: oldHash,
	})

	return n, localUserReadErr(err)
}

func (t localUserTx) BumpSessionGeneration(ctx context.Context, principalID string) (int64, error) {
	n, err := t.q.BumpSessionGeneration(ctx, principalID)

	return n, localUserReadErr(err)
}

func (t localUserTx) RevokeAccessTokens(ctx context.Context, principalID string, atMs int64) (int64, error) {
	n, err := t.q.RevokePrincipalAccessTokens(ctx, db.RevokePrincipalAccessTokensParams{
		RevokedAtMs: atMs, PrincipalID: principalID,
	})

	return n, localUserReadErr(err)
}

func (t localUserTx) CreateReset(ctx context.Context, r localuserstore.Reset) (int64, error) {
	n, err := t.q.CreateLocalUserReset(ctx, db.CreateLocalUserResetParams{
		ID:                 r.ID,
		UserID:             r.UserID,
		PrincipalID:        r.PrincipalID,
		Digest:             r.Digest,
		Purpose:            r.Purpose,
		CreatedBy:          r.CreatedBy,
		Reason:             r.Reason,
		RevokeAccessTokens: r.RevokeAccessTokens,
		CreatedAtMs:        r.CreatedAtMs,
		ExpiresAtMs:        r.ExpiresAtMs,
	})

	return n, localUserReadErr(err)
}

func (t localUserTx) ResetByDigest(ctx context.Context, digest string) (localuserstore.Reset, error) {
	r, err := t.q.GetLocalUserResetByDigest(ctx, digest)
	if err != nil {
		return localuserstore.Reset{}, localUserReadErr(err)
	}

	return localuserstore.Reset{
		ID:                 r.ID,
		UserID:             r.UserID,
		PrincipalID:        r.PrincipalID,
		Digest:             r.Digest,
		Purpose:            r.Purpose,
		CreatedBy:          r.CreatedBy,
		Reason:             r.Reason,
		CreatedAtMs:        r.CreatedAtMs,
		ExpiresAtMs:        r.ExpiresAtMs,
		RevokeAccessTokens: r.RevokeAccessTokens,
	}, nil
}

func (t localUserTx) DeleteReset(ctx context.Context, id string) (int64, error) {
	n, err := t.q.DeleteLocalUserReset(ctx, id)

	return n, localUserReadErr(err)
}
