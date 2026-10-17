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
// it. At is the time of the write.
type Change struct {
	At            time.Time
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
// unit is not history, so the entry has none.
func (c Change) Entry() Entry {
	return Entry{
		LastChanged:   c.At,
		Namespace:     c.Namespace,
		Tool:          c.Tool,
		Name:          c.Name,
		UnitID:        c.UnitID,
		DisplayName:   c.DisplayName,
		Status:        c.Status,
		ResourceCount: c.ResourceCount,
		Lock:          nil,
		Collection:    c.Collection,
	}
}

// ChangeCursor is the position after which a page of change log rows
// starts: the order key of the last row of the page before.
type ChangeCursor struct {
	At     time.Time
	Tool   string
	UnitID string
}

// HistoryReader is the read port of the inventory change log.
type HistoryReader interface {
	// ListChanges returns at most limit rows of ns with a time at or
	// before until, ordered by time, then tool, then unit ID. With a cursor,
	// it returns only the rows after the cursor in that order. A store that
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
}

// ReplayAt returns the inventory at the time at from the change log rows
// changes of one namespace, in any order: for each unit, the entry of its
// last row at or before at. A unit whose last such row is ChangeDeleted or
// ChangePurged is not in the answer. Two rows of one unit at the same time
// keep the order of changes. The answer is ordered by tool, then name, as
// Reader.ListUnits orders the live inventory. It defines what
// HistoryReader.UnitsAt answers.
func ReplayAt(changes []Change, at time.Time) []Entry {
	type unitKey struct{ tool, id string }

	ordered := slices.Clone(changes)
	slices.SortStableFunc(ordered, func(a, b Change) int { return a.At.Compare(b.At) })

	last := map[unitKey]Change{}

	for n := range ordered {
		c := ordered[n]
		if c.At.After(at) {
			break
		}

		last[unitKey{tool: c.Tool, id: c.UnitID}] = c
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
