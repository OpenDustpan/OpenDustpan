// Package inventory is the cross-tool read model of every unit in a
// namespace: its tool, its reference, its display name, its last change,
// its status, its resource count and its lock.
//
// It is a projection: each tool package publishes its own writes to it
// through this package, in the same transaction as the tool's own write,
// so it replicates with the namespace like any other data. Nothing else
// writes to the table, and it can be rebuilt from the tool packages' data.
// Tool-specific views read the tool's own model.
package inventory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Entry is one unit in a namespace, as the console's cross-tool views read
// it. Tool names the tool that owns the unit, for example "pulumi". Name is
// the tool's own name for the unit, for example "web/prod". The core keeps
// both opaque and never parses them. UnitID is the unit's stable,
// opaque identifier, for example "stk-...". It lets a consumer follow a unit
// through a rename. DisplayName is the name the console shows. LastChanged
// is the time of the unit's last change. Status is empty when unknown.
// ResourceCount is nil when unknown. Lock is nil when the unit is not
// locked: a lock is a flag, not a status, so a unit can hold a lock in any
// status.
type Entry struct {
	LastChanged   time.Time
	Namespace     namespace.Name
	Tool          string
	Name          string
	UnitID        string
	DisplayName   string
	Status        string
	ResourceCount *int64
	Lock          *Lock
	// ManualLock is the manual lock that covers the unit, or nil when none
	// does. Like Lock, it is a flag and not a status. A lock on the unit or on
	// its collection sets it.
	ManualLock *ManualLock
	// Collection is the name of the collection that holds the unit, as it was
	// written. It is empty when the unit has none (ADR 51). A collection never
	// changes the tool's own name of the unit.
	Collection string
	// Labels are the labels of the unit: a key and a value each. The map is
	// never nil in an entry that a store reads, and a unit with no label has
	// an empty map. A label never grants access.
	Labels map[string]string
}

// Lock is who holds a unit and why. Holder is the subject of the principal
// that holds the lock, never a token, a secret or a capability path. Since
// is when the holder took it. Operation is the tool's own word for what
// holds it: the update kind for the Pulumi CLI, the lock reason for
// OpenTofu, or "lock" when the tool names none.
type Lock struct {
	Since     time.Time
	Holder    string
	Operation string
}

// ManualLock is a manual lock that covers a unit: who holds it, why, since
// when and on which object. Kind is "unit" or "collection". Object names the
// object that holds the lock: the unit reference for a unit, or the
// collection reference <namespace>/<collection> for a collection. A lock on a
// collection covers each unit that is in the collection now.
type ManualLock struct {
	Since  time.Time
	Kind   string
	Object string
	Holder string
	Reason string
}

// Reference returns the unit reference in the form <tool>:<namespace>/<name>.
func (e Entry) Reference() string {
	return e.Tool + ":" + e.Namespace.String() + "/" + e.Name
}

// Reader is the persistence port for the inventory projection. ListUnits returns
// the entries of one namespace, ordered by tool, then name. A namespace with
// no units reads as an empty list, as does a namespace that does not exist.
// GetUnit returns one live unit by its stable ID. QueryUnits returns one page
// of the units of a namespace, as UnitQuerier defines it. Implementations
// report storage failures without a dedicated value.
type Reader interface {
	UnitQuerier
	// ListUnits returns the inventory entries of ns, ordered by tool, then name.
	ListUnits(ctx context.Context, ns namespace.Name) ([]Entry, error)
	// GetUnit returns the live unit with the stable ID unitID. The ID is
	// unique in the installation, so the call names no namespace. It returns
	// ErrUnitNotFound for an unknown ID and for a deleted unit.
	GetUnit(ctx context.Context, unitID string) (Entry, error)
}

// ErrUnitNotFound reports that the inventory holds no live unit with the ID
// that a call names.
var ErrUnitNotFound = errors.New("unit not found")

// DeletedRetention is the default time that the server keeps a deleted unit
// before the final delete (ADR 47, decision 2). The setting
// --soft-delete-retention changes it.
const DeletedRetention = 30 * 24 * time.Hour

// Deletion holds what a soft delete of a unit records with it: the audit
// entry of the delete, whose time is the delete time, and the end of the
// retention period (ADR 47). The store writes both in the delete
// transaction, so the entry commits only with the delete (ADR 60, fail
// closed).
type Deletion struct {
	PurgeAfter time.Time
	Entry      audit.Entry
}

// DeletedEntry is one deleted unit in a namespace (ADR 47). The delete of a
// unit moves its entry from the live inventory to the deleted units, in the
// same transaction as the tool's own write. Tool and UnitID identify it,
// because a new unit can take its name at once. Name is the tool's own name of
// the unit at the time of the delete. PurgeAfter is the end of its retention
// period.
type DeletedEntry struct {
	DeletedAt  time.Time
	PurgeAfter time.Time
	Namespace  namespace.Name
	Tool       string
	UnitID     string
	Name       string
}

