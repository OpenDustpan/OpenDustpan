package inventory

import (
	"cmp"
	"context"
	"slices"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxUnitQueryLimit is the largest number of entries that one QueryUnits call
// reads.
const MaxUnitQueryLimit = 1000

// UnitLabel is one label that a unit must have: a key and its exact value.
type UnitLabel struct {
	Key   string
	Value string
}

// UnitName is one unit of a namespace by the tool and the tool's own name.
type UnitName struct {
	Tool string
	Name string
}

// UnitQuery selects live units of one namespace in the byte order of the unit
// ID. After keeps the units whose ID is above it, so a caller pages with the
// ID of the last unit that it read. An empty After starts at the first unit.
// Limit is the largest number of entries to read: zero or less means one,
// and a value above MaxUnitQueryLimit means MaxUnitQueryLimit.
//
// A unit matches when it has each label in Labels. When RestrictUnits is
// true, it must also be one of Units: an empty Units then matches nothing.
// When RestrictStates is true, it must have one of States: an empty States
// then matches nothing.
type UnitQuery struct {
	Namespace      namespace.Name
	After          string
	Labels         []UnitLabel
	Units          []UnitName
	States         []State
	Limit          int
	RestrictUnits  bool
	RestrictStates bool
}

// UnitQuerier is the read port of one page of the inventory projection.
type UnitQuerier interface {
	// QueryUnits returns at most q.PageLimit() live units of q.Namespace that
	// match q and whose ID is above q.After, in the byte order of the unit ID.
	// PageUnits defines the result for a list of entries. A namespace that
	// does not exist reads as an empty list. Implementations report storage
	// failures without a dedicated value.
	QueryUnits(ctx context.Context, q UnitQuery) ([]Entry, error)
}

// PageLimit returns the number of entries that q reads, after the floor and
// the cap.
func (q UnitQuery) PageLimit() int {
	return min(max(q.Limit, 1), MaxUnitQueryLimit)
}

// MatchesNothing reports whether no unit can match q. A store can answer an
// empty list for it without a query.
func (q UnitQuery) MatchesNothing() bool {
	if q.RestrictUnits && len(q.Units) == 0 {
		return true
	}

	return q.RestrictStates && !slices.ContainsFunc(q.States, derived)
}

// LockedOnly reports whether q keeps only the units that have the locked
// state. A store reads the lock columns for it. The locked state is the only
// state that the server derives so far, so a state filter keeps the locked
// units or none.
func (q UnitQuery) LockedOnly() bool {
	return q.RestrictStates && slices.Contains(q.States, StateLocked)
}

// Matches reports whether e passes the filters of q. It ignores After and
// Limit.
func (q UnitQuery) Matches(e Entry) bool {
	if e.Namespace != q.Namespace {
		return false
	}

	for _, l := range q.Labels {
		if got, ok := e.Labels[l.Key]; !ok || got != l.Value {
			return false
		}
	}

	if q.RestrictUnits && !slices.Contains(q.Units, UnitName{Tool: e.Tool, Name: e.Name}) {
		return false
	}

	if q.RestrictStates && !slices.ContainsFunc(q.States, e.HasState) {
		return false
	}

	return true
}

// PageUnits returns the result of q over entries, in any order. It defines
// what QueryUnits answers, so a store that holds its entries in memory can use
// it.
func PageUnits(entries []Entry, q UnitQuery) []Entry {
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b Entry) int { return cmp.Compare(a.UnitID, b.UnitID) })

	out := []Entry{}
	limit := q.PageLimit()

	for i := range sorted {
		if len(out) == limit {
			break
		}

		if sorted[i].UnitID > q.After && q.Matches(sorted[i]) {
			out = append(out, sorted[i])
		}
	}

	return out
}
