package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// maxLockBodyBytes bounds the body of a manual lock. The body holds one reason
// of at most 500 characters. A JSON escape writes a character outside the Basic
// Multilingual Plane as a surrogate pair of 12 bytes, so the reason takes up to
// 6000 bytes, and the rest of the body takes less than 200 bytes.
const maxLockBodyBytes = 8192

// errLockBody names the body that a manual lock needs. Its text is the error of
// the 400 answer.
var errLockBody = errors.New(`invalid lock body: provide {"reason":"<why the object is locked>"}`)

// errLockText is the answer for a lock body that is not valid UTF-8 or that
// holds a lone surrogate escape. The server refuses the reason and does not
// change it.
var errLockText = errors.New(
	`invalid lock body: send the reason as valid UTF-8 text, with no invalid byte and no lone surrogate escape such as \ud800`,
)

// The routes of the manual locks. A refusal of a manual lock names the same
// routes, so both read them from package refusal.
const (
	unitLockPath       = refusal.UnitLockPath
	collectionLockPath = refusal.CollectionLockPath
	namespaceLockPath  = refusal.NamespaceLockPath
)

// lockRoutes returns the manual lock rows of the route table. Reading a lock
// needs read access to the object. Taking a lock and removing your own lock
// need unit:lock on the object, and a force-unlock needs unit:manage on it.
// The object of a collection route is the collection, so a grant on the
// collection or on its namespace reaches it. The object of a namespace route is
// the namespace, so only a grant on the namespace reaches it.
func (h *Handler) lockRoutes() []Route {
	unit := func(method, path string, action identity.Action, serve RouteHandler) Route {
		return Route{
			Method:           method,
			Path:             path,
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{action},
			Resolve:          resolveUnitQuery,
			Serve:            serve,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
		}
	}

	coll := func(method, path string, action identity.Action, serve RouteHandler) Route {
		return Route{
			Method:           method,
			Path:             path,
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{action},
			Resolve:          resolveCollectionPath,
			Serve:            serve,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
		}
	}

	space := func(method, path string, action identity.Action, serve RouteHandler) Route {
		return Route{
			Method:           method,
			Path:             path,
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{action},
			Resolve:          resolveCollectionNamespace,
			Serve:            serve,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
		}
	}

	return []Route{
		unit(http.MethodPut, unitLockPath, identity.ActionUnitLock, h.putLock),
		unit(http.MethodGet, unitLockPath, identity.ActionUnitRead, h.getLock),
		unit(http.MethodDelete, unitLockPath, identity.ActionUnitLock, h.deleteLock),
		unit(http.MethodPost, unitLockPath+"/force-unlock", identity.ActionUnitManage, h.forceUnlock),
		coll(http.MethodPut, collectionLockPath, identity.ActionUnitLock, h.putLock),
		coll(http.MethodGet, collectionLockPath, identity.ActionCollectionRead, h.getLock),
		coll(http.MethodDelete, collectionLockPath, identity.ActionUnitLock, h.deleteLock),
		coll(http.MethodPost, collectionLockPath+"/force-unlock", identity.ActionUnitManage, h.forceUnlock),
		space(http.MethodPut, namespaceLockPath, identity.ActionUnitLock, h.putLock),
		space(http.MethodGet, namespaceLockPath, identity.ActionNamespaceRead, h.getLock),
		space(http.MethodDelete, namespaceLockPath, identity.ActionUnitLock, h.deleteLock),
		space(http.MethodPost, namespaceLockPath+"/force-unlock", identity.ActionUnitManage, h.forceUnlock),
	}
}

// lockEntryDocument is a manual lock as the console reads it. Kind is "unit",
// "collection" or "namespace". Object is the unit reference,
// <namespace>/<collection>, or the name of the namespace. Since is an RFC 3339
// time in UTC.
//
// Contract: never remove or rename these fields.
type lockEntryDocument struct {
	Kind   string `json:"kind"`
	Object string `json:"object"`
	Holder string `json:"holder"`
	Reason string `json:"reason"`
	Since  string `json:"since"`
}

// lockRequest is the body of a lock. The field is a pointer so that a missing
// reason differs from an empty one.
type lockRequest struct {
	Reason *string `dustpan:"required" json:"reason"`
}

// manualLockEntry is the manual lock flag of a unit in the unit list: the lock
// that covers the unit now, with the object that holds it.
//
// Contract: never remove or rename these fields.
type manualLockEntry = lockEntryDocument

