package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// ViewPrincipals finds the principal record of a signed-in person. The ID of
// the record is the owner of the views of that person.
type ViewPrincipals interface {
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
}

// ViewsConfig holds the dependencies of the saved view routes. A Handler with
// no ViewsConfig serves no saved view route.
type ViewsConfig struct {
	Service    *savedview.Service
	Principals ViewPrincipals
	// Idempotency serves the Idempotency-Key header of the create and the
	// delete. Nil makes every request with the header answer 400.
	Idempotency *idempotent.Middleware
}

// views is the state of the saved view routes of a Handler.
type views struct {
	service    *savedview.Service
	principals ViewPrincipals
	idem       *idempotent.Middleware
}

// newViews checks the saved view config. A nil config gives nil: no saved view
// route.
func newViews(cfg *ViewsConfig) (*views, error) {
	if cfg == nil {
		return nil, nil //nolint:nilnil // No saved view routes: the config is optional.
	}

	switch {
	case cfg.Service == nil:
		return nil, fmt.Errorf("%w: saved view service", ErrMissingDependency)
	case cfg.Principals == nil:
		return nil, fmt.Errorf("%w: saved view principals", ErrMissingDependency)
	}

	return &views{service: cfg.Service, principals: cfg.Principals, idem: cfg.Idempotency}, nil
}

// classifyView maps the errors of the saved view routes. The 403 for a caller
// who is not a person has one text for every kind of principal.
func classifyView(err error) (int, string, bool) {
	switch {
	case errors.Is(err, savedview.ErrNotPerson):
		return http.StatusForbidden, savedview.ErrNotPerson.Error(), true
	case errors.Is(err, savedview.ErrNotFound):
		return http.StatusNotFound, savedview.ErrNotFound.Error(), true
	case errors.Is(err, savedview.ErrInvalid), errors.Is(err, errViewBody):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, errViewIdempotencyKey):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, savedview.ErrExists), errors.Is(err, savedview.ErrLimit):
		return http.StatusConflict, err.Error(), true
	case errors.Is(err, idempotency.ErrLost):
		return http.StatusConflict, "a newer request took this Idempotency-Key before this request ended: retry later", true
	default:
		return 0, "", false
	}
}

var (
	errViewBody = errors.New(
		`invalid saved view body: send {"name":"<name>","query":{"labels":[{"key":"<key>","value":"<value>"}],` +
			`"references":["<unit reference>"]}}`,
	)
	errViewQueryValue = fmt.Errorf(
		"%w: this route takes the pageSize and pageToken query values only: the view holds the filters",
		errInvalidQuery,
	)
	errViewIdempotencyKey = errors.New(
		"the Idempotency-Key header is not available on this server: send the request without it",
	)
)

// viewPathPrefix is the path of the saved view routes under a namespace.
const viewPathPrefix = v1Prefix + "/namespaces/{namespace}/views"

// viewRoutes returns the saved view rows of the route table. The object of
// every action is the namespace of the path. The owner rule (a person reads
// and deletes only their own view) is a check in the store and in the
// handler, and it is not a role. The units route also needs namespace:read,
// as the unit list does, and it checks unit:read for each unit again.
func (h *Handler) viewRoutes() []Route {
	nsRead := []identity.Action{identity.ActionViewRead, identity.ActionNamespaceRead}

	return []Route{
		{
			Method: http.MethodPost, Path: viewPathPrefix, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionViewCreate}, NamespaceActions: nil,
			Resolve: resolveCollectionNamespace, DeniedAsNotFound: false, Serve: h.createView,
		},
		{
			Method: http.MethodGet, Path: viewPathPrefix, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionViewRead}, NamespaceActions: nil,
			Resolve: resolveCollectionNamespace, DeniedAsNotFound: false, Serve: h.listViews,
		},
		{
			Method: http.MethodGet, Path: viewPathPrefix + "/{view_id}", Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionViewRead}, NamespaceActions: nil,
			Resolve: resolveCollectionNamespace, DeniedAsNotFound: false, Serve: h.getView,
		},
		{
			Method: http.MethodDelete, Path: viewPathPrefix + "/{view_id}", Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionViewDelete}, NamespaceActions: nil,
			Resolve: resolveCollectionNamespace, DeniedAsNotFound: false, Serve: h.deleteView,
		},
		{
			Method: http.MethodGet, Path: viewPathPrefix + "/{view_id}/units", Scope: ScopeAuthorized,
			Actions: nsRead, NamespaceActions: nil,
			Resolve: resolveCollectionNamespace, DeniedAsNotFound: false, Serve: h.listViewUnits,
		},
	}
}

