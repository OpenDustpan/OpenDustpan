package triggers

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestCycleAnswerNamesOnlyReadableUnits sends cycle errors straight to the
// answer builders, as if the manager had not marked the units: a cycle error
// with no marks names no unit, and a marked one names only the readable units.
func TestCycleAnswerNamesOnlyReadableUnits(t *testing.T) {
	t.Parallel()

	a := graph.Unit{Namespace: "acme", Tool: "pulumi", ID: "ws-a"}
	hidden := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-hidden"}
	path := []graph.Unit{a, hidden, a}
	names := map[graph.Unit]string{a: "app", hidden: "secret-net"}

	for _, tc := range []struct {
		name      string
		readable  map[graph.Unit]bool
		wantUnits int
		wantText  string
	}{
		{name: "no marks", readable: nil, wantUnits: 0, wantText: "trigger closes a loop of units"},
		{
			name: "one readable", readable: map[graph.Unit]bool{a: true}, wantUnits: 2,
			wantText: "trigger closes a loop of units: app -> 1 unit that you cannot read -> app",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cycle := &trigger.CycleError{Path: path, Names: names, Readable: tc.readable}

			units := cycleUnits(cycle)
			if len(units) != tc.wantUnits || slices.ContainsFunc(units, func(u unitResponse) bool {
				return u.ID == hidden.ID || u.Name == "secret-net"
			}) {
				t.Errorf("units = %+v, want %d units and none that the caller cannot read", units, tc.wantUnits)
			}

			msg := cycleMessage(cycle)
			if msg != tc.wantText || strings.Contains(msg, "ws-hidden") || strings.Contains(msg, "secret-net") {
				t.Errorf("message = %q, want %q", msg, tc.wantText)
			}
		})
	}
}
