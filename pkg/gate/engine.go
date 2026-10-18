package gate

import (
	"context"
	"fmt"
	"slices"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"
)

// Engine evaluates gates and access limits against a Registry, writes one
// record for each evaluation, and keeps approvals.
type Engine struct {
	Registry *Registry
	Records  *RecordStore
}

// NewEngine returns an engine with empty stores.
func NewEngine() *Engine {
	return &Engine{Registry: NewRegistry(), Records: NewRecordStore()}
}

// PlannedRequest is the input of PointChangePlanned.
type PlannedRequest struct {
	Subject   Subject
	Principal Principal
	Facts     Facts
	// Owners is the content of the ownership file at the commit of the
	// plan, or nil. The file is a minimal source of owner rules.
	Owners []byte
}

// evaluated holds what one Cedar run found for the stored rules.
type evaluated struct {
	gates    map[string]Gate
	defaults map[string]ApprovalDefault
	owners   map[string]OwnerRule
	matched  map[string]bool
}

// Planned evaluates the gates, the approval defaults and the owner rules of
// the scope chain of the unit at PointChangePlanned in one Cedar request.
// It stores the record and returns it. When a rule fails to run, the result
// is ResultError and the error wraps ErrRuleFailed.
func (e *Engine) Planned(_ context.Context, req PlannedRequest) (Record, error) {
	rec := Record{
		ID: newRecordID(), Point: PointChangePlanned, Schema: SchemaVersion,
		Subject:        RecordSubject{Unit: req.Subject.Ref(), Principal: req.Principal.ID},
		InputDigest:    inputDigest(req.Subject, req.Principal, req.Facts, string(req.Owners)),
		PolicyVersions: map[string]string{},
		Matched:        []Match{}, Blockers: []string{}, Approvals: []Approval{}, Errors: []string{},
	}

	set, ev, err := e.policySet(req.Subject, &rec)
	if err != nil {
		return e.fail(rec, err)
	}

	ctxRec, err := contextRecord(req.Facts)
	if err != nil {
		return e.fail(rec, err)
	}

	_, diag := cedar.Authorize(set, entities(req.Subject, req.Principal),
		request(req.Subject, req.Principal, string(PointChangePlanned), ctxRec))

	if len(diag.Errors) > 0 {
		for _, de := range diag.Errors {
			rec.Errors = append(rec.Errors, fmt.Sprintf("%s: %s", de.PolicyID, de.Message))
		}

		return e.fail(rec, nil)
	}

	for _, reason := range diag.Reasons {
		ev.matched[string(reason.PolicyID)] = true
	}

	for id := range ev.gates {
		if g := ev.gates[id]; ev.matched[id] {
			rec.Matched = append(rec.Matched, Match{
				Gate: g.ID, Scope: g.Scope.String(), Outcome: g.Outcome, Mode: g.Mode, Message: g.Message,
			})
		}
	}

	slices.SortFunc(rec.Matched, func(a, b Match) int { return compareStrings(a.Gate, b.Gate) })

	err = e.combine(&rec, req, ev)
	if err != nil {
		return e.fail(rec, err)
	}

	e.Records.Put(rec)

	return rec, nil
}

// policySet puts the stored rules of the scope chain in one Cedar policy
// set and notes their versions in rec.
func (e *Engine) policySet(sub Subject, rec *Record) (*cedar.PolicySet, *evaluated, error) {
	set := cedar.NewPolicySet()
	ev := &evaluated{
		gates: map[string]Gate{}, defaults: map[string]ApprovalDefault{},
		owners: map[string]OwnerRule{}, matched: map[string]bool{},
	}

	add := func(id, text, version string) error {
		p, err := parseOne(text, cedar.Permit)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}

		set.Add(cedar.PolicyID(id), p)

		rec.PolicyVersions[id] = version

		return nil
	}

	for _, g := range e.Registry.Gates(sub, PointChangePlanned) {
		ev.gates[g.ID] = g

		err := add(g.ID, g.policyText(), g.Version())
		if err != nil {
			return nil, nil, err
		}
	}

	for _, d := range e.Registry.Defaults(sub) {
		ev.defaults[defaultPrefix+d.ID] = d

		err := add(defaultPrefix+d.ID, d.policyText(), digest(d.ID, d.Scope.String(), d.Unless)[:12])
		if err != nil {
			return nil, nil, err
		}
	}

	for _, o := range e.Registry.OwnerRules(sub) {
		ev.owners[ownerPrefix+o.ID] = o

		err := add(
			ownerPrefix+o.ID,
			o.policyText(),
			digest(o.ID, o.Scope.String(), o.When, strings.Join(o.Paths, ","))[:12],
		)
		if err != nil {
			return nil, nil, err
		}
	}

	return set, ev, nil
}

