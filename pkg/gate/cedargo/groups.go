package cedargo

import (
	"fmt"

	"github.com/cedar-policy/cedar-go/types"
	xast "github.com/cedar-policy/cedar-go/x/exp/ast"

	"opendustpan.org/opendustpan/pkg/gate"
)

// GroupDirections reports the changes of the members of a group that can
// lift the forbid of one access limit or the match of one gate.
type GroupDirections struct {
	// Add reports that an add of a member can lift the forbid.
	Add bool
	// Remove reports that a remove of a member can lift the forbid.
	Remove bool
}

// LimitGroupDirections returns, for each group name that the text of an
// access limit names, the changes of its members that can lift the forbid.
//
// A forbid holds for a principal when its scope matches, each when clause is
// true and no unless clause is true. So an add of a member to a group that
// an unless clause names can lift the forbid for that member, and a remove
// of a member from a group that a when clause or the principal scope names
// can lift it too. A group in any other position is read as one that both
// changes can lift: such a text is refused at save, and both directions are
// the safe answer for a stored row that no save wrote.
//
// A membership test of principal must name a group or a set of groups. It
// returns ErrLimitMembership for a text that names another value: the
// server cannot tell which group such a limit names, so the save refuses
// the text and a reader of a stored row refuses the change.
//
// It returns errPolicy for a text that the engine refuses, and for a text
// that holds a count of policies other than one.
func LimitGroupDirections(text string) (map[string]GroupDirections, error) {
	policies, err := parseAll(text)
	if err != nil {
		return nil, err
	}

	if len(policies) != 1 {
		return nil, fmt.Errorf(
			"%w: the text holds %d policies, and an access limit holds one",
			errPolicy,
			len(policies),
		)
	}

	p := policies[0]

	directions := &groupDirections{byName: map[string]GroupDirections{}, gatePath: false}

	// The scope of a policy is not a condition, so the walk below never
	// sees it: read the scope of each of the three variables too.
	if err := directions.addPrincipalScope(p.Principal); err != nil {
		return nil, err
	}

	directions.addOtherScope(p.Action)
	directions.addOtherScope(p.Resource)

	for _, c := range p.Conditions {
		pol := positive
		if c.Condition == xast.ConditionUnless {
			pol = negative
		}

		if err := directions.addClause(c.Body, pol); err != nil {
			return nil, err
		}
	}

	return directions.byName, nil
}

// GateGroupDirections returns, for each group name that the condition or the
// unless of a gate names, the changes of its members that can lift the gate.
//
// A gate matches when its condition is true and its unless is false. So an
// add of a member to a group that the unless names can lift the gate for that
// member, and a remove of a member from a group that the condition names can
// lift it too. A group in any other position is read as one that both changes
// can lift: such a text is refused at save, and both directions are the safe
// answer for a stored row that no save wrote.
//
// An empty clause names no group.
//
// A membership test of principal names a group or a set of groups, or it
// names no group: a right side that holds no group literal names no group,
// because the gate schema has no attribute of a group type, so no change of
// the members of a group can change the answer of that test. A right side that
// holds a group literal in another shape returns ErrLimitMembership: the
// server cannot tell which group such a gate names, so a reader of a stored
// row refuses the change, and the answer fails closed.
//
// It returns errPolicy for a clause that the engine refuses.
func GateGroupDirections(condition, unless string) (map[string]GroupDirections, error) {
	directions := &groupDirections{byName: map[string]GroupDirections{}, gatePath: true}

	for _, clause := range []struct {
		template string
		body     string
		kind     xast.Condition
		pol      polarity
	}{
		{template: whenTemplate, body: condition, kind: xast.ConditionWhen, pol: positive},
		{template: unlessTemplate, body: unless, kind: xast.ConditionUnless, pol: negative},
	} {
		body, err := clauseBody(clause.template, clause.body, clause.kind)
		if err != nil {
			return nil, err
		}

		if err := directions.addClause(body.AsIsNode(), clause.pol); err != nil {
			return nil, err
		}
	}

	return directions.byName, nil
}

// polarity is the polarity of a node of a clause: a when clause starts
// positive and an unless clause negative, and each ! flips it. In a positive
// node a remove of a member can lift the forbid, and in a negative node an
// add can.
type polarity int

const (
	positive polarity = iota
	negative
)

// flip returns the other polarity.
func (p polarity) flip() polarity {
	if p == positive {
		return negative
	}

	return positive
}

// groupDirections collects the changes that can lift the forbid of one
// policy for each group name that the policy names.
type groupDirections struct {
	byName map[string]GroupDirections
	// gatePath reports whether the directions are for a gate. The gate
	// schema has no attribute of a group type, so a membership test of
	// principal whose right side holds no group literal cannot name a group.
	gatePath bool
}

