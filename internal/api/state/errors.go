package state

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	// errBadRef is the answer to a unit reference that does not parse. It
	// repeats no part of the reference.
	errBadRef = problem.BadRequest(errcode.RequestInvalidPath,
		"the unit reference in the path is not valid: write it as <tool>:<namespace>/<name>, "+
			"with each slash escaped as %2F")
	// errBadQuery is the answer to a query that does not parse or that holds a
	// name the route does not read.
	errBadQuery = problem.BadRequest(errcode.RequestInvalidQuery,
		"the query of the state read is not valid: send only version and cursor")
	// errBadPath is the refusal of the handler for a version or a cursor that
	// fails its boundary check.
	errBadPath = errors.New("state: bad path")
	// errUnitNotFound is the answer to a unit that does not exist, and to a
	// unit in a namespace that the caller cannot read.
	errUnitNotFound = errors.New("unit not found")
)

// Messages of the answers. They repeat no value of the request and no byte of
// the state.
const (
	msgBadPath = "the version or the cursor of the state read is not valid: " +
		"read the first page again without a cursor"
	msgTooLarge = "the state version is larger than 5 MB: the masked read refuses it"
	msgTimeout  = "the state read did not end within 30 seconds: try again later"
	msgNotFound = "the unit does not exist, or you cannot read its namespace"
	msgNoState  = "the state version does not exist: read the unit without a version for its current state"
	msgDenied   = "forbidden: you need the action unit:read on this unit: " +
		"ask an administrator for a grant of a role with unit:read"
)

// writeError answers err.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: keepDenied, Classify: classify}.Write(ctx, w, err)
}

// writeNoState answers the read of the current state of a unit that has no
// state: 404 with a message that names the unit.
func (h *Handler) writeNoState(ctx context.Context, w http.ResponseWriter, obj identity.Object) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: nil}.WriteProblem(ctx, w, problem.New(
		http.StatusNotFound, errcode.UnitNoState, fmt.Sprintf("the unit %s has no state yet", obj),
		errcode.Identifier("unit", obj.String()),
	))
}

// keepDenied answers the plain refusal of an action on the unit with the code
// unit.forbidden. A refusal with its own code, for example the refusal of the
// IP allowlist, keeps its code.
func keepDenied(err error) (problem.Problem, bool) {
	if errcode.Forbidden(err) != errcode.AccessDenied {
		return problem.Pass()
	}

	return problem.New(http.StatusForbidden, errcode.UnitForbidden, msgDenied), true
}

// classify maps the errors of the read to an answer that is safe to show
// callers. The shared module answers the rest.
func classify(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errBadPath), errors.Is(err, stack.ErrBadPath), errors.Is(err, workspace.ErrBadPath):
		return problem.New(http.StatusBadRequest, errcode.StateBadPath, msgBadPath), true
	case errors.Is(err, stack.ErrTooLarge), errors.Is(err, workspace.ErrTooLarge):
		return problem.New(http.StatusRequestEntityTooLarge, errcode.StateVersionTooLarge, msgTooLarge), true
	case errors.Is(err, stack.ErrReadDeadline), errors.Is(err, workspace.ErrReadDeadline),
		errors.Is(err, context.DeadlineExceeded):
		return problem.New(http.StatusGatewayTimeout, errcode.StateReadTimeout, msgTimeout), true
	case errors.Is(err, errUnitNotFound), errors.Is(err, stack.ErrNotFound), errors.Is(err, workspace.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.UnitNotFound, msgNotFound), true
	case errors.Is(err, stack.ErrNoState), errors.Is(err, stack.ErrUpdateNotFound),
		errors.Is(err, workspace.ErrNoState), errors.Is(err, workspace.ErrStateVersionNotFound):
		return problem.New(http.StatusNotFound, errcode.StateVersionNotFound, msgNoState), true
	}

	// Not an error of the read: package problem answers it.
	return problem.Pass()
}
