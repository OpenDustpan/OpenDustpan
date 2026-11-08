package tfe

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// stateCheck says what a route writes: the action that the route needs and the
// operation kind of the write.
type stateCheck struct {
	action identity.Action
	op     gate.OperationKind
	// state is what the write changes in the state of the unit, or nil.
	state *gate.StateChange
	// stateErr is the error of the read of the state, or nil. The write is
	// refused when it is set.
	stateErr error
}

// newStateCheck returns the check of a write that holds no state change.
func newStateCheck(action identity.Action, op gate.OperationKind) stateCheck {
	return stateCheck{action: action, op: op, state: nil, stateErr: nil}
}

// guardStateWrite asks the state guard before the write of the route. It
// returns true when the write may go on. When it returns false, the answer is
// written: a refusal of a gate or an access limit answers 403 as a JSON:API
// error. OpenTofu reads 403 as a refusal, never as a held lock, so it stops
// and holds no lock. The detail holds the message of each gate, the error
// code and the ID of the decision record. A store that cannot take the
// decision record answers the 503 of every other write path. The admission
// writes the audit entry of each refusal.
func (h *Handler) guardStateWrite(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, check stateCheck,
) bool {
	ctx := r.Context()

	out := h.admit.Admit(ctx, stategate.Write{
		Principal: p, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceCLI,
		State: check.state, StateErr: check.stateErr,
	}, func(ctx context.Context, reason string) {
		h.guard.Record(ctx, r, p, refusal.Denial{
			Action: check.action, Object: obj, Reason: reason, ReasonWithin: nil, Window: 0,
		})
	})

	switch {
	case out.Allowed:
		return true
	case out.Unavailable != nil:
		h.writeError(ctx, w, out.Unavailable)

		return false
	}

	var answer errcode.Answer

	answer, err := errcode.New(out.Code)
	if err != nil {
		faultlog.LogInternal(ctx, h.logger, "error code failed", faultlog.CauseUnclassified, out.Code)
		h.writeInternal(ctx, w)

		return false
	}

	h.writeAnswer(ctx, w, errorClass{
		status: http.StatusForbidden, title: titleForbidden, detail: out.Detail, code: answer.Code(), args: nil,
	}, answer, "")

	return false
}
