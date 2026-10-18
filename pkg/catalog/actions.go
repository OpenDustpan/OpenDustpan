package catalog

import (
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Actions of the Catalog. Each comment names the object of the action. The
// REST API declares them on its routes.
const (
	// ActionBlueprintRead allows reading a blueprint, its draft, its
	// versions and its form. Its object is a blueprint, or the namespace for
	// a list.
	ActionBlueprintRead identity.Action = "blueprint:read"
	// ActionBlueprintPublish allows writing the draft of a blueprint and
	// publishing it as a version. A publish also needs unit:read on each
	// source of a choice. Its object is a blueprint, or the namespace for a
	// new one.
	ActionBlueprintPublish identity.Action = "blueprint:publish"
	// ActionBlueprintRetire allows retiring a version. Its object is a
	// blueprint.
	ActionBlueprintRetire identity.Action = "blueprint:retire"
	// ActionCatalogRequest allows submitting a create from a blueprint,
	// reading the options of its choices, and checking a document with no
	// submit. Its object is a blueprint.
	ActionCatalogRequest identity.Action = "catalog:request"
	// ActionUnitEditInputs allows submitting an update of the request of a
	// unit, also an upgrade of its blueprint version. Its object is a unit.
	ActionUnitEditInputs identity.Action = "unit:edit-inputs"
	// ActionUnitRequestDestroy allows submitting a revision that asks for
	// the unit to be absent. Its object is a unit.
	ActionUnitRequestDestroy identity.Action = "unit:request-destroy"
	// ActionCatalogRead allows reading requests, their revisions, their
	// checks and their decisions. Its object is a unit, or the namespace for
	// a list.
	ActionCatalogRead identity.Action = "catalog:read"
	// ActionCatalogRender allows rendering a request document for the
	// team's CI. Its object is a unit.
	ActionCatalogRender identity.Action = "catalog:render"
	// ActionNamespaceManagePolicy allows binding policy engines to kinds,
	// and setting the namespace policy documents and the settings of the
	// adapters. Its object is a namespace.
	ActionNamespaceManagePolicy identity.Action = "namespace:manage-policy"
)

// ObjectKind names the kind of object of a Catalog action.
type ObjectKind string

// The kinds of object.
const (
	ObjectNamespace  ObjectKind = "namespace"
	ObjectCollection ObjectKind = "collection"
	ObjectBlueprint  ObjectKind = "blueprint"
	ObjectUnit       ObjectKind = "unit"
)

// Object is the object of a Catalog action: a namespace, a blueprint, or
// the unit of a request, and a collection for the sources of a publish.
// The identity package has no blueprint kind yet, so the Catalog names its
// objects with this type, and Identity gives the identity form of the
// other kinds, which Unit holds for a unit and a collection.
type Object struct {
	Unit      identity.Object
	Kind      ObjectKind
	Namespace namespace.Name
	Blueprint string
}

// Identity returns o as an identity object, and false for a blueprint.
func (o Object) Identity() (identity.Object, bool) {
	if o.Kind == ObjectNamespace {
		return identity.NamespaceObject(o.Namespace), true
	}

	if o.Kind == ObjectUnit || o.Kind == ObjectCollection {
		return o.Unit, true
	}

	return identity.Object{}, false
}

// Requirement is one action that a request needs, on one object.
type Requirement struct {
	Action identity.Action
	Object Object
}

// Op names an operation of the Catalog API.
type Op string

// The operations of the Catalog API.
const (
	OpListBlueprints Op = "list-blueprints"
	OpReadBlueprint  Op = "read-blueprint"
	OpWriteDraft     Op = "write-draft"
	OpRetire         Op = "retire"
	OpOptions        Op = "options"
	OpCheck          Op = "check"
	OpSubmit         Op = "submit"
	OpListRequests   Op = "list-requests"
	OpReadRequest    Op = "read-request"
	OpCancel         Op = "cancel"
	OpRender         Op = "render"
	OpManagePolicy   Op = "manage-policy"
)

// Target is what an operation acts on. Blueprint is the blueprint of the
// operation, and Unit the unit of the request, or the zero value for a
// create. Operation and BreakGlass are for OpSubmit.
type Target struct {
	Unit       identity.Object
	Namespace  namespace.Name
	Blueprint  string
	Operation  Operation
	BreakGlass bool
}

// Requirements returns the actions that op on t needs. A submit needs the
// action of its operation: catalog:request on the blueprint for a create,
// unit:edit-inputs on the unit for an update, and unit:request-destroy on
// the unit for a destroy. A break-glass also needs the break-glass action
// of the job model on the namespace. A cancel needs the cancel action of
// the job model. It returns ErrInvalid for an unknown operation, and for a
// target that lacks the object of the operation.
func Requirements(op Op, t Target) ([]Requirement, error) {
	ns := Object{Unit: identity.Object{}, Kind: ObjectNamespace, Namespace: t.Namespace, Blueprint: ""}
	bp := Object{Unit: identity.Object{}, Kind: ObjectBlueprint, Namespace: t.Namespace, Blueprint: t.Blueprint}
	unit := Object{Unit: t.Unit, Kind: ObjectUnit, Namespace: t.Namespace, Blueprint: t.Blueprint}

	if op == OpSubmit {
		return submitRequirements(t, ns, bp, unit)
	}

	r, ok := simpleRequirements()[op]
	if !ok {
		return nil, fmt.Errorf("%w: operation %q", ErrInvalid, op)
	}

	switch r.on {
	case ObjectBlueprint:
		if t.Blueprint == "" {
			return nil, fmt.Errorf("%w: %s needs a blueprint", ErrInvalid, op)
		}

		return []Requirement{{Action: r.action, Object: bp}}, nil
	case ObjectUnit:
		if !t.Unit.IsUnit() {
			return nil, fmt.Errorf("%w: %s needs a unit", ErrInvalid, op)
		}

		return []Requirement{{Action: r.action, Object: unit}}, nil
	case ObjectNamespace, ObjectCollection:
		return []Requirement{{Action: r.action, Object: ns}}, nil
	default:
		return nil, fmt.Errorf("%w: operation %q", ErrInvalid, op)
	}
}

type simpleRequirement struct {
	action identity.Action
	on     ObjectKind
}

// simpleRequirements maps each operation but OpSubmit to its one action and
// the kind of its object.
func simpleRequirements() map[Op]simpleRequirement {
	return map[Op]simpleRequirement{
		OpListBlueprints: {action: ActionBlueprintRead, on: ObjectNamespace},
		OpReadBlueprint:  {action: ActionBlueprintRead, on: ObjectBlueprint},
		OpWriteDraft:     {action: ActionBlueprintPublish, on: ObjectBlueprint},
		OpRetire:         {action: ActionBlueprintRetire, on: ObjectBlueprint},
		OpOptions:        {action: ActionCatalogRequest, on: ObjectBlueprint},
		OpCheck:          {action: ActionCatalogRequest, on: ObjectBlueprint},
		OpListRequests:   {action: ActionCatalogRead, on: ObjectNamespace},
		OpReadRequest:    {action: ActionCatalogRead, on: ObjectUnit},
		OpCancel:         {action: job.ActionJobCancel, on: ObjectUnit},
		OpRender:         {action: ActionCatalogRender, on: ObjectUnit},
		OpManagePolicy:   {action: ActionNamespaceManagePolicy, on: ObjectNamespace},
	}
}

func submitRequirements(t Target, ns, bp, unit Object) ([]Requirement, error) {
	var out []Requirement

	switch t.Operation {
	case OperationCreate:
		if t.Blueprint == "" {
			return nil, fmt.Errorf("%w: a create needs a blueprint", ErrInvalid)
		}

		out = append(out, Requirement{Action: ActionCatalogRequest, Object: bp})
	case OperationUpdate, OperationNone, OperationDestroy:
		if !t.Unit.IsUnit() {
			return nil, fmt.Errorf("%w: a submit of %s needs a unit", ErrInvalid, t.Operation)
		}

		action := ActionUnitEditInputs
		if t.Operation == OperationDestroy {
			action = ActionUnitRequestDestroy
		}

		out = append(out, Requirement{Action: action, Object: unit})
	default:
		return nil, fmt.Errorf("%w: submit of the operation %q", ErrInvalid, t.Operation)
	}

	if t.BreakGlass {
		out = append(out, Requirement{Action: job.ActionNamespaceBreakFreeze, Object: ns})
	}

	return out, nil
}

// PublishRequirements returns the actions that a publish of b in ns needs:
// blueprint:publish on the blueprint, and unit:read on the source of each
// choice. A source of the units of a collection needs it on the collection,
// and a source of the units of a blueprint on the namespace, because those
// units can be in any collection. The list has no duplicate.
func PublishRequirements(ns namespace.Name, b *Blueprint) []Requirement {
	out := []Requirement{{
		Action: ActionBlueprintPublish,
		Object: Object{Unit: identity.Object{}, Kind: ObjectBlueprint, Namespace: ns, Blueprint: b.Name},
	}}

	for i := range b.Inputs {
		c := b.Inputs[i].Choice
		if c == nil || c.Source == nil {
			continue
		}

		r := Requirement{
			Action: identity.ActionUnitRead,
			Object: Object{Unit: identity.Object{}, Kind: ObjectNamespace, Namespace: ns, Blueprint: ""},
		}
		if c.Source.Collection != "" {
			r.Object = Object{
				Unit: identity.CollectionObject(ns, c.Source.Collection), Kind: ObjectCollection, Namespace: ns,
				Blueprint: "",
			}
		}

		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}

	return out
}
