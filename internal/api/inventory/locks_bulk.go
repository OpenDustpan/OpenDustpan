package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// MaxBulkLocks is the most units that one bulk lock or bulk unlock call names.
const MaxBulkLocks = 100

// maxBulkBodyBytes bounds the body of a bulk call. The reason takes up to 6000
// bytes when each of its 500 characters is written as a 12-byte surrogate
// pair, and 100 unit IDs of at most 100 characters take at most 11200 bytes
// with their quotes, braces and commas.
const maxBulkBodyBytes = 20480

// maxBulkUnitIDLength is the longest unit ID that a bulk item may name.
const maxBulkUnitIDLength = 100

// bulkEndTimeout bounds the calls that free a key after a request that failed.
const bulkEndTimeout = 5 * time.Second

// The values of bulkResult.Status.
const (
	bulkStatusOK    = "ok"
	bulkStatusError = "error"
)

// Reasons of the audit entries that a bulk item writes for a refusal.
const reasonBulkLockDenied = "unit:lock denied for a unit of a bulk call"

var (
	// errBulkSize reports a bulk call with no unit or with too many.
	errBulkSize = fmt.Errorf("a bulk call needs 1 to %d units", MaxBulkLocks)

	// errBulkNotFound is the result of an item that names no unit that the
	// caller can read. A unit that does not exist, a unit of another namespace
	// and a unit that the caller cannot read all give this one error, so a
	// bulk call tells no more than the single calls do.
	errBulkNotFound = fmt.Errorf("%w: the unit does not exist or you cannot read it", manuallock.ErrTargetNotFound)

	// errBulkKeyUnsupported reports a request with the Idempotency-Key header
	// to a server that keeps no replay records.
	errBulkKeyUnsupported = errors.New(
		"this server keeps no replay records: send the request without the Idempotency-Key header",
	)

	// errBulkWritten reports a stored answer that the route cannot read.
	errBulkWritten = errors.New("the store passed a result that the route did not expect")
)

// bulkItem names one unit of a bulk call by its stable ID.
type bulkItem struct {
	ID string `dustpan:"required" json:"id"`
}

// bulkLockBody is the body of a bulk lock: one reason and the units.
type bulkLockBody struct {
	Reason *string    `dustpan:"required" json:"reason"`
	Units  []bulkItem `dustpan:"required" json:"units"`
}

// bulkUnlockBody is the body of a bulk unlock: the units.
type bulkUnlockBody struct {
	Units []bulkItem `dustpan:"required" json:"units"`
}

// bulkResult is the result of one item. Status is ok or error. A result holds
// a lock or an error, never both: the constructors below are the only way to
// make one.
type bulkResult struct {
	ID     string             `json:"id"`
	Status string             `json:"status"`
	Lock   *lockEntryDocument `json:"lock,omitempty"`
	Error  *problem.Body      `json:"error,omitempty"`
}

// bulkResponse holds one result for each unit of the request, in its order.
type bulkResponse struct {
	Results []bulkResult `json:"results"`
}

// lockedResult is the result of a unit that the call locked.
func lockedResult(id string, l manuallock.Lock) bulkResult {
	doc := toLockDocument(l)

	return bulkResult{ID: id, Status: bulkStatusOK, Lock: &doc, Error: nil}
}

// unlockedResult is the result of a unit that the call unlocked.
func unlockedResult(id string) bulkResult {
	return bulkResult{ID: id, Status: bulkStatusOK, Lock: nil, Error: nil}
}

// bulkRun is what one item of a call shares with the others: the reason (empty
// for an unlock), the direction and the replay record of the call.
type bulkRun struct {
	// raw is the body of the call, which the call record is compared by.
	raw    []byte
	units  []bulkItem
	reason string
	unlock bool
	// call is nil for a call with no Idempotency-Key header.
	call *bulkCall
	// ran is true when an item ran again in this request: its store call ran, or
	// it failed with a 5xx or 429 status. replayed is true when an item answered
	// its stored result. The two decide the Idempotent-Replayed header.
	ran      bool
	replayed bool
}

// bulkCall is the replay record of a whole call. It holds the fingerprint of
// the body, so the same key with another body is refused. The results of the
// items are in the records of the items.
type bulkCall struct {
	ref   idempotency.Ref
	key   idempotency.Key
	token string
	// started is true for the first request with the key.
	started bool
	// done is true when the call sent an answer for each unit.
	done bool
}