// combine joins the enforce matches: any deny wins; else every named
// approval rule is required, with the approval defaults; else the result
// is allow. An evaluate match changes nothing. A warn that is enforced
// blocks auto-apply.
func (e *Engine) combine(rec *Record, req PlannedRequest, ev *evaluated) error {
	rec.Result = ResultAllow

	var rules []string

	warned := false

	for _, m := range rec.Matched {
		if m.Mode != ModeEnforce {
			continue
		}

		warned = warned || m.Outcome == OutcomeWarn

		if m.Outcome == OutcomeDeny {
			rec.Blockers = append(rec.Blockers, m.Gate)
		}

		if rule, ok := m.Outcome.Rule(); ok && !slices.Contains(rules, rule) {
			rules = append(rules, rule)
		}
	}

	if len(rec.Blockers) > 0 {
		rec.Result = ResultDeny

		return nil
	}

	for _, rule := range rules {
		reqs, err := e.requirements(rule, req, ev)
		if err != nil {
			return err
		}

		rec.Approvals = append(rec.Approvals, reqs...)
	}

	if d, ok := strictestDefault(ev); ok {
		rec.Approvals = append(rec.Approvals, d)
	}

	if len(rec.Approvals) > 0 {
		rec.Result = ResultRequireApproval
	}

	rec.AutoApply = rec.Result == ResultAllow && !warned

	return nil
}

// strictestDefault joins the approval defaults that matched: the highest
// count and the smallest fresh window. No default gives no requirement.
func strictestDefault(ev *evaluated) (Approval, bool) {
	var out Approval

	found := false

	for id, d := range ev.defaults {
		if !ev.matched[id] {
			continue
		}

		if !found || d.Count > out.Need {
			out.Need = d.Count
		}

		out.FreshAuthSeconds = strictestWindow(out.FreshAuthSeconds, d.FreshAuthSeconds)
		found = true
	}

	out.Rule = "default"
	out.From = []string{"anyone"}

	return out, found
}

func strictestWindow(a, b *int64) *int64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case *b < *a:
		return b
	default:
		return a
	}
}

// ownersRule is the name of the approval outcome that takes its
// requirements from the owner rules and from the ownership file.
const ownersRule = "owners"

func (e *Engine) requirements(rule string, req PlannedRequest, ev *evaluated) ([]Approval, error) {
	if rule == ownersRule {
		return ownerRequirements(req, ev)
	}

	r, ok := e.Registry.Rule(rule)
	if !ok {
		return nil, fmt.Errorf("%w: approval rule %q is not stored", ErrInvalid, rule)
	}

	return []Approval{{
		Rule: r.Name, Need: r.Count, FreshAuthSeconds: r.FreshAuthSeconds, From: approvers(r.Groups, r.Users),
	}}, nil
}

func ownerRequirements(req PlannedRequest, ev *evaluated) ([]Approval, error) {
	var out []Approval

	ids := make([]string, 0, len(ev.owners))
	for id := range ev.owners {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		o := ev.owners[id]
		if !ev.matched[id] || (len(o.Paths) > 0 && !matchGlobs(o.Paths, req.Facts.Change.Paths)) {
			continue
		}

		out = append(out, Approval{
			Rule: ownersRule + "/" + o.ID, Need: o.Count, FreshAuthSeconds: o.FreshAuthSeconds,
			From: approvers(o.Groups, o.Users),
		})
	}

	secs, err := ParseOwners(req.Owners)
	if err != nil {
		return nil, err
	}

	for _, s := range secs {
		if s.Matches(req.Facts.Change.Paths) {
			out = append(out, Approval{
				Rule: ownersRule + "/" + s.Name, Need: s.Count, From: []string{"group:" + s.Group},
			})
		}
	}

	return out, nil
}

