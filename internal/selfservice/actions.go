package selfservice

import (
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Actions of the Self-service. Each comment names the object of the action. The
// REST API declares them on its routes.
const (
	// ActionBlueprintRead allows reading a blueprint, its draft, its
	// versions, its form and its context. Its object is a blueprint, or the
	// namespace for a list.
	ActionBlueprintRead = identity.ActionBlueprintRead
	// ActionBlueprintPublish allows writing the draft of a blueprint and
	// publishing it as a version. A publish that names a source principal
	// also needs principal:manage-tokens on that principal. Its object is a
	// blueprint, or the namespace for a new one.
	ActionBlueprintPublish identity.Action = "blueprint:publish"
	// ActionBlueprintRetire allows retiring a version. Its object is a
	// blueprint.
	ActionBlueprintRetire identity.Action = "blueprint:retire"
	// ActionSelfServiceRequest allows submitting a create from a blueprint,
	// and, for a create, reading the options of its choices, checking a
	// document with no submit and rendering a document from git. Its object
	// is a blueprint.
	ActionSelfServiceRequest identity.Action = "self-service:request"
	// ActionUnitEditInputs allows submitting an update of the request of a
	// unit, also an upgrade of its blueprint version, and, for an update,
	// reading the options, checking a document and rendering a document
	// from git. Its object is a unit.
	ActionUnitEditInputs identity.Action = "unit:edit-inputs"
	// ActionUnitRequestDestroy allows submitting a revision that asks for
	// the unit to be absent, and checking or rendering such a document. Its
	// object is a unit.
	ActionUnitRequestDestroy identity.Action = "unit:request-destroy"
	// ActionSelfServiceRead allows reading requests, their revisions, their
	// checks and their decisions. Its object is a unit, or the namespace for
	// a list.
	ActionSelfServiceRead identity.Action = "self-service:read"
	// ActionSelfServiceRender allows rendering a request document from git for
	// the team's CI. A render also needs the action of its operation. Its
	// object is the unit, or the blueprint for a create.
	ActionSelfServiceRender identity.Action = "self-service:render"
	// ActionNamespaceManagePolicy allows binding policy engines to kinds,
	// and setting the namespace policy documents and the settings of the
	// adapters. Its object is a namespace.
	ActionNamespaceManagePolicy identity.Action = "namespace:manage-policy"
)

// ObjectKind names the kind of object of a Self-service action.
type ObjectKind string

// The kinds of object.
const (
	ObjectNamespace  ObjectKind = "namespace"
	ObjectCollection ObjectKind = "collection"
	ObjectBlueprint  ObjectKind = "blueprint"
	ObjectUnit       ObjectKind = "unit"
	ObjectPrincipal  ObjectKind = "principal"
)

// Object is the object of a Self-service action: a namespace, a blueprint, or
// the unit of a request, a collection for a source of a choice, and a
// principal for the source principal of a publish. The identity package has
// no blueprint kind yet, so the Self-service names its objects with this type,
// and Identity gives the identity form of the other kinds, which Unit holds
// for a unit, a collection and a principal.
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

	if o.Kind == ObjectUnit || o.Kind == ObjectCollection || o.Kind == ObjectPrincipal {
		return o.Unit, true
	}

	return identity.Object{}, false
}

// Requirement is one action that a request needs, on one object.
type Requirement struct {
	Action identity.Action
	Object Object
}

// Op names an operation of the Self-service API.
type Op string

// The operations of the Self-service API.
const (
	OpListBlueprints Op = "list-blueprints"
	OpReadBlueprint  Op = "read-blueprint"
	OpWriteDraft     Op = "write-draft"
	OpPublish        Op = "publish"
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
// create. Operation is for OpSubmit, OpCheck, OpOptions and OpRender, and
// BreakGlass for OpSubmit.
type Target struct {
	Unit       identity.Object
	Namespace  namespace.Name
	Blueprint  string
	Operation  Operation
	BreakGlass bool
}

// Requirements returns the actions that op on t needs. A submit, a check
// and a read of the options need the action of their operation:
// self-service:request on the blueprint for a create, unit:edit-inputs on the
// unit for an update, and unit:request-destroy on the unit for a destroy.
// A render needs self-service:render, on the unit or on the blueprint for a
// create, and the action of its operation, so a render from git never does
// more than a submit of the same document can. A render that stores no
// revision needs self-service:render only. A break-glass also needs the
// break-glass action of the job model on the namespace. A cancel needs the
// cancel action of the job model. It returns ErrInvalid for an unknown
// operation, and for a target that lacks the object of the operation.
func Requirements(op Op, t Target) ([]Requirement, error) {
	ns := Object{Unit: identity.Object{}, Kind: ObjectNamespace, Namespace: t.Namespace, Blueprint: ""}
	bp := Object{Unit: identity.Object{}, Kind: ObjectBlueprint, Namespace: t.Namespace, Blueprint: t.Blueprint}
	unit := Object{Unit: t.Unit, Kind: ObjectUnit, Namespace: t.Namespace, Blueprint: t.Blueprint}

	if op == OpSubmit || op == OpCheck || op == OpOptions {
		out, err := operationRequirement(op, t, bp, unit)
		if err != nil {
			return nil, err
		}

		if op == OpSubmit && t.BreakGlass {
			out = append(out, Requirement{Action: job.ActionNamespaceBreakFreeze, Object: ns})
		}

		return out, nil
	}

	if op == OpRender {
		return renderRequirements(t, bp, unit)
	}

	return simpleRequirementsOf(op, t, ns, bp, unit)
}

// simpleRequirementsOf returns the one action of op from
// simpleRequirements, on its object.
func simpleRequirementsOf(op Op, t Target, ns, bp, unit Object) ([]Requirement, error) {
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
	case ObjectNamespace:
		return []Requirement{{Action: r.action, Object: ns}}, nil
	case ObjectCollection, ObjectPrincipal:
		return nil, fmt.Errorf("%w: no operation of the API acts on a %s", ErrInvalid, r.on)
	default:
		return nil, fmt.Errorf("%w: operation %q", ErrInvalid, op)
	}
}

