package agent_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
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