// viewLabel, viewQuery and viewDocument are the wire shapes of a saved view.
//
// Contract: never remove or rename these fields.
type viewLabel struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type viewQuery struct {
	Labels     []viewLabel `json:"labels"`
	References []string    `json:"references"`
}

type viewDocument struct {
	ID        string    `json:"id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Query     viewQuery `json:"query"`
	CreatedAt string    `json:"createdAt"`
}

// listViewsResponse is the answered document of the view list. The list holds
// every view of the caller in the namespace: at most savedview.MaxPerOwner.
type listViewsResponse struct {
	Views []viewDocument `json:"views"`
}

// createViewBody is the body of a create. The fields are pointers so that a
// missing name differs from an empty one.
type createViewBody struct {
	Name  *string    `json:"name"`
	Query *viewQuery `json:"query"`
}

func toViewDocument(v savedview.View) viewDocument {
	q := viewQuery{Labels: make([]viewLabel, 0, len(v.Query.Labels)), References: make([]string, 0, len(v.Query.References))}

	for _, l := range v.Query.Labels {
		q.Labels = append(q.Labels, viewLabel(l))
	}

	q.References = append(q.References, v.Query.References...)

	return viewDocument{
		ID: string(v.ID), Namespace: v.Namespace.String(), Name: v.Name, Query: q,
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// viewLocation is the path of one view.
func viewLocation(ns namespace.Name, id savedview.ID) string {
	return v1Prefix + "/namespaces/" + url.PathEscape(ns.String()) + "/views/" + url.PathEscape(string(id))
}

// viewCaller finds the person who calls. The owner of a view is the ID of the
// principal record, found by the subject of the request. Every caller that is
// not a person gets savedview.ErrNotPerson, and the refusal is audited.
func (h *Handler) viewCaller(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action,
) (savedview.Caller, error) {
	ctx := r.Context()

	refuse := func(reason string) (savedview.Caller, error) {
		// The audit log refuses a kind that it does not know, so an unknown kind
		// is recorded as no kind. The denial is still written.
		audited := p

		switch p.Kind {
		case identity.KindPerson, identity.KindService, identity.KindWorkload:
		default:
			audited.Kind = ""
		}

		h.guard.Record(ctx, r, audited, refusal.Denial{Action: action, Object: obj, Reason: reason, Window: 0})

		return savedview.Caller{}, savedview.ErrNotPerson
	}

	// An allowlist: only the kind "person" passes. The bootstrap token, a
	// service, a workload, a namespace principal, a job, a runner and a kind
	// that the server does not know all fail the same way.
	if p.Kind != identity.KindPerson || p.JobID != "" || p.Subject == "" {
		return refuse("the caller is not a person")
	}

	rec, err := h.views.principals.GetPrincipalBySubject(ctx, p.Subject)
	if errors.Is(err, principal.ErrNotFound) {
		return refuse("the caller has no principal record")
	}

	if err != nil {
		return savedview.Caller{}, fmt.Errorf("find principal: %w", err)
	}

	// Defense in depth: the authenticator filled the kind of the request, and
	// the record says it again. A record of another kind, of a namespace, or of
	// a principal that is disabled, owns no view.
	if rec.Kind != principal.KindPerson || rec.Namespace != "" || !rec.DisabledAt.IsZero() {
		return refuse("the principal record is not an active person")
	}

	return savedview.Caller{Owner: rec.ID, Principal: p, By: viewAuditTemplate(r)}, nil
}

// viewAuditTemplate returns the fields of the audit entry that the request
// knows. The service fills the rest.
func viewAuditTemplate(r *http.Request) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: truncateUserAgent(r.UserAgent()), Channel: sessionauth.AuditChannel(r, channelHeader),
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: requestid.FromContext(r.Context()),
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// ownedView reads the view of the path for the caller. The store filters by
// owner, and the handler checks the owner again.
func (h *Handler) ownedView(
	r *http.Request, c savedview.Caller, ns namespace.Name,
) (savedview.View, error) {
	id := savedview.ID(r.PathValue("view_id"))
	if err := id.Validate(); err != nil {
		return savedview.View{}, savedview.ErrNotFound
	}

	v, err := h.views.service.Get(r.Context(), c, ns, id)
	if err != nil {
		return savedview.View{}, err
	}

	return checkViewOwner(v, c, ns, id)
}

// checkViewOwner refuses a view that is not the view the caller asked for, or
// that another owner holds.
//
// Defense in depth: the store returns only the views of the owner that it is
// given. This check stays so that a store that ignores the owner, a wrong
// query or a later change cannot serve the view of another person. It does
// not rely on the store, and the store does not rely on it.
func checkViewOwner(v savedview.View, c savedview.Caller, ns namespace.Name, id savedview.ID) (savedview.View, error) {
	if v.Owner != c.Owner || v.ID != id || v.Namespace != ns {
		return savedview.View{}, savedview.ErrNotFound
	}

	return v, nil
}

// listViews serves GET .../views: the views of the caller.
func (h *Handler) listViews(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	ns := obj.Namespace()

	c, err := h.viewCaller(r, p, obj, identity.ActionViewRead)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, nsErr := h.namespaces.Get(ctx, ns); nsErr != nil {
		h.writeError(ctx, w, nsErr)

		return
	}

	found, err := h.views.service.List(ctx, c, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := make([]viewDocument, 0, len(found))

	for _, v := range found {
		// Defense in depth: the same owner check as the get route, for each view.
		if v.Owner != c.Owner || v.Namespace != ns {
			continue
		}

		out = append(out, toViewDocument(v))
	}

	h.writeJSON(ctx, w, http.StatusOK, listViewsResponse{Views: out})
}

// getView serves GET .../views/{view_id}.
func (h *Handler) getView(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	c, err := h.viewCaller(r, p, obj, identity.ActionViewRead)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, nsErr := h.namespaces.Get(ctx, obj.Namespace()); nsErr != nil {
		h.writeError(ctx, w, nsErr)

		return
	}

	v, err := h.ownedView(r, c, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toViewDocument(v))
}

// listViewUnits serves GET .../views/{view_id}/units. It runs the query of the
// view with the code of the unit list: the same filter, the same check of
// unit:read for each unit, the same order and the same paging.
func (h *Handler) listViewUnits(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	ns := obj.Namespace()

	c, err := h.viewCaller(r, p, obj, identity.ActionViewRead)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// An allowlist of query values: a filter in the request would change the
	// query that the view stores.
	values, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		h.writeError(ctx, w, fmt.Errorf("%w: the query string is not valid", errInvalidQuery))

		return
	}

	for key := range values {
		if key != "pageSize" && key != "pageToken" {
			h.writeError(ctx, w, errViewQueryValue)

			return
		}
	}

	if _, nsErr := h.namespaces.Get(ctx, ns); nsErr != nil {
		h.writeError(ctx, w, nsErr)

		return
	}

	v, err := h.ownedView(r, c, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.serveUnitList(w, r, p, ns, filterOfView(v.Query))
}

// filterOfView returns the unit filter that a stored query means. It has the
// meaning of the label and reference query values of the unit list.
func filterOfView(q savedview.Query) unitFilter {
	var f unitFilter

	for _, l := range q.Labels {
		f.labels = append(f.labels, labelFilter{key: l.Key, value: l.Value})
	}

	if len(q.References) > 0 {
		f.references = make(map[string]struct{}, len(q.References))
	}

	for _, ref := range q.References {
		// A reference was checked when the view was made, and the store checks it
		// again on every read. A reference that does not parse here selects
		// nothing, so the filter fails closed.
		obj, err := identity.ParseObject(ref)
		if err != nil {
			f.references[ref] = struct{}{}

			continue
		}

		f.references[obj.String()] = struct{}{}
	}

	return f
}

// createView serves POST .../views. It answers 201 with the view.
func (h *Handler) createView(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	ns := obj.Namespace()

	c, err := h.viewCaller(r, p, obj, identity.ActionViewCreate)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.keyed(w, r, c, createdViewAnswer, true, func(w http.ResponseWriter, r *http.Request) {
		h.makeView(w, r, c, ns)
	})
}

// makeView reads the body and makes the view.
func (h *Handler) makeView(w http.ResponseWriter, r *http.Request, c savedview.Caller, ns namespace.Name) {
	ctx := r.Context()

	name, query, err := readViewBody(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, nsErr := h.namespaces.Get(ctx, ns); nsErr != nil {
		h.writeError(ctx, w, nsErr)

		return
	}

	v, err := h.views.service.Create(ctx, c, ns, name, query)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	answer, err := createdViewAnswer(v)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	idempotent.Write(w, answer)
}

// deleteView serves DELETE .../views/{view_id}. It answers 204.
func (h *Handler) deleteView(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	ns := obj.Namespace()

	c, err := h.viewCaller(r, p, obj, identity.ActionViewDelete)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.keyed(w, r, c, deletedViewAnswer, false, func(w http.ResponseWriter, r *http.Request) {
		h.removeView(w, r, c, ns)
	})
}

func (h *Handler) removeView(w http.ResponseWriter, r *http.Request, c savedview.Caller, ns namespace.Name) {
	ctx := r.Context()

	if _, nsErr := h.namespaces.Get(ctx, ns); nsErr != nil {
		h.writeError(ctx, w, nsErr)

		return
	}

	// Read first: the handler checks the owner before it asks for the delete.
	v, err := h.ownedView(r, c, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if err := h.views.service.Delete(ctx, c, ns, v.ID); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	answer, _ := deletedViewAnswer(v) //nolint:errcheck // The answer of a delete has no body and cannot fail.
	idempotent.Write(w, answer)
}

// createdViewAnswer answers a create: 201 with the view and its Location.
func createdViewAnswer(result any) (idempotency.Answer, error) {
	v, ok := result.(savedview.View)
	if !ok {
		return idempotency.Answer{}, errViewResult
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(toViewDocument(v)); err != nil {
		return idempotency.Answer{}, fmt.Errorf("encode saved view: %w", err)
	}

	return idempotency.Answer{
		Status:  http.StatusCreated,
		Headers: map[string]string{"Content-Type": "application/json", "Location": viewLocation(v.Namespace, v.ID)},
		Body:    buf.Bytes(),
	}, nil
}

// deletedViewAnswer answers a delete: 204 with no body.
func deletedViewAnswer(any) (idempotency.Answer, error) {
	return idempotency.Answer{Status: http.StatusNoContent, Headers: map[string]string{}, Body: nil}, nil
}

var errViewResult = errors.New("the store passed a result that is not a saved view")

// keyed runs serve. A request with the Idempotency-Key header runs inside the
// middleware, which keeps the answer in the transaction of the write. The
// record of a create keeps the ID of the view and not the view: a view can
// hold a label value that names something private. A replay of a create then
// answers 409 with the ID. The key belongs to the ID of the principal, and not
// to the subject, so a new principal with the same subject cannot replay it.
func (h *Handler) keyed(
	w http.ResponseWriter, r *http.Request, c savedview.Caller,
	build func(result any) (idempotency.Answer, error), secret bool, serve http.HandlerFunc,
) {
	ctx := r.Context()

	if !idempotent.Present(r) {
		serve(w, r)

		return
	}

	if h.views.idem == nil {
		h.writeError(ctx, w, errViewIdempotencyKey)

		return
	}

	call := idempotent.Call{
		Principal: c.Owner, Build: build, SecretAnswer: nil, Required: false, Serve: serve,
	}

	if secret {
		call.SecretAnswer = viewIDOf
	}

	if err := h.views.idem.Handle(w, r, call); err != nil {
		h.writeError(ctx, w, err)
	}
}

// viewIDOf returns the ID of the view that a create made.
func viewIDOf(result any) string {
	if v, ok := result.(savedview.View); ok {
		return string(v.ID)
	}

	return ""
}

// readViewBody decodes a create body. It refuses a body over savedview.MaxBodyBytes,
// an unknown field, text after the object, and a body without a name.
func readViewBody(w http.ResponseWriter, r *http.Request) (string, savedview.Query, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, savedview.MaxBodyBytes))
	if err != nil {
		return "", savedview.Query{}, errViewBody
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var in createViewBody
	if err := dec.Decode(&in); err != nil || dec.More() || in.Name == nil {
		return "", savedview.Query{}, errViewBody
	}

	var q savedview.Query

	if in.Query != nil {
		for _, l := range in.Query.Labels {
			q.Labels = append(q.Labels, savedview.Label(l))
		}

		q.References = append(q.References, in.Query.References...)
	}

	return *in.Name, q, nil
}
