package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// CanActAs reports whether the maker with the subject maker still has an
// act-as path to the principal principalID: principal:manage-tokens on the
// principal, through a grant on the principal or on an object that holds it,
// directly or through a group. The subject of a bootstrap admin token holds
// every action by a code rule, so it has a path. A maker that is not a
// principal any more and a maker that is disabled have no path. accesstoken.Service reads it to revoke a service token
// whose maker lost the last path. A principal ID that does not parse
// returns ErrInvalid: the caller cannot decide, so it keeps the token and
// refuses its use.
func (s *Service) CanActAs(ctx context.Context, maker, principalID string) (bool, error) {
	if slices.Contains(s.bootstrap, maker) {
		return true, nil
	}

	obj, err := identity.ParsePrincipalObject(principalID)
	if err != nil {
		return false, fmt.Errorf("%w: principal %q: %w", ErrInvalid, principalID, err)
	}

	p, err := s.directory.GetPrincipalBySubject(ctx, maker)
	if errors.Is(err, principal.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("read the maker %q: %w", maker, err)
	}

	if !p.DisabledAt.IsZero() {
		return false, nil
	}

	caller := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: p.Subject, DisplayName: p.DisplayName, Admin: false,
		TokenID: "", TokenExpiresAt: time.Time{}, Kind: identity.Kind(p.Kind),
		Credential: "", JobID: "",
	}

	err = s.authz.Authorize(ctx, caller, identity.ActionPrincipalManageTokens, obj)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, identity.ErrDenied):
		return false, nil
	default:
		return false, fmt.Errorf("decide %s of %q on %s: %w",
			identity.ActionPrincipalManageTokens, maker, obj.String(), err)
	}
}
