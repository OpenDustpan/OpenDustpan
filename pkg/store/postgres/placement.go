package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/placementstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ collection.PlacementStore = (*Store)(nil)

// placementCollectionError maps the missing collection of a placement rule to
// collection.ErrPlacementCollectionMissing, with the name of the collection in
// the text. It returns err unchanged for any other error.
func placementCollectionError(name collection.Name, err error) error {
	if errors.Is(err, collection.ErrNotFound) {
		return fmt.Errorf("placement rule names collection %q: %w", name, collection.ErrPlacementCollectionMissing)
	}

	return err
}

// CreatePlacementRule implements collection.PlacementStore. The fence is
// checked and the audit entry is appended in the same transaction as the insert
// and the renumber of the list.
func (s *Store) CreatePlacementRule(
	ctx context.Context, r collection.Rule, fence namespace.Fence, e audit.Entry,
) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("placement rule: %w", err)
	}

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		if r.Namespace != fence.Namespace {
			return collection.ErrNotFound
		}

		ids, err := q.ListPlacementRuleIDs(ctx, r.Namespace.String())
		if err != nil {
			return fmt.Errorf("select placement rule IDs: %w", err)
		}

		if len(ids) >= collection.MaxPlacementRules {
			return collection.ErrTooManyPlacementRules
		}

		ordered, err := placementstore.Insert(ids, r.ID, r.Position)
		if err != nil {
			return fmt.Errorf("place rule: %w", err)
		}

		if _, idErr := collectionID(ctx, q, collection.Ref{Namespace: r.Namespace, Name: r.Collection}); idErr != nil {
			return placementCollectionError(r.Collection, idErr)
		}

		// The row gets a position behind the others. The renumber below then
		// puts it at its place. MaxPlacementRules keeps the value far below
		// the int32 limit.
		last := min(len(ids)+1, math.MaxInt32)

		err = q.CreatePlacementRule(ctx, db.CreatePlacementRuleParams{
			ID:            r.ID,
			Namespace:     r.Namespace.String(),
			Position:      int32(last),
			Tool:          r.Tool,
			Project:       r.Project.String(),
			Stack:         r.Stack.String(),
			CollectionKey: r.Collection.Key(),
			CreatedBy:     r.CreatedBy,
			CreatedAtMs:   codec.Millis(r.CreatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert placement rule: %w", err)
		}

		if renumErr := renumberPlacementRules(ctx, q, r.Namespace, ordered); renumErr != nil {
			return renumErr
		}

		return checkPlacementOrder(ctx, q, r.Namespace, r.ID)
	})
}

// UpdatePlacementRule implements collection.PlacementStore.
func (s *Store) UpdatePlacementRule(
	ctx context.Context, r collection.Rule, fence namespace.Fence, e audit.Entry,
) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("placement rule: %w", err)
	}

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		if r.Namespace != fence.Namespace {
			return collection.ErrPlacementRuleNotFound
		}

		ids, err := q.ListPlacementRuleIDs(ctx, r.Namespace.String())
		if err != nil {
			return fmt.Errorf("select placement rule IDs: %w", err)
		}

		ordered, err := placementstore.Move(ids, r.ID, r.Position)
		if err != nil {
			return fmt.Errorf("place rule: %w", err)
		}

		if _, idErr := collectionID(ctx, q, collection.Ref{Namespace: r.Namespace, Name: r.Collection}); idErr != nil {
			return placementCollectionError(r.Collection, idErr)
		}

		n, err := q.UpdatePlacementRuleContent(ctx, db.UpdatePlacementRuleContentParams{
			Tool:          r.Tool,
			Project:       r.Project.String(),
			Stack:         r.Stack.String(),
			CollectionKey: r.Collection.Key(),
			Namespace:     r.Namespace.String(),
			ID:            r.ID,
		})
		if err != nil {
			return fmt.Errorf("update placement rule: %w", err)
		}

		if n == 0 {
			return collection.ErrPlacementRuleNotFound
		}

		if renumErr := renumberPlacementRules(ctx, q, r.Namespace, ordered); renumErr != nil {
			return renumErr
		}

		return checkPlacementOrder(ctx, q, r.Namespace, r.ID)
	})
}

// DeletePlacementRule implements collection.PlacementStore.
func (s *Store) DeletePlacementRule(
	ctx context.Context, ns namespace.Name, id string, fence namespace.Fence, e audit.Entry,
) error {
	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		if ns != fence.Namespace {
			return collection.ErrPlacementRuleNotFound
		}

		ids, err := q.ListPlacementRuleIDs(ctx, ns.String())
		if err != nil {
			return fmt.Errorf("select placement rule IDs: %w", err)
		}

		ordered, err := placementstore.Remove(ids, id)
		if err != nil {
			return err
		}

		n, err := q.DeletePlacementRule(ctx, db.DeletePlacementRuleParams{Namespace: ns.String(), ID: id})
		if err != nil {
			return fmt.Errorf("delete placement rule: %w", err)
		}

		if n == 0 {
			return collection.ErrPlacementRuleNotFound
		}

		return renumberPlacementRules(ctx, q, ns, ordered)
	})
}

