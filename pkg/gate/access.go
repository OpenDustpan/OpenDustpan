package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// unknownAuthAgeSeconds is the authentication age of a subject whose time of
// authentication is not known. It is 100 years, so a limit that asks for a
// recent authentication forbids the call: an unknown age is never recent.
const unknownAuthAgeSeconds = 100 * 365 * 24 * 3600

// accessSchema returns the version of the access schema that this package
// evaluates.
func accessSchema() SchemaVersion {
	return SchemaVersion{Subject: SchemaAccess, Major: 1, Minor: 0}
}

// limitCheck is one evaluation of the access limits of one call.
type limitCheck struct {
	subject Subject
	call    AccessCall
	facts   AccessFacts
	limits  []AccessLimit
	answer  Answer
}

// limitVerdict is what the answer of the engine says about the limits.
type limitVerdict struct {
	results   []PolicyResult
	messages  []string
	forbidden bool
	failed    bool
}

// checkLimits evaluates the access limits of the scopes of the unit of
// call, after the grants allowed it. A call on an object that is not a unit
// is not implemented yet, and returns an error that wraps ErrUnavailable.
func (s *Service) checkLimits(ctx context.Context, subject Subject, call AccessCall) error {
	if !call.Object.IsUnit() {
		return errNotImplemented
	}

	coll, err := s.collectionOf(ctx, call.Object)
	if err != nil {
		return fmt.Errorf("access limits of %s: %w", call.Action, err)
	}

	chain, err := UnitChain(call.Object, coll)
	if err != nil {
		return fmt.Errorf("access limits of %s: %w", call.Action, err)
	}

	cfg, err := s.snapshot(ctx, chain)
	if err != nil {
		return fmt.Errorf("access limits of %s: %w: %w", call.Action, ErrUnavailable, err)
	}

	if len(cfg.AccessLimits) == 0 {
		return nil
	}

	labels, err := s.labelsOf(ctx, call.Object)
	if err != nil {
		return fmt.Errorf("access limits of %s: %w", call.Action, err)
	}

	lc := limitCheck{
		subject: subject,
		call:    call,
		limits:  cfg.AccessLimits,
		answer:  Answer{RequestID: "", Matched: nil, Diagnostics: nil},
		facts: AccessFacts{
			Schema:         accessSchema(),
			Principal:      PrincipalFacts{Kind: subject.Kind, Credential: subject.Credential},
			AuthAgeSeconds: s.authAge(subject),
			Unit:           AccessUnitFacts{Collection: coll.String(), Labels: labels},
		},
	}

	if askErr := s.askLimits(ctx, &lc); askErr != nil {
		return askErr
	}

	return s.refuseLimits(ctx, &lc)
}

// askLimits asks the engine about the limits of lc and keeps the answer.
func (s *Service) askLimits(ctx context.Context, lc *limitCheck) error {
	policies := make([]Policy, 0, len(lc.limits))
	for _, l := range lc.limits {
		policies = append(policies, Policy{
			ID: l.PolicyID(), Version: l.Version, Kind: PolicyLimit, Point: "", Condition: "", Unless: "",
			Text: l.Policy,
		})
	}

	set := PolicySet{Schema: Schema{Version: accessSchema(), PathSets: nil}, Policies: policies}
	req := Request{
		ID: lc.call.Object.String(), Principal: lc.subject.ID, Groups: lc.subject.Groups,
		Action: string(lc.call.Action), Resource: lc.call.Object, Facts: nil, Access: &lc.facts,
	}

	answers, err := s.engine.Evaluate(ctx, set, []Request{req})
	if err != nil {
		return fmt.Errorf("access limits of %s: %w: %w", lc.call.Action, ErrUnavailable, err)
	}

	if len(answers) != 1 || answers[0].RequestID != req.ID {
		return fmt.Errorf(
			"access limits of %s: %w: the engine answer does not echo the request", lc.call.Action, ErrUnavailable,
		)
	}

	lc.answer = answers[0]

	return nil
}

// verdict reads the answer of the engine for each limit.
func (lc *limitCheck) verdict() limitVerdict {
	v := limitVerdict{results: make([]PolicyResult, 0, len(lc.limits)), messages: nil, forbidden: false, failed: false}

	for _, l := range lc.limits {
		pr := PolicyResult{
			Policy: l.PolicyID(), Version: l.Version, Mode: ModeEnforce, Outcome: OutcomeDeny, Matched: false,
			Error: false, Diagnostic: "", Skipped: false, BrokenBy: "", Message: "",
		}

		for _, d := range lc.answer.Diagnostics {
			if d.Policy == pr.Policy {
				pr.Error, pr.Diagnostic, v.failed = true, cleanText(d.Message), true
			}
		}

		if slices.Contains(lc.answer.Matched, pr.Policy) {
			pr.Matched, v.forbidden = true, true
			pr.Message = fmt.Sprintf("the access limit %s forbids %s", pr.Policy, lc.call.Action)
		}

		switch {
		case pr.Matched:
			v.messages = append(v.messages, pr.Message)
		case pr.Error:
			v.messages = append(v.messages, "the access limit "+pr.Policy.String()+" gave an error")
		default:
		}

		v.results = append(v.results, pr)
	}

	return v
}

