// Package inventory serves the tool-neutral inventory API that the web
// console reads for its cross-tool unit list. It exposes the entries of the
// inventory.Reader projection of one namespace.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone (ADR 12).
package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// errUnitOrder reports a store page that is not in the order of the unit ID.
// It is an internal error.
var errUnitOrder = errors.New("store answered a unit out of order")

// errMissingOrganization reports a list request without the organization
// query value. classify maps it to the 400 response the console expects.
var errMissingOrganization = errors.New("missing organization query parameter")

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
	// Placements serves the placement rule routes. It is optional: nil serves
	// no such route, so a store that holds no placement rules keeps the rest of
	// the API unchanged.
	Placements *collection.PlacementService
	// Locks serves the manual lock routes. It is optional: a nil service serves
	// no lock route, so a namespace store that holds no manual locks keeps the
	// rest of the API unchanged.
	Locks *manuallock.Service
	// OutputSharing serves the routes of the output sharing setting of a
	// namespace. It is optional: nil serves no such route.
	OutputSharing *outputsharesvc.Service
	// Outputs serves the route that reads the root outputs of a unit. It is
	// optional: nil serves no such route.
	Outputs *outputsharesvc.Reader
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
	// the access limits and the gates of state.write. It is required. A
	// server whose store keeps no gates gives the guard of stategate.Off
	// with its reason.
	StateGuard stategate.Guard
}

// Handler serves the inventory API under /api/inventory/.
type Handler struct {
	namespaces    *namespace.Service
	units         coreinventory.Reader
	deleted       coreinventory.DeletedReader
	history       coreinventory.HistoryReader
	lifecycles    map[string]coreinventory.Lifecycle
	readScope     DeletedReadScope
	collections   *collection.Service
	placements    *collection.PlacementService
	locks         *manuallock.Service
	outputSharing *outputsharesvc.Service
	outputs       *outputsharesvc.Reader
	moves         MoveChecker
	views         *views
	authn         identity.Authenticator
	sessions      *sessionauth.Checker
	authz         identity.Authorizer
	guard         *refusal.Recorder
	publicURL     *url.URL
	logger        *slog.Logger
	now           func() time.Time
	admit         *stategate.Admitter
	mux           *route.Mux
	routes        []Route
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

	admit, err := stategate.New(cfg.StateGuard, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("%w: state guard: %w", ErrMissingDependency, err)
	}

	h := &Handler{
		views:         viewState,
		namespaces:    cfg.Namespaces,
		units:         cfg.Units,
		deleted:       cfg.Deleted,
		history:       cfg.History,
		lifecycles:    cfg.Lifecycles,
		readScope:     cfg.ReadScope,
		collections:   cfg.Collections,
		placements:    cfg.Placements,
		locks:         cfg.Locks,
		outputSharing: cfg.OutputSharing,
		outputs:       cfg.Outputs,
		moves:         cfg.Moves,
		authn:         cfg.Authenticator,
		sessions:      cfg.Sessions,
		authz:         cfg.Authorizer,
		guard:         recorder.WithLimiter(cfg.DenialLimiter).WithSurface("inventory"),
		publicURL:     publicURL,
		logger:        cfg.Logger,
		now:           clockOf(cfg.Now),
		admit:         admit,
		mux:           route.NewMux(),
		routes:        nil,
	}

	h.routes = h.buildRoutes()
	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Method+" "+rt.Path, h.enforce(rt))
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

