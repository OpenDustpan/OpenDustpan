package pulumi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

const (
	// updateTokenScheme is the Authorization scheme the Pulumi CLI uses for
	// the lease token that authorizes writes to a running update. It replaces
	// the access token on the renew, event and complete routes.
	updateTokenScheme = "update-token "
	// maxEventBatchBytes bounds one engine event batch. The CLI streams small
	// batches, so this is generous; larger bodies answer 413.
	maxEventBatchBytes = 8 << 20
)

// updateProgramOptions holds the create body option this server reads:
// whether the update is a dry run.
type updateProgramOptions struct {
	DryRun bool `json:"dryRun"`
}

// updateProgramRequest is the part of the CLI's create body this server
// reads: whether the update is a dry run. The CLI runs the preview phase of
// `up`, `refresh` and `destroy` with the real kind and dry run set, so the
// kind alone does not say whether an update changes state. Every other field
// is ignored.
type updateProgramRequest struct {
	Options updateProgramOptions `json:"options"`
}

// updateProgramResponse answers a create: the opaque ID of the update.
type updateProgramResponse struct {
	UpdateID string `json:"updateID"`
}

// startUpdateResponse answers a start: the stack version the update produces
// and the lease token its client must present on every later write. The
// journal version is left at zero, so journaling stays off.
type startUpdateResponse struct {
	TokenExpiration int64  `json:"tokenExpiration,omitempty"`
	Token           string `json:"token,omitempty"`
	Version         int    `json:"version"`
}

// renewLeaseRequest asks to extend an update's lease by Duration seconds.
type renewLeaseRequest struct {
	Duration int `json:"duration"`
}

// renewLeaseResponse answers a renewal. Renewal keeps the token, and the
// server stores only its digest, so the answer echoes the presented token.
type renewLeaseResponse struct {
	TokenExpiration int64  `json:"tokenExpiration,omitempty"`
	Token           string `json:"token"`
}

// engineEventBatch is a group of engine events the CLI posts during an
// update. Each event stays opaque: only its sequence and timestamp are read,
// the rest is stored as sent.
type engineEventBatch struct {
	Events []json.RawMessage `json:"events"`
}

// completeUpdateRequest ends an update with the status its client reports.
// Outputs and secrets providers are ignored in milestone 1.
type completeUpdateRequest struct {
	Status string `json:"status"`
}

// checkpointRequest is the whole checkpoint the CLI saves during an update:
// the deployment in the schema named by version, with its features. Without
// the delta capability the CLI sends no sequence number, so every write
// replaces the last one.
type checkpointRequest struct {
	IsInvalid  bool            `json:"isInvalid"`
	Version    int             `json:"version"`
	Features   []string        `json:"features,omitempty"`
	Deployment json.RawMessage `json:"deployment,omitempty"`
}

// importStackRequest is one deployment as `stack import` sends it: the
// export shape with the schema version and the opaque deployment. The CLI
// may gzip the body.
type importStackRequest struct {
	Deployment json.RawMessage `json:"deployment,omitempty"`
	Version    int             `json:"version,omitempty"`
	Features   []string        `json:"features,omitempty"`
}

// importStackResponse answers an import with the ID of the import update.
// The server already ended the update; the CLI polls it until it ends.
type importStackResponse struct {
	UpdateID string `json:"updateId"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
}

// updateResultEvent is one entry of the legacy event list. Engine events are
// write-only in milestone 1, so the list always stays empty.
type updateResultEvent struct{}

// importStack replaces the stack's state with the deployment the client
// sends, as `stack import` does. It runs as an update of kind import, so it
// shows in history and respects the stack lock: while another update runs it
// answers 409. The server always ends the update that it started. It records
// the caller as the creator of the import update.
func (h *Handler) importStack(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	limit := int64(h.updates.MaxCheckpointBytes())
	releaseHold, ok := h.hold(w, r, limit)
	if !ok {
		return
	}

	defer releaseHold()

	var req importStackRequest
	if !h.decodeLargeBody(w, r, limit, &req) {
		return
	}

	in := stack.CheckpointInput{
		Features:      req.Features,
		Deployment:    req.Deployment,
		SchemaVersion: req.Version,
		Sequence:      0,
		Invalid:       false,
	}

	u, err := h.updates.Import(ctx, ref, p.Subject, in)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, importStackResponse{UpdateID: u.ID.String()})
}

// restoreStackResponse answers a restore with the ID of the restore update.
// The server already ended the update; the caller reads it from history.
type restoreStackResponse struct {
	UpdateID string `json:"updateId"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
}

