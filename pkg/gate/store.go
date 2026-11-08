package gate

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ObjectRef names one version of one configuration object of a scope.
type ObjectRef struct {
	Scope   Scope
	ID      ID
	Version int64
}

// Object is the type set of the configuration objects that a scope holds
// by ID.
type Object interface {
	Gate | AccessLimit | ApprovalRule | OwnershipRule | PathSet

	// Ref returns the scope, the ID and the version of the object.
	Ref() ObjectRef
}

// ReasonKey is the key of the audit.Change that holds the reason of a write
// that makes a final gate weaker. The service adds it to the audit entry of
// the write, and the store reads it in the transaction.
const ReasonKey = "reason"

// Reason returns the reason that e holds under ReasonKey, or the empty
// string.
func Reason(e audit.Entry) string {
	for _, c := range e.Changes {
		if c.Key == ReasonKey {
			return c.New
		}
	}

	return ""
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
// write to such a scope returns ErrInvalid. A unit is in at most one
// collection, so the chain of a unit is the installation, its namespace,
// the collection of the unit and the unit (see UnitChain). A wider scope of
// a unit is a scope before it in that chain, and a narrower scope of a
// namespace or a collection is a scope after it in the chain of a unit.
//
// The rules that each write checks in its transaction, so a concurrent
// write cannot pass them. Two scopes can each hold a gate that is not final
// with the same ID. Each refusal names the scopes that conflict.
//   - Put returns ErrLimit when the scope already holds the most objects
//     of the kind, and the object is new.
//   - Put of a gate returns ErrFinal when a wider scope of its scope holds
//     a final gate with the same ID. It returns ErrInvalid when its
//     approval rule, or a path set in Reads, does not resolve at its scope
//     or a wider scope.
//   - Put of a gate that is final, or that a change makes final, returns
//     ErrFinal when a narrower scope holds a gate with the same ID.
//   - Put of a change that makes a final gate weaker, with no Reason in
//     the audit entry, returns ErrFinal. Weaker is a switch to evaluate, a
//     clear of Final, and a change of the decision point, the condition,
//     Unless, the outcome or OnError. Delete of a final gate with no
//     Reason returns ErrFinal.
//   - Put of an approval rule returns ErrFinal when a wider scope holds a
//     rule with the same ID, and ErrInvalid when a narrower scope holds
//     one.
//   - Put of a path set returns ErrInvalid when a wider or a narrower scope
//     holds a path set with the same ID.
//   - Put and Delete of an approval rule that a final gate names, or of a
//     path set that a final gate reads, with no Reason in the audit entry,
//     return ErrFinal. The change counts as a change of that gate.
//   - Put of an ownership rule returns ErrInvalid when a path set in Reads
//     does not resolve.
//   - Delete of an approval rule that a stored gate names, and of a path
//     set that a stored gate, approval default or ownership rule reads,
//     returns ErrInvalid. The error names each reader.
//
// The units. The configuration of a unit scope keys on the stable ID of the
// live unit that has the name of the scope, in the transaction of the read or
// the write. A rename and a move between collections keep it with no write.
// A unit scope whose name no live unit holds has no configuration: a read
// returns none, and a write returns ErrNotFound and changes nothing. The
// configuration of a deleted unit stays until its purge, and a restore keeps
// the ID, so it gets the configuration back. A new unit with the name of a
// deleted unit starts with none.
//
// A check for a narrower scope reads the namespaces that this installation
// holds. A check across installations is not part of this version.
type Objects[T Object] interface {
	// Get returns the object id of scope. It returns ErrNotFound when
	// scope holds no such object.
	Get(ctx context.Context, scope Scope, id ID) (T, error)
	// List returns the objects stored at scope, ordered by ID. It returns
	// none of a wider scope.
	List(ctx context.Context, scope Scope) ([]T, error)
	// ListEveryScope returns every object of this kind of the installation,
	// of every scope, with the objects of one scope ordered by ID. The
	// order of the scopes is not defined. It returns the objects of a
	// deleted unit too, because a restore brings them back.
	ListEveryScope(ctx context.Context) ([]T, error)
	// Put stores obj at its scope and returns its new version. With prev
	// 0, the object must be new: Put returns ErrConflict when it exists.
	// With prev above 0, it replaces the stored object of version prev and
	// returns ErrConflict for another version, and ErrNotFound when no
	// object exists. The version of obj is ignored.
	Put(ctx context.Context, obj T, prev int64, fence namespace.Fence, e audit.Entry) (int64, error)
	// Delete removes the object ref.ID of ref.Scope when its version is
	// ref.Version. It returns ErrNotFound when no such object exists, and
	// ErrConflict for another version.
	Delete(ctx context.Context, ref ObjectRef, fence namespace.Fence, e audit.Entry) error
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
	// DeleteApprovalDefault removes the approval default of scope when its
	// version is prev. It returns ErrNotFound when scope sets none,
	// ErrConflict for another version, and ErrInvalid for a scope with no
	// HoldsApprovalDefault. The service asks for a fresh authentication
	// before it calls DeleteApprovalDefault.
	DeleteApprovalDefault(ctx context.Context, scope Scope, prev int64, fence namespace.Fence, e audit.Entry) error
	// CarryOver returns the signature carry-over setting of scope. With
	// none stored, it returns CarryOverOff with version 0.
	CarryOver(ctx context.Context, scope Scope) (CarryOverSetting, error)
	// PutCarryOver stores s at its scope, with the version rules of
	// Objects.Put. It returns ErrInvalid for CarryOverOn at the
	// installation. A put of CarryOverOff deletes the stored setting and
	// returns version 0, so a PUT of off is the delete of the setting, and
	// the next put takes prev 0.
	PutCarryOver(
		ctx context.Context,
		s CarryOverSetting,
		prev int64,
		fence namespace.Fence,
		e audit.Entry,
	) (int64, error)
}

// Config is the configuration of a chain of scopes, read in one
// transaction. Each list is in the order of the scopes, then by ID.
type Config struct {
	Gates          []Gate
	AccessLimits   []AccessLimit
	ApprovalRules  []ApprovalRule
	OwnershipRules []OwnershipRule
	PathSets       []PathSet
	// ApprovalDefaults holds the default of each scope that sets one.
	ApprovalDefaults []ApprovalDefault
	// CarryOver holds the stored setting of each scope that has one.
	CarryOver []CarryOverSetting
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
// on an installation object is data of the installation: its namespace is
// audit.ServerScope and it takes the zero fence. A record that explains a
// step of a job, a change order or a profile is written in the transaction
// of that step, by the store of that step. Records holds the writes in
// their own transaction: a refusal, a write at state.write and an access
// limit.
type Records interface {
	// Put stores r in its own transaction. It returns ErrConflict for a
	// used ID, ErrInvalid for a fence of another namespace than
	// r.Namespace, and the fence errors of Objects.
	Put(ctx context.Context, r Record, fence namespace.Fence) error
	// NoteRefusal stores the refusal r of an access limit. When a record
	// of the same r.Subject, r.Action and limit has a LastAt within window
	// before r.At, it adds 1 to its Count, sets its LastAt to r.At and
	// returns its ID. Else it stores r with Count 1 and returns r.ID. The
	// fence rules of Put hold. The service gives AccessRefusalWindow.
	NoteRefusal(ctx context.Context, r Record, window time.Duration, fence namespace.Fence) (RecordID, error)
	// Get returns the record id of ns, where ns is audit.ServerScope for a
	// record of the installation. It returns ErrNotFound for an unknown ID
	// and for a record of another namespace.
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
	Records() Records
	// Snapshot returns each kind of object and each setting of each scope
	// of scopes, in one read transaction, for the evaluation of one unit.
	// The caller gives the chain of the unit and the scopes that a change
	// adds: a blueprint version or a profile, whose objects come from the
	// store of that object and are empty here. It returns ErrInvalid for a
	// zero scope.
	Snapshot(ctx context.Context, scopes []Scope) (Config, error)
}
