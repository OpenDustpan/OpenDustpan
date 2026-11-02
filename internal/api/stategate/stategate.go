// Package stategate is the admission of a write of state at the decision
// point state.write. Each HTTP adapter that writes state (the OpenTofu HTTP
// backend, the TFE-compatible API and the tool-neutral inventory API) asks
// one Admitter before the write, and keeps only the wire encoding of the
// answer.
//
// The package writes no HTTP answer and imports no net/http, so it does not
// depend on an adapter. It lives under internal/api because its callers are
// the HTTP adapters only, and because it reads the errors of the store
// (pkg/store/schema), which a domain package must not import.
package stategate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// Guard checks a write of state against the access limits of its action and
// the gates of the decision point state.write. *gate.Service implements it.
// A nil error with the result allow or warn lets the write go on. Any other
// answer refuses it.
type Guard interface {
	GuardStateWrite(ctx context.Context, w gate.StateWrite) (gate.Decision, error)
}

// ErrNoGuard means that an adapter got no guard. A server whose store keeps
// gates must give the gate service. A server whose store keeps no gates must
// give the guard of Off, which names the reason.
var ErrNoGuard = errors.New("no state.write guard: give the gate service, or stategate.Off with the reason")

// errNoRecorder is the cause of the refusal of a write with no recorder.
var errNoRecorder = errors.New("no audit recorder")

// errUnknownResult is the cause of the refusal of a result that is not allow
// or warn.
var errUnknownResult = errors.New("the guard gave an unknown result")

// errNilRefusal is the cause of the refusal of a guard that gave a nil
// *gate.RefusalError.
var errNilRefusal = errors.New("the guard gave a nil refusal")

// Mode is the effective state of the state.write gates of a server.
type Mode string

// The modes of the state.write gates.
const (
	// ModeOn means that the server checks each write at state.write.
	ModeOn Mode = "on"
	// ModeOff means that the store keeps no gates, so no write is checked.
	ModeOff Mode = "off"
)

// Effective is the effective value of the state.write gates and its reason.
// The installation API shows it, and a server that runs with the gates off
// writes it to the start log and to the audit log.
type Effective struct {
	Mode   Mode
	Reason string
}

// ReasonOn is the reason of the effective value of a guard that is not Off.
const ReasonOn = "the store keeps gates, so the server checks each write at state.write"

// OffGuard is the guard of a server whose store keeps no gates. It lets each
// write go on with no decision record. It is an explicit value with a reason,
// so that a missing guard is never read as gates off. Make it with Off: the
// zero value has no reason, and it refuses each write.
type OffGuard struct {
	reason string
}

// errOffNoReason means that an OffGuard has no reason: it was not made by Off.
var errOffNoReason = errors.New("state.write gates off with no reason")

// GuardStateWrite implements Guard. It allows the write, and refuses it when
// the guard has no reason.
func (o OffGuard) GuardStateWrite(context.Context, gate.StateWrite) (gate.Decision, error) {
	// Defense in depth: Off and New refuse an empty reason. This check stays
	// so that a zero OffGuard, which a caller can make with no call of Off,
	// never lets a write through. It does not rely on Off or New.
	if strings.TrimSpace(o.reason) == "" {
		return gate.Decision{Record: "", Result: "", Requirements: nil, Warnings: nil, AutoApply: false}, errOffNoReason
	}

	return gate.Decision{Record: "", Result: gate.ResultAllow, Requirements: nil, Warnings: nil, AutoApply: false}, nil
}

// Reason returns the reason why the gates are off.
func (o OffGuard) Reason() string {
	return o.reason
}

// Off returns the guard of a server whose store keeps no gates. It lets each
// write go on. The reason says why the gates are off, and the effective
// value shows it. A guard with an empty reason lets no write through: New
// refuses it, and the guard refuses each write.
func Off(reason string) OffGuard {
	return OffGuard{reason: reason}
}

// EffectiveOf returns the effective value of the state.write gates of g. A
// nil guard and an OffGuard with no reason read as on: the Admitter refuses
// each write with them, so they never let a write through.
func EffectiveOf(g Guard) Effective {
	if off, ok := g.(OffGuard); ok && strings.TrimSpace(off.reason) != "" {
		return Effective{Mode: ModeOff, Reason: off.reason}
	}

	return Effective{Mode: ModeOn, Reason: ReasonOn}
}

// Write is one write of state that an adapter asks about.
type Write struct {
	Principal identity.Principal
	Unit      identity.Object
	Operation gate.OperationKind
	Action    identity.Action
	Source    gate.SourceKind
}

// Recorder writes the audit entry of a refused write. The adapter binds it to
// the request, so that the entry names the route, the principal and the
// client. The reason is a short sentence with no value of the request.
type Recorder func(ctx context.Context, reason string)

