package cedargo

import (
	"fmt"

	"github.com/cedar-policy/cedar-go/types"
	xast "github.com/cedar-policy/cedar-go/x/exp/ast"
)

// GroupDirections reports the changes of the members of a group that can
// lift the forbid of one access limit.
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

	directions := &groupDirections{byName: map[string]GroupDirections{}}

	// The scope of a policy is not a condition, so the walk below never
	// sees it: read the scope of each of the three variables too.
	directions.addScope(p.Principal, principalScope)
	directions.addScope(p.Action, actionOrResourceScope)
	directions.addScope(p.Resource, actionOrResourceScope)

	for _, c := range p.Conditions {
		pol := positive
		if c.Condition == xast.ConditionUnless {
			pol = negative
		}

		directions.walk(c.Body, pol)
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

// scopeKind tells the variable of a policy scope. The principal scope makes
// a remove able to lift the forbid, and the scope of the action or the
// resource makes both changes able to.
type scopeKind int

const (
	principalScope scopeKind = iota
	actionOrResourceScope
)

// groupDirections collects the changes that can lift the forbid of one
// policy for each group name that the policy names.
type groupDirections struct {
	byName map[string]GroupDirections
}

// addScope adds the groups of a scope node. The principal scope makes a
// remove able to lift the forbid. A group in the scope of the action or the
// resource is not a membership rule, but a text that holds one is refused
// at save, and both directions are the safe answer for a stored row that no
// save wrote.
func (d *groupDirections) addScope(n any, kind scopeKind) {
	switch s := n.(type) {
	case xast.ScopeTypeEq:
		d.addBoth(s.Entity)
	case xast.ScopeTypeIn:
		d.addScopeEntity(s.Entity, kind)
	case xast.ScopeTypeInSet:
		for _, e := range s.Entities {
			d.addScopeEntity(e, kind)
		}
	case xast.ScopeTypeIsIn:
		d.addScopeEntity(s.Entity, kind)
	default:
	}
}

// addScopeEntity adds the group of a scope entity: the principal scope
// makes a remove able to lift the forbid, and every other scope both
// changes.
func (d *groupDirections) addScopeEntity(uid types.EntityUID, kind scopeKind) {
	if kind == principalScope {
		d.add(uid, GroupDirections{Add: false, Remove: true})

		return
	}

	d.addBoth(uid)
}

// walk visits the nodes of one clause with its polarity. A ! flips the
// polarity, and && and || keep it.
func (d *groupDirections) walk(n xast.IsNode, p polarity) {
	switch v := n.(type) {
	case xast.NodeTypeNot:
		d.walk(v.Arg, p.flip())
	case xast.NodeTypeAnd:
		d.walk(v.Left, p)
		d.walk(v.Right, p)
	case xast.NodeTypeOr:
		d.walk(v.Left, p)
		d.walk(v.Right, p)
	case xast.NodeTypeIn:
		// A membership rule is principal in <group or set of groups>. A
		// group in any other node of an in, and in any node type that the
		// walk does not know, counts for both changes.
		if isPrincipal(v.Left) && d.addMembership(v.Right, p) {
			return
		}

		d.addBothSubtree(n)
	default:
		d.addBothSubtree(n)
	}
}

// addMembership adds the direction of the right side of a membership rule
// principal in <groups>: in a positive node a remove of a member can lift
// the forbid, and in a negative node an add can. It reports false when the
// right side is not a group and not a set of groups.
func (d *groupDirections) addMembership(n xast.IsNode, p polarity) bool {
	if set, ok := n.(xast.NodeTypeSet); ok {
		uids := make([]types.EntityUID, 0, len(set.Elements))

		for _, e := range set.Elements {
			uid, ok := groupUID(e)
			if !ok {
				return false
			}

			uids = append(uids, uid)
		}

		for _, uid := range uids {
			d.addDirection(uid, p)
		}

		return true
	}

	uid, ok := groupUID(n)
	if !ok {
		return false
	}

	d.addDirection(uid, p)

	return true
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