// restoreStack copies the checkpoint of one stack version into a new update
// of kind restore, as the restore route does. It follows the rules of
// importStack: the restore update shows in history with RestoredFrom set and
// respects the stack lock, answering 409 while another update runs. A version
// with no checkpoint answers 404. The version is a positive integer: any
// other value answers 400. The update records the subject of the caller as
// its creator (ADR 28 decision 5).
func (h *Handler) restoreStack(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	version, err := stack.ParseStackVersion(r.PathValue("version"))
	if err != nil || version <= 0 {
		if err == nil {
			err = fmt.Errorf("%w: stack version %d", stack.ErrInvalidUpdate, version)
		}

		h.writeError(ctx, w, err)

		return
	}

	u, err := h.updates.Restore(ctx, ref, version, p.Subject)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, restoreStackResponse{UpdateID: u.ID.String()})
}

// updateResults answers a get of one update. Engine events are write-only in
// milestone 1: the legacy shape cannot carry them, so the list stays empty.
type updateResults struct {
	Status string              `json:"status"`
	Events []updateResultEvent `json:"events"`
}

// updateInfo is one entry of the stack history. Previews never appear: the
// history holds started mutating updates only. RequestedBy is the subject of
// the principal that created the update, never a token. CheckpointBytes is 0
// when the update saved no checkpoint. LeaseExpiresAt is the lease expiry in
// Unix seconds while the update runs, and 0 once it ended. RestoredFrom is
// the stack version a restore copied, and 0 for any other update.
type updateInfo struct {
	Result          string `json:"result"`
	Kind            string `json:"kind"`
	Version         int    `json:"version"`
	StartTime       int64  `json:"startTime"`
	EndTime         int64  `json:"endTime"`
	UpdateID        string `json:"updateID"`
	RequestedBy     string `json:"requestedBy"`
	CheckpointBytes int64  `json:"checkpointBytes"`
	LeaseExpiresAt  int64  `json:"leaseExpiresAt"`
	RestoredFrom    int64  `json:"restoredFrom"`
}

// historyResponse answers the stack history, newest first.
type historyResponse struct {
	Updates []updateInfo `json:"updates"`
}

// exportedDeployment is the stack's state as the CLI expects it: the schema
// version and the opaque deployment.
type exportedDeployment struct {
	Deployment json.RawMessage `json:"deployment,omitempty"`
	Version    int             `json:"version,omitempty"`
}

const (
	// emptyDeploymentVersion is the schema version answered for a stack with
	// no state: the version the CLI itself writes for new deployments.
	emptyDeploymentVersion = 3
	// nullDeployment is the deployment answered for a stack with no state.
	nullDeployment = "null"
)

// createUpdateHandler returns the handler that creates an update of kind.
// The five kinds share this shape; each has its own route.
func (h *Handler) createUpdateHandler(kind stack.UpdateKind) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
		h.createUpdate(w, r, kind, p.Subject)
	}
}

// createUpdate records a new update without locking the stack. subject is
// the caller, which the update records as its creator (ADR 28 decision 4).
func (h *Handler) createUpdate(w http.ResponseWriter, r *http.Request, kind stack.UpdateKind, subject string) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	body, ok := h.readBody(w, r, maxBodyBytes)
	if !ok {
		return
	}

	var req updateProgramRequest

	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			h.writeInvalidBody(ctx, w)

			return
		}
	}

	u, err := h.updates.Create(ctx, ref, stack.Plan{
		Kind: kind, DryRun: req.Options.DryRun, CreatedBy: subject, RestoredFrom: 0,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, updateProgramResponse{UpdateID: u.ID.String()})
}

