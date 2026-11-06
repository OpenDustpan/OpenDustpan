package pulumi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// PlacementRules finds the placement rule of a new stack.
type PlacementRules interface {
	// Place returns the first placement rule of ns that matches the new stack.
	// It returns an error that matches collection.ErrNoPlacement when no rule
	// matches. It writes nothing.
	Place(ctx context.Context, ns namespace.Name, tool, project, stackName string) (collection.Rule, error)
}

// PlacementCollections reads a collection and puts a unit in it.
type PlacementCollections interface {
	// CollectionID returns the stable ID of the collection obj, and false when
	// the collection does not exist.
	CollectionID(ctx context.Context, obj identity.Object) (string, bool, error)
	// SetUnit puts a unit that has no collection in the collection m.To and
	// appends by in the same transaction.
	SetUnit(ctx context.Context, m collection.Move, by audit.Entry) error
}

// Placer chooses the collection of a new stack and puts the stack in it. The
// server gives the Placer the placement rules and the collections of the
// installation.
type Placer struct {
	Rules       PlacementRules
	Collections PlacementCollections
}

// placement is the collection that a new stack goes into: the rule that chose
// it and the stable ID of the collection that the caller was authorized on.
type placement struct {
	rule collection.Rule
	id   string
}

// choosePlacement finds the collection of the new stack ref and authorizes the
// caller on it. It writes the answer and returns false when the stack must not
// be made: no rule matches, the caller lacks collection:create-unit on the
// collection of the rule, or the collection does not exist. Without a Placer
// the stack gets no collection and the answer is true.
//
// Defense in depth: the route already authorizes unit:manage on the new unit.
// The check of collection:create-unit stays here, in front of the write, so
// that no other check can stand in for it.
func (h *Handler) choosePlacement(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ref stack.Ref,
) (placement, bool) {
	ctx := r.Context()

	var none placement

	if h.placer == nil {
		return none, true
	}

	rule, err := h.placer.Rules.Place(ctx, ref.Namespace, unitTool, ref.Project.String(), ref.Stack.String())
	if errors.Is(err, collection.ErrNoPlacement) {
		// A namespace that does not exist has no rules. Its error comes first,
		// so that the CLI gets the hint to set the default organization and
		// not the error of a missing rule.
		if _, nsErr := h.namespaces.WritableHere(ctx, ref.Namespace); nsErr != nil {
			h.writeNamespaceNotFound(ctx, w, p, ref.Namespace, fmt.Errorf("create stack %s: %w", ref, nsErr))

			return none, false
		}
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return none, false
	}

	target := identity.CollectionObject(ref.Namespace, rule.Collection.String())

	err = h.guard.Authorize(ctx, r, p, identity.ActionCollectionCreateUnit, target)
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", identity.ActionCollectionCreateUnit, err))

		return none, false
	}

	id, found, err := h.placer.Collections.CollectionID(ctx, target)

	switch {
	case err != nil:
		h.writeError(ctx, w, err)

		return none, false
	case !found:
		h.writeError(ctx, w, fmt.Errorf("placement rule %s names collection %q: %w",
			rule.ID, rule.Collection, collection.ErrNotFound))

		return none, false
	default:
	}

	return placement{rule: rule, id: id}, true
}

// putInCollection puts the new stack ref in the collection of at and records
// the rule that chose it. When the move fails, it deletes the new stack, so
// that no stack stays without the collection that the rule gave. It returns
// the error of the move.
func (h *Handler) putInCollection(
	r *http.Request, p identity.Principal, ref stack.Ref, at placement,
) error {
	ctx := r.Context()

	obj, err := unitObject(ref)
	if err != nil {
		return err
	}

	entry := changeEntry(ctx, changeAudit{
		request: r, principal: p, object: obj, action: identity.ActionCollectionCreateUnit,
	})
	entry.Version = "from=none;to=" + at.rule.Collection.String() + ";rule=" + at.rule.ID

	unit := collection.UnitRef{
		Namespace: ref.Namespace, Tool: unitTool, Name: ref.Project.String() + "/" + ref.Stack.String(),
	}
	move := collection.Move{
		Unit: unit, From: "", To: collection.Ref{Namespace: ref.Namespace, Name: at.rule.Collection},
		ToID: at.id, EndsLock: false,
	}

	moveErr := h.placer.Collections.SetUnit(ctx, move, entry)
	if moveErr == nil {
		return nil
	}

	undo := changeEntry(ctx, changeAudit{
		request: r, principal: p, object: obj, action: identity.ActionUnitManage,
	})
	undo.Version = "undo create: the unit could not be put in its collection"

	if delErr := h.stacks.Delete(ctx, ref, undo); delErr != nil {
		return errors.Join(moveErr, fmt.Errorf("delete stack %s after a failed placement: %w", ref, delErr))
	}

	return moveErr
}
