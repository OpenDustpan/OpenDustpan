package gate

import (
	"fmt"
	"slices"
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
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
	// FreshAuthSeconds is the window of the fresh authentication of an
	// approver. Nil asks for none. 0 asks for a new authentication at each
	// approval.
	FreshAuthSeconds *int64 `json:"freshAuthSeconds,omitempty"`
}

const (
	defaultPrefix = "default/"
	ownerPrefix   = "owner/"
)

// ApprovalDefault sets the approvals that a change needs at a scope. A
// namespace, a collection and a unit each can set one; the strictest of the
// chain wins (the highest count, the smallest fresh window). With none set,
// the default is off: no approval is needed. Unless carves out exemptions
// with a Cedar condition over the facts.
type ApprovalDefault struct {
	ID               string `json:"id"`
	Scope            Scope  `json:"scope"`
	Count            int    `json:"count"`
	FreshAuthSeconds *int64 `json:"freshAuthSeconds,omitempty"`
	Unless           string `json:"unless,omitempty"`
}

func (d ApprovalDefault) policyText() string {
	return permitText(string(PointChangePlanned), "true", d.Unless)
}

// Validate checks the default. The installation scope has no default.
func (d ApprovalDefault) Validate() error {
	switch {
	case d.ID == "" || d.Count < 1:
		return fmt.Errorf("%w: approval default %q needs an ID and a count of 1 or more", ErrInvalid, d.ID)
	case d.Scope.Level == LevelInstallation:
		return fmt.Errorf("%w: approval default %s: the installation has none", ErrInvalid, d.ID)
	}

	_, err := parseOne(d.policyText(), cedar.Permit)
	if err != nil {
		return fmt.Errorf("%w: approval default %s: %w", ErrInvalid, d.ID, err)
	}

	return nil
}

// OwnerRule is an ownership rule that the server stores. It applies when
// every part that it sets is true: Paths (a glob matches a changed path;
// done in Go, because Cedar has no quantifier) and When (a Cedar condition
// over the facts, for example on changed inputs, plan facts or the
// operation). Approvers and Count say who must approve.
type OwnerRule struct {
	ID               string   `json:"id"`
	Scope            Scope    `json:"scope"`
	Paths            []string `json:"paths,omitempty"`
	When             string   `json:"when,omitempty"`
	Groups           []string `json:"groups,omitempty"`
	Users            []string `json:"users,omitempty"`
	Count            int      `json:"count"`
	FreshAuthSeconds *int64   `json:"freshAuthSeconds,omitempty"`
}

func (o OwnerRule) policyText() string {
	when := o.When
	if when == "" {
		when = "true"
	}

	return permitText(string(PointChangePlanned), when, "")
}

// Validate checks the rule.
func (o OwnerRule) Validate() error {
	if o.ID == "" || o.Count < 1 || (len(o.Groups) == 0 && len(o.Users) == 0) {
		return fmt.Errorf("%w: owner rule %q needs an ID, a count and an approver", ErrInvalid, o.ID)
	}

	_, err := parseOne(o.policyText(), cedar.Permit)
	if err != nil {
		return fmt.Errorf("%w: owner rule %s: %w", ErrInvalid, o.ID, err)
	}

	return nil
}

// Registry keeps gates, limits and approval rules in memory. It stands in
// for a store in the spike.
type Registry struct {
	mu     sync.RWMutex
	gates  map[string]Gate
	limits map[string]Limit
	rules  map[string]ApprovalRule
	defs   map[string]ApprovalDefault
	owners map[string]OwnerRule
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		gates: map[string]Gate{}, limits: map[string]Limit{}, rules: map[string]ApprovalRule{},
		defs: map[string]ApprovalDefault{}, owners: map[string]OwnerRule{},
	}
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

// PutDefault validates and stores d.
func (r *Registry) PutDefault(d ApprovalDefault) error {
	err := d.Validate()
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.defs[d.ID] = d

	return nil
}

// PutOwnerRule validates and stores o.
func (r *Registry) PutOwnerRule(o OwnerRule) error {
	err := o.Validate()
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.owners[o.ID] = o

	return nil
}

// Defaults returns the approval defaults of the scope chain of sub.
func (r *Registry) Defaults(sub Subject) []ApprovalDefault {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain := sub.Chain()

	var out []ApprovalDefault

	for _, d := range r.defs {
		if inChain(chain, d.Scope) {
			out = append(out, d)
		}
	}

	slices.SortFunc(out, func(a, b ApprovalDefault) int { return compareStrings(a.ID, b.ID) })

	return out
}

// OwnerRules returns the owner rules of the scope chain of sub.
func (r *Registry) OwnerRules(sub Subject) []OwnerRule {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain := sub.Chain()

	var out []OwnerRule

	for _, o := range r.owners {
		if inChain(chain, o.Scope) {
			out = append(out, o)
		}
	}

	slices.SortFunc(out, func(a, b OwnerRule) int { return compareStrings(a.ID, b.ID) })

	return out
}
