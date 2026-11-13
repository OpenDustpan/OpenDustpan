package settings

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// getSettings answers GET .../units/{unit_id}/settings with the settings of the
// unit and the effective value of each field. A unit with no record answers
// version 0 with every field from the namespace or the built-in value.
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request, _ identity.Principal, t *target) {
	ctx := r.Context()

	view, err := h.service.Read(ctx, t.obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(view.Settings.Version))
	h.writeJSON(ctx, w, settingsOf(&view))
}

// putSettings answers PUT .../units/{unit_id}/settings with the settings that
// the request stores in place of the record at the version of If-Match. A
// request with the Idempotency-Key header runs once: the same key with the same
// request answers the first answer again.
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	expected, err := requireIfMatch(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !idempotent.Present(r) {
		h.storeSettings(w, r, p, t, expected)

		return
	}

	if h.idem == nil {
		h.writeError(ctx, w, errKeyUnsupported)

		return
	}

	// The checks of the body run before the replay, so a caller who lost an
	// action never gets the stored answer.
	cur, err := h.keyedPreflight(w, r, p, t)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	err = h.idem.Handle(w, r, idempotent.Call{
		Principal: p.Subject,
		Build: func(result any) (idempotency.Answer, error) {
			out, ok := result.(unitsettings.PutResult)
			if !ok {
				return idempotency.Answer{}, errWritten
			}

			view := cur.Updated(&out.Settings, out.Defaults)

			return settingsAnswer(&view)
		},
		SecretAnswer: nil,
		Required:     false,
		Serve:        func(w http.ResponseWriter, r *http.Request) { h.storeSettings(w, r, p, t, expected) },
	})
	if err != nil {
		h.writeError(ctx, w, err)
	}
}

// storeSettings stores the body of r as the settings of the unit of t, over the
// record at version expected, and answers with the new record.
func (h *Handler) storeSettings(
	w http.ResponseWriter, r *http.Request, p identity.Principal, t *target, expected int64,
) {
	ctx := r.Context()

	var body settingsBody
	if err := decodeBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	view, err := h.replace(r, p, t, &body, expected)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	answer, err := settingsAnswer(&view)
	h.writeAnswer(ctx, w, &answer, err)
}

// replace stores body as the settings of the unit of t, over the record at
// version expected. It checks, besides the actions of the wrapper, the use
// action for a connection that the write names and the steps action for a
// change of the steps. It is the one path of a single write and of each item of
// a bulk call.
func (h *Handler) replace(
	r *http.Request, p identity.Principal, t *target, body *settingsBody, expected int64,
) (unitsettingssvc.View, error) {
	ctx := r.Context()

	in, err := body.toSettings(t.obj)
	if err != nil {
		return unitsettingssvc.View{}, err
	}

	if err = in.Validate(); err != nil {
		return unitsettingssvc.View{}, fmt.Errorf("unit settings: %w", err)
	}

	cur, err := h.service.Read(ctx, t.obj)
	if err != nil {
		return unitsettingssvc.View{}, err
	}

	// The version check comes first: the caller can read the unit, and a stale
	// version is the answer that makes it read again.
	//
	// Defense in depth: the store compares the version again in the
	// transaction of the write, and the fenced transaction serializes the
	// writers. This check stays so that a stale version never reaches the
	// checks of the actions or the write, and it relies on neither the store
	// nor the lock of the namespace row.
	if cur.Settings.Version != expected {
		return unitsettingssvc.View{}, unitsettings.ErrConflict
	}

	if authErr := h.authorizeExtra(r, p, t, unitsettingssvc.NeedsOf(&cur, &in)); authErr != nil {
		return unitsettingssvc.View{}, authErr
	}

	view, err := h.service.Put(ctx, &in, expected, entryOf(r, p))
	if err != nil {
		return unitsettingssvc.View{}, err
	}

	return view, nil
}

