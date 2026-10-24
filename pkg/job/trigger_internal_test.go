package job

import (
	"errors"
	"testing"
)

// TestCheckManualSourceRefusesAnAgent proves that the trigger check refuses
// an agent alone, without the source kind check of Start, which refuses the
// same start first.
func TestCheckManualSourceRefusesAnAgent(t *testing.T) {
	t.Parallel()

	a := new(Actor)
	a.Agent = "dusty"

	if err := checkManualSource(*a, SourceAPI); !errors.Is(err, ErrAgentDenied) {
		t.Fatalf("checkManualSource(agent, api) = %v, want %v", err, ErrAgentDenied)
	}
}
