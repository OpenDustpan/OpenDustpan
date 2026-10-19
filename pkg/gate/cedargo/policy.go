package cedargo

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/ast"
	"github.com/cedar-policy/cedar-go/types"
	xast "github.com/cedar-policy/cedar-go/x/exp/ast"

	"opendustpan.org/opendustpan/pkg/gate"
)

// clauseTemplates parse one clause of a gate. The text of a gate is only
// a parse input: the engine builds the policy from the parsed expressions.
const (
	whenTemplate   = "permit (principal, action, resource) when {\n%s\n};"
	unlessTemplate = "permit (principal, action, resource) unless {\n%s\n};"
)

// errPolicy is the cause of a refused policy. Check and Evaluate give its
// text as the message of a diagnostic.
var errPolicy = errors.New("policy refused")

// compiled is one policy that passed each check of one schema.
type compiled struct {
	policy *cedar.Policy
}

// build returns the Cedar policy of p, checked against ts. It returns an
// error that wraps errPolicy for a policy that the engine refuses.
func build(ts *typedSchema, p *gate.Policy) (*compiled, error) {
	var (
		xp  *xast.Policy
		err error
	)

	switch p.Kind {
	case gate.PolicyGate:
		xp, err = buildGate(ts, p)
	case gate.PolicyLimit:
		xp, err = buildLimit(ts, p)
	default:
		err = fmt.Errorf("%w: the policy kind %q is not known", errPolicy, p.Kind)
	}

	if err != nil {
		return nil, err
	}

	if xp == nil {
		return nil, fmt.Errorf("%w: the policy has no parsed form", errPolicy)
	}

	if err := checkReads(xp); err != nil {
		return nil, err
	}

	if err := ts.validator.Policy(p.ID.String(), xp); err != nil {
		return nil, fmt.Errorf("%w: %w", errPolicy, err)
	}

	return &compiled{policy: cedar.NewPolicyFromAST(toRoot(xp))}, nil
}

// buildGate builds the permit policy of a gate with the action pinned to
// its decision point.
func buildGate(ts *typedSchema, p *gate.Policy) (*xast.Policy, error) {
	if ts.access {
		return nil, fmt.Errorf("%w: a gate is not part of the access schema", errPolicy)
	}

	if p.Point != ts.point {
		return nil, fmt.Errorf("%w: the gate is at %q and the schema is of %q", errPolicy, p.Point, ts.point)
	}

	if p.Text != "" {
		return nil, fmt.Errorf("%w: a gate has a condition and an unless, never a text", errPolicy)
	}

	when, err := clauseBody(whenTemplate, p.Condition, xast.ConditionWhen)
	if err != nil {
		return nil, err
	}

	xp := xast.Permit().
		ActionEq(types.NewEntityUID(typeAction, types.String(p.Point))).
		When(when)

	if strings.TrimSpace(p.Unless) != "" {
		unless, err := clauseBody(unlessTemplate, p.Unless, xast.ConditionUnless)
		if err != nil {
			return nil, err
		}

		xp.Unless(unless)
	}

	if err := checkNoAction(xp); err != nil {
		return nil, err
	}

	return xp, nil
}

// clauseBody parses expr as one Cedar expression and returns it. An empty
// expr is true. It refuses an expr that closes its clause: a second policy,
// or a second clause, is no single expression.
func clauseBody(template, expr string, kind xast.Condition) (xast.Node, error) {
	if strings.TrimSpace(expr) == "" {
		expr = "true"
	}

	policies, err := parseAll(fmt.Sprintf(template, expr))
	if err != nil {
		return xast.Node{}, fmt.Errorf("%w: %s", errPolicy, userPositions(err))
	}

	if len(policies) != 1 {
		return xast.Node{}, fmt.Errorf(
			"%w: the text holds %d policies, and a gate holds one", errPolicy, len(policies),
		)
	}

	conds := policies[0].Conditions
	if len(conds) != 1 || conds[0].Condition != kind {
		return xast.Node{}, fmt.Errorf("%w: the text is not one Cedar expression", errPolicy)
	}

	return xast.NewNode(conds[0].Body), nil
}

// positionPattern matches the position that the Cedar parser gives in an
// error: "<input>:line:column".
var positionPattern = regexp.MustCompile(`<input>:(\d+):(\d+)`)

