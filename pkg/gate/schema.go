package gate

import (
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/x/exp/ast"
	"github.com/cedar-policy/cedar-go/x/exp/schema"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"
)

// FactSchema is the Cedar schema of the fact document v0 and of the
// entities of the spike. It uses the experimental schema package of
// cedar-go, which has no compatibility promise.
const FactSchema = `
entity Group;
entity User in [Group];
entity Unit { tool: String, namespace: String, collection: String };

action "change.planned" appliesTo {
	principal: User,
	resource: Unit,
	context: {
		operation: String,
		unit: { collection: String, labels: Set<String> },
		plan: { create: Long, update: Long, delete: Long, replace: Long },
		cost: { deltaMonthly: Long, available: Bool },
		change: { paths: Set<String>, inputKeys: Set<String> }
	}
};

action "unit:run-destroy" appliesTo { principal: User, resource: Unit, context: {} };
`

// Validator type-checks gate conditions and limits against FactSchema.
type Validator struct{ v *validate.Validator }

// NewValidator parses and resolves FactSchema.
func NewValidator() (*Validator, error) {
	var s schema.Schema

	err := s.UnmarshalCedar([]byte(FactSchema))
	if err != nil {
		return nil, fmt.Errorf("parse fact schema: %w", err)
	}

	res, err := s.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve fact schema: %w", err)
	}

	return &Validator{v: validate.New(res)}, nil
}

// CheckGate type-checks the condition of g. It returns ErrInvalid with the
// type error.
func (v *Validator) CheckGate(g Gate) error {
	return v.check(g.ID, g.policyText(), cedar.Permit)
}

// CheckLimit type-checks l.
func (v *Validator) CheckLimit(l Limit) error {
	return v.check(l.ID, l.Policy, cedar.Forbid)
}

func (v *Validator) check(id, text string, effect cedar.Effect) error {
	p, err := parseOne(text, effect)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, id, err)
	}

	err = v.v.Policy(id, (*ast.Policy)(p.AST()))
	if err != nil {
		return fmt.Errorf("%w: %s: type check: %w", ErrInvalid, id, err)
	}

	return nil
}