// startUpdate starts the update and hands out its lease. The request tags
// are ignored in milestone 1: they only annotate the stack.
func (h *Handler) startUpdate(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	started, err := h.updates.Start(ctx, ref, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, startUpdateResponse{
		TokenExpiration: started.Update.Lease.ExpiresAt.Unix(),
		Token:           started.Token.Reveal(),
		Version:         int(started.Update.Version),
	})
}

// renewLease extends the update's lease. The lease token arrives in the
// Authorization header, not in the deprecated body field.
func (h *Handler) renewLease(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	tok, raw, ok := h.leaseToken(w, r)
	if !ok {
		return
	}

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	body, ok := h.readBody(w, r, maxBodyBytes)
	if !ok {
		return
	}

	var req renewLeaseRequest

	if err := json.Unmarshal(body, &req); err != nil {
		h.writeInvalidBody(ctx, w)

		return
	}

	u, err := h.updates.Renew(ctx, ref, id, tok, time.Duration(req.Duration)*time.Second)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, renewLeaseResponse{TokenExpiration: u.Lease.ExpiresAt.Unix(), Token: raw})
}

// recordEvents stores a batch of engine events for the update. Retried
// events repeat their sequence and are stored once.
func (h *Handler) recordEvents(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	tok, _, ok := h.leaseToken(w, r)
	if !ok {
		return
	}

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	events, ok := h.readEvents(w, r)
	if !ok {
		return
	}

	if err := h.updates.AppendEvents(ctx, ref, id, tok, events); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, struct{}{})
}

// completeUpdate ends the update with the status its client reports.
// Repeating the status the update already ended with succeeds.
func (h *Handler) completeUpdate(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	tok, _, ok := h.leaseToken(w, r)
	if !ok {
		return
	}

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	body, ok := h.readBody(w, r, maxBodyBytes)
	if !ok {
		return
	}

	var req completeUpdateRequest

	if err := json.Unmarshal(body, &req); err != nil {
		h.writeInvalidBody(ctx, w)

		return
	}

	status, err := stack.ParseCompletion(req.Status)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := h.updates.Complete(ctx, ref, id, tok, status); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, struct{}{})
}

// cancelUpdate ends the update without its lease, as `pulumi cancel` does.
// It is the only way to release a stack whose client vanished. Canceling an
// already canceled update succeeds. The CLI authorizes with its access token,
// so this route needs no lease token.
func (h *Handler) cancelUpdate(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	if _, err := h.updates.Cancel(ctx, ref, id); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, struct{}{})
}

// saveCheckpoint stores the running update's checkpoint; the last write
// wins. The lease token in the Authorization header authorizes the write. A
// checkpoint over the size limit answers 413. The CLI may gzip the body. The
// limit bounds the whole wire body here; the domain bounds the deployment
// inside it, so a body just over the limit answers 413 here even when its
// deployment alone would fit.
func (h *Handler) saveCheckpoint(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	tok, _, ok := h.leaseToken(w, r)
	if !ok {
		return
	}

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	limit := int64(h.updates.MaxCheckpointBytes())
	releaseHold, ok := h.hold(w, r, limit)
	if !ok {
		return
	}

	defer releaseHold()

	var req checkpointRequest
	if !h.decodeLargeBody(w, r, limit, &req) {
		return
	}

	in := stack.CheckpointInput{
		Features:      req.Features,
		Deployment:    req.Deployment,
		SchemaVersion: req.Version,
		Sequence:      0,
		Invalid:       req.IsInvalid,
	}

	if err := h.updates.SaveCheckpoint(ctx, ref, id, tok, in); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, struct{}{})
}

