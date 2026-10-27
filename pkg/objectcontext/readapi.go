package objectcontext

import (
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
)

// The actions of context. Package identity holds their strings. Each one
// names its object.
const (
	// ActionUnitSetContext allows setting and deleting the API context of a
	// unit and of its resources. Its object is the unit.
	ActionUnitSetContext = identity.ActionUnitSetContext
	// ActionCollectionSetContext allows setting and deleting the API context
	// of a collection. Its object is the collection.
	ActionCollectionSetContext = identity.ActionCollectionSetContext
	// ActionNamespaceSetContext allows setting and deleting the API context
	// of a namespace. Its object is the namespace.
	ActionNamespaceSetContext = identity.ActionNamespaceSetContext
	// ActionBlueprintRead allows reading a blueprint and its context. Its
	// object is the blueprint.
	ActionBlueprintRead = identity.ActionBlueprintRead
	// ActionBlueprintSetContext allows setting and deleting the API context
	// of a blueprint. Its object is the blueprint.
	ActionBlueprintSetContext = identity.ActionBlueprintSetContext
)

// ReadAction returns the action that reads the context of an object of kind
// k. The object of the action is the object, or the unit of a resource.
func ReadAction(k Kind) identity.Action {
	switch k {
	case KindNamespace:
		return identity.ActionNamespaceRead
	case KindCollection:
		return identity.ActionCollectionRead
	case KindBlueprint:
		return ActionBlueprintRead
	case KindUnit, KindResource:
		return identity.ActionUnitRead
	default:
		// An unknown kind gets the action of a unit.
		return ReadAction(KindUnit)
	}
}

// SetAction returns the action that sets the API context of an object of
// kind k. The object of the action is the object, or the unit of a resource.
func SetAction(k Kind) identity.Action {
	switch k {
	case KindNamespace:
		return ActionNamespaceSetContext
	case KindCollection:
		return ActionCollectionSetContext
	case KindBlueprint:
		return ActionBlueprintSetContext
	case KindUnit, KindResource:
		return ActionUnitSetContext
	default:
		// An unknown kind gets the action of a unit.
		return SetAction(KindUnit)
	}
}

// Section is one part of the answer of the context read API. A client asks
// for the sections it needs. The API checks the action of each section on
// its object, and a denied section does not fail the call.
type Section string

// The sections of the context read API.
const (
	// SectionIdentity is the reference, the tool, the tool's own name, the
	// display name, the collection and the blueprint of a unit, or the type
	// and the key of a resource.
	SectionIdentity Section = "identity"
	// SectionContext is the resolved context with the provenance of each
	// value, the shadowed API values and the output warnings.
	SectionContext Section = "context"
	// SectionReadme is the readme output of the unit.
	SectionReadme Section = "readme"
	// SectionOutputs is the outputs of the latest state item, with secret
	// values masked.
	SectionOutputs Section = "outputs"
	// SectionDependencies is the units one hop upstream and one hop
	// downstream, with the kind, the evidence and the times of each edge.
	SectionDependencies Section = "dependencies"
	// SectionChanges is the newest history items and jobs of the unit, with
	// their actors, and the resource changes of the newest state item.
	SectionChanges Section = "changes"
	// SectionHealth is the status, the lock, the outcome of the newest job,
	// the age of the latest state item and the counts of open
	// unmanaged-resource findings that name the unit.
	SectionHealth Section = "health"
	// SectionDrift is the drift status of the unit, the time and the job of
	// the latest drift check, and the first addresses of the resources that
	// differ. It holds no attribute name and no attribute value.
	SectionDrift Section = "drift"
	// SectionAudit is the newest audit entries whose object is the unit.
	SectionAudit Section = "audit"
)

// ObjectRole says on which object the API checks the action of a section.
type ObjectRole string

// The object roles.
const (
	// RoleTarget is the unit of the call, or the unit of the resource.
	RoleTarget ObjectRole = "target"
	// RoleEachLayer is the object of each layer of the context chain. A
	// layer whose action the caller does not hold is left out, and the
	// section says so.
	RoleEachLayer ObjectRole = "each-layer"
	// RoleEachNamed is the target, and each other unit that the section
	// names. A unit that the caller cannot read is counted, not named.
	RoleEachNamed ObjectRole = "each-named-unit"
	// RoleNamespace is the namespace of the target.
	RoleNamespace ObjectRole = "namespace"
)

