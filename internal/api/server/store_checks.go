package server

// Store check routes of the installation API: start a deep check of a blob
// store, list the checks, and read one check with its report.
//
// POST /api/dustpan/v1/store-checks starts a check in the background and
// answers 202 with the ID. The body is {"target": "blobs"} or
// {"target": "backup"}: the request never holds an address. Only one check
// runs at a time, and a target waits 15 minutes after its last check ended;
// both answer 409. The installation in maintenance mode, or a restore that
// runs, answers 409 too. GET /api/dustpan/v1/store-checks lists the checks,
// newest first. GET /api/dustpan/v1/store-checks/{id} answers the state
// (running, succeeded or failed) and, when the check has a report, the
// report. An ID that has no check answers 404, also an ID that the server
// never makes.
//
// All three need the action installation:store-check on the installation
// (@server). A check adds load to the store and costs requests on a paid
// store.

import (
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The paths of the store check routes.
const (
	storeChecksPath = "/api/dustpan/v1/store-checks"
	storeCheckPath  = "/api/dustpan/v1/store-checks/{id}"
)

// maxStoreCheckBodyBytes bounds the start body, which holds only the target.
const maxStoreCheckBodyBytes = 1 << 10

// startStoreCheckRequest is the body of a start.
type startStoreCheckRequest struct {
	Target string `json:"target"`
}

// startStoreCheckResponse answers a started check: its ID and its state. The
// check runs in the background; poll GET /api/dustpan/v1/store-checks/{id}
// until the state leaves running.
//
// Contract: never remove or rename these fields.
type startStoreCheckResponse struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	State  string `json:"state"`
}

// storeCheckCaseJSON is the result of one conformance case. The message is
// fixed text and holds no address and no credential.
type storeCheckCaseJSON struct {
	Name    string `json:"name"`
	Message string `json:"message,omitempty"`
	Passed  bool   `json:"passed"`
}

// storeCheckLatencyJSON holds a measured latency in microseconds.
type storeCheckLatencyJSON struct {
	P50Micros int64 `json:"p50Micros"`
	P99Micros int64 `json:"p99Micros"`
}

// storeCheckReportJSON is the report of a check. Each figure is a value that
// one run measured on the store at the time of the run. It says nothing about
// the store at another time.
//
// Contract: never remove or rename these fields.
type storeCheckReportJSON struct {
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	// Prefix is the throwaway prefix that the check wrote under, without the
	// trailing slash.
	Prefix string `json:"prefix"`
	// ConditionalWrites is "enforced", "ignored" or "unknown".
	ConditionalWrites string                `json:"conditionalWrites"`
	Cases             []storeCheckCaseJSON  `json:"cases"`
	PutLatency        storeCheckLatencyJSON `json:"putLatency"`
	GetLatency        storeCheckLatencyJSON `json:"getLatency"`
	DeleteLatency     storeCheckLatencyJSON `json:"deleteLatency"`
	PutBytesPerSecond int64                 `json:"putBytesPerSecond"`
	GetBytesPerSecond int64                 `json:"getBytesPerSecond"`
	DeletesPerSecond  int64                 `json:"deletesPerSecond"`
	// ThrottlingSeen is true when a request failed with a throttling answer
	// that the retries of the adapter did not get through.
	ThrottlingSeen bool `json:"throttlingSeen"`
	// PrefixDeleted is false when the store refused the delete of the prefix,
	// or an object stayed in the listing after it.
	PrefixDeleted bool `json:"prefixDeleted"`
	Passed        bool `json:"passed"`
}

// storeCheckJSON is one check. EndedAt, Error and Report stay empty while the
// check runs.
//
// Contract: never remove or rename these fields.
type storeCheckJSON struct {
	ID          string                `json:"id"`
	Target      string                `json:"target"`
	State       string                `json:"state"`
	RequestedBy string                `json:"requestedBy"`
	StartedAt   string                `json:"startedAt"`
	EndedAt     string                `json:"endedAt,omitempty"`
	Error       string                `json:"error,omitempty"`
	Report      *storeCheckReportJSON `json:"report,omitempty"`
}

// listStoreChecksResponse lists the checks, newest first. The list never
// holds a report: read one check for its report.
type listStoreChecksResponse struct {
	StoreChecks []storeCheckJSON `json:"storeChecks"`
}

// storeCheckRoutes returns the store check rows of the route table. Without a
// service it returns none.
func (h *Handler) storeCheckRoutes() []Route {
	if h.storeChecks == nil {
		return nil
	}

	row := func(method, path string, serve RouteHandler) Route {
		return Route{
			Method: method, Path: path, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionInstallationStoreCheck},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: serve,
		}
	}

	return []Route{
		row(http.MethodPost, storeChecksPath, h.startStoreCheck),
		row(http.MethodGet, storeChecksPath, h.listStoreChecks),
		row(http.MethodGet, storeCheckPath, h.getStoreCheck),
	}
}

