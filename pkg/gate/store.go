package gate

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Object is the type set of the configuration objects that a scope holds
// by ID.
type Object interface {
	Gate | AccessLimit | ApprovalRule | OwnershipRule | PathSet
}

// Objects is the persistence port of one kind of configuration object.
//
// The fence. An object of the installation takes the zero fence, and its
// audit entry is in the scope audit.ServerScope. An object of a namespace,
// a collection or a unit takes the fence of its namespace, and its entry is
// in that namespace. The store checks the fence and appends the entry in
// the transaction of the write: both commit, or neither. A write returns
// namespace.ErrFenced for a stale fence, namespace.ErrNotFound for a
// namespace that does not exist, ErrInvalid for a fence that is not the
// fence of the scope (also the zero fence for a namespace scope), and
// audit.ErrInvalidEntry for an entry that fails validation or names
// another scope. A failed write changes nothing.
//
// The scopes. Only a scope with HoldsConfig takes a write: a gate of a
// blueprint version or of a profile is stored with that object, and a
// write to such a scope returns ErrInvalid. A wider scope of a unit is the
// installation, its namespace and each collection that holds the unit.
//
// The rules that each write checks in its transaction, so a concurrent
// write cannot pass them:
//   - Put returns ErrLimit when the scope already holds the most objects
//     of the kind, and the object is new.
//   - Put of a gate returns ErrFinal when a wider scope of its scope holds
//     a final gate with the same ID. It returns ErrInvalid when its
//     approval rule, or a path set in Reads, does not resolve at its scope
//     or a wider scope.
//   - Put of an approval rule returns ErrFinal when a wider scope holds a
//     rule with the same ID.
//   - Put of a path set returns ErrInvalid when a wider scope holds a path
//     set with the same ID.
//   - Put of an ownership rule returns ErrInvalid when a path set in Reads
//     does not resolve.
//   - Delete of an approval rule that a stored gate names, and of a path
//     set that a stored gate, approval default or ownership rule reads,
//     returns ErrInvalid. The error names each reader.
type Objects[T Object] interface {
	// Get returns the object id of scope. It returns ErrNotFound when
	// scope holds no such object.
	Get(ctx context.Context, scope Scope, id ID) (T, error)
	// List returns the objects stored at scope, ordered by ID. It returns
	// none of a wider scope.
	List(ctx context.Context, scope Scope) ([]T, error)
	// In returns the objects of each scope of scopes, in the order of
	// scopes and then by ID, in one read. It serves the evaluation of one
	// unit, which names the installation, the namespace, each collection
	// that holds the unit and the unit.
	In(ctx context.Context, scopes []Scope) ([]T, error)
	// Put stores obj at its scope and returns its new version. With prev
	// 0, the object must be new: Put returns ErrConflict when it exists.
	// With prev above 0, it replaces the stored object of version prev and
	// returns ErrConflict for another version, and ErrNotFound when no
	// object exists. The version of obj is ignored.
	Put(ctx context.Context, obj T, prev int64, fence namespace.Fence, e audit.Entry) (int64, error)
	// Delete removes the object id of scope when its version is prev. It
	// returns ErrNotFound when no such object exists, and ErrConflict for
	// another version.
	Delete(ctx context.Context, scope Scope, id ID, prev int64, fence namespace.Fence, e audit.Entry) error
}

// Settings is the persistence port of the settings that a scope holds
// once: the approval default and the signature carry-over. The fence and
// the audit entry follow the rules of Objects.
type Settings interface {
	// ApprovalDefault returns the approval default of scope. It returns
	// ErrNotFound when scope sets none.
	ApprovalDefault(ctx context.Context, scope Scope) (ApprovalDefault, error)
	// PutApprovalDefault stores d at its scope, with the version rules of
	// Objects.Put. It returns ErrInvalid for a scope with no
	// HoldsApprovalDefault, and when a path set in d.Reads does not
	// resolve.
	PutApprovalDefault(
		ctx context.Context,
		d ApprovalDefault,
		prev int64,
		fence namespace.Fence,
		e audit.Entry,
	) (int64, error)
	// CarryOver returns the signature carry-over setting of scope. With
	// none stored, it returns CarryOverOff with version 0.
	CarryOver(ctx context.Context, scope Scope) (CarryOverSetting, error)
	// PutCarryOver stores s at its scope, with the version rules of
	// Objects.Put. It returns ErrInvalid for CarryOverOn at the
	// installation.
	PutCarryOver(
		ctx context.Context,
		s CarryOverSetting,
		prev int64,
		fence namespace.Fence,
		e audit.Entry,
	) (int64, error)
}

// StateStore is the port of the check of the stored gates at start.
type StateStore interface {
	// SetState sets the state of the gate id of scope when its version is
	// version, and keeps the version. It checks fence as Objects does and
	// appends no audit entry: no person changed the gate. It returns
	// ErrNotFound for an unknown gate and ErrConflict for another version.
	SetState(ctx context.Context, scope Scope, id ID, version int64, state State, fence namespace.Fence) error
}

// RecordQuery selects stored records for the simulate route.
type RecordQuery struct {
	Point DecisionPoint
	From  time.Time
	To    time.Time
	// Limit is 1 to MaxSimulateRecords.
	Limit int
}

// Records is the persistence port of decision records. A record is data of
// its namespace, and a write checks its fence. A record of an access limit
// on an installation object is data of the installation and takes the
// zero fence. A record that explains a step of a job, a change order or a
// profile is written in the transaction of that step, by the store of that
// step. Records holds the writes in their own transaction: a refusal, a
// write at state.write and an access limit.
type Records interface {
	// Put stores r in its own transaction. It returns ErrConflict for a
	// used ID, ErrInvalid for a fence of another namespace than
	// r.Namespace, and the fence errors of Objects.
	Put(ctx context.Context, r Record, fence namespace.Fence) error
	// NoteRefusal stores the refusal r of an access limit. When a record
	// of the same subject, action and limit has a LastAt within window
	// before r.At, it adds 1 to its Count, sets its LastAt to r.At and
	// returns its ID. Else it stores r with Count 1 and returns r.ID. The
	// fence rules of Put hold.
	NoteRefusal(ctx context.Context, r Record, window time.Duration, fence namespace.Fence) (RecordID, error)
	// Get returns the record id of ns. It returns ErrNotFound for an
	// unknown ID and for a record of another namespace.
	Get(ctx context.Context, ns namespace.Name, id RecordID) (Record, error)
	// ListByChange returns the records of change in ns, oldest first.
	ListByChange(ctx context.Context, ns namespace.Name, change ChangeRef) ([]Record, error)
	// Query returns at most q.Limit records of ns at q.Point with At in
	// [q.From, q.To), newest first. It returns ErrInvalid for a limit
	// outside 1 to MaxSimulateRecords.
	Query(ctx context.Context, ns namespace.Name, q RecordQuery) ([]Record, error)
}

// Store bundles the persistence ports of the package. Each SQL adapter
// serves them from one database, so a check that reads several kinds runs
// in one transaction.
type Store interface {
	Gates() Objects[Gate]
	AccessLimits() Objects[AccessLimit]
	ApprovalRules() Objects[ApprovalRule]
	OwnershipRules() Objects[OwnershipRule]
	PathSets() Objects[PathSet]
	Settings() Settings
	GateStates() StateStore
	Records() Records
}
