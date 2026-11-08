package principal

import (
	"context"
	"fmt"
	"time"
)

// Limits of the removal log.
const (
	// MaxRemovalWindow is the longest window that a removal total covers,
	// and the age at which AddRemoval deletes the rows of a source.
	MaxRemovalWindow = 24 * time.Hour
	// MaxRemovalRowsPruned is the most rows that one AddRemoval deletes.
	MaxRemovalRowsPruned = 1000
	// MaxRemovalIDBytes is the most bytes of the source ID or of the group
	// ID of a removal.
	MaxRemovalIDBytes = 256
)

// RemovalSourceKind is the kind of record that removed the members of a
// group.
type RemovalSourceKind string

// The kinds of a removal source.
const (
	// RemovalSourceIdentityProvider is a sync of the group values of an
	// identity provider.
	RemovalSourceIdentityProvider RemovalSourceKind = "identity-provider"
	// RemovalSourceSCIMToken is a SCIM request of a token.
	RemovalSourceSCIMToken RemovalSourceKind = "scim-token"
)

// RemovalSource names the record that removed the members of a group: the
// kind and the ID of the identity provider or of the SCIM token. The ID is
// the provider ID, not the value of a claim.
type RemovalSource struct {
	Kind RemovalSourceKind
	ID   string
}

// Removal is one record of a removal of members from a group: the source
// that removed them, the group, how many members it removed, how many
// members the group held before the removal, and the time of the removal.
type Removal struct {
	Source        RemovalSource
	GroupID       string
	Removed       int
	MembersBefore int
	At            time.Time
}

// Validate returns ErrInvalid unless the removal is one that AddRemoval can
// store. The check is an allowlist on the form: it rejects a removal and
// never changes one.
func (r Removal) Validate() error {
	if err := validateRemovalSource(r.Source); err != nil {
		return err
	}

	if err := validateClaimText("group ID", r.GroupID, MaxRemovalIDBytes); err != nil {
		return err
	}

	if r.Removed < 1 {
		return fmt.Errorf("%w: %d members removed, the least is 1", ErrInvalid, r.Removed)
	}

	if r.MembersBefore < r.Removed {
		return fmt.Errorf("%w: %d members before the removal of %d", ErrInvalid, r.MembersBefore, r.Removed)
	}

	if r.At.IsZero() {
		return fmt.Errorf("%w: the removal has no time", ErrInvalid)
	}

	return nil
}

// validateRemovalSource returns ErrInvalid unless source names a record that
// can remove members or alert.
func validateRemovalSource(source RemovalSource) error {
	switch source.Kind {
	case RemovalSourceIdentityProvider, RemovalSourceSCIMToken:
	default:
		return fmt.Errorf("%w: the removal source kind %q is unknown", ErrInvalid, source.Kind)
	}

	return validateClaimText("source ID", source.ID, MaxRemovalIDBytes)
}

// ValidateRemovalAlert returns ErrInvalid unless source can mark an alert and
// at is not the zero time. The check is an allowlist on the form: it rejects
// a value and never changes one.
func ValidateRemovalAlert(source RemovalSource, at time.Time) error {
	if err := validateRemovalSource(source); err != nil {
		return err
	}

	if at.IsZero() {
		return fmt.Errorf("%w: the alert has no time", ErrInvalid)
	}

	return nil
}

// ValidateRemovalWindow returns ErrInvalid unless window is above zero and
// at most MaxRemovalWindow.
func ValidateRemovalWindow(window time.Duration) error {
	if window <= 0 || window > MaxRemovalWindow {
		return fmt.Errorf(
			"%w: the removal window is %s, it must be above zero and at most %s",
			ErrInvalid,
			window,
			MaxRemovalWindow,
		)
	}

	return nil
}

// RemovalTotals holds the totals of the removals that one window covers.
type RemovalTotals struct {
	// SourceRemoved is the sum of the members that the source removed in
	// the window, from any group.
	SourceRemoved int
	// GroupRemoved is the sum of the members that the source removed from
	// one group in the window.
	GroupRemoved int
	// GroupMembers is the largest number of members that the group held
	// before one of those removals.
	GroupMembers int
	// AlertedAt is the time of the last alert of the source, or the zero
	// time when the source has none.
	AlertedAt time.Time
}

// RemovalLog is the port of the log of the removals of members from a
// group. A service reads its totals to alert on a mass suspension, and it
// writes one row for each removal that a group claim sync makes.
//
// AddRemoval returns ErrInvalid when the removal or the window fails its
// checks. MarkRemovalAlert returns ErrInvalid when the source or the time
// fails its checks. Neither returns another sentinel.
type RemovalLog interface {
	// AddRemoval stores r and returns the totals of the removals of its
	// source in the window that ends at r.At: the rows with an at_ms above
	// r.At - window and at most r.At, the new row included. It also returns
	// the time of the last alert of the source. It deletes the rows of the
	// same source with an at_ms at most r.At - MaxRemovalWindow, at most
	// MaxRemovalRowsPruned of them, in the same transaction as the write.
	// window must be above zero and at most MaxRemovalWindow.
	AddRemoval(ctx context.Context, r Removal, window time.Duration) (RemovalTotals, error)
	// MarkRemovalAlert stores at as the time of the last alert of source. A
	// later call replaces the time. It returns ErrInvalid when the source
	// kind is unknown, the source ID fails the check of Removal.Validate,
	// or at is zero.
	MarkRemovalAlert(ctx context.Context, source RemovalSource, at time.Time) error
}