// getUpdate returns one update: its status in the CLI's wire values.
func (h *Handler) getUpdate(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, id, ok := h.updateTarget(w, r)
	if !ok {
		return
	}

	u, err := h.updates.Get(ctx, ref, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, updateResults{Status: string(u.Status), Events: []updateResultEvent{}})
}

// listHistory returns the stack's started mutating updates, newest first.
func (h *Handler) listHistory(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	page, err := historyPage(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	list, err := h.updates.History(ctx, ref, page)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	infos := make([]updateInfo, 0, len(list))
	for i := range list {
		infos = append(infos, updateInfo{
			Result:          updateResult(list[i].Status),
			Kind:            string(list[i].Kind),
			Version:         int(list[i].Version),
			StartTime:       list[i].StartedAt.Unix(),
			EndTime:         list[i].EndedAt.Unix(),
			UpdateID:        list[i].ID.String(),
			RequestedBy:     list[i].CreatedBy,
			CheckpointBytes: list[i].CheckpointBytes,
			LeaseExpiresAt:  leaseExpiresAt(list[i]),
			RestoredFrom:    list[i].RestoredFrom,
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, historyResponse{Updates: infos})
}

// exportStack returns the stack's latest checkpoint, or an empty deployment
// when the stack has no state yet. A preview needs the base snapshot before
// it runs, so this route serves it on every stack.
func (h *Handler) exportStack(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	cp, err := h.updates.Export(ctx, ref)
	if err != nil {
		if errors.Is(err, stack.ErrNoState) {
			h.writeJSON(ctx, w, http.StatusOK, exportedDeployment{
				Deployment: json.RawMessage(nullDeployment),
				Version:    emptyDeploymentVersion,
			})

			return
		}

		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, exportedDeployment{Deployment: cp.Deployment, Version: cp.SchemaVersion})
}

// exportStackVersion returns the checkpoint of one stack version, as
// `stack export --version N` reads it. It answers 404 when version N does
// not exist or has no checkpoint, and never falls back to an older one.
func (h *Handler) exportStackVersion(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	version, err := stack.ParseStackVersion(r.PathValue("version"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	cp, err := h.updates.ExportVersion(ctx, ref, version)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, exportedDeployment{Deployment: cp.Deployment, Version: cp.SchemaVersion})
}

// leaseExpiresAt returns the lease expiry of the update in Unix seconds
// while it runs, and 0 once it ended: a finished update holds no lease, so
// only the running update shows an expiry (ADR 28 decision 7). The token
// itself never leaves the start and renew answers.
func leaseExpiresAt(u stack.Update) int64 {
	if u.Status != stack.StatusRunning || u.Lease.ExpiresAt.IsZero() {
		return 0
	}

	return u.Lease.ExpiresAt.Unix()
}

// updateResult maps an update status to the history wire value. The wire
// type has no canceled value; a canceled update did not succeed, so it
// reports as failed.
func updateResult(status stack.UpdateStatus) string {
	switch status {
	case stack.StatusSucceeded:
		return "succeeded"
	case stack.StatusFailed, stack.StatusCanceled:
		return "failed"
	case stack.StatusRunning:
		return "in-progress"
	case stack.StatusNotStarted:
		return "not-started"
	}

	return "not-started"
}

// historyPage reads the pageSize and page query values. Absent values mean
// the domain default page.
func historyPage(r *http.Request) (stack.Page, error) {
	limit, err := queryNumber(r, "pageSize")
	if err != nil {
		return stack.Page{}, err
	}

	number, err := queryNumber(r, "page")
	if err != nil {
		return stack.Page{}, err
	}

	if limit == 0 || number == 0 {
		return stack.Page{Limit: limit, Offset: 0}, nil
	}

	return stack.Page{Limit: limit, Offset: (number - 1) * limit}, nil
}

// queryNumber parses one non-negative query value; absent means zero.
func queryNumber(r *http.Request, name string) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: query %q=%q", stack.ErrInvalidUpdate, name, raw)
	}

	return n, nil
}

// updateRef parses the stack reference in the path values.
func (h *Handler) updateRef(w http.ResponseWriter, r *http.Request) (stack.Ref, bool) {
	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		h.writeError(r.Context(), w, err)

		var zero stack.Ref

		return zero, false
	}

	return ref, true
}

// updateTarget parses the stack reference and update ID in the path values.
func (h *Handler) updateTarget(w http.ResponseWriter, r *http.Request) (stack.Ref, stack.UpdateID, bool) {
	ref, ok := h.updateRef(w, r)
	if !ok {
		var zero stack.Ref

		return zero, "", false
	}

	id, err := stack.ParseUpdateID(r.PathValue("update"))
	if err != nil {
		h.writeError(r.Context(), w, err)

		var zero stack.Ref

		return zero, "", false
	}

	return ref, id, true
}

// leaseToken reads the update lease token from the Authorization header. A
// missing or malformed token answers 401 without touching any store.
func (h *Handler) leaseToken(w http.ResponseWriter, r *http.Request) (token.Token, string, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), updateTokenScheme)
	if !ok || raw == "" {
		h.writeError(r.Context(), w, identity.ErrUnauthenticated)

		return token.Token{}, "", false
	}

	tok, err := token.Parse(raw)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return token.Token{}, "", false
	}

	return tok, raw, true
}

