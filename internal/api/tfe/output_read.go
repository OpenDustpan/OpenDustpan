package tfe

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errNoOutputChecker reports a handler without the output sharing checker. The
// routes that answer outputs fail closed with it: they answer 500 and no
// output.
var errNoOutputChecker = errors.New("no output sharing checker is configured")

// clientEncryptedDetail is the detail of the 409 that a read of outputs only
// gets for a state that the client encrypted. It names the reason and no
// object.
const clientEncryptedDetail = "the client encrypted the state of the workspace, " +
	"so the server cannot answer its outputs only; read the state with unit:read"

// outputRead is one read of the outputs of a workspace.
type outputRead struct {
	obj identity.Object
	// workspace is the stable ID of the workspace whose outputs are read.
	workspace workspace.ID
	// version is the state version that the route answers.
	version workspace.StateVersionID
	// secrets is true when the answer holds a secret output.
	secrets bool
}

// authorizeOutputRead decides a read of outputs of the state version
// read.version of a workspace. The route table already needed
// unit:read-outputs on the unit. This call adds the checks that the route
// table cannot make:
//   - A read of the current state version by a principal goes through the
//     output sharing checker: the grant, the secret output, the latest state
//     and the state that the client encrypted.
//   - A read of an older state version needs unit:read, and unit:read-secrets
//     for a secret output. Old outputs can hold rotated values, so unit:read-outputs
//     does not reach them.
//   - A read with a job token keeps the checks of a whole state read, unit:read
//     and, for a secret output, unit:read-secrets, until the checker serves the
//     job path.
//
// Defense in depth: the route table checks unit:read-outputs too. This stays
// so that a route that skips its row cannot answer outputs.
func (h *Handler) authorizeOutputRead(
	ctx context.Context, r *http.Request, p identity.Principal, read outputRead,
) error {
	current, err := h.workspaces.Current(ctx, read.workspace)

	isCurrent := err == nil && current.ID == read.version
	if err != nil && !errors.Is(err, workspace.ErrNoState) {
		return fmt.Errorf("read the current state of %s: %w", read.obj, err)
	}

	if !isCurrent || p.JobTokenID() != "" {
		return h.authorizeWholeStateRead(ctx, r, p, read)
	}

	if h.outputs == nil {
		return errNoOutputChecker
	}

	req := outputshare.ReadRequest{
		Caller: p,
		Producer: outputshare.Placement{
			Unit:       read.obj.WithUnitID(read.workspace.String()),
			Collection: identity.Object{},
		},
		Attachment: outputshare.AttachmentRef{Attachment: "", Profile: "", Block: "", Revision: 0},
		Latest:     read.version.String(),
		Secrets:    read.secrets,
	}

	if _, err := h.outputs.CheckRead(ctx, req); err != nil {
		return fmt.Errorf("check the read of outputs: %w", err)
	}

	return nil
}

// authorizeWholeStateRead needs the actions of a read of the whole state:
// unit:read, and unit:read-secrets for a secret output.
func (h *Handler) authorizeWholeStateRead(
	ctx context.Context, r *http.Request, p identity.Principal, read outputRead,
) error {
	actions := []identity.Action{identity.ActionUnitRead}
	if read.secrets {
		actions = append(actions, identity.ActionUnitReadSecrets)
	}

	for _, action := range actions {
		if err := h.guard.Authorize(ctx, r, p, action, read.obj); err != nil {
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}
