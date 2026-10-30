package tfe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

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

// guardStateWrite asks the state guard before the write of the route. It
// returns true when the write may go on. When it returns false, the answer is
// written: a refusal of a gate or an access limit answers 403 as a JSON:API
// error. OpenTofu reads 403 as a refusal, never as a held lock, so it stops
// and holds no lock. The detail holds the message of each gate, the error
// code and the ID of the decision record.
func (h *Handler) guardStateWrite(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, check stateCheck,
) bool {
	if h.stateGuard == nil {
		return true
	}

	ctx := r.Context()

	_, err := h.stateGuard.GuardStateWrite(ctx, gate.StateWrite{
		Principal: p, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceCLI, PlanLock: false,
	})
	if err == nil {
		return true
	}

	// The store is lost, or this binary is below the minimum binary version of
	// the schema. Neither is a decision of a gate, and neither is a refusal
	// that the client must keep: the write did not happen, and it may go to
	// another instance or run later. The answer is the 503 of every other
	// write path, with the text that names both versions. The check reads the
	// chain of the error, so it also finds the cause inside a refusal that
	// says the decision record could not be written.
	if errors.Is(err, schema.ErrBinaryBelowMinimum) || errors.Is(err, namespace.ErrUnavailable) {
		h.logger.WarnContext(ctx, "state write not decided: the store cannot take the decision record",
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
		// the decision record failed). The write stays refused. OpenTofu reads
		// 403 as a refusal and stops. It reads 5xx as a fault to retry.
		h.logger.ErrorContext(ctx, "state guard failed", slog.String("object", obj.String()), slog.Any("error", err))

		refusal = &gate.RefusalError{
			Err: fmt.Errorf("%w: %w", gate.ErrUnavailable, err), Record: "",
			Messages: []string{"The gates could not decide this write, so the write is refused."},
		}
	}

	code, _ := gate.Code(refusal)
	h.logger.InfoContext(ctx, "state write refused",
		slog.String("object", obj.String()), slog.String("code", code), slog.String("record", string(refusal.Record)))

	var answer errcode.Answer

	answer, err = errcode.New(code)
	if err != nil {
		h.logger.ErrorContext(ctx, "error code failed", slog.String("error_code", code))
		h.writeInternal(ctx, w)

		return false
	}

	h.writeAnswer(ctx, w, errorClass{
		status: http.StatusForbidden, title: titleForbidden, detail: refusalDetail(refusal, code),
		code: answer.Code(), args: nil,
	}, answer, "")

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
