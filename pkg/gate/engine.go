package gate

import (
	"context"
	"fmt"
	"strconv"

	"opendustpan.org/opendustpan/pkg/identity"
)

// PolicyKind is the kind of a policy of a policy set.
type PolicyKind string

// The policy kinds.
const (
	// PolicyGate is a permit policy that the engine builds from the parsed
	// Condition and Unless of a gate, with the action pinned to its
	// decision point.
	PolicyGate PolicyKind = "gate"
	// PolicyLimit is the forbid policy of an access limit, parsed from its
	// text.
	PolicyLimit PolicyKind = "limit"
)

// Policy is one policy that the engine checks or evaluates.
type Policy struct {
	ID      PolicyID
	Version int64
	Kind    PolicyKind
	// Point is the decision point of a gate.
	Point DecisionPoint
	// Condition and Unless are the Cedar expressions of a gate.
	Condition string
	Unless    string
	// Text is the Cedar policy of an access limit.
	Text string
}

// Schema names the fact schema that a policy set is checked against. The
// schema of one scope holds one attribute for each path set that holds for
// it, in paths.touches and paths.only.
type Schema struct {
	Version  SchemaVersion
	PathSets []ID
}

// PolicySet is the compiled unit of the engine: the policies of one
// decision point, or the access limits, for one evaluation. An adapter can
// cache its compiled form by the policy ID and the version of each policy.
type PolicySet struct {
	Schema   Schema
	Policies []Policy
}

// Request is one Cedar request: one unit at a decision point, or one call
// for the access limits.
type Request struct {
	// ID is unique in one Evaluate call. The answer echoes it.
	ID string
	// Principal is the subject, with Groups as its parents.
	Principal string
	Groups    []string
	// Action is the decision point of a gate, or the action of a call.
	Action string
	// Resource is the unit of a change, or the object of a call.
	Resource identity.Object
	// Facts is the Cedar context of a request at a decision point, and
	// Access the context of a request for the access limits. Exactly one
	// is set, and its schema is the schema of the policy set.
	Facts  *Facts
	Access *AccessFacts
}

// Validate returns ErrInvalid when r sets neither Facts nor Access, or
// both. An engine adapter calls it for each request.
func (r Request) Validate() error {
	if (r.Facts == nil) == (r.Access == nil) {
		return fmt.Errorf("%w: request %q: set exactly one of Facts and Access", ErrInvalid, r.ID)
	}

	return nil
}

// Diagnostic is one error of the engine for one policy.
type Diagnostic struct {
	Policy  PolicyID
	Message string
}

// Answer is the answer of the engine to one request.
type Answer struct {
	// RequestID echoes Request.ID.
	RequestID string
	// Matched holds the determining policies: the matched gates, or the
	// forbid policies that matched.
	Matched []PolicyID
	// Diagnostics holds each policy that gave an error. A policy with a
	// diagnostic is not in Matched.
	Diagnostics []Diagnostic
}

// Engine is the policy engine port: the linked Cedar adapter and the
// adapter of an external Cedar service implement it. Access limits always
// use the linked adapter, so an outage of the service never refuses every
// call.
type Engine interface {
	// Version names the engine and its version, for example
	// "cedar-go/1.2.3". A gate stores the version that it passed at save
	// (CheckedWith), so the Service checks it again after an upgrade.
	Version() string
	// Check parses and type-checks policies against schema, and stores
	// nothing. It refuses a policy that does not parse, that holds a
	// second policy, that names an action, an unknown fact or an unknown
	// attribute, that uses has on an attribute of the schema, that reads a
	// field of a group with available in a clause that does not read the
	// flag, or that has a type
	// error. It refuses an access limit whose effect is permit. It returns
	// one diagnostic for each refused policy, in the order of policies, and
	// a nil error. It returns ErrInvalid only for a schema that it cannot
	// use. An external adapter returns ErrUnavailable when the service does
	// not answer or its answer does not authenticate.
	Check(ctx context.Context, schema Schema, policies []Policy) ([]Diagnostic, error)
	// CheckLimitMembership checks the text of an access limit before Check
	// does. It returns an error that wraps ErrLimitMembership for a text
	// whose membership test of principal names a value that is not a named
	// group and not a set of named groups: the server cannot tell which
	// group such a limit names, so the save refuses the text and a group
	// guard refuses a change that the text could make it lift. It returns
	// nil for any other text, which Check then refuses with a diagnostic.
	CheckLimitMembership(ctx context.Context, text string) error
	// Evaluate evaluates set for each request, and returns one answer for
	// each request in request order. An error of one policy, also a policy
	// that does not compile, is a diagnostic of each answer, never an error
	// of the call. It returns ErrUnavailable when the engine does not
	// answer, when an answer does not echo each request ID, or when an
	// answer names a policy ID that is not in set. It returns ErrInvalid
	// only for a schema that it cannot use, a request that fails
	// Request.Validate, or a set that holds two policies with one ID.
	Evaluate(ctx context.Context, set PolicySet, requests []Request) ([]Answer, error)
}

// CheckError is the error that the Service gives for a save whose policies
// have diagnostics: one diagnostic for each refused policy. It wraps
// ErrInvalid.
type CheckError struct {
	Diagnostics []Diagnostic
}

// Error returns the first diagnostic and the count of the others.
func (e *CheckError) Error() string {
	if len(e.Diagnostics) == 0 {
		return ErrInvalid.Error()
	}

	d := e.Diagnostics[0]
	msg := ErrInvalid.Error() + ": " + d.Policy.String() + ": " + d.Message

	if n := len(e.Diagnostics) - 1; n > 0 {
		msg += " (and " + strconv.Itoa(n) + " more)"
	}

	return msg
}

// Unwrap returns ErrInvalid.
func (e *CheckError) Unwrap() error {
	return ErrInvalid
}
