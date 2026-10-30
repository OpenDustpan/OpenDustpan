package storecheck

import (
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
)

var (
	// ErrInvalidID reports a check ID that ParseID refuses.
	ErrInvalidID = errors.New("invalid store check ID")
	// ErrInvalidTarget reports a target that is not one of the fixed names.
	ErrInvalidTarget = errors.New("invalid store check target")
	// ErrNoTarget reports a check of a target that this server has not set,
	// for example a backup target when the server has none.
	ErrNoTarget = errors.New("store check target is not configured")
	// ErrNotFound reports a check ID that has no row.
	ErrNotFound = errors.New("store check not found")
	// ErrExists reports a check ID that has a row already.
	ErrExists = errors.New("store check already exists")
	// ErrRunning reports a start while another check runs. One check runs at
	// a time for each installation.
	ErrRunning = errors.New("a store check is already running")
	// ErrCooldown reports a start on a target whose last check ended less than
	// Cooldown ago.
	ErrCooldown = errors.New("store check target is cooling down")
	// ErrRefused reports a start while the installation is in maintenance mode
	// or while a restore runs.
	ErrRefused = errors.New("store check refused while the installation is in maintenance or restores")
	// ErrNotRunning reports a finish of a row that is not running.
	ErrNotRunning = errors.New("store check is not running")
	// ErrPrefixRefused reports a prefix that is not exactly the prefix of the
	// check that asked for it.
	ErrPrefixRefused = errors.New("store check prefix refused")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

const (
	// IDPrefix starts the ID of every check.
	IDPrefix = "chk-"
	// maxIDLength is the longest check ID, in bytes.
	maxIDLength = 40
)

// ID identifies one check. The service makes it. A caller never chooses it.
type ID string

// NewID returns the ID of a new check.
func NewID() ID {
	return ID(ident.NewID(IDPrefix))
}

// String returns the ID as a plain string.
func (id ID) String() string {
	return string(id)
}

// ParseID returns s as an ID, or ErrInvalidID. An ID is IDPrefix followed by
// lowercase letters and digits only, so it is one valid segment of a blob key
// and holds no slash and no dot.
func ParseID(s string) (ID, error) {
	rest, ok := strings.CutPrefix(s, IDPrefix)
	if !ok || rest == "" || len(s) > maxIDLength || strings.Trim(rest, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
		return "", fmt.Errorf("%w: %s", ErrInvalidID, ident.Echo(s))
	}

	return ID(s), nil
}

// ParseTarget returns s as a Target, or ErrInvalidTarget. It accepts only the
// names of the fixed list.
func ParseTarget(s string) (Target, error) {
	switch Target(s) {
	case TargetBlobs, TargetBackup:
		return Target(s), nil
	default:
		return "", fmt.Errorf("%w: use %q or %q", ErrInvalidTarget, TargetBlobs, TargetBackup)
	}
}
