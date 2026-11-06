package collection

import (
	"context"
	"fmt"
	"strconv"
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
// that the namespace has now. The store keeps the list free of hidden rules, in
// the transaction of each write.
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
// is the subject of by. Position 0 puts the rule at the end. The store decides
// the place and checks the order inside the transaction of the write. It returns
// ErrPlacementConflict when an earlier rule hides the new rule, or when the new
// rule hides a later one.
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

	at := s.now().UTC()
	r := Rule{
		CreatedAt: at, ID: ident.NewID("plr-"), Namespace: ns, Tool: in.Tool, Project: in.Project,
		Stack: in.Stack, Collection: in.Collection, CreatedBy: by.Subject, Position: in.Position,
	}

	by.At = at
	by.ObjectID = r.ID
	by.Version = auditText(&r)

	if err := s.store.CreatePlacementRule(ctx, r, fence, by); err != nil {
		return Rule{}, fmt.Errorf("create placement rule in %s: %w", ns, err)
	}

	return s.store.GetPlacementRule(ctx, ns, r.ID)
}

// Update replaces the content of the rule id and appends by. Position 0 keeps
// the place of the rule. It returns ErrPlacementConflict under the same
// conditions as Create.
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

	r := Rule{
		ID: id, Namespace: ns, Tool: in.Tool, Project: in.Project, Stack: in.Stack,
		Collection: in.Collection, Position: in.Position,
		// The store keeps the creator and the creation time of the stored rule.
		CreatedAt: time.Time{}, CreatedBy: "",
	}

	by.At = s.now().UTC()
	by.ObjectID = id
	by.Version = auditText(&r)

	if err := s.store.UpdatePlacementRule(ctx, r, fence, by); err != nil {
		return Rule{}, fmt.Errorf("update placement rule %s: %w", id, err)
	}

	return s.store.GetPlacementRule(ctx, ns, id)
}

// auditText returns the text that the audit entry of a make or a change holds:
// the tool, the patterns and the collection, and the position when the caller
// named one.
func auditText(r *Rule) string {
	if r.Position == 0 {
		return r.Text()
	}

	return r.Text() + "; position " + strconv.Itoa(r.Position)
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
	if in.Position < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidPosition, in.Position)
	}

	return validateRuleFields(in.Tool, in.Project, in.Stack, in.Collection)
}

func (s *PlacementService) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}
