package notifications

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// maxBodyBytes bounds a request body: a URL of at most 2 KiB with the other
// fields, escaped.
const maxBodyBytes = 64 << 10

// sinkDocument is one sink as the API answers it. It never holds a secret:
// URLSet says only that a chat sink has a webhook URL. Target is the address
// of an email sink, or the host of a webhook or of a chat webhook. The URL of
// a webhook is a secret: it goes in on create and never comes out.
// HostNeedsUpdate is true for a stored sink whose host fails the allowlist of
// a new write. The sink still sends. A webhook sink of this kind shows an
// empty target. A replace of it needs a host that passes the allowlist.
//
// Contract: never remove or rename these fields.
type sinkDocument struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Platform  string `json:"platform,omitempty"`
	Name      string `json:"name"`
	Target    string `json:"target"`
	URLSet    bool   `json:"urlSet"`
	CreatedBy string `json:"createdBy"`
	Version   int64  `json:"version"`

	HostNeedsUpdate bool `json:"hostNeedsUpdate,omitempty"`
}

// createdDocument is the answer of a create. SigningKey is set for a webhook
// sink, and no other answer holds it.
type createdDocument struct {
	sinkDocument

	SigningKey string `json:"signingKey,omitempty"`
}

type listResponse struct {
	Sinks []sinkDocument `json:"sinks"`
}

// testDocument is the answer of a test. Its text is fixed.
type testDocument struct {
	Delivered bool   `json:"delivered"`
	Class     string `json:"class,omitempty"`
	Message   string `json:"message"`
}

func toDocument(s *notify.Sink) sinkDocument {
	target := s.Target
	if s.Kind == notify.SinkWebhook {
		target = notify.WebhookHost(s.Target)
	}

	return sinkDocument{
		ID: s.ID, Namespace: s.Scope.String(), Kind: string(s.Kind), Platform: s.Platform, Name: s.Name,
		Target: target, URLSet: s.Kind == notify.SinkChat && len(s.SecretRefs) == 1, CreatedBy: s.CreatedBy,
		Version: s.Version, HostNeedsUpdate: s.HostNeedsUpdate(),
	}
}

// readBody decodes one JSON object into v. It refuses an unknown field, a
// second value and a body over the limit. The error text is fixed: it never
// holds a part of the body.
func readBody(w http.ResponseWriter, r *http.Request, v any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return badRequest("request.body-invalid", "the request body is not readable or is over 64 KiB")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err = dec.Decode(v); err != nil || dec.More() {
		return badRequest("request.body-invalid", "the request body must be one JSON object with known fields")
	}

	return nil
}

// caller makes the caller of the sink service from the request.
func caller(r *http.Request, p identity.Principal) *sinks.Caller {
	by := audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: truncateUserAgent(r.UserAgent()), Channel: sessionauth.AuditChannel(r, channelHeader),
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: requestid.FromContext(r.Context()),
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	return &sinks.Caller{Principal: p, By: by}
}

func truncateUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	cut := audit.MaxUserAgentLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

func location(ns namespace.Name, id string) string {
	return v1Prefix + "/namespaces/" + url.PathEscape(ns.String()) + "/sinks/" + url.PathEscape(id)
}

// jsonAnswer makes an answer with a JSON body. The answer is never cached: it
// can hold a signing key.
func jsonAnswer(status int, body any, headers map[string]string) (idempotency.Answer, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode sink answer: %w", err)
	}

	h := map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}
	maps.Copy(h, headers)

	return idempotency.Answer{Headers: h, Body: buf.Bytes(), Status: status}, nil
}

var errSinkResult = errors.New("the store passed a result that is not a sink")

func sinkOf(result any) (notify.Sink, error) {
	s, ok := result.(notify.Sink)
	if !ok {
		return notify.Sink{}, errSinkResult
	}

	return s, nil
}

// sinkID returns the ID of the sink that a create made. A replay of a create
// names it, because the first answer held a signing key.
func sinkID(result any) string {
	s, err := sinkOf(result)
	if err != nil {
		return ""
	}

	return s.ID
}

// createdAnswer answers a create: 201 with the sink, its Location and, for a
// webhook, the signing key.
func createdAnswer(st *requestState) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		s, err := sinkOf(result)
		if err != nil {
			return idempotency.Answer{}, err
		}

		return jsonAnswer(
			http.StatusCreated,
			createdDocument{sinkDocument: toDocument(&s), SigningKey: st.signingKey},
			map[string]string{"Location": location(s.Scope, s.ID)},
		)
	}
}

