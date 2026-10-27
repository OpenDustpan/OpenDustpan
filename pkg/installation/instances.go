package installation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors of the instance version rows.
var (
	// ErrInvalidInstance reports an instance row whose ID or binary version
	// is empty, too long or has a character outside the allowed set.
	ErrInvalidInstance = errors.New("invalid instance version row")
	// ErrInstanceNotFound reports an instance ID that has no row.
	ErrInstanceNotFound = errors.New("instance version row not found")
)

// MaxInstanceFieldLength is the longest instance ID and the longest binary
// version, in bytes.
const MaxInstanceFieldLength = 64

// Instance is the version row of one running server process: the ID that the
// process chose at start-up, the version of its binary, and the time that it
// started.
//
// The row is not a liveness lease. Nothing renews it, and nothing reads it to
// decide that an instance is live. A process that dies without a clean stop
// leaves its row, and an administrator removes it. The rows serve two checks:
// the list that the installation API answers, and the refusal of a contract
// migration while a row is below the minimum binary version that the
// migration sets.
type Instance struct {
	// StartedAt is the time that the process started, in UTC.
	StartedAt time.Time
	// ID names the process. It is unique in the installation.
	ID string
	// BinaryVersion is the version of the binary of the process, for example
	// v0.2.0, dev, or a commit hash.
	BinaryVersion string
}

// Validate returns an error that wraps ErrInvalidInstance when the ID or the
// binary version is not allowed. Both are letters, digits, dot, dash,
// underscore and plus, up to MaxInstanceFieldLength bytes. The check is an
// allowlist, so a value that reaches a log line, a URL or an audit entry has
// no control character, no space and no separator.
func (i Instance) Validate() error {
	if err := checkInstanceField("id", i.ID); err != nil {
		return err
	}

	return checkInstanceField("binary version", i.BinaryVersion)
}

func checkInstanceField(name, value string) error {
	if value == "" || len(value) > MaxInstanceFieldLength {
		return fmt.Errorf("%w: %s must have 1 to %d characters", ErrInvalidInstance, name, MaxInstanceFieldLength)
	}

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_', r == '+':
		default:
			return fmt.Errorf("%w: %s has a character outside letters, digits and . - _ +", ErrInvalidInstance, name)
		}
	}

	return nil
}

// InstanceStore is the port of the instance version rows. The rows live in
// the control tables, which a restore never replaces and a backup leaves out.
type InstanceStore interface {
	// RegisterInstance writes the row of inst. A row with the same ID is
	// replaced, so a process that starts again with its ID writes one row.
	// It returns ErrInvalidInstance, and changes nothing, when inst fails
	// Validate, and ErrUnavailable when the row cannot be written.
	RegisterInstance(ctx context.Context, inst Instance) error
	// UnregisterInstance deletes the row of id at a clean stop. A missing row
	// is not an error. It returns ErrInvalidInstance for an ID that is not
	// allowed, and ErrUnavailable when the row cannot be deleted.
	UnregisterInstance(ctx context.Context, id string) error
	// ListInstances returns every row ordered by start time, then by ID.
	// It returns ErrUnavailable when the rows cannot be read.
	ListInstances(ctx context.Context) ([]Instance, error)
	// DeleteInstance deletes the row of id for an administrator. It returns
	// ErrInstanceNotFound when no row has id, ErrInvalidInstance for an ID
	// that is not allowed, and ErrUnavailable when the row cannot be deleted.
	DeleteInstance(ctx context.Context, id string) error
}

// ValidInstanceID returns an error that wraps ErrInvalidInstance when id is
// not an allowed instance ID.
func ValidInstanceID(id string) error {
	return checkInstanceField("id", id)
}
