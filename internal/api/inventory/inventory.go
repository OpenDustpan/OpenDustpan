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
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
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
	Lifecycles map[string]coreinventory.Lifecycle
	ReadScope  DeletedReadScope
	// Collections serves the collection routes (ADR 51). It is optional: a nil
	// service serves no collection route, so a namespace store that holds no
	// collections keeps the rest of the API unchanged.
	Collections *collection.Service
	// Moves checks the escalation of a move into a collection. It is required
	// with Collections: a move with no checker is refused.
	Moves         MoveChecker
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	Logger   *slog.Logger
	Now      func() time.Time
}

// Handler serves the inventory API under /api/inventory/.
type Handler struct {
	namespaces  *namespace.Service
	units       coreinventory.Reader
	deleted     coreinventory.DeletedReader
	lifecycles  map[string]coreinventory.Lifecycle
	readScope   DeletedReadScope
	collections *collection.Service
	moves       MoveChecker
	authn       identity.Authenticator
	sessions    *sessionauth.Checker
	authz       identity.Authorizer
	logger      *slog.Logger
	now         func() time.Time
	mux         *patternMux
	routes      []Route
}

// NewHandler returns a Handler. Every Config field except Now, Collections
// and Sessions is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Units == nil:
		return nil, fmt.Errorf("%w: inventory reader", ErrMissingDependency)
	case cfg.Deleted == nil:
		return nil, fmt.Errorf("%w: deleted unit reader", ErrMissingDependency)
	case len(cfg.Lifecycles) == 0:
		return nil, fmt.Errorf("%w: unit lifecycles", ErrMissingDependency)
	case cfg.ReadScope == nil:
		return nil, fmt.Errorf("%w: deleted unit read scope", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	h := &Handler{
		namespaces:  cfg.Namespaces,
		units:       cfg.Units,
		deleted:     cfg.Deleted,
		lifecycles:  cfg.Lifecycles,
		readScope:   cfg.ReadScope,
		collections: cfg.Collections,
		moves:       cfg.Moves,
		authn:       cfg.Authenticator,
		sessions:    cfg.Sessions,
		authz:       cfg.Authorizer,
		logger:      cfg.Logger,
		now:         now,
		mux:         newPatternMux(),
		routes:      nil,
	}

	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// lockEntry is the lock of a unit as the console reads it: the subject of
// the principal that holds it, the take time in Unix seconds as lastChange
// is, and the tool's own word for what holds it. It never holds a token, a
// secret or a capability path: the projection stores only the subject.
type lockEntry struct {
	Holder    string `json:"holder"`
	Since     int64  `json:"since"`
	Operation string `json:"operation"`
}

// unitEntry is one unit as the console reads it. Tool is the tool label of
// the glossary ("Pulumi" or "OpenTofu"); Reference is the unit reference
// with the stored lowercase tool code ("pulumi:acme/web/prod"). Name is the
// tool's own name for the unit. LastChange is the time of the unit's last
// change in Unix seconds, as lastUpdate is in the stack list. Status is
// omitted when unknown; ResourceCount is omitted when unknown. Lock is
// omitted when the unit is not locked: a lock is a flag, not a status, so a
// unit can hold one in any status.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it ships on its own schedule, so a released console keeps working
// only while every field stays.
type unitEntry struct {
	Tool          string     `json:"tool"`
	Reference     string     `json:"reference"`
	Namespace     string     `json:"namespace"`
	Name          string     `json:"name"`
	LastChange    int64      `json:"lastChange"`
	Status        string     `json:"status,omitempty"`
	ResourceCount *int64     `json:"resourceCount,omitempty"`
	Lock          *lockEntry `json:"lock,omitempty"`
	// Collection is the name of the collection of the unit, omitted when the
	// unit has none (ADR 51).
	Collection string `json:"collection,omitempty"`
}

// listUnitsResponse is the answered document of the unit list.
//
// Contract: never remove or rename the units field.
type listUnitsResponse struct {
	Units []unitEntry `json:"units"`
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
func toUnitEntry(e coreinventory.Entry) unitEntry {
	return unitEntry{
		Tool:          toolLabel(e.Tool),
		Reference:     e.Reference(),
		Namespace:     e.Namespace.String(),
		Name:          e.Name,
		LastChange:    e.LastChanged.Unix(),
		Status:        e.Status,
		ResourceCount: e.ResourceCount,
		Lock:          toLockEntry(e.Lock),
		Collection:    e.Collection,
	}
}

// toLockEntry maps one projection lock to its wire shape, or nil when the
// unit is not locked, so the field stays omitted.
func toLockEntry(lock *coreinventory.Lock) *lockEntry {
	if lock == nil {
		return nil
	}

	return &lockEntry{Holder: lock.Holder, Since: lock.Since.Unix(), Operation: lock.Operation}
}

// listUnits answers GET /api/inventory/units with the entries of the
// namespace that the caller may read. The route wrapper already allowed
// namespace:read on the namespace; this removes every entry whose unit the
// caller may not read (unit:read on the reference). An unknown namespace
// answers 404.
func (h *Handler) listUnits(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	ns := obj.Namespace()

	if _, err := h.namespaces.Get(ctx, ns); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entries, err := h.units.ListUnits(ctx, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	units := make([]unitEntry, 0, len(entries))

	for i := range entries {
		uobj, objErr := unitObject(entries[i])
		if objErr != nil {
			h.writeError(ctx, w, objErr)

			return
		}

		if authErr := h.authz.Authorize(ctx, p, identity.ActionUnitRead, uobj); authErr != nil {
			if errors.Is(authErr, identity.ErrDenied) {
				continue
			}

			h.writeError(ctx, w, authErr)

			return
		}
		units = append(units, toUnitEntry(entries[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, listUnitsResponse{Units: units})
}

// unitObject maps one inventory entry to its tool-neutral unit reference. It
// uses the stored lowercase tool code, so authorization matches the
// references the tool adapters declare.
func unitObject(e coreinventory.Entry) (identity.Object, error) {
	return identity.UnitObject(e.Tool, e.Namespace, e.Name)
}

// authenticate resolves the "Authorization: token <t>" or the
// "Authorization: Bearer <t>" header to a principal. A request with no
// Authorization header uses the console session cookie.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		p, _, err := h.sessions.Authenticate(r)

		return p, err
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok {
		raw, ok = strings.CutPrefix(header, bearerScheme)
	}

	if !ok || raw == "" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return h.authn.AuthenticateToken(r.Context(), raw)
}

// errorResponse is the error body the console expects, as in the API the
// console reads today.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, message := classify(err)
	if status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text
		// may hold driver detail, a token, or state (ADR 32 decision 7).
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", status))
	}

	h.writeJSON(ctx, w, status, errorResponse{Code: status, Message: message})
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

	switch {
	case errors.Is(err, errMissingOrganization):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrInvalidName), errors.Is(err, identity.ErrInvalidObject):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, namespace.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, collection.ErrInvalidName):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, collection.ErrNotFound), errors.Is(err, collection.ErrUnitNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, collection.ErrAlreadyExists), errors.Is(err, collection.ErrNotEmpty),
		errors.Is(err, collection.ErrCollectionChanged), errors.Is(err, collection.ErrNoCollection),
		errors.Is(err, namespace.ErrFenced):
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
	case errors.Is(err, coreinventory.ErrInvalidName):
		return http.StatusBadRequest, "invalid unit name: use the name rules of the tool"
	case errors.Is(err, coreinventory.ErrNotFound):
		return http.StatusNotFound, "deleted unit not found"
	case errors.Is(err, coreinventory.ErrNameTaken):
		return http.StatusConflict, "a live unit holds the name: send a new name in the restore body"
	case errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, "write fenced: try again"
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, "namespace is not homed in this region"
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
