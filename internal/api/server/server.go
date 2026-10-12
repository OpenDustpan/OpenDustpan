// Package server serves the tool-neutral installation API under
// /api/server/. Backups (ADR 30) and metrics (ADR 32) share this adapter.
// The metrics adapter label is server, the route prefix.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone (ADR 12).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// bearerScheme is the Authorization scheme the adapter reads.
const bearerScheme = "Bearer "

// Config holds the dependencies of a Handler. The backup service is optional:
// nil means no backup target is configured.
type Config struct {
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
	// Namespaces creates namespaces homed in the region of the server.
	Namespaces *namespace.Service
	// Backups runs installation backups against the configured target. Nil
	// means no backup target is configured. The target comes only from this
	// service: no request field, header or query value sets it.
	Backups *backup.Service
	// Maintenance enters, leaves and reads maintenance mode (ADR 38).
	// Required: the installation state lives in the database.
	Maintenance Maintenance
	// Metrics writes the registry for the metrics route.
	Metrics MetricsWriter
	// Version is the server version the upgrade check answers. Empty means
	// dev, as the backup manifest records it.
	Version string
	// Schema reports the applied migrations the upgrade check answers. Nil
	// means the upgrade check answers 500: every production server wires its
	// store.
	Schema SchemaReporter
	// KeyEncryptionKey reports whether the server runs with a key-encryption
	// key. The upgrade check answers it, so that the command can warn about
	// a server that still runs without one (ADR 41 decision 12).
	KeyEncryptionKey bool
}

// SchemaReporter reports the applied migrations of the database. Both store
// adapters implement it. It is defined here, in the package that consumes
// it, so the domain stays free of transport code.
type SchemaReporter interface {
	// AppliedMigrations returns the rows of schema_migrations ordered by
	// version, or nothing when the database records none.
	AppliedMigrations(ctx context.Context) ([]schema.AppliedMigration, error)
}

// Handler serves the installation API under /api/server/.
type Handler struct {
	authn       identity.Authenticator
	authz       identity.Authorizer
	logger      *slog.Logger
	namespaces  *namespace.Service
	backups     *backup.Service
	maintenance Maintenance
	metrics     MetricsWriter
	version     string
	schema      SchemaReporter
	keyed       bool
	mux         *patternMux
	routes      []Route
}

// NewHandler returns a Handler. Authenticator, Authorizer, Logger,
// Namespaces, Maintenance and Metrics are required. Backups is optional: nil
// means no backup target is configured.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Maintenance == nil:
		return nil, fmt.Errorf("%w: maintenance", ErrMissingDependency)
	case cfg.Metrics == nil:
		return nil, fmt.Errorf("%w: metrics", ErrMissingDependency)
	}

	h := &Handler{
		authn:       cfg.Authenticator,
		authz:       cfg.Authorizer,
		logger:      cfg.Logger,
		namespaces:  cfg.Namespaces,
		backups:     cfg.Backups,
		maintenance: cfg.Maintenance,
		metrics:     cfg.Metrics,
		version:     cfg.Version,
		schema:      cfg.Schema,
		keyed:       cfg.KeyEncryptionKey,
		mux:         newPatternMux(),
		routes:      nil,
	}

	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// Backups returns the backup service the handler was wired with, or nil when
// no backup target is configured.
func (h *Handler) Backups() *backup.Service {
	return h.backups
}

// ServeHTTP implements http.Handler. A path the route table does not hold
// answers 404 as a JSON error, so callers can tell it reached this adapter
// and not the Pulumi adapter behind /api/.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	catch := &notFoundWriter{ResponseWriter: w, notFound: false}

	h.mux.ServeHTTP(catch, r)

	if !catch.notFound {
		return
	}

	header := w.Header()
	header.Del("Content-Length")
	header.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)

	doc := errorResponse{Code: http.StatusNotFound, Message: "not found"}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		h.logger.WarnContext(r.Context(), "write response failed", slog.String("error", err.Error()))
	}
}