// startStoreCheck serves POST /api/dustpan/v1/store-checks.
func (h *Handler) startStoreCheck(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	var req startStoreCheckRequest

	body, err := readBodyUpTo(w, r, maxStoreCheckBodyBytes, errInvalidBody)
	if err == nil {
		err = decodeBody(body, &req, errInvalidBody)
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// A check is a durable operation that a worker runs: with no worker, the
	// start answers 503 before it stores anything. A target that is not on the
	// list skips the guard, so that the service answers it and writes the
	// denial entry.
	if _, parseErr := storecheck.ParseTarget(req.Target); parseErr == nil && !h.admitStart(ctx, w) {
		return
	}

	entry := callerEntry(r, p, identity.ActionInstallationStoreCheck)

	id, err := h.storeChecks.Start(ctx, storecheck.StartRequest{
		Target: req.Target,
		Actor:  storecheck.Actor{Subject: p.Subject, TokenID: p.TokenID, PrincipalKind: string(p.Kind)},
		Entry:  entry,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusAccepted, startStoreCheckResponse{
		ID: id.String(), Target: req.Target, State: string(storecheck.StateRunning),
	})
}

// listStoreChecks serves GET /api/dustpan/v1/store-checks.
func (h *Handler) listStoreChecks(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	records, err := h.storeChecks.List(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := listStoreChecksResponse{StoreChecks: make([]storeCheckJSON, 0, len(records))}
	for i := range records {
		out.StoreChecks = append(out.StoreChecks, storeCheckSummary(records[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// getStoreCheck serves GET /api/dustpan/v1/store-checks/{id}.
func (h *Handler) getStoreCheck(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	// An ID that the server never makes has no check. It answers 404 like an
	// ID that is unknown, so the answer shows nothing about the form of an ID.
	id, err := storecheck.ParseID(r.PathValue("id"))
	if err != nil {
		h.writeError(ctx, w, storecheck.ErrNotFound)

		return
	}

	rec, err := h.storeChecks.Get(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc := storeCheckSummary(rec)

	report, ok, err := storecheck.DecodeReport(rec)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if ok {
		doc.Report = storeCheckReportOf(&report)
	}

	h.writeJSON(ctx, w, http.StatusOK, doc)
}

// storeCheckSummary renders a row without its report.
func storeCheckSummary(rec storecheck.Record) storeCheckJSON {
	doc := storeCheckJSON{
		ID: rec.ID.String(), Target: string(rec.Target), State: string(rec.State), RequestedBy: rec.RequestedBy,
		StartedAt: wireTime(rec.StartedAt), EndedAt: "", Error: rec.Error, Report: nil,
	}

	if !rec.EndedAt.IsZero() {
		doc.EndedAt = wireTime(rec.EndedAt)
	}

	return doc
}

func storeCheckReportOf(report *storecheck.Report) *storeCheckReportJSON {
	cases := make([]storeCheckCaseJSON, 0, len(report.Cases))
	for _, c := range report.Cases {
		cases = append(cases, storeCheckCaseJSON{Name: c.Name, Message: c.Message, Passed: c.Passed})
	}

	latency := func(l storecheck.Latency) storeCheckLatencyJSON {
		return storeCheckLatencyJSON{P50Micros: l.P50.Microseconds(), P99Micros: l.P99.Microseconds()}
	}

	return &storeCheckReportJSON{
		StartedAt: wireTime(report.StartedAt), FinishedAt: wireTime(report.FinishedAt), Prefix: report.Prefix.String(),
		ConditionalWrites: string(report.ConditionalWrites), Cases: cases, PutLatency: latency(report.PutLatency),
		GetLatency: latency(report.GetLatency), DeleteLatency: latency(report.DeleteLatency),
		PutBytesPerSecond: report.PutBytesPerSecond, GetBytesPerSecond: report.GetBytesPerSecond,
		DeletesPerSecond: report.DeletesPerSecond, ThrottlingSeen: report.ThrottlingSeen,
		PrefixDeleted: report.PrefixDeleted, Passed: report.Passed,
	}
}

// classifyStoreCheck maps the errors of the store checks. Each text is fixed:
// the error of a store can hold an address, so it never reaches an answer.
func classifyStoreCheck(err error) (failure, bool) {
	if errors.Is(err, storecheck.ErrInvalidTarget) {
		return failCoded(http.StatusBadRequest, `invalid target: use "blobs" or "backup"`,
			errcode.StoreCheckInvalidTarget), true
	}

	if errors.Is(err, storecheck.ErrNotFound) {
		return failCoded(http.StatusNotFound, "store check not found: GET /api/dustpan/v1/store-checks lists the IDs",
			errcode.StoreCheckNotFound), true
	}

	return classifyStoreCheckConflict(err)
}

// classifyStoreCheckConflict maps the refusals of a start, which answer 409.
func classifyStoreCheckConflict(err error) (failure, bool) {
	if errors.Is(err, storecheck.ErrNoTarget) {
		return failCoded(http.StatusConflict, "this server has no store for the target: use another target",
			errcode.StoreCheckNoTarget), true
	}

	if errors.Is(err, storecheck.ErrRunning) {
		return failCoded(http.StatusConflict, "a store check is running: wait until it ends, then start again",
			errcode.StoreCheckRunning), true
	}

	if errors.Is(err, storecheck.ErrCooldown) {
		return failCoded(http.StatusConflict,
			"a store check on this target ended less than 15 minutes ago: wait, then start again",
			errcode.StoreCheckCooldown), true
	}

	if errors.Is(err, storecheck.ErrRefused) {
		return failCoded(http.StatusConflict,
			"the installation is in maintenance mode or a restore runs: wait until it ends, then start again",
			errcode.StoreCheckRefused), true
	}

	return fail(0, ""), false
}
