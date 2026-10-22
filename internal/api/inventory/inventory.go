// Package inventory serves the tool-neutral inventory API that the web
// console reads for its cross-tool unit list. It exposes the entries of the
// inventory.Reader projection of one namespace.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone (ADR 12).
package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// errMissingOrganization reports a list request without the organization
// query value. classify maps it to the 400 response the console expects.
var errMissingOrganization = errors.New("missing organization query parameter")

const tokenScheme = "token "

// bearerScheme is the scheme of the TFE-compatible API and the installation API.
// The inventory API accepts it too, so one client can read every API.
const bearerScheme = "Bearer "

// Config holds the dependencies of a Handler. Deleted reads the deleted
// units, and Lifecycles maps each stored tool code to the restore and purge
// of that tool (ADR 47). ReadScope answers which deleted units a caller may
// read, so the store pages only those. Now is the clock of the purgeWithinDays filter; nil
// means time.Now.
type Config struct {
	Namespaces *namespace.Service
	Units      coreinventory.Reader
	Deleted    coreinventory.DeletedReader
	// History reads the inventory change log for the view at a time. It is
	// optional: with nil, a request with the at value answers 501.
	History    coreinventory.HistoryReader
	Lifecycles map[string]coreinventory.Lifecycle
	ReadScope  DeletedReadScope
	// Collections serves the collection routes (ADR 51). It is optional: a nil
	// service serves no collection route, so a namespace store that holds no
	// collections keeps the rest of the API unchanged.
	Collections *collection.Service
	// Locks serves the manual lock routes. It is optional: a nil service serves
	// no lock route, so a namespace store that holds no manual locks keeps the
	// rest of the API unchanged.
	Locks *manuallock.Service
	// Moves checks the escalation of a move into a collection. It is required
	// with Collections: a move with no checker is refused.
	Moves         MoveChecker
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses. Nil
	// writes none.
	Audit audit.Store
	// OnAuditFailure, when set, runs once for each refusal whose entry could not
	// be written, so the server can count it.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	// Views serves the saved view routes. It is optional: nil serves no
	// saved view route.
	Views *ViewsConfig
	// PublicURL is the external base URL of the server, for example
	// https://dustpan.example.com. It makes the consoleUrl of each unit.
	// Empty means the installation has no public URL, and the entries have
	// no consoleUrl.
	PublicURL string
	Logger    *slog.Logger
	Now       func() time.Time
	// StateGuard checks the restore and the final delete of a unit against
	// the access limits and the gates of state.write. It is optional: nil
	// means the server keeps no gates, and no call is checked.
	StateGuard StateGuard
}

// Handler serves the inventory API under /api/inventory/.
type Handler struct {
	namespaces  *namespace.Service
	units       coreinventory.Reader
	deleted     coreinventory.DeletedReader
	history     coreinventory.HistoryReader
	lifecycles  map[string]coreinventory.Lifecycle
	readScope   DeletedReadScope
	collections *collection.Service
	locks       *manuallock.Service
	moves       MoveChecker
	views       *views
	authn       identity.Authenticator
	sessions    *sessionauth.Checker
	authz       identity.Authorizer
	guard       *refusal.Recorder
	publicURL   *url.URL
	logger      *slog.Logger
	now         func() time.Time
	stateGuard  StateGuard
	mux         *patternMux
	routes      []Route
}

