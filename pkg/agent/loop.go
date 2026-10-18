package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
)

// KindTurn is the kind of the durable operation of one turn. A run is a
// chain of turn operations: the last step of a turn starts the next turn in
// the transaction of its own write, or ends the run. So a turn that an
// instance stops in the middle goes on at another instance from its last
// recorded step, and a run holds no claim while it waits for nothing.
const KindTurn durable.Kind = "agent.turn"

// The steps of a turn, in order.
const (
	// StepCheck checks the cancel request, the limits, the credential of
	// the person and the model connection. A failed check ends the run.
	StepCheck = "check"
	// StepModel calls the model and records its answer and its usage.
	StepModel = "model"
	// StepTools runs the tool calls of the answer, one at a time, and
	// records each result.
	StepTools = "tools"
	// StepAdvance starts the next turn, or ends the run when the answer was
	// final.
	StepAdvance = "advance"
)

// TurnKey returns the start key of turn n of run id. Two starts of one turn
// give one operation, so a step that starts the next turn is safe to repeat.
func TurnKey(id RunID, n int) string {
	return fmt.Sprintf("agent-run/%s/turn/%d", id, n)
}

// CallKey returns the key of tool call index of turn n of run id. A call
// with an effect passes it to the Changes port, so a turn that runs again
// proposes no second change.
func CallKey(id RunID, n, index int) string {
	return fmt.Sprintf("agent-run/%s/turn/%d/call/%d", id, n, index)
}

// TurnInput is the input of a turn operation.
type TurnInput struct {
	Run  RunID `json:"run"`
	Turn int   `json:"turn"`
}

// TurnKindVersion is the version of KindTurn that this binary starts.
const TurnKindVersion = 1

// Credentials is the port that tells whether the credential that started a
// run is still valid, and resolves the person as it is now. The identity
// service implements it and runs the conformance suite in package agenttest.
// The loop calls it at the start of each step of each claimed attempt, so
// also after a takeover, and again before each tool call. The Toolset
// authorizes each call with the principal that the last call answered. A
// run never caches a principal across steps.
type Credentials interface {
	// Resolve returns the principal of subject for the credential
	// credentialID: an access token ID or a console session ID. It returns
	// ErrCredentialEnded when the credential expired, a person revoked it,
	// the session ended, the break-glass session reached its time limit, or
	// the principal is disabled. It sets Resolved.ServicePrincipal for the
	// principal of a service token.
	Resolve(ctx context.Context, subject, credentialID string) (Resolved, error)
}

// Resolved is a credential as Credentials resolved it. BreakGlass is true
// when the credential belongs to a break-glass session. Then the audit entry
// of each tool call says so, and the run ends with the session.
// ServicePrincipal is true for a principal that cannot sign in to the
// console, for example the principal of a service token. The loop copies
// both into the Actor of each tool call.
type Resolved struct {
	Principal        identity.Principal
	BreakGlass       bool
	ServicePrincipal bool
}

// TurnContext is what a step of a turn gets: the step of the durable
// operation, the input of the turn, the run as the step read it, and the
// person as Credentials resolved it at the start of the step. Resolve
// resolves the person again: the tools step calls it before each tool call
// and passes the answer to the Toolset.
type TurnContext struct {
	Step    durable.StepContext
	Input   TurnInput
	Run     Run
	Person  Resolved
	Resolve func(ctx context.Context) (Resolved, error)
}

// TurnHandler runs the four steps of a turn. The loop of the built-in agent
// implements it. A handler step that ends the run calls RunStore.Finish with
// the fence of its step. When Resolve answers ErrCredentialEnded in the
// tools step, the handler runs no further tool call and ends the run with
// StopCredentialEnded.
type TurnHandler interface {
	Check(ctx context.Context, tc *TurnContext) ([]byte, error)
	Model(ctx context.Context, tc *TurnContext) ([]byte, error)
	Tools(ctx context.Context, tc *TurnContext) ([]byte, error)
	Advance(ctx context.Context, tc *TurnContext) ([]byte, error)
}

// TurnDeps are what TurnKind needs.
type TurnDeps struct {
	Runs        RunStore
	Credentials Credentials
	Handler     TurnHandler
}

// TurnKind returns the durable kind of a turn, for durable.RunnerConfig.Kinds.
// Each step decodes the input, reads the run, does nothing for a run that
// ended, and resolves the person before it calls the handler. When the
// credential ended, the step ends the run with StopCredentialEnded through
// RunStore.Finish and does not call the handler. An input that does not
// decode or names an unknown run fails the operation for good.
func TurnKind(d TurnDeps) durable.KindSpec {
	return durable.KindSpec{
		Kind:       KindTurn,
		Version:    TurnKindVersion,
		MinVersion: TurnKindVersion,
		Steps: []durable.Step{
			d.step(StepCheck, d.Handler.Check),
			d.step(StepModel, d.Handler.Model),
			d.step(StepTools, d.Handler.Tools),
			d.step(StepAdvance, d.Handler.Advance),
		},
	}
}

func (d TurnDeps) step(name string, run func(context.Context, *TurnContext) ([]byte, error)) durable.Step {
	return durable.Step{Name: name, Run: func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
		var in TurnInput
		if err := json.Unmarshal(sc.Operation.Input, &in); err != nil || in.Run == "" || in.Turn < 1 {
			return nil, durable.Permanent(fmt.Errorf("%w: turn input", ErrInvalid))
		}

		r, err := d.Runs.Get(ctx, in.Run)

		switch {
		case errors.Is(err, ErrNotFound):
			return nil, durable.Permanent(fmt.Errorf("turn of run %s: %w", in.Run, err))
		case err != nil:
			return nil, fmt.Errorf("read run %s: %w", in.Run, err)
		case r.State.Terminal():
			return nil, nil
		default:
		}

		resolve := func(ctx context.Context) (Resolved, error) {
			return d.Credentials.Resolve(ctx, r.Subject, r.CredentialID)
		}

		person, err := resolve(ctx)
		if errors.Is(err, ErrCredentialEnded) {
			return nil, d.endCredential(ctx, sc.Fence, r.ID)
		}

		if err != nil {
			return nil, fmt.Errorf("resolve the person of run %s: %w", r.ID, err)
		}

		return run(ctx, &TurnContext{Step: sc, Input: in, Run: r, Person: person, Resolve: resolve})
	}}
}

// endCredential ends the run id with StopCredentialEnded and records the
// step of f.
func (d TurnDeps) endCredential(ctx context.Context, f durable.StepFence, id RunID) error {
	end := RunEnd{Run: id, State: StateFailed, Stop: StopCredentialEnded, LastError: ErrCredentialEnded.Error()}
	if err := d.Runs.Finish(ctx, f, end); err != nil {
		return fmt.Errorf("end run %s: %w", id, err)
	}

	return nil
}
