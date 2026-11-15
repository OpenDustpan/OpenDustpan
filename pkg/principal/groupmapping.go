package principal

import (
	"context"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Limits of the group values of an identity provider, and of a stored
// mapping from one of them.
const (
	// MaxGroupClaimValues is the most group values that one call of
	// ApplyGroupClaims accepts.
	MaxGroupClaimValues = 1000
	// MaxGroupClaimValueBytes is the most bytes of one group value.
	MaxGroupClaimValueBytes = 256
	// MaxGroupSourceIDBytes is the most bytes of the source ID of a claim
	// or of a mapping.
	MaxGroupSourceIDBytes = 128
	// MaxUnmappedAudited is the most unmapped values that the audit entry
	// of a call lists. The entry also holds the count of all of them.
	MaxUnmappedAudited = 20
)

// Audit actions of the group claims. Each is written in the scope
// audit.ServerScope.
const (
	// AuditActionClaimAdd is written for each membership that a call adds.
	AuditActionClaimAdd = "group-claim:add-member"
	// AuditActionClaimRemove is written for each membership that a call
	// ends.
	AuditActionClaimRemove = "group-claim:remove-member"
	// AuditActionClaimRefuse is written for each group that a call may not
	// add a member to. Its outcome is audit.OutcomeDenied.
	AuditActionClaimRefuse = "group-claim:refuse-member"
	// AuditActionClaimUnmapped is written once for the values of a call
	// that map to no group.
	AuditActionClaimUnmapped = "group-claim:unmapped-values"
	// AuditActionClaimNoMapping is written once for a call whose source has
	// no mapping. Its outcome is audit.OutcomeDenied.
	AuditActionClaimNoMapping = "group-claim:no-mapping"
	// AuditActionMappingPut is written for each mapping that is stored.
	AuditActionMappingPut = "group-mapping:put"
	// AuditActionMappingDelete is written for each mapping that is deleted.
	AuditActionMappingDelete = "group-mapping:delete"
	// AuditActionMassRemovalAlert is written once when the removals of one
	// source cross a limit of the removal alert. The entry is in the scope
	// audit.ServerScope.
	AuditActionMassRemovalAlert = "principal:mass-removal-alert"
)

// ValidateGroupValue returns ErrInvalid unless v is a group value that an
// identity provider may send: 1 to MaxGroupClaimValueBytes bytes of valid
// UTF-8 with no control character. The check is an allowlist on the form:
// it rejects a value and never changes it. A hostile value is kept out of
// the error text.
func ValidateGroupValue(v string) error {
	return validateClaimText("group value", v, MaxGroupClaimValueBytes)
}

// ValidateGroupSource returns ErrInvalid unless source can hold the
// memberships or the mappings of an identity provider (not local), and
// sourceID names the provider: 1 to MaxGroupSourceIDBytes bytes of valid
// UTF-8 with no control character.
func ValidateGroupSource(source MembershipSource, sourceID string) error {
	parsed, err := ParseMembershipSource(string(source))
	if err != nil {
		return err
	}

	if parsed == MembershipSourceLocal {
		return fmt.Errorf("%w: the source %q has no identity provider", ErrInvalid, source)
	}

	return validateClaimText("source ID", sourceID, MaxGroupSourceIDBytes)
}

// validateClaimText returns ErrInvalid unless s is 1 to limit bytes of valid
// UTF-8 with no control character.
func validateClaimText(what, s string, limit int) error {
	switch {
	case s == "":
		return fmt.Errorf("%w: %s is empty", ErrInvalid, what)
	case len(s) > limit:
		return fmt.Errorf("%w: %s has %d bytes, the limit is %d", ErrInvalid, what, len(s), limit)
	case !utf8.ValidString(s):
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalid, what)
	}

	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s has a control character", ErrInvalid, what)
		}
	}

	return nil
}