// validate returns ErrMissingDependency when cfg lacks a required dependency.
func (cfg Config) validate() error {
	switch {
	case cfg.Audit == nil:
		return fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Units == nil:
		return fmt.Errorf("%w: inventory reader", ErrMissingDependency)
	case cfg.Deleted == nil:
		return fmt.Errorf("%w: deleted unit reader", ErrMissingDependency)
	case len(cfg.Lifecycles) == 0:
		return fmt.Errorf("%w: unit lifecycles", ErrMissingDependency)
	case cfg.ReadScope == nil:
		return fmt.Errorf("%w: deleted unit read scope", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	return nil
}

// NewHandler returns a Handler. Every Config field except Now, Collections
// and Sessions is required.
func NewHandler(cfg Config) (*Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	publicURL, err := parsePublicURL(cfg.PublicURL)
	if err != nil {
		return nil, err
	}

	viewState, err := newViews(cfg.Views)
	if err != nil {
		return nil, err
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	h := &Handler{
		views:       viewState,
		namespaces:  cfg.Namespaces,
		units:       cfg.Units,
		deleted:     cfg.Deleted,
		history:     cfg.History,
		lifecycles:  cfg.Lifecycles,
		readScope:   cfg.ReadScope,
		collections: cfg.Collections,
		locks:       cfg.Locks,
		moves:       cfg.Moves,
		authn:       cfg.Authenticator,
		sessions:    cfg.Sessions,
		authz:       cfg.Authorizer,
		guard:       recorder.WithLimiter(cfg.DenialLimiter).WithSurface("inventory"),
		publicURL:   publicURL,
		logger:      cfg.Logger,
		now:         clockOf(cfg.Now),
		stateGuard:  cfg.StateGuard,
		mux:         newPatternMux(),
		routes:      nil,
	}

	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// clockOf returns now, or time.Now when now is nil.
func clockOf(now func() time.Time) func() time.Time {
	if now == nil {
		return time.Now
	}

	return now
}

// parsePublicURL parses the public URL of the server. An empty value is no
// public URL and gives nil.
func parsePublicURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil //nolint:nilnil // No public URL is a valid setup: the entries have no consoleUrl.
	}

	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("%w: public URL", ErrMissingDependency)
	}

	return u, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// lockEntry is the lock of a unit as the console reads it: the subject of
// the principal that holds it, the take time as an RFC 3339 time in UTC,
// and the tool's own word for what holds it. It never holds a token, a
// secret or a capability path: the projection stores only the subject.
type lockEntry struct {
	Holder    string `json:"holder"`
	Since     string `json:"since"`
	Operation string `json:"operation"`
}

// stateEntry is one state of a unit: its wire value and the time it began, an
// RFC 3339 time in UTC.
type stateEntry struct {
	State string `json:"state"`
	Since string `json:"since"`
}

// unitEntry is one unit as the console reads it. Tool is the tool label of
// the glossary ("Pulumi" or "OpenTofu"); Reference is the unit reference
// with the stored lowercase tool code ("pulumi:acme/web/prod"). UnitID is the
// stable ID of the unit, which a rename keeps. Name is the
// tool's own name for the unit. LastChange is the time of the unit's last
// change, an RFC 3339 time in UTC. Status is
// omitted when unknown; ResourceCount is omitted when unknown. Lock is
// omitted when the unit is not locked: a lock is a flag, not a status, so a
// unit can hold one in any status.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it ships on its own schedule, so a released console keeps working
// only while every field stays.
type unitEntry struct {
	Tool       string `json:"tool"`
	Reference  string `json:"reference"`
	UnitID     string `json:"unitID"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	LastChange string `json:"lastChange"`
	Status     string `json:"status,omitempty"`
	// States are the states of the unit in the order of the badges. The array
	// is empty when nothing needs attention.
	States        []stateEntry `json:"states"`
	ResourceCount *int64       `json:"resourceCount,omitempty"`
	Lock          *lockEntry   `json:"lock,omitempty"`
	// ManualLock is the manual lock that covers the unit, omitted when none
	// does. Like Lock, it is a flag and not a status.
	ManualLock *manualLockEntry `json:"manualLock,omitempty"`
	// Collection is the name of the collection of the unit, omitted when the
	// unit has none (ADR 51).
	Collection string `json:"collection,omitempty"`
	// Labels are the labels of the unit, omitted when the unit has none.
	Labels map[string]string `json:"labels,omitempty"`
	// ConsoleURL is the URL of the page of the unit in the console, omitted
	// when the installation has no public URL or the unit is deleted.
	ConsoleURL string `json:"consoleUrl,omitempty"` //nolint:tagliatelle // The tool-neutral API names this field consoleUrl.
}

// listUnitsResponse is one page of the unit list. NextPageToken is omitted on
// the last page.
//
// Contract: never remove or rename the units field.
type listUnitsResponse struct {
	Units         []unitEntry `json:"units"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

// toolLabel maps a stored lowercase tool code to the tool label of the
// glossary that the console shows. The stores keep the lowercase code
// ("pulumi", "opentofu"); the API answers the label ("Pulumi", "OpenTofu").
// A code without a label answers unchanged, so a new tool still lists.
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

// toUnitEntry maps one projection entry to its wire shape.
func (h *Handler) toUnitEntry(e coreinventory.Entry) unitEntry {
	return unitEntry{
		Tool:          toolLabel(e.Tool),
		Reference:     e.Reference(),
		UnitID:        e.UnitID,
		Namespace:     e.Namespace.String(),
		Name:          e.Name,
		LastChange:    formatTime(e.LastChanged),
		Status:        e.Status,
		States:        toStateEntries(e),
		ResourceCount: e.ResourceCount,
		Lock:          toLockEntry(e.Lock),
		ManualLock:    toManualLockEntry(e.ManualLock),
		Collection:    e.Collection,
		Labels:        e.Labels,
		ConsoleURL:    h.consoleURL(e.UnitID),
	}
}

// toStateEntries maps the states of the entry e to their wire shape. The
// array is never nil, so the field reads as a list in every response.
func toStateEntries(e coreinventory.Entry) []stateEntry {
	states := e.StatesOf()
	out := make([]stateEntry, len(states))

	for i, s := range states {
		out[i] = stateEntry{State: string(s.State), Since: formatTime(s.Since)}
	}

	return out
}

// consoleURL returns the URL of the page of the unit in the console, or an
// empty string when the installation has no public URL. The path holds the
// unit ID, so a rename of the unit keeps the link.
func (h *Handler) consoleURL(unitID string) string {
	if h.publicURL == nil {
		return ""
	}

	return h.publicURL.JoinPath("units", url.PathEscape(unitID)).String()
}

// toLockEntry maps one projection lock to its wire shape, or nil when the
// unit is not locked, so the field stays omitted.
func toLockEntry(lock *coreinventory.Lock) *lockEntry {
	if lock == nil {
		return nil
	}

	return &lockEntry{Holder: lock.Holder, Since: formatTime(lock.Since), Operation: lock.Operation}
}

// listUnits answers GET /api/dustpan/v1/namespaces/{namespace}/units with one
// page of the entries of the namespace that the caller may read. The route wrapper already allowed
// namespace:read on the namespace; this removes every entry whose unit the
// caller may not read (unit:read on the reference). An unknown namespace
// answers 404.
func (h *Handler) listUnits(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	ns := obj.Namespace()

	at, hasAt, atErr := atQuery(r)
	if atErr != nil {
		h.writeError(ctx, w, atErr)

		return
	}

	filter, err := parseUnitFilter(r, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if hasAt {
		if filter.active() {
			h.writeError(ctx, w, errFilterAt)

			return
		}

		if r.URL.Query().Get("pageToken") != "" {
			h.writeError(ctx, w, errPageTokenAt)

			return
		}

		h.listUnitsAt(w, r, p, obj, at)

		return
	}

	h.serveUnitList(w, r, p, ns, filter)
}

// serveUnitList answers one page of the units of ns that pass the filter and
// that the caller may read. The unit list and the units of a saved view both
// end here, so a view returns the same page as the query that it stores.
func (h *Handler) serveUnitList(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name, filter unitFilter,
) {
	ctx := r.Context()

	page, err := parseUnitPage(r, ns, filter)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, nsErr := h.namespaces.Get(ctx, ns); nsErr != nil {
		h.writeError(ctx, w, nsErr)

		return
	}

	entries, err := h.units.ListUnits(ctx, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	units, next, err := h.readableUnits(ctx, p, entries, filter, page)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	resp := listUnitsResponse{Units: units, NextPageToken: ""}
	if next != "" {
		resp.NextPageToken = encodePageToken(next, queryFingerprint(ns, filter))
	}

	h.writeJSON(ctx, w, http.StatusOK, resp)
}

// readableUnits returns one page of the wire entries of the units in entries
// that pass the filter and that the caller may read (unit:read on the
// reference), in the order of the unit ID. A denied unit is not in the result.
// The second result is the ID of the last unit of the page when a later page
// exists, else empty.
func (h *Handler) readableUnits(
	ctx context.Context, p identity.Principal, entries []coreinventory.Entry, filter unitFilter, page unitPage,
) ([]unitEntry, string, error) {
	units := make([]unitEntry, 0, min(len(entries), page.size+1))

	sorted := sortedByUnitID(entries, func(e coreinventory.Entry) string { return e.UnitID })

	for i := range sorted {
		e := &sorted[i]
		if page.after != "" && e.UnitID <= page.after {
			continue
		}

		uobj, err := unitObject(*e)
		if err != nil {
			return nil, "", err
		}

		if !filter.matches(*e, uobj) {
			continue
		}

		if err := h.authz.Authorize(ctx, p, identity.ActionUnitRead, uobj); err != nil {
			if errors.Is(err, identity.ErrDenied) {
				continue
			}

			return nil, "", fmt.Errorf("authorize %s: %w", identity.ActionUnitRead, err)
		}

		if len(units) == page.size {
			return units, units[len(units)-1].UnitID, nil
		}

		units = append(units, h.toUnitEntry(*e))
	}

	return units, "", nil
}

// unitObject maps one inventory entry to its tool-neutral unit reference. It
// uses the stored lowercase tool code, so authorization matches the
// references the tool adapters declare.
func unitObject(e coreinventory.Entry) (identity.Object, error) {
	return identity.UnitObject(e.Tool, e.Namespace, e.Name)
}

// authenticate resolves the "Authorization: token <t>" or the
// "Authorization: Bearer <t>" header to a principal. A request with no
// Authorization header uses the console session cookie, and the bool
// reports that the cookie signed it in. A request with an
// Authorization header never uses the cookie.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, bool, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		p, _, err := h.sessions.Authenticate(r)

		return p, err == nil, err
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok {
		raw, ok = strings.CutPrefix(header, bearerScheme)
	}

	if !ok || raw == "" {
		return identity.Principal{}, false, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, false, err
}

// errorResponse is the error body the console expects, as in the API the
// console reads today.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
	// ErrorCode is a stable machine code. Only the Idempotency-Key refusals
	// set it.
	ErrorCode string `json:"errorCode,omitempty"`
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	if ie, ok := errors.AsType[*idempotent.Error](err); ok {
		h.writeJSON(ctx, w, ie.Status, errorResponse{Code: ie.Status, Message: ie.Message, ErrorCode: ie.Code})

		return
	}

	status, message := classify(err)
	if status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text
		// may hold driver detail, a token, or state (ADR 32 decision 7).
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", status))
	}

	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	code := ""
	if status == http.StatusForbidden {
		code = errcode.Forbidden(err)
	}

	h.writeJSON(ctx, w, status, errorResponse{Code: status, Message: message, ErrorCode: code})
}

// classifyAuth maps the errors of the authentication and the
// authorization of a request.
func classifyAuth(err error) (int, string, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later", true
	case errors.Is(err, identity.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated", true
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return http.StatusForbidden, err.Error(), true
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden", true
	default:
		return 0, "", false
	}
}

// classify maps domain errors to an HTTP status and a message safe to show callers.
func classify(err error) (int, string) {
	if status, message, ok := classifyAuth(err); ok {
		return status, message
	}

	if status, message, ok := classifyView(err); ok {
		return status, message
	}

	switch {
	case errors.Is(err, errMissingOrganization):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrInvalidName), errors.Is(err, identity.ErrInvalidObject):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, collection.ErrInvalidName):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, coreinventory.ErrUnitNotFound):
		return http.StatusNotFound, "unit not found"
	case errors.Is(err, collection.ErrNotFound), errors.Is(err, collection.ErrUnitNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, collection.ErrAlreadyExists), errors.Is(err, collection.ErrNotEmpty),
		errors.Is(err, collection.ErrCollectionChanged), errors.Is(err, collection.ErrNoCollection),
		errors.Is(err, collection.ErrMoveOutOfLockedCollection):
		return http.StatusConflict, err.Error()
	case errors.Is(err, errCollectionBody), errors.Is(err, errMoveBody), errors.Is(err, errMissingUnit):
		return http.StatusBadRequest, err.Error()
	default:
		return classifyChange(err)
	}
}

