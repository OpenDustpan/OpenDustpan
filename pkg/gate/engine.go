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
	// plan, or nil.
	Owners []byte
}

// Planned evaluates the gates of the scope chain of the unit at
// PointChangePlanned. It stores the record and returns it. When a gate
// fails to run, the result is ResultError and the error wraps
// ErrRuleFailed.
func (e *Engine) Planned(_ context.Context, req PlannedRequest) (Record, error) {
	gates := e.Registry.Gates(req.Subject, PointChangePlanned)

	rec := Record{
		ID: newRecordID(), Point: PointChangePlanned, Schema: SchemaVersion,
		Subject:        RecordSubject{Unit: req.Subject.Ref(), Principal: req.Principal.ID},
		InputDigest:    inputDigest(req.Subject, req.Principal, req.Facts, string(req.Owners)),
		PolicyVersions: map[string]string{},
		Matched:        []Match{}, Blockers: []string{}, Approvals: []Approval{}, Errors: []string{},
	}

	set := cedar.NewPolicySet()
	byID := map[string]Gate{}

	for _, g := range gates {
		p, err := parseOne(g.policyText(), cedar.Permit)
		if err != nil {
			return e.fail(rec, fmt.Errorf("gate %s: %w", g.ID, err))
		}

		set.Add(cedar.PolicyID(g.ID), p)
		byID[g.ID] = g
		rec.PolicyVersions[g.ID] = g.Version()
	}

	ctxRec, err := contextRecord(req.Facts)
	if err != nil {
		return e.fail(rec, err)
	}

	_, diag := cedar.Authorize(set, entities(req.Subject, req.Principal),
		request(req.Subject, req.Principal, string(PointChangePlanned), ctxRec))

	if len(diag.Errors) > 0 {
		for _, de := range diag.Errors {
			rec.Errors = append(rec.Errors, fmt.Sprintf("gate %s: %s", de.PolicyID, de.Message))
		}

		return e.fail(rec, nil)
	}

	for _, reason := range diag.Reasons {
		g := byID[string(reason.PolicyID)]
		rec.Matched = append(rec.Matched, Match{
			Gate: g.ID, Scope: g.Scope.String(), Outcome: g.Outcome, Mode: g.Mode, Message: g.Message,
		})
	}

	slices.SortFunc(rec.Matched, func(a, b Match) int { return compareStrings(a.Gate, b.Gate) })

	err = e.combine(&rec, req)
	if err != nil {
		return e.fail(rec, err)
	}

	e.Records.Put(rec)

	return rec, nil
}

// combine joins the enforce matches: any deny wins; else every named
// approval rule is required; else the result is allow. An evaluate match
// changes nothing.
func (e *Engine) combine(rec *Record, req PlannedRequest) error {
	rec.Result = ResultAllow

	var rules []string

	for _, m := range rec.Matched {
		if m.Mode != ModeEnforce {
			continue
		}

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
		reqs, err := e.requirements(rule, req)
		if err != nil {
			return err
		}

		rec.Approvals = append(rec.Approvals, reqs...)
	}

	if len(rec.Approvals) > 0 {
		rec.Result = ResultRequireApproval
	}

	return nil
}

// ownersRule is the name of the approval outcome that takes its
// requirements from the ownership file.
const ownersRule = "owners"

func (e *Engine) requirements(rule string, req PlannedRequest) ([]Approval, error) {
	if rule == ownersRule {
		secs, err := ParseOwners(req.Owners)
		if err != nil {
			return nil, err
		}

		var out []Approval

		for _, s := range secs {
			if s.Matches(req.Facts.Change.Paths) {
				out = append(out, Approval{
					Rule: ownersRule + "/" + s.Name, Need: s.Count, From: []string{"group:" + s.Group},
				})
			}
		}

		return out, nil
	}

	r, ok := e.Registry.Rule(rule)
	if !ok {
		return nil, fmt.Errorf("%w: approval rule %q is not stored", ErrInvalid, rule)
	}

	from := make([]string, 0, len(r.Groups)+len(r.Users))
	for _, g := range r.Groups {
		from = append(from, "group:"+g)
	}

	for _, u := range r.Users {
		from = append(from, "user:"+u)
	}

	return []Approval{{Rule: r.Name, Need: r.Count, FreshAuth: r.FreshAuth, From: from}}, nil
}

// fail stores a record with the result error and returns it with an error
// that wraps ErrRuleFailed.
func (e *Engine) fail(rec Record, cause error) (Record, error) {
	rec.Result = ResultError
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