// bulkLock serves POST .../unit-locks/bulk-lock. It locks each unit of the call
// with the reason of the call. The call is not atomic: a unit that fails does
// not stop or undo the others. The answer is 200 with one result for each unit,
// in the order of the request.
func (h *Handler) bulkLock(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if idempotent.Present(r) && h.records == nil {
		h.writeError(ctx, w, errBulkKeyUnsupported)

		return
	}

	var body bulkLockBody

	raw, err := h.readBulkBody(w, r, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if body.Reason == nil {
		h.writeError(ctx, w, errLockBody)

		return
	}

	// Defense in depth: Service.Lock checks the reason for each item too. This
	// check stays so that a bad reason refuses the whole call before any item
	// runs, and does not rely on the service or on the store.
	if err = checkLockReason(*body.Reason); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.runBulk(w, r, p, obj, &bulkRun{raw: raw, units: body.Units, reason: *body.Reason, unlock: false, call: nil, ran: false, replayed: false})
}

// bulkUnlock serves POST .../unit-locks/bulk-unlock. It removes the lock that
// the caller holds on each unit of the call, as the single unlock does.
func (h *Handler) bulkUnlock(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if idempotent.Present(r) && h.records == nil {
		h.writeError(ctx, w, errBulkKeyUnsupported)

		return
	}

	var body bulkUnlockBody

	raw, err := h.readBulkBody(w, r, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.runBulk(w, r, p, obj, &bulkRun{raw: raw, units: body.Units, reason: "", unlock: true, call: nil, ran: false, replayed: false})
}

// checkLockReason refuses a reason that a lock refuses.
func checkLockReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return manuallock.ErrReasonRequired
	}

	if err := audit.ValidateReason(reason); err != nil {
		return fmt.Errorf("manual lock reason: %w", err)
	}

	return nil
}

// readBulkBody reads and decodes the body of a bulk call. The decode refuses
// an unknown field, text that a decode would change, and text after the first
// JSON value. It returns the bytes, which the call record is compared by.
func (h *Handler) readBulkBody(w http.ResponseWriter, r *http.Request, v any) ([]byte, error) {
	raw, err := textbody.Read(w, r, maxBulkBodyBytes)
	if err != nil {
		return nil, err
	}

	if err := textbody.Unmarshal(raw, v); err != nil {
		return nil, err
	}

	return raw, nil
}

