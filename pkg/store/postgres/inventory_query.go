package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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
// statement reads it once and not once for each row.
const unitPageJoins = `
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
LEFT JOIN manual_locks AS ul ON ul.kind = 'unit' AND ul.object_id = i.unit_id
LEFT JOIN manual_locks AS cl ON cl.kind = 'collection' AND cl.object_id = c.id
LEFT JOIN (
    SELECT l.holder, l.reason, l.since_ms
    FROM namespaces AS n
    JOIN manual_locks AS l ON l.kind = 'namespace' AND l.object_id = n.id
    WHERE n.name = $1
) AS nl ON TRUE
WHERE `

// unitLockedFilter keeps the units that have the locked state: a lock, as
// inventoryLock reads it, or a manual lock on the unit, its collection or its
// namespace.
const unitLockedFilter = ` AND (COALESCE(i.lock_holder, '') <> '' OR i.lock_since_ms IS NOT NULL
    OR COALESCE(i.lock_operation, '') <> ''
    OR ul.holder IS NOT NULL OR cl.holder IS NOT NULL OR nl.holder IS NOT NULL)`

// QueryUnits implements inventory.UnitQuerier with one page query and one
// label query for the units of the page. The filters are in the WHERE clause,
// so the statement reads only the rows of the page and the rows that the
// filters skip.
func (s *Store) QueryUnits(ctx context.Context, q inventory.UnitQuery) ([]inventory.Entry, error) {
	if q.MatchesNothing() {
		return []inventory.Entry{}, nil
	}

	query, args := unitPageQuery(q)

	var page []inventory.Entry

	// One snapshot, so that a restore swap between the two statements cannot
	// give the units of one state with the labels of another.
	err := s.inSnapshotConn(ctx, func(tx pgx.Tx) error {
		var err error

		page, err = queryUnitPage(ctx, s.queries.WithTx(tx), tx, query, args)

		return err
	})
	if err != nil {
		return nil, err
	}

	return page, nil
}

// queryUnitPage runs the page statement and the label statement of QueryUnits
// on tx.
func queryUnitPage(ctx context.Context, q *db.Queries, tx pgx.Tx, query string, args []any) ([]inventory.Entry, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select inventory page: %w", err)
	}

	page, err := pgx.CollectRows(rows, scanUnitPageRow)
	if err != nil {
		return nil, fmt.Errorf("read inventory page: %w", err)
	}

	if len(page) == 0 {
		return []inventory.Entry{}, nil
	}

	ids := make([]string, len(page))
	for i := range page {
		ids[i] = page[i].UnitID
	}

	labels, err := q.ListInventoryLabelsOfUnits(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("select inventory page labels: %w", err)
	}

	byUnit := make(map[string]map[string]string, len(page))
	for i := range page {
		byUnit[page[i].UnitID] = map[string]string{}
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
// value is a placeholder. The unit ID compares in the "C" collation, the byte
// order of inventory.PageUnits. With no label filter, the index on
// (namespace, unit_id COLLATE "C") of the inventory gives the rows in the page
// order. With a label filter, the first label drives the read: the index on
// (namespace, label_key, label_value, unit_id COLLATE "C") of the labels gives
// the units with the label in the page order. The statement checks the other
// labels on each of these units, so when the other labels match few or none of
// them, a page can read every unit that has the first label.
func unitPageQuery(q inventory.UnitQuery) (string, []any) {
	var b strings.Builder

	args := []any{q.Namespace.String(), q.After}
	labels := q.Labels
	order := "i.unit_id"

	b.WriteString(unitPageColumns)

	if len(labels) > 0 {
		args = append(args, labels[0].Key, labels[0].Value)
		order = "lb0.unit_id"

		b.WriteString("\nFROM inventory_labels AS lb0\nJOIN inventory AS i ON i.unit_id = lb0.unit_id")
		b.WriteString(unitPageJoins)
		b.WriteString(`lb0.namespace = $1 AND lb0.label_key = $3 AND lb0.label_value = $4
    AND lb0.unit_id COLLATE "C" > $2 AND i.namespace = $1`)

		labels = labels[1:]
	} else {
		b.WriteString("\nFROM inventory AS i")
		b.WriteString(unitPageJoins)
		b.WriteString(`i.namespace = $1 AND i.unit_id COLLATE "C" > $2`)
	}

	for _, l := range labels {
		args = append(args, l.Key, l.Value)
		fmt.Fprintf(&b, ` AND EXISTS (SELECT 1 FROM inventory_labels AS lb
    WHERE lb.unit_id = i.unit_id AND lb.label_key = $%d AND lb.label_value = $%d)`, len(args)-1, len(args))
	}

	if q.RestrictUnits {
		tools := make([]string, len(q.Units))
		names := make([]string, len(q.Units))

		for i, u := range q.Units {
			tools[i], names[i] = u.Tool, u.Name
		}

		args = append(args, tools, names)
		fmt.Fprintf(
			&b,
			` AND (i.tool, i.name) IN (SELECT u.tool, u.name FROM unnest($%d::text[], $%d::text[]) AS u (tool, name))`,
			len(args)-1,
			len(args),
		)
	}

	if q.LockedOnly() {
		b.WriteString(unitLockedFilter)
	}

	args = append(args, q.PageLimit())
	fmt.Fprintf(&b, ` ORDER BY %s COLLATE "C" LIMIT $%d`, order, len(args))

	return b.String(), args
}

// scanUnitPageRow reads one row of the page statement into an entry with no
// labels.
func scanUnitPageRow(row pgx.CollectableRow) (inventory.Entry, error) {
	var r db.ListInventoryRow

	err := row.Scan(
		&r.Namespace, &r.Tool, &r.Name, &r.UnitID, &r.DisplayName, &r.LastChangedMs, &r.Status,
		&r.ResourceCount, &r.LockHolder, &r.LockSinceMs, &r.LockOperation,
		&r.LastReleaseLockID, &r.LastReleaseHolder, &r.LastReleaseBy, &r.LastReleaseKind, &r.LastReleaseAtMs,
		&r.Collection,
		&r.UnitManualHolder, &r.UnitManualReason, &r.UnitManualSinceMs,
		&r.CollectionManualHolder, &r.CollectionManualReason, &r.CollectionManualSinceMs,
		&r.NamespaceManualHolder, &r.NamespaceManualReason, &r.NamespaceManualSinceMs,
	)
	if err != nil {
		return inventory.Entry{}, fmt.Errorf("scan inventory page row: %w", err)
	}

	return inventoryEntry(r), nil
}
