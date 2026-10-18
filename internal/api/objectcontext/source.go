package objectcontext

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// errNoReaderList reports a call of the access port that this API does not
// need: the read document of a namespace or a unit has no dependencies
// section, so it never asks which units a caller can read.
var errNoReaderList = errors.New("the context API lists no readable units")

// nounOf returns the word of a tool for its unit: "stack" for the Pulumi CLI
// and "workspace" for OpenTofu. A tool that the API does not know
// uses "unit".
func nounOf(tool string) string {
	switch tool {
	case "pulumi":
		return "stack"
	case "opentofu":
		return "workspace"
	default:
		return "unit"
	}
}

// requestSource implements the source ports of the read document for one
// request: the inventory facts and the access rules of the caller. It holds
// the target, so it needs no second lookup of the unit.
type requestSource struct {
	h *Handler
	p identity.Principal
	t target
}

var (
	_ objectcontext.InventoryReader = (*requestSource)(nil)
	_ objectcontext.Access          = (*requestSource)(nil)
)

// Chain implements objectcontext.InventoryReader: the unit, its collection if
// it has one, and its namespace. A unit has no blueprint layer yet.
func (s requestSource) Chain(ctx context.Context, object objectcontext.Ref) ([]objectcontext.Ref, error) {
	if object != s.t.ref {
		return nil, objectcontext.ErrNotFound
	}

	if object.Kind != objectcontext.KindUnit {
		return []objectcontext.Ref{object}, nil
	}

	chain := []objectcontext.Ref{object}

	if s.h.collections != nil {
		name, ok, err := s.h.collections.CollectionOf(ctx, s.t.obj)
		if err != nil {
			return nil, fmt.Errorf("collection of unit: %w", err)
		}

		if ok {
			chain = append(chain, objectcontext.CollectionRef(object.Namespace, name))
		}
	}

	return append(chain, objectcontext.NamespaceRef(object.Namespace)), nil
}

// Identity implements objectcontext.InventoryReader.
func (s requestSource) Identity(_ context.Context, object objectcontext.Ref) (objectcontext.IdentityData, error) {
	if object != s.t.ref {
		return objectcontext.IdentityData{}, objectcontext.ErrNotFound
	}

	switch {
	case s.t.unit != nil:
		return objectcontext.IdentityData{
			Name: s.t.unit.Name, DisplayName: s.t.unit.DisplayName, Noun: nounOf(s.t.unit.Tool),
			Collection: s.t.unit.Collection, Blueprint: "", Type: "", Key: "",
		}, nil
	case s.t.deleted != nil:
		return objectcontext.IdentityData{
			Name: s.t.deleted.Name, DisplayName: "", Noun: nounOf(s.t.deleted.Tool),
			Collection: "", Blueprint: "", Type: "", Key: "",
		}, nil
	default:
		return objectcontext.IdentityData{}, objectcontext.ErrUnavailable
	}
}

// Outputs implements objectcontext.InventoryReader. The section is not on:
// the server does not read outputs for this API yet.
func (requestSource) Outputs(context.Context, graph.Unit, int) (objectcontext.OutputsData, error) {
	return objectcontext.OutputsData{Outputs: nil, Truncated: false}, objectcontext.ErrUnavailable
}

// Health implements objectcontext.InventoryReader from the inventory entry of
// the unit. A deleted unit has no health.
func (s requestSource) Health(context.Context, graph.Unit) (objectcontext.HealthData, error) {
	u := s.t.unit
	if u == nil {
		return objectcontext.HealthData{}, objectcontext.ErrUnavailable
	}

	out := objectcontext.HealthData{
		LatestItemAt: u.LastChanged, NewestJob: nil, OpenFindings: nil, Status: u.Status, LockedBy: "", Locked: false,
	}

	if u.Lock != nil {
		out.Locked, out.LockedBy = true, u.Lock.Holder
	}

	return out, nil
}

// Layers implements objectcontext.Access.
func (s requestSource) Layers(ctx context.Context, chain []objectcontext.Ref) ([]bool, error) {
	out := make([]bool, len(chain))

	for i, ref := range chain {
		obj, err := s.objectFor(ref)
		if err != nil {
			return nil, err
		}

		if out[i], err = s.allowed(ctx, objectcontext.ReadAction(ref.Kind), obj); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// Holds implements objectcontext.Access.
func (s requestSource) Holds(ctx context.Context, action identity.Action, object objectcontext.Ref) (bool, error) {
	obj, err := s.objectFor(object)
	if err != nil {
		return false, err
	}

	return s.allowed(ctx, action, obj)
}

// Readable implements objectcontext.Access. The read document of this API
// has no section that names other units, so it never calls this method.
func (requestSource) Readable(context.Context, []graph.Unit) (map[graph.Unit]bool, error) {
	return nil, errNoReaderList
}

// objectFor returns the authorization object of a layer of the chain of the
// target.
func (s requestSource) objectFor(ref objectcontext.Ref) (identity.Object, error) {
	switch ref.Kind {
	case objectcontext.KindNamespace:
		return identity.NamespaceObject(ref.Namespace), nil
	case objectcontext.KindCollection:
		return identity.CollectionObject(ref.Namespace, ref.Name), nil
	case objectcontext.KindUnit:
		if ref == s.t.ref {
			return s.t.obj, nil
		}

		return identity.Object{}, fmt.Errorf("%w: %s is not in the chain", objectcontext.ErrInvalidObject, ref.Key())
	case objectcontext.KindResource, objectcontext.KindBlueprint:
		return identity.Object{}, fmt.Errorf("%w: %s has no layer", objectcontext.ErrInvalidObject, ref.Kind)
	default:
		return identity.Object{}, fmt.Errorf("%w: kind %q", objectcontext.ErrInvalidObject, ref.Kind)
	}
}

// allowed reports whether the caller holds action on object.
func (s requestSource) allowed(ctx context.Context, action identity.Action, object identity.Object) (bool, error) {
	err := s.h.authz.Authorize(ctx, s.p, action, object)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, identity.ErrDenied):
		return false, nil
	default:
		return false, fmt.Errorf("authorize %s: %w", action, err)
	}
}