func approvers(groups, users []string) []string {
	from := make([]string, 0, len(groups)+len(users))
	for _, g := range groups {
		from = append(from, "group:"+g)
	}

	for _, u := range users {
		from = append(from, "user:"+u)
	}

	return from
}

// fail stores a record with the result error and returns it with an error
// that wraps ErrRuleFailed.
func (e *Engine) fail(rec Record, cause error) (Record, error) {
	rec.Result = ResultError
	rec.AutoApply = false

	if cause != nil {
		rec.Errors = append(rec.Errors, cause.Error())
	}

	e.Records.Put(rec)

	return rec, fmt.Errorf("%w: %s", ErrRuleFailed, strings.Join(rec.Errors, "; "))
}

// LimitedRequest is the input of an access-limit check, after a grant
// allowed the call.
type LimitedRequest struct {
	Subject   Subject
	Principal Principal
	Action    string
}

// permitAll is the fixed policy that lets a satisfied forbid decide alone.
const permitAll = "permit (principal, action, resource);"

// CheckLimits evaluates the forbid policies of the scope chain of the unit
// with the fixed permit. A satisfied forbid denies and the error wraps
// ErrLimited with the policy ID. A policy that fails to run gives
// ErrRuleFailed. It stores a record in both cases and when the call passes.
func (e *Engine) CheckLimits(_ context.Context, req LimitedRequest) (Record, error) {
	rec := Record{
		ID: newRecordID(), Point: PointAccessLimit, Schema: SchemaVersion,
		Subject:        RecordSubject{Unit: req.Subject.Ref(), Principal: req.Principal.ID, Action: req.Action},
		InputDigest:    inputDigest(req.Subject, req.Principal, req.Action),
		PolicyVersions: map[string]string{},
		Matched:        []Match{}, Blockers: []string{}, Approvals: []Approval{}, Errors: []string{},
	}

	set := cedar.NewPolicySet()
	byID := map[string]Limit{}

	for _, l := range e.Registry.Limits(req.Subject) {
		p, err := parseOne(l.Policy, cedar.Forbid)
		if err != nil {
			return e.fail(rec, fmt.Errorf("limit %s: %w", l.ID, err))
		}

		set.Add(cedar.PolicyID(l.ID), p)
		byID[l.ID] = l
		rec.PolicyVersions[l.ID] = l.Version()
	}

	all, err := parseOne(permitAll, cedar.Permit)
	if err != nil {
		return e.fail(rec, err)
	}

	set.Add("fixed-permit", all)

	decision, diag := cedar.Authorize(set, entities(req.Subject, req.Principal),
		request(req.Subject, req.Principal, req.Action, cedarEmptyContext()))

	if len(diag.Errors) > 0 {
		for _, de := range diag.Errors {
			rec.Errors = append(rec.Errors, fmt.Sprintf("limit %s: %s", de.PolicyID, de.Message))
		}

		return e.fail(rec, nil)
	}

	if decision == cedar.Allow {
		rec.Result = ResultAllow
		e.Records.Put(rec)

		return rec, nil
	}

	rec.Result = ResultDeny

	for _, reason := range diag.Reasons {
		l := byID[string(reason.PolicyID)]
		rec.Blockers = append(rec.Blockers, l.ID)
		rec.Matched = append(rec.Matched, Match{
			Gate: l.ID, Scope: l.Scope.String(), Outcome: OutcomeDeny, Mode: ModeEnforce, Message: "access limit",
		})
	}

	slices.Sort(rec.Blockers)
	e.Records.Put(rec)

	return rec, fmt.Errorf("%w: %s by %s", ErrLimited, req.Action, strings.Join(rec.Blockers, ", "))
}