// classifyChange maps the errors of the deleted-unit routes and of a fenced
// write to an HTTP status and a message safe to show callers.
func classifyChange(err error) (int, string) {
	switch {
	case errors.Is(err, errInvalidQuery), errors.Is(err, errInvalidBody):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, errNoHistory):
		return http.StatusNotImplemented, err.Error()
	case errors.Is(err, coreinventory.ErrInvalidName):
		return http.StatusBadRequest, "invalid unit name: use the name rules of the tool"
	case errors.Is(err, coreinventory.ErrNotFound):
		return http.StatusNotFound, "deleted unit not found"
	case errors.Is(err, coreinventory.ErrNameTaken):
		return http.StatusConflict, "a live unit holds the name: send a new name in the restore body"
	case errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, namespace.ErrFenced.Error()
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, "namespace is not homed in this region"
	case errors.Is(err, maintenance.ErrInMaintenance):
		return http.StatusServiceUnavailable, maintenance.ErrInMaintenance.Error() + ": try again later"
	default:
		return classifyManualLock(err)
	}
}

// classifyManualLock maps the errors of the manual lock routes. Any other error
// is an internal error.
func classifyManualLock(err error) (int, string) {
	if status, message, ok := classifyLock(err); ok {
		return status, message
	}

	return http.StatusInternalServerError, "internal error"
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
