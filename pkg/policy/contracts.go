package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// OPACall is the body that the OPA adapter sends to the Data API of an OPA
// server: POST <url>/v1/data/<path>.
type OPACall struct {
	Input json.RawMessage `json:"input"`
}

// ParseOPAAnswer reads the answer of the Data API of an OPA server. The
// policy at the path must give a decision as its result:
// {"result": {"effect": ..., "reasons": [...], "approvals": {...}}}. A
// missing result is an undefined decision, which usually means a wrong path.
// ParseOPAAnswer returns ErrInvalidDecision for it, never a deny. It also
// returns ErrInvalidDecision for an answer over MaxAnswerBytes, an unknown
// field and a decision that Decision.Validate refuses. revision is the
// revision of the bundle, or empty. It becomes the PolicyVersion.
func ParseOPAAnswer(body []byte, revision string) (Decision, error) {
	var answer struct {
		Result *Decision `json:"result"`
	}

	if err := decodeAnswer(body, &answer); err != nil {
		return Decision{}, err
	}

	if answer.Result == nil {
		return Decision{}, fmt.Errorf("%w: the result is undefined, check the path of the policy", ErrInvalidDecision)
	}

	d := *answer.Result
	d.Engine = "opa"
	d.PolicyVersion = revision

	if err := d.Validate(); err != nil {
		return Decision{}, err
	}

	return d, nil
}

// ParseWebhookAnswer reads the answer of a webhook: the JSON of a Decision.
// It returns ErrInvalidDecision as ParseOPAAnswer does.
func ParseWebhookAnswer(body []byte) (Decision, error) {
	var d Decision

	if err := decodeAnswer(body, &d); err != nil {
		return Decision{}, err
	}

	d.Engine = "webhook"

	if err := d.Validate(); err != nil {
		return Decision{}, err
	}

	return d, nil
}

// decodeAnswer decodes the answer of an engine into v. Its errors never
// hold a part of body: an answer can echo the input document, or hold text
// that the engine did not mean for the log. They name the size, or the
// offset of a syntax error, only.
func decodeAnswer(body []byte, v any) error {
	if len(body) > MaxAnswerBytes {
		return fmt.Errorf("%w: %d bytes, the limit is %d", ErrInvalidDecision, len(body), MaxAnswerBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		if syntax, ok := errors.AsType[*json.SyntaxError](err); ok {
			return fmt.Errorf("%w: the answer is not JSON, at byte %d", ErrInvalidDecision, syntax.Offset)
		}

		return fmt.Errorf("%w: the answer is not of the shape of a decision", ErrInvalidDecision)
	}

	if dec.More() {
		return fmt.Errorf("%w: data after the decision", ErrInvalidDecision)
	}

	return nil
}

// CedarEntity is the type and the ID of a Cedar entity, for example
// OpenDustpan::Principal and "alice".
type CedarEntity struct {
	Type string
	ID   string
}

// The Cedar entity types of the Cedar adapter.
const (
	CedarPrincipal = "OpenDustpan::Principal"
	CedarGroup     = "OpenDustpan::Group"
	CedarAction    = "OpenDustpan::Action"
	CedarBlueprint = "OpenDustpan::Blueprint"
	CedarUnit      = "OpenDustpan::Unit"
)

// CedarWithoutApproval is the suffix of the second action that the Cedar
// adapter asks: allow on it lets a change go in with no approval.
const CedarWithoutApproval = ".without-approval"

// CedarRequest is one authorization request of the Cedar adapter. Parents
// are the groups of the principal. Context is the input document.
type CedarRequest struct {
	Principal CedarEntity
	Action    CedarEntity
	Resource  CedarEntity
	Parents   []CedarEntity
	Context   []byte
}

// CedarRequests returns the two requests that the Cedar adapter asks for
// one input: the action of kind, then the same action with the suffix
// CedarWithoutApproval. Cedar gives only allow or deny, so the second
// request tells allow from approve (see CedarDecision).
func CedarRequests(subject string, groups []string, kind Kind, resource CedarEntity, document []byte) [2]CedarRequest {
	parents := make([]CedarEntity, 0, len(groups))
	for _, g := range groups {
		parents = append(parents, CedarEntity{Type: CedarGroup, ID: g})
	}

	first := CedarRequest{
		Principal: CedarEntity{Type: CedarPrincipal, ID: subject},
		Action:    CedarEntity{Type: CedarAction, ID: string(kind)},
		Resource:  resource,
		Parents:   parents,
		Context:   document,
	}
	second := first
	second.Action.ID = string(kind) + CedarWithoutApproval

	return [2]CedarRequest{first, second}
}

// CedarDecision joins the answers of the two requests of CedarRequests.
// Deny on the first gives EffectDeny. Allow on the first and deny on the
// second gives EffectApprove with approvals, which come from a setting of
// the namespace. Allow on both gives EffectAllow. policies are the IDs of
// the Cedar policies that decided, which become the reasons.
func CedarDecision(first, second bool, approvals Approvals, policies []string) Decision {
	d := Decision{Effect: EffectAllow, Reasons: policies, Approvals: nil, Engine: "cedar", PolicyVersion: ""}

	switch {
	case !first:
		d.Effect = EffectDeny
	case !second:
		d.Effect = EffectApprove
		d.Approvals = &approvals
	default:
	}

	return d
}
