package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/firstadminstore"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
)

var _ firstadmin.Store = (*Store)(nil)

// State implements firstadmin.Store. It never reads the digest into its answer.
func (s *Store) State(ctx context.Context, now time.Time) (firstadmin.State, error) {
	var state firstadmin.State

	marker, err := s.queries.GetFirstAdministratorMarker(ctx)

	switch {
	case err == nil:
		state.Marker = &firstadmin.Marker{
			MadeAt:      codec.FromMillis(marker.MadeAtMs),
			PrincipalID: marker.PrincipalID,
			GrantID:     marker.GrantID,
			SessionID:   marker.SessionID,
			CreatedBy:   marker.CreatedBy,
		}
	case errors.Is(err, pgx.ErrNoRows):
		// No marker: the route is open.
	default:
		return firstadmin.State{}, fmt.Errorf("select first administrator marker: %w", err)
	}

	code, err := s.queries.GetFirstAdministratorCode(ctx)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return state, nil
	case err != nil:
		return firstadmin.State{}, fmt.Errorf("select first administrator code: %w", err)
	}

	if codec.Millis(now) >= code.ExpiresAtMs {
		return state, nil
	}

	state.Offer = &firstadmin.Offer{
		CreatedAt:   codec.FromMillis(code.CreatedAtMs),
		ExpiresAt:   codec.FromMillis(code.ExpiresAtMs),
		PrincipalID: code.PrincipalID,
		CreatedBy:   code.CreatedBy,
		Reason:      code.Reason,
	}

	return state, nil
}

// Offer implements firstadmin.Store.
func (s *Store) Offer(ctx context.Context, in firstadmin.OfferInput, e audit.Entry) (firstadmin.OfferResult, error) {
	if err := firstadminstore.CheckOffer(in); err != nil {
		return firstadmin.OfferResult{}, err
	}

	var result firstadmin.OfferResult

	err := s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		if err := requireNoMarker(ctx, q); err != nil {
			return err
		}

		live, err := readFirstAdminPrincipal(ctx, q, in.PrincipalID)
		if err != nil {
			return err
		}

		if !live.found {
			return firstadmin.ErrPrincipalNotLive
		}

		if checkErr := firstadmin.CheckPrincipal(live.p, live.tempAdmin); checkErr != nil {
			return checkErr
		}

		_, codeErr := q.GetFirstAdministratorCode(ctx)

		switch {
		case codeErr == nil:
			result.Replaced = true
		case errors.Is(codeErr, pgx.ErrNoRows):
			// No open code: the offer replaces nothing.
		default:
			return fmt.Errorf("select first administrator code: %w", codeErr)
		}

		created := codec.Millis(in.Now)
		expires := created + firstadmin.LifetimeMs

		replace := db.ReplaceFirstAdministratorCodeParams{
			PrincipalID: in.PrincipalID,
			Digest:      in.Digest,
			CreatedBy:   in.CreatedBy,
			Reason:      in.Reason,
			CreatedAtMs: created,
			ExpiresAtMs: expires,
		}
		if err := q.ReplaceFirstAdministratorCode(ctx, replace); err != nil {
			return fmt.Errorf("replace first administrator code: %w", err)
		}

		result.ExpiresAt = codec.FromMillis(expires)

		return nil
	})
	if err != nil {
		return firstadmin.OfferResult{}, err
	}

	return result, nil
}

// Redeem implements firstadmin.Store. The checks, the grant, the spend of the
// code, the marker and the audit entry run in one transaction.
func (s *Store) Redeem(ctx context.Context, in firstadmin.RedeemInput, e audit.Entry) (firstadmin.Result, error) {
	if err := firstadminstore.CheckRedeem(in); err != nil {
		return firstadmin.Result{}, err
	}

	var result firstadmin.Result

	err := s.inAuditedTxOf(ctx, &e, func(tx serverAuditTx) error {
		q := tx.q

		facts, code, err := readRedeemFacts(ctx, q, in)
		if err != nil {
			return err
		}

		if checkErr := firstadmin.CheckRedeem(facts); checkErr != nil {
			return checkErr
		}

		if err := spendCode(ctx, q, in, code); err != nil {
			return err
		}

		// The reason and the creator come from the code row of this
		// transaction, never from the entry of the caller: an offer that
		// replaced the code after the caller read the state cannot leave its
		// own reason in the entry.
		e = firstadmin.StampRedeemEntry(e, code.CreatedBy, code.Reason)

		result = firstadmin.Result{
			MadeAt:      codec.FromMillis(codec.Millis(in.Now)),
			PrincipalID: in.PrincipalID,
			GrantID:     in.GrantID,
		}

		return nil
	})
	if err != nil {
		return firstadmin.Result{}, err
	}

	return result, nil
}

// requireNoMarker returns firstadmin.ErrAlreadyMade when the marker is stored.
func requireNoMarker(ctx context.Context, q *db.Queries) error {
	_, err := q.GetFirstAdministratorMarker(ctx)

	switch {
	case err == nil:
		return firstadmin.ErrAlreadyMade
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	default:
		return fmt.Errorf("select first administrator marker: %w", err)
	}
}

// livePrincipal is the live row of a principal and what the check of the
// first administrator needs beside it.
type livePrincipal struct {
	p         principal.Principal
	tempAdmin bool
	found     bool
}

