package cedargo

import (
	"encoding/json"
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"

	"opendustpan.org/opendustpan/pkg/gate"
)

// toCedar returns the Cedar request of r and the entities that it needs:
// the principal with its groups as parents, the groups, and the resource.
func toCedar(r *gate.Request) (cedar.Request, types.EntityMap, error) {
	ctx, resourceType, err := contextOf(r)
	if err != nil {
		return cedar.Request{}, nil, err
	}

	entities := types.EntityMap{}
	parents := make([]types.EntityUID, 0, len(r.Groups))

	for _, g := range r.Groups {
		uid := types.NewEntityUID(typeGroup, types.String(g))
		parents = append(parents, uid)
		entities[uid] = types.Entity{
			UID:        uid,
			Parents:    types.NewEntityUIDSet(),
			Attributes: types.NewRecord(nil),
			Tags:       types.NewRecord(nil),
		}
	}

	principal := types.NewEntityUID(typePrincipal, types.String(r.Principal))
	entities[principal] = types.Entity{
		UID: principal, Parents: types.NewEntityUIDSet(parents...),
		Attributes: types.NewRecord(nil), Tags: types.NewRecord(nil),
	}

	resource := types.NewEntityUID(resourceType, types.String(r.Resource.String()))
	entities[resource] = types.Entity{
		UID: resource, Parents: types.NewEntityUIDSet(),
		Attributes: types.NewRecord(nil), Tags: types.NewRecord(nil),
	}

	return cedar.Request{
		Principal: principal,
		Action:    types.NewEntityUID(typeAction, types.String(r.Action)),
		Resource:  resource,
		Context:   ctx,
	}, entities, nil
}

// contextOf returns the Cedar context of r, and the entity type of its
// resource: a unit at a decision point, an object for an access limit.
func contextOf(r *gate.Request) (types.Record, types.EntityType, error) {
	if r.Facts != nil {
		rec, err := record(r.Facts)

		return rec, typeUnit, err
	}

	access := *r.Access
	if access.Unit.Labels == nil {
		access.Unit.Labels = []string{}
	}

	rec, err := record(access)

	return rec, typeObject, err
}

// record returns the fields of doc, but "schema", as a Cedar record. The
// JSON form of the facts has a value for each fact, so the record has too.
func record(doc any) (types.Record, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return types.Record{}, fmt.Errorf("%w: facts: %w", gate.ErrInvalid, err)
	}

	fields := map[string]json.RawMessage{}

	if err = json.Unmarshal(raw, &fields); err != nil {
		return types.Record{}, fmt.Errorf("%w: facts: %w", gate.ErrInvalid, err)
	}

	delete(fields, "schema")

	raw, err = json.Marshal(fields)
	if err != nil {
		return types.Record{}, fmt.Errorf("%w: facts: %w", gate.ErrInvalid, err)
	}

	var rec types.Record

	if err = rec.UnmarshalJSON(raw); err != nil {
		return types.Record{}, fmt.Errorf("%w: facts: %w", gate.ErrInvalid, err)
	}

	return rec, nil
}
