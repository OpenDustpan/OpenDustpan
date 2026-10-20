package inventory

import (
	"cmp"
	"context"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// ChangeKind is what one row of the inventory change log records.
type ChangeKind string

// The kinds of a change log row.
const (
	// ChangeCreated is the create of a unit.
	ChangeCreated ChangeKind = "created"
	// ChangeChanged is a change of the fields of a unit, for example its
	// status or its resource count after a new state item.
	ChangeChanged ChangeKind = "changed"
	// ChangeRenamed is a rename of a unit. Its Name is the new name.
	ChangeRenamed ChangeKind = "renamed"
	// ChangeDeleted is the soft delete of a unit.
	ChangeDeleted ChangeKind = "deleted"
	// ChangeRestored is the restore of a deleted unit.
	ChangeRestored ChangeKind = "restored"
	// ChangePurged is the final delete of a unit.
	ChangePurged ChangeKind = "purged"
)

// Change is one row of the inventory change log: the fields of the entry of
// a unit just after one inventory write. Each tool appends one row in the
// same transaction as each inventory write that it makes. A row holds names
// and counts, never state, and no foreign key to the unit, so a purge keeps
// it. At is the time of the write. Seq is a sequence that the store assigns
// in the transaction of the write: in one namespace it increases in commit
// order, so it orders two rows that have the same time, or a time out of
// commit order. Two namespaces have their own sequences, and a sequence may
// skip a number.
type Change struct {
	At            time.Time
	Seq           int64
	Namespace     namespace.Name
	Tool          string
	UnitID        string
	Name          string
	DisplayName   string
	Status        string
	Collection    string
	Kind          ChangeKind
	ResourceCount *int64
}

// Entry returns the inventory entry that the row records. The lock of a
// unit is not history, so the entry has none. The collection of a unit is
// not history either: a move to another collection appends no row, so the
// collection in a row may be out of date, and the entry has none.
func (c Change) Entry() Entry {
	return Entry{
		ManualLock:    nil,
		LastChanged:   c.At,
		Namespace:     c.Namespace,
		Tool:          c.Tool,
		Name:          c.Name,
		UnitID:        c.UnitID,
		DisplayName:   c.DisplayName,
		Status:        c.Status,
		ResourceCount: c.ResourceCount,
		Lock:          nil,
		Collection:    "",
		Labels:        nil,
	}
}

// ChangeCursor is the position after which a page of change log rows
// starts: the sequence of the last row of the page before.
type ChangeCursor struct {
	Seq int64
}

// HistoryReader is the read port of the inventory change log.
type HistoryReader interface {
	// ListChanges returns at most limit rows of ns with a time at or
	// before until, ordered by sequence. With a cursor, it returns only the
	// rows with a sequence above the cursor. A store that
	// holds its rows in memory can answer UnitsAt with ReplayAt over them.
	ListChanges(
		ctx context.Context,
		ns namespace.Name,
		until time.Time,
		after *ChangeCursor,
		limit int,
	) ([]Change, error)
	// UnitsAt returns the inventory of ns at the time at, as ReplayAt
	// defines it. A namespace with no row at or before at reads as an empty
	// list, as does a namespace that does not exist.
	UnitsAt(ctx context.Context, ns namespace.Name, at time.Time) ([]Entry, error)
	// ViewAt returns the units of ns at the time at, as ReplayView defines
	// it: the live units with their current names, the units that are
	// soft-deleted now, and the count of units that a purge removed since.
	// A namespace with no row at or before at reads as an empty view, as
	// does a namespace that does not exist.
	ViewAt(ctx context.Context, ns namespace.Name, at time.Time) (UnitsView, error)
	// UnitNames returns the names of one unit at the time at and now, as
	// NamesAt defines it. A unit with no row, and a namespace that does not
	// exist, read as empty names.
	UnitNames(ctx context.Context, ns namespace.Name, tool, unitID string, at time.Time) (Names, error)
}

// UnitAt is one unit in the inventory at a time. Entry is the entry that the
// unit had at that time, with its name at that time. CurrentName is the name
// that the unit has now: after a soft delete, its name before the delete.
// DeletedAt is the time of the soft delete when the unit is soft-deleted
// now, and zero otherwise.
type UnitAt struct {
	DeletedAt   time.Time
	CurrentName string
	Entry       Entry
}

// Deleted reports whether the unit is soft-deleted now.
func (u UnitAt) Deleted() bool {
	return !u.DeletedAt.IsZero()
}

// UnitsView is the inventory of one namespace at a time, with the units that
// the caller may not name. Purged counts the units that existed at the time
// and that a purge removed since. The view never names them.
type UnitsView struct {
	Units  []UnitAt
	Purged int
}

// Names holds the name of one unit at a time and its name now. A name is
// empty when the change log does not know it: the unit has no row at or
// before the time, or a purge removed the unit.
type Names struct {
	AtTime  string
	Current string
}

// unitKey names a unit in the change log of one namespace.
type unitKey struct{ tool, id string }

// unitBounds holds, for one unit, its row with the highest sequence among the
// rows at or before a time, and its row with the highest sequence of all.
type unitBounds struct {
	atRow   *Change
	lastRow *Change
}

// add counts the row c, for the time at.
func (b *unitBounds) add(c *Change, at time.Time) {
	if b.lastRow == nil || c.Seq > b.lastRow.Seq {
		b.lastRow = c
	}

	if !c.At.After(at) && (b.atRow == nil || c.Seq > b.atRow.Seq) {
		b.atRow = c
	}
}

// boundsOf returns the bounds of each unit that has a row in changes, for the
// time at.
func boundsOf(changes []Change, at time.Time) map[unitKey]*unitBounds {
	out := map[unitKey]*unitBounds{}

	for n := range changes {
		c := &changes[n]
		key := unitKey{tool: c.Tool, id: c.UnitID}

		b, ok := out[key]
		if !ok {
			b = &unitBounds{atRow: nil, lastRow: nil}
			out[key] = b
		}

		b.add(c, at)
	}

	return out
}

// nameOf returns the name in the row c, or "" for no row and for a purged
// row.
func nameOf(c *Change) string {
	if c == nil || c.Kind == ChangePurged {
		return ""
	}

	return c.Name
}

// NamesAt returns the names of the unit tool and unitID at the time at, from
// the change log rows of one namespace, in any order. AtTime is the name in
// the row with the highest sequence among the rows with a time at or before
// at. Current is the name in the row with the highest sequence of all. A
// purged row gives an empty name. It defines what HistoryReader.UnitNames
// answers.
func NamesAt(changes []Change, tool, unitID string, at time.Time) Names {
	b, ok := boundsOf(changes, at)[unitKey{tool: tool, id: unitID}]
	if !ok {
		return Names{AtTime: "", Current: ""}
	}

	return Names{AtTime: nameOf(b.atRow), Current: nameOf(b.lastRow)}
}

// ReplayView returns the units at the time at from the change log rows
// changes of one namespace, in any order. A unit is in the view when its row
// with the highest sequence among the rows with a time at or before at is
// not ChangeDeleted or ChangePurged. Its CurrentName and DeletedAt come from
// its row with the highest sequence of all: a unit whose last row is
// ChangeDeleted is soft-deleted now, and a unit whose last row is
// ChangePurged is counted in Purged and not named. The units are ordered as
// ReplayAt orders them. It defines what HistoryReader.ViewAt answers.
func ReplayView(changes []Change, at time.Time) UnitsView {
	view := UnitsView{Units: []UnitAt{}, Purged: 0}

	for _, b := range boundsOf(changes, at) {
		if b.atRow == nil || b.lastRow == nil || b.atRow.Kind == ChangeDeleted || b.atRow.Kind == ChangePurged {
			continue
		}

		if b.lastRow.Kind == ChangePurged {
			view.Purged++

			continue
		}

		unit := UnitAt{DeletedAt: time.Time{}, CurrentName: b.lastRow.Name, Entry: b.atRow.Entry()}
		if b.lastRow.Kind == ChangeDeleted {
			unit.DeletedAt = b.lastRow.At
		}

		view.Units = append(view.Units, unit)
	}

	slices.SortFunc(view.Units, func(a, b UnitAt) int {
		return cmp.Or(
			cmp.Compare(a.Entry.Tool, b.Entry.Tool),
			cmp.Compare(a.Entry.Name, b.Entry.Name),
			cmp.Compare(a.Entry.UnitID, b.Entry.UnitID),
		)
	})

	return view
}

// ReplayAt returns the inventory at the time at from the change log rows
// changes of one namespace, in any order: for each unit, the entry of the
// row with the highest sequence among its rows with a time at or before at.
// The order of the sequences decides, not the order of the times, so rows
// with equal times, or with times out of commit order, replay in commit
// order. A unit whose row is ChangeDeleted or ChangePurged is not in the
// answer. The answer is ordered by tool, then name, as Reader.ListUnits
// orders the live inventory. It defines what HistoryReader.UnitsAt answers.
func ReplayAt(changes []Change, at time.Time) []Entry {
	type unitKey struct{ tool, id string }

	last := map[unitKey]Change{}

	for n := range changes {
		c := changes[n]
		if c.At.After(at) {
			continue
		}

		key := unitKey{tool: c.Tool, id: c.UnitID}
		if stored, ok := last[key]; !ok || c.Seq > stored.Seq {
			last[key] = c
		}
	}

	entries := []Entry{}

	for i := range last {
		c := last[i]
		if c.Kind == ChangeDeleted || c.Kind == ChangePurged {
			continue
		}

		entries = append(entries, c.Entry())
	}

	slices.SortFunc(entries, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Tool, b.Tool), cmp.Compare(a.Name, b.Name), cmp.Compare(a.UnitID, b.UnitID))
	})

	return entries
}