// GetPlacementRule implements collection.PlacementStore.
func (s *Store) GetPlacementRule(ctx context.Context, ns namespace.Name, id string) (collection.Rule, error) {
	row, err := s.queries.GetPlacementRule(ctx, db.GetPlacementRuleParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return collection.Rule{}, collection.ErrPlacementRuleNotFound
	}

	if err != nil {
		return collection.Rule{}, fmt.Errorf("select placement rule: %w", err)
	}

	return placementRuleFromRow(&row), nil
}

// ListPlacementRules implements collection.PlacementStore.
func (s *Store) ListPlacementRules(ctx context.Context, ns namespace.Name) ([]collection.Rule, error) {
	rows, err := s.queries.ListPlacementRules(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select placement rules: %w", err)
	}

	out := make([]collection.Rule, 0, len(rows))
	for i := range rows {
		row := db.GetPlacementRuleRow(rows[i])
		out = append(out, placementRuleFromRow(&row))
	}

	return out, nil
}

// placementRuleFromRow converts a placement_rules row into a domain rule.
func placementRuleFromRow(row *db.GetPlacementRuleRow) collection.Rule {
	return collection.Rule{
		CreatedAt:  codec.FromMillis(row.CreatedAtMs),
		ID:         row.ID,
		Namespace:  namespace.Name(row.Namespace),
		Tool:       row.Tool,
		Project:    collection.Pattern(row.Project),
		Stack:      collection.Pattern(row.Stack),
		Collection: collection.Name(row.CollectionName),
		CreatedBy:  row.CreatedBy,
		Position:   int(row.Position),
	}
}

// checkPlacementOrder reads the rules of ns in the caller's transaction and
// returns collection.ErrPlacementConflict when a rule hides the rule changed, or
// the rule changed hides another. The caller holds the write lock of the
// namespace, so no other writer changes the list between the read and the
// commit. A conflict rolls back the transaction of the write.
func checkPlacementOrder(ctx context.Context, q *db.Queries, ns namespace.Name, changed string) error {
	rows, err := q.ListPlacementRules(ctx, ns.String())
	if err != nil {
		return fmt.Errorf("select placement rules: %w", err)
	}

	rules := make([]collection.Rule, 0, len(rows))

	for i := range rows {
		row := db.GetPlacementRuleRow(rows[i])
		rules = append(rules, placementRuleFromRow(&row))
	}

	return collection.CheckPlacementOrder(rules, changed)
}

// renumberPlacementRules gives the rules of ns the positions 1 to n in the
// order of ids, in the caller's transaction.
func renumberPlacementRules(ctx context.Context, q *db.Queries, ns namespace.Name, ids []string) error {
	for i, id := range ids {
		err := q.SetPlacementRulePosition(ctx, db.SetPlacementRulePositionParams{
			Position: int32(i + 1), Namespace: ns.String(), ID: id,
		})
		if err != nil {
			return fmt.Errorf("set placement rule position: %w", err)
		}
	}

	return nil
}

// purgePlacementRules deletes the rules that name the collection ref, closes
// the gaps in the list, and returns the audit changes that name the rules. The
// room is the number of changes that the entry still accepts. It runs in the
// transaction of the delete of the collection.
func purgePlacementRules(
	ctx context.Context, q *db.Queries, ref collection.Ref, room int,
) ([]audit.Change, error) {
	rows, err := q.ListPlacementRulesOfCollection(ctx, db.ListPlacementRulesOfCollectionParams{
		Namespace: ref.Namespace.String(), CollectionKey: ref.Name.Key(),
	})
	if err != nil {
		return nil, fmt.Errorf("select placement rules of collection: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	removed := make([]placementstore.Removed, len(rows))
	for i := range rows {
		removed[i] = placementstore.Removed{
			ID: rows[i].ID, Tool: rows[i].Tool, Match: rows[i].Project + "/" + rows[i].Stack,
		}
	}

	err = q.DeletePlacementRulesOfCollection(ctx, db.DeletePlacementRulesOfCollectionParams{
		Namespace: ref.Namespace.String(), CollectionKey: ref.Name.Key(),
	})
	if err != nil {
		return nil, fmt.Errorf("delete placement rules of collection: %w", err)
	}

	ids, err := q.ListPlacementRuleIDs(ctx, ref.Namespace.String())
	if err != nil {
		return nil, fmt.Errorf("select placement rule IDs: %w", err)
	}

	if err := renumberPlacementRules(ctx, q, ref.Namespace, ids); err != nil {
		return nil, err
	}

	return placementstore.RemovedChanges(removed, room), nil
}
