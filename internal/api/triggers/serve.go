package triggers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/internal/driftsvc"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// maxBody bounds the body of a request. A trigger has a kind, a frequency and
// a source: far less.
const maxBody = 16 << 10

type unitResponse struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// frequencyBody is the frequency of a schedule trigger, in a request and in an
// answer. Exactly one of the two fields is set.
type frequencyBody struct {
	Preset cron.Preset `json:"preset,omitempty"`
	Cron   string      `json:"cron,omitempty"`
}

// frequencyAnswer is the frequency of a schedule trigger in an answer. The
// request has no expression: a client cannot set the minute and the hour of a
// preset. Expression is the five-field cron expression, in UTC, that the
// frequency means for the unit of the trigger. For a preset, it holds the
// offset that the unit ID sets.
type frequencyAnswer struct {
	Preset     cron.Preset `json:"preset,omitempty"`
	Cron       string      `json:"cron,omitempty"`
	Expression string      `json:"expression"`
}

// sourceBody names the source unit of a follows trigger in a request, by its
// unit ID.
type sourceBody struct {
	ID string `json:"id"`
}

// saveBody is the body of the requests that create and replace a trigger. The
// server fixes the unit, from the path, and the last saver, from the caller. A
// client never sends them, and a field that is not in this type fails the
// request.
type saveBody struct {
	Kind      trigger.Kind   `json:"kind"`
	Frequency *frequencyBody `json:"frequency"`
	Source    *sourceBody    `json:"source"`
	Enabled   *bool          `json:"enabled"`
}

// triggerResponse is the answered document of a trigger.
type triggerResponse struct {
	ID        string           `json:"id"`
	Namespace string           `json:"namespace"`
	Unit      unitResponse     `json:"unit"`
	Kind      trigger.Kind     `json:"kind"`
	Frequency *frequencyAnswer `json:"frequency,omitempty"`
	Source    *unitResponse    `json:"source,omitempty"`
	Enabled   bool             `json:"enabled"`
	// LastSaver is the subject of the person who saved the trigger last.
	LastSaver string `json:"lastSaver"`
	// NeedsNewSave is true when the last saver no longer holds the actions
	// that a start needs. The trigger then starts nothing until a person with
	// the actions saves it again.
	NeedsNewSave bool `json:"needsNewSave"`
}

type listResponse struct {
	Triggers []triggerResponse `json:"triggers"`
}

// fireBody is the optional body of the request that fires a trigger. The
// location is where the program and the config are in git. Until the settings
// of a unit hold a code location, a fire names one.
type fireBody struct {
	Reason   string           `json:"reason"`
	Location *locationRequest `json:"location"`
}

type locationRequest struct {
	Connection string `json:"connection"`
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Directory  string `json:"directory"`
	Revision   string `json:"revision"`
}

type jobRef struct {
	ID string `json:"id"`
}

type fireResponse struct {
	Job jobRef `json:"job"`
}

