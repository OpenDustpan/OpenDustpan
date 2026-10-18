package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	_ inventory.Reader     = (*Store)(nil)
	_ inventory.BlobPurges = (*Store)(nil)
)

// inventoryTool names the Pulumi CLI in inventory entries.
const inventoryTool = "pulumi"

// inventoryToolOpenTofu names OpenTofu in inventory entries.
const inventoryToolOpenTofu = "opentofu"

// stackUnitName returns the tool's own name for a stack: project/stack.
func stackUnitName(ref stack.Ref) string {
	return ref.Project.String() + "/" + ref.Stack.String()
}

// publishStackInventory writes the Pulumi entry for ref with the given unit
// ID and change time, and appends the change log row of the kind kind. Status
// and resource count stay unknown: the Pulumi CLI reports no status
// vocabulary and the server never reads deployments. The lock columns stay
// untouched: only UpdateInventoryLock changes them, in the same transaction
// as the update start or end that they describe. The caller runs it as the
// last write of its transaction, because the sequence of the change log takes
// a row lock until the commit.
func publishStackInventory(ctx context.Context, q *db.Queries, ref stack.Ref, w unitWrite) error {
	if err := upsertStackInventory(ctx, q, ref, w.unitID, w.atMs); err != nil {
		return err
	}

	return appendStackChange(ctx, q, ref, w)
}

// appendStackChange appends the change log row of the stack ref.
func appendStackChange(ctx context.Context, q *db.Queries, ref stack.Ref, w unitWrite) error {
	return appendInventoryChange(ctx, q, unitChange(ref.Namespace, inventoryTool, stackUnitName(ref), w))
}

// upsertStackInventory writes the Pulumi entry for ref and appends no change
// log row. A caller that writes the lock after the entry uses it, so that the
// change log row is the last write of its transaction.
func upsertStackInventory(
	ctx context.Context, q *db.Queries, ref stack.Ref, unitID string, lastChangedMs int64,
) error {
	name := stackUnitName(ref)

	err := q.UpsertInventory(ctx, db.UpsertInventoryParams{
		Namespace:     ref.Namespace.String(),
		Tool:          inventoryTool,
		Name:          name,
		UnitID:        unitID,
		DisplayName:   name,
		LastChangedMs: lastChangedMs,
		Status:        "",
		ResourceCount: pgtype.Int8{Int64: 0, Valid: false},
	})
	if err != nil {
		return fmt.Errorf("upsert inventory entry: %w", err)
	}

	return nil
}

// removeStackInventory removes the Pulumi entry for ref.
func removeStackInventory(ctx context.Context, q *db.Queries, ref stack.Ref) error {
	err := q.DeleteInventory(ctx, db.DeleteInventoryParams{
		Namespace: ref.Namespace.String(),
		Tool:      inventoryTool,
		Name:      stackUnitName(ref),
	})
	if err != nil {
		return fmt.Errorf("delete inventory entry: %w", err)
	}

	return nil
}

// inventoryLockKey names one inventory row: its namespace, tool and name.
type inventoryLockKey struct {
	namespace string
	tool      string
	name      string
}

// stackInventoryKey returns the inventory key of the stack of u.
func stackInventoryKey(u stack.Update) inventoryLockKey {
	return inventoryLockKey{namespace: u.Ref.Namespace.String(), tool: inventoryTool, name: stackUnitName(u.Ref)}
}

// writeInventoryLock publishes the lock change of one unit in the caller's
// transaction: lock to set it, nil to clear it. The holder is the subject of
// the principal that holds the lock, never a token or a secret.
func writeInventoryLock(
	ctx context.Context, q *db.Queries, key inventoryLockKey, lock *inventory.Lock,
) error {
	// The lock columns stay NULL to clear; the set below fills them when
	// the unit is locked.
	params := db.UpdateInventoryLockParams{ //nolint:exhaustruct_v5 // The lock columns stay NULL to clear.
		Namespace: key.namespace,
		Tool:      key.tool,
		Name:      key.name,
	}

	if lock != nil {
		params.LockHolder = pgtype.Text{String: lock.Holder, Valid: true}
		params.LockSinceMs = pgtype.Int8{Int64: codec.Millis(lock.Since), Valid: true}
		params.LockOperation = pgtype.Text{String: lock.Operation, Valid: true}
	}

	if err := q.UpdateInventoryLock(ctx, params); err != nil {
		return fmt.Errorf("update inventory lock: %w", err)
	}

	return nil
}

