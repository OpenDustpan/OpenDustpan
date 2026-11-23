package access

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// HousekeepingRevokeReason is the reason of the audit entry of a revoke that
// the housekeeping revoke rule allowed: the holder of the grant is a
// housekeeping principal, and the caller holds server:manage-access on the
// installation, so the caller does not need to hold each action of the
// grant. A revoke never gives a privilege. The rule applies to a revoke only,
// never to a grant.
//
// Code rule: docs/reference/code-rules.md lists it.
const HousekeepingRevokeReason = "housekeeping grant revoked by a holder of server:manage-access on @server"

// HousekeepingSubject reports whether s is the subject of a housekeeping
// principal: a principal whose ID is the ID of a purpose of
// principal.HousekeepingPurposes.
func HousekeepingSubject(s Subject) bool {
	if s.Kind != SubjectPrincipal {
		return false
	}

	for _, p := range principal.HousekeepingPurposes() {
		if p.ID() == s.ID {
			return true
		}
	}

	return false
}

// housekeepingRevoke reports whether the housekeeping revoke rule applies to
// a revoke of g by caller: a housekeeping principal holds g, and caller holds
// server:manage-access on the installation. A denial of that action means
// that the rule does not apply. Any other error of the check fails the
// revoke.
func (s *Service) housekeepingRevoke(ctx context.Context, caller identity.Principal, g Grant) (bool, error) {
	if !HousekeepingSubject(g.Subject) {
		return false, nil
	}

	err := s.authz.Authorize(ctx, caller, identity.ActionServerManageAccess, identity.ServerObject())
	if errors.Is(err, identity.ErrDenied) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("check the housekeeping revoke rule: %w", err)
	}

	return true, nil
}

// housekeepingRevokeCheck returns the check that a revoke of g by caller
// under the housekeeping revoke rule runs in its write: a housekeeping
// principal holds g, caller still holds server:manage-access on the
// installation, and caller still holds the manage-access action of the
// object of g, which the route authorized. It does not ask that caller holds
// each action of g.
func (s *Service) housekeepingRevokeCheck(caller identity.Principal, g Grant, chain []identity.Object) GrantCheck {
	return func(ctx context.Context, reads GrantReads) error {
		// Defense in depth: the service tests the holder before it picks this
		// check. This test stays so that a new caller of the check cannot use
		// it for a grant of another holder. It does not rely on the service.
		if !HousekeepingSubject(g.Subject) {
			return fmt.Errorf("%w: the holder of grant %s is not a housekeeping principal", ErrEscalation, g.ID)
		}

		if err := s.checkUnitCollection(ctx, reads, g.Object, chain); err != nil {
			return err
		}

		w, ctx := s.within(ctx, reads, g.Object, chain)

		// Defense in depth: the service authorized this action before the
		// write. This test runs in the transaction of the revoke, so a revoke
		// of the caller's own grant that commits first is visible. It does
		// not rely on the test of the service.
		server := identity.ServerObject()
		if err := w.authz.Authorize(ctx, caller, identity.ActionServerManageAccess, server); err != nil {
			return err
		}

		kind, _ := KindOf(g.Object)

		return w.authz.Authorize(ctx, caller, manageAccessAction(kind), g.Object)
	}
}
