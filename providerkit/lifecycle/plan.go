package lifecycle

import (
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/providerkit/schema"
)

// Action is what a plan does to one object.
type Action uint8

// The actions of a plan.
const (
	// ActionNoOp: nothing changes.
	ActionNoOp Action = iota
	// ActionCreate: the object does not exist and the configuration has it.
	ActionCreate
	// ActionUpdate: a changed attribute can change in place.
	ActionUpdate
	// ActionReplace: a changed attribute forces a new object. The adapter
	// deletes and creates, in the order that its tool asks for.
	ActionReplace
	// ActionDelete: the object exists and the configuration does not have
	// it.
	ActionDelete
)

// String returns the name of the action.
func (a Action) String() string {
	switch a {
	case ActionNoOp:
		return "no-op"
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionReplace:
		return "replace"
	case ActionDelete:
		return "delete"
	}

	return fmt.Sprintf("action(%d)", uint8(a))
}

// Change is the plan for one object.
type Change struct {
	// Action is what the apply does.
	Action Action
	// Planned is the expected values after the apply: the configuration,
	// plus prior values for computed attributes, plus unknown values for
	// what the remote system sets. It is nil for a delete.
	Planned schema.Values
	// Changed names the attributes whose planned value differs from the
	// prior value, in schema order.
	Changed []string
	// ReplacedBy names the changed attributes that force a replace, in
	// schema order.
	ReplacedBy []string
}

// Plan decides the change for one object of a resource schema. Prior is nil
// when the object does not exist. Config is nil when the configuration does
// not have the object. Both protocol adapters call Plan, so OpenTofu and
// Pulumi show the same change for the same declaration.
//
// The rules:
//   - A write-only attribute is null in Planned and never counts as changed.
//   - A computed attribute keeps its prior value, or is unknown on create and
//     replace.
//   - An optional computed attribute takes the configuration value when it
//     is not null, and else behaves as a computed attribute.
//   - An unknown configuration value counts as changed.
//
// Errors: ErrInvalidConfig when config sets an attribute that the schema does
// not have, sets a computed attribute, or leaves a required one null.
func Plan(s schema.Schema, prior, config schema.Values) (Change, error) {
	if config == nil {
		if prior == nil {
			return Change{Action: ActionNoOp, Planned: nil, Changed: nil, ReplacedBy: nil}, nil
		}

		return Change{Action: ActionDelete, Planned: nil, Changed: nil, ReplacedBy: nil}, nil
	}

	if err := CheckConfig(s, config); err != nil {
		return Change{Action: ActionNoOp, Planned: nil, Changed: nil, ReplacedBy: nil}, err
	}

	if prior == nil {
		return Change{Action: ActionCreate, Planned: fresh(s, config), Changed: nil, ReplacedBy: nil}, nil
	}

	planned := make(schema.Values, len(s.Attributes))
	change := Change{Action: ActionNoOp, Planned: planned, Changed: nil, ReplacedBy: nil}

	for _, a := range s.Attributes {
		planned[a.Name] = keep(a, prior, config)
		if a.WriteOnly || planned[a.Name].Equal(prior.Get(a)) {
			continue
		}

		change.Changed = append(change.Changed, a.Name)
		if a.Replace {
			change.ReplacedBy = append(change.ReplacedBy, a.Name)
		}
	}

	switch {
	case len(change.ReplacedBy) > 0:
		change.Action = ActionReplace
		change.Planned = fresh(s, config)
	case len(change.Changed) > 0:
		change.Action = ActionUpdate
	default:
	}

	return change, nil
}

// CheckConfig returns ErrInvalidConfig when config sets an attribute that the
// schema does not have, sets a computed attribute, or leaves a required one
// null. A data source and a provider configuration use it too.
func CheckConfig(s schema.Schema, config schema.Values) error {
	names := make([]string, 0, len(config))
	for name := range config {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		a, ok := s.Lookup(name)
		if !ok {
			return fmt.Errorf("%w: unknown attribute %q", ErrInvalidConfig, name)
		}

		if !a.Mode.UserSets() && !config[name].IsNull() {
			return fmt.Errorf("%w: attribute %q is computed and cannot be set", ErrInvalidConfig, name)
		}
	}

	for _, a := range s.Attributes {
		if a.Mode == schema.ModeRequired && config.Get(a).IsNull() {
			return fmt.Errorf("%w: attribute %q is required", ErrInvalidConfig, a.Name)
		}
	}

	return nil
}

// fresh returns the planned values of a new object.
func fresh(s schema.Schema, config schema.Values) schema.Values {
	planned := make(schema.Values, len(s.Attributes))

	for _, a := range s.Attributes {
		switch {
		case a.WriteOnly:
			planned[a.Name] = schema.NullOf(a.Type)
		case a.Mode == schema.ModeComputed,
			a.Mode == schema.ModeOptionalComputed && config.Get(a).IsNull():
			planned[a.Name] = schema.UnknownOf(a.Type)
		default:
			planned[a.Name] = config.Get(a)
		}
	}

	return planned
}

// keep returns the planned value of one attribute of an existing object.
func keep(a schema.Attribute, prior, config schema.Values) schema.Value {
	switch {
	case a.WriteOnly:
		return schema.NullOf(a.Type)
	case a.Mode == schema.ModeComputed,
		a.Mode == schema.ModeOptionalComputed && config.Get(a).IsNull():
		return prior.Get(a)
	default:
		return config.Get(a)
	}
}
