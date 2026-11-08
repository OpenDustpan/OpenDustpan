package server

// Maintenance routes of the installation API (ADR 38 decision 4): read the
// installation state, enter maintenance mode, and leave it.
//
// GET /api/dustpan/v1/maintenance answers the state (normal or maintenance).
// POST /api/dustpan/v1/maintenance/enter moves to maintenance mode: while an
// update or a run is active it answers 409 naming each one, unless the body
// sets force, which cancels them first and names them in the answer.
// POST /api/dustpan/v1/maintenance/leave moves back to normal operation.
//
// All three need the action server:maintenance on the installation (@server).
// The restore of a later issue enters and leaves through the same service,
// so operators and the restore share one mechanism.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// Paths of the maintenance routes of the installation API.
const (
	maintenancePath      = "/api/dustpan/v1/maintenance"
	maintenanceEnterPath = "/api/dustpan/v1/maintenance/enter"
	maintenanceLeavePath = "/api/dustpan/v1/maintenance/leave"
)

// errInvalidBody reports a request body that is not valid JSON. classify maps
// it to the 400 response the installation API answers.
var errInvalidBody = errors.New("invalid JSON body")

// maxEnterBodyBytes bounds the enter-maintenance body, which holds only the
// force flag.
const maxEnterBodyBytes = 4 << 10

// Maintenance enters, leaves and reads maintenance mode. The server wires
// the domain service; tests stub the three calls.
type Maintenance interface {
	// Get returns the installation state.
	Get(ctx context.Context) (maintenance.State, error)
	// Enter moves the installation to maintenance mode. While an update or
	// a run is active it reports maintenance.ErrActiveWork naming each one,
	// unless force cancels them first. Work that starts during a forced
	// enter makes it report the same error; a retry cancels that work.
	Enter(ctx context.Context, force bool) (maintenance.EnterResult, error)
	// Leave moves the installation back to normal operation.
	Leave(ctx context.Context) error
}

// maintenanceStateResponse answers the installation state.
type maintenanceStateResponse struct {
	State string `json:"state"`
}

// enterMaintenanceRequest asks to enter maintenance mode. Force cancels the
// active updates and runs first and names them in the answer.
type enterMaintenanceRequest struct {
	Force bool `json:"force"`
}

// enterMaintenanceResponse answers an enter: the state and the unit
// references it canceled, empty when it canceled none.
type enterMaintenanceResponse struct {
	State    string   `json:"state"`
	Canceled []string `json:"canceled"`
}

// enterRefusedResponse answers an enter that gave up with work that kept
// starting: the error shape, and the unit references that the forced attempts
// canceled before it gave up.
type enterRefusedResponse struct {
	Code      int            `json:"code"`
	Message   string         `json:"message"`
	Canceled  []string       `json:"canceled"`
	ErrorCode string         `json:"errorCode,omitempty"`
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
}

// getMaintenance answers the installation state.
func (h *Handler) getMaintenance(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	state, err := h.maintenance.Get(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, maintenanceStateResponse{State: state.String()})
}

// enterMaintenance moves the installation to maintenance mode. An empty body
// means no force. While an update or a run is active it answers 409 naming
// each one; with force it cancels them first and names them in the answer.
// When work keeps starting and a forced enter gives up, the 409 also lists
// the work it canceled.
func (h *Handler) enterMaintenance(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	req, err := parseEnterRequest(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	res, err := h.maintenance.Enter(ctx, req.Force)
	if err != nil {
		h.writeEnterError(ctx, w, err)

		return
	}

	canceled := res.Canceled
	if canceled == nil {
		canceled = []string{}
	}

	h.writeJSON(ctx, w, http.StatusOK, enterMaintenanceResponse{
		State:    maintenance.StateMaintenance.String(),
		Canceled: canceled,
	})
}

// writeEnterError answers a failed enter. A forced enter that canceled work
// before it failed names that work in the answer, whether it gave up with
// work that kept starting or failed on another error, so the caller knows
// what stopped; any other error uses the common answer.
func (h *Handler) writeEnterError(ctx context.Context, w http.ResponseWriter, err error) {
	var canceled []string

	if blocked, ok := errors.AsType[*maintenance.ActiveWorkError](err); ok {
		canceled = blocked.Canceled
	} else if failed, ok := errors.AsType[*maintenance.CanceledError](err); ok {
		canceled = failed.Canceled
	}

	// A denial or a refusal of the credential answers first, as in writeError,
	// so the list of canceled work never reaches a caller that has no right.
	if _, denied := deniedFailure(err); denied || len(canceled) == 0 {
		h.writeError(ctx, w, err)

		return
	}

	f := classify(err)

	// Defense in depth: writeError sets the same header for the same errors;
	// this answer uses its own writer, so it sets the header here too.
	if errors.Is(err, maintenance.ErrInMaintenance) || errors.Is(err, maintenance.ErrBusy) {
		w.Header().Set("Retry-After", strconv.FormatInt(problem.RetryAfterSeconds(maintenanceRetryAfter), 10))
	}

	if f.code == "" {
		h.writeJSON(ctx, w, f.status, enterRefusedResponse{
			Code: f.status, Message: f.message, Canceled: canceled, ErrorCode: "", ErrorArgs: nil,
		})

		return
	}

	status, message, answer := h.answer(ctx, f)

	h.writeJSON(ctx, w, status, enterRefused(status, message, canceled, answer))
}

// enterRefused returns the refusal of an enter that canceled work, with the
// code and the arguments of a.
func enterRefused(status int, message string, canceled []string, a errcode.Answer) enterRefusedResponse {
	return enterRefusedResponse{
		Code: status, Message: message, Canceled: canceled, ErrorCode: a.Code(), ErrorArgs: a.Args(),
	}
}

// parseEnterRequest decodes the enter body: the whole body must be one JSON
// value, or nothing for no force.
func parseEnterRequest(w http.ResponseWriter, r *http.Request) (enterMaintenanceRequest, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnterBodyBytes))
	if err != nil {
		return enterMaintenanceRequest{Force: false}, errInvalidBody
	}

	if len(body) == 0 {
		return enterMaintenanceRequest{Force: false}, nil
	}

	var req enterMaintenanceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return enterMaintenanceRequest{Force: false}, errInvalidBody
	}

	return req, nil
}

// leaveMaintenance moves the installation back to normal operation.
func (h *Handler) leaveMaintenance(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if err := h.maintenance.Leave(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, maintenanceStateResponse{State: maintenance.StateNormal.String()})
}
