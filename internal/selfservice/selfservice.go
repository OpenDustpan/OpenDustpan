// Package selfservice is the self-service gateway of OpenDustpan. A principal
// sends a Self-service request: a declarative JSON document that asks for one
// unit from one blueprint, with the answers to the inputs of the blueprint.
// The request states the result, never the steps or the code of a tool.
//
// A blueprint adds to a Pulumi component or an OpenTofu module that a
// platform team already has. It names the program, its inputs with their
// types, defaults and validation in Expr, its choices, its policy and quota
// rules, and its delivery. Choices come from the outputs of other units,
// and the server alone computes the options.
//
// Each submit of a document makes a revision of the request, and a revision
// starts one job through the job service. A request lives in one home. In
// git, a write-back job commits the request file, and the team's own CI
// renders and applies it. In OpenDustpan alone, a runner job applies it.
// The server checks each revision again at approval and at apply, because
// the values that it read can change in between.
//
// The package is tool-neutral. It imports no tool package, no store
// adapter, no forge adapter and no transport. The internal wiring of the
// server implements the ports that touch a tool: UnitProvisioner, Renderer
// and OutputSource.
package selfservice

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	// ErrInvalid reports a document, a blueprint or a request with an empty
	// or bad field.
	ErrInvalid = errors.New("self-service request is not valid")
	// ErrSchemaVersion reports a document of a schema version that the
	// server does not read.
	ErrSchemaVersion = errors.New("schema version is not supported")
	// ErrLimit reports a document, a list or a store above a limit. The
	// message names the limit.
	ErrLimit = errors.New("self-service limit exceeded")
	// ErrNotFound reports a request, a revision, a blueprint or a version
	// that the store does not know.
	ErrNotFound = errors.New("self-service object not found")
	// ErrConflict reports a write that read an older version of a request or
	// a draft, or a supersede of a revision whose change is already in git.
	// The caller reads again and decides again.
	ErrConflict = errors.New("self-service object changed since it was read")
	// ErrAlreadyPublished reports a publish of a draft version that has a
	// published version. One draft version gives one version.
	ErrAlreadyPublished = errors.New("draft version is already published")
	// ErrInFlight reports a submit while a revision of the same request has
	// not ended.
	ErrInFlight = errors.New("another revision of the request has not ended")
	// ErrFinished reports a change of a revision that ended.
	ErrFinished = errors.New("revision already ended")
	// ErrTransition reports a change of the state of a revision that the
	// rules do not allow.
	ErrTransition = errors.New("revision state does not allow this change")
	// ErrPathMismatch reports a request file whose path in git names another
	// namespace, blueprint or name than its document.
	ErrPathMismatch = errors.New("request file path does not match its document")
	// ErrCheckFailed reports a document that fails one or more checks. The
	// error is a *CheckError, which lists every failure.
	ErrCheckFailed = errors.New("self-service request failed its checks")
	// ErrPolicyChanged reports a recheck whose policy asks for more
	// approvals than the gates of the job have. The requester submits again.
	ErrPolicyChanged = errors.New("policy asks for more approvals than the job has")
	// ErrRetired reports a create or an upgrade to a retired blueprint
	// version.
	ErrRetired = errors.New("blueprint version is retired")
	// ErrExprVersion reports a blueprint version whose expressions do not
	// compile with the Expr version of the server.
	ErrExprVersion = errors.New("blueprint version does not compile with this Expr version")
	// ErrRuleFailed reports an Expr rule that failed to run. It is a
	// failure of the answer, never a pass.
	ErrRuleFailed = errors.New("validation rule failed to run")
	// ErrChoiceCycle reports choices that depend on each other in a loop.
	ErrChoiceCycle = errors.New("choices depend on each other in a loop")
	// ErrOneHome reports a key that more than one home sets: two inputs, an
	// input and a platform value, or the render and another home of the
	// unit.
	ErrOneHome = errors.New("a value has more than one home")
	// ErrUnitName reports a unit name that the tool refuses, or a unit that
	// exists already.
	ErrUnitName = errors.New("unit name is not available")
	// ErrSourceUnavailable reports an output source that cannot read. It is
	// not a failure of the answer: the check cannot run.
	ErrSourceUnavailable = errors.New("output source is not available")
	// ErrHasState reports a delete of the unit of a request that has a state
	// version.
	ErrHasState = errors.New("unit has a state version")
	// ErrForgeUnavailable reports a forge that cannot give a request file.
	ErrForgeUnavailable = errors.New("forge is not available")
)

