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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrLocked reports that a manual lock covers the unit. The error
	// that carries it is a *LockedError, which names the holder and the reason.
	ErrLocked = errors.New("object has a manual lock")
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
	// ErrLockChanged reports a removal that found the lock changed since it
	// read it: another principal ended the lock and took a new one. The new
	// lock stays. Read the lock again and decide again.
	ErrLockChanged = errors.New("manual lock changed during the removal: read the lock again")
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

// String returns the object for a log line or an error that wraps a store
// failure: "collection <namespace>/<name>", or the unit reference
// "<tool>:<namespace>/<name>". It names no noun for a unit, because each tool
// has its own word for it. A refusal that a person reads uses LockedError.
func (t Target) String() string {
	if t.Kind == KindCollection {
		return "collection " + t.Collection.String()
	}

	return t.Unit.String()
}

// Lock is one manual lock. Holder is the subject of the principal that took
// it, never a token. Since is when the holder took it.
type Lock struct {
	Since  time.Time
	Target Target
	Holder string
	Reason string
}

// Detail returns the holder, the time and the reason of the lock, without a
// name for the object: "by <holder> since <time>: <reason>". A tool adds its
// own word for the unit. A person reads the text in a terminal, so the holder
// and the reason go through SafeText: a lock that holds a control character
// (for example from a restored backup) cannot change what the terminal shows.
//
// Defense in depth: the lock service and the backup import refuse such a
// reason too. This escape stays so that a value which skipped both cannot reach
// a terminal.
func (l Lock) Detail() string {
	return fmt.Sprintf("by %s since %s: %s", SafeText(l.Holder), l.Since.UTC().Format(time.RFC3339), SafeText(l.Reason))
}

// SafeText returns s with each control character (Cc), format character (Cf,
// for example the bidirectional overrides), line separator and paragraph
// separator written as \uXXXX, and each byte that is not valid UTF-8 as
// U+FFFD. The result has no character that moves a cursor, ends a line or
// changes the direction of the text.
func SafeText(s string) string {
	if utf8.ValidString(s) && !strings.ContainsFunc(s, isHiddenRune) {
		return s
	}

	var b strings.Builder

	for _, r := range s {
		if isHiddenRune(r) {
			fmt.Fprintf(&b, "\\u%04X", r)

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

// isHiddenRune reports whether r is a control, format, line separator or
// paragraph separator character.
func isHiddenRune(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// LockedError is the error of a request that a manual lock refuses. It
// matches ErrLocked. The tool that refuses the request sets Noun to its own
// word for the unit, for example "stack" or "workspace", and Name to the name
// of the unit in that tool. The text names the unit, the holder and the
// reason.
type LockedError struct {
	Lock Lock
	Noun string
	Name string
}

// Error returns the text of the refusal. A lock on the unit gives
// "<noun> <name> has a manual lock by ...". A lock on the collection of the
// unit gives "<noun> <name> is in collection <ref>, which has a manual lock
// by ...".
func (e *LockedError) Error() string {
	noun := e.Noun
	if noun == "" {
		noun = "object"
	}

	noun, name := SafeText(noun), SafeText(e.Name)

	if e.Lock.Target.Kind == KindCollection {
		return fmt.Sprintf("%s %s is in %s, which has a manual lock %s",
			noun, name, SafeText(e.Lock.Target.String()), e.Lock.Detail())
	}

	return fmt.Sprintf("%s %s has a manual lock %s", noun, name, e.Lock.Detail())
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
	// and the reason of the lock it ends. The holder must not be empty and
	// must hold the lock, else it returns ErrNotHolder; an empty holder
	// removes nothing. It removes only the lock that it read and checked: a
	// lock that changed in between stays, and the call returns
	// ErrLockChanged. It returns the lock it removed, ErrNotLocked when the
	// object has none, and namespace.ErrFenced when the fence is stale.
	RemoveLock(ctx context.Context, t Target, holder string, fence namespace.Fence, e audit.Entry) (Lock, error)
	// ForceRemoveLock is RemoveLock for a removal by a principal that does not
	// hold the lock. It removes the lock whoever holds it, and the audit entry
	// e names the principal that forced the removal. The service calls it only
	// after the authorization check for unit:manage. It has the same errors
	// as RemoveLock, except ErrNotHolder.
	ForceRemoveLock(ctx context.Context, t Target, fence namespace.Fence, e audit.Entry) (Lock, error)
	// GetLock returns the manual lock of t, or ErrNotLocked. It does not
	// look at the collection of a unit.
	GetLock(ctx context.Context, t Target) (Lock, error)
	// CoveringLock returns the manual lock that covers the unit: its own
	// lock, or else the lock of the collection it is in now. The Target of
	// the answer is the object that holds the lock. It returns ErrNotLocked
	// when no lock covers the unit or the unit does not exist.
	CoveringLock(ctx context.Context, unit collection.UnitRef) (Lock, error)
}
