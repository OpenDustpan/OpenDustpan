package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// errWritten reports that the store passed a result that the route does not
// know to the Completion of a request.
var errWritten = errors.New("the store passed a result that the route did not expect")

// endTimeout bounds the call that frees a key after a request that failed.
const endTimeout = 5 * time.Second

// settingsAnswer is the answer of a write of the settings of a unit: the record
// with its effective values. The route sends it and the store keeps it, so a
// replay and the first answer are the same bytes.
func settingsAnswer(view *unitsettingssvc.View) (idempotency.Answer, error) {
	return jsonAnswer(settingsOf(view), view.Settings.Version)
}

// defaultsAnswer is the answer of a write of the unit defaults of a namespace.
func defaultsAnswer(d *unitsettings.NamespaceDefaults) (idempotency.Answer, error) {
	return jsonAnswer(defaultsOf(d), d.Version)
}

// deletedAnswer is the answer of a reset: 204 with no body.
func deletedAnswer() idempotency.Answer {
	return idempotency.Answer{Status: http.StatusNoContent, Headers: nil, Body: nil}
}

// jsonAnswer makes the 200 answer with the JSON of body and the ETag of
// version.
func jsonAnswer(body any, version int64) (idempotency.Answer, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode answer: %w", err)
	}

	return idempotency.Answer{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json", "ETag": etag(version)},
		Body:    buf.Bytes(),
	}, nil
}

// writeAnswer sends a, or answers the error of the step that made it.
func (h *Handler) writeAnswer(ctx context.Context, w http.ResponseWriter, a *idempotency.Answer, err error) {
	if err != nil || a == nil {
		if err == nil {
			err = errNoAnswer
		}

		h.writeError(ctx, w, err)

		return
	}

	idempotent.Write(w, *a)
}

// readBody reads the body of r, decodes it into v and puts the bytes back in
// the request. It returns the bytes. The errors are those of decodeBody.
func readBody(w http.ResponseWriter, r *http.Request, v any) ([]byte, error) {
	raw, err := textbody.Read(w, r, unitsettings.MaxBodyBytes)
	if err != nil {
		if errors.Is(err, textbody.ErrTooLarge) {
			return nil, err
		}

		return nil, fmt.Errorf("%w: %w", errBody, err)
	}

	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err := textbody.Unmarshal(raw, v); err != nil {
		return nil, fmt.Errorf("%w: %w", errBody, err)
	}

	return raw, nil
}

// keyedPreflight runs the checks of the body of a single write that has the
// Idempotency-Key header, before the replay. It leaves the body in the request.
func (h *Handler) keyedPreflight(
	w http.ResponseWriter, r *http.Request, p identity.Principal, t *target,
) (unitsettingssvc.View, error) {
	var body settingsBody
	if _, err := readBody(w, r, &body); err != nil {
		return unitsettingssvc.View{}, err
	}

	return h.preflight(r, p, t, &body)
}

// preflight checks the body of a write that has the key: the body is valid and
// the caller holds the actions that the body decides, as unitsettingssvc.
// NeedsKeyed says. A replay answers from the stored answer, so these checks run
// before it, and a caller who lost an action never gets the stored answer. It
// returns the view of the unit before the write.
//
// Defense in depth: the wrapper checked the actions on the unit, and the first
// request checks the actions of the body again against the record. This check
// stays so that a replay never relies on the first request.
func (h *Handler) preflight(
	r *http.Request, p identity.Principal, t *target, body *settingsBody,
) (unitsettingssvc.View, error) {
	in, err := body.toSettings(t.obj)
	if err != nil {
		return unitsettingssvc.View{}, err
	}

	if err = in.Validate(); err != nil {
		return unitsettingssvc.View{}, fmt.Errorf("unit settings: %w", err)
	}

	cur, err := h.service.Read(r.Context(), t.obj)
	if err != nil {
		return unitsettingssvc.View{}, fmt.Errorf("read unit settings: %w", err)
	}

	if authErr := h.authorizeExtra(r, p, t, unitsettingssvc.NeedsKeyed(&in)); authErr != nil {
		return unitsettingssvc.View{}, authErr
	}

	return cur, nil
}

// bulkRun is one item of a bulk call with the body that all items share and the
// record of the call. call is nil for a call with no key.
type bulkRun struct {
	item bulkUnit
	body *settingsBody
	call *bulkCall
}

// bulkCall is the replay record of a whole bulk call. It holds the fingerprint
// of the body, so the same key with another body is refused. The results of the
// items are in the records of the items.
type bulkCall struct {
	ref idempotency.Ref
	// key is the Idempotency-Key of the request.
	key idempotency.Key
	// token names the pending record, when this request started it.
	token string
	// started is true for the first request with the key. A retry finds the
	// record done.
	started bool
	done    bool
}

