package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// unknownAuthAgeSeconds is the authentication age of a subject whose time of
// authentication is not known. It is 100 years, so a limit that asks for a
// recent authentication forbids the call: an unknown age is never recent.
const unknownAuthAgeSeconds = 100 * 365 * 24 * 3600

// accessSchema is the version of the access schema that this package
// evaluates.
var accessSchema = SchemaVersion{Subject: SchemaAccess, Major: 1, Minor: 0}

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

	facts := AccessFacts{
		Schema:         accessSchema,
		Principal:      PrincipalFacts{Kind: subject.Kind, Credential: subject.Credential},
		AuthAgeSeconds: s.authAge(subject),
		Unit:           AccessUnitFacts{Collection: coll.String(), Labels: labels},
	}

	policies := make([]Policy, 0, len(cfg.AccessLimits))
	for _, l := range cfg.AccessLimits {
		policies = append(policies, Policy{
			ID: l.PolicyID(), Version: l.Version, Kind: PolicyLimit, Point: "", Condition: "", Unless: "",
			Text: l.Policy,
		})
	}

	set := PolicySet{Schema: Schema{Version: accessSchema, PathSets: nil}, Policies: policies}
	req := Request{
		ID: call.Object.String(), Principal: subject.ID, Groups: subject.Groups, Action: string(call.Action),
		Resource: call.Object, Facts: nil, Access: &facts,
	}

	answers, err := s.engine.Evaluate(ctx, set, []Request{req})
	if err != nil {
		return fmt.Errorf("access limits of %s: %w: %w", call.Action, ErrUnavailable, err)
	}

	if len(answers) != 1 || answers[0].RequestID != req.ID {
		return fmt.Errorf("access limits of %s: %w: the engine answer does not echo the request", call.Action, ErrUnavailable)
	}

	return s.refuseLimits(ctx, subject, call, &facts, cfg.AccessLimits, &answers[0])
}

// refuseLimits turns the answer of the engine into the refusal of a call. It
// returns nil when no limit matched and none gave an error.
func (s *Service) refuseLimits(
	ctx context.Context,
	subject Subject,
	call AccessCall,
	facts *AccessFacts,
	limits []AccessLimit,
	answer *Answer,
) error {
	results := make([]PolicyResult, 0, len(limits))
	failed, forbidden := false, false

	var messages []string

	for _, l := range limits {
		pr := PolicyResult{
			Policy: l.PolicyID(), Version: l.Version, Mode: ModeEnforce, Outcome: OutcomeDeny, Matched: false,
			Error: false, Diagnostic: "", Skipped: false, BrokenBy: "", Message: "",
		}

		for _, d := range answer.Diagnostics {
			if d.Policy == pr.Policy {
				pr.Error, pr.Diagnostic = true, cleanText(d.Message)
				failed = true
			}
		}

		for _, m := range answer.Matched {
			if m == pr.Policy {
				pr.Matched = true
				forbidden = true
				pr.Message = fmt.Sprintf("the access limit %s forbids %s", pr.Policy, call.Action)
			}
		}

		if pr.Matched || pr.Error {
			messages = append(messages, firstNonEmpty(pr.Message, "the access limit "+pr.Policy.String()+" gave an error"))
		}

		results = append(results, pr)
	}

	if !failed && !forbidden {
		return nil
	}

	cause, result := ErrLimited, ResultDeny
	if !forbidden {
		cause, result = ErrRuleFailed, ResultError
	}

	digest, err := digestOf(facts)
	if err != nil {
		return err
	}

	at := s.now()
	rec := Record{
		ID: NewRecordID(), Point: "", Change: ChangeRef{Kind: ChangeAccess, ID: ""}, Namespace: call.Object.Namespace(),
		At: at, Subject: subject.ID, Schema: accessSchema, PathSets: nil,
		Units: []UnitResult{{
			Unit: UnitRef{Object: call.Object}, Facts: Facts{Schema: accessSchema}, Digest: digest, Gates: results,
			Result: result,
		}},
		Requirements: nil, Warnings: nil, AutoApply: false, Controls: nil, BreakGlass: "", Result: result,
		Action: string(call.Action), Count: 1, LastAt: at,
	}

	fence, err := s.fences.Fence(ctx, rec.Namespace)
	if err != nil {
		return fmt.Errorf("access limits of %s: fence: %w", call.Action, err)
	}

	id, err := s.store.Records().NoteRefusal(ctx, rec, AccessRefusalWindow, fence)
	if err != nil {
		// Fail closed: a refusal that cannot be recorded still refuses.
		s.log.ErrorContext(ctx, "write the access refusal record", "error", err)

		return &RefusalError{Err: cause, Record: "", Messages: messages}
	}

	return &RefusalError{Err: cause, Record: id, Messages: messages}
}

// authAge returns the authentication age of subject in whole seconds.
func (s *Service) authAge(subject Subject) int64 {
	if subject.AuthAt.IsZero() {
		return unknownAuthAgeSeconds
	}

	age := int64(s.now().Sub(subject.AuthAt).Seconds())

	return max(age, 0)
}

// collectionOf returns the collection of unit, or the empty name for a unit
// in no collection.
func (s *Service) collectionOf(ctx context.Context, unit identity.Object) (collection.Name, error) {
	name, err := s.members.Collection(ctx, unit)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("%w: collection of %s: %w", ErrUnavailable, unit, err)
	}

	return name, nil
}

// labelsOf returns the labels of unit, or none for an unknown unit.
func (s *Service) labelsOf(ctx context.Context, unit identity.Object) ([]string, error) {
	labels, err := s.units.Labels(ctx, unit)
	if errors.Is(err, ErrNotFound) {
		return []string{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%w: labels of %s: %w", ErrUnavailable, unit, err)
	}

	if labels == nil {
		return []string{}, nil
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

// sameNamespaceFence reports whether fence belongs to ns. A record is
// written only with the fence of its own namespace.
func sameNamespaceFence(f namespace.Fence, ns namespace.Name) bool {
	return f.Namespace == ns
}
