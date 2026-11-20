package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/passkeystore"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/internal/store/storeerr"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
)

var _ mfa.PasskeyStore = (*Store)(nil)

// AddPasskey implements mfa.PasskeyStore.
func (s *Store) AddPasskey(ctx context.Context, p mfa.Passkey, e audit.Entry) error {
	if err := p.Validate(); err != nil {
		return err
	}

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return passkeystore.AddPasskey(ctx, passkeyTx{q: q}, p)
	})
}

// Passkeys implements mfa.PasskeyStore.
func (s *Store) Passkeys(ctx context.Context, personID string) ([]mfa.Passkey, error) {
	return passkeystore.Passkeys(ctx, passkeyTx{q: s.queries}, personID)
}

// PasskeyByCredentialID implements mfa.PasskeyStore.
func (s *Store) PasskeyByCredentialID(ctx context.Context, id []byte) (mfa.Passkey, error) {
	return passkeystore.ByCredentialID(ctx, passkeyTx{q: s.queries}, id)
}

// errPasskeyCounterRegressed reports a passkey use that found a counter
// regression. The first transaction of RecordUse returns it to roll back, so
// the suspension and its entry run in the locked transaction that follows.
var errPasskeyCounterRegressed = errors.New("passkey counter regressed")

// RecordUse implements mfa.PasskeyStore. A regression suspends the passkey
// and appends its entry in one transaction, and then returns
// mfa.ErrCounterRegressed.
//
// A use with no regression runs in one transaction that updates the counter
// and takes no lock, so a sign-in never waits for the installation-wide lock
// of @server. When that transaction finds a regression, it rolls back, and a
// second transaction takes the lock of @server first and then suspends the
// passkey and appends the entry. It returns nil when that second transaction
// finds no regression, because a concurrent use moved the counter first.
func (s *Store) RecordUse(ctx context.Context, u mfa.PasskeyUse, regressed audit.Entry) error {
	if err := validServerEntry(regressed); err != nil {
		return err
	}

	err := s.inTx(ctx, func(q *db.Queries) error {
		suspended, useErr := passkeystore.RecordUse(ctx, passkeyTx{q: q}, u)
		if useErr != nil {
			return useErr
		}

		if suspended {
			return errPasskeyCounterRegressed
		}

		return nil
	})
	if err == nil {
		return nil
	}

	if !errors.Is(err, errPasskeyCounterRegressed) {
		return err
	}

	var suspended bool

	err = s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		// The regression check depends on the stored row, so read it again
		// under the lock.
		var useErr error

		suspended, useErr = passkeystore.RecordUse(ctx, passkeyTx(tx), u)
		if useErr != nil {
			return useErr
		}

		if !suspended {
			return nil
		}

		if _, appendErr := appendServerEntry(ctx, tx, regressed); appendErr != nil {
			return fmt.Errorf("append audit entry: %w", appendErr)
		}

		return nil
	})
	if err != nil {
		return err
	}

	if !suspended {
		return nil
	}

	return mfa.ErrCounterRegressed
}

// RemovePasskey implements mfa.PasskeyStore.
func (s *Store) RemovePasskey(ctx context.Context, personID, id string, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return passkeystore.RemovePasskey(ctx, passkeyTx{q: q}, personID, id)
	})
}

// CreateChallenge implements mfa.PasskeyStore.
func (s *Store) CreateChallenge(ctx context.Context, c mfa.Challenge, d mfa.Digest) error {
	if err := c.Validate(); err != nil {
		return err
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		return passkeystore.CreateChallenge(ctx, passkeyTx{q: q}, c, d)
	})
}

// TakeChallenge implements mfa.PasskeyStore. The delete of a challenge that
// expired commits too.
func (s *Store) TakeChallenge(ctx context.Context, d mfa.Digest, at time.Time) (mfa.Challenge, error) {
	var taken passkeystore.Taken

	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error

		taken, err = passkeystore.TakeChallenge(ctx, passkeyTx{q: q}, d, at)

		return err
	})
	if err != nil {
		return mfa.Challenge{}, err
	}

	if taken.Refused != nil {
		return mfa.Challenge{}, taken.Refused
	}

	return taken.Challenge, nil
}

