package principal

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
)

// This file holds the port of the group claims: the one function that
// applies the groups of an identity provider to the memberships of one
// principal. A sign-in calls it with the groups of its Assertion, and a SCIM
// change of the groups of a user calls it too. No implementation exists
// yet. The conformance suite RunGroupClaims in package principaltest skips
// each case until one exists.

// GroupClaims are the groups that one identity provider gives for one
// principal.
type GroupClaims struct {
	// PrincipalID names the principal.
	PrincipalID string
	// Source is MembershipSourceClaim or MembershipSourceSCIM, or the
	// source "ldap" that the LDAP method adds with its first adapter.
	// MembershipSourceLocal is not valid.
	Source MembershipSource
	// SourceID names the sign-in provider of a claim or of LDAP, or the
	// SCIM connection.
	SourceID string
	// Groups are the group names of the identity provider, flat. Each name
	// has 1 to 256 bytes and no control character. At most 1000 names.
	Groups []string
	// Present is false when the identity provider sent no groups value.
	// Then the call changes nothing and returns an empty result.
	Present bool
}

// GroupClaimResult is what a call of ApplyGroupClaims did. Each list holds
// group IDs, except Unknown, which holds the names of the identity provider
// that match no group.
type GroupClaimResult struct {
	// Added are the groups that got an active membership.
	Added []string
	// Removed are the groups whose membership the call ended.
	Removed []string
	// Suspended are the protected groups whose membership the call
	// suspended.
	Suspended []string
	// Refused are the groups that the identity provider may not add a
	// member to: a protected group that does not trust the identity
	// provider, or a group that a person closed to it.
	Refused []string
	// Unknown are the group names that match no group of the home of the
	// principal. The call makes no group.
	Unknown []string
}

// GroupClaimApplier applies the groups of an identity provider to the
// memberships of one principal, with the rules for groups from an identity
// provider. It changes only the memberships of the source and the source ID
// of the claims. It never changes a local membership, a membership of
// another source or source ID, or a pinned membership.
//
// The conformance suite RunGroupClaims in package principaltest checks an
// implementation.
type GroupClaimApplier interface {
	// ApplyGroupClaims applies c and writes one audit entry, from e, for
	// each add, removal, suspension and refused add, in one transaction
	// with the changes. It returns ErrInvalid for claims that fail the
	// limits or name the source local, ErrNotFound for an unknown
	// principal, ErrDisabled for a disabled principal, ErrNotSignInTarget
	// for a principal that the identity provider cannot manage (not a
	// person, a principal of another home than the source, a break-glass
	// account or a temporary administrator), and an error that wraps the
	// error of the store when a write fails. On any error, it changes
	// nothing.
	ApplyGroupClaims(ctx context.Context, e audit.Entry, c GroupClaims) (GroupClaimResult, error)
}