// refuseLimits turns the answer of the engine into the refusal of a call. It
// returns nil when no limit matched and none gave an error.
func (s *Service) refuseLimits(ctx context.Context, lc *limitCheck) error {
	v := lc.verdict()
	if !v.forbidden && !v.failed {
		return nil
	}

	cause, result := ErrLimited, ResultDeny
	if !v.forbidden {
		cause, result = ErrRuleFailed, ResultError
	}

	id, err := s.noteLimitRefusal(ctx, lc, &v, result)
	if err != nil {
		// Fail closed: a refusal that cannot be recorded still refuses.
		s.log.ErrorContext(ctx, "write the access refusal record", slog.String("error", err.Error()))
	}

	return &RefusalError{Err: cause, Record: id, Messages: v.messages}
}

// noteLimitRefusal writes the decision record of a refused call. Refusals of
// one subject, action and limit within AccessRefusalWindow share a record.
func (s *Service) noteLimitRefusal(
	ctx context.Context,
	lc *limitCheck,
	v *limitVerdict,
	result Result,
) (RecordID, error) {
	digest, err := digestOf(lc.facts)
	if err != nil {
		return "", err
	}

	var empty Facts

	empty.Schema = accessSchema()

	at := s.now()
	rec := Record{
		ID: NewRecordID(), Point: "", Change: ChangeRef{Kind: ChangeAccess, ID: ""},
		Namespace: lc.call.Object.Namespace(), At: at, Subject: lc.subject.ID, Schema: accessSchema(), PathSets: nil,
		Units: []UnitResult{{
			Unit: UnitRef{Object: lc.call.Object}, Facts: empty, Digest: digest, Gates: v.results, Result: result,
		}},
		Requirements: nil, Warnings: nil, AutoApply: false, Controls: nil, BreakGlass: "", Result: result,
		Action: string(lc.call.Action), Count: 1, LastAt: at,
	}

	fence, err := s.fences.Fence(ctx, rec.Namespace)
	if err != nil {
		return "", fmt.Errorf("fence of %s: %w", rec.Namespace, err)
	}

	return s.store.Records().NoteRefusal(ctx, rec, AccessRefusalWindow, fence)
}

// authAge returns the authentication age of subject in whole seconds.
func (s *Service) authAge(subject Subject) int64 {
	if subject.AuthAt.IsZero() {
		return unknownAuthAgeSeconds
	}

	return max(int64(s.now().Sub(subject.AuthAt).Seconds()), 0)
}

// factMemoKey is the context key of the factMemo of one call.
type factMemoKey struct{}

// factMemo keeps the unit facts and the gate configuration that one call
// read, so a call that checks the limits and then the gates reads each one
// once, and both checks see the same rows. It lives for one call only, so it
// never serves a stale value to a later call. It keeps no failed read: a
// store error refuses the call, and nothing reads the value again after it.
type factMemo struct {
	mu    sync.Mutex
	units map[string]unitRead
	// configs holds the configuration of each list of scopes that the call
	// read. A call reads one or two lists, so a slice is enough.
	configs []configRead
}

// unitRead is one read of the facts of a unit. known is false for a unit
// that the source does not know.
type unitRead struct {
	info  UnitInfo
	known bool
}

// configRead is one read of the configuration of a list of scopes. The list
// compares by value, so the stable ID of a unit scope is part of the match.
type configRead struct {
	scopes []Scope
	cfg    Config
}

// withFactMemo returns ctx with a new factMemo.
func withFactMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, factMemoKey{}, &factMemo{
		mu: sync.Mutex{}, units: map[string]unitRead{}, configs: nil,
	})
}

// memoOf returns the factMemo of ctx, or nil.
func memoOf(ctx context.Context) *factMemo {
	m, ok := ctx.Value(factMemoKey{}).(*factMemo)
	if !ok {
		return nil
	}

	return m
}

