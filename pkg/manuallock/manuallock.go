// Package manuallock models manual locks: a lock that a person puts on one
// unit, on one collection or on a whole namespace, with a reason that everyone
// sees.
//
// A manual lock is separate from the lock of a tool. A tool lock (an OpenTofu
// lock, a Pulumi update lease) is taken and released by the tool. A manual
// lock is taken and released through the lock API, and the lock of a tool
// never removes it. While a manual lock covers a unit, a request of a tool to
// lock that unit fails and names the holder and the reason.
//
// A lock on a collection covers each unit that is in the collection now. A
// unit that joins the collection is covered from that moment, and a unit that
// leaves it is not. A lock on a namespace covers each unit of the namespace,
// also a unit that someone creates later.
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
	ErrNotHolder = errors.New("another person holds this manual lock: " +
		"ask the holder to remove it, or force-unlock it (`unit:manage`)")
	// ErrLockChanged reports a removal that found the lock changed since it
	// read it: another principal ended the lock and took a new one. The new
	// lock stays. Read the lock again and decide again.
	ErrLockChanged = errors.New("manual lock changed during the removal: read the lock again")
	// ErrTargetNotFound reports that the unit, the collection or the
	// namespace to lock does not exist.
	ErrTargetNotFound = errors.New("stack, workspace, collection or namespace to lock not found")
	// ErrReasonRequired reports a lock without a reason.
	ErrReasonRequired = errors.New("a manual lock needs a reason: provide a reason of 1 to 500 characters")
	// ErrInvalidTarget reports a target that names no unit, no collection and
	// no namespace.
	ErrInvalidTarget = errors.New("invalid manual lock: name a stack, workspace, collection or namespace")
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
	// KindNamespace is a lock on a namespace and on each unit in it.
	KindNamespace Kind = "namespace"
)

// Unit names a unit for a lock request: the reference that the request carries
// and the stable IDs that a phase check compares. A rename of the unit changes
// the reference and not the IDs. UnitID is empty only for a unit that does not
// exist yet. For such a unit, the phase clock matches the job by the namespace
// ID, the tool and the name. An empty namespace ID matches no job, so a request
// with an empty namespace ID never passes a lock.
type Unit struct {
	Ref         collection.UnitRef
	NamespaceID namespace.ID
	UnitID      string
}

// Target names the object of a manual lock: one unit, one collection or one
// namespace. Only the field of its Kind is set.
type Target struct {
	Kind       Kind
	Unit       collection.UnitRef
	Collection collection.Ref
	// Space is the namespace of a KindNamespace target.
	Space namespace.Name
}

// UnitTarget returns the target for the unit u.
func UnitTarget(u collection.UnitRef) Target {
	return Target{Kind: KindUnit, Unit: u, Collection: collection.Ref{Namespace: "", Name: ""}, Space: ""}
}

// CollectionTarget returns the target for the collection ref.
func CollectionTarget(ref collection.Ref) Target {
	return Target{
		Kind: KindCollection, Unit: collection.UnitRef{Namespace: "", Tool: "", Name: ""}, Collection: ref, Space: "",
	}
}

// NamespaceTarget returns the target for the whole namespace ns.
func NamespaceTarget(ns namespace.Name) Target {
	return Target{
		Kind: KindNamespace, Unit: collection.UnitRef{Namespace: "", Tool: "", Name: ""},
		Collection: collection.Ref{Namespace: "", Name: ""}, Space: ns,
	}
}

// Namespace returns the namespace of the object. For a KindNamespace target it
// is the locked namespace itself.
func (t Target) Namespace() namespace.Name {
	switch t.Kind {
	case KindCollection:
		return t.Collection.Namespace
	case KindNamespace:
		return t.Space
	case KindUnit:
		return t.Unit.Namespace
	default:
		return ""
	}
}

// Valid reports whether the target names one object.
func (t Target) Valid() error {
	switch {
	case t.Kind == KindUnit && t.Unit.Namespace != "" && t.Unit.Tool != "" && t.Unit.Name != "":
		return nil
	case t.Kind == KindCollection && t.Collection.Namespace != "" && t.Collection.Name != "":
		return nil
	case t.Kind == KindNamespace && t.Space != "":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidTarget, t.Kind)
	}
}

// String returns the object for a log line or an error that wraps a store
// failure: "namespace <name>", "collection <namespace>/<name>", or the unit
// reference "<tool>:<namespace>/<name>". It names no noun for a unit, because
// each tool has its own word for it. A refusal that a person reads uses
// LockedError.
func (t Target) String() string {
	switch t.Kind {
	case KindCollection:
		return "collection " + t.Collection.String()
	case KindNamespace:
		return "namespace " + t.Space.String()
	case KindUnit:
		return t.Unit.String()
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
	if utf8.ValidString(s) && !strings.ContainsFunc(s, audit.IsHiddenRune) {
		return s
	}

	var b strings.Builder

	for _, r := range s {
		if audit.IsHiddenRune(r) {
			fmt.Fprintf(&b, "\\u%04X", r)

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

// LockedError is the error of a request that a manual lock refuses. It
// matches ErrLocked. The tool that refuses the request sets Noun to its own
// word for the unit, for example "stack" or "workspace", and Name to the name
// of the unit in that tool. The text names the unit, the holder and the
// reason. It names no route: each adapter adds the route of its own API. Unit
// is the refused unit as a typed reference: the answer of the refusal reads
// it, never Name. A restore of a
// deleted unit by its stable ID sets Unit.Name to that ID, because the unit
// has no live name yet.
type LockedError struct {
	Lock Lock
	Unit collection.UnitRef
	Noun string
	Name string
}

// Error returns the text of the refusal (Refusal).
func (e *LockedError) Error() string {
	return e.Refusal()
}

// Refusal returns the text of the refusal with no route. A lock on the unit
// gives "<noun> <name> has a manual lock by ...". A lock on the collection of
// the unit gives "<noun> <name> is in collection <ref>, which has a manual
// lock by ...". A lock on the namespace gives "<noun> <name> is in namespace
// <name>, which has a manual lock by ...". The text ends with the reason.
func (e *LockedError) Refusal() string {
	noun := e.Noun
	if noun == "" {
		noun = "stack or workspace"
	}

	noun, name := SafeText(noun), SafeText(e.Name)

	if e.Lock.Target.Kind == KindCollection || e.Lock.Target.Kind == KindNamespace {
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
	// object has a manual lock, ErrTargetNotFound when the unit, the
	// collection or the namespace does not exist, and namespace.ErrFenced when the fence is
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
	// look at the collection or the namespace of a unit.
	GetLock(ctx context.Context, t Target) (Lock, error)
	// CoveringLock returns the manual lock that covers the unit: its own
	// lock, or else the lock of the collection it is in now, or else the
	// lock of its namespace. The Target of the answer is the object that
	// holds the lock. It returns ErrNotLocked when no lock covers the unit
	// or the unit does not exist.
	CoveringLock(ctx context.Context, unit collection.UnitRef) (Lock, error)
	// CoveringLocks returns every manual lock that covers the unit, in one
	// read: the lock of the unit first, then the lock of its collection, then
	// the lock of its namespace. It returns ErrNotLocked, and never an empty
	// list, when no lock covers the unit or the unit does not exist.
	CoveringLocks(ctx context.Context, unit collection.UnitRef) ([]Lock, error)
}