// readFirstAdminPrincipal reads the live row of the principal and whether it
// has a temporary administrator record, in the transaction of q. A principal
// that is deleted or does not exist gives found false.
func readFirstAdminPrincipal(ctx context.Context, q *db.Queries, id string) (livePrincipal, error) {
	var live livePrincipal

	row, err := q.GetPrincipal(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return live, nil
	}

	if err != nil {
		return live, fmt.Errorf("select principal: %w", err)
	}

	live.p = principalOf(principalRow(row))
	live.found = true

	_, tempErr := q.GetTempAdmin(ctx, id)

	switch {
	case tempErr == nil:
		live.tempAdmin = true
	case errors.Is(tempErr, pgx.ErrNoRows):
		// No record: the person is no temporary administrator.
	default:
		return livePrincipal{}, fmt.Errorf("select temporary administrator: %w", tempErr)
	}

	return live, nil
}

// readRedeemFacts reads the rows that CheckRedeem needs in the transaction of
// q, and the code row for its creator.
func readRedeemFacts(
	ctx context.Context, q *db.Queries, in firstadmin.RedeemInput,
) (firstadmin.Facts, db.GetFirstAdministratorCodeRow, error) {
	var facts firstadmin.Facts

	facts.Now = in.Now
	// The sign-in time is the stored one: SetSession reads it from the session row.
	facts.InDigest = in.Digest
	facts.InPrincipal = in.PrincipalID
	facts.Window = in.Window

	var code db.GetFirstAdministratorCodeRow

	markerErr := requireNoMarker(ctx, q)
	if errors.Is(markerErr, firstadmin.ErrAlreadyMade) {
		facts.MarkerSet = true

		return facts, code, nil
	}

	if markerErr != nil {
		return facts, code, markerErr
	}

	code, err := q.GetFirstAdministratorCode(ctx)

	switch {
	case err == nil:
		facts.HaveCode = true
		facts.CodeExpires = codec.FromMillis(code.ExpiresAtMs)
		facts.CodePrin = code.PrincipalID
		facts.CodeDigest = code.Digest
	case errors.Is(err, pgx.ErrNoRows):
		// No open code: CheckRedeem answers ErrCodeInvalid.
	default:
		return facts, code, fmt.Errorf("select first administrator code: %w", err)
	}

	live, err := readFirstAdminPrincipal(ctx, q, in.PrincipalID)
	if err != nil {
		return facts, code, err
	}

	facts.Principal = live.p
	facts.TempAdmin = live.tempAdmin
	facts.HavePrin = live.found

	if err := readRedeemSession(ctx, q, in.SessionID, &facts); err != nil {
		return facts, code, err
	}

	return facts, code, nil
}

// readRedeemSession reads the session row by its ID and the session policy in
// the transaction of q, and puts them in facts. A session that does not exist
// leaves HaveSession false, and CheckRedeem refuses it. The row, not the
// caller, gives the owner, the status and the sign-in time.
func readRedeemSession(ctx context.Context, q *db.Queries, id string, facts *firstadmin.Facts) error {
	row, err := q.GetSessionForShare(ctx, id)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("select session: %w", err)
	}

	policy, err := q.GetSessionPolicy(ctx)
	if err != nil {
		return fmt.Errorf("select session policy: %w", err)
	}

	pol := sessionPolicyOf(policy)

	// The stored policy limits the freshness window. The caller may give a
	// smaller window, never a larger one.
	facts.Window = min(facts.Window, pol.TokenFreshness)
	facts.SetSession(sessionOfRow(db.GetSessionRow(row)), pol.Limits)

	return nil
}

// spendCode makes the grant, spends the code row and stores the marker in the
// transaction of q. The delete matches the digest, the principal and the time
// that CheckRedeem checked.
func spendCode(
	ctx context.Context,
	q *db.Queries,
	in firstadmin.RedeemInput,
	code db.GetFirstAdministratorCodeRow,
) error {
	grant := access.Grant{
		CreatedAt: in.Now,
		Condition: nil,
		Object:    identity.ServerObject(),
		ID:        in.GrantID,
		Role:      firstadmin.Role,
		CreatedBy: code.CreatedBy,
		Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: in.PrincipalID},
	}

	if grantErr := insertGrant(ctx, q, grant); errors.Is(grantErr, access.ErrAlreadyExists) {
		return firstadmin.ErrGrantExists
	} else if grantErr != nil {
		return grantErr
	}

	spent, err := q.DeleteFirstAdministratorCode(ctx, db.DeleteFirstAdministratorCodeParams{
		Digest: in.Digest, PrincipalID: in.PrincipalID, NowMs: codec.Millis(in.Now),
	})
	if err != nil {
		return fmt.Errorf("spend first administrator code: %w", err)
	}

	// Defense in depth: the read of the code row above already found it in this
	// transaction, and CheckRedeem compared the digest, the principal and the
	// time. The delete repeats the three checks in its WHERE clause. This
	// check stays so that a delete that matches no row never gives a grant,
	// whatever the isolation of the dialect is.
	if spent != 1 {
		return firstadmin.ErrCodeInvalid
	}

	marker := db.InsertFirstAdministratorMarkerParams{
		PrincipalID: in.PrincipalID,
		GrantID:     in.GrantID,
		SessionID:   in.SessionID,
		CreatedBy:   code.CreatedBy,
		MadeAtMs:    codec.Millis(in.Now),
	}
	if err := q.InsertFirstAdministratorMarker(ctx, marker); err != nil {
		if isUniqueViolation(err) {
			// Another transaction stored the marker first: the route is closed.
			return firstadmin.ErrAlreadyMade
		}

		return fmt.Errorf("insert first administrator marker: %w", err)
	}

	return nil
}
