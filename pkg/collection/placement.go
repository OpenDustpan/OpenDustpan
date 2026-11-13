package collection

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of placement rules, returned by this package and by
// PlacementStore implementations.
var (
	// ErrPlacementRuleNotFound reports a placement rule that the namespace does not hold.
	ErrPlacementRuleNotFound = errors.New("placement rule not found: list the rules with " +
		"GET /api/dustpan/v1/namespaces/{namespace}/placement-rules")
	// ErrInvalidPattern reports a pattern that fails validation.
	ErrInvalidPattern = errors.New("invalid placement pattern")
	// ErrInvalidPlacementTool reports a tool that has no placement rules.
	ErrInvalidPlacementTool = errors.New("invalid placement tool")
	// ErrInvalidPosition reports a position outside the list of rules.
	ErrInvalidPosition = errors.New("invalid placement rule position")
	// ErrPlacementConflict reports a rule that an earlier rule hides, or a rule
	// that would hide a later rule. A hidden rule never matches, because the
	// first match wins.
	ErrPlacementConflict = errors.New("placement rule conflict")
	// ErrTooManyPlacementRules reports a namespace that holds the most rules.
	ErrTooManyPlacementRules = errors.New("placement rule limit reached: delete a rule you do not need")
	// ErrNoPlacement reports that no placement rule matches a new unit.
	ErrNoPlacement = errors.New("no placement rule matches")
)

// Limits of placement rules.
const (
	// MaxPatternLength is the most characters of one pattern.
	MaxPatternLength = 64
	// MaxPatternWildcards is the most '*' characters of one pattern.
	MaxPatternWildcards = 3
	// MaxNameLength is the longest project or stack name that a rule matches.
	// A longer name matches no rule.
	MaxNameLength = 255
	// MaxPlacementRules is the most rules of one namespace.
	MaxPlacementRules = 200
)

// The tools whose new units a placement rule places. A rule for ToolOpenTofu
// matches a new workspace with the project "*" and the workspace name in the
// stack pattern, because a workspace has no project.
const (
	ToolPulumi   = "pulumi"
	ToolOpenTofu = "opentofu"
)

// PlacementTools lists the tools that placement rules support.
func PlacementTools() []string {
	return []string{ToolPulumi, ToolOpenTofu}
}

// Pattern is a validated pattern for a project name or a stack name. It is a
// glob: a literal character matches itself, and '*' matches any run of
// characters, also the empty run. The match is case-sensitive. A pattern is
// never a regular expression, and the match runs in time that is bounded by
// the length of the pattern and of the name.
type Pattern string

// ParsePattern validates s as a pattern. A pattern has 1 to MaxPatternLength
// characters of ASCII letters, digits, '.', '_', '-' and '*'. It has at most
// MaxPatternWildcards '*' characters, and no two of them are next to each
// other. The function rejects an invalid pattern and does not change it.
func ParsePattern(s string) (Pattern, error) {
	if err := checkPattern(s); err != nil {
		return "", err
	}

	return Pattern(s), nil
}

// checkPattern returns nil when s is a valid pattern, else the error that
// ParsePattern returns.
func checkPattern(s string) error {
	if s == "" || len(s) > MaxPatternLength {
		return fmt.Errorf("%w: %q must have 1 to %d characters", ErrInvalidPattern, s, MaxPatternLength)
	}

	stars := 0

	for i := range len(s) {
		c := s[i]

		switch {
		case c == '*':
			stars++

			if i > 0 && s[i-1] == '*' {
				return fmt.Errorf("%w: %q has two '*' next to each other", ErrInvalidPattern, s)
			}
		case isPatternLiteral(c):
		default:
			return fmt.Errorf("%w: %q has a character that is not allowed", ErrInvalidPattern, s)
		}
	}

	if stars > MaxPatternWildcards {
		return fmt.Errorf("%w: %q has more than %d '*' characters", ErrInvalidPattern, s, MaxPatternWildcards)
	}

	return nil
}

func isPatternLiteral(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '-'
}

// String returns the pattern as written.
func (p Pattern) String() string {
	return string(p)
}