// Validate returns ErrInvalid unless the claims meet the limits of
// GroupClaims. The values are not checked when Present is false, because the
// call then changes nothing.
func (c GroupClaims) Validate() error {
	if c.PrincipalID == "" {
		return fmt.Errorf("%w: group claims name no principal", ErrInvalid)
	}

	if err := ValidateGroupSource(c.Source, c.SourceID); err != nil {
		return err
	}

	if !c.Present {
		return nil
	}

	if len(c.Groups) > MaxGroupClaimValues {
		return fmt.Errorf("%w: %d group values, the limit is %d", ErrInvalid, len(c.Groups), MaxGroupClaimValues)
	}

	for _, v := range c.Groups {
		if err := ValidateGroupValue(v); err != nil {
			return err
		}
	}

	return nil
}

// GroupMapping is one stored, explicit mapping: the group value ClaimValue
// of the identity provider (Source and SourceID) joins the group GroupID.
// The value is compared byte for byte: a value that differs only in case is
// another value. A value can map to more than one group, one mapping each.
type GroupMapping struct {
	CreatedAt time.Time
	// Source is MembershipSourceClaim or MembershipSourceSCIM.
	Source MembershipSource
	// SourceID names the sign-in provider or the SCIM connection.
	SourceID   string
	ClaimValue string
	GroupID    string
	// CreatedBy is the subject of the person that made the mapping.
	CreatedBy string
}

// GroupMappingKey names one mapping: the value of a source and a source ID,
// and the group it maps to.
type GroupMappingKey struct {
	Source     MembershipSource
	SourceID   string
	ClaimValue string
	GroupID    string
}

// Key returns the key of m.
func (m GroupMapping) Key() GroupMappingKey {
	return GroupMappingKey{Source: m.Source, SourceID: m.SourceID, ClaimValue: m.ClaimValue, GroupID: m.GroupID}
}

// Validate returns ErrInvalid unless the key names a source, a source ID and
// a value that pass the checks.
func (k GroupMappingKey) Validate() error {
	if err := ValidateGroupSource(k.Source, k.SourceID); err != nil {
		return err
	}

	return ValidateGroupValue(k.ClaimValue)
}

// Validate returns ErrInvalid unless m holds usable values.
func (m GroupMapping) Validate() error {
	if err := ValidateGroupSource(m.Source, m.SourceID); err != nil {
		return err
	}

	if err := ValidateGroupValue(m.ClaimValue); err != nil {
		return err
	}

	switch {
	case m.GroupID == "":
		return fmt.Errorf("%w: a group mapping names no group", ErrInvalid)
	case m.CreatedBy == "":
		return fmt.Errorf("%w: a group mapping names no creator", ErrInvalid)
	case m.CreatedAt.IsZero():
		return fmt.Errorf("%w: a group mapping has no time", ErrInvalid)
	default:
		return nil
	}
}

// GroupMappingStore keeps the explicit group mappings. A mapping is a stored
// setting that a person can list, audit and delete. Each write takes the
// audit entry of the change, in the scope audit.ServerScope, and appends it
// in the same transaction: both commit, or neither. A write returns
// audit.ErrInvalidEntry, and changes nothing, when the entry fails validation
// or names another scope.
type GroupMappingStore interface {
	// PutGroupMapping stores m. It returns ErrInvalid for a mapping that
	// fails Validate, ErrNotFound when the group does not exist or was
	// deleted, ErrManagedByDirectory when a directory keeps the group, and
	// ErrAlreadyExists when the same mapping exists.
	PutGroupMapping(ctx context.Context, m GroupMapping, e audit.Entry) error
	// DeleteGroupMapping deletes the mapping that key names. It returns
	// ErrInvalid for a source, a source ID or a value that fails the checks,
	// and ErrNotFound when no such mapping exists.
	DeleteGroupMapping(ctx context.Context, key GroupMappingKey, e audit.Entry) error
	// ListGroupMappings returns the mappings of the source and the source
	// ID, ordered by value and then by group ID. It returns ErrInvalid for a
	// source or a source ID that fails the checks.
	ListGroupMappings(ctx context.Context, source MembershipSource, sourceID string) ([]GroupMapping, error)
}
