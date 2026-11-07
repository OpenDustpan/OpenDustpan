// Package registry serves the tool-neutral REST API of the private registry
// under /api/dustpan/v1/namespaces/{namespace}/registry. It stages the files of
// an upload, publishes a provider release, lists releases and manages the
// trusted signing keys of a namespace. It is an HTTP adapter with its own route
// table. It never imports another API adapter: the protocol that OpenTofu reads
// lives in the TFE-compatible adapter.
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/registry/modulecheck"
	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/registry"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	// v1Prefix is the prefix of the tool-neutral REST API.
	v1Prefix = "/api/dustpan/v1"
	// channelHeader says where a request came from, for the audit entry.
	channelHeader = "Dustpan-Channel"
)

// Config holds the dependencies of a Handler. Every field except Sessions,
// Idempotency, Audit, Namespaces, OnAuditFailure and DenialLimiter is required.
type Config struct {
	Service       *service.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// MaxUploadBytes bounds the body of one staged file. It must equal the limit
	// of the service, so the adapter refuses early what the service refuses.
	MaxUploadBytes int64
	// Audit keeps the entries of the requests that the Authorizer refuses. Nil
	// writes none.
	Audit audit.Store
	// Namespaces reads the namespace of an entry that the shared denial limiter
	// flushes for a key of another adapter.
	Namespaces refusal.Namespaces
	// OnAuditFailure runs once for each refusal whose entry could not be written.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. Nil means a
	// limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	// Idempotency serves the Idempotency-Key header. Nil makes every request
	// with the header answer 400.
	Idempotency *idempotent.Middleware
	Logger      *slog.Logger
}

// Handler serves the registry API.
type Handler struct {
	service  *service.Service
	authn    identity.Authenticator
	authz    identity.Authorizer
	guard    *refusal.Recorder
	sessions *sessionauth.Checker
	idem     *idempotent.Middleware
	logger   *slog.Logger
	maxFile  int64
	mux      *route.Mux
	routes   []Route
}

// NewHandler returns a Handler.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	// Defense in depth: the recorder of refusals needs the namespaces to write the
	// entry of a namespace. Without them every refusal of a namespace is lost, so
	// the handler refuses to start. This check stays so that a new caller cannot
	// skip it.
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespaces", ErrMissingDependency)
	case cfg.Service == nil:
		return nil, fmt.Errorf("%w: registry service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.MaxUploadBytes <= 0:
		return nil, fmt.Errorf("%w: upload limit", ErrMissingDependency)
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	h := &Handler{
		service:  cfg.Service,
		authn:    cfg.Authenticator,
		authz:    cfg.Authorizer,
		guard:    recorder.WithLimiter(cfg.DenialLimiter).WithSurface("registry"),
		sessions: cfg.Sessions,
		idem:     cfg.Idempotency,
		logger:   cfg.Logger,
		maxFile:  cfg.MaxUploadBytes,
		mux:      route.NewMux(),
		routes:   nil,
	}

	h.routes = h.buildRoutes()
	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Pattern(), h.enforce(rt))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// writeError answers err through the shared error module. The classifier of
// this API answers the errors of the registry first; the shared classes
// answer the rest, and an error that none of them knows is a 500.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classifyRegistry}.Write(ctx, w, err)
}

// registryProblem maps one error of the registry to its answer.
type registryProblem struct {
	err    error
	code   string
	status int
}

// registryProblems lists the errors of the registry with their answers. The
// first match wins, so a specific error comes before a general one.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant slices.
var registryProblems = []registryProblem{
	{registry.ErrNotFound, errcode.RegistryNotFound, http.StatusNotFound},
	{registry.ErrKeyNotFound, errcode.RegistryNotFound, http.StatusNotFound},
	{service.ErrNotStaged, errcode.RegistryNotStaged, http.StatusNotFound},
	{service.ErrTooLarge, errcode.RegistryTooLarge, http.StatusRequestEntityTooLarge},
	{registry.ErrVersionExists, errcode.RegistryVersionExists, http.StatusConflict},
	{registry.ErrKeyExists, errcode.RegistryKeyExists, http.StatusConflict},
	{registry.ErrBadSignature, errcode.RegistrySignatureInvalid, http.StatusUnprocessableEntity},
	{registry.ErrUntrustedKey, errcode.RegistrySignatureUntrustedKey, http.StatusUnprocessableEntity},
	{releasecheck.ErrRefused, errcode.RegistryArchiveSetRefused, http.StatusUnprocessableEntity},
	{modulecheck.ErrRefused, errcode.RegistryModuleArchiveRefused, http.StatusUnprocessableEntity},
	{service.ErrNoKeys, errcode.RegistryNoKeys, http.StatusUnprocessableEntity},
	{registry.ErrKeyLimit, errcode.RegistryKeyLimit, http.StatusUnprocessableEntity},
	{registry.ErrBadKey, errcode.RegistryKeyInvalid, http.StatusBadRequest},
	{registry.ErrInvalid, errcode.RegistryInvalid, http.StatusBadRequest},
	{service.ErrBadRequest, errcode.RegistryInvalid, http.StatusBadRequest},
}

// classifyRegistry answers the errors of the registry. The text of a refusal
// names the field or the file that fails, and holds no byte of a key.
func classifyRegistry(err error) (problem.Problem, bool) {
	rp := registryProblemOf(err)
	if rp == nil {
		return problem.Pass()
	}

	return problem.New(rp.status, rp.code, err.Error()), true
}

// registryProblemOf returns the element of registryProblems for err, or nil
// for an error that the table does not hold.
func registryProblemOf(err error) *registryProblem {
	for i := range registryProblems {
		if errors.Is(err, registryProblems[i].err) {
			return &registryProblems[i]
		}
	}

	return nil
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}
