package principal

import (
	"context"
	"errors"

	"opendustpan.org/opendustpan/pkg/audit"
)

// This file holds the ports of the group claims: the mapping from the group
// values of an identity provider to groups, and the one function that
// applies those values to the memberships of one principal. A sign-in calls
// it with the groups of its Assertion, and a SCIM change of the groups of a
// user calls it too. The SQL stores implement both ports over the explicit
// mappings of GroupMappingStore. The conformance suites RunGroupMapper and
// RunGroupClaims in package principaltest check an implementation. The rules
// for protected groups, for groups that a person closed to the identity
// provider, for trusted groups and for full sync are not applied yet, so the
// cases of those rules stay skipped in RunGroupClaims, and the
// implementations apply none of them.

// ErrNoGroupMapping reports a source with no group mapping. The claims of
// that source then map to no group: the server joins no group from them.
var ErrNoGroupMapping = errors.New("no group mapping for the source")

// GroupMapper turns the group values of one identity provider into groups.
// The rule of the mapping is a stored, visible setting. The server never
// infers it. The conformance suite RunGroupMapper in package principaltest
// checks an implementation.
type GroupMapper interface {
	// MapGroups returns, for each value in values, the IDs of the groups
	// that the value maps to, in the home of the principal principalID. A
	// value that maps to no group has no key in the answer. A group of
	// another home is never in the answer. It returns ErrNoGroupMapping
	// when the source and source ID have no mapping, and an error that
	// wraps the error of the store when a read fails.
	MapGroups(
		ctx context.Context, principalID string, source MembershipSource, sourceID string, values []string,
	) (map[string][]string, error)
}

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
	// Groups are the group values of the identity provider, flat. Each
	// value has 1 to 256 bytes and no control character. At most 1000
	// values.
	Groups []string
	// Present is false when the identity provider sent no groups value.
	// Then the call changes nothing and returns an empty result.
	Present bool
}

// GroupClaimResult is what a call of ApplyGroupClaims did. Each list holds
// group IDs, except Unmapped, which holds the values of the identity
// provider that map to no group.
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
	// provider, or a group that a person closed to it. A group that a
	// mapping names but that does not exist, was deleted or is kept by a
	// directory is refused too.
	Refused []string
	// Unmapped are the values that map to no group. With no mapping for
	// the source, it holds each value.
	Unmapped []string
	// NoMapping is true when the source has no group mapping. The call
	// then changed nothing.
	NoMapping bool
}

// GroupClaimApplier applies the groups of an identity provider to the
// memberships of one principal, with a GroupMapper and the rules for groups
// from an identity provider. It changes only the memberships of the source
// and the source ID of the claims. It never changes a local membership, a
// membership of another source or source ID, or a pinned membership. It
// never makes a group.
//
// When the GroupMapper returns ErrNoGroupMapping, the call fails closed: it
// joins no group, changes no membership, sets NoMapping and writes one audit
// entry.
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
	// error of the mapper or the store when a read or a write fails. On any
	// error, it changes nothing.
	ApplyGroupClaims(ctx context.Context, e audit.Entry, c GroupClaims) (GroupClaimResult, error)
}
