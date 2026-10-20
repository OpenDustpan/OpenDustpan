package tfe

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// StateGuard checks a write of state against the access limits of its action
// and the gates of the decision point state.write. *gate.Service implements
// it. A nil error lets the write go on. Any error refuses it.
type StateGuard interface {
	GuardStateWrite(ctx context.Context, w gate.StateWrite) (gate.Decision, error)
}

// gateErrorCodes lists the machine codes that a refusal by a gate or an
// access limit gives. They are the codes of pkg/gate.
func gateErrorCodes() []string {
	return []string{"gate.denied", "gate.limited", "gate.rule-failed", "gate.unavailable"}
}

// guardStateWrite asks the state guard before the write of the route. It
// returns true when the write may go on. When it returns false, the answer is
// written: a refusal of a gate or an access limit answers 403 as a JSON:API
// error. OpenTofu reads 403 as a refusal, never as a held lock, so it stops
// and holds no lock. The detail holds the message of each gate, the error
// code and the ID of the decision record.
func (h *Handler) guardStateWrite(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
	action identity.Action, op gate.OperationKind,
) bool {
	if h.stateGuard == nil {
		return true
	}

	ctx := r.Context()

	_, err := h.stateGuard.GuardStateWrite(ctx, gate.StateWrite{
		Principal: p, Unit: obj, Operation: op, Action: action, Source: gate.SourceCLI,
	})
	if err == nil {
		return true
	}

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok {
		// Not a decision: the guard could not decide. The write stays refused.
		h.writeError(ctx, w, err)

		return false
	}

	code, _ := gate.Code(refusal)
	h.logger.InfoContext(ctx, "state write refused",
		slog.String("object", obj.String()), slog.String("code", code), slog.String("record", string(refusal.Record)))
	h.writeClassError(ctx, w, errorClass{
		status: http.StatusForbidden, title: titleForbidden, detail: refusalDetail(refusal, code),
	}, code)

	return false
}

// refusalDetail returns the text of a refusal: the gate messages, the error
// code and the ID of the decision record.
func refusalDetail(refusal *gate.RefusalError, code string) string {
	var b strings.Builder

	for _, m := range refusal.Messages {
		b.WriteString(m)
		b.WriteString(" ")
	}

	b.WriteString("(error " + code)

	if refusal.Record != "" {
		b.WriteString(", decision record " + string(refusal.Record))
	}

	b.WriteString(")")

	return b.String()
}
