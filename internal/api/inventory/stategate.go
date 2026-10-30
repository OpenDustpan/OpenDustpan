package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// StateGuard checks a write of state against the access limits of its action
// and the gates of the decision point state.write. *gate.Service implements
// it. A nil error lets the write go on. Any error refuses it.
type StateGuard interface {
	GuardStateWrite(ctx context.Context, w gate.StateWrite) (gate.Decision, error)
}

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
// does not answer 503. The message holds the message of each gate, the error
// code and the ID of the decision record, and the answer carries the code of
// the catalog.
func (h *Handler) guardStateWrite(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, check stateCheck,
) bool {
	if h.stateGuard == nil {
		return true
	}

	ctx := r.Context()

	_, err := h.stateGuard.GuardStateWrite(ctx, gate.StateWrite{
		Principal: p, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceAPI, PlanLock: false,
	})
	if err == nil {
		return true
	}

	// A database that does not answer is not a decision of a gate. The call
	// stays refused, and the answer is the same 503 with Retry-After as on
	// every other route.
	if errors.Is(err, namespace.ErrUnavailable) {
		h.writeError(ctx, w, err)

		return false
	}

	// This binary is below the minimum binary version of the schema. That is
	// not a decision of a gate, and the write may go to another instance. The
	// answer is the 503 of every other write path, with the text that names
	// both versions. The check reads the chain of the error, so it also finds
	// the cause inside a refusal that says the decision record could not be
	// written. A lost store answers the 503 of namespace.unavailable above.
	if errors.Is(err, schema.ErrBinaryBelowMinimum) {
		h.logger.WarnContext(ctx, "state write not decided: this binary is below the minimum of the schema",
			slog.String("object", obj.String()), slog.Any("error", err))
		h.writeError(ctx, w, err)

		return false
	}

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok {
		if errors.Is(err, identity.ErrDenied) {
			h.writeError(ctx, w, err)

			return false
		}

		// Not a decision: the guard could not decide (the store, the fence or
		// the decision record failed). The call stays refused.
		h.logger.ErrorContext(ctx, "state guard failed", slog.String("object", obj.String()), slog.Any("error", err))

		refusal = &gate.RefusalError{
			Err: fmt.Errorf("%w: %w", gate.ErrUnavailable, err), Record: "",
			Messages: []string{"The gates could not decide this call, so the call is refused."},
		}
	}

	code, _ := gate.Code(refusal)
	h.logger.InfoContext(ctx, "state write refused",
		slog.String("object", obj.String()), slog.String("code", code), slog.String("record", string(refusal.Record)))

	// A final gate is a gate that refuses the write, so its refusal answers
	// gate.denied. The message keeps its own code.
	status, answerCode := http.StatusForbidden, errcode.GateDenied

	switch {
	case errors.Is(refusal, gate.ErrLimited):
		answerCode = errcode.GateLimited
	case errors.Is(refusal, gate.ErrRuleFailed):
		status, answerCode = http.StatusConflict, errcode.GateRuleFailed
	case errors.Is(refusal, gate.ErrUnavailable):
		status, answerCode = http.StatusServiceUnavailable, errcode.GateUnavailable
	default:
	}

	h.writeFailure(ctx, w, problem.New(status, answerCode, refusalMessage(refusal, code)))

	return false
}

// refusalMessage returns the text of a refusal: the gate messages, the error
// code and the ID of the decision record.
func refusalMessage(refusal *gate.RefusalError, code string) string {
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
