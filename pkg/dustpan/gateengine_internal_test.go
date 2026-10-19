package dustpan

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
)

// TestWithGateEngineSetsTheEngine checks that the option keeps the engine
// that the caller gives, and that a server without the option has none.
func TestWithGateEngineSetsTheEngine(t *testing.T) {
	t.Parallel()

	if got := defaultSettings().gateEngine; got != nil {
		t.Fatalf("default gate engine = %v, want nil", got)
	}

	var engine gate.Engine = cedargo.New()

	s := defaultSettings()
	WithGateEngine(engine)(s)

	if s.gateEngine != engine {
		t.Fatalf("gate engine = %v, want the engine of the option", s.gateEngine)
	}
}
