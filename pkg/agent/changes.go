package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Errors of the change modes and of the Changes port.
var (
	// ErrDirectNotAllowed reports a direct change when the server flag, the
	// namespace opt-in or the person opt-in for direct changes is not set.
	ErrDirectNotAllowed = errors.New("direct changes by agents are not allowed here")
	// ErrModeNotSupported reports a mode that the kind of change does not
	// support, for example a review request for a kind with no source to
	// write back to.
	ErrModeNotSupported = errors.New("the kind of change does not support this mode")
	// ErrChangeFrozen reports a change that a change freeze stops, as it
	// stops the same change by a person.
	ErrChangeFrozen = errors.New("a change freeze stops changes to this unit")
	// ErrUnknownChangeKind reports a kind of change that the job model does
	// not know for the unit.
	ErrUnknownChangeKind = errors.New("unknown kind of change for this unit")
	// ErrChangeNotFound reports a change ID that the job model does not
	// know, or a change that the actor cannot read.
	ErrChangeNotFound = errors.New("change not found")
	// ErrChangeKeyConflict reports a propose whose key names a change with
	// another unit, kind, mode or spec.
	ErrChangeKeyConflict = errors.New("change key names another change")
	// ErrChangeFinished reports a cancel of a change that already started
	// to apply or ended.
	ErrChangeFinished = errors.New("change already started or ended")
)

// ChangeMode is how a proposed change goes in.
type ChangeMode string

// The change modes.
const (
	// ModeReviewRequest writes the change back to the source of the unit
	// as a review request. A person merges it, and the usual path applies
	// it.
	ModeReviewRequest ChangeMode = "review-request"
	// ModeApproveFirst makes a job that waits for the approval of a person
	// before it applies.
	ModeApproveFirst ChangeMode = "approve-first"
	// ModeDirect makes a job that applies with no approval by a person.
	// Policies and change freezes still apply.
	ModeDirect ChangeMode = "direct"
)

// ChangePolicy holds the settings that decide the mode of a change by an
// agent. Direct changes need all three switches.
type ChangePolicy struct {
	// ServerDirect is the installation setting that allows direct changes
	// by agents. The default is off.
	ServerDirect bool
	// NamespaceDirect is the opt-in of the namespace of the unit.
	NamespaceDirect bool
	// UserDirect is the opt-in of the person of the call.
	UserDirect bool
}

// KindSupport tells what a kind of change supports for a unit, as the
// Changes port answers it. WriteBack is true when the kind can write the
// change back to the source of the unit as a review request.
type KindSupport struct {
	WriteBack bool
}

// Mode returns the mode of a change of a kind with support kind, for a
// requested mode. An empty request takes the default: a review
// request for a kind with write-back, and approve-first for the others. A
// direct change is never a default. It returns ErrDirectNotAllowed,
// ErrModeNotSupported, or ErrInvalid for an unknown mode.
func (p ChangePolicy) Mode(kind KindSupport, requested ChangeMode) (ChangeMode, error) {
	switch requested {
	case "":
		if kind.WriteBack {
			return ModeReviewRequest, nil
		}

		return ModeApproveFirst, nil
	case ModeReviewRequest:
		if !kind.WriteBack {
			return "", ErrModeNotSupported
		}

		return ModeReviewRequest, nil
	case ModeApproveFirst:
		return ModeApproveFirst, nil
	case ModeDirect:
		if !p.ServerDirect || !p.NamespaceDirect || !p.UserDirect {
			return "", ErrDirectNotAllowed
		}

		return ModeDirect, nil
	default:
		return "", fmt.Errorf("%w: unknown change mode %q", ErrInvalid, requested)
	}
}

// ChangeID identifies a change. The job model assigns it.
type ChangeID string

// ChangeState is the state of a change.
type ChangeState string

// The states of a change. Applied, failed, rejected and canceled are
// terminal.
const (
	// ChangeWaiting: the change waits for a review or an approval.
	ChangeWaiting ChangeState = "waiting"
	// ChangeQueued: the change is approved, or direct, and waits for a
	// runner.
	ChangeQueued  ChangeState = "queued"
	ChangeRunning ChangeState = "running"
	ChangeApplied ChangeState = "applied"
	ChangeFailed  ChangeState = "failed"
	// ChangeRejected: a person rejected the review request or the approval.
	ChangeRejected ChangeState = "rejected"
	ChangeCanceled ChangeState = "canceled"
)

// ChangeRequest asks the job model for a change. Key makes the request safe
// to repeat: the loop derives it from the run, the turn and the call.
type ChangeRequest struct {
	Key    string
	Actor  Actor
	Unit   identity.Object
	Kind   string
	Mode   ChangeMode
	Spec   json.RawMessage
	Reason string
}

// Change is what the job model answers about a change. ReviewURL is the
// address of the review request or of the approval page. Proposer is the
// subject of the actor that proposed the change, and RunID is the run of the
// agent that proposed it, or empty.
type Change struct {
	ID        ChangeID
	Key       string
	Unit      identity.Object
	Kind      string
	Mode      ChangeMode
	State     ChangeState
	ReviewURL string
	Proposer  string
	RunID     RunID
	CreatedAt time.Time
}

// Changes is the port to the shared job model. The job model implements it.
// An agent calls it for every change: it never writes a unit, a source or a
// setting another way. The job model applies to a change by an agent the
// same checks as to the same change by a person: the action of the kind of
// change, the policies, the change freezes and the approvals. Each
// implementation runs the conformance suite in package agenttest.
type Changes interface {
	// Support returns what the kind of change supports for the unit. It
	// returns ErrUnknownChangeKind.
	Support(ctx context.Context, unit identity.Object, kind string) (KindSupport, error)
	// Propose makes the change of req, or returns the change that req.Key
	// already names. It returns ErrInvalid, ErrUnknownChangeKind,
	// ErrModeNotSupported, ErrChangeFrozen, ErrChangeKeyConflict, and an
	// error that wraps identity.ErrDenied when the actor does not hold the
	// action of the kind.
	Propose(ctx context.Context, req ChangeRequest) (Change, error)
	// Get returns the change id. It returns ErrChangeNotFound, also for a
	// change that the actor cannot read.
	Get(ctx context.Context, actor Actor, id ChangeID) (Change, error)
	// Cancel cancels the change id and returns it. A second cancel of a
	// canceled change returns it again. It returns ErrChangeNotFound,
	// ErrChangeFinished for a change that started to apply or ended, and an
	// error that wraps identity.ErrDenied.
	Cancel(ctx context.Context, actor Actor, id ChangeID) (Change, error)
}
