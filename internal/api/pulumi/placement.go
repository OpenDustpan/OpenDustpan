package pulumi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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

// PlacementCollections reads a collection of a placement rule.
type PlacementCollections interface {
	// CollectionID returns the stable ID of the collection obj, and false when
	// the collection does not exist. A missing collection is not an error: the
	// caller answers with collection.ErrPlacementCollectionMissing.
	CollectionID(ctx context.Context, obj identity.Object) (string, bool, error)
}

// Placer chooses the collection of a new stack. The stack store then makes the
// stack and its membership in one transaction. The server gives the Placer the
// placement rules and the collections of the installation. Without a Placer
// the server makes no stack.
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
// collection of the rule, or the collection does not exist. A missing
// collection answers with an error that matches
// collection.ErrPlacementCollectionMissing. Without a Placer the stack gets no
// collection and the answer is true.
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
		// A namespace that does not exist gets its own error first, as it does
		// when a rule is missing.
		if _, nsErr := h.namespaces.WritableHere(ctx, ref.Namespace); nsErr != nil {
			h.writeNamespaceNotFound(ctx, w, p, ref.Namespace, fmt.Errorf("create stack %s: %w", ref, nsErr))

			return none, false
		}

		h.writeError(ctx, w, errPlacementOff)

		return none, false
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

		// A stack that exists is a conflict, whatever the rules say.
		if _, getErr := h.stacks.Get(ctx, ref); getErr == nil {
			h.writeError(ctx, w, fmt.Errorf("create stack %s: %w", ref, stack.ErrAlreadyExists))

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
			rule.ID, rule.Collection, collection.ErrPlacementCollectionMissing))

		return none, false
	default:
	}

	return placement{rule: rule, id: id}, true
}

// errPlacementOff reports a server with no Placer: it keeps no placement
// rules, so it cannot put a new stack in a collection and makes no stack.
var errPlacementOff = errors.New(
	"this server keeps no placement rules, so it cannot place a new stack: use a store with collections and placement rules",
)

// stackPlacement returns the placement that puts the new stack ref in the
// collection of at, with the audit entry that names the rule and the caller.
func stackPlacement(r *http.Request, p identity.Principal, ref stack.Ref, at placement) (stack.Placement, error) {
	obj, err := unitObject(ref)
	if err != nil {
		return stack.Placement{}, err
	}

	entry := changeEntry(r.Context(), changeAudit{
		request: r, principal: p, object: obj, action: identity.ActionCollectionCreateUnit,
	})
	entry.Version = "from=none;to=" + at.rule.Collection.String() + ";rule=" + at.rule.ID

	return stack.Placement{
		Collection:   collection.Ref{Namespace: ref.Namespace, Name: at.rule.Collection},
		CollectionID: at.id,
		Entry:        entry,
	}, nil
}