// readBody reads at most limit request bytes. An oversized body answers 413,
// an unreadable one 400.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(http.MaxBytesReader(w, r.Body, limit), limit+1))
	if err != nil {
		if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok && tooLarge != nil {
			h.writeTooLarge(w, r, limit)
		} else {
			h.writeInvalidBody(r.Context(), w)
		}

		return nil, false
	}

	if int64(len(body)) > limit {
		h.writeTooLarge(w, r, limit)

		return nil, false
	}

	return body, true
}

// writeTooLarge answers a request body over the route's size limit.
func (h *Handler) writeTooLarge(w http.ResponseWriter, r *http.Request, limit int64) {
	h.writeError(r.Context(), w, fmt.Errorf("%w: body over %d bytes", stack.ErrTooLarge, limit))
}

// readEvents reads an engine event batch, streaming gzip decompression into
// the decoder so only one copy is held.
func (h *Handler) readEvents(w http.ResponseWriter, r *http.Request) ([]stack.Event, bool) {
	releaseHold, ok := h.hold(w, r, maxEventBatchBytes)
	if !ok {
		return nil, false
	}

	defer releaseHold()

	var batch engineEventBatch
	if !h.decodeLargeBody(w, r, maxEventBatchBytes, &batch) {
		return nil, false
	}

	events := make([]stack.Event, 0, len(batch.Events))

	for _, raw := range batch.Events {
		event, err := parseEvent(raw)
		if err != nil {
			h.writeInvalidBody(r.Context(), w)

			return nil, false
		}

		events = append(events, event)
	}

	return events, true
}

// parseEvent reads one engine event's sequence and timestamp and keeps its
// JSON as the opaque payload.
func parseEvent(raw json.RawMessage) (stack.Event, error) {
	var header struct {
		Timestamp int64 `json:"timestamp"`
		Sequence  int   `json:"sequence"`
	}

	if err := json.Unmarshal(raw, &header); err != nil {
		return stack.Event{}, fmt.Errorf("%w: %w", errInvalidBody, err)
	}

	return stack.Event{
		Timestamp: time.Unix(header.Timestamp, 0).UTC(),
		Payload:   []byte(raw),
		Sequence:  int64(header.Sequence),
	}, nil
}

// writeInvalidBody answers a request body that is not valid JSON.
func (h *Handler) writeInvalidBody(ctx context.Context, w http.ResponseWriter) {
	h.writeJSON(ctx, w, http.StatusBadRequest, errorResponse{Code: http.StatusBadRequest, Message: invalidBodyMessage})
}
