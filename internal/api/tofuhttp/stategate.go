package tofuhttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// StateGuard checks a write of state against the access limits of its action
// and the gates of the decision point state.write. *gate.Service implements
// it. A nil error with the result allow or warn lets the write go on. Any
// other answer refuses it.
type StateGuard interface {
	GuardStateWrite(ctx context.Context, w gate.StateWrite) (gate.Decision, error)
}

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

// guardFailedText is the message of a refusal when the gates could not decide.
const guardFailedText = "The gates could not decide this write, so the write is refused."

// unknownResultText is the message of a refusal when the guard gave no error
// and a result that is not allow or warn.
const unknownResultText = "The gates gave a result that this server does not know, so the write is refused."

// guardCall is one check of the state guard: the call, its unit and what
// the route writes.
type guardCall struct {
	call  call
	obj   identity.Object
	check stateCheck
}

// guardStateWrite asks the state guard before the write of the route. It
// returns true when the write may go on. When it returns false, the answer is
// written: a refusal of a gate or an access limit answers 403 with the
// message as plain text, so that OpenTofu stops, holds no lock and does not
// retry. The text holds the message of each gate, the error code and the ID
// of the decision record. Each refusal writes an audit entry.
func (h *Handler) guardStateWrite(ctx context.Context, w http.ResponseWriter, c call, check stateCheck) bool {
	if h.stateGuard == nil {
		return true
	}

	obj, err := unitObject(c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return false
	}

	decision, err := h.stateGuard.GuardStateWrite(ctx, gate.StateWrite{
		Principal: c.principal, Unit: obj, Operation: check.op, Action: check.action, Source: gate.SourceCLI,
		PlanLock: check.op == gate.OperationLock && c.principal.JobTokenID() != "",
	})
	g := guardCall{call: c, obj: obj, check: check}

	if err == nil {
		return h.admitDecision(ctx, w, g, decision)
	}

	// The store is lost, or this binary is below the minimum binary version of
	// the schema. Neither is a decision of a gate, and neither is a refusal
	// that the client must keep: the write did not happen, and it may go to
	// another instance or run later. The answer is the 503 of every other
	// write path. The check reads the chain of the error, so it also finds the
	// cause inside a refusal that says the decision record could not be
	// written.
	if errors.Is(err, schema.ErrBinaryBelowMinimum) || errors.Is(err, namespace.ErrUnavailable) {
		h.logger.WarnContext(ctx, "state write not decided: the store cannot take the decision record",
			slog.String("object", obj.String()), slog.Any("error", err))
		h.recordGuardRefusal(ctx, g, "the store cannot take the decision record")
		h.writeError(ctx, w, err)

		return false
	}

	refused, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || refused == nil {
		refused = h.notDecided(ctx, obj, err)
	}

	h.writeRefusal(ctx, w, g, refused)

	return false
}

// admitDecision returns true for a decision that lets the write go on: allow
// or warn. Any other result refuses the write with 403, because a guard that
// gives no error must still give a result that this server knows.
func (h *Handler) admitDecision(ctx context.Context, w http.ResponseWriter, g guardCall, d gate.Decision) bool {
	// Defense in depth: the gate service gives an error for each result other
	// than allow and warn. This check stays so that a new result, or another
	// guard, cannot let a write through with no error.
	if d.Result == gate.ResultAllow || d.Result == gate.ResultWarn {
		return true
	}

	h.logger.ErrorContext(ctx, "state guard gave an unknown result",
		slog.String("object", g.obj.String()), slog.String("result", string(d.Result)))

	h.writeRefusal(ctx, w, g, &gate.RefusalError{
		Err:      fmt.Errorf("%w: the result %q", gate.ErrUnavailable, d.Result),
		Record:   d.Record,
		Messages: []string{unknownResultText},
	})

	return false
}

// notDecided returns the refusal of an error that is not a decision: the guard
// could not decide (the store, the fence or the decision record failed), or a
// job token may not make the write. The write stays refused.
func (h *Handler) notDecided(ctx context.Context, obj identity.Object, err error) *gate.RefusalError {
	if errors.Is(err, identity.ErrDenied) {
		return &gate.RefusalError{
			Err: fmt.Errorf("%w: %w", gate.ErrDenied, err), Record: "",
			Messages: []string{"The credential of the caller may not make this write."},
		}
	}

	h.logger.ErrorContext(ctx, "state guard failed", slog.String("object", obj.String()), slog.Any("error", err))

	return &gate.RefusalError{
		Err: fmt.Errorf("%w: %w", gate.ErrUnavailable, err), Record: "", Messages: []string{guardFailedText},
	}
}

// writeRefusal writes the audit entry of the refusal and answers 403 with
// the text of the refusal.
func (h *Handler) writeRefusal(ctx context.Context, w http.ResponseWriter, g guardCall, refused *gate.RefusalError) {
	code, ok := gate.Code(refused)
	if !ok {
		// Fail closed: a refusal with no known sentinel is still a refusal. It
		// takes the code of a guard that could not decide.
		code = errcode.GateUnavailable
	}

	h.logger.InfoContext(ctx, "state write refused", slog.String("object", g.obj.String()),
		slog.String("code", code), slog.String("record", string(refused.Record)))
	h.recordGuardRefusal(ctx, g, "state.write refused with "+code)

	text := refusalText(refused, code)
	if g.check.op == gate.OperationUnlockedWrite {
		text = lostStateText + " " + text
	}

	http.Error(w, text, http.StatusForbidden)
}

// recordGuardRefusal writes the audit entry of a write that the state guard
// refused. The entry names the action of the check and the unit.
func (h *Handler) recordGuardRefusal(ctx context.Context, g guardCall, reason string) {
	h.guard.Record(ctx, g.call.request, g.call.principal, refusal.Denial{
		Action: g.check.action, Object: g.obj, Reason: reason, Window: 0,
	})
}

// refusalText returns the text of a refusal: the gate messages, the error
// code and the ID of the decision record, on one line.
func refusalText(refused *gate.RefusalError, code string) string {
	var b strings.Builder

	for _, m := range refused.Messages {
		b.WriteString(oneLine(m))
		b.WriteString(" ")
	}

	b.WriteString("(error " + code)

	if refused.Record != "" {
		b.WriteString(", decision record " + oneLine(string(refused.Record)))
	}

	b.WriteString(")")

	return b.String()
}

// oneLine replaces each CR and LF of s with a space, so that a message cannot
// add a line to the plain-text body.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
