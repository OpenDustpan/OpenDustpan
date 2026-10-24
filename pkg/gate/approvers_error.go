package gate

import "fmt"

// ApproversReason names why no one can meet an approver list.
type ApproversReason int

// The reasons of an ApproversError.
const (
	// ApproversGroupInvalid is a group name that is not a valid group name.
	ApproversGroupInvalid ApproversReason = iota + 1
	// ApproversGroupMissing is a group that does not exist.
	ApproversGroupMissing
	// ApproversUserInvalid is a user name that is not a name.
	ApproversUserInvalid
	// ApproversCountAbove is a count above the people who can approve.
	ApproversCountAbove
)

// ApproversError is the error of an approver list that no one can meet. It
// matches ErrApprovers with errors.Is. Count and People are set for
// ApproversCountAbove only.
type ApproversError struct {
	Reason ApproversReason
	Count  int64
	People int64
	detail string
}

// Error returns the text of ErrApprovers and the detail of the reason.
func (e *ApproversError) Error() string {
	return fmt.Sprintf("%s: %s", ErrApprovers.Error(), e.detail)
}

// Is reports whether target is ErrApprovers.
func (e *ApproversError) Is(target error) bool { return target == ErrApprovers }

// newApproversError returns the error of reason with its detail text.
func newApproversError(reason ApproversReason, detail string) *ApproversError {
	return &ApproversError{Reason: reason, Count: 0, People: 0, detail: detail}
}