// passkeyTx is the passkeystore.Tx of the queries q.
type passkeyTx struct {
	q *db.Queries
}

// passkeyErr maps the no-rows error of the driver to passkeystore.ErrNoRow
// and each other error to mfa.ErrUnavailable, without driver text.
func passkeyErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return passkeystore.ErrNoRow
	}

	if err != nil {
		return storeerr.Sentinel(mfa.ErrUnavailable, err)
	}

	return nil
}

func (t passkeyTx) LockPrincipal(ctx context.Context, id string) (passkeystore.Principal, error) {
	r, err := t.q.GetLocalUserPrincipal(ctx, id)
	if err != nil {
		return passkeystore.Principal{}, passkeyErr(err)
	}

	return passkeystore.Principal{Kind: r.Kind, BreakGlass: r.BreakGlass}, nil
}

func (t passkeyTx) LocalUserOf(ctx context.Context, principalID string) (passkeystore.LocalUser, error) {
	r, err := t.q.GetLocalUserByPrincipal(ctx, principalID)
	if err != nil {
		return passkeystore.LocalUser{}, passkeyErr(err)
	}

	return passkeystore.LocalUser{PasswordHash: r.PasswordHash, Demo: r.Demo}, nil
}

func (t passkeyTx) CountPasskeys(ctx context.Context, principalID string) (int64, error) {
	n, err := t.q.CountPasskeys(ctx, principalID)

	return n, passkeyErr(err)
}

func (t passkeyTx) CountLivePasskeys(ctx context.Context, principalID string) (int64, error) {
	n, err := t.q.CountLivePasskeys(ctx, principalID)

	return n, passkeyErr(err)
}

func (t passkeyTx) CreatePasskey(ctx context.Context, r passkeystore.Row) (int64, error) {
	n, err := t.q.CreatePasskey(ctx, db.CreatePasskeyParams{
		ID:             r.ID,
		PrincipalID:    r.PrincipalID,
		Name:           r.Name,
		CredentialID:   r.CredentialID,
		PublicKey:      r.PublicKey,
		Transports:     r.Transports,
		Aaguid:         r.AAGUID,
		SignCount:      r.SignCount,
		BackupEligible: r.BackupEligible,
		CreatedAtMs:    r.CreatedAtMs,
	})

	return n, passkeyErr(err)
}

// passkeyRow returns the shared row of a passkey row.
func passkeyRow(r db.Passkey) passkeystore.Row {
	return passkeystore.Row{
		ID:             r.ID,
		PrincipalID:    r.PrincipalID,
		Name:           r.Name,
		Transports:     r.Transports,
		CredentialID:   r.CredentialID,
		PublicKey:      r.PublicKey,
		AAGUID:         r.Aaguid,
		SignCount:      r.SignCount,
		CreatedAtMs:    r.CreatedAtMs,
		LastUsedAtMs:   r.LastUsedAtMs,
		SuspendedAtMs:  r.SuspendedAtMs,
		BackupEligible: r.BackupEligible,
	}
}

func (t passkeyTx) PasskeyByID(ctx context.Context, id string) (passkeystore.Row, error) {
	r, err := t.q.GetPasskey(ctx, id)
	if err != nil {
		return passkeystore.Row{}, passkeyErr(err)
	}

	return passkeyRow(r), nil
}

func (t passkeyTx) PasskeyByCredentialID(ctx context.Context, credentialID []byte) (passkeystore.Row, error) {
	r, err := t.q.GetPasskeyByCredentialID(ctx, credentialID)
	if err != nil {
		return passkeystore.Row{}, passkeyErr(err)
	}

	return passkeyRow(r), nil
}

func (t passkeyTx) ListPasskeys(ctx context.Context, principalID string) ([]passkeystore.Row, error) {
	rows, err := t.q.ListPasskeys(ctx, principalID)
	if err != nil {
		return nil, passkeyErr(err)
	}

	out := make([]passkeystore.Row, 0, len(rows))
	for i := range rows {
		out = append(out, passkeyRow(rows[i]))
	}

	return out, nil
}

