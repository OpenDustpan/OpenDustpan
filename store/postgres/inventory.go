package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/inventory"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/internal/codec"
	"opendustpan.org/opendustpan/store/postgres/internal/db"
	"opendustpan.org/opendustpan/workspace"
)

var _ inventory.Reader = (*Store)(nil)

// inventoryTool names the Pulumi CLI in inventory entries.
const inventoryTool = "pulumi"

// inventoryToolOpenTofu names OpenTofu in inventory entries.
const inventoryToolOpenTofu = "opentofu"

// stackUnitName returns the tool's own name for a stack: project/stack.
func stackUnitName(ref stack.Ref) string {
	return ref.Project.String() + "/" + ref.Stack.String()
}

// publishStackInventory writes the Pulumi entry for ref with the given unit
// ID and change time. Status and resource count stay unknown: the Pulumi CLI
// reports no status vocabulary and the server never reads deployments.
func publishStackInventory(
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

// publishWorkspaceInventory writes the OpenTofu entry for ref with the given
// unit ID and change time. Status and resource count stay unknown: OpenTofu
// reports no status vocabulary and the server never reads state.
func publishWorkspaceInventory(
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

// ListUnits implements inventory.Reader: entries of one namespace, ordered by
// tool, then name. A namespace with no units reads as an empty list.
func (s *Store) ListUnits(ctx context.Context, ns namespace.Name) ([]inventory.Entry, error) {
	rows, err := s.queries.ListInventory(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select inventory: %w", err)
	}

	out := make([]inventory.Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, inventoryEntry(row))
	}

	return out, nil
}

// inventoryEntry converts an inventory row into a domain entry. A null
// resource count reads as nil, meaning unknown.
func inventoryEntry(row db.Inventory) inventory.Entry {
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
	}
}
