package gate

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Subject is the principal of a change or a call, with the facts that the
// server knows from its credential.
type Subject struct {
	ID         string
	Groups     []string
	Kind       PrincipalKind
	Credential CredentialKind
	// AuthAt is the time of the last authentication, or the zero time.
	AuthAt time.Time
	// BreakGlassAccount is true for a session of a break-glass account.
	// Such a session is outside the access limits, and meets each gate.
	BreakGlassAccount bool
}

// Change is one evaluation request of one decision point.
type Change struct {
	Point     DecisionPoint
	Namespace namespace.Name
	Ref       ChangeRef
	Subject   Subject
	// Author is the subject of the author, for the rules that do not let
	// the author approve.
	Author    string
	Operation OperationKind
	// Units holds the units of the change. One Cedar request runs for each
	// unit.
	Units []identity.Object
	// Holders holds the extra scopes whose gates apply: the blueprint
	// version or the profile that the change uses.
	Holders []Scope
	// BreakGlass is the ID of the audit entry of a break-glass for gates
	// in error, or empty. It passes only the gates in error.
	BreakGlass string
	// Fence is the fence of Namespace. The record of a refusal is written
	// with it.
	Fence namespace.Fence
}

// Decision is the result of one evaluation.
type Decision struct {
	Record       RecordID
	Result       Result
	Requirements []Requirement
	Warnings     []string
	AutoApply    bool
}

// Evaluator is the port that each caller of a decision point uses: the job
// model, the change orders, the Catalog, the profiles, the tool-neutral
// state operations and each tool adapter at state.write. A tool adapter
// calls it before the write and writes a refusal in the error shape of its
// own tool. No tool adapter calls another.
type Evaluator interface {
	// Evaluate produces the facts, evaluates the gates of c.Point for each
	// unit, combines the results, and writes the decision record.
	//
	// It returns the decision and a nil error for allow, warn and
	// awaiting-approval. At state.write, a CLI cannot wait, so an approval
	// outcome refuses. A refusal is a *RefusalError that wraps ErrDenied for a
	// deny or an approval at state.write, ErrRuleFailed for a gate in error
	// in enforce mode, or ErrUnavailable when the engine does not answer.
	// It returns ErrLimit when the fact document does not fit with each
	// large set empty. It returns ErrInvalid for a change that names no
	// unit or an unknown decision point. The record of a refusal is
	// written in its own transaction. A store error comes back wrapped,
	// and refuses the change.
	Evaluate(ctx context.Context, c Change) (Decision, error)
}

// AccessCall is one call that the grants allowed, for the access limits.
type AccessCall struct {
	Subject Subject
	Action  identity.Action
	Object  identity.Object
	// Labels holds the key=value labels of the unit of Object.
	Labels []string
	// Collection is the collection of the unit of Object, or empty.
	Collection string
}

// AccessChecker evaluates the access limits of each scope that holds the
// object of a call. The decorator of identity.Authorizer calls it after
// the grants allow, and only with the linked engine.
type AccessChecker interface {
	// CheckAccess returns nil when no limit forbids the call, and for a
	// session of a break-glass account. A refusal is a *RefusalError that wraps
	// ErrLimited and names the limit, or ErrRuleFailed for a limit that
	// gave an engine error. It writes the record of a refusal with
	// Records.NoteRefusal.
	CheckAccess(ctx context.Context, call AccessCall) error
}

// Membership gives the collections that hold a unit: the scopes between
// its namespace and the unit.
type Membership interface {
	// Collections returns the collections of the unit unit, ordered by
	// name. It returns none for a unit in no collection.
	Collections(ctx context.Context, unit identity.Object) ([]collection.Name, error)
}
