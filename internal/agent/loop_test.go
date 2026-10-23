package agent_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/durable"
)

func TestKeysAreDistinct(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}
	add := func(what, key string) {
		t.Helper()

		if other, ok := seen[key]; ok {
			t.Fatalf("%s and %s have one key %q", what, other, key)
		}

		seen[key] = what
	}

	for _, run := range []agent.RunID{"r1", "r12"} {
		for turn := 1; turn <= 12; turn++ {
			add("turn", agent.TurnKey(run, turn))

			for call := range 3 {
				add("call", agent.CallKey(run, turn, call))
			}
		}
	}
}

func TestTurnKeyIsStable(t *testing.T) {
	t.Parallel()

	// The key is stored with each operation: a change of its form makes an
	// operation that already started start a second time.
	if got, want := agent.TurnKey("r1", 2), "agent-run/r1/turn/2"; got != want {
		t.Fatalf("TurnKey() = %q, want %q", got, want)
	}
}

// noHandler is a turn handler whose steps fail the test.
type noHandler struct{ t *testing.T }

func (h noHandler) Check(ctx context.Context, tc *agent.TurnContext) ([]byte, error) {
	return h.step(ctx, tc)
}

func (h noHandler) Model(ctx context.Context, tc *agent.TurnContext) ([]byte, error) {
	return h.step(ctx, tc)
}

func (h noHandler) Tools(ctx context.Context, tc *agent.TurnContext) ([]byte, error) {
	return h.step(ctx, tc)
}

func (h noHandler) Advance(ctx context.Context, tc *agent.TurnContext) ([]byte, error) {
	return h.step(ctx, tc)
}

func (h noHandler) step(context.Context, *agent.TurnContext) ([]byte, error) {
	h.t.Error("the handler ran for an input that is not valid")

	return nil, nil
}

func TestTurnKind(t *testing.T) {
	t.Parallel()

	kind := agent.TurnKind(agent.TurnDeps{Runs: nil, Credentials: nil, Handler: noHandler{t: t}})

	names := make([]string, 0, len(kind.Steps))
	for _, st := range kind.Steps {
		names = append(names, st.Name)
	}

	want := []string{agent.StepCheck, agent.StepModel, agent.StepTools, agent.StepAdvance}
	if kind.Kind != agent.KindTurn || kind.Version != agent.TurnKindVersion || !slices.Equal(names, want) {
		t.Fatalf("TurnKind() = %s v%d %v, want %s v%d %v", kind.Kind, kind.Version, names, agent.KindTurn,
			agent.TurnKindVersion, want)
	}

	for _, input := range []string{`{`, `{"run":"","turn":1}`, `{"run":"r1","turn":0}`} {
		var sc durable.StepContext

		sc.Operation.Input = []byte(input)

		_, err := kind.Steps[0].Run(t.Context(), sc)
		if !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("step of input %s = %v, want ErrInvalid", input, err)
		}
	}
}