// Outcome is the answer of Admit. Allowed is true when the write may go on.
// Otherwise exactly one of Unavailable and Refusal is set:
//   - Unavailable is the error of a store that cannot take the decision
//     record. It is not a decision of a gate: the adapter answers the 503 of
//     every other write path for it, and the client may retry.
//   - Refusal is the refusal that the client must keep. Code is its code of
//     the catalog, and Detail is its text on one line.
//
// The audit entry of each refusal is written before Admit returns.
type Outcome struct {
	Allowed     bool
	Unavailable error
	Refusal     *gate.RefusalError
	Code        string
	Detail      string
}

// The messages of the refusals that the Admitter makes. Each one is the
// first sentence of the detail of its refusal.
const (
	// GuardFailedText is the message when the gates could not decide.
	GuardFailedText = "The gates could not decide this write, so the write is refused."
	// UnknownResultText is the message when the guard gave no error and a
	// result that is not allow or warn.
	UnknownResultText = "The gates gave a result that this server does not know, so the write is refused."
	// NoGuardText is the message when the adapter has no guard.
	NoGuardText = "The server has no state.write guard, so the write is refused."
	// NoRecorderText is the message when the adapter cannot write the audit
	// entry of a refusal.
	NoRecorderText = "The server cannot record a refusal of this write, so the write is refused."
	// DeniedText is the message when the credential may not make the write.
	DeniedText = "The credential of the caller may not make this write."
)

// Admitter asks the guard about each write of state, writes the audit entry
// of each refusal and gives the adapter an Outcome to encode.
type Admitter struct {
	guard Guard
	log   *slog.Logger
}

// New returns the Admitter of guard. A nil guard is ErrNoGuard, so that a
// server cannot start with no guard. A nil logger is the default logger.
func New(guard Guard, log *slog.Logger) (*Admitter, error) {
	if guard == nil {
		return nil, ErrNoGuard
	}

	if off, ok := guard.(OffGuard); ok && strings.TrimSpace(off.reason) == "" {
		return nil, errOffNoReason
	}

	if log == nil {
		log = slog.Default()
	}

	return &Admitter{guard: guard, log: log}, nil
}

// Effective returns the effective value of the state.write gates of a.
func (a *Admitter) Effective() Effective {
	if a == nil {
		return EffectiveOf(nil)
	}

	return EffectiveOf(a.guard)
}

// StateWrite returns the question that the guard gets for w. It is the one
// place that fills a gate.StateWrite for the adapters, so a fact that every
// write carries is added here.
func StateWrite(w Write) gate.StateWrite {
	return gate.StateWrite{
		Principal: w.Principal, Unit: w.Unit, Operation: w.Operation, Action: w.Action, Source: w.Source,
		// A job token takes the lock of its plan with the operation kind
		// lock. Every adapter computes the flag here, in the same way.
		PlanLock: w.Operation == gate.OperationLock && w.Principal.JobTokenID() != "",
	}
}

// Admit asks the guard about w. It returns an Outcome with Allowed true when
// the write may go on. Every other Outcome refuses the write, and Admit has
// written its audit entry with record. Admit fails closed: a nil Admitter, a
// nil guard, a nil record, a guard error and a result that is not allow or
// warn all refuse the write.
func (a *Admitter) Admit(ctx context.Context, w Write, record Recorder) Outcome {
	log := slog.Default()
	if a != nil && a.log != nil {
		log = a.log
	}

	if out, refused := a.precheck(ctx, log, w, record); refused {
		return out
	}

	decision, err := a.guard.GuardStateWrite(ctx, StateWrite(w))
	if err == nil {
		return a.admitDecision(ctx, log, w, record, decision)
	}

	return guardError(ctx, log, w, record, err)
}

// precheck refuses the write before the guard is asked when a has no guard
// or the adapter gives no recorder. It returns the refusal and true then.
func (a *Admitter) precheck(ctx context.Context, log *slog.Logger, w Write, record Recorder) (Outcome, bool) {
	// Defense in depth: New refuses a nil guard, and the server refuses to
	// start with no guard. This check stays so that a zero Admitter, or a
	// caller that skips New, cannot let a write through. It does not rely on
	// the start check.
	if a == nil || a.guard == nil {
		log.ErrorContext(ctx, "state write refused: no state.write guard", slog.String("object", w.Unit.String()))

		return refuse(ctx, log, w, record, unavailable(ErrNoGuard, "", NoGuardText)), true
	}

	// Defense in depth: each adapter gives a recorder. This check stays so
	// that a write is never allowed or refused with no audit entry: with no
	// recorder, the write is refused and the log has the refusal.
	if record == nil {
		log.ErrorContext(ctx, "state write refused: no audit recorder", slog.String("object", w.Unit.String()))

		return refuse(ctx, log, w, nil, unavailable(errNoRecorder, "", NoRecorderText)), true
	}

	return Outcome{Allowed: false, Unavailable: nil, Refusal: nil, Code: "", Detail: ""}, false
}

