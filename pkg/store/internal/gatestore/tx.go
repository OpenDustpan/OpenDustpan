// Package gatestore holds the logic of the SQL adapters of the gate.Store
// port that does not depend on the SQL dialect: the version rules, the limits
// of a scope, the checks across scopes and the encoding of the stored
// objects. An adapter gives it a Runner, which opens the transactions, and a
// Tx, which reads and writes the rows of one transaction. So the adapters
// differ in their queries only, and the checks that need one transaction
// are written once.
package gatestore

import (
	"context"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The kinds of a stored row. The approval default and the carry-over setting
// are rows of a scope with an empty ID.
const (
	KindGate            = "gate"
	KindAccessLimit     = "access-limit"
	KindApprovalRule    = "approval-rule"
	KindOwnershipRule   = "ownership-rule"
	KindPathSet         = "path-set"
	KindApprovalDefault = "approval-default"
	KindCarryOver       = "carry-over"
)

// The kinds of a reference of an object.
const (
	RefApprovalRule = "approval-rule"
	RefPathSet      = "path-set"
)

// Row is one stored object of a scope.
type Row struct {
	Kind    string
	ID      string
	Version int64
	Body    string
}

// Located is a stored object and the scope key of the scope that holds it.
type Located struct {
	ScopeKey string
	Row      Row
}

// Ref names an approval rule or a path set that an object uses.
type Ref struct {
	Kind string
	ID   string
}

// RecordRow is the stored form of a decision record: the columns that a query
// reads, and the whole record as text.
type RecordRow struct {
	ID         string
	Point      string
	ChangeKind string
	ChangeID   string
	AtMs       int64
	LastAtMs   int64
	Count      int64
	RefusalKey string
	Body       string
}

// Window selects the records of one decision point in a time range.
type Window struct {
	Point string
	// From and To bound the At of a record: From up to, not including, To.
	From  time.Time
	To    time.Time
	Limit int
}

// Tx reads and writes the rows of one transaction. An adapter implements it
// over its queries. The scope arguments name the scope that holds a row; a
// scope whose namespace does not exist holds no row.
//
//nolint:interfacebloat // One adapter contract for one table set: the rows, the scopes and the records share a transaction.
type Tx interface {
	// Get returns the row, and false when the scope holds none.
	Get(ctx context.Context, scope gate.Scope, kind, id string) (Row, bool, error)
	// List returns the rows of one kind in the scope, ordered by ID.
	List(ctx context.Context, scope gate.Scope, kind string) ([]Row, error)
	// ListScope returns every row of the scope, ordered by kind and ID.
	ListScope(ctx context.Context, scope gate.Scope) ([]Row, error)
	// Count returns the count of rows of one kind in the scope.
	Count(ctx context.Context, scope gate.Scope, kind string) (int, error)
	// Insert stores a new row at version 1 or above.
	Insert(ctx context.Context, scope gate.Scope, row Row, at time.Time) error
	// Update replaces the row of version prev with row, and reports false
	// when the stored version is not prev.
	Update(ctx context.Context, scope gate.Scope, row Row, prev int64, at time.Time) (bool, error)
	// Delete removes the row of version prev, and reports false when the
	// stored version is not prev.
	Delete(ctx context.Context, scope gate.Scope, kind, id string, prev int64) (bool, error)
	// SetReferences replaces the references of the row.
	SetReferences(ctx context.Context, scope gate.Scope, kind, id string, refs []Ref) error
	// Narrower returns the rows of one kind and ID in the scope and in the
	// scopes after it: every scope for the installation, the scopes of the
	// namespace for a namespace, the collection and its units for a
	// collection, and the unit for a unit.
	Narrower(ctx context.Context, scope gate.Scope, kind, id string) ([]Located, error)
	// Referrers returns the rows, of any kind, in the scope and in the
	// scopes after it that name the approval rule or the path set.
	Referrers(ctx context.Context, scope gate.Scope, ref Ref) ([]Located, error)
	// UnitCollection returns the key of the collection that holds the unit of
	// a unit scope, and false for a unit in no collection.
	UnitCollection(ctx context.Context, scope gate.Scope) (string, bool, error)

	// RecordExists reports whether a record has the ID, in any namespace.
	RecordExists(ctx context.Context, id string) (bool, error)
	// InsertRecord stores a record of the namespace ns, where ns is
	// audit.ServerScope for a record of the installation.
	InsertRecord(ctx context.Context, ns namespace.Name, r RecordRow) error
	// Record returns the body of the record id of ns, and false for none.
	Record(ctx context.Context, ns namespace.Name, id string) (string, bool, error)
	// RecordsByChange returns the bodies of the records of one change,
	// oldest first.
	RecordsByChange(ctx context.Context, ns namespace.Name, kind, id string) ([]string, error)
	// QueryRecords returns the bodies of the records of the window, newest
	// first.
	QueryRecords(ctx context.Context, ns namespace.Name, w Window) ([]string, error)
	// FindRefusal returns the newest record of the key whose last refusal is
	// at or after since, and false for none. It locks the key until the
	// transaction ends, where the database needs it.
	FindRefusal(ctx context.Context, ns namespace.Name, key string, since time.Time) (RecordRow, bool, error)
	// UpdateRefusal sets the count, the last refusal and the body of a record.
	UpdateRefusal(ctx context.Context, r RecordRow) error
}

// Runner opens the transactions of an adapter.
type Runner interface {
	// Write runs fn in one transaction that checks fence first and appends e
	// after fn. For the installation, fence is the zero fence and e is in
	// audit.ServerScope. An error of fn rolls the transaction back. The
	// transaction orders itself against every other write of the
	// configuration, so a check across scopes cannot race a write.
	Write(ctx context.Context, scope gate.Scope, fence namespace.Fence, e audit.Entry, fn func(Tx) error) error
	// WriteRecord runs fn in one transaction that checks fence first, with no
	// audit entry. ns is audit.ServerScope for a record of the installation,
	// with the zero fence.
	WriteRecord(ctx context.Context, ns namespace.Name, fence namespace.Fence, fn func(Tx) error) error
	// Read runs fn in one read transaction.
	Read(ctx context.Context, fn func(Tx) error) error
}

// Store serves the ports of gate.Store from one Runner.
type Store struct {
	gates          *Objects[gate.Gate]
	accessLimits   *Objects[gate.AccessLimit]
	approvalRules  *Objects[gate.ApprovalRule]
	ownershipRules *Objects[gate.OwnershipRule]
	pathSets       *Objects[gate.PathSet]
	settings       *Settings
	records        *Records
	run            Runner
}

var _ gate.Store = (*Store)(nil)

// New returns the gate.Store over run.
func New(run Runner) *Store {
	return &Store{
		gates:          newObjects[gate.Gate](run),
		accessLimits:   newObjects[gate.AccessLimit](run),
		approvalRules:  newObjects[gate.ApprovalRule](run),
		ownershipRules: newObjects[gate.OwnershipRule](run),
		pathSets:       newObjects[gate.PathSet](run),
		settings:       &Settings{run: run},
		records:        &Records{run: run},
		run:            run,
	}
}

// Gates implements gate.Store.
func (s *Store) Gates() gate.Objects[gate.Gate] { return s.gates }

// AccessLimits implements gate.Store.
func (s *Store) AccessLimits() gate.Objects[gate.AccessLimit] { return s.accessLimits }

// ApprovalRules implements gate.Store.
func (s *Store) ApprovalRules() gate.Objects[gate.ApprovalRule] { return s.approvalRules }

// OwnershipRules implements gate.Store.
func (s *Store) OwnershipRules() gate.Objects[gate.OwnershipRule] { return s.ownershipRules }

// PathSets implements gate.Store.
func (s *Store) PathSets() gate.Objects[gate.PathSet] { return s.pathSets }

// Settings implements gate.Store.
//
//nolint:ireturn // The method implements gate.Store, which returns the port.
func (s *Store) Settings() gate.Settings { return s.settings }

// Records implements gate.Store.
//
//nolint:ireturn // The method implements gate.Store, which returns the port.
func (s *Store) Records() gate.Records { return s.records }

// Removed names one configuration object that a purge or a delete removes.
type Removed struct {
	Kind     string
	ID       string
	ScopeKey string
}

// RemovedChanges returns the audit changes that name the removed objects: one
// change for each object, keyed by its kind and ID, with the scope key as the
// value. The room is the number of changes that the entry still accepts. A
// list above the room ends with one change that gives the count of the
// objects that it leaves out. It returns nil when the room is less than 1.
func RemovedChanges(removed []Removed, room int) []audit.Change {
	return objectChanges("removed", removed, room)
}

// MovedChanges returns the audit changes that name the objects that a restore
// moved to a new unit name. It has the shape and the cap of RemovedChanges.
func MovedChanges(moved []Removed, room int) []audit.Change {
	return objectChanges("moved", moved, room)
}

// objectChanges builds the changes of RemovedChanges and MovedChanges with the
// key prefix verb.
func objectChanges(verb string, objects []Removed, room int) []audit.Change {
	if room < 1 || len(objects) == 0 {
		return nil
	}

	limit := room - 1

	out := make([]audit.Change, 0, min(len(objects), room))

	for i, r := range objects {
		if i == limit && len(objects) > room {
			out = append(out, audit.Change{
				Key: verb + ":more", Old: nil, New: strconv.Itoa(len(objects) - limit), Layer: "", TakesEffect: "",
			})

			break
		}

		out = append(out, audit.Change{
			Key: verb + ":" + r.Kind + ":" + r.ID, Old: nil, New: r.ScopeKey, Layer: "", TakesEffect: "",
		})
	}

	return out
}