// notFoundWriter swallows the default 404 of the mux, so ServeHTTP can
// replace it with a JSON error. A 404 that already carries the JSON media
// type comes from a route handler and passes through untouched.
type notFoundWriter struct {
	http.ResponseWriter

	notFound bool
}

// WriteHeader swallows a 404 without the JSON media type and records it.
// Every other status passes through.
func (c *notFoundWriter) WriteHeader(status int) {
	if status == http.StatusNotFound && c.Header().Get("Content-Type") != "application/json" {
		c.notFound = true

		return
	}

	c.ResponseWriter.WriteHeader(status)
}

// Write swallows the body of a swallowed 404. Every other body passes
// through.
func (c *notFoundWriter) Write(p []byte) (int, error) {
	if c.notFound {
		return len(p), nil
	}

	return c.ResponseWriter.Write(p) //nolint:wrapcheck // Transparent wrapper: callers must see the original error.
}

// authenticate resolves the "Authorization: Bearer <token>" header to a
// principal.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerScheme)
	if !ok || raw == "" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return h.authn.AuthenticateToken(r.Context(), raw)
}

// errorResponse is the error body the installation API answers.
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

// classify maps domain errors to an HTTP status and a message safe to show
// callers. Backup errors map in classifyBackup, namespace errors in
// classifyNamespace and maintenance errors in classifyMaintenance.
func classify(err error) (int, string) {
	if status, message, ok := classifyMaintenance(err); ok {
		return status, message
	}

	if status, message, ok := classifyNamespace(err); ok {
		return status, message
	}

	if status, message, ok := classifyBackup(err); ok {
		return status, message
	}

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, identity.ErrInvalidObject):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, errInvalidBody):
		return http.StatusBadRequest, errInvalidBody.Error()
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

// classifyBackup maps the backup errors to their status and a fixed
// message: registry and target failures may hold driver text, so only the
// sentinel decides the status and the message. A missing backup answers 404,
// a second start 409, and a server with no backup target 503 that names the
// setting.
func classifyBackup(err error) (int, string, bool) {
	switch {
	case errors.Is(err, backup.ErrAlreadyRunning):
		return http.StatusConflict, "a backup is already running", true
	case errors.Is(err, backup.ErrAlreadyExists):
		return http.StatusConflict, "backup already exists", true
	case errors.Is(err, backup.ErrNotFound):
		return http.StatusNotFound, "backup not found", true
	case errors.Is(err, backup.ErrInvalidID):
		return http.StatusBadRequest, "invalid backup ID", true
	case errors.Is(err, errNoBackupTarget):
		// No target is a setup problem of the server, not a bad request.
		return http.StatusServiceUnavailable, errNoBackupTarget.Error() + ": set --backup-target", true
	default:
		return 0, "", false
	}
}

// classifyNamespace maps the namespace errors to their status and message: a
// bad namespace name or body answers 400, a duplicate namespace 409 and an
// unknown namespace 404.
func classifyNamespace(err error) (int, string, bool) {
	switch {
	case errors.Is(err, namespace.ErrInvalidName), errors.Is(err, errInvalidNamespaceBody):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, namespace.ErrAlreadyExists):
		return http.StatusConflict, err.Error(), true
	case errors.Is(err, namespace.ErrNotFound):
		return http.StatusNotFound, err.Error(), true
	default:
		return 0, "", false
	}
}

// classifyMaintenance maps the maintenance errors to their status and
// message: an invalid state value answers 400, active work answers 409
// naming each update and run, and an unreadable or unknown stored state
// answers 500 without driver text.
func classifyMaintenance(err error) (int, string, bool) {
	switch {
	case errors.Is(err, maintenance.ErrInvalidState):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, maintenance.ErrActiveWork):
		return http.StatusConflict, err.Error(), true
	case errors.Is(err, maintenance.ErrUnavailable):
		return http.StatusInternalServerError, maintenance.ErrUnavailable.Error(), true
	default:
		return 0, "", false
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
