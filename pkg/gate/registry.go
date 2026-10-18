package gate

import (
	"fmt"
	"slices"
	"sync"
)

// ApprovalRule is data only: who may approve, how many, and whether the
// author may count.
type ApprovalRule struct {
	Name   string   `json:"name"`
	Groups []string `json:"groups,omitempty"`
	Users  []string `json:"users,omitempty"`
	Count  int      `json:"count"`
	// AuthorMayApprove defaults to false.
	AuthorMayApprove bool `json:"authorMayApprove,omitempty"`
	// FreshAuth asks for a fresh authentication at each approval.
	FreshAuth bool `json:"freshAuth,omitempty"`
}

// Registry keeps gates, limits and approval rules in memory. It stands in
// for a store in the spike.
type Registry struct {
	mu     sync.RWMutex
	gates  map[string]Gate
	limits map[string]Limit
	rules  map[string]ApprovalRule
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{gates: map[string]Gate{}, limits: map[string]Limit{}, rules: map[string]ApprovalRule{}}
}

// PutGate validates and stores g. A gate of the same ID in a final gate of
// another scope is refused with ErrFinal.
func (r *Registry) PutGate(g Gate) error {
	err := g.Validate()
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if old, ok := r.gates[g.ID]; ok && old.Final && old.Scope != g.Scope {
		return fmt.Errorf("%w: %s is final at %s", ErrFinal, g.ID, old.Scope)
	}

	r.gates[g.ID] = g

	return nil
}

// PutLimit validates and stores l.
func (r *Registry) PutLimit(l Limit) error {
	err := l.Validate()
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.limits[l.ID] = l

	return nil
}

// PutRule validates and stores a rule.
func (r *Registry) PutRule(rule ApprovalRule) error {
	if rule.Name == "" || rule.Count < 1 || (len(rule.Groups) == 0 && len(rule.Users) == 0) {
		return fmt.Errorf("%w: approval rule %q needs a name, a count and an approver", ErrInvalid, rule.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.rules[rule.Name] = rule

	return nil
}

// Rule returns the approval rule of name.
func (r *Registry) Rule(name string) (ApprovalRule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rule, ok := r.rules[name]

	return rule, ok
}

func inChain(chain []Scope, s Scope) bool { return slices.Contains(chain, s) }

// Gates returns the gates of the scope chain of sub for point, sorted by ID.
func (r *Registry) Gates(sub Subject, point DecisionPoint) []Gate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain := sub.Chain()

	var out []Gate

	for _, g := range r.gates {
		if g.DecisionPoint == point && inChain(chain, g.Scope) {
			out = append(out, g)
		}
	}

	slices.SortFunc(out, func(a, b Gate) int { return compareStrings(a.ID, b.ID) })

	return out
}

// Limits returns the limits of the scope chain of sub, sorted by ID.
func (r *Registry) Limits(sub Subject) []Limit {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain := sub.Chain()

	var out []Limit

	for _, l := range r.limits {
		if inChain(chain, l.Scope) {
			out = append(out, l)
		}
	}

	slices.SortFunc(out, func(a, b Limit) int { return compareStrings(a.ID, b.ID) })

	return out
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