// addClause walks one clause with its polarity and adds its groups.
func (d *groupDirections) addClause(body xast.IsNode, p polarity) error {
	// Defense in depth: walk refuses the membership tests that it visits.
	// This pass stays so that a membership test inside a node type that the
	// walk does not open is refused too.
	if err := d.checkMemberships(body); err != nil {
		return err
	}

	return d.walk(body, p)
}

// addPrincipalScope adds the groups of the principal scope, which is a
// membership test of principal: a remove of a member can lift the forbid.
// It returns ErrLimitMembership for a right side that is not a group and
// not a set of groups: the server cannot tell which group such a scope
// names.
func (d *groupDirections) addPrincipalScope(n any) error {
	switch s := n.(type) {
	case xast.ScopeTypeEq:
		d.addBoth(s.Entity)
	case xast.ScopeTypeIn:
		return d.addMembershipEntity(s.Entity, positive)
	case xast.ScopeTypeInSet:
		for _, e := range s.Entities {
			if err := d.addMembershipEntity(e, positive); err != nil {
				return err
			}
		}
	case xast.ScopeTypeIsIn:
		return d.addMembershipEntity(s.Entity, positive)
	default:
	}

	return nil
}

// addOtherScope adds the groups of the scope of the action or the resource.
// A group in such a scope is not a membership test of principal, but a text
// that holds one is refused at save, and both directions are the safe
// answer for a stored row that no save wrote.
func (d *groupDirections) addOtherScope(n any) {
	switch s := n.(type) {
	case xast.ScopeTypeEq:
		d.addBoth(s.Entity)
	case xast.ScopeTypeIn:
		d.addBoth(s.Entity)
	case xast.ScopeTypeInSet:
		for _, e := range s.Entities {
			d.addBoth(e)
		}
	case xast.ScopeTypeIsIn:
		d.addBoth(s.Entity)
	default:
	}
}

// walk visits the nodes of one clause with its polarity. A ! flips the
// polarity, and && and || keep it. It returns ErrLimitMembership for a
// membership test of principal whose right side is not a group and not a
// set of groups.
func (d *groupDirections) walk(n xast.IsNode, p polarity) error {
	switch v := n.(type) {
	case xast.NodeTypeNot:
		return d.walk(v.Arg, p.flip())
	case xast.NodeTypeAnd:
		if err := d.walk(v.Left, p); err != nil {
			return err
		}

		return d.walk(v.Right, p)
	case xast.NodeTypeOr:
		if err := d.walk(v.Left, p); err != nil {
			return err
		}

		return d.walk(v.Right, p)
	case xast.NodeTypeIn:
		// A membership rule is principal in <group or set of groups>. A
		// group in any other node of an in, and in any node type that the
		// walk does not know, counts for both changes.
		if isPrincipal(v.Left) {
			return d.addMembership(v.Right, p)
		}

		d.addBothSubtree(n)
	case xast.NodeTypeIsIn:
		// The is operator with an in: principal is <type> in <group>.
		if isPrincipal(v.Left) {
			return d.addMembership(v.Entity, p)
		}

		d.addBothSubtree(n)
	default:
		d.addBothSubtree(n)
	}

	return nil
}

// addMembership adds the direction of the right side of a membership rule
// principal in <groups>: in a positive node a remove of a member can lift
// the forbid, and in a negative node an add can. It returns
// ErrLimitMembership for a right side that holds a group literal in a shape
// that is not a group and not a set of groups. In the gate path a right side
// that holds no group literal names no group: see membershipUIDs.
func (d *groupDirections) addMembership(n xast.IsNode, p polarity) error {
	uids, err := d.membershipUIDs(n)
	if err != nil {
		return err
	}

	for _, uid := range uids {
		d.addDirection(uid, p)
	}

	return nil
}

// checkMemberships returns ErrLimitMembership for a membership test of
// principal whose right side holds a group literal in a shape that is not a
// group and not a set of groups, also inside a node type that the direction
// walk does not open. In the gate path a right side that holds no group
// literal names no group: see membershipUIDs.
func (d *groupDirections) checkMemberships(n xast.IsNode) error {
	var membership error

	xast.Inspect(xast.NewNode(n), func(child xast.IsNode) bool {
		if membership != nil {
			return false
		}

		membership = d.checkMembership(child)

		return membership == nil
	})

	return membership
}

