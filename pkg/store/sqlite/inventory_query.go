package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// unitPageSelect reads the columns of ListInventory for one page of units.
// The namespace manual lock is the same for each unit of the page, so the
// statement reads it once and not once for each row. The first argument is
// the namespace of that read. The unit ID compares in the BINARY collation,
// the byte order of inventory.PageUnits, so the index on (namespace, unit_id)
// gives the rows in the page order.
const unitPageSelect = `SELECT i.namespace, i.tool, i.name, i.unit_id, i.display_name, i.last_changed_ms, i.status,
       i.resource_count, i.lock_holder, i.lock_since_ms, i.lock_operation,
       COALESCE(c.name, '') AS collection,
       ul.holder, ul.reason, ul.since_ms,
       cl.holder, cl.reason, cl.since_ms,
       nl.holder, nl.reason, nl.since_ms
FROM inventory AS i
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
WHERE i.namespace = ? AND i.unit_id > ?`

// unitLockedFilter keeps the units that have the locked state: a lock, as
// inventoryLock reads it, or a manual lock on the unit, its collection or its
// namespace.
const unitLockedFilter = ` AND (COALESCE(i.lock_holder, '') <> '' OR i.lock_since_ms IS NOT NULL
    OR COALESCE(i.lock_operation, '') <> ''
    OR ul.holder IS NOT NULL OR cl.holder IS NOT NULL OR nl.holder IS NOT NULL)`

// QueryUnits implements inventory.UnitQuerier with one page query and one
// label query for the units of the page. The filters are in the WHERE
// clause, so the statement reads only the rows of the page and the rows that
// the filters skip.
func (s *Store) QueryUnits(ctx context.Context, q inventory.UnitQuery) ([]inventory.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if q.MatchesNothing() {
		return []inventory.Entry{}, nil
	}

	query, args := unitPageQuery(q)

	rows, err := s.conn.QueryContext(ctx, query, args...)
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

	ids := make([]string, len(page))
	byUnit := make(map[string]map[string]string, len(page))

	for i := range page {
		ids[i] = page[i].UnitID
		byUnit[page[i].UnitID] = map[string]string{}
	}

	labels, err := s.queries.ListInventoryLabelsOfUnits(ctx, ids)
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
// value is a placeholder.
func unitPageQuery(q inventory.UnitQuery) (string, []any) {
	var b strings.Builder

	b.WriteString(unitPageSelect)

	ns := q.Namespace.String()
	args := []any{ns, ns, q.After}

	for _, l := range q.Labels {
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

	b.WriteString(" ORDER BY i.unit_id LIMIT ?")

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