// replacedAnswer answers a replace: 200 with the sink.
func replacedAnswer(*requestState) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		s, err := sinkOf(result)
		if err != nil {
			return idempotency.Answer{}, err
		}

		return jsonAnswer(http.StatusOK, toDocument(&s), nil)
	}
}

// deletedAnswer answers a delete: 204 with no body.
func deletedAnswer(*requestState) answerFunc {
	return func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Headers: map[string]string{}, Body: nil, Status: http.StatusNoContent}, nil
	}
}

// testedAnswer answers a test: 200 with the class of the result and a fixed
// text. It never holds the URL or the answer of the receiver.
func testedAnswer(st *requestState) answerFunc {
	return func(any) (idempotency.Answer, error) {
		return jsonAnswer(http.StatusOK, testDocument{
			Delivered: st.test.Delivered, Class: st.test.Class, Message: st.test.Message,
		}, nil)
	}
}

// send writes the answer that build makes for the result of a write.
func (h *Handler) send(w http.ResponseWriter, r *http.Request, build answerFunc, result any) {
	a, err := build(result)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	idempotent.Write(w, a)
}

// list serves GET /sinks.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	all, err := h.sinks.List(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	resp := listResponse{Sinks: make([]sinkDocument, 0, len(all))}
	for i := range all {
		resp.Sinks = append(resp.Sinks, toDocument(&all[i]))
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, resp)
}

// get serves GET /sinks/{sink_id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	s, err := h.sinks.Get(ctx, obj.Namespace(), r.PathValue("sink_id"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, toDocument(&s))
}

// createBody is the body of a create. The fields are pointers so that a
// missing field differs from an empty one.
type createBody struct {
	Kind     *string `dustpan:"required" json:"kind"`
	Platform *string `dustpan:"optional" json:"platform"`
	Name     *string `dustpan:"required" json:"name"`
	Target   *string `dustpan:"optional" json:"target"`
	URL      *string `dustpan:"optional" json:"url"`
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// create serves POST /sinks.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var body createBody
	if err := readBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if body.Kind == nil || body.Name == nil {
		h.writeError(ctx, w, badRequest("sink.invalid", "send the fields kind and name"))

		return
	}

	st := stateOf(ctx)

	made, err := h.sinks.Create(ctx, caller(r, p), obj.Namespace(), sinks.New{
		Kind: notify.SinkKind(*body.Kind), Platform: orEmpty(body.Platform), Name: *body.Name,
		Target: orEmpty(body.Target), URL: orEmpty(body.URL),
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	st.signingKey = made.SigningKey

	h.send(w, r, createdAnswer(st), made.Sink)
}

// replaceBody is the body of a replace.
type replaceBody struct {
	Version *int64  `dustpan:"required" json:"version"`
	Name    *string `dustpan:"required" json:"name"`
	Target  *string `dustpan:"optional" json:"target"`
	URL     *string `dustpan:"optional" json:"url"`
}

// replace serves PUT /sinks/{sink_id}.
func (h *Handler) replace(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var body replaceBody
	if err := readBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if body.Version == nil || body.Name == nil {
		h.writeError(ctx, w, badRequest("sink.invalid", "send the fields version and name"))

		return
	}

	next, err := h.sinks.Replace(ctx, caller(r, p), obj.Namespace(), r.PathValue("sink_id"), sinks.Change{
		Version: *body.Version, Name: *body.Name, Target: body.Target, URL: body.URL,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(w, r, replacedAnswer(stateOf(ctx)), next)
}

// delete serves DELETE /sinks/{sink_id}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	id := r.PathValue("sink_id")

	if err := h.sinks.Delete(ctx, caller(r, p), obj.Namespace(), id); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(w, r, deletedAnswer(stateOf(ctx)), id)
}

// test serves POST /sinks/{sink_id}/test. The answer holds the class of the
// result and a fixed text, never the URL or the answer of the receiver.
func (h *Handler) test(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	id := r.PathValue("sink_id")

	result, err := h.sinks.SendTest(ctx, caller(r, p), obj.Namespace(), id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	st := stateOf(ctx)
	st.test = result

	if err = h.sinks.RecordTest(ctx, caller(r, p), obj.Namespace(), id, result); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(w, r, testedAnswer(st), id)
}
