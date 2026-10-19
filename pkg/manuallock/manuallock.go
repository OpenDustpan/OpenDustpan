// Package manuallock models manual locks: a lock that a person puts on one
// unit or on one collection, with a reason that everyone sees.
//
// A manual lock is separate from the lock of a tool. A tool lock (an OpenTofu
// lock, a Pulumi update lease) is taken and released by the tool. A manual
// lock is taken and released through the lock API, and the lock of a tool
// never removes it. While a manual lock covers a unit, a request of a tool to
// lock that unit fails and names the holder and the reason.
//
// A lock on a collection covers each unit that is in the collection now. A
// unit that joins the collection is covered from that moment, and a unit that
// leaves it is not.
//
// The package is tool-neutral: a unit is named by its tool, its namespace and
// the tool's own name, and the package never parses that name.
package manuallock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrLocked reports that a manual lock covers the unit. The error
	// that carries it is a *LockedError, which names the holder and the reason.
	ErrLocked = errors.New("unit has a manual lock")
	// ErrAlreadyLocked reports a lock on an object that already holds a
	// manual lock.
	ErrAlreadyLocked = errors.New("object already has a manual lock")
	// ErrNotLocked reports a request to remove or read a manual lock that
	// does not exist.
	ErrNotLocked = errors.New("object has no manual lock")
	// ErrNotHolder reports a removal by a principal that does not hold the
	// lock. Only a force-unlock removes the lock of another principal.
	ErrNotHolder = errors.New("manual lock is held by another principal: " +
		"ask the holder to remove it, or force-unlock it with unit:manage")
	// ErrTargetNotFound reports that the unit or the collection to lock does
	// not exist.
	ErrTargetNotFound = errors.New("object to lock not found")
	// ErrReasonRequired reports a lock without a reason.
	ErrReasonRequired = errors.New("a manual lock needs a reason: provide a reason of 1 to 500 characters")
	// ErrInvalidTarget reports a target that names no unit and no collection.
	ErrInvalidTarget = errors.New("invalid manual lock object")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

// Kind tells what a manual lock is on.
type Kind string

// The kinds of object that a manual lock can cover.
const (
	// KindUnit is a lock on one unit.
	KindUnit Kind = "unit"
	// KindCollection is a lock on one collection and on each unit in it.
	KindCollection Kind = "collection"
)

// Target names the object of a manual lock: one unit or one collection. Only
// the field of its Kind is set.
type Target struct {
	Kind       Kind
	Unit       collection.UnitRef
	Collection collection.Ref
}

// UnitTarget returns the target for the unit u.
func UnitTarget(u collection.UnitRef) Target {
	return Target{Kind: KindUnit, Unit: u, Collection: collection.Ref{Namespace: "", Name: ""}}
}

// CollectionTarget returns the target for the collection ref.
func CollectionTarget(ref collection.Ref) Target {
	return Target{Kind: KindCollection, Unit: collection.UnitRef{Namespace: "", Tool: "", Name: ""}, Collection: ref}
}

// Namespace returns the namespace of the object.
func (t Target) Namespace() namespace.Name {
	if t.Kind == KindCollection {
		return t.Collection.Namespace
	}

	return t.Unit.Namespace
}

// Valid reports whether the target names one object.
func (t Target) Valid() error {
	switch {
	case t.Kind == KindUnit && t.Unit.Namespace != "" && t.Unit.Tool != "" && t.Unit.Name != "":
		return nil
	case t.Kind == KindCollection && t.Collection.Namespace != "" && t.Collection.Name != "":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidTarget, t.Kind)
	}
}

// String returns the object as a person reads it: the unit reference, or
// "collection <namespace>/<name>".
func (t Target) String() string {
	if t.Kind == KindCollection {
		return "collection " + t.Collection.String()
	}

	return "unit " + t.Unit.String()
}

// Lock is one manual lock. Holder is the subject of the principal that took
// it, never a token. Since is when the holder took it.
type Lock struct {
	Since  time.Time
	Target Target
	Holder string
	Reason string
}

// String returns the lock as the text that a refused tool request shows: the
// object, the holder, the time and the reason.
func (l Lock) String() string {
	return fmt.Sprintf("%s has a manual lock by %s since %s: %s",
		l.Target, l.Holder, l.Since.UTC().Format(time.RFC3339), l.Reason)
}

// LockedError is the error of a request that a manual lock refuses. It
// matches ErrLocked. Its text names the holder and the reason, and is the
// same text for each tool.
type LockedError struct {
	Lock Lock
}

// Error returns the text of the lock.
func (e *LockedError) Error() string {
	return e.Lock.String()
}

// Is reports whether target is ErrLocked.
func (e *LockedError) Is(target error) bool {
	return target == ErrLocked
}

// Store is the persistence port for manual locks. Every write checks the
// fence and appends its audit entry in the same transaction, so a write that
// cannot be audited is not made. Every adapter runs the conformance suite in
// pkg/manuallock/manuallocktest.
type Store interface {
	// PutLock records l and appends e. It returns ErrAlreadyLocked when the
	// object has a manual lock, ErrTargetNotFound when the unit or the
	// collection does not exist, and namespace.ErrFenced when the fence is
	// stale. The audit entry names the object and the reason.
	PutLock(ctx context.Context, l Lock, fence namespace.Fence, e audit.Entry) error
	// RemoveLock removes the manual lock of t and appends e, with the holder
	// and the reason of the lock it ends. A holder that is not empty must
	// hold the lock, else it returns ErrNotHolder. An empty holder removes
	// the lock whoever holds it. It returns the lock it removed, ErrNotLocked
	// when the object has none, and namespace.ErrFenced when the fence is
	// stale.
	RemoveLock(ctx context.Context, t Target, holder string, fence namespace.Fence, e audit.Entry) (Lock, error)
	// GetLock returns the manual lock of t, or ErrNotLocked. It does not
	// look at the collection of a unit.
	GetLock(ctx context.Context, t Target) (Lock, error)
	// CoveringLock returns the manual lock that covers the unit: its own
	// lock, or else the lock of the collection it is in now. The Target of
	// the answer is the object that holds the lock. It returns ErrNotLocked
	// when no lock covers the unit or the unit does not exist.
	CoveringLock(ctx context.Context, unit collection.UnitRef) (Lock, error)
}
