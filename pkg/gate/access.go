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

	cfg, err := s.store.Snapshot(ctx, chain)
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

// factMemo keeps the collection and the labels of the units that one call
// read, so a call that checks the limits and then the gates reads each one
// once. It lives for one call only, so it never serves a stale value to a
// later call.
type factMemo struct {
	mu          sync.Mutex
	collections map[string]collection.Name
	labels      map[string][]string
}

// withFactMemo returns ctx with a new factMemo.
func withFactMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, factMemoKey{}, &factMemo{
		mu: sync.Mutex{}, collections: map[string]collection.Name{}, labels: map[string][]string{},
	})
}

// memoOf returns the factMemo of ctx, or nil.
func memoOf(ctx context.Context) *factMemo {
	m, _ := ctx.Value(factMemoKey{}).(*factMemo)

	return m
}

// collectionOf returns the collection of unit, or the empty name for a unit
// in no collection.
func (s *Service) collectionOf(ctx context.Context, unit identity.Object) (collection.Name, error) {
	if m := memoOf(ctx); m != nil {
		m.mu.Lock()
		name, ok := m.collections[unit.String()]
		m.mu.Unlock()

		if ok {
			return name, nil
		}
	}

	name, err := s.members.Collection(ctx, unit)
	if errors.Is(err, ErrNotFound) {
		name, err = "", nil
	}

	if err != nil {
		return "", fmt.Errorf("%w: collection of %s: %w", ErrUnavailable, unit, err)
	}

	if m := memoOf(ctx); m != nil {
		m.mu.Lock()
		m.collections[unit.String()] = name
		m.mu.Unlock()
	}

	return name, nil
}

// labelsOf returns the labels of unit, or none for an unknown unit.
func (s *Service) labelsOf(ctx context.Context, unit identity.Object) ([]string, error) {
	if m := memoOf(ctx); m != nil {
		m.mu.Lock()
		labels, ok := m.labels[unit.String()]
		m.mu.Unlock()

		if ok {
			return labels, nil
		}
	}

	labels, err := s.units.Labels(ctx, unit)
	if errors.Is(err, ErrNotFound) {
		labels, err = nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%w: labels of %s: %w", ErrUnavailable, unit, err)
	}

	if labels == nil {
		labels = []string{}
	}

	if m := memoOf(ctx); m != nil {
		m.mu.Lock()
		m.labels[unit.String()] = labels
		m.mu.Unlock()
	}

	return labels, nil
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
