package inventory

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errNoUnitID is the refusal of a restore or a final delete whose unit has no
// stable ID.
const errNoUnitID = "the deleted unit has no stable ID, so its gates cannot be checked"

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
//
// The unit of a restore or a final delete is a deleted unit, which the
// admission must name by its stable ID: the gates and the access limits of that
// unit hold, and never those of a new unit with its name. An object with no
// unit ID fails closed with 403 gate.denied, and the refusal is in the audit
// log.
func (h *Handler) guardStateWrite(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, check stateCheck,
) bool {
	ctx := r.Context()

	// Defense in depth: the route resolves the object from the unit ID in its
	// path, and the store reads the gates of a scope by the ID that it
	// carries. This check stays so that a caller with an object by name
	// alone can never check the gates of the live unit of that name.
	if obj.UnitID() == "" {
		h.guard.Record(ctx, r, p, refusal.Denial{
			Action: check.action, Object: obj, Reason: errNoUnitID, ReasonWithin: nil, Window: 0,
		})
		h.writeFailure(ctx, w, problem.New(http.StatusForbidden, errcode.GateDenied, errNoUnitID))

		return false
	}

	out := h.admit.Admit(ctx, stategate.Write{
		Principal: p, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceAPI,
		State: nil, StateErr: nil,
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

	status, answerCode := refusalAnswer(out.Code)

	h.writeFailure(ctx, w, problem.New(status, answerCode, out.Detail))

	return false
}

// refusalAnswer maps the code of a refusal, which the admission chose, to the
// status and the code of the answer. The admission also wrote the code in
// the detail and in the audit entry, so the three say the same thing. A
// final gate is a gate that refuses the write, so its refusal answers
// gate.denied, and the detail keeps gate.final. Any other code fails closed
// to gate.denied with 403: the write stays refused.
func refusalAnswer(code string) (int, string) {
	switch code {
	case errcode.GateLimited:
		return http.StatusForbidden, errcode.GateLimited
	case errcode.GateRuleFailed:
		return http.StatusConflict, errcode.GateRuleFailed
	case errcode.GateUnavailable:
		return http.StatusServiceUnavailable, errcode.GateUnavailable
	default:
		return http.StatusForbidden, errcode.GateDenied
	}
}