type simpleRequirement struct {
	action identity.Action
	on     ObjectKind
}

// simpleRequirements maps each operation whose action does not depend on
// the operation of a revision to its one action and the kind of its object.
func simpleRequirements() map[Op]simpleRequirement {
	return map[Op]simpleRequirement{
		OpListBlueprints: {action: ActionBlueprintRead, on: ObjectNamespace},
		OpReadBlueprint:  {action: ActionBlueprintRead, on: ObjectBlueprint},
		OpWriteDraft:     {action: ActionBlueprintPublish, on: ObjectBlueprint},
		OpPublish:        {action: ActionBlueprintPublish, on: ObjectBlueprint},
		OpRetire:         {action: ActionBlueprintRetire, on: ObjectBlueprint},
		OpListRequests:   {action: ActionSelfServiceRead, on: ObjectNamespace},
		OpReadRequest:    {action: ActionSelfServiceRead, on: ObjectUnit},
		OpCancel:         {action: job.ActionJobCancel, on: ObjectUnit},
		OpManagePolicy:   {action: ActionNamespaceManagePolicy, on: ObjectNamespace},
	}
}

// operationRequirement returns the action of the operation of t, on the
// blueprint for a create and on the unit else. A document equal to the last
// applied revision needs the action of an update.
func operationRequirement(op Op, t Target, bp, unit Object) ([]Requirement, error) {
	switch t.Operation {
	case OperationCreate:
		if t.Blueprint == "" {
			return nil, fmt.Errorf("%w: %s of a create needs a blueprint", ErrInvalid, op)
		}

		return []Requirement{{Action: ActionSelfServiceRequest, Object: bp}}, nil
	case OperationUpdate, OperationNone, OperationDestroy:
		if !t.Unit.IsUnit() {
			return nil, fmt.Errorf("%w: %s of %s needs a unit", ErrInvalid, op, t.Operation)
		}

		action := ActionUnitEditInputs
		if t.Operation == OperationDestroy {
			action = ActionUnitRequestDestroy
		}

		return []Requirement{{Action: action, Object: unit}}, nil
	default:
		return nil, fmt.Errorf("%w: %s of the operation %q", ErrInvalid, op, t.Operation)
	}
}

// renderRequirements returns self-service:render on the unit, or on the
// blueprint for a create, and the action of the operation of the render.
func renderRequirements(t Target, bp, unit Object) ([]Requirement, error) {
	on := unit

	switch {
	case t.Operation == OperationCreate:
		on = bp
	case !t.Unit.IsUnit():
		return nil, fmt.Errorf("%w: %s of %s needs a unit", ErrInvalid, OpRender, t.Operation)
	default:
	}

	render := Requirement{Action: ActionSelfServiceRender, Object: on}

	if t.Operation == OperationNone {
		return []Requirement{render}, nil
	}

	op, err := operationRequirement(OpRender, t, bp, unit)
	if err != nil {
		return nil, err
	}

	return append([]Requirement{render}, op...), nil
}

// PublishRequirements returns the actions that a publish of b in ns needs:
// blueprint:publish on the blueprint, and, when b names a source principal,
// principal:manage-tokens on that principal. A holder of that action can
// already act as the principal, so the publish gives the blueprint no
// grant that the publisher could not use itself. The publisher needs no
// grant on the sources: the server reads them as the source principal
// (SourceRequirements).
func PublishRequirements(ns namespace.Name, b *Blueprint) []Requirement {
	out := []Requirement{{
		Action: ActionBlueprintPublish,
		Object: Object{Unit: identity.Object{}, Kind: ObjectBlueprint, Namespace: ns, Blueprint: b.Name},
	}}

	if b.SourcePrincipal != "" {
		out = append(out, Requirement{
			Action: identity.ActionPrincipalManageTokens,
			Object: Object{
				Unit: identity.PrincipalObject(b.SourcePrincipal), Kind: ObjectPrincipal, Namespace: ns, Blueprint: "",
			},
		})
	}

	return out
}

// SourceRequirements returns the actions that the source principal of b
// needs to read the sources of the choices of b in ns: unit:read on the
// collection of a source of a collection, and on the namespace for a source
// of the units of a blueprint, because those units can be in any
// collection. The list has no duplicate. The server checks it for the
// source principal at each computation of the options, at the options route
// and at each recheck, so a revoke of a grant of the principal stops the
// reads at once. A missing action makes the check unable to run: the
// options route answers with the error, and a recheck stops with no
// change.
func SourceRequirements(ns namespace.Name, b *Blueprint) []Requirement {
	var out []Requirement

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