// DeletedFacts holds what the gates and the access limits need about a
// deleted unit: the collection that still holds it and its labels. A deleted
// unit keeps its membership of a collection until it is purged.
type DeletedFacts struct {
	// Collection is the name of the collection of the unit, or empty when the
	// unit has none.
	Collection string
	// Labels are the labels of the unit. The map is never nil.
	Labels map[string]string
}

// DeletedFactsReader is the read port of the facts of a deleted unit. A store
// that keeps deleted units implements it next to DeletedReader.
type DeletedFactsReader interface {
	// GetDeletedUnitFacts returns the facts of the deleted unit of ns with the
	// tool and the unit ID, or ErrNotFound. A live unit is not a deleted unit.
	GetDeletedUnitFacts(ctx context.Context, ns namespace.Name, tool, unitID string) (DeletedFacts, error)
}

// DeletedReader is the read port of the deleted units.
type DeletedReader interface {
	// ListDeletedUnits returns the deleted units of ns, ordered by tool, then
	// name, then unit ID.
	ListDeletedUnits(ctx context.Context, ns namespace.Name) ([]DeletedEntry, error)
	// QueryDeletedUnits returns one page of the deleted units that match q,
	// in the order of ListDeletedUnits, with the total count of every match.
	// PageDeleted defines the result for a list of entries.
	QueryDeletedUnits(ctx context.Context, q DeletedQuery) (DeletedPage, error)
	// GetDeletedUnit returns the deleted unit of ns with the tool and the unit
	// ID, or ErrNotFound. A live unit is not a deleted unit.
	GetDeletedUnit(ctx context.Context, ns namespace.Name, tool, unitID string) (DeletedEntry, error)
}

// ErrNotFound reports that a namespace has no deleted unit with the tool and
// the unit ID that a call names.
var ErrNotFound = errors.New("deleted unit not found")

// ErrNameTaken reports a restore under a name that a live unit of the same
// tool holds. The caller can send a new name.
var ErrNameTaken = errors.New("a live unit holds the name")

// ErrInvalidName reports a restore with a new name that the tool refuses.
var ErrInvalidName = errors.New("invalid unit name")

// Lifecycle restores and purges the deleted units of one tool (ADR 47). Each
// tool implements it with its own model, so shared code acts on a deleted
// unit by tool and unit ID only (ADR 12, ADR 13). Each call writes its audit
// entry e in the transaction of the change (ADR 60, fail closed); the tool
// stamps the time.
type Lifecycle interface {
	// Restore brings back the deleted unit unitID of ns under name, or under
	// its name before the delete when name is empty, and returns the tool's
	// own name of the live unit. name follows the tool's own name rules. It
	// returns ErrNotFound if ns has no deleted unit unitID, ErrNameTaken if a
	// live unit holds the name, and ErrInvalidName if the tool refuses name.
	Restore(ctx context.Context, ns namespace.Name, unitID, name string, e audit.Entry) (string, error)
	// Purge removes the deleted unit unitID of ns for good, with its state,
	// history and data key, and ends every current grant on the unit. It
	// returns ErrNotFound if ns has no deleted unit unitID.
	//
	// Open work for collections (#478, not merged yet): the purge must also
	// delete the membership row of the unit, and a restore of a unit whose
	// collection is gone must take a collection (409 without one, then the
	// checks of a move). Add both, with their conformance cases, after #478
	// lands.
	Purge(ctx context.Context, ns namespace.Name, unitID string, e audit.Entry) error
}

// BlobPurge is a purged unit whose blobs may still exist (ADR 47). A purge
// records it in its own transaction, and the caller deletes the blobs of the
// unit after the commit. A save that passed its checks before the purge can
// still write a blob after that delete, so only the retention sweep
// (Sweeper) finishes the record: it deletes the blobs again after
// BlobPurgeGrace, which is longer than any save.
type BlobPurge struct {
	PurgedAt time.Time
	// NamespaceID is the ID of the namespace that held the unit when the purge
	// wrote the record. The store keeps the ID, never the name, because a name
	// can come back for another namespace. It is empty only for a record that
	// an upgrade could not match to a namespace.
	NamespaceID namespace.ID
	Tool        string
	UnitID      string
}

// BlobPurgeCursor is the position after which a page of blob purge records
// starts: the order key of the last record of the page before.
type BlobPurgeCursor struct {
	PurgedAt time.Time
	Tool     string
	UnitID   string
}