// Matches reports whether the pattern matches the whole of name. A name that
// is longer than MaxNameLength matches nothing.
//
// The loop keeps the position of the last '*' and moves back to it at most
// once for each character of name, so the run time is at most the length of the
// pattern times the length of the name.
func (p Pattern) Matches(name string) bool {
	if len(name) > MaxNameLength || len(p) > MaxPatternLength {
		return false
	}

	pat := string(p)
	pi, ni := 0, 0
	star, mark := -1, 0

	for ni < len(name) {
		switch {
		case pi < len(pat) && pat[pi] == '*':
			star, mark = pi, ni
			pi++
		case pi < len(pat) && pat[pi] == name[ni]:
			pi++
			ni++
		case star >= 0:
			mark++
			ni = mark
			pi = star + 1
		default:
			return false
		}
	}

	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}

	return pi == len(pat)
}

// Covers reports whether every name that other matches is also a name that
// the pattern matches. It is exact for the glob here: a '*' of other can only
// be consumed by a '*' of the pattern, so the pattern covers other when it
// matches the text of other with '*' read as a literal character.
func (p Pattern) Covers(other Pattern) bool {
	return p.Matches(string(other))
}

// Rule is one placement rule of a namespace. A rule matches a new unit of its
// tool when the project and the stack name both match, and then names the
// collection of the unit. The rules of a namespace are an ordered list and the
// first rule that matches wins. ID is stable and random, with the prefix plr-.
type Rule struct {
	CreatedAt  time.Time
	ID         string
	Namespace  namespace.Name
	Tool       string
	Project    Pattern
	Stack      Pattern
	Collection Name
	// CreatedBy is the subject that made the rule.
	CreatedBy string
	// Position is the place of the rule in the list, from 1.
	Position int
}

// Validate checks the tool, the patterns, the collection name and the position
// of the rule (0 or more; the store reads 0 as "the end" for a new rule and as
// "the current place" for a change), and returns the error of the first one that is invalid. Every
// PlacementStore adapter calls it before a write, so a caller that builds a
// Pattern without ParsePattern cannot store a pattern that was not checked.
func (r Rule) Validate() error {
	if r.Position < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidPosition, r.Position)
	}

	return validateRuleFields(r.Tool, r.Project, r.Stack, r.Collection)
}

// validateRuleFields checks the tool, the two patterns and the collection name
// of a rule.
func validateRuleFields(tool string, project, stack Pattern, coll Name) error {
	if !slices.Contains(PlacementTools(), tool) {
		return fmt.Errorf("%w: %q, want one of %v", ErrInvalidPlacementTool, tool, PlacementTools())
	}

	if err := checkPattern(project.String()); err != nil {
		return err
	}

	if err := checkPattern(stack.String()); err != nil {
		return err
	}

	if _, err := ParseName(coll.String()); err != nil {
		return err
	}

	return nil
}

// Matches reports whether the rule matches a new unit of tool with the given
// project and stack name.
func (r Rule) Matches(tool, project, stack string) bool {
	return r.Tool == tool && r.Project.Matches(project) && r.Stack.Matches(stack)
}

// Hides reports whether r matches every unit that other matches. When r is
// before other in the list, other never matches.
func (r Rule) Hides(other Rule) bool {
	return r.Tool == other.Tool && r.Project.Covers(other.Project) && r.Stack.Covers(other.Stack)
}

// Text returns the rule in the form that the audit log and the errors use.
func (r Rule) Text() string {
	return r.Tool + " " + r.Project.String() + "/" + r.Stack.String() + " -> " + r.Collection.String()
}

// FirstMatch returns the first rule of rules that matches a new unit of tool
// with the given project and stack name, with true. The caller passes the rules
// in list order. It returns false when no rule matches.
func FirstMatch(rules []Rule, tool, project, stack string) (Rule, bool) {
	for i := range rules {
		if rules[i].Matches(tool, project, stack) {
			return rules[i], true
		}
	}

	var none Rule

	return none, false
}

// PlacementError is the error of a unit that no rule places. It matches
// ErrNoPlacement and names the unit and the fix.
type PlacementError struct {
	Namespace namespace.Name
	Tool      string
	Project   string
	Stack     string
}

func (e *PlacementError) Error() string {
	return fmt.Sprintf(
		"no placement rule matches %s stack %s/%s in namespace %s: add a rule with "+
			"POST /api/dustpan/v1/namespaces/%s/placement-rules, "+
			"or create a collection with POST /api/dustpan/v1/namespaces/%s/collections "+
			"and put the unit into it with PUT /api/inventory/unit-collection",
		e.Tool, quoteName(e.Project), quoteName(e.Stack), e.Namespace, e.Namespace, e.Namespace,
	)
}