// lastReleaseEntry is the last released lock of a unit: the lock ID, the
// subject of the principal that held the lock, the subject of the principal
// that released it, the time of the release as an RFC 3339 time in UTC, and
// the kind of release ("normal" or "forced"). A repeated unlock of the same
// lock succeeds for 24 hours after the time of the release, because of this
// record. It never holds a token or a secret.
type lastReleaseEntry struct {
	LockID     string `json:"id"`
	Holder     string `json:"holder"`
	ReleasedBy string `json:"releasedBy"`
	ReleasedAt string `json:"releasedAt"`
	Kind       string `json:"kind"`
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
	// LastRelease is the last released lock of the unit, omitted when the
	// unit has no record. Only an OpenTofu workspace has one.
	LastRelease *lastReleaseEntry `json:"lastRelease,omitempty"`
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
		LastRelease:   toLastReleaseEntry(e.LastRelease),
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

	//nolint:forbidigo // This builds a link from the configured public URL; no request is read.
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

// toLastReleaseEntry maps one projection record to its wire shape, or nil
// when the unit has none, so the field stays omitted.
func toLastReleaseEntry(r *coreinventory.LastRelease) *lastReleaseEntry {
	if r == nil {
		return nil
	}

	return &lastReleaseEntry{
		LockID:     r.LockID,
		Holder:     r.Holder,
		ReleasedBy: r.By,
		ReleasedAt: formatTime(r.At),
		Kind:       r.Kind,
	}
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

		if queryOf(r).Get("pageToken") != "" {
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

	units, next, err := h.readableUnits(ctx, p, ns, filter, page)
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

// storePageGrowth is the factor by which each store page of the unit list
// is larger than the one before.
const storePageGrowth = 2

// readableUnits returns one page of the wire entries of the units of ns that
// pass the filter and that the caller may read (unit:read on the reference),
// in the order of the unit ID. A denied unit is not in the result. The second
// result is the ID of the last unit of the page when a later page exists,
// else empty.
//
// The store reads the units in pages of the unit ID with the filter in its
// query. A caller who may read every unit fills the page from the first
// store page. For a caller who may read fewer units, the loop reads more
// store pages, each one larger than the one before, until the page is full
// or the namespace has no more units.
func (h *Handler) readableUnits(
	ctx context.Context, p identity.Principal, ns namespace.Name, filter unitFilter, page unitPage,
) ([]unitEntry, string, error) {
	fill := unitFill{units: make([]unitEntry, 0, page.size+1), after: page.after, size: page.size}

	q := filter.query(ns)
	q.Limit = page.size + 1

	for {
		q.After = fill.after

		batch, err := h.units.QueryUnits(ctx, q)
		if err != nil {
			return nil, "", fmt.Errorf("query units: %w", err)
		}

		full, err := h.addReadable(ctx, p, filter, &fill, batch)

		switch {
		case err != nil:
			return nil, "", err
		case full:
			return fill.units, fill.units[len(fill.units)-1].UnitID, nil
		case len(batch) < q.PageLimit():
			return fill.units, "", nil
		}

		q.Limit = min(storePageGrowth*q.Limit, coreinventory.MaxUnitQueryLimit)
	}
}

// unitFill is one page of the unit list while the store pages fill it: the
// wire entries so far, the ID of the last unit that the loop read, and the
// page size.
type unitFill struct {
	units []unitEntry
	after string
	size  int
}

// addReadable appends to the page the wire entries of the units of batch
// that readableUnit keeps, up to the page size, and moves the cursor of the
// page to each unit that it reads. It returns true when the page is full and
// batch holds one more readable unit, so a later page exists.
func (h *Handler) addReadable(
	ctx context.Context, p identity.Principal, filter unitFilter, fill *unitFill, batch []coreinventory.Entry,
) (bool, error) {
	for i := range batch {
		e := &batch[i]

		// Defense in depth: the store keeps only the units after the cursor.
		// This check stays so that a store that answers a unit again cannot
		// make the loop read the same page forever.
		if e.UnitID <= fill.after {
			return false, fmt.Errorf("%w: %q is not after %q", errUnitOrder, e.UnitID, fill.after)
		}

		fill.after = e.UnitID

		keep, err := h.readableUnit(ctx, p, *e, filter)
		if err != nil {
			return false, err
		}

		if !keep {
			continue
		}

		if len(fill.units) == fill.size {
			return true, nil
		}

		fill.units = append(fill.units, h.toUnitEntry(*e))
	}

	return false, nil
}

// readableUnit reports whether the unit e passes the filter and the caller
// may read it (unit:read on the reference). A denial is not an error.
func (h *Handler) readableUnit(
	ctx context.Context, p identity.Principal, e coreinventory.Entry, filter unitFilter,
) (bool, error) {
	uobj, err := unitObject(e)
	if err != nil {
		return false, err
	}

	// Defense in depth: the store applies the filter in its query. This
	// check stays so that a store that answers more units cannot add a unit
	// that the filter does not select.
	if !filter.matches(e, uobj) {
		return false, nil
	}

	if err := h.authz.Authorize(ctx, p, identity.ActionUnitRead, uobj); err != nil {
		if errors.Is(err, identity.ErrDenied) {
			return false, nil
		}

		return false, fmt.Errorf("authorize %s: %w", identity.ActionUnitRead, err)
	}

	return true, nil
}

// unitObject maps one inventory entry to its tool-neutral unit reference. It
// uses the stored lowercase tool code, so authorization matches the
// references the tool adapters declare.
func unitObject(e coreinventory.Entry) (identity.Object, error) {
	return identity.UnitObject(e.Tool, e.Namespace, e.Name)
}

// writeError answers err through the shared error module of the
// tool-neutral APIs, with the classes of the inventory routes.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classify}.Write(ctx, w, err)
}

// classify maps the errors of the inventory routes to an answer that is safe
// to show callers. The shared module answers the rest.
func classify(err error) (problem.Problem, bool) {
	for _, try := range []func(error) (problem.Problem, bool){
		classifyView, classifyRequest, classifyCollection, classifyPlacement, classifyChange, classifyLock,
	} {
		if p, ok := try(err); ok {
			return p, true
		}
	}

	return problem.Pass()
}

// classifyRequest maps the errors of a request that names a value that is not
// valid, or a namespace or a unit that does not exist.
func classifyRequest(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errMissingOrganization), errors.Is(err, errMissingUnit):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidQuery, err.Error()), true
	case errors.Is(err, errOutputSharingBody), errors.Is(err, errCollectionBody), errors.Is(err, errMoveBody),
		errors.Is(err, errPlacementBody):
		return problem.New(http.StatusBadRequest, errcode.RequestBodyInvalid, err.Error()), true
	case errors.Is(err, errPlacementRuleID):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidPath, err.Error()), true
	case errors.Is(err, namespace.ErrInvalidName):
		return problem.New(http.StatusBadRequest, errcode.NamespaceInvalidName, err.Error()), true
	case errors.Is(err, identity.ErrInvalidObject):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidObject, err.Error()), true
	case errors.Is(err, namespace.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.NamespaceNotFound, err.Error()), true
	case errors.Is(err, coreinventory.ErrUnitNotFound):
		return problem.New(http.StatusNotFound, errcode.UnitNotFound, "unit not found"), true
	case errors.Is(err, collection.ErrUnitNotFound):
		return problem.New(http.StatusNotFound, errcode.UnitNotFound, err.Error()), true
	case errors.Is(err, access.ErrUnitMissing):
		return problem.New(http.StatusNotFound, errcode.UnitNotFound, "unit not found"), true
	}

	var none problem.Problem

	return none, false
}

