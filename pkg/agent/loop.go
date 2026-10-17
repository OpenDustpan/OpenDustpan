package agent

import (
	"context"
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
	Run  RunID
	Turn int
}

// Credentials is the port that tells whether the credential that started a
// run is still valid, and resolves the person as it is now. The identity
// service implements it. The loop calls it at the check step of each turn,
// and the Toolset authorizes each call with the principal that it answers.
type Credentials interface {
	// Resolve returns the principal of subject for the credential
	// credentialID: an access token ID or a console session ID. It returns
	// ErrCredentialEnded when the credential expired, a person revoked it,
	// the session ended, the break-glass session reached its time limit, or
	// the principal is disabled.
	Resolve(ctx context.Context, subject, credentialID string) (Resolved, error)
}

// Resolved is a credential as Credentials resolved it. BreakGlass is true
// when the credential belongs to a break-glass session. Then the audit entry
// of each tool call says so, and the run ends with the session.
type Resolved struct {
	Principal  identity.Principal
	BreakGlass bool
}