// BlobPurges reads and finishes the records of purged units whose blobs may
// still exist.
type BlobPurges interface {
	// ListBlobPurges returns at most limit records, ordered by PurgedAt,
	// then tool, then unit ID. With a cursor, it returns only the records
	// after the cursor in that order.
	ListBlobPurges(ctx context.Context, after *BlobPurgeCursor, limit int) ([]BlobPurge, error)
	// FinishBlobPurge removes the record of the unit unitID of tool. A
	// missing record is not an error.
	FinishBlobPurge(ctx context.Context, tool, unitID string) error
}

// DefaultDeletedPageSize is the page size of a query that asks for none.
const DefaultDeletedPageSize = 50

// MaxDeletedPageSize is the largest page that a query gets.
const MaxDeletedPageSize = 500

// DeletedCursor is the position after which a page of deleted units starts:
// the order key of the last entry of the page before.
type DeletedCursor struct {
	Tool   string
	Name   string
	UnitID string
}

// DeletedQuery selects deleted units of one namespace. An empty Tool or
// NamePrefix and a zero PurgeBefore match every unit. PurgeBefore keeps the
// units whose retention ends at or before it, so that a client can ask which
// retention periods end in the next days. After starts the page after a
// cursor. Limit is the page size: zero means DefaultDeletedPageSize, and a
// value above MaxDeletedPageSize means MaxDeletedPageSize.
//
// When Restricted is true, only the units whose ID is in UnitIDs match, so
// the page, the cursor and the total cover only the units that a caller may
// read. An empty UnitIDs then matches nothing.
type DeletedQuery struct {
	PurgeBefore time.Time
	After       *DeletedCursor
	Namespace   namespace.Name
	Tool        string
	NamePrefix  string
	UnitIDs     []string
	Limit       int
	Restricted  bool
}

// PageSize returns the page size of q after the default and the cap.
func (q DeletedQuery) PageSize() int {
	switch {
	case q.Limit <= 0:
		return DefaultDeletedPageSize
	case q.Limit > MaxDeletedPageSize:
		return MaxDeletedPageSize
	default:
		return q.Limit
	}
}

// Matches reports whether e passes the filters of q. It ignores the cursor.
func (q DeletedQuery) Matches(e DeletedEntry) bool {
	return e.Namespace == q.Namespace &&
		(q.Tool == "" || e.Tool == q.Tool) &&
		strings.HasPrefix(e.Name, q.NamePrefix) &&
		(q.PurgeBefore.IsZero() || !e.PurgeAfter.After(q.PurgeBefore)) &&
		(!q.Restricted || slices.Contains(q.UnitIDs, e.UnitID))
}

// MatchesNothing reports whether q is restricted to no unit. A store can
// answer an empty page for it without a query.
func (q DeletedQuery) MatchesNothing() bool {
	return q.Restricted && len(q.UnitIDs) == 0
}

// DeletedPage is one page of deleted units. Total counts every unit that
// matches the filters, on every page. Next is the cursor of the next page, or
// nil on the last page.
type DeletedPage struct {
	Next    *DeletedCursor
	Entries []DeletedEntry
	Total   int
}

// cursorOf returns the order key of e.
func cursorOf(e DeletedEntry) DeletedCursor {
	return DeletedCursor{Tool: e.Tool, Name: e.Name, UnitID: e.UnitID}
}

// after reports whether e comes after c in the order tool, name, unit ID.
func after(e DeletedEntry, c DeletedCursor) bool {
	if e.Tool != c.Tool {
		return e.Tool > c.Tool
	}

	if e.Name != c.Name {
		return e.Name > c.Name
	}

	return e.UnitID > c.UnitID
}

// TrimDeletedPage builds a page from rows that a store read with a limit of
// size plus one: the extra row, if any, only shows that a next page exists.
func TrimDeletedPage(rows []DeletedEntry, size, total int) DeletedPage {
	page := DeletedPage{Next: nil, Entries: rows, Total: total}

	if len(rows) > size {
		page.Entries = rows[:size]
		c := cursorOf(rows[size-1])
		page.Next = &c
	}

	return page
}

// PageDeleted returns the page of q from entries, which must be in the order
// tool, name, unit ID. It defines what QueryDeletedUnits answers, so a store
// that holds its entries in memory can use it.
func PageDeleted(entries []DeletedEntry, q DeletedQuery) DeletedPage {
	page := DeletedPage{Next: nil, Entries: []DeletedEntry{}, Total: 0}
	size := q.PageSize()

	for i := range entries {
		e := entries[i]
		if !q.Matches(e) {
			continue
		}

		page.Total++

		if q.After != nil && !after(e, *q.After) {
			continue
		}

		if len(page.Entries) < size {
			page.Entries = append(page.Entries, e)

			continue
		}

		if page.Next == nil {
			c := cursorOf(page.Entries[len(page.Entries)-1])
			page.Next = &c
		}
	}

	return page
}
