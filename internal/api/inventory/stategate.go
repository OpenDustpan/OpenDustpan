package inventory

import (
	"context"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
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
}

// guardStateWrite asks the state guard before the restore or the final
// delete of a unit. It returns true when the call may go on. When it returns
// false, the answer is written in the error shape of this API: a refusal by a
// gate or an access limit answers 403, a gate in error 409, and an engine that
// does not answer 503. A store that cannot take the decision record answers
// the 503 of every other route. The message holds the message of each gate,
// the error code and the ID of the decision record, and the answer carries
// the code of the catalog. The admission writes the audit entry of each
// refusal.
func (h *Handler) guardStateWrite(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, check stateCheck,
) bool {
	ctx := r.Context()

	out := h.admit.Admit(ctx, stategate.Write{
		Principal: p, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceAPI,
	}, func(ctx context.Context, reason string) {
		h.guard.Record(ctx, r, p, refusal.Denial{Action: check.action, Object: obj, Reason: reason, Window: 0})
	})

	switch {
	case out.Allowed:
		return true
	case out.Unavailable != nil:
		h.writeError(ctx, w, out.Unavailable)

		return false
	}

	// A final gate is a gate that refuses the write, so its refusal answers
	// gate.denied. The message keeps its own code.
	status, answerCode := http.StatusForbidden, errcode.GateDenied

	switch {
	case errors.Is(out.Refusal, gate.ErrLimited):
		answerCode = errcode.GateLimited
	case errors.Is(out.Refusal, gate.ErrRuleFailed):
		status, answerCode = http.StatusConflict, errcode.GateRuleFailed
	case errors.Is(out.Refusal, gate.ErrUnavailable):
		status, answerCode = http.StatusServiceUnavailable, errcode.GateUnavailable
	default:
	}

	h.writeFailure(ctx, w, problem.New(status, answerCode, out.Detail))

	return false
}