// lockTarget returns the object of a lock route: the unit, the collection or the
// namespace that the route resolved. An object of another kind gives a target
// that is not valid, which the lock service refuses.
func lockTarget(obj identity.Object) manuallock.Target {
	switch {
	case obj.IsCollection():
		return manuallock.CollectionTarget(collectionRef(obj))
	case obj.IsNamespace():
		return manuallock.NamespaceTarget(obj.Namespace())
	case !obj.IsUnit():
		// A unit target with no name is not valid: the lock service refuses it.
		return manuallock.UnitTarget(collection.UnitRef{Namespace: "", Tool: "", Name: ""})
	}

	return manuallock.UnitTarget(collection.UnitRef{
		Namespace: obj.Namespace(), Tool: obj.Tool(), Name: obj.UnitName(),
	})
}

// lockObjectName returns the name that a lock document shows for its object.
func lockObjectName(t manuallock.Target) string {
	switch t.Kind {
	case manuallock.KindCollection:
		return t.Collection.String()
	case manuallock.KindNamespace:
		return t.Space.String()
	case manuallock.KindUnit:
		return t.Unit.String()
	default:
		return ""
	}
}

// toLockDocument maps a manual lock to its wire shape.
func toLockDocument(l manuallock.Lock) lockEntryDocument {
	return lockEntryDocument{
		Kind:   string(l.Target.Kind),
		Object: lockObjectName(l.Target),
		Holder: l.Holder,
		Reason: l.Reason,
		Since:  formatTime(l.Since),
	}
}

// toManualLockEntry maps the manual lock flag of a projection entry, or nil when
// no manual lock covers the unit, so the field stays omitted.
func toManualLockEntry(l *coreinventory.ManualLock) *manualLockEntry {
	if l == nil {
		return nil
	}

	return &manualLockEntry{
		Kind: l.Kind, Object: l.Object, Holder: l.Holder, Reason: l.Reason, Since: formatTime(l.Since),
	}
}