// publishStartLock publishes the lock a started update holds, in the
// caller's transaction. A preview or a dry run publishes none: only a
// mutating update holds the stack.
func publishStartLock(ctx context.Context, q *db.Queries, u stack.Update) error {
	lock := stackLock(u)
	if lock == nil {
		return nil
	}

	return writeInventoryLock(ctx, q, stackInventoryKey(u), lock)
}

// stackLock returns the inventory lock a running mutating update holds: its
// creator with its kind, since its start. Previews and dry runs never lock.
func stackLock(u stack.Update) *inventory.Lock {
	if !u.Mutates() {
		return nil
	}

	return &inventory.Lock{Since: u.StartedAt, Holder: u.CreatedBy, Operation: string(u.Kind)}
}

// publishWorkspaceInventory writes the OpenTofu entry for ref with the given
// unit ID and change time, and appends the change log row of the kind kind.
// Status and resource count stay unknown: OpenTofu reports no status
// vocabulary and the server never reads state. The lock columns stay
// untouched: only writeInventoryLock changes them, in the same transaction as
// the lock, unlock or force-unlock that they describe. The caller runs it as
// the last write of its transaction.
func publishWorkspaceInventory(ctx context.Context, q *db.Queries, ref workspace.Ref, w unitWrite) error {
	if err := upsertWorkspaceInventory(ctx, q, ref, w.unitID, w.atMs); err != nil {
		return err
	}

	return appendWorkspaceChange(ctx, q, ref, w)
}

// appendWorkspaceChange appends the change log row of the workspace ref.
func appendWorkspaceChange(ctx context.Context, q *db.Queries, ref workspace.Ref, w unitWrite) error {
	return appendInventoryChange(ctx, q, unitChange(ref.Namespace, inventoryToolOpenTofu, ref.Name.String(), w))
}

// upsertWorkspaceInventory writes the OpenTofu entry for a workspace and
// appends no change log row. A write that changes only the lock uses it,
// because the lock is not history.
func upsertWorkspaceInventory(
	ctx context.Context, q *db.Queries, ref workspace.Ref, unitID string, lastChangedMs int64,
) error {
	name := ref.Name.String()

	err := q.UpsertInventory(ctx, db.UpsertInventoryParams{
		Namespace:     ref.Namespace.String(),
		Tool:          inventoryToolOpenTofu,
		Name:          name,
		UnitID:        unitID,
		DisplayName:   name,
		LastChangedMs: lastChangedMs,
		Status:        "",
		ResourceCount: pgtype.Int8{Int64: 0, Valid: false},
	})
	if err != nil {
		return fmt.Errorf("upsert inventory entry: %w", err)
	}

	return nil
}

// moveStackInventoryToDeleted moves the inventory entry of the stack ref with
// stackID to the deleted units, in the transaction of the delete (ADR 13, ADR
// 47). The retention period ends at purgeAfter.
func moveStackInventoryToDeleted(
	ctx context.Context, q *db.Queries, ref stack.Ref, stackID string, d inventory.Deletion,
) error {
	err := q.InsertDeletedInventory(ctx, db.InsertDeletedInventoryParams{
		Namespace:    ref.Namespace.String(),
		Tool:         inventoryTool,
		UnitID:       stackID,
		Name:         stackUnitName(ref),
		DeletedAtMs:  d.Entry.At.UnixMilli(),
		PurgeAfterMs: d.PurgeAfter.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("insert deleted inventory entry: %w", err)
	}

	if removeErr := removeStackInventory(ctx, q, ref); removeErr != nil {
		return removeErr
	}

	deleted := unitChange(
		ref.Namespace,
		inventoryTool,
		stackUnitName(ref),
		unitWrite{unitID: stackID, atMs: d.Entry.At.UnixMilli(), kind: inventory.ChangeDeleted},
	)

	return appendInventoryChange(ctx, q, deleted)
}

// QueryDeletedUnits implements inventory.DeletedReader with one count and one
// page query, built from the filters that q sets. The order and the cursor
// compare with the "C" collation, the byte order that every store and
// inventory.PageDeleted share. The page query reads one row more than the
// page size, so it knows whether a next page exists.
func (s *Store) QueryDeletedUnits(ctx context.Context, q inventory.DeletedQuery) (inventory.DeletedPage, error) {
	if q.MatchesNothing() {
		return inventory.TrimDeletedPage([]inventory.DeletedEntry{}, q.PageSize(), 0), nil
	}

	var page inventory.DeletedPage

	// One REPEATABLE READ snapshot for the count and the page, so the total
	// agrees with the rows.
	opts := pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly, DeferrableMode: pgx.NotDeferrable, BeginQuery: "",
		CommitQuery: "",
	}

	err := pgx.BeginTxFunc(ctx, s.pool, opts, func(tx pgx.Tx) error {
		var readErr error

		page, readErr = queryDeletedPage(ctx, tx, q)

		return readErr
	})
	if err != nil {
		return inventory.DeletedPage{}, fmt.Errorf("deleted inventory snapshot: %w", err)
	}

	return page, nil
}