// checkMembership returns ErrLimitMembership for a membership test of
// principal in child whose right side holds a group literal in a shape that
// is not a group and not a set of groups. In the gate path a right side that
// holds no group literal names no group: see membershipUIDs.
func (d *groupDirections) checkMembership(child xast.IsNode) error {
	switch v := child.(type) {
	case xast.NodeTypeIn:
		if !isPrincipal(v.Left) {
			return nil
		}

		_, err := d.membershipUIDs(v.Right)

		return err
	case xast.NodeTypeIsIn:
		if !isPrincipal(v.Left) {
			return nil
		}

		_, err := d.membershipUIDs(v.Entity)

		return err
	default:
		return nil
	}
}

// membershipUIDs returns the groups of the right side of a membership test
// of principal. It returns ErrLimitMembership for a value that is not a
// group and not a set of groups. In the gate path, a right side whose subtree
// holds no group literal names no group: it returns no groups and no error,
// because the gate schema has no attribute of a group type, so no change of
// the members of a group can change the answer of that test.
func (d *groupDirections) membershipUIDs(n xast.IsNode) ([]types.EntityUID, error) {
	if set, ok := n.(xast.NodeTypeSet); ok {
		uids := make([]types.EntityUID, 0, len(set.Elements))

		for _, e := range set.Elements {
			uid, ok := groupUID(e)
			if !ok {
				return nil, d.membershipError(n)
			}

			uids = append(uids, uid)
		}

		return uids, nil
	}

	uid, ok := groupUID(n)
	if !ok {
		return nil, d.membershipError(n)
	}

	return []types.EntityUID{uid}, nil
}

// membershipError returns ErrLimitMembership for the right side n of a
// membership test. In the gate path it returns nil when the subtree of n
// holds no group literal: the gate schema has no attribute of a group type,
// so such a right side cannot be a group and names no group.
func (d *groupDirections) membershipError(n xast.IsNode) error {
	if d.gatePath && !holdsGroupLiteral(n) {
		return nil
	}

	return gate.ErrLimitMembership
}

// holdsGroupLiteral reports whether n is a group or a node whose subtree
// holds a group.
func holdsGroupLiteral(n xast.IsNode) bool {
	found := false

	xast.Inspect(xast.NewNode(n), func(child xast.IsNode) bool {
		if _, ok := groupUID(child); ok {
			found = true

			return false
		}

		return true
	})

	return found
}

// addMembershipEntity adds the direction of a membership rule that names
// one entity: the principal scope, and the is operator with an in. It
// returns ErrLimitMembership for an entity that is not a group.
func (d *groupDirections) addMembershipEntity(uid types.EntityUID, p polarity) error {
	if string(uid.Type) != typeGroup {
		return gate.ErrLimitMembership
	}

	d.addDirection(uid, p)

	return nil
}

// addDirection adds the direction that can lift the forbid for uid: a
// remove in a positive node, and an add in a negative one.
func (d *groupDirections) addDirection(uid types.EntityUID, p polarity) {
	if p == positive {
		d.add(uid, GroupDirections{Add: false, Remove: true})

		return
	}

	d.add(uid, GroupDirections{Add: true, Remove: false})
}

// addBothSubtree adds both directions for each group of the subtree of n.
// Defense in depth: groupUID and add both refuse a value that is not a
// group, so this check cannot change the answer. It stays so that a new
// caller of addBoth cannot count a value that is not a group.
func (d *groupDirections) addBothSubtree(n xast.IsNode) {
	xast.Inspect(xast.NewNode(n), func(child xast.IsNode) bool {
		if uid, ok := groupUID(child); ok {
			d.addBoth(uid)
		}

		return true
	})
}

// addBoth adds uid with both directions.
func (d *groupDirections) addBoth(uid types.EntityUID) {
	d.add(uid, GroupDirections{Add: true, Remove: true})
}

// add joins directions to the entry of the group uid. It ignores a value
// that is not a group.
func (d *groupDirections) add(uid types.EntityUID, directions GroupDirections) {
	if string(uid.Type) != typeGroup {
		return
	}

	current := d.byName[string(uid.ID)]
	current.Add = current.Add || directions.Add
	current.Remove = current.Remove || directions.Remove
	d.byName[string(uid.ID)] = current
}

// isPrincipal reports whether n is the variable principal.
func isPrincipal(n xast.IsNode) bool {
	v, ok := n.(xast.NodeTypeVariable)

	return ok && v.Name == "principal"
}

// groupUID returns the group entity of a value node, and false for any
// other node.
func groupUID(n xast.IsNode) (types.EntityUID, bool) {
	v, ok := n.(xast.NodeValue)
	if !ok {
		var none types.EntityUID

		return none, false
	}

	uid, ok := v.Value.(types.EntityUID)
	if !ok || string(uid.Type) != typeGroup {
		var none types.EntityUID

		return none, false
	}

	return uid, true
}
