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
)

// stateWriteSchema is the version of the fact schema of state.write.
var stateWriteSchema = SchemaVersion{Subject: string(PointStateWrite), Major: 1, Minor: 0}

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
}

// GuardStateWrite checks the access limits of the action of w, then
// evaluates the gates of state.write for the unit of w. It returns the
// decision and nil when nothing refuses the write. A refusal is a
// *RefusalError, and any other error also refuses the write: the guard fails
// closed.
//
// A job token passes the gates here, because the job model decided the
// change of such a write (see Evaluate). The access limits hold for it.
func (s *Service) GuardStateWrite(ctx context.Context, w StateWrite) (Decision, error) {
	subject, err := s.subjects.Subject(ctx, w.Principal)
	if err != nil {
		return Decision{}, fmt.Errorf("state.write: %w", err)
	}

	if !subject.BreakGlassAccount {
		if err = s.checkLimits(ctx, subject, AccessCall{
			Principal: w.Principal, Action: w.Action, Object: w.Unit,
		}); err != nil {
			return Decision{}, err
		}
	}

	if subject.Credential == CredentialJobToken {
		return Decision{Record: "", Result: ResultAllow, Requirements: nil, Warnings: nil, AutoApply: false}, nil
	}

	fence, err := s.fences.Fence(ctx, w.Unit.Namespace())
	if err != nil {
		return Decision{}, fmt.Errorf("state.write: fence: %w", err)
	}

	return s.Evaluate(ctx, Change{
		Point: PointStateWrite, Namespace: w.Unit.Namespace(), Ref: ChangeRef{Kind: ChangeStateWrite, ID: ""},
		Subject: subject, Source: w.Source, Author: subject.ID, Operation: w.Operation,
		Units: []identity.Object{w.Unit}, Holders: nil, PlanLock: false, BreakGlass: "", Fence: fence,
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

// evaluateStateWrite evaluates the gates of state.write for each unit of c
// and writes the decision record.
func (s *Service) evaluateStateWrite(ctx context.Context, c Change) (Decision, error) {
	if c.BreakGlass != "" {
		return Decision{}, fmt.Errorf("%w: a break-glass for a gate in error is not implemented in this version", ErrUnavailable)
	}

	// Defense in depth: the store checks the fence of the record too. This
	// check stays so that no record is built with the fence of another
	// namespace.
	if !sameNamespaceFence(c.Fence, c.Namespace) {
		return Decision{}, fmt.Errorf("%w: the fence is not the fence of namespace %q", ErrInvalid, c.Namespace)
	}

	units, scopes, err := s.stateWriteUnits(ctx, c)
	if err != nil {
		return Decision{}, err
	}

	cfg, err := s.store.Snapshot(ctx, scopes)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: gate configuration: %w", ErrUnavailable, err)
	}

	gates := slices.DeleteFunc(slices.Clone(cfg.Gates), func(g Gate) bool { return g.DecisionPoint != PointStateWrite })

	invalid, engineErr := s.recheck(ctx, gates)

	var answers []Answer
	if engineErr == nil {
		answers, engineErr = s.askEngine(ctx, c, units, gates)
	}

	rec := Record{
		ID: NewRecordID(), Point: PointStateWrite, Change: ChangeRef{Kind: ChangeStateWrite, ID: ""},
		Namespace: c.Namespace, At: s.now(), Subject: c.Subject.ID, Schema: stateWriteSchema, PathSets: nil,
		Units: make([]UnitResult, 0, len(units)), Requirements: nil, Warnings: nil, AutoApply: false,
		Controls: nil, BreakGlass: "", Result: ResultAllow, Action: "", Count: 1, LastAt: time.Time{},
	}
	rec.Change.ID = string(rec.ID)
	rec.LastAt = rec.At

	out := outcome{}

	for i := range units {
		var answer *Answer
		if engineErr == nil && answers != nil {
			answer = &answers[i]
		}

		res := s.unitResult(&units[i], gates, &cfg, answer, engineErr, invalid, &out)
		rec.Units = append(rec.Units, res)
	}

	rec.Result = out.result()
	rec.Requirements = out.requirements
	rec.Warnings = out.warnings

	if err := s.store.Records().Put(ctx, rec, c.Fence); err != nil {
		return Decision{}, fmt.Errorf("%w: write the decision record: %w", ErrUnavailable, err)
	}

	return out.decision(&rec, engineErr)
}

// stateWriteUnits builds the facts of each unit of c, and the scopes whose
// configuration holds for the change.
func (s *Service) stateWriteUnits(ctx context.Context, c Change) ([]unitEval, []Scope, error) {
	units := make([]unitEval, 0, len(c.Units))

	var scopes []Scope

	add := func(sc Scope) {
		if !slices.Contains(scopes, sc) {
			scopes = append(scopes, sc)
		}
	}

	for _, u := range c.Units {
		coll, err := s.collectionOf(ctx, u)
		if err != nil {
			return nil, nil, err
		}

		chain, err := UnitChain(u, coll)
		if err != nil {
			return nil, nil, err
		}

		labels, err := s.labelsOf(ctx, u)
		if err != nil {
			return nil, nil, err
		}

		facts, doc, err := stateWriteFacts(c, u, coll, labels)
		if err != nil {
			return nil, nil, err
		}

		held := slices.Concat(chain, c.Holders)
		for _, sc := range held {
			add(sc)
		}

		units = append(units, unitEval{unit: u, scopes: held, facts: facts, doc: doc})
	}

	return units, scopes, nil
}

// stateWriteFacts builds the fact document of one unit at state.write. The
// plan facts and the state facts are not available: the write comes from a
// CLI that sends no plan.
func stateWriteFacts(c Change, u identity.Object, coll collection.Name, labels []string) (Facts, map[string]any, error) {
	source := c.Source
	if source == "" {
		source = SourceAPI
	}

	f := Facts{
		Schema:    stateWriteSchema,
		Principal: PrincipalFacts{Kind: c.Subject.Kind, Credential: c.Subject.Credential},
		Source:    SourceFacts{Kind: source, Ref: "", Protected: false, Commit: "", UpstreamUnit: ""},
		Unit:      UnitFacts{Tool: u.Tool(), Collection: coll.String(), Labels: labels},
		Operation: OperationFacts{Kind: c.Operation},
		Document:  DocumentFacts{Truncated: nil},
	}

	raw, err := json.Marshal(f)
	if err != nil {
		return Facts{}, nil, fmt.Errorf("%w: fact document: %w", ErrInvalid, err)
	}

	if len(raw) > MaxFactDocumentBytes {
		f.Unit.Labels = nil
		f.Document.Truncated = []string{"unit.labels"}

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
	current := CheckedWith{Engine: s.engine.Version(), Schema: stateWriteSchema}
	invalid := map[PolicyID]string{}

	for i := range gates {
		g := &gates[i]
		g.State = StateValid

		if g.CheckedWith == current {
			continue
		}

		diags, err := s.engine.Check(ctx, Schema{Version: stateWriteSchema, PathSets: nil}, []Policy{gatePolicy(g)})
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
// asks nothing when no gate exists.
func (s *Service) askEngine(ctx context.Context, c Change, units []unitEval, gates []Gate) ([]Answer, error) {
	policies := make([]Policy, 0, len(gates))

	for i := range gates {
		g := &gates[i]
		if g.State != StateValid {
			continue
		}

		policies = append(policies, gatePolicy(g))
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

	set := PolicySet{Schema: Schema{Version: stateWriteSchema, PathSets: nil}, Policies: policies}

	answers, err := s.engine.Evaluate(ctx, set, requests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	if len(answers) != len(requests) {
		return nil, fmt.Errorf("%w: the engine gave %d answers for %d requests", ErrUnavailable, len(answers), len(requests))
	}

	for i := range answers {
		if answers[i].RequestID != requests[i].ID {
			return nil, fmt.Errorf("%w: the engine answer %d does not echo its request", ErrUnavailable, i)
		}
	}

	return answers, nil
}

// outcome collects the combined result of the units of one evaluation.
type outcome struct {
	denies       []string
	errors       []string
	approvals    []string
	requirements []Requirement
	warnings     []string
	unavailable  bool
}

// result returns the combined result: the strictest of the units.
func (o *outcome) result() Result {
	switch {
	case len(o.denies) > 0:
		return ResultDeny
	case o.unavailable || len(o.errors) > 0:
		return ResultError
	case len(o.approvals) > 0:
		return ResultAwaitingApproval
	case len(o.warnings) > 0:
		return ResultWarn
	default:
		return ResultAllow
	}
}

// decision returns the decision of the evaluation, or the refusal.
func (o *outcome) decision(rec *Record, engineErr error) (Decision, error) {
	switch {
	case len(o.denies) > 0:
		return Decision{}, &RefusalError{Err: ErrDenied, Record: rec.ID, Messages: o.denies}
	case o.unavailable:
		return Decision{}, &RefusalError{Err: ErrUnavailable, Record: rec.ID, Messages: []string{cleanText(engineErr.Error())}}
	case len(o.errors) > 0:
		return Decision{}, &RefusalError{Err: ErrRuleFailed, Record: rec.ID, Messages: o.errors}
	case len(o.approvals) > 0:
		// A CLI cannot wait for people, so an approval outcome refuses.
		return Decision{}, &RefusalError{Err: ErrDenied, Record: rec.ID, Messages: o.approvals}
	}

	return Decision{
		Record: rec.ID, Result: rec.Result, Requirements: nil, Warnings: o.warnings, AutoApply: false,
	}, nil
}

// unitResult evaluates the gates for one unit from its answer and adds the
// effect to out.
func (s *Service) unitResult(
	u *unitEval, gates []Gate, cfg *Config, answer *Answer, engineErr error, invalid map[PolicyID]string,
	out *outcome,
) UnitResult {
	digest, err := digestOf(u.doc)
	if err != nil {
		digest = ""
	}

	res := UnitResult{
		Unit: UnitRef{Object: u.unit}, Facts: u.facts, Digest: digest, Gates: []PolicyResult{}, Result: ResultAllow,
	}

	var (
		denies, approvals, warns, errs []string
	)

	for i := range gates {
		g := &gates[i]
		if !slices.Contains(u.scopes, g.Scope) {
			continue
		}

		pr := PolicyResult{
			Policy: g.PolicyID(), Version: g.Version, Mode: g.Mode, Outcome: g.Outcome, Matched: false,
			Error: false, Diagnostic: "", Skipped: false, BrokenBy: "", Message: "",
		}

		s.fillGateResult(&pr, g, u, answer, engineErr, invalid)

		if g.Mode != ModeEnforce {
			res.Gates = append(res.Gates, pr)

			continue
		}

		switch {
		case pr.Error && g.OnError == OnErrorSkip:
			pr.Skipped = true
		case pr.Error:
			errs = append(errs, "the gate "+pr.Policy.String()+" failed: "+pr.Diagnostic)
		case pr.Matched:
			s.applyOutcome(&pr, u, cfg, &denies, &approvals, &warns, &errs, out)
		}

		res.Gates = append(res.Gates, pr)
	}

	out.denies = append(out.denies, denies...)
	out.errors = append(out.errors, errs...)
	out.approvals = append(out.approvals, approvals...)
	out.warnings = append(out.warnings, warns...)

	if engineErr != nil && hasEnforcedGate(gates) {
		out.unavailable = true
		res.Result = ResultError

		return res
	}

	switch {
	case len(denies) > 0:
		res.Result = ResultDeny
	case len(errs) > 0:
		res.Result = ResultError
	case len(approvals) > 0:
		res.Result = ResultAwaitingApproval
	case len(warns) > 0:
		res.Result = ResultWarn
	}

	return res
}

// hasEnforcedGate reports whether a gate in enforce mode exists. An engine
// that does not answer matters only then.
func hasEnforcedGate(gates []Gate) bool {
	return slices.ContainsFunc(gates, func(g Gate) bool { return g.Mode == ModeEnforce && g.State == StateValid })
}

// fillGateResult sets the match, the error and the message of one gate for
// one unit.
func (s *Service) fillGateResult(
	pr *PolicyResult, g *Gate, u *unitEval, answer *Answer, engineErr error, invalid map[PolicyID]string,
) {
	switch {
	case g.State != StateValid:
		pr.Error, pr.Diagnostic = true, firstNonEmpty(invalid[pr.Policy], "the gate failed its last check and is not valid")
	case engineErr != nil:
		pr.Error, pr.Diagnostic = true, cleanText(engineErr.Error())
	case answer != nil:
		for _, d := range answer.Diagnostics {
			if d.Policy == pr.Policy {
				pr.Error, pr.Diagnostic = true, cleanText(d.Message)
			}
		}

		if !pr.Error && slices.Contains(answer.Matched, pr.Policy) {
			pr.Matched = true
		}
	}

	if !pr.Matched {
		return
	}

	msg, err := renderMessage(g.Message, u.doc)
	if err != nil {
		pr.Matched, pr.Error, pr.Diagnostic = false, true, cleanText(err.Error())

		return
	}

	pr.Message = cleanText(msg)
}

// applyOutcome adds the effect of the matched gate pr to the lists of the
// unit.
func (s *Service) applyOutcome(
	pr *PolicyResult, u *unitEval, cfg *Config, denies, approvals, warns, errs *[]string, out *outcome,
) {
	switch {
	case pr.Outcome == OutcomeDeny:
		*denies = append(*denies, firstNonEmpty(pr.Message, "the gate "+pr.Policy.String()+" denies the write"))
	case pr.Outcome == OutcomeWarn:
		*warns = append(*warns, firstNonEmpty(pr.Message, "the gate "+pr.Policy.String()+" warns about the write"))
	default:
		id, ok := pr.Outcome.Rule()
		rule, found := findRule(cfg, u, id)

		// Defense in depth: the store refuses a gate whose approval rule
		// does not resolve. This check stays so that a row from another
		// version, or a restored row, still fails closed.

		if !ok || !found {
			pr.Error, pr.Matched = true, false
			pr.Diagnostic = "the approval rule " + string(pr.Outcome) + " does not exist for the unit"
			*errs = append(*errs, "the gate "+pr.Policy.String()+" failed: "+pr.Diagnostic)

			return
		}

		req := requirementOfRule(rule)
		if !slices.ContainsFunc(out.requirements, func(r Requirement) bool { return r.Name == req.Name }) {
			out.requirements = append(out.requirements, req)
		}

		*approvals = append(*approvals, fmt.Sprintf(
			"%s The write needs approval by rule %s. A write from a CLI cannot wait for approval: "+
				"open a change order for %s with the OpenDustpan CLI, and write after it is approved.",
			firstNonEmpty(pr.Message, "The gate "+pr.Policy.String()+" matches."), id, u.unit,
		))
	}
}

// findRule returns the approval rule id of the scopes of the unit.
func findRule(cfg *Config, u *unitEval, id ID) (ApprovalRule, bool) {
	for i := range cfg.ApprovalRules {
		r := cfg.ApprovalRules[i]
		if r.ID == id && slices.Contains(u.scopes, r.Scope) {
			return r, true
		}
	}

	return ApprovalRule{}, false
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

	for _, part := range strings.Split(path, ".") {
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