// putLock serves PUT on a lock route. The body gives the reason. It answers 200
// with the lock. An object that has a lock answers 409 and names its holder. The
// write and its audit entry are one transaction in the store.
func (h *Handler) putLock(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	reason, err := readLockReason(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entry := lockAuditEntry(r, p, obj, identity.ActionUnitLock, "")

	l, err := h.locks.Lock(ctx, p, lockTarget(obj), reason, entry)
	if err != nil {
		denial := refusal.Denial{
			Action: identity.ActionUnitLock, Object: obj, Reason: "", ReasonWithin: nil, Window: 0,
		}
		h.writeLockFailure(w, r, p, denial, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toLockDocument(l))
}

// getLock serves GET on a lock route. An object with no lock answers 404.
func (h *Handler) getLock(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	l, err := h.locks.Get(ctx, p, lockTarget(obj))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toLockDocument(l))
}

// deleteLock serves DELETE on a lock route: the holder removes the lock. It
// answers 204. A lock that another principal holds answers 409 and stays.
func (h *Handler) deleteLock(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	entry := lockAuditEntry(r, p, obj, identity.ActionUnitLock, "unlock")

	if err := h.locks.Unlock(ctx, p, lockTarget(obj), entry); err != nil {
		denial := refusal.Denial{
			Action: identity.ActionUnitLock, Object: obj, Reason: "", ReasonWithin: nil, Window: 0,
		}
		h.writeLockFailure(w, r, p, denial, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// forceUnlock serves POST on a force-unlock route. The route checked unit:manage
// on the object. The audit entry names the principal that forced the removal,
// and the store adds the holder and the reason of the lock that ends. A tool
// force-unlock never reaches this lock. It answers 204.
func (h *Handler) forceUnlock(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	entry := lockAuditEntry(r, p, obj, identity.ActionUnitManage, "force-unlock")

	if err := h.locks.ForceUnlock(ctx, p, lockTarget(obj), entry); err != nil {
		h.writeLockFailure(
			w,
			r,
			p,
			refusal.Denial{
				Action: identity.ActionUnitManage, Object: obj, Reason: "", ReasonWithin: nil, Window: 0,
			},
			err,
		)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Reasons of the audit entries of the lock refusals that the service decides.
const (
	reasonNotHolder   = "lock held by another principal"
	reasonLockChanged = "lock changed during the removal"
)

// writeLockFailure answers the error of a lock route. A removal that the
// service refuses because the principal does not hold the lock, or because the
// lock changed, writes an audit entry of the outcome denied: the route check
// has allowed the request, so the middleware has not recorded it. A lock that
// is already held, and an object with no lock, are state and not refusals, and
// write no entry. d holds the action and the object of the route.
func (h *Handler) writeLockFailure(
	w http.ResponseWriter, r *http.Request, p identity.Principal, d refusal.Denial, err error,
) {
	switch {
	case errors.Is(err, manuallock.ErrNotHolder):
		d.Reason = reasonNotHolder
		h.guard.Record(r.Context(), r, p, d)
	case errors.Is(err, manuallock.ErrLockChanged):
		d.Reason = reasonLockChanged
		h.guard.Record(r.Context(), r, p, d)
	default:
		// State and other errors write no entry.
	}

	h.writeError(r.Context(), w, err)
}

// lockAuditEntry returns the audit entry of a lock change. version names the
// kind of removal; it is empty for a lock.
func lockAuditEntry(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action, version string,
) audit.Entry {
	return auditEntry(r, p, obj, action, version)
}

// readLockReason decodes a lock body. It refuses an unknown field, a body that
// is not one object, and a body without a reason.
func readLockReason(w http.ResponseWriter, r *http.Request) (string, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLockBodyBytes))
	if err != nil {
		return "", errLockBody
	}

	// encoding/json replaces each invalid UTF-8 byte and each lone surrogate
	// escape with U+FFFD, so the check of the reason could never see what the
	// client sent. Refuse the body here, before the decode changes it.
	if textbody.Refused(body) {
		return "", errLockText
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in lockRequest

	if err := dec.Decode(&in); err != nil {
		return "", errLockBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errLockBody
	}

	if in.Reason == nil {
		return "", errLockBody
	}

	return *in.Reason, nil
}

// classifyLock maps the errors of the lock routes to an HTTP status, a
// message safe to show callers and a code of the catalog. ok is false for any
// other error.
func classifyLock(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, manuallock.ErrTargetNotFound):
		return problem.New(http.StatusNotFound, errcode.LockTargetNotFound, err.Error()), true
	case errors.Is(err, manuallock.ErrNotLocked):
		return problem.New(http.StatusNotFound, errcode.LockNotLocked, err.Error()), true
	case errors.Is(err, manuallock.ErrAlreadyLocked):
		return problem.New(http.StatusConflict, errcode.LockAlreadyLocked, err.Error()), true
	case errors.Is(err, manuallock.ErrNotHolder):
		return problem.New(http.StatusConflict, errcode.LockNotHolder, err.Error()), true
	case errors.Is(err, manuallock.ErrLockChanged):
		return problem.New(http.StatusConflict, errcode.LockChanged, err.Error()), true
	case errors.Is(err, manuallock.ErrLocked):
		return classifyLocked(err), true
	case errors.Is(err, manuallock.ErrReasonRequired):
		return problem.New(http.StatusBadRequest, errcode.LockReasonRequired, err.Error()), true
	case errors.Is(err, manuallock.ErrInvalidTarget):
		return problem.New(http.StatusBadRequest, errcode.LockInvalidTarget, err.Error()), true
	case errors.Is(err, audit.ErrInvalidReason):
		return problem.New(http.StatusBadRequest, errcode.AuditReasonInvalid, err.Error()), true
	case errors.Is(err, errLockBody), errors.Is(err, errLockText):
		return problem.New(http.StatusBadRequest, errcode.RequestBodyInvalid, err.Error()), true
	}

	var none problem.Problem

	return none, false
}

// classifyLocked maps a refusal of a manual lock to 409 with the code
// lock.locked. refusal.Locked reads the holder, the time and the locked object
// from the *manuallock.LockedError that err carries, and never the reason of
// the lock. A refusal without that error has no values for the arguments of
// the code, so it answers 500: every refusal of a manual lock carries one.
func classifyLocked(err error) problem.Problem {
	locked, ok := errors.AsType[*manuallock.LockedError](err)
	if !ok || locked == nil {
		return problem.New(http.StatusInternalServerError, errcode.ServerInternal, "internal error")
	}

	a, buildErr := refusal.Locked(locked)

	return problem.Built(http.StatusConflict, refusal.WithRemoval(err.Error(), locked.Lock.Target),
		errcode.LockLocked, a, buildErr)
}
