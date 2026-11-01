package collection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces namespace.Store
	Now        func() time.Time
}

// Service implements the collection use cases. It reads the fence of the
// namespace before each write, so a write carries the failover version that
// the namespace has now (ADR 4).
type Service struct {
	store      Store
	namespaces namespace.Store
	now        func() time.Time
}

// NewService returns a Service. It returns ErrMissingDependency when a port
// is missing. A nil Now uses time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: collection store and namespace store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{store: cfg.Store, namespaces: cfg.Namespaces, now: now}, nil
}

// Create makes the collection ref with a new stable ID and appends by. The
// audit time is the injected clock. The name must be valid (ParseName).
func (s *Service) Create(ctx context.Context, ref Ref, by audit.Entry) (Collection, error) {
	fence, err := s.fence(ctx, ref.Namespace)
	if err != nil {
		return Collection{}, err
	}

	c := Collection{CreatedAt: s.now().UTC(), ID: ident.NewID("col-"), Ref: ref}

	by.At = c.CreatedAt
	if err := s.store.CreateCollection(ctx, c, fence, by); err != nil {
		return Collection{}, fmt.Errorf("create collection %s: %w", ref, err)
	}

	return c, nil
}

// Get returns the collection ref, or ErrNotFound.
func (s *Service) Get(ctx context.Context, ref Ref) (Collection, error) {
	return s.store.GetCollection(ctx, ref)
}

// List returns the collections of ns, ordered by name.
func (s *Service) List(ctx context.Context, ns namespace.Name) ([]Collection, error) {
	return s.store.ListCollections(ctx, ns)
}

// Delete removes the collection ref when no live unit is in it (ErrNotEmpty),
// and appends by.
func (s *Service) Delete(ctx context.Context, ref Ref, by audit.Entry) error {
	fence, err := s.fence(ctx, ref.Namespace)
	if err != nil {
		return err
	}

	by.At = s.now().UTC()
	if err := s.store.DeleteCollection(ctx, ref, fence, by); err != nil {
		return fmt.Errorf("delete collection %s: %w", ref, err)
	}

	return nil
}

// SetUnit applies m and appends by, which names the unit and the two
// collections. The store refuses the move with ErrCollectionChanged when the
// collection of the unit is no longer m.From, or when m.To has another stable
// ID than m.ToID. A move with no ToID is refused with ErrMoveUnchecked.
func (s *Service) SetUnit(ctx context.Context, m Move, by audit.Entry) error {
	if m.Unit.Namespace != m.To.Namespace {
		return fmt.Errorf("collection %s: %w", m.To, ErrNotFound)
	}

	if m.ToID == "" {
		return fmt.Errorf("move %s to %s: %w", m.Unit, m.To, ErrMoveUnchecked)
	}

	fence, err := s.fence(ctx, m.Unit.Namespace)
	if err != nil {
		return err
	}

	by.At = s.now().UTC()
	if err := s.store.SetUnitCollection(ctx, m, fence, by); err != nil {
		return fmt.Errorf("set collection of %s: %w", m.Unit, err)
	}

	return nil
}

// UnitCollection returns the collection of the unit. It returns
// ErrNoCollection when the unit has none.
func (s *Service) UnitCollection(ctx context.Context, unit UnitRef) (Ref, error) {
	return s.store.UnitCollection(ctx, unit)
}

// CollectionOf returns the name of the collection that holds unit, with true.
// It returns false and no error when the unit is in no collection or does not
// exist, so an authorizer can deny the unit without a failure. It returns an
// error only for a real store failure, or for an object that is not a unit.
// It runs once per unit decision, so it costs one store lookup of the unit.
func (s *Service) CollectionOf(ctx context.Context, unit identity.Object) (string, bool, error) {
	if !unit.IsUnit() {
		return "", false, fmt.Errorf("collection of %s: %w", unit, identity.ErrInvalidObject)
	}

	ref, err := s.store.UnitCollection(ctx, UnitRef{
		Namespace: unit.Namespace(),
		Tool:      unit.Tool(),
		Name:      unit.UnitName(),
	})
	if errors.Is(err, ErrNoCollection) || errors.Is(err, ErrUnitNotFound) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("collection of %s: %w", unit, err)
	}

	return ref.Name.String(), true, nil
}

// CollectionUnitIDs returns the stable IDs of the units in the collection obj,
// deleted units that are not purged included. The object must carry the stable
// ID of the collection: it returns ErrNotFound when the collection does not
// exist in the namespace of obj, when it has another ID, or when obj has none.
// It returns an error for an object that is not a collection.
func (s *Service) CollectionUnitIDs(ctx context.Context, obj identity.Object) ([]string, error) {
	if !obj.IsCollection() {
		return nil, fmt.Errorf("units of %s: %w", obj, identity.ErrInvalidObject)
	}

	name, err := ParseName(obj.CollectionName())
	if err != nil {
		return nil, fmt.Errorf("units of %s: %w", obj, err)
	}

	// Defense in depth: the store checks the ID in its transaction too. This
	// check stays so that a caller with no ID never reaches the store.
	if obj.CollectionID() == "" {
		return nil, fmt.Errorf("units of %s: %w", obj, ErrNotFound)
	}

	ids, err := s.store.ListCollectionUnitIDs(ctx, Ref{Namespace: obj.Namespace(), Name: name}, obj.CollectionID())
	if err != nil {
		return nil, fmt.Errorf("units of %s: %w", obj, err)
	}

	return ids, nil
}

// CollectionID returns the stable ID of the collection obj, with true. It
// returns false and no error when no such collection exists. A grant on a
// collection is keyed by this ID, so a deleted and re-created name does not
// inherit its grants (ADR 52). It returns an error for an object that is not a
// collection, and for a real store failure.
func (s *Service) CollectionID(ctx context.Context, obj identity.Object) (string, bool, error) {
	if !obj.IsCollection() {
		return "", false, fmt.Errorf("collection id of %s: %w", obj, identity.ErrInvalidObject)
	}

	name, err := ParseName(obj.CollectionName())
	if err != nil {
		return "", false, fmt.Errorf("collection id of %s: %w", obj, err)
	}

	c, err := s.store.GetCollection(ctx, Ref{Namespace: obj.Namespace(), Name: name})
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("collection id of %s: %w", obj, err)
	}

	return c.ID, true, nil
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}