func (t passkeyTx) DeletePasskey(ctx context.Context, id, principalID string) (int64, error) {
	n, err := t.q.DeletePasskey(ctx, db.DeletePasskeyParams{ID: id, PrincipalID: principalID})

	return n, passkeyErr(err)
}

func (t passkeyTx) RecordUse(ctx context.Context, id string, signCount, atMs int64) (int64, error) {
	n, err := t.q.RecordPasskeyUse(ctx, db.RecordPasskeyUseParams{SignCount: signCount, AtMs: atMs, ID: id})

	return n, passkeyErr(err)
}

func (t passkeyTx) Suspend(ctx context.Context, id string, atMs int64) (int64, error) {
	n, err := t.q.SuspendPasskey(ctx, db.SuspendPasskeyParams{AtMs: atMs, ID: id})

	return n, passkeyErr(err)
}

func (t passkeyTx) CreateChallenge(ctx context.Context, r passkeystore.ChallengeRow) (int64, error) {
	n, err := t.q.CreatePasskeyChallenge(ctx, db.CreatePasskeyChallengeParams{
		Digest:      r.Digest,
		Kind:        r.Kind,
		PrincipalID: r.PrincipalID,
		PendingID:   r.PendingID,
		SessionID:   r.SessionID,
		Source:      r.Source,
		SourceGroup: r.SourceGroup,
		CreatedAtMs: r.CreatedAtMs,
		ExpiresAtMs: r.ExpiresAtMs,
	})

	return n, passkeyErr(err)
}

func (t passkeyTx) CountOpenChallenges(ctx context.Context, kind string, atMs int64) (int64, error) {
	n, err := t.q.CountOpenPasskeyChallenges(ctx, db.CountOpenPasskeyChallengesParams{Kind: kind, ExpiresAtMs: atMs})

	return n, passkeyErr(err)
}

func (t passkeyTx) CountOpenChallengesBySource(
	ctx context.Context, kind, source string, atMs int64,
) (int64, error) {
	n, err := t.q.CountOpenPasskeyChallengesBySource(ctx, db.CountOpenPasskeyChallengesBySourceParams{
		Kind: kind, Source: source, ExpiresAtMs: atMs,
	})

	return n, passkeyErr(err)
}

func (t passkeyTx) LockSignInChallenges(ctx context.Context) error {
	return passkeyErr(t.q.LockPasskeySignInChallenges(ctx))
}

func (t passkeyTx) CountOpenChallengesBySourceGroup(
	ctx context.Context, kind, group string, atMs int64,
) (int64, error) {
	n, err := t.q.CountOpenPasskeyChallengesBySourceGroup(ctx, db.CountOpenPasskeyChallengesBySourceGroupParams{
		Kind: kind, SourceGroup: group, ExpiresAtMs: atMs,
	})

	return n, passkeyErr(err)
}

func (t passkeyTx) DeleteExpiredChallenges(ctx context.Context, atMs int64) (int64, error) {
	n, err := t.q.DeleteExpiredPasskeyChallenges(ctx, atMs)

	return n, passkeyErr(err)
}

func (t passkeyTx) TakeChallenge(ctx context.Context, digest string) (passkeystore.ChallengeRow, error) {
	r, err := t.q.TakePasskeyChallenge(ctx, digest)
	if err != nil {
		return passkeystore.ChallengeRow{}, passkeyErr(err)
	}

	return passkeystore.ChallengeRow{
		Digest:      r.Digest,
		Kind:        r.Kind,
		PrincipalID: r.PrincipalID,
		PendingID:   r.PendingID,
		SessionID:   r.SessionID,
		Source:      r.Source,
		SourceGroup: r.SourceGroup,
		CreatedAtMs: r.CreatedAtMs,
		ExpiresAtMs: r.ExpiresAtMs,
	}, nil
}
