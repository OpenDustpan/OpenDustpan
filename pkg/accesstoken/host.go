package accesstoken

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// HostFirstTokenWindow is the time after the creation of a person in which
// the host command may issue its first token. The host command makes the
// person and the token in one run, so a person older than this window is
// not the person of the run.
const HostFirstTokenWindow = 5 * time.Minute

// HostIssuer issues the first token of a temporary administrator that the
// host command just created. It is a narrow exception to the own-token
// rule: recovery has no sign-in, so the person cannot make its own first
// token.
//
// Only the host command wiring builds a HostIssuer. No HTTP route builds
// one, and the normal Create path has no flag that reaches it. Every other
// caller keeps ErrNotOwnToken.
type HostIssuer struct {
	svc *Service
}

// NewHostIssuer returns the issuer of the host command over svc. It
// returns ErrNoTempAdminReader when svc has no temporary administrator
// reader: without the record the issuer cannot prove that the person is a
// temporary administrator, so it fails closed.
func NewHostIssuer(svc *Service) (*HostIssuer, error) {
	if svc == nil || svc.tempAdmins == nil {
		return nil, ErrNoTempAdminReader
	}

	return &HostIssuer{svc: svc}, nil
}

// ErrNoTempAdminReader reports a HostIssuer over a Service that has no
// temporary administrator reader.
var ErrNoTempAdminReader = errors.New("the host issuer needs a temporary administrator reader")

// IssueFirstToken issues the first personal token of the principal
// principalID, with the expiry expires and the audit entry e. It refuses
// with identity.ErrDenied wrapping ErrNotOwnToken when any of these fails:
//
//   - the principal is a person that is not disabled, made at most
//     HostFirstTokenWindow ago;
//   - a working temporary administrator record names the principal;
//   - the principal holds no token of any status;
//   - the expiry lies after now and at most tempadmin.Lifetime from now.
//
// The token carries the name tempadmin.HostTokenName and the creator
// tempadmin.HostSubject. The audit entry e records the creation of the
// token in the same transaction as the token.
func (h *HostIssuer) IssueFirstToken(
	ctx context.Context, e audit.Entry, principalID string, expires time.Time,
) (Token, Secret, error) {
	s := h.svc

	if err := s.requireFresh(ctx, e.Subject); err != nil {
		return Token{}, Secret{}, err
	}

	at := s.Now()

	p, err := s.hostTarget(ctx, principalID, at)
	if err != nil {
		return Token{}, Secret{}, fmt.Errorf("issue host access token: %w", err)
	}

	if want := at.Add(tempadmin.Lifetime); !expires.After(at) || expires.After(want) {
		return Token{}, Secret{}, hostRefusal(fmt.Sprintf(
			"the expiry must be after now and at most %s from now", tempadmin.Lifetime,
		))
	}

	expiresAt, err := s.expiry(ctx, at, expires)
	if err != nil {
		return Token{}, Secret{}, err
	}

	n, err := s.newToken(tokenSpec{
		at:          at,
		expiresAt:   expiresAt,
		principalID: p.ID,
		name:        tempadmin.HostTokenName,
		kind:        KindPersonal,
		createdBy:   tempadmin.HostSubject,
	})
	if err != nil {
		return Token{}, Secret{}, err
	}

	if err := s.store.CreateToken(ctx, n.token, n.digest, change(e, at, TokenRef(p.ID, n.token.ID))); err != nil {
		return Token{}, Secret{}, fmt.Errorf("create host access token: %w", err)
	}

	return n.token, n.secret, nil
}

// hostTarget reads the principal principalID and checks that the host may
// issue its first token at the time at.
func (s *Service) hostTarget(ctx context.Context, principalID string, at time.Time) (principal.Principal, error) {
	p, err := s.principals.GetPrincipal(ctx, principalID)
	if errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, fmt.Errorf("%w: %q", ErrPrincipalNotFound, principalID)
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("read principal: %w", err)
	}

	if p.Kind != principal.KindPerson || !p.DisabledAt.IsZero() {
		return principal.Principal{}, hostRefusal("the principal is not an enabled person")
	}

	if at.Sub(p.CreatedAt) > HostFirstTokenWindow {
		return principal.Principal{}, hostRefusal("the person was not made by this run of the host command")
	}

	if recErr := s.hostRecord(ctx, p, at); recErr != nil {
		return principal.Principal{}, recErr
	}

	held, _, err := s.store.ListTokens(ctx, Query{
		At: at, AfterCreatedAt: time.Time{}, PrincipalID: p.ID, AfterID: "", Filter: FilterAll,
		Limit: 1, HasAfter: false,
	})
	if err != nil {
		return principal.Principal{}, fmt.Errorf("list tokens of the person: %w", err)
	}

	if len(held) > 0 {
		return principal.Principal{}, hostRefusal("the person already holds a token")
	}

	return p, nil
}

// hostRecord checks that a working temporary administrator record names
// the principal p at the time at.
func (s *Service) hostRecord(ctx context.Context, p principal.Principal, at time.Time) error {
	rec, err := s.tempAdmins.GetTempAdminBySubject(ctx, p.Subject)
	if errors.Is(err, tempadmin.ErrNotFound) {
		return hostRefusal("the person is not a working temporary administrator")
	}

	if err != nil {
		return fmt.Errorf("read temporary administrator: %w", err)
	}

	if rec.PrincipalID != p.ID || !rec.ActiveAt(at) {
		return hostRefusal("the person is not a working temporary administrator")
	}

	return nil
}

// hostRefusal returns the refusal of the host issuer for the reason why:
// the same denial as a create of the token of another person.
func hostRefusal(why string) error {
	return fmt.Errorf("%w: %w: the host issues only the first token of a new temporary administrator: %s",
		identity.ErrDenied, ErrNotOwnToken, why)
}
