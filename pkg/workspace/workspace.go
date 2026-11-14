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
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by its ports.
var (
	// ErrNotFound reports that a workspace does not exist.
	ErrNotFound = errors.New("workspace not found")
	// ErrBlobsRemain reports that a blob of a purged unit may remain after a
	// delete (ADR 47). The blob purge record stays for the sweep.
	ErrBlobsRemain = errors.New("blobs of the purged unit remain")
	// ErrAlreadyExists reports that a workspace with the same name exists in the namespace.
	ErrAlreadyExists = errors.New("workspace already exists")
	// ErrInvalidName reports a workspace name that fails validation.
	ErrInvalidName = errors.New("invalid workspace name")
	// ErrLocked reports a lock request on a workspace that is already locked.
	ErrLocked = errors.New("workspace is locked")
	// ErrPendingState reports a delete of a workspace that has a pending state
	// version: an upload that started and did not finish or expire.
	ErrPendingState = errors.New("workspace has a pending state version")
	// ErrNotLocked reports an operation that needs the caller to hold the lock.
	ErrNotLocked = errors.New("workspace is not locked")
	// ErrOverrideRefused reports an override that the unit or its namespace
	// does not allow, or that the caller lacks the action for. Its text names
	// the switch and the action that would allow it (ADR 44, decision 8).
	ErrOverrideRefused = errors.New("write refused")
	// ErrNoRecorder reports a write that uses an override and gives no callback
	// that records it. The write is refused before it changes anything, so that
	// no override goes without an audit entry.
	ErrNoRecorder = errors.New("override needs a record callback")
	// ErrLockedByOther reports a lock held by someone other than the caller.
	ErrLockedByOther = errors.New("workspace is locked by someone else")
	// ErrLockMismatch reports a write that names a lock ID other than the current lock.
	ErrLockMismatch = errors.New("lock ID does not match the current lock")
	// ErrConcurrentModification reports that the workspace changed between read and write.
	ErrConcurrentModification = errors.New("workspace changed concurrently")
	// ErrInvalidLockID reports a lock ID that the caller chose and that fails validation.
	ErrInvalidLockID = errors.New("invalid lock ID")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrMissingSealer reports a Config without a Sealer. Every service
	// seals state, so none runs without one.
	ErrMissingSealer = errors.New("missing sealer")
)

const maxNameLength = 90

// Name is a validated workspace name: 1 to 90 ASCII letters, digits, '-' and
// '_', the rule OpenTofu's cloud block and the TFE API share.
type Name string

// ParseName validates s as a workspace name. A refusal states the rule and
// echoes s as ident.Echo does. An empty s gets the same text as an invalid
// one.
func ParseName(s string) (Name, error) {
	if s == "" || len(s) > maxNameLength {
		return "", nameRuleError(s)
	}

	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return "", nameRuleError(s)
		}
	}

	return Name(s), nil
}

