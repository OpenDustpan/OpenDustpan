// Package placementstore holds the logic of the placement rule tables that the
// SQL adapters share: the order of the list and the audit text of a removal.
// The adapters keep their own queries.
package placementstore

import (
	"fmt"
	"slices"
	"strconv"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
)

// Insert returns ids with id added at the 1-based position pos. The position
// must be from 1 to len(ids)+1, otherwise it returns collection.ErrInvalidPosition.
func Insert(ids []string, id string, pos int) ([]string, error) {
	if pos < 1 || pos > len(ids)+1 {
		return nil, fmt.Errorf("%w: %d is not from 1 to %d", collection.ErrInvalidPosition, pos, len(ids)+1)
	}

	return slices.Insert(slices.Clone(ids), pos-1, id), nil
}

// Move returns ids with id taken out and put at the 1-based position pos. The
// position must be from 1 to len(ids), otherwise it returns
// collection.ErrInvalidPosition. It returns collection.ErrPlacementRuleNotFound
// when ids does not hold id.
func Move(ids []string, id string, pos int) ([]string, error) {
	idx := slices.Index(ids, id)
	if idx < 0 {
		return nil, collection.ErrPlacementRuleNotFound
	}

	if pos < 1 || pos > len(ids) {
		return nil, fmt.Errorf("%w: %d is not from 1 to %d", collection.ErrInvalidPosition, pos, len(ids))
	}

	rest := slices.Delete(slices.Clone(ids), idx, idx+1)

	return slices.Insert(rest, pos-1, id), nil
}

// Remove returns ids without id. It returns collection.ErrPlacementRuleNotFound
// when ids does not hold id.
func Remove(ids []string, id string) ([]string, error) {
	idx := slices.Index(ids, id)
	if idx < 0 {
		return nil, collection.ErrPlacementRuleNotFound
	}

	return slices.Delete(slices.Clone(ids), idx, idx+1), nil
}

// Removed names a rule that the delete of its collection removed.
type Removed struct {
	ID    string
	Tool  string
	Match string
}

// RemovedChanges returns the audit changes that name the removed rules: one
// change for each rule, keyed by "placement-rule:<id>", with the tool and the
// patterns as the old value and "removed" as the new value. The room is the
// number of changes that the entry still accepts. A list above the room ends
// with one change that gives the count of the rules that it leaves out. It
// returns nil when the room is less than 1.
func RemovedChanges(removed []Removed, room int) []audit.Change {
	if room < 1 || len(removed) == 0 {
		return nil
	}

	limit := room - 1
	out := make([]audit.Change, 0, min(len(removed), room))

	for i, r := range removed {
		if i == limit && len(removed) > room {
			more := strconv.Itoa(len(removed) - limit)

			out = append(out, audit.Change{
				Key: "placement-rule:more", Old: nil, New: more, Layer: "", TakesEffect: "",
			})

			break
		}

		old := r.Tool + " " + r.Match

		out = append(out, audit.Change{
			Key: "placement-rule:" + r.ID, Old: &old, New: "removed", Layer: "", TakesEffect: "",
		})
	}

	return out
}
