package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/placementstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ collection.PlacementStore = (*Store)(nil)

// CreatePlacementRule implements collection.PlacementStore. The fence is
// checked and the audit entry is appended in the same transaction as the insert
// and the renumber of the list.
func (s *Store) CreatePlacementRule(
	ctx context.Context, r collection.Rule, fence namespace.Fence, e audit.Entry,
) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("placement rule: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

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

		if _, err := collectionID(ctx, q, collection.Ref{Namespace: r.Namespace, Name: r.Collection}); err != nil {
			return err
		}

		err = q.CreatePlacementRule(ctx, db.CreatePlacementRuleParams{
			ID:            r.ID,
			Namespace:     r.Namespace.String(),
			Position:      int64(len(ids) + 1),
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

		return renumberPlacementRules(ctx, q, r.Namespace, ordered)
	})
}

// UpdatePlacementRule implements collection.PlacementStore.
func (s *Store) UpdatePlacementRule(
	ctx context.Context, r collection.Rule, fence namespace.Fence, e audit.Entry,
) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("placement rule: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

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

		if _, err := collectionID(ctx, q, collection.Ref{Namespace: r.Namespace, Name: r.Collection}); err != nil {
			return err
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

		return renumberPlacementRules(ctx, q, r.Namespace, ordered)
	})
}

// DeletePlacementRule implements collection.PlacementStore.
func (s *Store) DeletePlacementRule(
	ctx context.Context, ns namespace.Name, id string, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetPlacementRule(ctx, db.GetPlacementRuleParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Rule{}, collection.ErrPlacementRuleNotFound
	}

	if err != nil {
		return collection.Rule{}, fmt.Errorf("select placement rule: %w", err)
	}

	return placementRuleFromRow(row.ID, row.Namespace, row.Position, row.Tool, row.Project, row.Stack,
		row.CollectionName, row.CreatedBy, row.CreatedAtMs), nil
}

// ListPlacementRules implements collection.PlacementStore.
func (s *Store) ListPlacementRules(ctx context.Context, ns namespace.Name) ([]collection.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListPlacementRules(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select placement rules: %w", err)
	}

	out := make([]collection.Rule, 0, len(rows))
	for _, row := range rows {
		out = append(out, placementRuleFromRow(row.ID, row.Namespace, row.Position, row.Tool, row.Project,
			row.Stack, row.CollectionName, row.CreatedBy, row.CreatedAtMs))
	}

	return out, nil
}

// placementRuleFromRow converts a placement_rules row into a domain rule.
func placementRuleFromRow(
	id, ns string, position int64, tool, project, stack, coll, createdBy string, createdAtMs int64,
) collection.Rule {
	return collection.Rule{
		CreatedAt:  codec.FromMillis(createdAtMs),
		ID:         id,
		Namespace:  namespace.Name(ns),
		Tool:       tool,
		Project:    collection.Pattern(project),
		Stack:      collection.Pattern(stack),
		Collection: collection.Name(coll),
		CreatedBy:  createdBy,
		Position:   int(position),
	}
}

// renumberPlacementRules gives the rules of ns the positions 1 to n in the
// order of ids, in the caller's transaction.
func renumberPlacementRules(ctx context.Context, q *db.Queries, ns namespace.Name, ids []string) error {
	for i, id := range ids {
		err := q.SetPlacementRulePosition(ctx, db.SetPlacementRulePositionParams{
			Position: int64(i + 1), Namespace: ns.String(), ID: id,
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
	for i, row := range rows {
		removed[i] = placementstore.Removed{ID: row.ID, Tool: row.Tool, Match: row.Project + "/" + row.Stack}
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
