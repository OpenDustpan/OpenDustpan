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
// in the transaction of the write: it increases in commit order across the
// installation, so it orders two rows that have the same time, or a time
// out of commit order.
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