// guardError returns the Outcome of a guard error. Every guard error refuses
// the write.
func guardError(ctx context.Context, log *slog.Logger, w Write, record Recorder, err error) Outcome {
	// A nil *gate.RefusalError is an error that panics when the chain is
	// read. The guard gave no decision, so the write is refused before any
	// read of the chain.
	if refused, ok := errors.AsType[*gate.RefusalError](err); ok && refused == nil {
		log.ErrorContext(ctx, "state guard gave a nil refusal", slog.String("object", w.Unit.String()))

		return refuse(ctx, log, w, record, unavailable(errNilRefusal, "", GuardFailedText))
	}

	// The store is lost, or this binary is below the minimum binary version of
	// the schema. Neither is a decision of a gate, and neither is a refusal
	// that the client must keep: the write did not happen, and it may go to
	// another instance or run later. The check reads the chain of the error,
	// so it also finds the cause inside a refusal that says the decision
	// record could not be written.
	if errors.Is(err, schema.ErrBinaryBelowMinimum) || errors.Is(err, namespace.ErrUnavailable) {
		log.WarnContext(ctx, "state write not decided: the store cannot take the decision record",
			slog.String("object", w.Unit.String()), slog.Any("error", err))
		record(ctx, "the store cannot take the decision record")

		return Outcome{Allowed: false, Unavailable: err, Refusal: nil, Code: "", Detail: ""}
	}

	refused, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || refused.Err == nil {
		refused = notDecided(ctx, log, w, err)
	}

	return refuse(ctx, log, w, record, refused)
}

// admitDecision allows a decision with the result allow or warn. Any other
// result refuses the write, because a guard that gives no error must still
// give a result that this server knows.
func (a *Admitter) admitDecision(
	ctx context.Context, log *slog.Logger, w Write, record Recorder, d gate.Decision,
) Outcome {
	// Defense in depth: the gate service gives an error for each result other
	// than allow and warn. This check stays so that a new result, or another
	// guard, cannot let a write through with no error. It does not rely on
	// the gate service.
	if d.Result == gate.ResultAllow || d.Result == gate.ResultWarn {
		return Outcome{Allowed: true, Unavailable: nil, Refusal: nil, Code: "", Detail: ""}
	}

	log.ErrorContext(ctx, "state guard gave an unknown result",
		slog.String("object", w.Unit.String()), slog.String("result", oneLine(string(d.Result))))

	return refuse(ctx, log, w, record,
		unavailable(fmt.Errorf("%w: %q", errUnknownResult, d.Result), d.Record, UnknownResultText))
}

// notDecided returns the refusal of an error that is not a decision: the guard
// could not decide (the store, the fence or the decision record failed), or a
// job token may not make the write.
func notDecided(ctx context.Context, log *slog.Logger, w Write, err error) *gate.RefusalError {
	if errors.Is(err, identity.ErrDenied) {
		return &gate.RefusalError{
			Err:      fmt.Errorf("%w: %w", gate.ErrDenied, err),
			Record:   "",
			Messages: []string{DeniedText},
		}
	}

	log.ErrorContext(ctx, "state guard failed", slog.String("object", w.Unit.String()), slog.Any("error", err))

	return unavailable(err, "", GuardFailedText)
}

// unavailable returns the refusal of a guard that could not decide.
func unavailable(cause error, rec gate.RecordID, message string) *gate.RefusalError {
	return &gate.RefusalError{
		Err: fmt.Errorf("%w: %w", gate.ErrUnavailable, cause), Record: rec, Messages: []string{message},
	}
}

// refuse writes the log line and the audit entry of a refusal and returns its
// Outcome. A nil record writes the log line only.
func refuse(ctx context.Context, log *slog.Logger, w Write, record Recorder, refused *gate.RefusalError) Outcome {
	code, ok := gate.Code(refused)
	if !ok {
		// Fail closed: a refusal with no known sentinel is still a refusal. It
		// takes the code of a guard that could not decide.
		code = errcode.GateUnavailable
	}

	log.InfoContext(ctx, "state write refused", slog.String("object", w.Unit.String()),
		slog.String("code", code), slog.String("record", oneLine(string(refused.Record))))

	if record != nil {
		record(ctx, "state.write refused with "+code)
	}

	return Outcome{Allowed: false, Unavailable: nil, Refusal: refused, Code: code, Detail: Detail(refused, code)}
}

// Detail returns the text of a refusal on one line: the gate messages, the
// error code and the ID of the decision record. Each CR and LF becomes a
// space, so that a message cannot add a line to a plain-text body or a log.
func Detail(refused *gate.RefusalError, code string) string {
	var b strings.Builder

	for _, m := range refused.Messages {
		b.WriteString(oneLine(m))
		b.WriteString(" ")
	}

	b.WriteString("(error " + oneLine(code))

	if refused.Record != "" {
		b.WriteString(", decision record " + oneLine(string(refused.Record)))
	}

	b.WriteString(")")

	return b.String()
}

// oneLine replaces each CR and LF of s with a space.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
