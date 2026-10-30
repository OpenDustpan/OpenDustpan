package issuersvc

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

func keyChanges(add []profile.Key, moves []profile.KeyMove) []audit.Change {
	out := make([]audit.Change, 0, len(add)+len(moves))

	for i := range add {
		out = append(out, audit.Change{
			Key: "key." + string(add[i].ID), Old: nil, New: string(add[i].State), Layer: "", TakesEffect: "",
		})
	}

	for i := range moves {
		from := string(moves[i].From)

		out = append(out, audit.Change{
			Key: "key." + string(moves[i].ID), Old: &from, New: string(moves[i].To), Layer: "", TakesEffect: "",
		})
	}

	return out
}

// CreateKeys makes the active key and the next key of a namespace that has no
// key. ErrConflict when it has keys.
func (s *Service) CreateKeys(ctx context.Context, a *Actor, ns namespace.Name) ([]profile.KeyInfo, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return nil, err
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	keys, err := s.store.Live(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) > 0 {
		return nil, fmt.Errorf("%w: the namespace has signing keys: rotate them with POST %s/rotate",
			profile.ErrConflict, keysRoute(ns))
	}

	made, err := s.makeFirstKeys(ctx, &n, func(add []profile.Key) audit.Entry {
		return s.entry(a, ns, "", keyChanges(add, nil))
	})
	if err != nil {
		return nil, err
	}

	return infos(made), nil
}

// firstKeys makes the keys of a namespace at the first open of a login block.
// The entry names the principal of the open and why the server acted. It
// returns the keys of the namespace, also when another instance made them
// first.
func (s *Service) firstKeys(ctx context.Context, n *namespace.Namespace, by *audit.Entry) ([]profile.Key, error) {
	made, err := s.makeFirstKeys(ctx, n, func(add []profile.Key) audit.Entry {
		e := *by
		e.At = s.now().UTC()
		e.Namespace = n.Name
		e.Action = string(identity.ActionNamespaceManageIssuer)
		e.Object = identity.NamespaceObject(n.Name).String()
		e.ObjectID = ""
		e.Kind = audit.KindChange
		e.Outcome = audit.OutcomeSuccess
		e.Reason = "the first open of a login block"
		e.Changes = keyChanges(add, nil)

		return e
	})
	if errors.Is(err, profile.ErrConflict) {
		keys, readErr := s.store.Live(ctx, n.Name)
		if readErr != nil {
			return nil, fmt.Errorf("read signing keys: %w", readErr)
		}

		return keys, nil
	}

	return made, err
}

func (s *Service) makeFirstKeys(
	ctx context.Context, n *namespace.Namespace, entry func([]profile.Key) audit.Entry,
) ([]profile.Key, error) {
	now := s.now().UTC()

	active, err := s.newKey(ctx, n.Name, profile.KeyActive, now)
	if err != nil {
		return nil, err
	}

	next, err := s.newKey(ctx, n.Name, profile.KeyNext, now)
	if err != nil {
		return nil, err
	}

	add := []profile.Key{active, next}

	err = s.store.Apply(ctx, n.Name, profile.KeyChange{Add: add, Move: nil, Initial: true}, n.Fence(),
		[]audit.Entry{entry(add)})
	if err != nil {
		return nil, fmt.Errorf("store signing keys: %w", err)
	}

	return add, nil
}

func infos(keys []profile.Key) []profile.KeyInfo {
	sorted := sortedKeys(keys)
	out := make([]profile.KeyInfo, 0, len(sorted))

	for i := range sorted {
		out = append(out, sorted[i].Info())
	}

	return out
}