// nameRuleError returns the refusal of the workspace name s: ErrInvalidName,
// the echo of s and the rule of a workspace name.
func nameRuleError(s string) error {
	return fmt.Errorf(
		"%w: %s: use 1 to %d ASCII letters, digits, - and _", ErrInvalidName, ident.Echo(s), maxNameLength,
	)
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

// ReleaseKind is how the last lock of a workspace ended.
type ReleaseKind string

// The kinds of a release. The zero ReleaseKind means that the workspace keeps
// no record of a release.
const (
	// ReleaseNormal is a release by the holder of the lock.
	ReleaseNormal ReleaseKind = "normal"
	// ReleaseForced is a force-unlock. The releaser is the principal that
	// forced the release, and it is usually not the holder.
	ReleaseForced ReleaseKind = "forced"
)

// Valid reports whether k is ReleaseNormal or ReleaseForced. Any other value,
// the zero value included, is no record.
func (k ReleaseKind) Valid() bool {
	return k == ReleaseNormal || k == ReleaseForced
}

// ReleaseWindow is how long the record of a release matches a request that
// repeats the release. No tool lock has a lease to use as the bound: an
// OpenTofu lock has none. 24 hours is far above each retry window of the
// CLIs, which is seconds to minutes, and a record cannot match an unlock from
// a later day.
const ReleaseWindow = 24 * time.Hour

// Release is the record of the last released lock of a workspace. A write of
// OpenTofu can commit while its answer is lost, and OpenTofu then sends the
// request again. The record lets the repeat of an unlock or a force-unlock
// succeed without a change. The zero Release means no record.
type Release struct {
	// At is the time of the release.
	At time.Time
	// LockID is the ID of the released lock.
	LockID string
	// Holder is the subject of the principal that held the lock.
	Holder string
	// By is the subject of the principal that released the lock: the holder
	// in a normal release, the principal that forced it in a forced release.
	By string
	// Kind is how the lock ended.
	Kind ReleaseKind
}

// NoRelease returns the zero Release: no record of a release.
func NoRelease() Release {
	return Release{At: time.Time{}, LockID: "", Holder: "", By: "", Kind: ""}
}

// Recorded reports whether r is a record of a release. A record with a kind
// that is not valid is no record, so it matches no request.
func (r Release) Recorded() bool {
	return r.Kind.Valid()
}

// ReleaseRepeat is a request that can repeat the last release of a workspace.
type ReleaseRepeat struct {
	// LockID is the lock ID that the request names. It is empty when the
	// protocol sends none: then the record matches on the principals only.
	LockID string
	// Holder is the holder that the request names, or empty. Only a forced
	// release reads it: a force-unlock can name the lock that it saw.
	Holder string
	// Caller is the subject of the principal that sends the request.
	Caller string
	// Kind is the kind of release that the request asks for.
	Kind ReleaseKind
}

// Workspace is the aggregate root for one OpenTofu state.
type Workspace struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	Lock      Lock
	// LastRelease is the record of the last released lock. It is the zero
	// Release when the workspace has no record, for example before its first
	// release.
	LastRelease Release
	ID          ID
	Ref         Ref
	// NamespaceID is the ID of the namespace in Ref. The store fills it in the
	// read that returns the workspace, so a state blob call needs no read of
	// the namespace. A write ignores it: the row keeps the namespace name.
	NamespaceID namespace.ID
	// Overrides holds the switches of this unit. A switch only narrows the
	// switch of its namespace: an override needs both (ADR 44, decision 6).
	Overrides namespace.Overrides
	// Revision increases with every change; stores accept a write only if they
	// hold Revision-1.
	Revision int64
	// Origin names the job whose token made the request that writes the row.
	// A write records it when it releases the lock and publishes the current
	// version of the unit; a read gives it as zero.
	Origin identity.StateOrigin
}

// RepeatsRelease reports whether q repeats the release that the record of w
// names, at now. It is true only when all of these are true:
//   - w is not locked;
//   - the record is valid, of the kind of q, and less than ReleaseWindow old;
//   - q names no lock ID, or the lock ID of the record;
//   - for a normal release, the caller is the holder of the record;
//   - for a forced release, the caller is the releaser of the record, and q
//     names no holder or the holder of the record.
//
// A repeat changes nothing: the caller answers success without a write and
// without an audit entry.
func (w Workspace) RepeatsRelease(now time.Time, q ReleaseRepeat) bool {
	r := w.LastRelease

	switch {
	case w.Lock.Locked(), !r.Recorded(), r.Kind != q.Kind, q.Caller == "":
		return false
	case r.At.IsZero(), now.Sub(r.At) >= ReleaseWindow:
		return false
	case q.LockID != "" && q.LockID != r.LockID:
		return false
	case r.Kind == ReleaseNormal:
		return r.Holder == q.Caller
	default:
		return r.By == q.Caller && (q.Holder == "" || q.Holder == r.Holder)
	}
}

// RepeatsLock reports whether a lock request of holder under lockID repeats
// the lock that w holds: the lock is held, with the same lock ID and the same
// holder. The client of the HTTP backend chose the ID, so a match is the same
// request. A repeat changes nothing.
func (w Workspace) RepeatsLock(holder, lockID string) bool {
	return w.Lock.Locked() && lockID != "" && w.Lock.ID == lockID && w.Lock.Holder == holder
}

