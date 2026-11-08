package server

import (
	"context"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
)

// UnclaimedOperation is a durable operation that no worker holds. Seconds is
// how long it has waited.
type UnclaimedOperation struct {
	ID      string
	Kind    string
	State   string
	Seconds float64
}

// DueSchedule is a schedule whose tick is due. LateSeconds is how long ago
// the tick fell due.
type DueSchedule struct {
	Name        string
	LateSeconds float64
}

// WorkerReport is what the installation API reports about the worker
// component: the time since the last worker heartbeat, each due schedule and
// each unclaimed operation older than 2 lease periods. Stuck is true when no
// worker claimed or renewed a claim for more than 2 lease periods. The due
// schedules and the unclaimed operations are information only.
type WorkerReport struct {
	LeaseSeconds float64
	// SilentSeconds is the time since a worker last claimed or renewed a
	// claim, on the clock of the store.
	SilentSeconds          float64
	OldestUnclaimedSeconds float64
	Stuck                  bool
	Operations             []UnclaimedOperation
	Schedules              []DueSchedule
}

// WorkerSource reports the components of this process and the state of the
// worker. It is defined here, in the package that consumes it.
type WorkerSource interface {
	// Components answers the names of the components this process runs.
	Components() []string
	// WorkerReport answers the due schedules and the unclaimed operations.
	WorkerReport(ctx context.Context) (WorkerReport, error)
	admit.Source
}

// admitStart answers false and writes the refusal when no worker claimed
// work for 2 lease periods of the worker. It admits every start when no
// source is wired. Every route that inserts a durable operation calls it
// before the insert.
func (h *Handler) admitStart(ctx context.Context, w http.ResponseWriter) bool {
	if err := admit.Start(ctx, h.workers); err != nil {
		h.writeError(ctx, w, err)

		return false
	}

	return true
}

// componentsResponse lists the components of the process that answers.
//
// Contract: never remove or rename these fields. The console reads only
// this API, and it updates on its own schedule.
type componentsResponse struct {
	Components []string `json:"components"`
}

type unclaimedOperationJSON struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	State   string  `json:"state"`
	Seconds float64 `json:"seconds"`
}

type dueScheduleJSON struct {
	Name        string  `json:"name"`
	LateSeconds float64 `json:"lateSeconds"`
}

type workerResponse struct {
	Components             []string                 `json:"components"`
	LeaseSeconds           float64                  `json:"leaseSeconds"`
	SilentSeconds          float64                  `json:"silentSeconds"`
	OldestUnclaimedSeconds float64                  `json:"oldestUnclaimedSeconds"`
	Stuck                  bool                     `json:"stuck"`
	Operations             []unclaimedOperationJSON `json:"operations"`
	Schedules              []dueScheduleJSON        `json:"schedules"`
}

// workerRoutes returns the component rows of the route table.
func (h *Handler) workerRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/server/components",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerObserve},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.getComponents,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/worker",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerObserve},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.getWorker,
		},
	}
}

// getComponents serves GET /api/server/components: the components of the
// process that answers. It needs the action server:observe.
func (h *Handler) getComponents(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	names := []string{}
	if h.workers != nil {
		names = h.workers.Components()
	}

	h.writeJSON(r.Context(), w, http.StatusOK, componentsResponse{Components: names})
}

// getWorker serves GET /api/server/worker: the due schedules and the
// operations that no worker claimed for more than 2 lease periods. It needs
// the action server:observe.
func (h *Handler) getWorker(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	body := workerResponse{
		Components: []string{}, LeaseSeconds: 0, SilentSeconds: 0, OldestUnclaimedSeconds: 0, Stuck: false,
		Operations: []unclaimedOperationJSON{}, Schedules: []dueScheduleJSON{},
	}

	if h.workers != nil {
		report, err := h.workers.WorkerReport(ctx)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		body.Components = h.workers.Components()
		body.LeaseSeconds = report.LeaseSeconds
		body.SilentSeconds = report.SilentSeconds
		body.OldestUnclaimedSeconds = report.OldestUnclaimedSeconds
		body.Stuck = report.Stuck

		for _, op := range report.Operations {
			body.Operations = append(body.Operations, unclaimedOperationJSON(op))
		}

		for _, sch := range report.Schedules {
			body.Schedules = append(body.Schedules, dueScheduleJSON(sch))
		}
	}

	h.writeJSON(ctx, w, http.StatusOK, body)
}

// classifyWorker maps ErrNoWorker to 503 with the command that helps.
func classifyWorker(err error) (failure, bool) {
	if errors.Is(err, admit.ErrNoWorker) {
		return failCoded(http.StatusServiceUnavailable, admit.Message, errcode.WorkerAbsent), true
	}

	return fail(0, ""), false
}
