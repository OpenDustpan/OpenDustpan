package pulumi

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// stateCheck says what a route writes: the action that the route needs and the
// operation kind of the write. The Pulumi routes give no state change: the
// server does not read the state of a Pulumi stack for the gates, so the
// facts of the group state are not available for these writes.
type stateCheck struct {
	action identity.Action
	op     gate.OperationKind
}

// checkImport is a `pulumi stack import` and each `pulumi state` command that
// sends a new deployment: it replaces the state of the stack.
func checkImport() stateCheck {
	return stateCheck{action: identity.ActionUnitWrite, op: gate.OperationStateImport}
}

// checkRestore copies the checkpoint of an earlier stack version into a new
// update.
func checkRestore() stateCheck {
	return stateCheck{action: identity.ActionUnitWrite, op: gate.OperationRollback}
}

// checkRename gives the stack and its state a new name.
func checkRename() stateCheck {
	return stateCheck{action: identity.ActionUnitManage, op: gate.OperationRename}
}

// checkDelete is a `pulumi stack rm`: it removes the stack.
func checkDelete() stateCheck {
	return stateCheck{action: identity.ActionUnitManage, op: gate.OperationDelete}
}

// checkCancel is a `pulumi cancel`: it ends the update of a holder with no
// lease, so it frees the stack.
func checkCancel() stateCheck {
	return stateCheck{action: identity.ActionUnitWrite, op: gate.OperationUnlock}
}

// guardStateWrite asks the state guard before the write of the route. It
// returns true when the write may go on. When it returns false, the answer is
// written: a refusal of a gate or an access limit answers 403 in the Pulumi
// error shape {"code": 403, "message": "..."}, with the code of the catalog in
// errorCode. The message holds the message of each gate, the error code and
// the ID of the decision record. A store that cannot take the decision record
// answers the 503 of every other write path. The admission writes the audit
// entry of each refusal.
func (h *Handler) guardStateWrite(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, check stateCheck,
) bool {
	ctx := r.Context()

	// The facts of the group state stay not available: State is nil. The
	// server gives no fact that it did not read.
	out := h.admit.Admit(ctx, stategate.Write{
		Principal: p, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceCLI,
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

	// Each refusal answers 403, whatever its code: the write did not happen,
	// and the client must not retry it. errorCode and the message carry the
	// code that the admission chose. A code that the catalog does not know is
	// a bug of the server: writeFailure answers 500 with server.internal for
	// it, so the write stays refused.
	h.writeFailure(ctx, w, coded(http.StatusForbidden, out.Detail, out.Code))

	return false
}