// classifyCollection maps the errors of the collections of units.
func classifyCollection(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, collection.ErrInvalidName):
		return problem.New(http.StatusBadRequest, errcode.CollectionInvalidName, err.Error()), true
	case errors.Is(err, collection.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.CollectionNotFound, err.Error()), true
	case errors.Is(err, access.ErrCollectionMissing):
		// The move check found no such collection. The text is the fixed one of
		// the collection error, so it does not repeat the check's wrap.
		return problem.New(http.StatusNotFound, errcode.CollectionNotFound, collection.ErrNotFound.Error()), true
	case errors.Is(err, collection.ErrAlreadyExists):
		return problem.New(http.StatusConflict, errcode.CollectionAlreadyExists, err.Error()), true
	case errors.Is(err, collection.ErrNotEmpty):
		return problem.New(http.StatusConflict, errcode.CollectionNotEmpty, err.Error()), true
	case errors.Is(err, collection.ErrCollectionChanged):
		return problem.New(http.StatusConflict, errcode.CollectionChanged, err.Error()), true
	case errors.Is(err, collection.ErrNoCollection):
		return problem.New(http.StatusConflict, errcode.UnitNoCollection, err.Error()), true
	case errors.Is(err, collection.ErrMoveOutOfLockedCollection):
		return problem.New(http.StatusConflict, errcode.CollectionMoveOutLocked, err.Error()), true
	}

	var none problem.Problem

	return none, false
}

// classifyPlacement maps the errors of the placement rules.
func classifyPlacement(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, collection.ErrInvalidPattern):
		return problem.New(http.StatusBadRequest, errcode.PlacementInvalidPattern, err.Error()), true
	case errors.Is(err, collection.ErrInvalidPlacementTool):
		return problem.New(http.StatusBadRequest, errcode.PlacementInvalidTool, err.Error()), true
	case errors.Is(err, collection.ErrInvalidPosition):
		return problem.New(http.StatusBadRequest, errcode.PlacementInvalidPosition, err.Error()), true
	case errors.Is(err, collection.ErrPlacementRuleNotFound):
		return problem.New(http.StatusNotFound, errcode.PlacementRuleNotFound, err.Error()), true
	case errors.Is(err, collection.ErrPlacementCollectionMissing):
		// The rule names a collection that does not exist. The text is the
		// fixed one of the collection error, so the answer keeps the shape of
		// a missing collection.
		return problem.New(http.StatusNotFound, errcode.CollectionNotFound, collection.ErrNotFound.Error()), true
	case errors.Is(err, collection.ErrPlacementConflict):
		return problem.New(http.StatusConflict, errcode.PlacementConflict, err.Error()), true
	case errors.Is(err, collection.ErrTooManyPlacementRules):
		return problem.New(http.StatusConflict, errcode.PlacementTooManyRules, err.Error()), true
	}

	var none problem.Problem

	return none, false
}

// classifyChange maps the errors of the deleted-unit routes and of a fenced
// write to an HTTP status and a message safe to show callers.
func classifyChange(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errInvalidQuery):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidQuery, err.Error()), true
	case errors.Is(err, errInvalidBody):
		return problem.New(http.StatusBadRequest, errcode.RequestBodyInvalid, err.Error()), true
	case errors.Is(err, errNoHistory):
		return problem.New(http.StatusNotImplemented, errcode.UnitHistoryUnavailable, err.Error()), true
	case errors.Is(err, coreinventory.ErrInvalidName):
		return problem.New(
			http.StatusBadRequest,
			errcode.UnitInvalidName,
			"invalid unit name: use the name rules of the tool",
		), true
	case errors.Is(err, coreinventory.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.UnitDeletedNotFound, "deleted unit not found"), true
	case errors.Is(err, coreinventory.ErrNameTaken):
		return problem.New(
			http.StatusConflict,
			errcode.UnitNameTaken,
			"a live unit holds the name: send a new name in the restore body",
		), true
	}

	var none problem.Problem

	return none, false
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}
