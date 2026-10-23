package oncall

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is the persistence port for on-call schedules. A schedule is a
// row of its namespace: each write carries the fence of that namespace,
// and the store checks it in the transaction of the write. A fence of
// another namespace, or a zero fence, does not belong to the row: Create
// then returns ErrInvalid, and every write that names a stored schedule
// returns ErrNotFound, so the write does not tell whether the schedule
// exists. A schedule never belongs to the server scope. A fence of the
// row's namespace at an older failover version returns
// namespace.ErrFenced. Every write of a person appends the audit entry e
// in its transaction, and a failed write changes nothing and appends
// nothing. The entry belongs to the namespace of the row: a write whose
// entry names another namespace returns ErrInvalid, so a write cannot
// append to the audit chain of another namespace.
type Store interface {
	// Create persists s with version 1. It returns ErrInvalid for a
	// schedule that fails Validate, that has copied shifts, a sync time
	// or overrides, or whose namespace is not the namespace of fence. It
	// returns ErrExists for a used ID or a name that the namespace
	// already has, and namespace.ErrFenced. Only AddOverride writes
	// overrides, so each one has its own action, the time of the store
	// and the cap.
	Create(ctx context.Context, s Schedule, fence namespace.Fence, e audit.Entry) error
	// Get returns the schedule ref names, or ErrNotFound. Overrides come
	// in the order of their adds: the store gives each add a sequence
	// number in its transaction and orders by it, so the last add wins
	// where two overrides cover one time, also when two adds have the
	// same time.
	Get(ctx context.Context, ref Ref) (Schedule, error)
	// Update replaces the stored schedule s.ID when s.Version is the
	// stored version, and increments the version. It keeps the stored
	// overrides, copied shifts and sync time, and ignores the copied
	// shifts and the sync time of s. s.Overrides is empty or the stored
	// list (SameOverrides), so an update with no overrides does not delete
	// the live ones, and a client that reads a schedule, changes it and
	// sends it back keeps working. A request with other overrides is
	// refused, so a caller never thinks that Update wrote an override. It
	// checks the version before the overrides: a request at a stale
	// version gets ErrConflict, also when its overrides are the list of
	// that version and not the stored list. It returns ErrNotFound for an unknown ID or a stored schedule of
	// another namespace than fence, ErrInvalid for a schedule that fails
	// Validate, that names another namespace than the stored one, or whose
	// overrides are not empty and not the stored list, ErrExists for a new
	// name that the namespace already has, ErrConflict, and
	// namespace.ErrFenced.
	Update(ctx context.Context, s Schedule, fence namespace.Fence, e audit.Entry) error
	// Delete deletes the schedule ref names. It returns ErrNotFound for
	// an unknown schedule or a schedule of another namespace than fence,
	// and namespace.ErrFenced. A context whose on-call field names the
	// schedule then shows the field as it is, with a warning.
	Delete(ctx context.Context, ref Ref, fence namespace.Fence, e audit.Entry) error
	// AddOverride adds o after the overrides of the schedule ref names,
	// and increments the version, with no check of the version: two
	// people can add overrides at one time. It is the only write of
	// overrides. It sets o.CreatedAt to the time of its transaction and
	// ignores the CreatedAt of the request. In the same transaction, it
	// first deletes each override of the schedule whose end is not after
	// that time, so the cap counts only the overrides that did not end. It returns
	// ErrNotFound for an unknown schedule or a schedule of another
	// namespace than fence, ErrInvalid for an override that fails
	// Validate or when MaxOverrides overrides that did not end are
	// stored, and namespace.ErrFenced.
	AddOverride(ctx context.Context, ref Ref, o Override, fence namespace.Fence, e audit.Entry) error
	// ReplaceCopy replaces the copied shifts of the external schedule ref
	// names with shifts, and sets Synced to syncedAt. It returns
	// ErrNotFound for an unknown schedule or a schedule of another
	// namespace than fence, ErrInvalid for a local schedule, and
	// namespace.ErrFenced when a failover moved the namespace since the
	// copy read the fence. It writes no audit entry: a scheduled copy is
	// not an action of a person.
	ReplaceCopy(ctx context.Context, ref Ref, shifts []Shift, syncedAt time.Time, fence namespace.Fence) error
	// ForPrincipal returns the schedules of every namespace in which
	// principal is a member of a layer, has an override, or has a copied
	// shift, ordered by namespace and name. The Home page reads it. The
	// caller filters the result by the read action of each namespace.
	ForPrincipal(ctx context.Context, principal string) ([]Schedule, error)
}

// Person is a person of an external on-call tool.
type Person struct {
	// Email is the address that the tool gives, used to find the
	// principal. It can be empty.
	Email string
	// Name is the name that the tool shows.
	Name string
}

// ExternalShift is a shift that an external on-call tool gives.
type ExternalShift struct {
	Person Person
	Start  time.Time
	End    time.Time
}

// Provider reads the shifts of a schedule of an external on-call tool. An
// adapter holds the address and the credential of the tool, from the
// configuration of the installation, and never answers the credential.
type Provider interface {
	// Shifts returns the shifts of the schedule key in the range that
	// starts at from and ends at to, ordered by start. It returns ErrUnknownExternal for a key that the
	// tool does not know, and ErrProviderUnavailable when the tool does
	// not answer or refuses the credential.
	Shifts(ctx context.Context, key string, from, to time.Time) ([]ExternalShift, error)
}

// Directory finds the principal of a person of an external tool. The
// identity package implements it.
type Directory interface {
	// PrincipalByEmail returns the ID of the enabled principal with the
	// verified address email. It returns false when no principal or more
	// than one principal has it.
	PrincipalByEmail(ctx context.Context, email string) (string, bool, error)
}

// Handoff is a note that the person who ends a shift leaves for the next
// person: open incidents, running jobs, things to watch.
type Handoff struct {
	Schedule string
	// At is the end of the shift that the note closes.
	At   time.Time
	From string
	To   string
	Note string
	By   string
}

// MaxHandoffNoteLen caps the note of a handoff, in bytes.
const MaxHandoffNoteLen = 8192

// Handoffs is the persistence port for handoff notes. A note is a row of
// the namespace of its schedule, and its write carries the fence of that
// namespace, with the rules of Store.
type Handoffs interface {
	// AddHandoff persists h and appends e in one transaction. It returns
	// ErrNotFound for an unknown schedule or a schedule of another
	// namespace than fence, ErrInvalid for an empty note or a note above
	// MaxHandoffNoteLen, and namespace.ErrFenced.
	AddHandoff(ctx context.Context, h Handoff, fence namespace.Fence, e audit.Entry) error
	// Handoffs returns the notes of the schedule with the ID schedule of
	// the namespace ns, in the range that starts at from and ends at to,
	// newest first. It returns ErrNotFound for an unknown schedule or a
	// schedule of another namespace than ns, so a read does not tell
	// whether the schedule exists in another namespace.
	Handoffs(ctx context.Context, ns namespace.Name, schedule string, from, to time.Time) ([]Handoff, error)
}
