package accesstoken

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// MakerChecker decides whether the maker of a service token still has an
// act-as path to the principal of the token. access.Service meets it.
type MakerChecker interface {
	// CanActAs reports whether the subject maker holds
	// principal:manage-tokens on the principal principalID, through any
	// grant, directly or through a group, or as a bootstrap admin. A maker
	// that is not a principal any more, or that is disabled, has no path.
	// An error means that it could not decide.
	CanActAs(ctx context.Context, maker, principalID string) (bool, error)
}

// Cause names the committed change after which the server checks the makers
// of the service tokens: the subject of the caller of the change, its action
// and its audit object. The audit entry of each revocation names it.
type Cause struct {
	Subject string
	Action  string
	Object  string
}

// MakerSubject is the subject of the audit entry of a revocation of a service
// token whose maker lost the act-as path. The server does it, not a person,
// so the entry names the change that caused it in its changes. It starts
// with '@', which no subject of a principal holds.
const MakerSubject = SweepSubject

// The reasons of the audit entry of a revocation of a service token whose
// maker lost the act-as path.
const (
	// ReasonMakerLostPath is the reason of a revocation after a change.
	ReasonMakerLostPath = "the maker of the service token has no act-as path to its principal"
	// ReasonMakerLostPathAtUse is the reason of a revocation at the use of
	// the token, when no change before it revoked the token.
	ReasonMakerLostPathAtUse = "the maker of the service token has no act-as path to its principal at its use"
)

// The keys of the changes of the audit entry of a revocation of a service
// token whose maker lost the act-as path.
const (
	changeMaker    = "maker"
	changeCause    = "cause"
	changeCausedBy = "causedBy"
)

// RevokeResult lists the service tokens that a check of the makers revoked,
// by their IDs.
type RevokeResult struct {
	Revoked []string
}

// RevokeLostMakers revokes each active service token whose maker has no
// act-as path to the principal of the token any more, after the committed
// change cause. Each revocation writes its audit entry, with the subject
// MakerSubject, the reason ReasonMakerLostPath and the changes "maker",
// "cause" and "causedBy", in the transaction of the revocation. A token that
// another write ended first is not an error. A maker that the checker could
// not decide keeps its tokens here, and RevokeLostMakers returns the error
// with the result: the check at the use of each such token refuses it (fail
// closed). With no MakerChecker it does nothing.
func (s *Service) RevokeLostMakers(ctx context.Context, cause Cause) (RevokeResult, error) {
	out := RevokeResult{Revoked: nil}

	if s.makers == nil {
		return out, nil
	}

	// The check runs after a committed change. A request cache in ctx holds
	// the groups and the roles that the route check of the same request read
	// before the change. No caller may pass it: the service drops it here,
	// whatever the MakerChecker does. Defense in depth: access.Service.CanActAs
	// drops it too, and this guard does not rely on that.
	ctx = access.WithoutRequestCache(ctx)

	decided := map[makerKey]bool{}

	var errs []error

	q := Query{
		At: s.Now(), AfterCreatedAt: time.Time{}, PrincipalID: "", AfterID: "",
		Filter: FilterActive, Limit: MaxLimit, HasAfter: false,
	}

	for {
		rows, _, err := s.store.ListTokens(ctx, q)
		if err != nil {
			return out, errors.Join(append(errs, fmt.Errorf("list the active access tokens: %w", err))...)
		}

		for i := range rows {
			revoked, err := s.checkMakerOf(ctx, rows[i], cause, ReasonMakerLostPath, decided)
			if err != nil {
				errs = append(errs, err)
			}

			if revoked {
				out.Revoked = append(out.Revoked, rows[i].ID)
			}
		}

		if len(rows) < q.Limit {
			return out, errors.Join(errs...)
		}

		last := rows[len(rows)-1]
		q.AfterCreatedAt, q.AfterID, q.HasAfter = last.CreatedAt, last.ID, true
	}
}

// makerKey is one maker and one principal, so a check decides each pair
// once.
type makerKey struct {
	maker     string
	principal string
}