// SectionSpec says how the API answers one section: its action, the object
// of the action, whether a call without a section list answers it, and the
// kinds of object it applies to.
type SectionSpec struct {
	Section Section
	Action  identity.Action
	Object  ObjectRole
	Kinds   []Kind
	Default bool
}

// unitKinds returns the kinds that every section of a unit applies to.
func unitKinds() []Kind {
	return []Kind{KindUnit, KindResource}
}

// SectionCatalog returns the catalog of the context read API, in the order
// of the answer. The action of SectionContext varies by layer; ReadAction
// gives it. A test checks that each section names an action and an object.
// Each call returns a new slice, so a caller cannot change the catalog.
func SectionCatalog() []SectionSpec {
	return []SectionSpec{
		{
			Section: SectionIdentity,
			Action:  identity.ActionUnitRead,
			Object:  RoleTarget,
			Kinds:   unitKinds(),
			Default: true,
		},
		{
			Section: SectionContext, Action: identity.ActionUnitRead, Object: RoleEachLayer, Default: true,
			Kinds: []Kind{KindUnit, KindResource, KindCollection, KindNamespace, KindBlueprint},
		},
		{
			Section: SectionReadme,
			Action:  identity.ActionUnitRead,
			Object:  RoleTarget,
			Kinds:   []Kind{KindUnit},
			Default: true,
		},
		{
			Section: SectionOutputs,
			Action:  identity.ActionUnitRead,
			Object:  RoleTarget,
			Kinds:   []Kind{KindUnit},
			Default: false,
		},
		{
			Section: SectionDependencies,
			Action:  identity.ActionUnitRead,
			Object:  RoleEachNamed,
			Kinds:   unitKinds(),
			Default: true,
		},
		{
			Section: SectionChanges,
			Action:  identity.ActionUnitRead,
			Object:  RoleTarget,
			Kinds:   unitKinds(),
			Default: true,
		},
		{
			Section: SectionHealth,
			Action:  identity.ActionUnitRead,
			Object:  RoleTarget,
			Kinds:   unitKinds(),
			Default: true,
		},
		{
			Section: SectionDrift,
			Action:  identity.ActionUnitRead,
			Object:  RoleTarget,
			Kinds:   unitKinds(),
			Default: false,
		},
		{
			Section: SectionAudit,
			Action:  identity.ActionNamespaceReadAudit,
			Object:  RoleNamespace,
			Kinds:   []Kind{KindUnit},
			Default: false,
		},
	}
}

// Status is the outcome of one section in an answer.
type Status string

// The outcomes of a section.
const (
	// StatusOK is a full answer.
	StatusOK Status = "ok"
	// StatusPartial is an answer that left out layers or units that the
	// caller cannot read, or that a limit cut. The section says how many.
	StatusPartial Status = "partial"
	// StatusDenied is no answer: the caller does not hold the action of the
	// section. The section names the action.
	StatusDenied Status = "denied"
	// StatusUnavailable is no answer: the server has no data for the
	// section, for example no job ran on the unit.
	StatusUnavailable Status = "unavailable"
)

// Plan returns the specs of the sections that a call for an object of kind k
// answers. An empty include gives the default sections. It returns
// ErrUnknownSection for a section that does not exist or does not apply to
// k, and ErrInvalidObject for an unknown kind.
func Plan(k Kind, include []Section) ([]SectionSpec, error) {
	if !slices.Contains(chainOrder(), k) {
		return nil, fmt.Errorf("%w: kind %q", ErrInvalidObject, k)
	}

	var out []SectionSpec

	for _, s := range SectionCatalog() {
		asked := slices.Contains(include, s.Section) || (len(include) == 0 && s.Default)
		if asked && slices.Contains(s.Kinds, k) {
			out = append(out, s)
		}
	}

	for _, want := range include {
		if !slices.ContainsFunc(out, func(s SectionSpec) bool { return s.Section == want }) {
			return nil, fmt.Errorf("%w: %q for a %s", ErrUnknownSection, want, k)
		}
	}

	return out, nil
}
