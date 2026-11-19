package stack

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/collection"
)

// Membership answers which collection holds a stack. Updates asks it before it
// creates or starts an update, so that no update of a stack in no collection
// begins. It is a required port: an Updates without one fails closed.
type Membership interface {
	// CollectionOf returns the stable ID of the collection that holds ref, with
	// true. It returns "", false and no error when the stack is in no
	// collection or does not exist. It returns an error only for a store
	// failure.
	CollectionOf(ctx context.Context, ref Ref) (string, bool, error)
}

// UnitCollections is the part of collection.Store that CollectionMembership
// reads.
type UnitCollections interface {
	// UnitCollection returns the collection of the unit, collection.ErrNoCollection
	// for a unit in no collection, or collection.ErrUnitNotFound for an unknown unit.
	UnitCollection(ctx context.Context, unit collection.UnitRef) (collection.Ref, error)
}

type collectionMembership struct {
	units UnitCollections
}

// CollectionMembership returns the Membership that reads the collection of a
// stack from units. It returns nil for a nil units, which Updates refuses.
//
//nolint:ireturn // The port is the return type.
func CollectionMembership(units UnitCollections) Membership {
	if units == nil {
		return nil
	}

	return collectionMembership{units: units}
}

// CollectionOf implements Membership.
func (m collectionMembership) CollectionOf(ctx context.Context, ref Ref) (string, bool, error) {
	got, err := m.units.UnitCollection(ctx, collection.UnitRef{
		Namespace: ref.Namespace,
		Tool:      InventoryTool,
		Name:      ref.Project.String() + "/" + ref.Stack.String(),
	})
	if errors.Is(err, collection.ErrNoCollection) || errors.Is(err, collection.ErrUnitNotFound) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("collection of %s: %w", ref, err)
	}

	return got.Name.String(), true, nil
}

// requirePlaced returns an error that matches collection.ErrNoCollection when
// no collection holds ref. It fails closed: a missing port, a failing store and
// an unknown answer all refuse. It only reads.
//
// Defense in depth: the stores refuse the same writes in their own
// transaction, and the HTTP adapter maps the refusal. This check stays so that
// a store without that check cannot start an update of a stack in no
// collection. It does not rely on the stores or on the adapter.
func (s *Updates) requirePlaced(ctx context.Context, ref Ref) error {
	if s.membership == nil {
		return fmt.Errorf("write to %s: %w: collection membership", ref, ErrMissingDependency)
	}

	_, found, err := s.membership.CollectionOf(ctx, ref)
	if err != nil {
		return fmt.Errorf("write to %s: %w", ref, err)
	}

	if !found {
		return fmt.Errorf("write to %s: %w", ref, collection.ErrNoCollection)
	}

	return nil
}
