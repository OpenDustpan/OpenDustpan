package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// placementCollection is the collection that PlaceStack puts a stack in.
const placementCollection = "main"

// collectionHolder is what PlaceStack needs from a store: the namespace, the
// collections and the unit membership. Every adapter that runs the update
// suites holds all three, because its writes of a stack state refuse a stack in
// no collection.
type collectionHolder interface {
	collection.Store
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// PlaceStack puts the existing stack ref in a collection, and does nothing for
// a stack that one holds already. The state writes of a stack in no collection
// fail, so a suite places a stack before it writes an update to it.
func PlaceStack(t *testing.T, s any, ref stack.Ref) {
	t.Helper()

	if err := PlaceStackCtx(t.Context(), s, ref); err != nil {
		t.Fatalf("place %s: %v", ref, err)
	}
}

// PlaceStackCtx is PlaceStack for code that holds no *testing.T.
func PlaceStackCtx(ctx context.Context, s any, ref stack.Ref) error {
	// Find the store that holds collections in s or in the stores that s
	// wraps.
	var h collectionHolder

	for range 8 {
		if got, ok := s.(collectionHolder); ok {
			h = got

			break
		}

		w, ok := s.(Unwrapper)
		if !ok {
			break
		}

		s = w.Unwrap()
	}

	if h == nil {
		// A store of a test double keeps no collections and refuses nothing.
		return nil
	}

	unit := collection.UnitRef{
		Namespace: ref.Namespace, Tool: stack.InventoryTool, Name: ref.Project.String() + "/" + ref.Stack.String(),
	}

	if _, err := h.UnitCollection(ctx, unit); err == nil {
		return nil
	} else if !errors.Is(err, collection.ErrNoCollection) {
		return fmt.Errorf("collection of %s: %w", ref, err)
	}

	ns, err := h.GetNamespace(ctx, ref.Namespace)
	if err != nil {
		return fmt.Errorf("namespace %s: %w", ref.Namespace, err)
	}

	target, err := placementTarget(ctx, h, ns)
	if err != nil {
		return err
	}

	move := collection.Move{Unit: unit, From: "", To: target.Ref, ToID: target.ID, EndsLock: false}
	entry := placementEntry(ref.Namespace, "unit:set-collection")

	if err = h.SetUnitCollection(ctx, move, ns.Fence(), entry); err != nil {
		return fmt.Errorf("place %s: %w", ref, err)
	}

	return nil
}

// placementTarget returns the collection of placementCollection in ns, and
// creates it when it does not exist.
func placementTarget(ctx context.Context, h collectionHolder, ns namespace.Namespace) (collection.Collection, error) {
	ref := collection.Ref{Namespace: ns.Name, Name: placementCollection}

	target, err := h.GetCollection(ctx, ref)
	if err == nil {
		return target, nil
	}

	if !errors.Is(err, collection.ErrNotFound) {
		return collection.Collection{}, fmt.Errorf("get collection: %w", err)
	}

	created := collection.Collection{CreatedAt: gateTime(), ID: ident.NewID("col-"), Ref: ref}

	err = h.CreateCollection(ctx, created, ns.Fence(), placementEntry(ns.Name, "collection:create"))
	if err != nil && !errors.Is(err, collection.ErrAlreadyExists) {
		return collection.Collection{}, fmt.Errorf("create collection: %w", err)
	}

	target, err = h.GetCollection(ctx, ref)
	if err != nil {
		return collection.Collection{}, fmt.Errorf("get collection: %w", err)
	}

	return target, nil
}

func placementEntry(ns namespace.Name, action string) audit.Entry {
	return gateEntry(ns, action)
}

// Unwrapper is the optional capability of a store wrapper that hides the
// methods of the store that it wraps: it returns that store.
type Unwrapper interface {
	Unwrap() any
}