// queryDeletedPage counts the deleted units that match q and reads one page
// of them with tx.
func queryDeletedPage(ctx context.Context, tx pgx.Tx, q inventory.DeletedQuery) (inventory.DeletedPage, error) {
	where, args := deletedFilter(q)

	var total int

	count := tx.QueryRow(ctx, "SELECT COUNT(*) FROM inventory_deleted WHERE "+where, args...)
	if err := count.Scan(&total); err != nil {
		return inventory.DeletedPage{}, fmt.Errorf("count deleted inventory: %w", err)
	}

	if q.After != nil {
		cursor := []any{q.After.Tool, q.After.Name, q.After.UnitID}
		where += ` AND (tool COLLATE "C", name COLLATE "C", unit_id COLLATE "C") > (` +
			placeholders(len(args)+1, len(cursor)) + ")"
		args = append(args, cursor...)
	}

	size := q.PageSize()
	args = append(args, size+1)

	rows, err := tx.Query(ctx,
		"SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms FROM inventory_deleted WHERE "+
			where+fmt.Sprintf(` ORDER BY tool COLLATE "C", name COLLATE "C", unit_id COLLATE "C" LIMIT $%d`, len(args)),
		args...)
	if err != nil {
		return inventory.DeletedPage{}, fmt.Errorf("select deleted inventory: %w", err)
	}

	entries, err := scanDeletedRows(rows)
	if err != nil {
		return inventory.DeletedPage{}, err
	}

	return inventory.TrimDeletedPage(entries, size, total), nil
}

// placeholders returns n numbered placeholders from $first, joined by commas.
func placeholders(first, n int) string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("$%d", first+i))
	}

	return strings.Join(out, ", ")
}

// deletedFilter returns the WHERE clause and its arguments for the filters
// of q, without the cursor. The name prefix compares characters exactly.
func deletedFilter(q inventory.DeletedQuery) (string, []any) {
	where := "namespace = $1"
	args := []any{q.Namespace.String()}

	if q.Tool != "" {
		args = append(args, q.Tool)
		where += fmt.Sprintf(" AND tool = $%d", len(args))
	}

	if q.NamePrefix != "" {
		args = append(args, utf8.RuneCountInString(q.NamePrefix), q.NamePrefix)
		where += fmt.Sprintf(" AND left(name, $%d) = $%d", len(args)-1, len(args))
	}

	if !q.PurgeBefore.IsZero() {
		args = append(args, q.PurgeBefore.UnixMilli())
		where += fmt.Sprintf(" AND purge_after_ms <= $%d", len(args))
	}

	if q.Restricted {
		args = append(args, q.UnitIDs)
		where += fmt.Sprintf(" AND unit_id = ANY($%d::text[])", len(args))
	}

	return where, args
}

