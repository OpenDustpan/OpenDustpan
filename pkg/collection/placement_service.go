package collection

import (
	"context"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// RuleInput is the content of a placement rule that a caller sets. Position is
// the place in the list from 1. Zero means the end of the list for a new rule
// and the current place for a changed rule.
type RuleInput struct {
	Tool       string
	Project    Pattern
	Stack      Pattern
	Collection Name
	Position   int
}

// PlacementConfig holds the dependencies of a PlacementService.
type PlacementConfig struct {
	Store      PlacementStore
	Namespaces namespace.Store
	Now        func() time.Time
}

// PlacementService implements the placement rule use cases. It reads the fence
// of the namespace before each write, so a write carries the failover version
// that the namespace has now. It also keeps the list free of hidden rules.
type PlacementService struct {
	store      PlacementStore
	namespaces namespace.Store
	now        func() time.Time
}

// NewPlacementService returns a PlacementService. It returns
// ErrMissingDependency when a port is missing. A nil Now uses time.Now.
func NewPlacementService(cfg PlacementConfig) (*PlacementService, error) {
	if cfg.Store == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: placement store and namespace store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &PlacementService{store: cfg.Store, namespaces: cfg.Namespaces, now: now}, nil
}

// Create makes a rule in ns with a new stable ID and appends by. The creator
// is the subject of by. It returns ErrPlacementConflict when an earlier rule
// hides the new rule, or when the new rule hides a later one.
func (s *PlacementService) Create(
	ctx context.Context, ns namespace.Name, in RuleInput, by audit.Entry,
) (Rule, error) {
	if err := checkRuleInput(in); err != nil {
		return Rule{}, err
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return Rule{}, err
	}

	rules, err := s.store.ListPlacementRules(ctx, ns)
	if err != nil {
		return Rule{}, fmt.Errorf("list placement rules of %s: %w", ns, err)
	}

	at := s.now().UTC()
	r := Rule{
		CreatedAt: at, ID: ident.NewID("plr-"), Namespace: ns, Tool: in.Tool, Project: in.Project,
		Stack: in.Stack, Collection: in.Collection, CreatedBy: by.Subject, Position: in.Position,
	}

	if r.Position == 0 {
		r.Position = len(rules) + 1
	}

	if r.Position < 1 || r.Position > len(rules)+1 {
		return Rule{}, fmt.Errorf("%w: %d is not from 1 to %d", ErrInvalidPosition, r.Position, len(rules)+1)
	}

	if err := checkConflicts(slices.Insert(slices.Clone(rules), r.Position-1, r), r.ID); err != nil {
		return Rule{}, err
	}

	by.At = at
	by.ObjectID = r.ID

	by.Version = r.Text() + "; position " + fmt.Sprint(r.Position)
	if err := s.store.CreatePlacementRule(ctx, r, fence, by); err != nil {
		return Rule{}, fmt.Errorf("create placement rule in %s: %w", ns, err)
	}

	return r, nil
}

// Update replaces the content of the rule id and appends by. It returns
// ErrPlacementConflict under the same conditions as Create.
func (s *PlacementService) Update(
	ctx context.Context, ns namespace.Name, id string, in RuleInput, by audit.Entry,
) (Rule, error) {
	if err := checkRuleInput(in); err != nil {
		return Rule{}, err
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return Rule{}, err
	}

	rules, err := s.store.ListPlacementRules(ctx, ns)
	if err != nil {
		return Rule{}, fmt.Errorf("list placement rules of %s: %w", ns, err)
	}

	idx := slices.IndexFunc(rules, func(r Rule) bool { return r.ID == id })
	if idx < 0 {
		return Rule{}, fmt.Errorf("placement rule %s: %w", id, ErrPlacementRuleNotFound)
	}

	r := rules[idx]
	r.Tool, r.Project, r.Stack, r.Collection = in.Tool, in.Project, in.Stack, in.Collection

	r.Position = in.Position
	if r.Position == 0 {
		r.Position = idx + 1
	}

	if r.Position < 1 || r.Position > len(rules) {
		return Rule{}, fmt.Errorf("%w: %d is not from 1 to %d", ErrInvalidPosition, r.Position, len(rules))
	}

	rest := slices.Delete(slices.Clone(rules), idx, idx+1)
	if err := checkConflicts(slices.Insert(rest, r.Position-1, r), r.ID); err != nil {
		return Rule{}, err
	}

	by.At = s.now().UTC()
	by.ObjectID = r.ID

	by.Version = r.Text() + "; position " + fmt.Sprint(r.Position)
	if err := s.store.UpdatePlacementRule(ctx, r, fence, by); err != nil {
		return Rule{}, fmt.Errorf("update placement rule %s: %w", id, err)
	}

	return r, nil
}

// Delete removes the rule id and appends by.
func (s *PlacementService) Delete(ctx context.Context, ns namespace.Name, id string, by audit.Entry) error {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return err
	}

	by.At = s.now().UTC()
	by.ObjectID = id

	if err := s.store.DeletePlacementRule(ctx, ns, id, fence, by); err != nil {
		return fmt.Errorf("delete placement rule %s: %w", id, err)
	}

	return nil
}

