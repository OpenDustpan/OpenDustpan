package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// stateWriteSchema returns the version of the fact schema of state.write.
func stateWriteSchema() SchemaVersion {
	return SchemaVersion{Subject: string(PointStateWrite), Major: 1, Minor: 0}
}

// defaultApprovalTimeout is the timeout of an approval rule that sets none.
const defaultApprovalTimeout = 7 * 24 * time.Hour

// placeholder matches the fact paths that a gate message names, as
// {{unit.collection}}.
var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9]*(?:\.[A-Za-z][A-Za-z0-9]*)*)\s*\}\}`)

// StateWrite is one write of state, or one end of a lock, that a tool
// adapter is about to make for a person's own CLI or CI, or that the
// tool-neutral API is about to make.
type StateWrite struct {
	// Principal is the caller, after the grants allowed the call.
	Principal identity.Principal
	// Unit is the unit that the write changes.
	Unit identity.Object
	// Operation says what kind of write it is.
	Operation OperationKind
	// Action is the authorization action of the route. The access limits
	// are evaluated for it.
	Action identity.Action
	// Source is cli for a write from the CLI of a tool, and api for a call
	// of the tool-neutral API.
	Source SourceKind
	// PlanLock is true when the write takes or ends the lock or the update
	// lease of the plan of a job. Only a job token uses it.
	PlanLock bool
	// State is what the write changes in the state of the unit, read by the
	// adapter, or nil when the server holds no new state for the write. The
	// guard puts it into the facts state.resources and state.removed.
	State *StateChange
}

// GuardStateWrite checks the access limits of the action of w, then
// evaluates the gates of state.write for the unit of w. It returns the
// decision and nil when nothing refuses the write. A refusal is a
// *RefusalError, and any other error also refuses the write: the guard fails
// closed.
//
// A job token skips the gates only as far as Evaluate allows: a write by a
// phase that passed change.apply to a unit of its own job, or the lock or
// the update lease of the plan in any other phase. Each other write with a
// job token fails with identity.ErrDenied. The access limits hold for it.
func (s *Service) GuardStateWrite(ctx context.Context, w StateWrite) (Decision, error) {
	ctx = withFactMemo(ctx)

	subject, err := s.subjects.Subject(ctx, w.Principal)
	if err != nil {
		return Decision{}, fmt.Errorf("state.write: %w", err)
	}

	if !subject.BreakGlassAccount {
		call := AccessCall{Principal: w.Principal, Action: w.Action, Object: w.Unit}

		if limitErr := s.checkLimits(ctx, subject, call); limitErr != nil {
			return Decision{}, limitErr
		}
	}

	if subject.Credential == CredentialJobToken {
		// The job model decides a write with a job token. The write needs no
		// fence, because it writes no decision record.
		return s.Evaluate(ctx, Change{
			Point:     PointStateWrite,
			Namespace: w.Unit.Namespace(),
			Ref:       ChangeRef{Kind: ChangeStateWrite, ID: ""},
			Subject:   subject,
			Source:    w.Source,
			Author:    subject.ID,
			Operation: w.Operation,
			Units: []identity.Object{
				w.Unit,
			},
			Holders:    nil,
			PlanLock:   w.PlanLock,
			BreakGlass: "",
			Fence:      namespace.Fence{Namespace: w.Unit.Namespace(), FailoverVersion: 0},
			State:      nil,
		})
	}

	fence, err := s.fences.Fence(ctx, w.Unit.Namespace())
	if err != nil {
		return Decision{}, fmt.Errorf("state.write: fence: %w", err)
	}

	return s.Evaluate(ctx, Change{
		Point: PointStateWrite, Namespace: w.Unit.Namespace(), Ref: ChangeRef{Kind: ChangeStateWrite, ID: ""},
		Subject: subject, Source: w.Source, Author: subject.ID, Operation: w.Operation,
		Units: []identity.Object{w.Unit}, Holders: nil, PlanLock: w.PlanLock, BreakGlass: "", Fence: fence,
		State: w.State,
	})
}

// unitEval holds what the evaluation of one unit needs.
type unitEval struct {
	unit identity.Object
	// scopes holds the scopes whose configuration holds for the unit: its
	// chain and the holders of the change.
	scopes []Scope
	facts  Facts
	doc    map[string]any
}

// stateWriteRun is one evaluation of state.write.
type stateWriteRun struct {
	svc       *Service
	change    Change
	units     []unitEval
	cfg       Config
	gates     []Gate
	invalid   map[PolicyID]string
	answers   []Answer
	engineErr error
	requires  []Requirement
	warnings  []string
	result    Result
	refusal   *RefusalError
}

// unitEffect collects what the enforced gates say about one unit.
type unitEffect struct {
	denies    []string
	errs      []string
	approvals []string
	warns     []string
}

// result returns the result of the unit: the strictest effect.
func (e *unitEffect) result() Result {
	switch {
	case len(e.denies) > 0:
		return ResultDeny
	case len(e.errs) > 0:
		return ResultError
	case len(e.approvals) > 0:
		return ResultAwaitingApproval
	case len(e.warns) > 0:
		return ResultWarn
	default:
		return ResultAllow
	}
}

// evaluateStateWrite evaluates the gates of state.write for each unit of c
// and writes the decision record.
func (s *Service) evaluateStateWrite(ctx context.Context, c Change) (Decision, error) {
	if c.BreakGlass != "" {
		return Decision{}, fmt.Errorf(
			"%w: a break-glass for a gate in error is not implemented in this version", ErrUnavailable,
		)
	}

	// Defense in depth: the store checks the fence of the record too. This
	// check stays so that no record is built with the fence of another
	// namespace.
	if !sameNamespaceFence(c.Fence, c.Namespace) {
		return Decision{}, fmt.Errorf("%w: the fence is not the fence of namespace %q", ErrInvalid, c.Namespace)
	}

	// Boundary check of the state change. It runs before any read of the
	// configuration, so a hostile change costs no store call. Defense in
	// depth: the adapter reads the state from the store, and the facts
	// builder checks the change again. This check does not rely on either.
	if err := checkStateChange(c); err != nil {
		return Decision{}, &RefusalError{Err: err, Record: "", Messages: []string{cleanText(err.Error())}}
	}

	run, err := s.newStateWriteRun(ctx, c)
	if err != nil {
		return Decision{}, err
	}

	rec := run.record()

	if err = s.store.Records().Put(ctx, rec, c.Fence); err != nil {
		// The record ID is known, but the record is not stored. The refusal
		// names the ID so that the person can tell support which write failed.
		return Decision{}, &RefusalError{
			Err:      fmt.Errorf("%w: write the decision record: %w", ErrUnavailable, err),
			Record:   rec.ID,
			Messages: []string{"The decision record could not be written, so the write is refused."},
		}
	}

	if run.refusal != nil {
		run.refusal.Record = rec.ID

		return Decision{}, run.refusal
	}

	return Decision{
		Record:       rec.ID,
		Result:       rec.Result,
		Requirements: nil,
		Warnings:     run.warnings,
		AutoApply:    false,
	}, nil
}

// newStateWriteRun reads the configuration and asks the engine.
func (s *Service) newStateWriteRun(ctx context.Context, c Change) (*stateWriteRun, error) {
	units, scopes, err := s.stateWriteUnits(ctx, c)
	if err != nil {
		return nil, err
	}

	cfg, err := s.store.Snapshot(ctx, scopes)
	if err != nil {
		return nil, fmt.Errorf("%w: gate configuration: %w", ErrUnavailable, err)
	}

	run := &stateWriteRun{
		svc:       s,
		change:    c,
		units:     units,
		cfg:       cfg,
		invalid:   nil,
		answers:   nil,
		engineErr: nil,
		gates: slices.DeleteFunc(
			slices.Clone(cfg.Gates),
			func(g Gate) bool { return g.DecisionPoint != PointStateWrite },
		),
		requires: nil,
		warnings: nil,
		result:   ResultAllow,
		refusal:  nil,
	}

	run.invalid, run.engineErr = s.recheck(ctx, run.gates)
	if run.engineErr == nil {
		run.answers, run.engineErr = s.askEngine(ctx, c, units, run.gates)
	}

	return run, nil
}

// record builds the decision record of the run and decides the result.
func (r *stateWriteRun) record() Record {
	at := r.svc.now()
	id := NewRecordID()

	rec := Record{
		ID: id, Point: PointStateWrite, Change: ChangeRef{Kind: ChangeStateWrite, ID: string(id)},
		Namespace: r.change.Namespace, At: at, Subject: r.change.Subject.ID, Schema: stateWriteSchema(),
		PathSets: nil, Units: make([]UnitResult, 0, len(r.units)), Requirements: nil, Warnings: nil,
		AutoApply: false, Controls: nil, BreakGlass: "", Result: ResultAllow, Action: "", Count: 1, LastAt: at,
	}

	total := unitEffect{denies: nil, errs: nil, approvals: nil, warns: nil}

	for i := range r.units {
		res, eff := r.unitResult(i)
		rec.Units = append(rec.Units, res)

		total.denies = append(total.denies, eff.denies...)
		total.errs = append(total.errs, eff.errs...)
		total.approvals = append(total.approvals, eff.approvals...)
		total.warns = append(total.warns, eff.warns...)
	}

	rec.Requirements, rec.Warnings = r.requires, total.warns
	r.warnings = total.warns

	r.decide(&rec, &total, r.engineErr != nil && hasEnforcedGate(r.gates))

	return rec
}

// decide sets the result of the record and the refusal of the run.
func (r *stateWriteRun) decide(rec *Record, total *unitEffect, engineDown bool) {
	rec.Result = total.result()

	switch {
	case len(total.denies) > 0:
		r.refusal = &RefusalError{Err: ErrDenied, Record: "", Messages: total.denies}
	case engineDown:
		rec.Result = ResultError
		r.refusal = &RefusalError{Err: ErrUnavailable, Record: "", Messages: []string{cleanText(r.engineErr.Error())}}
	case len(total.errs) > 0:
		r.refusal = &RefusalError{Err: ErrRuleFailed, Record: "", Messages: total.errs}
	case len(total.approvals) > 0:
		// A CLI cannot wait for people, so an approval outcome refuses.
		r.refusal = &RefusalError{Err: ErrDenied, Record: "", Messages: total.approvals}
	default:
	}
}

// stateWriteUnits builds the facts of each unit of c, and the scopes whose
// configuration holds for the change.
func (s *Service) stateWriteUnits(ctx context.Context, c Change) ([]unitEval, []Scope, error) {
	units := make([]unitEval, 0, len(c.Units))

	var scopes []Scope

	for i := range c.Units {
		u := c.Units[i]

		coll, err := s.collectionOf(ctx, u)
		if err != nil {
			return nil, nil, err
		}

		chain, err := UnitChain(u, coll)
		if err != nil {
			return nil, nil, err
		}

		// A write is checked against facts that exist. A unit reference that
		// resolves to nothing fails closed: the gates never see "no collection,
		// no labels" for a unit that the source does not know.
		labels, err := s.labelsOfKnown(ctx, u)
		if err != nil {
			return nil, nil, err
		}

		facts, doc, err := stateWriteFacts(c, u, coll, labels)
		if err != nil {
			return nil, nil, err
		}

		held := slices.Concat(chain, c.Holders)
		for _, sc := range held {
			if !slices.Contains(scopes, sc) {
				scopes = append(scopes, sc)
			}
		}

		units = append(units, unitEval{unit: u, scopes: held, facts: facts, doc: doc})
	}

	return units, scopes, nil
}

// checkStateChange checks the state change of c. A change is valid for a
// rollback of exactly one unit. It returns ErrInvalid or ErrLimit.
func checkStateChange(c Change) error {
	if c.State == nil {
		return nil
	}

	if len(c.Units) != 1 {
		return fmt.Errorf("%w: a state change needs exactly one unit, not %d", ErrInvalid, len(c.Units))
	}

	return c.State.validate(c.Operation)
}

// stateWriteFacts builds the fact document of one unit at state.write. The
// plan facts are not available: the write comes from a CLI that sends no
// plan. The state facts are available only when c holds a state change, which
// the adapter read from the current state and the new state. Without it,
// state.available is false and the other state facts are the zero value.
func stateWriteFacts(
	c Change,
	u identity.Object,
	coll collection.Name,
	labels []string,
) (Facts, map[string]any, error) {
	source := c.Source
	if source == "" {
		source = SourceAPI
	}

	var f Facts

	f.Schema = stateWriteSchema()
	f.Principal = PrincipalFacts{Kind: c.Subject.Kind, Credential: c.Subject.Credential}
	f.Source = SourceFacts{Kind: source, Ref: "", Protected: false, Commit: "", UpstreamUnit: ""}
	f.Unit = UnitFacts{Tool: u.Tool(), Collection: coll.String(), Labels: labels}
	f.Operation = OperationFacts{Kind: c.Operation}

	if c.State != nil {
		// Defense in depth: the evaluation checked the change before. The
		// facts builder checks it again, so that no other caller of this
		// function builds a fact from a change that is not valid.
		if err := checkStateChange(c); err != nil {
			return Facts{}, nil, err
		}

		f.State = c.State.facts()
	}

	raw, err := json.Marshal(f)
	if err != nil {
		return Facts{}, nil, fmt.Errorf("%w: fact document: %w", ErrInvalid, err)
	}

	if len(raw) > MaxFactDocumentBytes {
		f.Unit.Labels = nil
		f.Document = DocumentFacts{Truncated: []string{"unit.labels"}}

		if raw, err = json.Marshal(f); err != nil || len(raw) > MaxFactDocumentBytes {
			return Facts{}, nil, fmt.Errorf("%w: the fact document of %s", ErrLimit, u)
		}
	}

	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return Facts{}, nil, fmt.Errorf("%w: fact document: %w", ErrInvalid, err)
	}

	return f, doc, nil
}

// recheck sets the State of each gate, as the State type says. A gate whose
// CheckedWith is the engine version and the schema version of this build is
// valid. Each other gate is checked again with the engine, and a diagnostic
// makes it invalid. It returns the diagnostic of each invalid gate, or the
// error of an engine that cannot check.
func (s *Service) recheck(ctx context.Context, gates []Gate) (map[PolicyID]string, error) {
	current := CheckedWith{Engine: s.engine.Version(), Schema: stateWriteSchema()}
	invalid := map[PolicyID]string{}

	for i := range gates {
		g := &gates[i]
		g.State = StateValid

		if g.CheckedWith == current {
			continue
		}

		diags, err := s.engine.Check(ctx, Schema{Version: stateWriteSchema(), PathSets: nil}, []Policy{gatePolicy(g)})
		if err != nil {
			return nil, fmt.Errorf("%w: check gate %s: %w", ErrUnavailable, g.PolicyID(), err)
		}

		if len(diags) > 0 {
			g.State = StateInvalid
			invalid[g.PolicyID()] = cleanText(diags[0].Message)
		}
	}

	return invalid, nil
}

// gatePolicy returns the policy that the engine takes for g.
func gatePolicy(g *Gate) Policy {
	return Policy{
		ID: g.PolicyID(), Version: g.Version, Kind: PolicyGate, Point: g.DecisionPoint,
		Condition: g.Condition, Unless: g.Unless, Text: "",
	}
}

// askEngine gives each unit its answer. It returns the answers in the order
// of the units, or the error of the engine, which fails the write closed. It
// asks nothing when no valid gate exists.
func (s *Service) askEngine(ctx context.Context, c Change, units []unitEval, gates []Gate) ([]Answer, error) {
	policies := make([]Policy, 0, len(gates))

	for i := range gates {
		if gates[i].State == StateValid {
			policies = append(policies, gatePolicy(&gates[i]))
		}
	}

	if len(policies) == 0 {
		return make([]Answer, len(units)), nil
	}

	requests := make([]Request, 0, len(units))

	for i := range units {
		f := units[i].facts
		requests = append(requests, Request{
			ID: units[i].unit.String(), Principal: c.Subject.ID, Groups: c.Subject.Groups,
			Action: string(PointStateWrite), Resource: units[i].unit, Facts: &f, Access: nil,
		})
	}

	set := PolicySet{Schema: Schema{Version: stateWriteSchema(), PathSets: nil}, Policies: policies}

	answers, err := s.engine.Evaluate(ctx, set, requests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	if len(answers) != len(requests) {
		return nil, fmt.Errorf(
			"%w: the engine gave %d answers for %d requests", ErrUnavailable, len(answers), len(requests),
		)
	}

	for i := range answers {
		if answers[i].RequestID != requests[i].ID {
			return nil, fmt.Errorf("%w: the engine answer %d does not echo its request", ErrUnavailable, i)
		}
	}

	return answers, nil
}

// hasEnforcedGate reports whether a valid gate in enforce mode exists. An
// engine that does not answer matters only then.
func hasEnforcedGate(gates []Gate) bool {
	return slices.ContainsFunc(gates, func(g Gate) bool { return g.Mode == ModeEnforce && g.State == StateValid })
}

// unitResult evaluates the gates for unit i from its answer.
func (r *stateWriteRun) unitResult(i int) (UnitResult, *unitEffect) {
	u := &r.units[i]

	digest, err := digestOf(u.doc)
	if err != nil {
		digest = ""
	}

	res := UnitResult{
		Unit: UnitRef{Object: u.unit}, Facts: u.facts, Digest: digest, Gates: []PolicyResult{}, Result: ResultAllow,
	}
	eff := &unitEffect{denies: nil, errs: nil, approvals: nil, warns: nil}

	var answer *Answer
	if r.engineErr == nil {
		answer = &r.answers[i]
	}

	for gi := range r.gates {
		g := &r.gates[gi]
		if !slices.Contains(u.scopes, g.Scope) {
			continue
		}

		pr := r.gateResult(g, u, answer)
		if g.Mode == ModeEnforce {
			r.enforce(&pr, g, u, eff)
		}

		res.Gates = append(res.Gates, pr)
	}

	res.Result = eff.result()
	if r.engineErr != nil && hasEnforcedGate(r.gates) {
		res.Result = ResultError
	}

	return res, eff
}

// gateResult sets the match, the error and the message of one gate for one
// unit.
func (r *stateWriteRun) gateResult(g *Gate, u *unitEval, answer *Answer) PolicyResult {
	pr := PolicyResult{
		Policy: g.PolicyID(), Version: g.Version, Mode: g.Mode, Outcome: g.Outcome, Matched: false,
		Error: false, Diagnostic: "", Skipped: false, BrokenBy: "", Message: "",
	}

	switch {
	case g.State != StateValid:
		pr.Error = true
		pr.Diagnostic = firstNonEmpty(r.invalid[pr.Policy], "the gate failed its last check and is not valid")
	case r.engineErr != nil:
		pr.Error, pr.Diagnostic = true, cleanText(r.engineErr.Error())
	case answer != nil:
		r.readAnswer(&pr, answer)
	default:
	}

	if !pr.Matched {
		return pr
	}

	msg, err := renderMessage(g.Message, u.doc)
	if err != nil {
		pr.Matched, pr.Error, pr.Diagnostic = false, true, cleanText(err.Error())

		return pr
	}

	pr.Message = cleanText(msg)

	return pr
}

// readAnswer sets the match and the error of pr from the answer.
func (r *stateWriteRun) readAnswer(pr *PolicyResult, answer *Answer) {
	for _, d := range answer.Diagnostics {
		if d.Policy == pr.Policy {
			pr.Error, pr.Diagnostic = true, cleanText(d.Message)
		}
	}

	if !pr.Error && slices.Contains(answer.Matched, pr.Policy) {
		pr.Matched = true
	}
}

// enforce adds the effect of the enforced gate g to eff.
func (r *stateWriteRun) enforce(pr *PolicyResult, g *Gate, u *unitEval, eff *unitEffect) {
	switch {
	case pr.Error && g.OnError == OnErrorSkip:
		pr.Skipped = true
	case pr.Error:
		eff.errs = append(eff.errs, "the gate "+pr.Policy.String()+" failed: "+pr.Diagnostic)
	case pr.Matched:
		r.applyOutcome(pr, u, eff)
	default:
	}
}

// applyOutcome adds the effect of the matched gate pr to eff.
func (r *stateWriteRun) applyOutcome(pr *PolicyResult, u *unitEval, eff *unitEffect) {
	switch pr.Outcome {
	case OutcomeDeny:
		eff.denies = append(eff.denies, firstNonEmpty(pr.Message, "the gate "+pr.Policy.String()+" denies the write"))
	case OutcomeWarn:
		eff.warns = append(
			eff.warns,
			firstNonEmpty(pr.Message, "the gate "+pr.Policy.String()+" warns about the write"),
		)
	default:
		r.requireApproval(pr, u, eff)
	}
}

// requireApproval adds the requirement of an approval outcome. A CLI cannot
// wait for it, so the message tells the person what to do.
func (r *stateWriteRun) requireApproval(pr *PolicyResult, u *unitEval, eff *unitEffect) {
	id, ok := pr.Outcome.Rule()
	rule, found := r.findRule(u, id)

	// Defense in depth: the store refuses a gate whose approval rule does
	// not resolve. This check stays so that a row from another version, or
	// a restored row, still fails closed.
	if !ok || !found {
		pr.Error, pr.Matched = true, false
		pr.Diagnostic = "the approval rule " + string(pr.Outcome) + " does not exist for the unit"
		eff.errs = append(eff.errs, "the gate "+pr.Policy.String()+" failed: "+pr.Diagnostic)

		return
	}

	req := requirementOfRule(*rule)
	if !slices.ContainsFunc(r.requires, func(x Requirement) bool { return x.Name == req.Name }) {
		r.requires = append(r.requires, req)
	}

	eff.approvals = append(eff.approvals, fmt.Sprintf(
		"%s The write needs approval by rule %s. A write from a CLI cannot wait for approval, "+
			"so the server refuses the write to %s.",
		firstNonEmpty(pr.Message, "The gate "+pr.Policy.String()+" matches."), id, u.unit,
	))
}

// findRule returns the approval rule id of the scopes of the unit.
func (r *stateWriteRun) findRule(u *unitEval, id ID) (*ApprovalRule, bool) {
	for i := range r.cfg.ApprovalRules {
		if rule := &r.cfg.ApprovalRules[i]; rule.ID == id && slices.Contains(u.scopes, rule.Scope) {
			return rule, true
		}
	}

	return nil, false
}

// requirementOfRule returns the requirement that rule gives, with no
// signatures.
func requirementOfRule(r ApprovalRule) Requirement {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = defaultApprovalTimeout
	}

	return Requirement{
		Name: RuleRequirement(r.ID), Version: r.Version, Approvers: r.Approvers, Count: r.Count,
		AuthorMayApprove: r.AuthorMayApprove, FreshAuth: r.FreshAuth, TimeoutSeconds: int64(timeout.Seconds()),
		Signatures: nil, Evaluate: false,
	}
}

// errUnknownFact reports a message template that names a path that is not a
// fact.
var errUnknownFact = errors.New("the message names a path that is not a fact")

// renderMessage fills the placeholders of template with the facts of doc. A
// placeholder that names no fact gives an error, never an empty string.
func renderMessage(template string, doc map[string]any) (string, error) {
	var failed string

	out := placeholder.ReplaceAllStringFunc(template, func(m string) string {
		path := placeholder.FindStringSubmatch(m)[1]

		v, ok := lookup(doc, path)
		if !ok {
			failed = path

			return m
		}

		return factText(v)
	})

	if failed != "" {
		return "", fmt.Errorf("%w: %q", errUnknownFact, failed)
	}

	return out, nil
}

// lookup returns the value at the dotted path in doc.
func lookup(doc map[string]any, path string) (any, bool) {
	var cur any = doc

	for part := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}

	return cur, true
}

// factText returns the text of a fact value in a message.
func factText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, factText(e))
		}

		return strings.Join(parts, ", ")
	case float64:
		return fmt.Sprintf("%.0f", t)
	default:
		return fmt.Sprint(t)
	}
}
