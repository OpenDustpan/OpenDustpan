// Package cedargo is the linked Cedar adapter of the policy engine port
// gate.Engine. It runs the cedar-go library in the process of the server.
//
// A gate is one Cedar permit policy that the engine builds from the parsed
// condition and unless of the gate, with the action pinned to its decision
// point. An access limit is one forbid policy that the engine parses from
// its text, and evaluates with a fixed policy that permits each request.
// The engine type-checks each policy
// against the Cedar schema of its facts with the validator of cedar-go.
// That validator is in the package x/exp of cedar-go, which has no
// compatibility promise, so the engine keeps it behind this package.
//
// The engine gives each error as a diagnostic and never as a match. The
// caller decides what a diagnostic does, with the onError field of the
// gate.
package cedargo

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/ast"

	"opendustpan.org/opendustpan/pkg/gate"
)

// cedarGoVersion is the version of the cedar-go module that go.mod pins.
// A test compares it with the build information.
const cedarGoVersion = "1.8.0"

// maxPolicies bounds the compiled policies that the engine keeps.
const maxPolicies = 8192

// fixedPermitID is the Cedar ID of the fixed permit of an access limit set.
// It is not the ID of a policy of the caller, because each such ID holds a
// scope key and a slash.
const fixedPermitID = "fixed-permit"

// Option configures an Engine.
type Option func(*Engine)

// WithAccessActions names the actions that an access limit can name. The
// access schema holds one Cedar action for each name, and an access limit
// that names another action is refused. Without this option the engine
// cannot use the access schema.
func WithAccessActions(actions ...string) Option {
	return func(e *Engine) { e.accessActions = slices.Clone(actions) }
}

// Engine is a gate.Engine that links cedar-go. It is safe for concurrent
// use.
type Engine struct {
	accessActions []string
	schemas       *schemaCache
	fixedPermit   *cedar.Policy

	mu       sync.Mutex
	compiled map[[sha256.Size]byte]result
}

// result is the outcome of the build of one policy. It is a value of the
// cache, so a refused policy is not built again.
type result struct {
	policy *compiled
	err    error
}

var _ gate.Engine = (*Engine)(nil)

// New returns an Engine.
func New(opts ...Option) *Engine {
	e := &Engine{
		accessActions: nil,
		schemas:       newSchemaCache(),
		fixedPermit:   cedar.NewPolicyFromAST(ast.Permit()),
		mu:            sync.Mutex{},
		compiled:      map[[sha256.Size]byte]result{},
	}

	for _, o := range opts {
		o(e)
	}

	return e
}

// Version returns "cedar-go/" and the version of the library.
func (e *Engine) Version() string {
	return "cedar-go/" + cedarGoVersion
}

// Check implements gate.Engine.
func (e *Engine) Check(ctx context.Context, schema gate.Schema, policies []gate.Policy) ([]gate.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("check policies: %w", err)
	}

	ts, err := e.schemas.get(schema, e.accessActions)
	if err != nil {
		return nil, err
	}

	var diags []gate.Diagnostic

	for i := range policies {
		if _, err := e.compile(ts, schema, &policies[i]); err != nil {
			diags = append(diags, diagnostic(policies[i].ID, err))
		}
	}

	return diags, nil
}

// CheckLimitMembership implements gate.Engine.
func (e *Engine) CheckLimitMembership(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("check the membership of an access limit: %w", err)
	}

	if _, err := LimitGroupDirections(text); errors.Is(err, gate.ErrLimitMembership) {
		return err
	}

	return nil
}

// Evaluate implements gate.Engine.
func (e *Engine) Evaluate(ctx context.Context, set gate.PolicySet, requests []gate.Request) ([]gate.Answer, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("evaluate policies: %w", err)
	}

	for i := range requests {
		if err := requests[i].Validate(); err != nil {
			return nil, err
		}
	}

	ts, err := e.schemas.get(set.Schema, e.accessActions)
	if err != nil {
		return nil, err
	}

	for i := range requests {
		if (requests[i].Access != nil) != ts.access {
			return nil, fmt.Errorf(
				"%w: request %q does not hold the context of the schema %s",
				gate.ErrInvalid, requests[i].ID, set.Schema.Version,
			)
		}
	}

	cs, err := e.compileSet(ts, set)
	if err != nil {
		return nil, err
	}

	answers := make([]gate.Answer, 0, len(requests))

	for i := range requests {
		if err = ctx.Err(); err != nil {
			return nil, fmt.Errorf("evaluate policies: %w", err)
		}

		answer, err := cs.answer(&requests[i])
		if err != nil {
			return nil, err
		}

		answers = append(answers, answer)
	}

	return answers, nil
}