// runBulk checks the count, starts the call record when the request has the
// key, runs the items in order and answers.
func (h *Handler) runBulk(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, run *bulkRun,
) {
	ctx := r.Context()
	units := run.units

	if len(units) == 0 || len(units) > MaxBulkLocks {
		h.writeError(ctx, w, errBulkSize)

		return
	}

	if idempotent.Present(r) {
		call, err := h.beginBulkCall(r, p, run.raw)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		run.call = call

		defer call.end(ctx, h)
	}

	out := bulkResponse{Results: make([]bulkResult, 0, len(units))}

	for pos, item := range units {
		// Fail closed: a request that ended runs no further item. An item that
		// did not run has no lock and no audit entry, and a retry with the same
		// key does it. The call record stays unfinished, so the key is freed.
		if ctx.Err() != nil {
			return
		}

		out.Results = append(out.Results, h.bulkItem(r, p, obj, run, pos, item.ID))
	}

	run.call.finish()

	// The answer is a replay only when no item ran again. A first call runs
	// every item, so it is never a replay.
	if run.call != nil && !run.ran && (run.replayed || !run.call.started) {
		w.Header().Set(idempotent.ReplayedHeader, "true")
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// bulkTarget is the unit of an item: the ID that the item named and the object
// that the ID resolved to.
type bulkTarget struct {
	id  string
	obj identity.Object
	// pos is the position of the item in the request. The key of the item
	// holds it, so a repeated ID is two items with two records.
	pos int
}

// bulkItem runs one item.
func (h *Handler) bulkItem(
	r *http.Request, p identity.Principal, ns identity.Object, run *bulkRun, pos int, id string,
) bulkResult {
	obj, err := h.bulkUnit(r, p, ns, id)
	if err != nil {
		out := h.bulkFailure(r, id, err)
		if out.Error != nil && !storableFailure(out) {
			// A 5xx or 429 result is not kept: the item ran again.
			run.ran = true
		}

		return out
	}

	unit := bulkTarget{id: id, obj: obj, pos: pos}

	if run.call == nil {
		return h.bulkApply(r, p, unit, run)
	}

	return h.bulkItemKeyed(r, p, unit, run)
}

// bulkUnit finds the unit of an item and checks unit:read on it. A unit that
// does not exist, a unit of another namespace and a unit that the caller cannot
// read give errBulkNotFound.
//
// Defense in depth: the route checked namespace:read, and Service.Lock checks
// unit:lock again. The ID check, the namespace check, the check that the unit
// has the ID that the item named (e.UnitID != id) and the unit:read check each
// stay on their own, so none relies on the others.
func (h *Handler) bulkUnit(
	r *http.Request, p identity.Principal, ns identity.Object, id string,
) (identity.Object, error) {
	ctx := r.Context()

	if !ident.Valid(id, maxBulkUnitIDLength) {
		return identity.Object{}, errBulkNotFound
	}

	e, err := h.units.GetUnit(ctx, id)
	if errors.Is(err, coreinventory.ErrUnitNotFound) {
		return identity.Object{}, errBulkNotFound
	}

	if err != nil {
		return identity.Object{}, fmt.Errorf("inventory unit: %w", err)
	}

	if e.Namespace != ns.Namespace() || e.UnitID != id {
		return identity.Object{}, errBulkNotFound
	}

	unit, err := identity.UnitObject(e.Tool, ns.Namespace(), e.Name)
	if err != nil {
		return identity.Object{}, errBulkNotFound
	}

	unit = unit.WithUnitID(e.UnitID)

	if err = h.guard.Authorize(ctx, r, p, identity.ActionUnitRead, unit); err != nil {
		if errors.Is(err, identity.ErrDenied) {
			return identity.Object{}, errBulkNotFound
		}

		return identity.Object{}, fmt.Errorf("authorize unit:read: %w", err)
	}

	return unit, nil
}

// bulkApply locks or unlocks one unit with the same code as the single call.
func (h *Handler) bulkApply(r *http.Request, p identity.Principal, unit bulkTarget, run *bulkRun) bulkResult {
	ctx := r.Context()

	if run.unlock {
		if err := h.unlockOne(ctx, r, p, unit.obj); err != nil {
			return h.bulkLockFailure(r, p, unit, err)
		}

		return unlockedResult(unit.id)
	}

	l, err := h.lockOne(ctx, r, p, unit.obj, run.reason)
	if err != nil {
		return h.bulkLockFailure(r, p, unit, err)
	}

	return lockedResult(unit.id, l)
}

// bulkLockFailure makes the result of an item that the lock service refused. A
// denial of unit:lock writes an audit entry of the outcome denied, as the route
// check of a single call does.
func (h *Handler) bulkLockFailure(r *http.Request, p identity.Principal, unit bulkTarget, err error) bulkResult {
	if errors.Is(err, identity.ErrDenied) {
		h.guard.Record(r.Context(), r, p, refusal.Denial{
			Action:       identity.ActionUnitLock,
			Object:       unit.obj,
			Reason:       reasonBulkLockDenied,
			ReasonWithin: nil,
			Window:       0,
		})
	}

	return h.bulkFailure(r, unit.id, err)
}

// bulkFailure makes the result of an item that failed. The error is the error
// that a call on that unit alone answers.
func (h *Handler) bulkFailure(r *http.Request, id string, err error) bulkResult {
	pr := problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classify}.Problem(err)

	// The code is the internal error code until the code of the problem is
	// proved to be a code of the catalog with its arguments.
	failure := problem.Body{
		Message: pr.Message, Code: pr.Status, ErrorCode: errcode.ServerInternal, ErrorArgs: nil,
		Reauthenticate: "", Units: nil,
	}

	if made, mErr := errcode.New(pr.Code, pr.Values...); mErr == nil {
		failure.ErrorCode = pr.Code
		failure.ErrorArgs = made.Args()
	}

	if pr.Status >= http.StatusInternalServerError {
		faultlog.LogInternal(r.Context(), h.logger, "bulk lock item failed", faultlog.Cause(err), pr.Code)
	}

	return bulkResult{ID: id, Status: bulkStatusError, Lock: nil, Error: &failure}
}

// keyRefusal returns the refusal of the Idempotency-Key with a code of the
// catalog.
func keyRefusal(status int, code, message string) *idempotent.Error {
	answer, err := errcode.New(code)
	if err != nil {
		answer = errcode.Internal()
		status = http.StatusInternalServerError
	}

	return &idempotent.Error{Answer: answer, Message: message, Status: status}
}

// digestKey returns a key for a record that derives from key and the parts.
// The digest keeps the key inside the length limit, whatever the parts hold.
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

// beginBulkCall finds or starts the record of a call with the key of r. raw is
// the body of the call. It returns an *idempotent.Error for a refusal.
func (h *Handler) beginBulkCall(r *http.Request, p identity.Principal, raw []byte) (*bulkCall, error) {
	values := apidesc.HeaderValues(r, idempotent.Header)
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

	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bulkEndTimeout)
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

// itemMaterial is what the record of an item is compared by: the unit, the
// direction and the reason.
type itemMaterial struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Unlock bool   `json:"unlock"`
}

