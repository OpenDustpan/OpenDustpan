package tofuhttp

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// stateCheck says what a route writes: the action that the access limits read
// and the operation kind of the write.
type stateCheck struct {
	action identity.Action
	op     gate.OperationKind
}

// lostStateText is the first sentence of the refusal of a state version with
// no lock. OpenTofu sends such a version after it changed the resources, so a
// refusal can leave resources that no stored state records.
const lostStateText = "The server refused this state version, so resources may exist that no stored state records. " +
	"Run `tofu state push` again after the cause of the refusal is gone."

// guardStateWrite asks the state guard before the write of the route. It
// returns true when the write may go on. When it returns false, the answer is
// written: a refusal of a gate or an access limit answers 403 with the
// message as plain text, so that OpenTofu stops, holds no lock and does not
// retry. The text holds the message of each gate, the error code and the ID
// of the decision record. A store that cannot take the decision record
// answers the 503 of every other write path. The admission writes the audit
// entry of each refusal.
func (h *Handler) guardStateWrite(ctx context.Context, w http.ResponseWriter, c call, check stateCheck) bool {
	obj, err := unitObject(c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return false
	}

	out := h.admit.Admit(ctx, stategate.Write{
		Principal: c.principal, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceCLI,
		State: nil, StateErr: nil,
	}, func(ctx context.Context, reason string) {
		h.guard.Record(ctx, c.request, c.principal, refusal.Denial{
			Action: check.action, Object: obj, Reason: reason, ReasonWithin: nil, Window: 0,
		})
	})

	switch {
	case out.Allowed:
		return true
	case out.Unavailable != nil:
		h.writeError(ctx, w, out.Unavailable)
	default:
		text := out.Detail
		if check.op == gate.OperationUnlockedWrite {
			text = lostStateText + " " + text
		}

		http.Error(w, text, http.StatusForbidden)
	}

	return false
}