// compiledSet is the Cedar policy set of a gate.PolicySet.
type compiledSet struct {
	policies *cedar.PolicySet
	byID     map[cedar.PolicyID]gate.PolicyID
	// refused holds the policies that the engine refuses. They are a
	// diagnostic of each answer.
	refused []gate.Diagnostic
}

// answer evaluates r against the set.
func (cs *compiledSet) answer(r *gate.Request) (gate.Answer, error) {
	creq, entities, err := toCedar(r)
	if err != nil {
		return gate.Answer{}, err
	}

	_, diag := cedar.Authorize(cs.policies, entities, creq)

	answer := gate.Answer{RequestID: r.ID, Matched: nil, Diagnostics: slices.Clone(cs.refused)}

	for _, reason := range diag.Reasons {
		if id, ok := cs.byID[reason.PolicyID]; ok {
			answer.Matched = append(answer.Matched, id)
		}
	}

	for _, de := range diag.Errors {
		answer.Diagnostics = append(
			answer.Diagnostics,
			gate.Diagnostic{Policy: cs.byID[de.PolicyID], Message: de.Message},
		)
	}

	sortAnswer(&answer)

	return answer, nil
}

// compileSet builds the Cedar policy set of set. A policy that the engine
// refuses is not in it, and is a diagnostic of each answer. It returns
// gate.ErrInvalid for two policies with one ID.
func (e *Engine) compileSet(ts *typedSchema, set gate.PolicySet) (*compiledSet, error) {
	cs := &compiledSet{
		policies: cedar.NewPolicySet(),
		byID:     make(map[cedar.PolicyID]gate.PolicyID, len(set.Policies)),
		refused:  nil,
	}

	hasLimit := false

	for i := range set.Policies {
		p := &set.Policies[i]
		id := cedar.PolicyID(p.ID.String())

		if _, dup := cs.byID[id]; dup {
			return nil, fmt.Errorf("%w: the policy set holds the policy %s twice", gate.ErrInvalid, p.ID)
		}

		cs.byID[id] = p.ID
		hasLimit = hasLimit || p.Kind == gate.PolicyLimit

		c, err := e.compile(ts, set.Schema, p)
		if err != nil {
			cs.refused = append(cs.refused, diagnostic(p.ID, err))

			continue
		}

		cs.policies.Add(id, c.policy)
	}

	if hasLimit {
		cs.policies.Add(fixedPermitID, e.fixedPermit)
	}

	return cs, nil
}

// compile returns the built policy of p, from the cache when the engine
// built it for the same schema and the same text.
func (e *Engine) compile(ts *typedSchema, schema gate.Schema, p *gate.Policy) (*compiled, error) {
	var in strings.Builder

	for _, part := range []string{
		schema.Version.String(), joinIDs(schema.PathSets), p.ID.String(), string(p.Kind), string(p.Point),
		p.Condition, p.Unless, p.Text,
	} {
		in.WriteString(strconv.Itoa(len(part)) + ":" + part + ";")
	}

	key := sha256.Sum256([]byte(in.String()))

	e.mu.Lock()
	cached, ok := e.compiled[key]
	e.mu.Unlock()

	if ok {
		return cached.policy, cached.err
	}

	c, err := build(ts, p)

	e.mu.Lock()

	if len(e.compiled) >= maxPolicies {
		clear(e.compiled)
	}

	e.compiled[key] = result{policy: c, err: err}

	e.mu.Unlock()

	return c, err
}

// diagnostic returns the diagnostic of a refused policy.
func diagnostic(id gate.PolicyID, err error) gate.Diagnostic {
	return gate.Diagnostic{Policy: id, Message: strings.TrimPrefix(err.Error(), errPolicy.Error()+": ")}
}

// sortAnswer puts the matches and the diagnostics of a in the order of the
// policy IDs, so the same set gives the same answer in any order.
func sortAnswer(a *gate.Answer) {
	slices.SortFunc(a.Matched, func(x, y gate.PolicyID) int { return strings.Compare(x.String(), y.String()) })
	slices.SortStableFunc(a.Diagnostics, func(x, y gate.Diagnostic) int {
		return strings.Compare(x.Policy.String(), y.Policy.String())
	})
}