// bulkItemKeyed runs one item of a call with a key. The checks of the item run
// before its record, so a replay checks unit:read of the caller on the unit
// again. The record of an item holds its result, except a result with a 5xx or
// 429 status: a retry answers the stored result and does the other items
// again. The key of an item holds its position in the call.
func (h *Handler) bulkItemKeyed(r *http.Request, p identity.Principal, unit bulkTarget, run *bulkRun) bulkResult {
	ctx := r.Context()
	id := unit.id

	itemKey, err := digestKey("item-", run.call.key, strconv.Itoa(unit.pos), id)
	if err != nil {
		return h.bulkFailure(r, id, err)
	}

	material, err := json.Marshal(itemMaterial{ID: id, Reason: run.reason, Unlock: run.unlock})
	if err != nil {
		return h.bulkFailure(r, id, fmt.Errorf("fingerprint item: %w", err))
	}

	ref := idempotency.Ref{Principal: p.Subject, Method: r.Method, Path: r.URL.EscapedPath(), Key: itemKey}

	res, err := h.records.Begin(ctx, ref, material, time.Time{})
	if err != nil {
		return h.bulkFailure(r, id, fmt.Errorf("begin idempotent request: %w", err))
	}

	switch res.Outcome {
	case idempotency.Replayed:
		run.replayed = true

		return h.replayedBulkItem(r, id, &res.Answer)
	case idempotency.Reused:
		// Defense in depth: the record of the whole call refuses a changed
		// body first, so a changed item does not reach this case today. It
		// stays so that an item never answers the stored result of another
		// request, and it relies on neither the record of the call nor the
		// fingerprint of the call body.
		return h.bulkFailure(r, id, keyRefusal(http.StatusUnprocessableEntity,
			errcode.RequestIdempotencyKeyReused, "this Idempotency-Key was used with another request"))
	case idempotency.InProgress:
		return h.bulkFailure(r, id, keyRefusal(http.StatusConflict,
			errcode.RequestIdempotencyKeyInProgress, "the first request for this unit has not ended: retry later"))
	case idempotency.Started:
		run.ran = true

		return h.writeBulkItem(r, p, unit, run, &pendingBulkItem{ref: ref, token: res.Token})
	default:
		return h.bulkFailure(r, id, fmt.Errorf("%w: unknown outcome %d", idempotency.ErrInvalid, res.Outcome))
	}
}

// bulkItemAnswer makes the answer that the record of one item keeps: the
// result of the item. The store calls it with the lock that the write made or
// removed, in the transaction of the write.
func bulkItemAnswer(id string, run *bulkRun) func(result any) (idempotency.Answer, error) {
	return func(result any) (idempotency.Answer, error) {
		l, ok := result.(manuallock.Lock)
		if !ok {
			return idempotency.Answer{}, errBulkWritten
		}

		out := lockedResult(id, l)
		if run.unlock {
			out = unlockedResult(id)
		}

		return encodeBulkResult(out)
	}
}

// encodeBulkResult makes the answer that the record of an item keeps.
func encodeBulkResult(out bulkResult) (idempotency.Answer, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(out); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode item answer: %w", err)
	}

	return idempotency.Answer{Status: http.StatusOK, Headers: nil, Body: buf.Bytes()}, nil
}

