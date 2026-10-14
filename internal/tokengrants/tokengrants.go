// Package tokengrants is the escalation check of the access token service
// (ADR 53, decision 3) over the grants of ADR 52. A token acts as its
// principal, so to create or rotate a token for a principal is to grant,
// by proxy, everything that the principal holds: the caller must hold
// every action that the grants of the principal give, through its groups
// too. That is the check of a principal delete (access.Service
// CheckPrincipalGrants).
package tokengrants

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Grants checks the grants of a principal. access.Service implements it.
type Grants interface {
	// CheckPrincipalGrants returns nil when caller holds every action that
	// the grants of the principal principalID give, and access.ErrEscalation
	// when it does not. It returns nil for a principal that does not exist.
	CheckPrincipalGrants(ctx context.Context, caller identity.Principal, principalID string) error
}

// Principals reads a principal record. principal.Store implements it.
type Principals interface {
	// GetPrincipal returns the principal with the ID id, or
	// principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// Check implements accesstoken.GrantCheck. Both fields are required.
type Check struct {
	Grants     Grants
	Principals Principals
}

var _ accesstoken.GrantCheck = Check{Grants: nil, Principals: nil}

// RequireHeld implements accesstoken.GrantCheck. A refusal wraps
// identity.ErrDenied, so the API answers 403: an escalation, and a
// principal that does not exist, for which the grant check would answer
// nil. Any other error is returned wrapped, so the API answers 500.
func (c Check) RequireHeld(ctx context.Context, caller identity.Principal, principalID string) error {
	if _, err := c.Principals.GetPrincipal(ctx, principalID); errors.Is(err, principal.ErrNotFound) {
		return fmt.Errorf("%w: principal %q does not exist", identity.ErrDenied, principalID)
	} else if err != nil {
		return fmt.Errorf("read principal %q: %w", principalID, err)
	}

	err := c.Grants.CheckPrincipalGrants(ctx, caller, principalID)
	if errors.Is(err, access.ErrEscalation) {
		return fmt.Errorf("%w: %w", identity.ErrDenied, err)
	}

	if err != nil {
		return fmt.Errorf("check the grants of principal %q: %w", principalID, err)
	}

	return nil
}
