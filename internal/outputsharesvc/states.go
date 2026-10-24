package outputsharesvc

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// ToolStates routes the States port to the port of the tool of the unit. It is
// keyed by the tool of the unit reference. A unit of a tool with no entry is an
// unknown producer.
type ToolStates map[string]outputshare.States

var _ outputshare.States = ToolStates(nil)

// Latest implements outputshare.States.
func (t ToolStates) Latest(ctx context.Context, u identity.Object) (outputshare.State, error) {
	if !u.IsUnit() {
		return outputshare.State{}, fmt.Errorf("%w: %q is not a unit", outputshare.ErrInvalid, u)
	}

	states, ok := t[u.Tool()]
	if !ok {
		return outputshare.State{}, fmt.Errorf("%w: no state source for %q", outputshare.ErrUnknownProducer, u)
	}

	return states.Latest(ctx, u)
}