// listTriggers answers GET .../units/{unit_id}/triggers with the triggers of the
// unit. The route wrapper allowed namespace:read and unit:read.
func (h *Handler) listTriggers(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	list, err := h.manager.List(ctx, p, t.ns.Name, t.unit.UnitID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := listResponse{Triggers: make([]triggerResponse, 0, len(list))}
	for i := range list {
		one, err := h.toResponse(r, p, &list[i], t)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		out.Triggers = append(out.Triggers, one)
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// getTrigger answers GET .../triggers/{trigger_id} with the trigger.
func (h *Handler) getTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	got, _, err := h.manager.Get(ctx, p, t.ns.Name, t.trigger.ID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out, err := h.toResponse(r, p, &got, t)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// createTrigger answers POST .../units/{unit_id}/triggers with the trigger that
// the request saves. The caller becomes its last saver.
func (h *Handler) createTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	in, err := h.saveInput(w, r, t)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	got, err := h.manager.Create(ctx, p, requestInfo(r), in)
	if err != nil {
		h.manageError(r, p, err)
		h.writeError(ctx, w, err)

		return
	}

	out, err := h.toResponse(r, p, &got, t)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Location", triggerLocation(t, got.ID))
	h.writeJSON(ctx, w, http.StatusCreated, out)
}

// replaceTrigger answers PUT .../triggers/{trigger_id} with the trigger that the
// request saves in place of the old one. The caller becomes its last saver.
func (h *Handler) replaceTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	in, err := h.saveInput(w, r, t)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	got, err := h.manager.Replace(ctx, p, requestInfo(r), t.trigger.ID, in)
	if err != nil {
		h.manageError(r, p, err)
		h.writeError(ctx, w, err)

		return
	}

	out, err := h.toResponse(r, p, &got, t)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// deleteTrigger answers DELETE .../triggers/{trigger_id} with 204.
func (h *Handler) deleteTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	if err := h.manager.Delete(ctx, p, requestInfo(r), t.ns.Name, t.trigger.ID); err != nil {
		h.manageError(r, p, err)
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// fireTrigger answers POST .../triggers/{trigger_id}/fire with the job that
// the fire starts. The job service checks the start action of the caller and
// applies the gates, the approval rules, the freezes and the manual locks. With
// an Idempotency-Key header, two fires of one caller with one key give the same
// job. Without one, a double click of one person within one minute gives the
// same job, and a double click across a minute boundary can give two.
func (h *Handler) fireTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	in, err := readFireInput(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// A fire starts a job, which inserts a durable operation that only a
	// worker runs.
	if err = admit.Start(ctx, h.workers); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	in.IdempotencyKey = key
	in.Request = requestInfo(r)

	id, err := h.manager.Fire(ctx, p, t.ns.Name, t.trigger.ID, in)
	if err != nil {
		h.manageError(r, p, err)
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Location", "/api/dustpan/v1/namespaces/"+url.PathEscape(t.ns.Name.String())+"/jobs/"+
		url.PathEscape(string(id)))
	h.writeJSON(ctx, w, http.StatusCreated, fireResponse{Job: jobRef{ID: string(id)}})
}

// idempotencyKey returns the key of the request, or "" for a request with no
// key. A key that is not valid, or a second key, is an error.
func idempotencyKey(r *http.Request) (string, error) {
	values := apidesc.HeaderValues(r, idempotencyHeader)

	switch len(values) {
	case 0:
		return "", nil
	case 1:
		k, err := idempotency.ParseKey(values[0])
		if err != nil {
			return "", fmt.Errorf("%w: send 1 to %d printable ASCII characters", err, idempotency.MaxKeyLength)
		}

		// Defense in depth: the job service refuses the prefix too. A key
		// with the prefix of the drift checks of the server could name the
		// job of a drift check of the same caller, so no fire sends one.
		if driftsvc.ReservedKey(string(k)) {
			return "", fmt.Errorf(
				"%w: a key does not start with %q",
				idempotency.ErrInvalidKey,
				driftsvc.CheckKeyPrefix,
			)
		}

		return string(k), nil
	default:
		return "", fmt.Errorf("%w: send one Idempotency-Key header", idempotency.ErrInvalidKey)
	}
}

// manageError writes the audit entry of a refusal of an action that the manager
// checked and the wrapper did not: for example unit:read on the source unit of a
// follows trigger. The wrapper writes the entry of its own refusals.
func (h *Handler) manageError(r *http.Request, p identity.Principal, err error) {
	var denied *trigger.DeniedError
	if !errors.As(err, &denied) {
		return
	}

	h.guard.Record(r.Context(), r, p, refusal.Denial{
		Action: denied.Action, Object: denied.Object, Reason: refusal.ReasonPolicy, ReasonWithin: nil, Window: 0,
	})
}

// requestInfo describes the request for the audit entry of a change.
func requestInfo(r *http.Request) trigger.RequestInfo {
	return trigger.RequestInfo{
		Channel:   sessionauth.AuditChannel(r, channelHeader),
		SourceIP:  clientip.String(r),
		UserAgent: truncate(apidesc.Header(r, "User-Agent"), audit.MaxUserAgentLen),
		RequestID: requestid.FromContext(r.Context()),
	}
}

// triggerLocation returns the path of the trigger id on the unit of t.
func triggerLocation(t *target, id trigger.ID) string {
	return "/api/dustpan/v1/namespaces/" + url.PathEscape(t.ns.Name.String()) + "/units/" +
		url.PathEscape(t.unit.UnitID) + "/triggers/" + url.PathEscape(string(id))
}

// readRaw reads the body of the request. It refuses a body above maxBody bytes.
func readRaw(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%w: send at most %d bytes of JSON", errBody, maxBody)
	}

	return raw, nil
}

// decodeBody decodes one JSON object into v. It refuses an unknown field and
// data after the document.
func decodeBody(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: send one JSON object with a known field set", errBody)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: send one JSON object only", errBody)
	}

	return nil
}

// readFireInput reads the optional body of a fire. An empty body is no reason
// and no location.
func readFireInput(w http.ResponseWriter, r *http.Request) (trigger.FireInput, error) {
	none := trigger.FireInput{Reason: "", IdempotencyKey: "", Request: requestInfo(r), Location: job.Location{
		Connection: "", Repository: "", Ref: "", Directory: "", Revision: "",
	}}

	raw, err := readRaw(w, r)
	if err != nil {
		return none, err
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return none, nil
	}

	var body fireBody
	if err := decodeBody(raw, &body); err != nil {
		return none, err
	}

	in := trigger.FireInput{Reason: body.Reason, Location: none.Location, IdempotencyKey: "", Request: none.Request}
	if l := body.Location; l != nil {
		in.Location = job.Location{
			Connection: l.Connection, Repository: l.Repository, Ref: l.Ref, Directory: l.Directory,
			Revision: l.Revision,
		}
	}

	return in, nil
}

// saveInput builds the input of the manager from the body. It checks each field
// against an allowlist and refuses a value without changing it.
func (h *Handler) saveInput(w http.ResponseWriter, r *http.Request, t *target) (trigger.SaveInput, error) {
	raw, err := readRaw(w, r)
	if err != nil {
		return trigger.SaveInput{}, err
	}

	var body saveBody
	if err := decodeBody(raw, &body); err != nil {
		return trigger.SaveInput{}, err
	}

	kind := body.Kind
	if !kind.Valid() {
		return trigger.SaveInput{}, fmt.Errorf("%w: the kind is one of review-request-push, schedule and follows",
			trigger.ErrInvalid)
	}

	in := trigger.SaveInput{
		Namespace: t.ns.Name, UnitID: t.unit.UnitID, Kind: kind, Frequency: trigger.Frequency{Preset: "", Cron: ""},
		SourceID: "", Enabled: true,
	}

	if body.Enabled != nil {
		in.Enabled = *body.Enabled
	}

	if f := body.Frequency; f != nil {
		in.Frequency = trigger.Frequency{Preset: f.Preset, Cron: f.Cron}
	}

	if s := body.Source; s != nil {
		if s.ID == "" {
			return trigger.SaveInput{}, fmt.Errorf("%w: name the source unit by its unit ID", trigger.ErrInvalid)
		}

		in.SourceID = s.ID
	}

	return in, nil
}

// toResponse maps a trigger to its wire shape for the caller p. t is the target
// of the request: its unit is the unit of the trigger. It returns
// errBrokenFrequency when the stored frequency of a schedule trigger has no
// expression. The caller answers it as a failure of the server.
func (h *Handler) toResponse(
	r *http.Request, p identity.Principal, got *trigger.Trigger, t *target,
) (triggerResponse, error) {
	out := triggerResponse{
		ID: string(got.ID), Namespace: got.Unit.Namespace.String(),
		Unit: unitResponse{ID: t.unit.UnitID, Tool: toolLabel(t.unit.Tool), Name: t.unit.Name},
		Kind: got.Kind, Frequency: nil, Source: nil, Enabled: got.Enabled, LastSaver: got.LastSaver,
		NeedsNewSave: !h.manager.SaverOK(r.Context(), got),
	}

	if got.Kind == trigger.KindSchedule {
		expr, err := got.Frequency.Expression(got.Unit)
		if err != nil {
			faultlog.LogFault(r.Context(), h.logger, "stored frequency has no expression",
				faultlog.Cause(err), "trigger.frequency")

			return triggerResponse{}, errBrokenFrequency
		}

		out.Frequency = &frequencyAnswer{Preset: got.Frequency.Preset, Cron: got.Frequency.Cron, Expression: expr}
	}

	if got.Kind == trigger.KindFollows {
		out.Source = h.sourceResponse(r, p, got.Source)
	}

	return out, nil
}

// sourceResponse describes the source unit of a follows trigger. A source that
// the inventory no longer knows, and a source that p cannot read, have their ID
// and tool only.
func (h *Handler) sourceResponse(r *http.Request, p identity.Principal, source graph.Unit) *unitResponse {
	out := &unitResponse{ID: source.ID, Tool: toolLabel(source.Tool), Name: ""}

	if !h.manager.CanRead(r.Context(), p, source) {
		return out
	}

	if e, err := h.units.GetUnit(r.Context(), source.ID); err == nil && e.Namespace == source.Namespace {
		out.Tool, out.Name = toolLabel(e.Tool), e.Name
	}

	return out
}

// cycleUnits describes the units of a loop in order, for the answer of a
// refused save. It answers only the units that the manager marked readable for
// the caller.
//
// Defense in depth: the manager gives no name to a unit that the caller cannot
// read. This check stays so that a cycle error that did not pass the manager
// names no unit, and it does not rely on the manager.
func cycleUnits(cycle *trigger.CycleError) []problem.Unit {
	out := make([]problem.Unit, 0, len(cycle.Path))

	for _, u := range cycle.Path {
		if !cycle.Readable[u] {
			continue
		}

		out = append(out, problem.Unit{ID: u.ID, Tool: toolLabel(u.Tool), Name: cycle.Names[u]})
	}

	return out
}

// cycleMessage is the message of the answer of a refused save that closes a
// loop. A cycle error with no readable marks names no unit.
//
// Defense in depth: the manager marks the units that the caller may read. This
// check stays so that a cycle error that did not pass the manager names no
// unit, and it does not rely on the manager.
func cycleMessage(cycle *trigger.CycleError) string {
	if cycle.Readable == nil {
		return trigger.ErrCycle.Error()
	}

	return refusal.Clean(cycle.Error(), problem.MaxMessageBytes)
}

// toolLabel maps a stored lowercase tool code to the tool label of the
// glossary ("Pulumi" or "OpenTofu"). A code without a label answers unchanged,
// so that a new tool still reads.
func toolLabel(code string) string {
	switch code {
	case "pulumi":
		return "Pulumi"
	case "opentofu":
		return "OpenTofu"
	default:
		return code
	}
}

// truncate cuts s to at most n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return strings.ToValidUTF8(s[:n], "")
}
