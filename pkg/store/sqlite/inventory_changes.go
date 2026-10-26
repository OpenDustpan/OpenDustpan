package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ inventory.HistoryReader = (*Store)(nil)

// appendInventoryChange appends one row to the inventory change log of the
// namespace of c, in the caller's transaction. It takes the sequence from the
// counter row of the namespace, so the caller runs it as the last statement of
// its transaction. It ignores the Collection and the Seq of c: the row holds the
// collection that holds the unit now, and the sequence of the counter. It
// returns namespace.ErrNotFound when the namespace does not exist.
func appendInventoryChange(ctx context.Context, q *db.Queries, c inventory.Change) error {
	_, err := insertInventoryChange(ctx, q, c)

	return err
}

// insertInventoryChange is appendInventoryChange that also returns the
// sequence it took.
func insertInventoryChange(ctx context.Context, q *db.Queries, c inventory.Change) (int64, error) {
	nsID, err := q.GetNamespaceID(ctx, c.Namespace.String())
	if errors.Is(err, sql.ErrNoRows) {
		return 0, namespace.ErrNotFound
	}

	if err != nil {
		return 0, fmt.Errorf("select namespace ID: %w", err)
	}

	collection, err := q.GetUnitCollectionName(ctx, c.UnitID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("select collection of unit: %w", err)
	}

	seq, err := q.NextInventoryChangeSeq(ctx, nsID)
	if err != nil {
		return 0, fmt.Errorf("take change log sequence: %w", err)
	}

	err = q.InsertInventoryChange(ctx, db.InsertInventoryChangeParams{
		NamespaceID:   nsID,
		Seq:           seq,
		AtMs:          codec.Millis(c.At),
		Tool:          c.Tool,
		UnitID:        c.UnitID,
		Name:          c.Name,
		DisplayName:   c.DisplayName,
		Status:        c.Status,
		Collection:    collection,
		Kind:          string(c.Kind),
		ResourceCount: sql.NullInt64{Int64: 0, Valid: false},
	})
	if err != nil {
		return 0, fmt.Errorf("insert inventory change: %w", err)
	}

	return seq, nil
}

// unitWrite names one inventory write of a unit: its stable ID, the time of
// the write in Unix milliseconds, and the kind of its change log row.
type unitWrite struct {
	unitID string
	atMs   int64
	kind   inventory.ChangeKind
}

// unitChange returns the change log row of the write w of a unit of tool that
// names it name.
func unitChange(ns namespace.Name, tool, name string, w unitWrite) inventory.Change {
	return inventory.Change{
		At:            codec.FromMillis(w.atMs),
		Seq:           0,
		Namespace:     ns,
		Tool:          tool,
		UnitID:        w.unitID,
		Name:          name,
		DisplayName:   name,
		Status:        "",
		Collection:    "",
		Kind:          w.kind,
		ResourceCount: nil,
	}
}

// changeOf converts a change log row into a domain row of the namespace ns.
func changeOf(ns namespace.Name, row db.InventoryChange) inventory.Change {
	var count *int64

	if row.ResourceCount.Valid {
		v := row.ResourceCount.Int64
		count = &v
	}

	return inventory.Change{
		At:            codec.FromMillis(row.AtMs),
		Seq:           row.Seq,
		Namespace:     ns,
		Tool:          row.Tool,
		UnitID:        row.UnitID,
		Name:          row.Name,
		DisplayName:   row.DisplayName,
		Status:        row.Status,
		Collection:    row.Collection,
		Kind:          inventory.ChangeKind(row.Kind),
		ResourceCount: count,
	}
}

// changesOf converts change log rows into domain rows of the namespace ns.
func changesOf(ns namespace.Name, rows []db.InventoryChange) []inventory.Change {
	out := make([]inventory.Change, 0, len(rows))
	for i := range rows {
		out = append(out, changeOf(ns, rows[i]))
	}

	return out
}

// namespaceID returns the stable ID of the namespace called ns, and false
// when no such namespace exists.
func (s *Store) namespaceID(ctx context.Context, ns namespace.Name) (string, bool, error) {
	id, err := s.queries.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select namespace ID: %w", err)
	}

	return id, true, nil
}

// ListChanges implements inventory.HistoryReader.
func (s *Store) ListChanges(
	ctx context.Context, ns namespace.Name, until time.Time, after *inventory.ChangeCursor, limit int,
) ([]inventory.Change, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, found, err := s.namespaceID(ctx, ns)
	if err != nil || !found || limit <= 0 {
		return []inventory.Change{}, err
	}

	var afterSeq int64
	if after != nil {
		afterSeq = after.Seq
	}

	rows, err := s.queries.ListInventoryChanges(ctx, db.ListInventoryChangesParams{
		NamespaceID: id, UntilMs: codec.Millis(until), AfterSeq: afterSeq, PageLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select inventory changes: %w", err)
	}

	return changesOf(ns, rows), nil
}

// boundRows reads, for each unit of ns, the row at the time at and the last
// row, with the one query that the namespace ID allows.
func (s *Store) boundRows(ctx context.Context, ns namespace.Name, at time.Time) ([]inventory.Change, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, found, err := s.namespaceID(ctx, ns)
	if err != nil || !found {
		return []inventory.Change{}, err
	}

	rows, err := s.queries.ListInventoryChangeBounds(ctx, db.ListInventoryChangeBoundsParams{
		NamespaceID: id, AtMs: codec.Millis(at),
	})
	if err != nil {
		return nil, fmt.Errorf("select inventory change bounds: %w", err)
	}

	return changesOf(ns, rows), nil
}

// UnitsAt implements inventory.HistoryReader.
func (s *Store) UnitsAt(ctx context.Context, ns namespace.Name, at time.Time) ([]inventory.Entry, error) {
	rows, err := s.boundRows(ctx, ns, at)
	if err != nil {
		return nil, err
	}

	return inventory.ReplayAt(rows, at), nil
}

// ViewAt implements inventory.HistoryReader.
func (s *Store) ViewAt(ctx context.Context, ns namespace.Name, at time.Time) (inventory.UnitsView, error) {
	rows, err := s.boundRows(ctx, ns, at)
	if err != nil {
		return inventory.UnitsView{}, err
	}

	return inventory.ReplayView(rows, at), nil
}

// UnitNames implements inventory.HistoryReader.
func (s *Store) UnitNames(
	ctx context.Context, ns namespace.Name, tool, unitID string, at time.Time,
) (inventory.Names, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, found, err := s.namespaceID(ctx, ns)
	if err != nil || !found {
		return inventory.Names{AtTime: "", Current: ""}, err
	}

	rows, err := s.queries.ListInventoryUnitBounds(ctx, db.ListInventoryUnitBoundsParams{
		NamespaceID: id, Tool: tool, UnitID: unitID, AtMs: codec.Millis(at),
	})
	if err != nil {
		return inventory.Names{AtTime: "", Current: ""}, fmt.Errorf("select inventory unit bounds: %w", err)
	}

	return inventory.NamesAt(changesOf(ns, rows), tool, unitID, at), nil
}

// namespaceID returns the stable ID of the namespace called ns, and false
// when no such namespace exists.
func namespaceID(ctx context.Context, q *db.Queries, ns namespace.Name) (string, bool, error) {
	id, err := q.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select namespace ID: %w", err)
	}

	return id, true, nil
}