// storableFailure reports whether the record of an item keeps this result. A
// result with a 5xx or 429 status is not kept, so a retry does the item again.
func storableFailure(out bulkResult) bool {
	if out.Status != bulkStatusError || out.Error == nil {
		return false
	}

	return out.Error.Code < http.StatusInternalServerError && out.Error.Code != http.StatusTooManyRequests
}

// pendingBulkItem names the pending record that an item started.
type pendingBulkItem struct {
	ref   idempotency.Ref
	token string
}

// writeBulkItem writes one item with a Completion, so the store keeps the
// result of the item in the transaction of the write. A write that fails frees
// the key.
func (h *Handler) writeBulkItem(
	r *http.Request, p identity.Principal, unit bulkTarget, run *bulkRun, pending *pendingBulkItem,
) bulkResult {
	completion := idempotency.NewCompletion(pending.ref, pending.token, bulkItemAnswer(unit.id, run))
	keyed := r.WithContext(idempotency.WithCompletion(r.Context(), completion))

	out := h.bulkApply(keyed, p, unit, run)

	if storableFailure(out) {
		h.keepBulkFailure(r.Context(), pending, out)

		return out
	}

	if out.Status != bulkStatusOK || !completion.Stored() {
		if out.Status == bulkStatusOK {
			// The write passed and the store kept no answer: free the key, so
			// a retry does the item again.
			faultlog.LogFault(r.Context(), h.logger, "the store kept no answer for a bulk item",
				faultlog.CauseUnclassified, "bulk.item")
		}

		h.abandon(context.WithoutCancel(r.Context()), pending.ref, pending.token)
	}

	return out
}

// keepBulkFailure stores the result of an item that the lock service refused
// with a status below 500. The refusal wrote no lock, so the store call kept no
// answer. A record that cannot be stored is freed, so a retry does the item
// again.
func (h *Handler) keepBulkFailure(ctx context.Context, pending *pendingBulkItem, out bulkResult) {
	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bulkEndTimeout)
	defer cancel()

	answer, err := encodeBulkResult(out)
	if err == nil {
		err = h.records.Complete(endCtx, pending.ref, pending.token, answer)
	}

	if err == nil || errors.Is(err, idempotency.ErrLost) {
		return
	}

	faultlog.LogFault(ctx, h.logger, "store the record of a bulk item failed", faultlog.Cause(err), "bulk.item")
	h.abandon(endCtx, pending.ref, pending.token)
}

// replayedBulkItem answers the stored result of an item.
func (h *Handler) replayedBulkItem(r *http.Request, id string, a *idempotency.Answer) bulkResult {
	var out bulkResult

	if err := json.Unmarshal(a.Body, &out); err != nil || out.ID != id {
		return h.bulkFailure(r, id, errBulkWritten)
	}

	return out
}

// lockOne locks the unit or collection or namespace obj for p with the reason.
// A single lock call and each item of a bulk lock use it, so an item cannot
// differ from a single call. It returns the error of the lock service, and
// writes the audit entry of a refusal that the service decides.
func (h *Handler) lockOne(
	ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object, reason string,
) (manuallock.Lock, error) {
	entry := lockAuditEntry(r, p, obj, identity.ActionUnitLock, "")

	l, err := h.locks.Lock(ctx, p, lockTarget(obj), reason, entry)
	if err != nil {
		h.recordLockFailure(r, p, lockDenial(obj, identity.ActionUnitLock), err)

		return manuallock.Lock{}, err
	}

	return l, nil
}

// unlockOne removes the lock that p holds on obj. A single unlock call and each
// item of a bulk unlock use it.
func (h *Handler) unlockOne(ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object) error {
	entry := lockAuditEntry(r, p, obj, identity.ActionUnitLock, "unlock")

	if err := h.locks.Unlock(ctx, p, lockTarget(obj), entry); err != nil {
		h.recordLockFailure(r, p, lockDenial(obj, identity.ActionUnitLock), err)

		return err
	}

	return nil
}

// lockDenial returns the refusal record of an action on obj, with no reason
// yet.
func lockDenial(obj identity.Object, action identity.Action) refusal.Denial {
	return refusal.Denial{Action: action, Object: obj, Reason: "", ReasonWithin: nil, Window: 0}
}