// Is reports whether target is ErrNoPlacement.
func (e *PlacementError) Is(target error) bool {
	return target == ErrNoPlacement
}

// quoteName shows a name that a client sent. It cuts a long name and removes
// control characters, so that the error text stays on one line.
func quoteName(s string) string {
	if len(s) > MaxPatternLength {
		s = s[:MaxPatternLength] + "..."
	}

	return fmt.Sprintf("%q", strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return '?'
		}

		return r
	}, s))
}

// PlacementStore is the persistence port for placement rules. Every write
// checks the fence and appends its audit entry in the same transaction. The
// rules of a namespace keep the positions 1 to n with no gap: a write shifts
// the positions of the other rules in the same transaction.
//
// The store also keeps the list free of hidden rules. Each create and update
// reads the list in the transaction that writes it, applies the change, and
// runs CheckPlacementOrder on the result. A change that leaves a hidden rule
// returns ErrPlacementConflict and is not stored, also when two callers write
// at the same time. Every adapter runs RunPlacement in
// pkg/collection/collectiontest.
type PlacementStore interface {
	// CreatePlacementRule inserts r at r.Position and moves the rules from that
	// position on down by one place. Position 0 means the end of the list, read
	// in the same transaction. It returns ErrNotFound when r.Collection does not
	// exist in the namespace, ErrInvalidPosition when r.Position is not 0 or from
	// 1 to the count of rules plus 1, ErrPlacementConflict when the result has a
	// hidden rule, ErrTooManyPlacementRules at MaxPlacementRules, and
	// namespace.ErrFenced when the fence is stale. It also returns ErrNotFound
	// when r.Namespace is not the namespace of the fence.
	CreatePlacementRule(ctx context.Context, r Rule, fence namespace.Fence, e audit.Entry) error
	// UpdatePlacementRule replaces the tool, the patterns and the collection of
	// the rule r.ID, and moves it to r.Position. Position 0 keeps the place of
	// the rule. It does not change the creator or the creation time. It returns
	// ErrPlacementRuleNotFound (also when r.Namespace is not the namespace of the
	// fence), ErrNotFound for a missing collection, ErrInvalidPosition when
	// r.Position is not 0 or from 1 to the count of rules,
	// ErrPlacementConflict when the result has a hidden rule, and
	// namespace.ErrFenced.
	UpdatePlacementRule(ctx context.Context, r Rule, fence namespace.Fence, e audit.Entry) error
	// DeletePlacementRule removes the rule id and closes the gap in the
	// positions. It returns ErrPlacementRuleNotFound or namespace.ErrFenced.
	DeletePlacementRule(ctx context.Context, ns namespace.Name, id string, fence namespace.Fence, e audit.Entry) error
	// GetPlacementRule returns the rule id of ns, or ErrPlacementRuleNotFound.
	GetPlacementRule(ctx context.Context, ns namespace.Name, id string) (Rule, error)
	// ListPlacementRules returns the rules of ns in list order. A namespace with
	// no rule gives an empty list.
	ListPlacementRules(ctx context.Context, ns namespace.Name) ([]Rule, error)
}

// CheckPlacementOrder checks the rules, in list order, for the rule with the ID
// changed: no earlier rule may hide it, and it may not hide a later rule. It
// returns ErrPlacementConflict, with a message that names the two rules, for a
// hidden rule. A list that does not hold the ID passes.
func CheckPlacementOrder(rules []Rule, changed string) error {
	i := slices.IndexFunc(rules, func(r Rule) bool { return r.ID == changed })
	if i < 0 {
		return nil
	}

	r := &rules[i]

	for j := range i {
		if rules[j].Hides(*r) {
			return fmt.Errorf("%w: rule %q already matches every unit of %q, "+
				"so this rule never matches: put it before that rule or narrow its patterns",
				ErrPlacementConflict, rules[j].Text(), r.Text())
		}
	}

	for j := i + 1; j < len(rules); j++ {
		if r.Hides(rules[j]) {
			return fmt.Errorf("%w: rule %q would match every unit of %q, "+
				"so that rule never matches: put it after that rule or narrow its patterns",
				ErrPlacementConflict, r.Text(), rules[j].Text())
		}
	}

	return nil
}
