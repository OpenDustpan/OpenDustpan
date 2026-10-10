// Package workspace models OpenTofu workspaces inside a namespace: their
// identity, their lock and their state versions (RFC 1). The namespace is the
// organization that OpenTofu's cloud block names.
//
// The server never reads state. With OpenTofu's state encryption it may be
// opaque, so lineage, serial and MD5 come from the client's request, and only
// the MD5 is checked, against the bytes uploaded.
package workspace

import (
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/namespace"
)

// Sentinel errors returned by this package and by its ports.
var (
	// ErrNotFound reports that a workspace does not exist.
	ErrNotFound = errors.New("workspace not found")
	// ErrAlreadyExists reports that a workspace with the same name exists in the namespace.
	ErrAlreadyExists = errors.New("workspace already exists")
	// ErrInvalidName reports a workspace name that fails validation.
	ErrInvalidName = errors.New("invalid workspace name")
	// ErrLocked reports a lock request on a workspace that is already locked.
	ErrLocked = errors.New("workspace is locked")
	// ErrNotLocked reports an operation that needs the caller to hold the lock.
	ErrNotLocked = errors.New("workspace is not locked")
	// ErrLockedByOther reports a lock held by someone other than the caller.
	ErrLockedByOther = errors.New("workspace is locked by someone else")
	// ErrConcurrentModification reports that the workspace changed between read and write.
	ErrConcurrentModification = errors.New("workspace changed concurrently")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

const maxNameLength = 90

// Name is a validated workspace name: 1 to 90 ASCII letters, digits, '-' and
// '_', the rule OpenTofu's cloud block and the TFE API share.
type Name string

// ParseName validates s as a workspace name.
func ParseName(s string) (Name, error) {
	if s == "" || len(s) > maxNameLength {
		return "", fmt.Errorf("%w: %q", ErrInvalidName, s)
	}

	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return "", fmt.Errorf("%w: %q", ErrInvalidName, s)
		}
	}

	return Name(s), nil
}

// String returns the name as a plain string.
func (n Name) String() string {
	return string(n)
}

// Ref is the identity clients use: namespace (organization) and name.
type Ref struct {
	Namespace namespace.Name
	Name      Name
}

// String formats the reference as namespace/name.
func (r Ref) String() string {
	return r.Namespace.String() + "/" + r.Name.String()
}

// ID is a workspace's stable, opaque identifier ("ws-..."). API paths after
// the first lookup use it, so it must survive renames.
type ID string

// String returns the ID as a plain string.
func (id ID) String() string {
	return string(id)
}

// Lock is who holds a workspace and why. The zero Lock means unlocked.
type Lock struct {
	LockedAt time.Time
	// ID identifies this acquisition. The same principal locking again after a
	// force-unlock gets a new ID, so a state version declared before the
	// force-unlock cannot finalize afterwards. The TFE protocol sends no lock
	// ID, so with a shared token a stale job's later declare or unlock is
	// attributed to the new lock: CI jobs should use distinct principals.
	ID string
	// Holder is the subject of the principal holding the lock.
	Holder string
	Reason string
}

// Locked reports whether the lock is held.
func (l Lock) Locked() bool {
	return l.Holder != ""
}

// Workspace is the aggregate root for one OpenTofu state.
type Workspace struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	Lock      Lock
	ID        ID
	Ref       Ref
	// Revision increases with every change; stores accept a write only if they
	// hold Revision-1.
	Revision int64
}

// lock takes the lock for holder.
func (w Workspace) lock(now time.Time, holder, reason string) (Workspace, error) {
	if w.Lock.Locked() {
		return Workspace{}, fmt.Errorf(
			"%w by %s since %s",
			ErrLocked,
			w.Lock.Holder,
			w.Lock.LockedAt.Format(time.RFC3339),
		)
	}

	return w.withLock(now, Lock{LockedAt: now, ID: ident.NewID("lock-"), Holder: holder, Reason: reason}), nil
}

// unlock releases holder's lock.
func (w Workspace) unlock(now time.Time, holder string) (Workspace, error) {
	if err := w.heldBy(holder); err != nil {
		return Workspace{}, err
	}

	return w.withLock(now, Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}), nil
}

// forceUnlock releases the lock whoever holds it.
func (w Workspace) forceUnlock(now time.Time) (Workspace, error) {
	if !w.Lock.Locked() {
		return Workspace{}, ErrNotLocked
	}

	return w.withLock(now, Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}), nil
}

// heldBy reports whether holder holds the lock.
func (w Workspace) heldBy(holder string) error {
	switch {
	case !w.Lock.Locked():
		return ErrNotLocked
	case w.Lock.Holder != holder:
		return fmt.Errorf("%w: held by %s", ErrLockedByOther, w.Lock.Holder)
	default:
		return nil
	}
}

// withLock returns a copy of w holding lock, one revision on.
func (w Workspace) withLock(now time.Time, lock Lock) Workspace {
	next := w
	next.Lock = lock
	next.UpdatedAt = now
	next.Revision++

	return next
}