// keyRefusal returns the refusal of the Idempotency-Key with the code of the
// catalog.
func keyRefusal(status int, code, message string) *idempotent.Error {
	answer, err := errcode.New(code)
	if err != nil {
		answer = errcode.Internal()
		status = http.StatusInternalServerError
	}

	return &idempotent.Error{Answer: answer, Message: message, Status: status}
}

// digestKey returns a key for a record that derives from key and the parts. The
// digest keeps the key inside the length limit, whatever the parts hold.
func digestKey(prefix string, key idempotency.Key, parts ...string) (idempotency.Key, error) {
	sum := sha256.New()
	sum.Write([]byte(key))

	for _, part := range parts {
		sum.Write([]byte{0})
		sum.Write([]byte(part))
	}

	derived, err := idempotency.ParseKey(prefix + hex.EncodeToString(sum.Sum(nil)))
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}

	return derived, nil
}

// beginBulk finds or starts the record of a bulk call with the key of r. raw is
// the body of the call. It returns an *idempotent.Error for a refusal.
func (h *Handler) beginBulk(r *http.Request, p identity.Principal, raw []byte) (*bulkCall, error) {
	if h.records == nil {
		return nil, errKeyUnsupported
	}

	values := apidesc.HeaderValues(r, idempotencyHeader)
	if len(values) != 1 {
		return nil, keyRefusal(http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid,
			"send the Idempotency-Key header one time")
	}

	key, err := idempotency.ParseKey(values[0])
	if err != nil {
		return nil, keyRefusal(http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid,
			"the Idempotency-Key must have 1 to "+strconv.Itoa(idempotency.MaxKeyLength)+" printable ASCII characters")
	}

	callKey, err := digestKey("call-", key)
	if err != nil {
		return nil, err
	}

	ref := idempotency.Ref{Principal: p.Subject, Method: r.Method, Path: r.URL.EscapedPath(), Key: callKey}

	res, err := h.records.Begin(r.Context(), ref, raw, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("begin idempotent request: %w", err)
	}

	call := &bulkCall{ref: ref, key: key, token: res.Token, started: res.Outcome == idempotency.Started, done: false}

	switch res.Outcome {
	case idempotency.Started, idempotency.Replayed:
		return call, nil
	case idempotency.Reused:
		return nil, keyRefusal(http.StatusUnprocessableEntity, errcode.RequestIdempotencyKeyReused,
			"this Idempotency-Key was used with another request: send a new key for a new request")
	case idempotency.InProgress:
		return nil, keyRefusal(http.StatusConflict, errcode.RequestIdempotencyKeyInProgress,
			"the first request with this Idempotency-Key has not ended: retry later")
	default:
		return nil, fmt.Errorf("%w: unknown outcome %d", idempotency.ErrInvalid, res.Outcome)
	}
}

// finish marks the call as ended with an answer. A nil call is a call with no
// key.
func (c *bulkCall) finish() {
	if c != nil {
		c.done = true
	}
}

// end stores the record of the call when the answer was sent, and frees the key
// otherwise, so a retry does the call again.
func (c *bulkCall) end(ctx context.Context, h *Handler) {
	if !c.started {
		return
	}

	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), endTimeout)
	defer cancel()

	if !c.done {
		h.abandon(endCtx, c.ref, c.token)

		return
	}

	done := idempotency.Answer{Status: http.StatusOK, Headers: nil, Body: nil}
	if err := h.records.Complete(endCtx, c.ref, c.token, done); err != nil && !errors.Is(err, idempotency.ErrLost) {
		faultlog.LogFault(ctx, h.logger, "store the record of a bulk call failed", faultlog.Cause(err), "bulk.complete")
	}
}

// abandon frees the key of a record that ended with no stored answer.
func (h *Handler) abandon(ctx context.Context, ref idempotency.Ref, token string) {
	if err := h.records.Abandon(ctx, ref, token); err != nil {
		faultlog.LogFault(ctx, h.logger, "free an Idempotency-Key failed", faultlog.Cause(err), "bulk.abandon")
	}
}

// itemAnswer is the answer that the record of one item keeps: the result of the
// item with the version that the write made.
func itemAnswer(id string, version int64) (idempotency.Answer, error) {
	var buf bytes.Buffer

	result := bulkResult{ID: id, Status: statusOK, Version: version, Error: nil}
	if err := json.NewEncoder(&buf).Encode(result); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode item answer: %w", err)
	}

	return idempotency.Answer{Status: http.StatusOK, Headers: nil, Body: buf.Bytes()}, nil
}