// checkMakerOf revokes t when it is a service token whose maker has no act-as
// path to its principal, with the reason and the cause of the entry. It
// reports whether it revoked t. It reads decided before the checker, and
// stores each answer in it.
func (s *Service) checkMakerOf(
	ctx context.Context, t Token, cause Cause, reason string, decided map[makerKey]bool,
) (bool, error) {
	if t.Kind != KindService {
		return false, nil
	}

	key := makerKey{maker: t.CreatedBy, principal: t.PrincipalID}

	can, known := decided[key]
	if !known {
		var err error

		can, err = s.makers.CanActAs(ctx, t.CreatedBy, t.PrincipalID)
		if err != nil {
			return false, fmt.Errorf("check the maker of access token %q: %w", t.ID, err)
		}

		decided[key] = can
	}

	if can {
		return false, nil
	}

	err := s.store.RevokeToken(ctx, t.ID, lostMakerEntry(s.Now(), t, cause, reason))

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrEnded), errors.Is(err, ErrNotFound):
		// Another write ended the token first: it does not act any more.
		return false, nil
	default:
		return false, fmt.Errorf("revoke access token %q of a maker with no act-as path: %w", t.ID, err)
	}
}

// lostMakerEntry returns the audit entry of the revocation of t at the time
// at, after cause, with reason.
func lostMakerEntry(at time.Time, t Token, cause Cause, reason string) audit.Entry {
	e := SweepEntry()
	e.Subject = MakerSubject
	e.Action = string(identity.ActionPrincipalManageTokens)
	e.Reason = reason
	e.Changes = []audit.Change{
		{Key: changeCause, Old: nil, New: cause.Action + " " + cause.Object, Layer: "", TakesEffect: ""},
		{Key: changeCausedBy, Old: nil, New: cause.Subject, Layer: "", TakesEffect: ""},
		{Key: changeMaker, Old: nil, New: t.CreatedBy, Layer: "", TakesEffect: ""},
	}

	return change(e, at, TokenRef(t.PrincipalID, t.ID))
}

// useCause returns the cause of a check at the use of the token t.
func useCause(t Token) Cause {
	return Cause{Subject: MakerSubject, Action: "use", Object: TokenRef(t.PrincipalID, t.ID)}
}

// checkMakerAtUse refuses a service token whose maker has no act-as path to
// its principal, and revokes it with the reason ReasonMakerLostPathAtUse. It
// is the one check at the use of a service token: the authentication of the
// token and each lease request of a running update run it.
//
// A checker that cannot decide returns its own error, which does not wrap
// identity.ErrUnauthenticated, so a failure of the store reads as a server
// error and not as the answer of a revoked token. The token is refused and
// kept, with no revocation and no audit entry (fail closed): the next use
// checks again.
//
// Defense in depth: a committed change of a grant, a group, a membership, a
// role or a principal runs RevokeLostMakers too. This check stays so that a
// path that ends with no such run, for example an identity provider sync, a
// change on another instance, or a stop between the change and the run,
// never lets the token act.
func (s *Service) checkMakerAtUse(ctx context.Context, t Token) error {
	if s.makers == nil || t.Kind != KindService {
		return nil
	}

	can, err := s.makers.CanActAs(ctx, t.CreatedBy, t.PrincipalID)
	if err != nil {
		return fmt.Errorf("check the maker of access token %q: %w", t.ID, err)
	}

	if can {
		return nil
	}

	return s.revokeLostMakerAtUse(ctx, t)
}

// revokeLostMakerAtUse revokes t with the reason ReasonMakerLostPathAtUse and
// returns the refusal of a token whose maker has no act-as path.
func (s *Service) revokeLostMakerAtUse(ctx context.Context, t Token) error {
	revokeErr := s.store.RevokeToken(ctx, t.ID, lostMakerEntry(s.Now(), t, useCause(t), ReasonMakerLostPathAtUse))
	if revokeErr != nil && !errors.Is(revokeErr, ErrEnded) {
		s.logger.WarnContext(ctx, "revoke of an access token whose maker has no act-as path failed",
			slog.String("token_id", t.ID))
	}

	return fmt.Errorf("%w: the maker of the access token has no act-as path to its principal",
		identity.ErrUnauthenticated)
}
