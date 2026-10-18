package lifecycle

import (
	"fmt"
	"maps"
	"slices"

	"opendustpan.org/providerkit/schema"
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
	// Planned is the expected values after the apply, as the engine gets
	// them in Create or Update: the configuration, with the write-only
	// values; prior values for stable computed attributes and the id;
	// unknown values for what the remote system sets. It is nil for a
	// delete. An adapter shows a tool ForTool(schema, Planned), never
	// Planned itself.
	Planned schema.Values
	// Changed names the attributes of the configuration whose planned value
	// differs from the prior value, in schema order. A computed attribute
	// that an update makes unknown is not in it.
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
//   - A write-only attribute takes the configuration value in Planned, so
//     the engine can send it. It never counts as changed.
//   - A computed attribute is unknown on create and replace. On an update it
//     is unknown too, because an API can change it on any write. The id and
//     a stable attribute keep their prior values on an update. Every
//     computed attribute keeps its prior value when nothing changes.
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
		unknownOnUpdate(s, config, planned)
	default:
	}

	return change, nil
}

// ForTool returns a copy of vs with every write-only attribute null. An
// adapter passes each value set to its tool through it: the planned values
// of a change, and the values that Create, Read and Update return. A tool
// then never stores a write-only value.
func ForTool(s schema.Schema, vs schema.Values) schema.Values {
	if vs == nil {
		return nil
	}

	out := maps.Clone(vs)

	for _, a := range s.Attributes {
		if _, ok := out[a.Name]; ok && a.WriteOnly {
			out[a.Name] = schema.NullOf(a.Type)
		}
	}

	return out
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
		if setByRemote(a, config) {
			planned[a.Name] = schema.UnknownOf(a.Type)
		} else {
			planned[a.Name] = config.Get(a)
		}
	}

	return planned
}

// keep returns the planned value of one attribute of an existing object.
func keep(a schema.Attribute, prior, config schema.Values) schema.Value {
	if setByRemote(a, config) {
		return prior.Get(a)
	}

	return config.Get(a)
}

// unknownOnUpdate makes each computed value of an update unknown, except the
// id and the stable attributes.
func unknownOnUpdate(s schema.Schema, config, planned schema.Values) {
	for _, a := range s.Attributes {
		if setByRemote(a, config) && !a.Stable && a.Name != schema.IDAttribute {
			planned[a.Name] = schema.UnknownOf(a.Type)
		}
	}
}

// setByRemote reports whether the remote system sets the value: the
// attribute is computed, or optional computed with no configured value.
func setByRemote(a schema.Attribute, config schema.Values) bool {
	return a.Mode == schema.ModeComputed || a.Mode == schema.ModeOptionalComputed && config.Get(a).IsNull()
}
