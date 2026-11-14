package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// unitPageColumns are the columns of ListInventory for one page of units.
const unitPageColumns = `SELECT i.namespace, i.tool, i.name, i.unit_id, i.display_name, i.last_changed_ms, i.status,
       i.resource_count, i.lock_holder, i.lock_since_ms, i.lock_operation,
       i.last_release_lock_id, i.last_release_holder, i.last_release_by, i.last_release_kind,
       i.last_release_at_ms,
       COALESCE(c.name, '') AS collection,
       ul.holder, ul.reason, ul.since_ms,
       cl.holder, cl.reason, cl.since_ms,
       nl.holder, nl.reason, nl.since_ms`

// unitPageJoins are the joins of ListInventory after the table of the units.
// The namespace manual lock is the same for each unit of the page, so the
// statement reads it once and not once for each row. Its argument is the
// namespace.
const unitPageJoins = `
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
LEFT JOIN manual_locks AS ul ON ul.kind = 'unit' AND ul.object_id = i.unit_id
LEFT JOIN manual_locks AS cl ON cl.kind = 'collection' AND cl.object_id = c.id
LEFT JOIN (
    SELECT l.holder, l.reason, l.since_ms
    FROM namespaces AS n
    JOIN manual_locks AS l ON l.kind = 'namespace' AND l.object_id = n.id
    WHERE n.name = ?
) AS nl ON 1
WHERE `

// unitLockedFilter keeps the units that have the locked state: a lock, as
// inventoryLock reads it, or a manual lock on the unit, its collection or its
// namespace.
const unitLockedFilter = ` AND (COALESCE(i.lock_holder, '') <> '' OR i.lock_since_ms IS NOT NULL
    OR COALESCE(i.lock_operation, '') <> ''
    OR ul.holder IS NOT NULL OR cl.holder IS NOT NULL OR nl.holder IS NOT NULL)`

// QueryUnits implements inventory.UnitQuerier with one page query and one
// label query for the units of the page, in one read transaction. The filters
// are in the WHERE clause, so the statement reads only the rows of the page
// and the rows that the filters skip.
func (s *Store) QueryUnits(ctx context.Context, q inventory.UnitQuery) ([]inventory.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if q.MatchesNothing() {
		return []inventory.Entry{}, nil
	}

	tx, err := s.conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelDefault, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin read transaction: %w", err)
	}

	page, err := s.queryUnitPage(ctx, tx, q)
	if err != nil {
		return nil, errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit read transaction: %w", err)
	}

	return page, nil
}

// queryUnitPage reads the page of q and its labels in tx, so the labels
// belong to the same state as the rows that the label filter selected.
func (s *Store) queryUnitPage(ctx context.Context, tx *sql.Tx, q inventory.UnitQuery) ([]inventory.Entry, error) {
	query, args := unitPageQuery(q)

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select inventory page: %w", err)
	}

	page, err := scanUnitPage(rows)
	if err != nil {
		return nil, err
	}

	if len(page) == 0 {
		return page, nil
	}

	if s.betweenPageReads != nil {
		s.betweenPageReads(ctx)
	}

	ids := make([]string, len(page))
	byUnit := make(map[string]map[string]string, len(page))

	for i := range page {
		ids[i] = page[i].UnitID
		byUnit[page[i].UnitID] = map[string]string{}
	}

	labels, err := s.queries.WithTx(tx).ListInventoryLabelsOfUnits(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("select inventory page labels: %w", err)
	}

	for _, l := range labels {
		if m, ok := byUnit[l.UnitID]; ok {
			m[l.LabelKey] = l.LabelValue
		}
	}

	for i := range page {
		page[i].Labels = byUnit[page[i].UnitID]
	}

	return page, nil
}

// unitPageQuery returns the page statement of q and its arguments. Every
// value is a placeholder. The unit ID compares in the BINARY collation, the
// byte order of inventory.PageUnits. With no label filter, the index on
// (namespace, unit_id) of the inventory gives the rows in the page order.
// With a label filter, the first label drives the read: the index on
// (namespace, label_key, label_value, unit_id) of the labels gives the units
// with the label in the page order. The statement checks the other labels on
// each of these units, so when the other labels match few or none of them, a
// page can read every unit that has the first label.
func unitPageQuery(q inventory.UnitQuery) (string, []any) {
	var b strings.Builder

	ns := q.Namespace.String()
	labels := q.Labels
	order := "i.unit_id"

	var args []any

	b.WriteString(unitPageColumns)

	if len(labels) > 0 {
		order = "lb0.unit_id"

		b.WriteString("\nFROM inventory_labels AS lb0\nJOIN inventory AS i ON i.unit_id = lb0.unit_id")
		b.WriteString(unitPageJoins)
		b.WriteString(`lb0.namespace = ? AND lb0.label_key = ? AND lb0.label_value = ?
    AND lb0.unit_id > ? AND i.namespace = ?`)

		args = []any{ns, ns, labels[0].Key, labels[0].Value, q.After, ns}
		labels = labels[1:]
	} else {
		b.WriteString("\nFROM inventory AS i")
		b.WriteString(unitPageJoins)
		b.WriteString("i.namespace = ? AND i.unit_id > ?")

		args = []any{ns, ns, q.After}
	}

	for _, l := range labels {
		b.WriteString(` AND EXISTS (SELECT 1 FROM inventory_labels AS lb
    WHERE lb.unit_id = i.unit_id AND lb.label_key = ? AND lb.label_value = ?)`)

		args = append(args, l.Key, l.Value)
	}

	if q.RestrictUnits {
		terms := make([]string, len(q.Units))

		for i, u := range q.Units {
			terms[i] = "(i.tool = ? AND i.name = ?)"
			args = append(args, u.Tool, u.Name)
		}

		b.WriteString(" AND (" + strings.Join(terms, " OR ") + ")")
	}

	if q.LockedOnly() {
		b.WriteString(unitLockedFilter)
	}

	b.WriteString(" ORDER BY " + order + " LIMIT ?")

	args = append(args, q.PageLimit())

	return b.String(), args
}

// scanUnitPage reads every row of the page statement into entries with no
// labels, and closes rows.
func scanUnitPage(rows *sql.Rows) ([]inventory.Entry, error) {
	out, err := readUnitPage(rows)
	closeErr := rows.Close()

	if err != nil {
		return nil, err
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close inventory page: %w", closeErr)
	}

	return out, nil
}

// readUnitPage reads every row of the page statement into entries with no
// labels.
func readUnitPage(rows *sql.Rows) ([]inventory.Entry, error) {
	out := []inventory.Entry{}

	for rows.Next() {
		var r db.ListInventoryRow

		err := rows.Scan(
			&r.Namespace, &r.Tool, &r.Name, &r.UnitID, &r.DisplayName, &r.LastChangedMs, &r.Status,
			&r.ResourceCount, &r.LockHolder, &r.LockSinceMs, &r.LockOperation,
			&r.LastReleaseLockID, &r.LastReleaseHolder, &r.LastReleaseBy, &r.LastReleaseKind, &r.LastReleaseAtMs,
			&r.Collection,
			&r.UnitManualHolder, &r.UnitManualReason, &r.UnitManualSinceMs,
			&r.CollectionManualHolder, &r.CollectionManualReason, &r.CollectionManualSinceMs,
			&r.NamespaceManualHolder, &r.NamespaceManualReason, &r.NamespaceManualSinceMs,
		)
		if err != nil {
			return nil, fmt.Errorf("scan inventory page row: %w", err)
		}

		out = append(out, inventoryEntry(r))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read inventory page: %w", err)
	}

	return out, nil
}