// authorizeExtra checks the actions that the body decides. A refusal is
// recorded in the audit log by the guard.
//
// Defense in depth: the wrapper checked unit:manage already, and the store
// checks the fence and the version. This check stays so that a write that
// names a connection or changes the steps never relies on unit:manage alone.
func (h *Handler) authorizeExtra(
	r *http.Request, p identity.Principal, t *target, needs unitsettingssvc.Needs,
) error {
	ctx := r.Context()

	if needs.Connection != "" {
		action := identity.ActionNamespaceUseGitHostConnection

		if err := h.guard.Authorize(ctx, r, p, action, identity.NamespaceObject(t.ns.Name)); err != nil {
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	if needs.Steps {
		action := identity.ActionUnitDefineSteps

		if err := h.guard.Authorize(ctx, r, p, action, t.obj); err != nil {
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}

// deleteSettings answers DELETE .../units/{unit_id}/settings with 204. The
// record at the version of If-Match goes, and every field of the unit comes from
// the namespace or the built-in value again. The Idempotency-Key header makes a
// retry safe.
func (h *Handler) deleteSettings(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	expected, err := requireIfMatch(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !idempotent.Present(r) {
		h.resetSettings(w, r, p, t, expected)

		return
	}

	if h.idem == nil {
		h.writeError(ctx, w, errKeyUnsupported)

		return
	}

	err = h.idem.Handle(w, r, idempotent.Call{
		Principal:    p.Subject,
		Build:        func(any) (idempotency.Answer, error) { return deletedAnswer(), nil },
		SecretAnswer: nil,
		Required:     false,
		Serve:        func(w http.ResponseWriter, r *http.Request) { h.resetSettings(w, r, p, t, expected) },
	})
	if err != nil {
		h.writeError(ctx, w, err)
	}
}

// resetSettings removes the record of the unit of t at version expected.
func (h *Handler) resetSettings(
	w http.ResponseWriter, r *http.Request, p identity.Principal, t *target, expected int64,
) {
	ctx := r.Context()

	cur, err := h.service.Read(ctx, t.obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the store compares the version again in the
	// transaction of the delete. The second case stays so that a stale version
	// never reaches the store, and it relies on neither the store nor the lock
	// of the namespace row.
	switch {
	case cur.Settings.Version == 0:
		h.writeError(ctx, w, errNoRecord)

		return
	case cur.Settings.Version != expected:
		h.writeError(ctx, w, unitsettings.ErrConflict)

		return
	}

	if err := h.service.Reset(ctx, t.obj, expected, entryOf(r, p)); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	idempotent.Write(w, deletedAnswer())
}

// getSuggestions answers GET .../units/{unit_id}/settings/suggestions with the
// code locations that the git details of recent updates of the unit name. It
// never sets a field: a person accepts a suggestion by saving the settings.
func (h *Handler) getSuggestions(w http.ResponseWriter, r *http.Request, _ identity.Principal, t *target) {
	ctx := r.Context()
	out := suggestionsResponse{Suggestions: []suggestionBody{}}

	if h.suggestions != nil {
		found, err := h.suggestions.Suggest(ctx, t.obj)
		if err != nil {
			h.writeError(ctx, w, fmt.Errorf("suggest: %w", err))

			return
		}

		for i := range found {
			s := &found[i]
			out.Suggestions = append(out.Suggestions, suggestionBody{
				Repository: s.Repository, Commit: s.Commit, Source: s.Source, ObservedAt: wireTime(s.ObservedAt),
			})
		}
	}

	h.writeJSON(ctx, w, out)
}

// getDefaults answers GET .../settings/unit-defaults with the unit defaults of
// the namespace.
func (h *Handler) getDefaults(w http.ResponseWriter, r *http.Request, _ identity.Principal, t *target) {
	ctx := r.Context()

	d, err := h.service.Defaults(ctx, t.ns.Name)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(d.Version))
	h.writeJSON(ctx, w, defaultsOf(&d))
}

// putDefaults answers PUT .../settings/unit-defaults with the defaults that the
// request stores in place of the defaults at the version of If-Match. The
// Idempotency-Key header makes a retry safe.
func (h *Handler) putDefaults(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	expected, err := requireIfMatch(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !idempotent.Present(r) {
		h.storeDefaults(w, r, p, t, expected)

		return
	}

	if h.idem == nil {
		h.writeError(ctx, w, errKeyUnsupported)

		return
	}

	err = h.idem.Handle(w, r, idempotent.Call{
		Principal: p.Subject,
		Build: func(result any) (idempotency.Answer, error) {
			stored, ok := result.(unitsettings.NamespaceDefaults)
			if !ok {
				return idempotency.Answer{}, errWritten
			}

			return defaultsAnswer(&stored)
		},
		SecretAnswer: nil,
		Required:     false,
		Serve:        func(w http.ResponseWriter, r *http.Request) { h.storeDefaults(w, r, p, t, expected) },
	})
	if err != nil {
		h.writeError(ctx, w, err)
	}
}

// storeDefaults stores the pool of the body of r as the unit defaults of the
// namespace of t, over the defaults at version expected.
func (h *Handler) storeDefaults(
	w http.ResponseWriter, r *http.Request, p identity.Principal, t *target, expected int64,
) {
	ctx := r.Context()

	var body defaultsBody
	if err := decodeBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	d, err := h.service.SetDefaults(ctx, t.ns.Name, body.Pool, expected, entryOf(r, p))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	answer, err := defaultsAnswer(&d)
	h.writeAnswer(ctx, w, &answer, err)
}

func defaultsOf(d *unitsettings.NamespaceDefaults) defaultsResponse {
	return defaultsResponse{
		Namespace: d.Namespace.String(), Pool: d.Pool, Version: d.Version, UpdatedAt: wireTime(d.UpdatedAt),
	}
}

// bulkPut answers POST .../unit-settings/bulk-put. It stores one settings body
// as the settings of each unit that the request names, over the version that
// the caller read for that unit. The call is not atomic: an item that fails
// does not stop or undo the others. Each item has its own authorization checks,
// its own write and its own audit entry, as a call on one unit has. The answer
// is 200 with one result for each unit, in the order of the request. An item
// that does not exist and an item that the caller cannot read give the same
// error.
//
// With the Idempotency-Key header, each item keeps its own replay record. A
// retry answers the stored result of an item that was written, and does an item
// that failed again.
func (h *Handler) bulkPut(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	if idempotent.Present(r) && h.records == nil {
		h.writeError(ctx, w, errKeyUnsupported)

		return
	}

	var body bulkBody

	raw, err := readBody(w, r, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if len(body.Units) == 0 || len(body.Units) > MaxBulkUnits {
		h.writeError(ctx, w, errBulkSize)

		return
	}

	if body.Settings == nil {
		h.writeError(ctx, w, errBody)

		return
	}

	var call *bulkCall

	if idempotent.Present(r) {
		call, err = h.beginBulk(r, p, raw)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		defer call.end(ctx, h)

		if !call.started {
			w.Header().Set(idempotent.ReplayedHeader, "true")
		}
	}

	out := bulkResponse{Results: make([]bulkResult, 0, len(body.Units))}

	for _, item := range body.Units {
		out.Results = append(out.Results, h.bulkItem(r, p, t, &bulkRun{item: item, body: body.Settings, call: call}))
	}

	call.finish()
	h.writeJSON(ctx, w, out)
}

// bulkItem runs one item of a bulk call. call is nil for a call with no key.
func (h *Handler) bulkItem(r *http.Request, p identity.Principal, ns *target, run *bulkRun) bulkResult {
	if run.call != nil {
		return h.bulkItemKeyed(r, p, ns, run)
	}

	t, err := h.bulkTarget(r, p, ns, run.item)
	if err != nil {
		return h.bulkFailure(r, run.item.ID, err)
	}

	view, err := h.replace(r, p, &t, run.body, run.item.Version)
	if err != nil {
		return h.bulkFailure(r, run.item.ID, err)
	}

	return bulkResult{ID: run.item.ID, Status: statusOK, Version: view.Settings.Version, Error: nil}
}

// bulkTarget finds the unit of an item and checks the actions of the wrapper on
// it, as a call on that unit alone does.
func (h *Handler) bulkTarget(
	r *http.Request, p identity.Principal, ns *target, item bulkUnit,
) (target, error) {
	ctx := r.Context()

	if item.Version < 0 {
		return target{}, errIfMatchInvalid
	}

	t, err := h.unitIn(r, ns.ns, item.ID)
	if err != nil {
		return target{}, err
	}

	read := identity.ActionUnitRead
	manage := identity.ActionUnitManage

	g := route.Guard{Tokens: nil, Sessions: nil, Refusals: h.guard}
	checks := route.Checks{
		Namespace:     identity.NamespaceObject(ns.ns.Name),
		Reads:         []identity.Action{read},
		Actions:       []identity.Action{manage},
		NotFound:      errNotFound,
		RequireAction: false,
	}
	if err := g.AuthorizeObject(ctx, r, p, t.obj, checks); err != nil {
		return target{}, err
	}

	return t, nil
}

// bulkFailure makes the result of an item that failed. The error is the error
// that a call on that unit alone answers.
func (h *Handler) bulkFailure(r *http.Request, id string, err error) bulkResult {
	pr := h.writer().Problem(err)

	// The code is the internal error code until the code of the problem is
	// proved to be a code of the catalog with its arguments.
	failure := bulkError{Code: pr.Status, ErrorCode: errcode.ServerInternal, ErrorArgs: nil, Message: pr.Message}

	if made, mErr := errcode.New(pr.Code, pr.Values...); mErr == nil {
		failure.ErrorCode = pr.Code
		failure.ErrorArgs = made.Args()
	}

	if pr.Status >= http.StatusInternalServerError {
		faultlog.LogInternal(r.Context(), h.logger, "bulk item failed", faultlog.Cause(err), pr.Code)
	}

	return bulkResult{ID: id, Status: statusError, Version: 0, Error: &failure}
}

// requireIfMatch returns the version of the If-Match header. The header holds
// one quoted version, as the ETag of a read gives it: "0" names a unit with no
// record. A request without the header is errPreconditionRequired, and a value
// that is not one version is errIfMatchInvalid.
func requireIfMatch(r *http.Request) (int64, error) {
	raw := apidesc.Header(r, ifMatchHeader)
	if raw == "" {
		return 0, errPreconditionRequired
	}

	if len(raw) < 3 || !strings.HasPrefix(raw, `"`) || !strings.HasSuffix(raw, `"`) {
		return 0, errIfMatchInvalid
	}

	v, err := strconv.ParseInt(raw[1:len(raw)-1], 10, 64)
	if err != nil || v < 0 {
		return 0, errIfMatchInvalid
	}

	return v, nil
}

// etag returns the ETag of a version.
func etag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// decodeBody reads and decodes the body of r into v. It refuses a body above
// unitsettings.MaxBodyBytes, text that a decode would change, an unknown field
// and data after the document.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	if err := textbody.Decode(w, r, unitsettings.MaxBodyBytes, v); err != nil {
		if errors.Is(err, textbody.ErrTooLarge) {
			return err
		}

		return fmt.Errorf("%w: %w", errBody, err)
	}

	return nil
}

// entryOf describes the request and the caller for the audit entry of a
// change. The service and the store add the namespace, the time, the object and
// the action.
func entryOf(r *http.Request, p identity.Principal) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: p.Subject, IDPSubject: "", TokenID: p.TokenID, Action: "",
		Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: audit.TruncateUserAgent(apidesc.Header(r, "User-Agent")),
		Channel:   sessionauth.AuditChannel(r, channelHeader), Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: requestid.FromContext(r.Context()), ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: string(p.Kind), JobID: p.JobID, Reason: "",
	}
}
