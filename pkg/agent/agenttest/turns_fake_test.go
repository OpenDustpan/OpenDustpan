package agenttest_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/agent/agenttest"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
)

func TestTurnsSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunTurns(t, func(t *testing.T) agenttest.TurnHarness {
		t.Helper()

		ops := durabletest.NewMem().Store()
		runs := newFakeRuns(ops)
		creds := newFakeCredentials()
		h := &fakeTurnHandler{runs: runs, calls: atomic.Int64{}}

		return agenttest.TurnHarness{
			Kind: agent.TurnKind(agent.TurnDeps{Runs: runs, Credentials: creds, Handler: h}),
			Runs: runs,
			Ops:  ops,
			Start: func(t *testing.T) agent.Run {
				t.Helper()

				subject := creds.add(false, false)
				req := agenttest.NewRunFor(subject, "k1")
				req.CredentialID = credentialOf(subject)

				run, _, err := runs.Create(t.Context(), req)
				if err != nil {
					t.Fatalf("Create() = %v", err)
				}

				return run
			},
			Revoke:    func(_ *testing.T, run agent.Run) { creds.end(run.CredentialID) },
			ToolCalls: func() int { return int(h.calls.Load()) },
		}
	})
}

// fakeTurnHandler is a loop whose model always answers one tool call. Its
// tools step resolves the person before the call, as the port asks.
type fakeTurnHandler struct {
	runs  agent.RunStore
	calls atomic.Int64
}

func (*fakeTurnHandler) Check(context.Context, *agent.TurnContext) ([]byte, error) { return nil, nil }

func (*fakeTurnHandler) Model(context.Context, *agent.TurnContext) ([]byte, error) { return nil, nil }

func (h *fakeTurnHandler) Tools(ctx context.Context, tc *agent.TurnContext) ([]byte, error) {
	_, err := tc.Resolve(ctx)
	if errors.Is(err, agent.ErrCredentialEnded) {
		end := agent.RunEnd{Run: tc.Run.ID, State: agent.StateFailed, Stop: agent.StopCredentialEnded, LastError: ""}
		if endErr := h.runs.Finish(ctx, tc.Step.Fence, end); endErr != nil {
			return nil, fmt.Errorf("end the run: %w", endErr)
		}

		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}

	h.calls.Add(1)

	return nil, nil
}

func (*fakeTurnHandler) Advance(context.Context, *agent.TurnContext) ([]byte, error) { return nil, nil }