// scanDeletedRows reads every row of a deleted inventory query and closes
// rows.
func scanDeletedRows(rows pgx.Rows) ([]inventory.DeletedEntry, error) {
	defer rows.Close()

	out := []inventory.DeletedEntry{}

	for rows.Next() {
		var (
			ns, tool, unitID, name string
			deletedMs, purgeMs     int64
		)

		if err := rows.Scan(&ns, &tool, &unitID, &name, &deletedMs, &purgeMs); err != nil {
			return nil, fmt.Errorf("scan deleted inventory: %w", err)
		}

		out = append(out, inventory.DeletedEntry{
			DeletedAt:  time.UnixMilli(deletedMs).UTC(),
			PurgeAfter: time.UnixMilli(purgeMs).UTC(),
			Namespace:  namespace.Name(ns),
			Tool:       tool,
			UnitID:     unitID,
			Name:       name,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read deleted inventory: %w", err)
	}

	return out, nil
}

// GetDeletedUnit implements inventory.DeletedReader.
func (s *Store) GetDeletedUnit(
	ctx context.Context, ns namespace.Name, tool, unitID string,
) (inventory.DeletedEntry, error) {
	row, err := s.queries.GetDeletedInventory(ctx, db.GetDeletedInventoryParams{
		Namespace: ns.String(), Tool: tool, UnitID: unitID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return inventory.DeletedEntry{}, inventory.ErrNotFound
	}

	if err != nil {
		return inventory.DeletedEntry{}, fmt.Errorf("select deleted inventory entry: %w", err)
	}

	return inventory.DeletedEntry{
		DeletedAt:  time.UnixMilli(row.DeletedAtMs).UTC(),
		PurgeAfter: time.UnixMilli(row.PurgeAfterMs).UTC(),
		Namespace:  namespace.Name(row.Namespace),
		Tool:       row.Tool,
		UnitID:     row.UnitID,
		Name:       row.Name,
	}, nil
}

// ListDeletedUnits implements inventory.DeletedReader: the deleted units of
// one namespace, ordered by tool, then name, then unit ID.
func (s *Store) ListDeletedUnits(ctx context.Context, ns namespace.Name) ([]inventory.DeletedEntry, error) {
	rows, err := s.queries.ListDeletedInventory(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select deleted inventory: %w", err)
	}

	out := make([]inventory.DeletedEntry, 0, len(rows))
	for i := range rows {
		out = append(out, inventory.DeletedEntry{
			DeletedAt:  time.UnixMilli(rows[i].DeletedAtMs).UTC(),
			PurgeAfter: time.UnixMilli(rows[i].PurgeAfterMs).UTC(),
			Namespace:  namespace.Name(rows[i].Namespace),
			Tool:       rows[i].Tool,
			UnitID:     rows[i].UnitID,
			Name:       rows[i].Name,
		})
	}

	return out, nil
}

// ListUnits implements inventory.Reader: entries of one namespace, ordered by
// tool, then name. A namespace with no units reads as an empty list.
func (s *Store) ListUnits(ctx context.Context, ns namespace.Name) ([]inventory.Entry, error) {
	rows, err := s.queries.ListInventory(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select inventory: %w", err)
	}

	out := make([]inventory.Entry, 0, len(rows))
	for i := range rows {
		out = append(out, inventoryEntry(rows[i]))
	}

	return out, nil
}

// inventoryEntry converts an inventory row into a domain entry. A null
// resource count reads as nil, meaning unknown. A lock reads as set only
// when a holder is stored: an empty holder means unlocked, and the entry
// holds no lock. A lock stays stored while its update runs, even after its
// lease expires: an expired lease still holds the stack lock until someone
// cancels the update, so only the terminal transition clears it. Since
// carries the take time, so the console can show the age of a lock held too
// long.
func inventoryEntry(row db.ListInventoryRow) inventory.Entry {
	var count *int64

	if row.ResourceCount.Valid {
		v := row.ResourceCount.Int64
		count = &v
	}

	return inventory.Entry{
		LastChanged:   codec.FromMillis(row.LastChangedMs),
		Namespace:     namespace.Name(row.Namespace),
		Tool:          row.Tool,
		Name:          row.Name,
		UnitID:        row.UnitID,
		DisplayName:   row.DisplayName,
		Status:        row.Status,
		ResourceCount: count,
		Lock:          inventoryLock(row),
		Collection:    row.Collection,
	}
}

// inventoryLock converts the lock columns of an inventory row into a domain
// lock, or nil when the unit is not locked. The writer stores all three
// columns together or clears all three, so a row with any of them set holds
// a lock. The holder may be empty: an update created without a subject still
// locks its stack, and the entry reports the lock with no holder rather than
// hiding it.
func inventoryLock(row db.ListInventoryRow) *inventory.Lock {
	holder := ""
	if row.LockHolder.Valid {
		holder = row.LockHolder.String
	}

	operation := ""
	if row.LockOperation.Valid {
		operation = row.LockOperation.String
	}

	if holder == "" && !row.LockSinceMs.Valid && operation == "" {
		return nil
	}

	lock := &inventory.Lock{Holder: holder, Operation: operation, Since: time.Time{}}

	if row.LockSinceMs.Valid {
		lock.Since = codec.FromMillis(row.LockSinceMs.Int64)
	}

	return lock
}