// userPositions returns the text of a parse error with each position moved
// to the text of the author. The template puts the text of the author on
// the second line, so the line is one less. A position on the first line
// is in the template and stays as it is.
func userPositions(err error) string {
	msg := strings.TrimPrefix(err.Error(), errPolicy.Error()+": ")

	return positionPattern.ReplaceAllStringFunc(msg, func(m string) string {
		parts := positionPattern.FindStringSubmatch(m)

		line, convErr := strconv.Atoi(parts[1])
		if convErr != nil || line < 2 {
			return m
		}

		return "<input>:" + strconv.Itoa(line-1) + ":" + parts[2]
	})
}

// buildLimit parses the text of an access limit. It refuses a text that
// holds more than one policy, and a policy with the effect permit.
func buildLimit(ts *typedSchema, p *gate.Policy) (*xast.Policy, error) {
	if !ts.access {
		return nil, fmt.Errorf("%w: an access limit is part of the access schema", errPolicy)
	}

	if p.Condition != "" || p.Unless != "" {
		return nil, fmt.Errorf("%w: an access limit has a text, never a condition or an unless", errPolicy)
	}

	policies, err := parseAll(p.Text)
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

	if policies[0].Effect != xast.EffectForbid {
		return nil, fmt.Errorf("%w: an access limit has the effect forbid", errPolicy)
	}

	return policies[0], nil
}

// parseAll parses text as a Cedar policy set and returns the policies in
// the order of the text.
func parseAll(text string) ([]*xast.Policy, error) {
	set, err := cedar.NewPolicySetFromBytes("", []byte(text))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errPolicy, err)
	}

	var out []*xast.Policy

	for _, p := range set.All() {
		out = append(out, (*xast.Policy)(p.AST()))
	}

	return out, nil
}

// checkNoAction refuses a policy that reads the variable action. The
// engine pins the action of a gate, so a condition never names it.
func checkNoAction(p *xast.Policy) error {
	var found bool

	forEachNode(p, func(n xast.IsNode) {
		if v, ok := n.(xast.NodeTypeVariable); ok && v.Name == "action" {
			found = true
		}
	})

	if found {
		return fmt.Errorf("%w: a condition never names action, because the engine pins it", errPolicy)
	}

	return nil
}

// forEachNode calls fn for each node of each condition of p.
func forEachNode(p *xast.Policy, fn func(xast.IsNode)) {
	for _, c := range p.Conditions {
		xast.Inspect(xast.NewNode(c.Body), func(n xast.IsNode) bool {
			fn(n)

			return true
		})
	}
}

// checkReads refuses the has operator, and a read of a field of a group
// with the flag available that does not read the flag. It checks each
// clause on its own: a clause that reads a field of such a group reads the
// flag of the group itself, and the flag in the other clause does not count.
func checkReads(p *xast.Policy) error {
	for _, c := range p.Conditions {
		if err := checkClauseReads(xast.NewNode(c.Body)); err != nil {
			return err
		}
	}

	return nil
}

// checkClauseReads applies the rules of checkReads to one clause.
func checkClauseReads(body xast.Node) error {
	var (
		hasUsed bool
		whole   = map[gate.Group]int{}
		fields  = map[gate.Group][]string{}
	)

	xast.Inspect(body, func(n xast.IsNode) bool {
		if _, ok := n.(xast.NodeTypeHas); ok {
			hasUsed = true

			return true
		}

		acc, ok := n.(xast.NodeTypeAccess)
		if !ok {
			return true
		}

		if g, ok := contextGroup(acc); ok {
			whole[g]++
		} else if g, ok := contextGroup(acc.Arg); ok {
			fields[g] = append(fields[g], string(acc.Value))
		}

		return true
	})

	if hasUsed {
		return fmt.Errorf(
			"%w: has on an attribute of the schema is refused; read the flag available of its group",
			errPolicy,
		)
	}

	for g, n := range whole {
		if !g.HasAvailable() {
			continue
		}

		reads := fields[g]
		flagged := slices.Contains(reads, "available")
		other := slices.DeleteFunc(slices.Clone(reads), func(f string) bool { return f == "available" })

		if (len(other) > 0 || n > len(reads)) && !flagged {
			return fmt.Errorf(
				"%w: each clause that reads context.%s must read context.%s.available",
				errPolicy, g, g,
			)
		}
	}

	return nil
}

// contextGroup reports the group g when n is context.g.
func contextGroup(n xast.IsNode) (gate.Group, bool) {
	acc, ok := n.(xast.NodeTypeAccess)
	if !ok {
		return "", false
	}

	v, ok := acc.Arg.(xast.NodeTypeVariable)
	if !ok || v.Name != "context" {
		return "", false
	}

	return gate.Group(acc.Value), true
}

// toRoot converts a policy of the builder to the policy type of the root
// package of cedar-go.
func toRoot(p *xast.Policy) *ast.Policy {
	return (*ast.Policy)(p)
}