// Get returns the rule id of ns, or ErrPlacementRuleNotFound.
func (s *PlacementService) Get(ctx context.Context, ns namespace.Name, id string) (Rule, error) {
	return s.store.GetPlacementRule(ctx, ns, id)
}

// List returns the rules of ns in list order.
func (s *PlacementService) List(ctx context.Context, ns namespace.Name) ([]Rule, error) {
	return s.store.ListPlacementRules(ctx, ns)
}

// Place returns the first rule of ns that matches a new unit of tool with the
// given project and stack name. It does not write. It returns a
// *PlacementError, which matches ErrNoPlacement, when no rule matches, and
// does not make a collection. The caller that makes the unit must still check
// that the caller of the CLI may make a unit in the collection of the rule.
func (s *PlacementService) Place(
	ctx context.Context, ns namespace.Name, tool, project, stack string,
) (Rule, error) {
	rules, err := s.store.ListPlacementRules(ctx, ns)
	if err != nil {
		return Rule{}, fmt.Errorf("list placement rules of %s: %w", ns, err)
	}

	r, ok := FirstMatch(rules, tool, project, stack)
	if !ok {
		return Rule{}, &PlacementError{Namespace: ns, Tool: tool, Project: project, Stack: stack}
	}

	return r, nil
}

// checkRuleInput rejects a rule with a tool that has no placement rules, with
// an invalid pattern or collection name, or with a negative position. Defense
// in depth: the HTTP handler parses the same fields, and the store validates
// the rule again; this check stays so that a new caller of the service gets a
// clear error before any read.
func checkRuleInput(in RuleInput) error {
	r := Rule{Tool: in.Tool, Project: in.Project, Stack: in.Stack, Collection: in.Collection, Position: max(in.Position, 1)}
	if in.Position < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidPosition, in.Position)
	}

	return r.Validate()
}

// checkConflicts checks the list rules, in list order, for the rule with ID
// changed: no earlier rule may hide it, and it may not hide a later rule.
func checkConflicts(rules []Rule, changed string) error {
	for i, r := range rules {
		if r.ID != changed {
			continue
		}

		for _, earlier := range rules[:i] {
			if earlier.Hides(r) {
				return fmt.Errorf("%w: rule %q already matches every unit of %q, "+
					"so this rule never matches: put it before that rule or narrow its patterns",
					ErrPlacementConflict, earlier.Text(), r.Text())
			}
		}

		for _, later := range rules[i+1:] {
			if r.Hides(later) {
				return fmt.Errorf("%w: rule %q would match every unit of %q, "+
					"so that rule never matches: put it after that rule or narrow its patterns",
					ErrPlacementConflict, r.Text(), later.Text())
			}
		}

		return nil
	}

	return nil
}

func (s *PlacementService) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}