// snapshot returns the configuration of scopes from the memo or from the
// store. It returns the error of the store as it is. Each caller gets its own
// copy of the lists, so a caller that changes a list does not change the
// configuration of the other check.
func (s *Service) snapshot(ctx context.Context, scopes []Scope) (Config, error) {
	m := memoOf(ctx)
	if m != nil {
		m.mu.Lock()

		for i := range m.configs {
			if slices.Equal(m.configs[i].scopes, scopes) {
				cfg := cloneConfig(m.configs[i].cfg)
				m.mu.Unlock()

				return cfg, nil
			}
		}

		m.mu.Unlock()
	}

	cfg, err := s.store.Snapshot(ctx, scopes)
	if err != nil {
		return Config{}, err
	}

	if m != nil {
		m.mu.Lock()
		m.configs = append(m.configs, configRead{scopes: slices.Clone(scopes), cfg: cloneConfig(cfg)})
		m.mu.Unlock()
	}

	return cfg, nil
}

// cloneConfig returns a copy of cfg with lists of its own.
func cloneConfig(cfg Config) Config {
	return Config{
		Gates: slices.Clone(cfg.Gates), AccessLimits: slices.Clone(cfg.AccessLimits),
		ApprovalRules: slices.Clone(cfg.ApprovalRules), OwnershipRules: slices.Clone(cfg.OwnershipRules),
		PathSets: slices.Clone(cfg.PathSets), ApprovalDefaults: slices.Clone(cfg.ApprovalDefaults),
		CarryOver: slices.Clone(cfg.CarryOver),
	}
}

// unitOf reads the facts of unit from the memo or from the source. It returns
// known false for a unit that the source does not know, and the error of the
// source wrapped with ErrUnavailable. A unit that the source does not know
// stays unknown for the call, so a lenient read of it never hides it from a
// strict read in the same call.
func (s *Service) unitOf(ctx context.Context, unit identity.Object) (unitRead, error) {
	m := memoOf(ctx)
	if m != nil {
		m.mu.Lock()
		read, ok := m.units[unit.String()]
		m.mu.Unlock()

		if ok {
			return cloneUnitRead(read), nil
		}
	}

	info, err := s.units.Unit(ctx, unit)

	var read unitRead

	switch {
	case errors.Is(err, ErrNotFound):
		read = unitRead{info: UnitInfo{Collection: "", Labels: []string{}}, known: false}
	case err != nil:
		return unitRead{}, fmt.Errorf("%w: facts of %s: %w", ErrUnavailable, unit, err)
	default:
		if info.Labels == nil {
			info.Labels = []string{}
		}

		read = unitRead{info: info, known: true}
	}

	if m != nil {
		m.mu.Lock()
		m.units[unit.String()] = cloneUnitRead(read)
		m.mu.Unlock()
	}

	return read, nil
}

// cloneUnitRead returns a copy of r with a label list of its own.
func cloneUnitRead(r unitRead) unitRead {
	r.info.Labels = slices.Clone(r.info.Labels)

	return r
}

// collectionOf returns the collection of unit, or the empty name for a unit
// in no collection and for a unit that the source does not know.
func (s *Service) collectionOf(ctx context.Context, unit identity.Object) (collection.Name, error) {
	read, err := s.unitOf(ctx, unit)
	if err != nil {
		return "", fmt.Errorf("collection of %s: %w", unit, err)
	}

	return read.info.Collection, nil
}

// labelsOf returns the labels of unit, or none for an unknown unit.
func (s *Service) labelsOf(ctx context.Context, unit identity.Object) ([]string, error) {
	read, err := s.unitOf(ctx, unit)
	if err != nil {
		return nil, fmt.Errorf("labels of %s: %w", unit, err)
	}

	return read.info.Labels, nil
}

// labelsOfKnown returns the labels of a unit that must exist. A unit that the
// source does not know fails closed with ErrUnavailable, and never reads as a
// unit with no labels.
func (s *Service) labelsOfKnown(ctx context.Context, unit identity.Object) ([]string, error) {
	read, err := s.unitOf(ctx, unit)
	if err != nil {
		return nil, fmt.Errorf("labels of %s: %w", unit, err)
	}

	if !read.known {
		return nil, fmt.Errorf("%w: labels of %s: %w", ErrUnavailable, unit, ErrNotFound)
	}

	return read.info.Labels, nil
}

// digestOf returns the SHA-256 of the JSON form of doc, in lowercase hex.
func digestOf(doc any) (string, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("%w: fact document: %w", ErrInvalid, err)
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:]), nil
}

// cleanText removes the line breaks and the other control characters of s,
// so that a text from a gate or the engine is safe in a header, a log line
// and a one-line error.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}

		return r
	}, s)
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// sameNamespaceFence reports whether f belongs to ns. A record is written
// only with the fence of its own namespace.
func sameNamespaceFence(f namespace.Fence, ns namespace.Name) bool {
	return f.Namespace == ns
}