// bulkItemKeyed runs one item of a call with a key. The checks of the item run
// before its record, so a replay re-checks the actions of the caller on the
// unit. An item that was written answers its stored result on a retry. An item
// that failed leaves no record, so a retry does it again.
func (h *Handler) bulkItemKeyed(r *http.Request, p identity.Principal, ns *target, run *bulkRun) bulkResult {
	ctx := r.Context()
	item := run.item

	t, err := h.bulkTarget(r, p, ns, item)
	if err != nil {
		return h.bulkFailure(r, item.ID, err)
	}

	if _, err = h.preflight(r, p, &t, run.body); err != nil {
		return h.bulkFailure(r, item.ID, err)
	}

	itemKey, err := digestKey("item-", run.call.key, item.ID)
	if err != nil {
		return h.bulkFailure(r, item.ID, err)
	}

	material, err := json.Marshal(itemMaterial{Settings: run.body, ID: item.ID, Version: item.Version})
	if err != nil {
		return h.bulkFailure(r, item.ID, fmt.Errorf("fingerprint item: %w", err))
	}

	ref := idempotency.Ref{Principal: p.Subject, Method: r.Method, Path: r.URL.EscapedPath(), Key: itemKey}

	res, err := h.records.Begin(ctx, ref, material, time.Time{})
	if err != nil {
		return h.bulkFailure(r, item.ID, fmt.Errorf("begin idempotent request: %w", err))
	}

	switch res.Outcome {
	case idempotency.Replayed:
		return h.replayedItem(r, item.ID, &res.Answer)
	case idempotency.Reused:
		// Defense in depth: the record of the whole call refuses a changed
		// body first, so a changed item does not reach this case today. It
		// stays so that an item never answers the stored result of another
		// request, and it relies on neither the record of the call nor the
		// fingerprint of the call body.
		return h.bulkFailure(r, item.ID, keyRefusal(http.StatusUnprocessableEntity,
			errcode.RequestIdempotencyKeyReused, "this Idempotency-Key was used with another request"))
	case idempotency.InProgress:
		return h.bulkFailure(r, item.ID, keyRefusal(http.StatusConflict,
			errcode.RequestIdempotencyKeyInProgress, "the first request for this unit has not ended: retry later"))
	case idempotency.Started:
		return h.writeItem(r, p, &t, run, &pendingItem{ref: ref, token: res.Token})
	default:
		return h.bulkFailure(r, item.ID, fmt.Errorf("%w: unknown outcome %d", idempotency.ErrInvalid, res.Outcome))
	}
}

// itemMaterial is what the record of an item is compared by: the unit, the
// version that the caller read and the settings.
type itemMaterial struct {
	Settings *settingsBody `json:"settings"`
	ID       string        `json:"id"`
	Version  int64         `json:"version"`
}

// pendingItem names the pending record that an item started.
type pendingItem struct {
	ref   idempotency.Ref
	token string
}

// replayedItem answers the stored result of an item.
func (h *Handler) replayedItem(r *http.Request, id string, a *idempotency.Answer) bulkResult {
	var out bulkResult

	if err := json.Unmarshal(a.Body, &out); err != nil || out.ID != id {
		return h.bulkFailure(r, id, errWritten)
	}

	return out
}

// writeItem writes one item with a Completion, so the store keeps the result of
// the item in the transaction of the write. A write that fails frees the key.
func (h *Handler) writeItem(
	r *http.Request, p identity.Principal, t *target, run *bulkRun, pending *pendingItem,
) bulkResult {
	completion := idempotency.NewCompletion(pending.ref, pending.token,
		func(result any) (idempotency.Answer, error) {
			out, ok := result.(unitsettings.PutResult)
			if !ok {
				return idempotency.Answer{}, errWritten
			}

			return itemAnswer(run.item.ID, out.Settings.Version)
		})

	keyed := r.WithContext(idempotency.WithCompletion(r.Context(), completion))

	view, err := h.replace(keyed, p, t, run.body, run.item.Version)
	if err != nil {
		h.endItem(r.Context(), pending.ref, pending.token)

		return h.bulkFailure(r, run.item.ID, err)
	}

	if !completion.Stored() {
		// The store kept no answer: free the key, so a retry does the item
		// again.
		faultlog.LogFault(
			r.Context(),
			h.logger,
			"the store kept no answer for a bulk item",
			faultlog.CauseUnclassified,
			"bulk.item",
		)
		h.endItem(r.Context(), pending.ref, pending.token)
	}

	return bulkResult{ID: run.item.ID, Status: statusOK, Version: view.Settings.Version, Error: nil}
}

// endItem frees the key of an item that ended with no stored answer.
func (h *Handler) endItem(ctx context.Context, ref idempotency.Ref, token string) {
	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), endTimeout)
	defer cancel()

	h.abandon(endCtx, ref, token)
}

// errNoAnswer reports a step that made neither an answer nor an error. It
// maps to the internal error answer.
var errNoAnswer = errors.New("the step made no answer")