// Limits of the store and of the API. A store adapter returns ErrLimit
// above them. Installation settings can change them later.
const (
	MaxBlueprints       = 500
	MaxVersions         = 1000
	MaxRequests         = 10000
	MaxInFlight         = 1000
	MaxSourceUnits      = 10000
	SubmitsPerMinute    = 60
	ExprTimeoutMillis   = 50
	PolicyTimeoutMillis = 5000
)

// The bounds of a name of a request, of a blueprint and of an input.
const (
	MaxNameBytes      = 63
	MaxInputNameBytes = 64
)

// ValidName reports whether s is a valid name of a request or a blueprint:
// 1 to MaxNameBytes bytes of lowercase letters, digits and '-', with a
// letter first.
func ValidName(s string) bool {
	return ident.ValidSlug(s, MaxNameBytes)
}

// ValidInputName reports whether s is a valid name of an input: 1 to
// MaxInputNameBytes bytes of letters, digits and '_', with a letter first.
func ValidInputName(s string) bool {
	if s == "" || len(s) > MaxInputNameBytes || !isLetter(s[0]) {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if !isLetter(c) && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}

	return true
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// BlueprintRef names a blueprint: its namespace and its name.
type BlueprintRef struct {
	Namespace namespace.Name
	Name      string
}

// String returns the reference as <namespace>/<name>.
func (r BlueprintRef) String() string {
	return r.Namespace.String() + "/" + r.Name
}

// RequestRef names a request: its namespace, its blueprint and its name.
type RequestRef struct {
	Namespace namespace.Name
	Blueprint string
	Name      string
}

// String returns the reference as <namespace>/<blueprint>/<name>.
func (r RequestRef) String() string {
	return r.Namespace.String() + "/" + r.Blueprint + "/" + r.Name
}

// RequestID identifies a request. The store makes it at the first submit,
// with the prefix req-.
type RequestID string

// RequestIDPrefix starts every request ID.
const RequestIDPrefix = "req-"

// RevisionKey returns the key of revision n of request id:
// <request ID>/<n>. It is the idempotency key of the job of the revision,
// the start key of its durable operations and the ID of its reservation.
func RevisionKey(id RequestID, n int) string {
	return fmt.Sprintf("%s/%d", id, n)
}

// HomeKind names where the document of a request lives.
type HomeKind string

// The homes of a request.
const (
	// HomeGit keeps the document as a file in a repository of the team. A
	// write-back job commits it, and the team's own CI applies it.
	HomeGit HomeKind = "git"
	// HomeOpenDustpan keeps the document in OpenDustpan only. A runner job
	// applies it.
	HomeOpenDustpan HomeKind = "opendustpan"
)

// Valid reports whether k is one of the homes of this package.
func (k HomeKind) Valid() bool {
	return k == HomeGit || k == HomeOpenDustpan
}

// Ensure is whether the requester wants the unit to exist.
type Ensure string

// The values of Ensure.
const (
	// EnsurePresent asks for the unit with the answers of the document. It is
	// the default.
	EnsurePresent Ensure = "present"
	// EnsureAbsent asks for the destroy of the unit.
	EnsureAbsent Ensure = "absent"
)

// Operation is what a revision does to its unit. The server picks it from
// the document and the last applied revision (OperationFor).
type Operation string

// The operations of a revision.
const (
	OperationCreate  Operation = "create"
	OperationUpdate  Operation = "update"
	OperationDestroy Operation = "destroy"
	// OperationNone is a document equal to the last applied revision. It
	// makes no job.
	OperationNone Operation = "none"
)

// OperationFor returns the operation of a revision that asks for ensure,
// when the last applied revision of the request asked for last, or last is
// empty when no revision was applied. It returns ErrInvalid for an absent
// unit that never existed.
func OperationFor(last, ensure Ensure) (Operation, error) {
	if ensure == "" {
		ensure = EnsurePresent
	}

	switch {
	case ensure != EnsurePresent && ensure != EnsureAbsent:
		return "", fmt.Errorf("%w: ensure %q", ErrInvalid, ensure)
	case last == "" && ensure == EnsureAbsent:
		return "", fmt.Errorf("%w: the unit never existed, so it cannot be absent", ErrInvalid)
	case last == EnsureAbsent && ensure == EnsureAbsent:
		return OperationNone, nil
	case ensure == EnsureAbsent:
		return OperationDestroy, nil
	case last == EnsurePresent:
		return OperationUpdate, nil
	default:
		return OperationCreate, nil
	}
}

// Origin tells how a revision came to be.
type Origin string

// The origins of a revision.
const (
	// OriginAPI is a submit through the API.
	OriginAPI Origin = "api"
	// OriginGit is a change of the request file in git that the team's CI
	// rendered, with no submit. It has no job.
	OriginGit Origin = "git"
)
