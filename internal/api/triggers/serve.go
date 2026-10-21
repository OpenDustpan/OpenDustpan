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

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/cron"
	"opendustpan.org/opendustpan/pkg/graph"
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
	ID        string         `json:"id"`
	Namespace string         `json:"namespace"`
	Unit      unitResponse   `json:"unit"`
	Kind      trigger.Kind   `json:"kind"`
	Frequency *frequencyBody `json:"frequency,omitempty"`
	Source    *unitResponse  `json:"source,omitempty"`
	Enabled   bool           `json:"enabled"`
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
func (h *Handler) listTriggers(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	ctx := r.Context()

	list, err := h.manager.List(ctx, p, t.ns.Name, t.unit.UnitID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := listResponse{Triggers: make([]triggerResponse, 0, len(list))}
	for i := range list {
		out.Triggers = append(out.Triggers, h.toResponse(r, &list[i], t))
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// getTrigger answers GET .../triggers/{trigger_id} with the trigger.
func (h *Handler) getTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	ctx := r.Context()

	got, _, err := h.manager.Get(ctx, p, t.ns.Name, t.trigger.ID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, h.toResponse(r, &got, t))
}

// createTrigger answers POST .../units/{unit_id}/triggers with the trigger that
// the request saves. The caller becomes its last saver.
func (h *Handler) createTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
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

	w.Header().Set("Location", triggerLocation(t, got.ID))
	h.writeJSON(ctx, w, http.StatusCreated, h.toResponse(r, &got, t))
}

// replaceTrigger answers PUT .../triggers/{trigger_id} with the trigger that the
// request saves in place of the old one. The caller becomes its last saver.
func (h *Handler) replaceTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	ctx := r.Context()

	in, err := h.saveInput(w, r, t)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	got, err := h.manager.Replace(ctx, p, requestInfo(r), t.ns.Name, t.trigger.ID, in)
	if err != nil {
		h.manageError(r, p, err)
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, h.toResponse(r, &got, t))
}

// deleteTrigger answers DELETE .../triggers/{trigger_id} with 204.
func (h *Handler) deleteTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
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
// applies the gates, the approval rules, the freezes and the manual locks. A
// double click of one person within one minute gives the same job.
func (h *Handler) fireTrigger(w http.ResponseWriter, r *http.Request, p identity.Principal, t target) {
	ctx := r.Context()

	in, err := readFireInput(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

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

// manageError writes the audit entry of a refusal of an action that the manager
// checked and the wrapper did not: for example unit:read on the source unit of a
// follows trigger. The wrapper writes the entry of its own refusals.
func (h *Handler) manageError(r *http.Request, p identity.Principal, err error) {
	var denied *trigger.DeniedError
	if !errors.As(err, &denied) {
		return
	}

	h.guard.Record(r.Context(), r, p, refusal.Denial{
		Action: denied.Action, Object: denied.Object, Reason: refusal.ReasonPolicy, Window: 0,
	})
}

// requestInfo describes the request for the audit entry of a change.
func requestInfo(r *http.Request) trigger.RequestInfo {
	return trigger.RequestInfo{
		Channel:   sessionauth.AuditChannel(r, channelHeader),
		SourceIP:  clientip.String(r),
		UserAgent: truncate(r.UserAgent(), audit.MaxUserAgentLen),
		RequestID: requestid.FromContext(r.Context()),
	}
}

// triggerLocation returns the path of the trigger id on the unit of t.
func triggerLocation(t target, id trigger.ID) string {
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
	none := trigger.FireInput{Reason: "", Location: job.Location{
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

	in := trigger.FireInput{Reason: body.Reason, Location: none.Location}
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
func (h *Handler) saveInput(w http.ResponseWriter, r *http.Request, t target) (trigger.SaveInput, error) {
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

// toResponse maps a trigger to its wire shape. t is the target of the request:
// its unit is the unit of the trigger.
func (h *Handler) toResponse(r *http.Request, got *trigger.Trigger, t target) triggerResponse {
	out := triggerResponse{
		ID: string(got.ID), Namespace: got.Unit.Namespace.String(),
		Unit: unitResponse{ID: t.unit.UnitID, Tool: toolLabel(t.unit.Tool), Name: t.unit.Name},
		Kind: got.Kind, Frequency: nil, Source: nil, Enabled: got.Enabled, LastSaver: got.LastSaver,
		NeedsNewSave: !h.manager.SaverOK(r.Context(), got),
	}

	if got.Kind == trigger.KindSchedule {
		out.Frequency = &frequencyBody{Preset: got.Frequency.Preset, Cron: got.Frequency.Cron}
	}

	if got.Kind == trigger.KindFollows {
		out.Source = h.sourceResponse(r, got.Source)
	}

	return out
}

// sourceResponse describes the source unit of a follows trigger. A source that
// the inventory no longer knows has its ID only.
func (h *Handler) sourceResponse(r *http.Request, source graph.Unit) *unitResponse {
	out := &unitResponse{ID: source.ID, Tool: toolLabel(source.Tool), Name: ""}

	if e, err := h.units.GetUnit(r.Context(), source.ID); err == nil && e.Namespace == source.Namespace {
		out.Tool, out.Name = toolLabel(e.Tool), e.Name
	}

	return out
}

// cycleUnits describes the units of a loop in order, for the answer of a
// refused save.
func cycleUnits(cycle *trigger.CycleError) []unitResponse {
	out := make([]unitResponse, 0, len(cycle.Path))

	for _, u := range cycle.Path {
		out = append(out, unitResponse{ID: u.ID, Tool: toolLabel(u.Tool), Name: cycle.Names[u]})
	}

	return out
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