// lock takes the lock for holder under a new lock ID.
func (w Workspace) lock(now time.Time, holder, reason string) (Workspace, error) {
	return w.lockAs(now, holder, reason, ident.NewID("lock-"))
}

// lockAs takes the lock for holder under lockID. The HTTP backend names its
// lock in its LOCK request, and it sends that ID back with each write (ADR 49,
// decision 6), so the lock keeps the ID the client chose.
func (w Workspace) lockAs(now time.Time, holder, reason, lockID string) (Workspace, error) {
	if w.Lock.Locked() {
		return Workspace{}, lockRefused(w.Lock, fmt.Errorf(
			"%w by %s since %s",
			ErrLocked,
			w.Lock.Holder,
			w.Lock.LockedAt.Format(time.RFC3339),
		))
	}

	return w.withLock(now, Lock{LockedAt: now, ID: lockID, Holder: holder, Reason: reason}), nil
}

// unlock releases holder's lock, and records a normal release.
func (w Workspace) unlock(now time.Time, holder string) (Workspace, error) {
	if err := w.heldBy(holder); err != nil {
		return Workspace{}, err
	}

	return w.released(now, holder, ReleaseNormal), nil
}

// forceUnlock releases the lock whoever holds it, and records a forced
// release by the principal by.
func (w Workspace) forceUnlock(now time.Time, by string) (Workspace, error) {
	if !w.Lock.Locked() {
		return Workspace{}, ErrNotLocked
	}

	return w.released(now, by, ReleaseForced), nil
}

// released returns a copy of w without its lock, one revision on, with the
// record of the release of the lock of w by the principal by.
func (w Workspace) released(now time.Time, by string, kind ReleaseKind) Workspace {
	next := w.withLock(now, Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""})
	next.LastRelease = Release{At: now, LockID: w.Lock.ID, Holder: w.Lock.Holder, By: by, Kind: kind}

	return next
}

// heldBy reports whether holder holds the lock.
func (w Workspace) heldBy(holder string) error {
	switch {
	case !w.Lock.Locked():
		return ErrNotLocked
	case w.Lock.Holder != holder:
		return lockRefused(w.Lock, fmt.Errorf("%w: held by %s", ErrLockedByOther, w.Lock.Holder))
	default:
		return nil
	}
}

// withLock returns a copy of w holding lock, one revision on.
// bump records a change of the unit at now.
func (w Workspace) bump(now time.Time) Workspace {
	next := w
	next.UpdatedAt = now
	next.Revision++

	return next
}

func (w Workspace) withLock(now time.Time, lock Lock) Workspace {
	next := w
	next.Lock = lock
	next.UpdatedAt = now
	next.Revision++

	return next
}

// LockRefusedError is the refusal of a call because of the current lock of
// the workspace: a lock of a locked workspace, an unlock by another holder,
// or a write with another lock ID or with none. It names the lock as the
// refusing revision held it, so a caller can show the holder without a
// second read that could see another lock. It matches the sentinel of the
// refusal: ErrLocked, ErrLockedByOther or ErrLockMismatch.
type LockRefusedError struct {
	// Lock is the lock of the workspace when the call was refused. It is the
	// zero Lock when a write named a lock ID on an unlocked workspace.
	Lock Lock
	// Err is the refusal. It wraps the sentinel, and it is never nil.
	Err error
}

// Error returns the text of the refusal.
func (e *LockRefusedError) Error() string { return e.Err.Error() }

// Unwrap returns the refusal, which wraps its sentinel.
func (e *LockRefusedError) Unwrap() error { return e.Err }

// lockRefused returns err as a LockRefusedError that names lock.
func lockRefused(lock Lock, err error) error {
	return &LockRefusedError{Lock: lock, Err: err}
}

// RefusingLock returns the lock of the first LockRefusedError in the chain
// of err, and false when the chain holds none, for example for a refusal of
// the store after a lost compare-and-swap.
func RefusingLock(err error) (Lock, bool) {
	if refused, ok := errors.AsType[*LockRefusedError](err); ok {
		return refused.Lock, true
	}

	return Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}, false
}
