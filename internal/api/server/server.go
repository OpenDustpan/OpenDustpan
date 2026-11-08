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
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	denials "opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/forgelinksvc"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/tempadmin"
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
	// Namespaces creates namespaces whose primary installation is the installation of the server.
	Namespaces *namespace.Service
	// Backups runs installation backups against the configured target. Nil
	// means no backup target is configured. The target comes only from this
	// service: no request field, header or query value sets it.
	Backups *backup.Service
	// Restores runs installation restores from the configured target. Nil
	// means no backup target is configured. The backup comes only from this
	// service: no request field, header or query value sets the target.
	Restores *backup.RestoreService
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
	// Journal counts the running journaled updates the upgrade check
	// answers. Nil means the upgrade check answers 0: a store that predates
	// the count still serves.
	Journal JournalReporter
	// Principals keeps the principals and the groups of the installation
	// (ADR 52). Nil means the store keeps none: the principal and group
	// routes answer 501. Both SQL adapters keep them.
	Principals *principal.Service
	// Access keeps the roles and the grants of the installation (ADR 52).
	// Nil means the store keeps none: the role and grant routes answer 501,
	// and a namespace create stores no grant. Both SQL adapters keep them.
	Access *access.Service
	// Tokens keeps the access tokens of the principals (ADR 53). Nil means
	// the store keeps none: the token routes answer 501. Both SQL adapters
	// keep them.
	Tokens *accesstoken.Service
	// Reach reads the reach of a group for the protected flag of the group
	// routes. A zero config makes each of those routes fail closed: the
	// check answers an error, never a false flag.
	Reach access.ReachConfig
	// Audit appends the entries of the scope @server: each change of a
	// principal or a group writes one (ADR 60, decision 7). With Access set,
	// it is required and must also keep the entries of each namespace
	// (audit.Store), because each access-review export writes an entry (ADR
	// 52, decision 13). Without Access, nil writes no entry.
	Audit audit.ServerStore
	// IPAllowlists keeps the IP allowlist of each namespace. Nil means no
	// allowlist store is configured: the IP allowlist routes are not mounted.
	IPAllowlists *ipallow.Service
	// Gates keeps the approval defaults and the approval rules of each
	// namespace. Nil means the store keeps none: the approval default and
	// approval rule routes are not mounted.
	Gates GateConfig
	// SessionPolicy keeps the session policy of the installation. Nil
	// means the store keeps none: the session policy routes are not
	// mounted. Both SQL adapters keep it.
	SessionPolicy *session.Policies
	// CheckpointEpochs starts a new epoch of the signed audit checkpoints.
	// Nil means signed checkpoints are off: the epoch route answers 409.
	CheckpointEpochs CheckpointEpochs
	// OnAuditFailure, when set, runs once for each lost audit entry, so the
	// wiring can count it on a metric.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter. Nil means a limiter of this handler
	// only.
	DenialLimiter *denylimit.Limiter
	// AuditChain reads the state of the audit chain and approves a waiting
	// change. Nil means the store keeps no audit chain: the audit chain
	// routes answer 501.
	AuditChain AuditChain
	// DeletedUnits runs the retention sweep of the deleted units.
	// Nil means the store keeps no deleted units: the sweep route answers
	// 501. Both SQL adapters keep them.
	DeletedUnits DeletedUnitSweeper
	// TempAdmins reads the temporary administrator records for the banner
	// flag of the principal routes. Nil means no record is read: the flag
	// stays false. Both SQL adapters keep them.
	TempAdmins tempadmin.Reader
	// Now returns the current time for the banner flag. Nil means time.Now.
	Now func() time.Time
	// AuditSinks lists the configured audit sinks with their delivery
	// state. Nil means no sink is configured: the sink list is empty.
	AuditSinks AuditSinkSource
	// Preferences keeps the preferences of a person. Nil means the store
	// keeps none: the preferences routes answer 501. Both SQL adapters keep
	// them.
	Preferences *preferences.Service
	// GitHostLinks keeps the links from Git host accounts to principals. Nil
	// means the store keeps none: the link routes answer 501. Both SQL adapters
	// keep them.
	GitHostLinks *forgelinksvc.Service
	// LocalUsers is the service of the local users and of the password
	// sign-in. Nil means the server keeps no local users: their routes are
	// not mounted.
	LocalUsers *localusersvc.Service
	// Settings keeps the settings resource of the installation and of the
	// namespaces. Nil means the store keeps none: the settings routes answer
	// 501. Both SQL adapters keep them.
	Settings *setting.Service
	// Workers reports the components of the process and the state of the
	// worker, and refuses the start of a durable operation when no worker
	// claimed or renewed a claim. Nil means no refusal and an empty report.
	Workers WorkerSource
	// SignIn is the console sign-in through OpenID Connect providers. Nil
	// means no sign-in: the sign-in routes are not mounted, and the
	// session cookie authenticates nothing. With SignIn set, Principals is
	// required.
	SignIn *SignIn
	// InstallationKind is the kind that the capabilities document answers
	// to a signed-in caller. Empty means production.
	InstallationKind installation.Kind
	// StateWriteGates is the effective value of the state.write gates, which
	// the capabilities document answers to a signed-in caller. A zero value
	// means that the wiring gives none, and the document leaves it out.
	StateWriteGates stategate.Effective
	// GitHost keeps the registration of the GitHub App of the installation, the
	// map of its installations and the connections of the namespaces. Nil
	// means the store keeps none: the Git host routes are not mounted. Both SQL
	// adapters keep them.
	GitHost *forge.Service
	// Idempotency serves the Idempotency-Key header of the Git host writes. Nil
	// makes each request with the header answer 400.
	Idempotency *idempotent.Middleware
	// Instances keeps the version rows of the server processes. Nil means the
	// store keeps none: the instance routes are not mounted. Both SQL adapters
	// keep them.
	Instances installation.InstanceStore
	// StoreChecks runs the deep checks of the blob store. Nil means the server
	// has none: the store check routes are not mounted.
	StoreChecks *storecheck.Service
}

// SchemaReporter reports the applied migrations of the database. Both store
// adapters implement it. It is defined here, in the package that consumes
// it, so the domain stays free of transport code.
type SchemaReporter interface {
	// AppliedMigrations returns the rows of schema_migrations ordered by
	// version, or nothing when the database records none.
	AppliedMigrations(ctx context.Context) ([]schema.AppliedMigration, error)
}

// JournalReporter counts the updates that run now with journal version 1
// or higher. Both store adapters implement it. It is defined here, in the
// package that consumes it, so the domain stays free of transport code.
type JournalReporter interface {
	// CountRunningJournaledUpdates returns the count of running updates
	// with journal version above 0.
	CountRunningJournaledUpdates(ctx context.Context) (int64, error)
}

// Handler serves the installation API under /api/server/.
type Handler struct {
	authn          identity.Authenticator
	authz          identity.Authorizer
	guard          *denials.Recorder
	logger         *slog.Logger
	namespaces     *namespace.Service
	backups        *backup.Service
	restores       *backup.RestoreService
	maintenance    Maintenance
	metrics        MetricsWriter
	version        string
	schema         SchemaReporter
	journal        JournalReporter
	principals     *principal.Service
	access         *access.Service
	reach          access.ReachConfig
	tokens         *accesstoken.Service
	audit          audit.ServerStore
	ipAllowlists   *ipallow.Service
	gates          GateConfig
	sessions       *session.Policies
	epochs         CheckpointEpochs
	onAuditFailure func()
	deletedUnits   DeletedUnitSweeper
	tempAdmins     tempadmin.Reader
	now            func() time.Time
	auditChain     AuditChain
	auditSinks     AuditSinkSource
	workers        WorkerSource
	signIn         *signInState
	kind           installation.Kind
	stateWrite     stategate.Effective
	preferences    *preferences.Service
	gitHostLinks   *forgelinksvc.Service
	localUsers     *localusersvc.Service
	// passkeys is the passkey service of the console sign-in. With no
	// sign-in there is no service.
	passkeys      PasskeyService
	passkeyBegins *beginLimiter
	settings      *setting.Service
	gitHost       *forge.Service
	idem          *idempotent.Middleware
	instances     installation.InstanceStore
	storeChecks   *storecheck.Service
	mux           *patternMux
	routes        []Route
}

// NewHandler returns a Handler. Authenticator, Authorizer, Logger,
// Namespaces, Maintenance and Metrics are required. Backups is optional: nil
// means no backup target is configured. A nil cfg is the zero Config, so it
// fails with the first missing required dependency.
func NewHandler(cfg *Config) (*Handler, error) {
	if cfg == nil {
		var zero Config

		cfg = &zero
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	signIn, err := newSignInState(cfg.SignIn, cfg.InstallationKind)
	if err != nil {
		return nil, err
	}

	guard, err := newGuard(cfg)
	if err != nil {
		return nil, err
	}

	h := newHandlerFields(cfg, signIn, guard)
	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// newHandlerFields returns a Handler with the dependencies of cfg. The caller
// has validated cfg.
func newHandlerFields(cfg *Config, signIn *signInState, guard *denials.Recorder) *Handler {
	h := &Handler{
		authn:          cfg.Authenticator,
		authz:          cfg.Authorizer,
		guard:          guard,
		logger:         cfg.Logger,
		namespaces:     cfg.Namespaces,
		backups:        cfg.Backups,
		restores:       cfg.Restores,
		maintenance:    cfg.Maintenance,
		metrics:        cfg.Metrics,
		version:        cfg.Version,
		schema:         cfg.Schema,
		journal:        cfg.Journal,
		principals:     cfg.Principals,
		access:         cfg.Access,
		reach:          cfg.Reach,
		tokens:         cfg.Tokens,
		deletedUnits:   cfg.DeletedUnits,
		tempAdmins:     cfg.TempAdmins,
		auditChain:     cfg.AuditChain,
		audit:          cfg.Audit,
		ipAllowlists:   cfg.IPAllowlists,
		gates:          cfg.Gates,
		sessions:       cfg.SessionPolicy,
		epochs:         cfg.CheckpointEpochs,
		onAuditFailure: cfg.OnAuditFailure,
		now:            cfg.Now,
		auditSinks:     cfg.AuditSinks,
		workers:        cfg.Workers,
		signIn:         signIn,
		kind:           cfg.InstallationKind,
		gitHostLinks:   cfg.GitHostLinks,
		stateWrite:     cfg.StateWriteGates,
		preferences:    cfg.Preferences,
		localUsers:     cfg.LocalUsers,
		passkeys:       nil, passkeyBegins: newBeginLimiter(),
		settings:  cfg.Settings,
		gitHost:   cfg.GitHost,
		idem:      cfg.Idempotency,
		instances: cfg.Instances, storeChecks: cfg.StoreChecks,
		mux:    newPatternMux(),
		routes: nil,
	}

	if h.now == nil {
		h.now = time.Now
	}

	if signIn != nil {
		h.passkeys = signIn.passkeys
	}

	return h
}

// newGuard returns the recorder of the refused requests of the installation
// API. The audit store must keep the entries of refused requests too, so it
// must also keep the entries of each namespace. A store that does not gives
// no handler: a refusal must never go without an entry.
func newGuard(cfg *Config) (*denials.Recorder, error) {
	denialStore, ok := cfg.Audit.(audit.Store)
	if !ok {
		return nil, fmt.Errorf(
			"%w: an audit store with namespace entries, for the refused requests",
			ErrMissingDependency,
		)
	}

	recorder, err := denials.New(cfg.Authorizer, denialStore, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	return recorder.WithLimiter(cfg.DenialLimiter).WithSurface("installation"), nil
}

// validate returns ErrMissingDependency when cfg lacks a required
// dependency.
func (cfg *Config) validate() error {
	switch {
	case cfg.Audit == nil:
		return fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Maintenance == nil:
		return fmt.Errorf("%w: maintenance", ErrMissingDependency)
	case cfg.Metrics == nil:
		return fmt.Errorf("%w: metrics", ErrMissingDependency)
	case cfg.Access != nil && !keepsNamespaceAudit(cfg.Audit):
		// Each access-review export writes an audit entry, in @server or in
		// the namespace of the export.
		return fmt.Errorf("%w: an audit store with namespace entries, for the access review", ErrMissingDependency)
	case cfg.SignIn != nil && cfg.Principals == nil:
		return fmt.Errorf("%w: principal service, for the sign-in", ErrMissingDependency)
	}

	return nil
}

// keepsNamespaceAudit reports whether entries keeps the entries of @server
// and of each namespace, as the export of an access review needs.
func keepsNamespaceAudit(entries audit.ServerStore) bool {
	if entries == nil {
		return false
	}

	_, ok := entries.(namespaceAppender)

	return ok
}

// Backups returns the backup service the handler was wired with, or nil when
// no backup target is configured.
func (h *Handler) Backups() *backup.Service {
	return h.backups
}

// Restores returns the restore service the handler was wired with, or nil
// when no backup target is configured.
func (h *Handler) Restores() *backup.RestoreService {
	return h.restores
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

	w.Header().Del("Content-Length")

	ctx := r.Context()
	status, message, answer := h.answer(ctx, failCoded(http.StatusNotFound, "not found", errcode.ServerRouteNotFound))
	h.writeAnswer(ctx, w, status, message, answer)
}

// notFoundWriter swallows the default 404 of the mux, so ServeHTTP can
// replace it with a JSON error. A 404 that already carries the JSON media
// type comes from a route handler and passes through untouched.
type notFoundWriter struct {
	http.ResponseWriter

	notFound bool
}

// FlushError sends the data written so far to the client, so a stream reaches it
// before the handler ends. It does not expose the other optional methods of
// the writer below, such as Hijack.
func (c *notFoundWriter) FlushError() error {
	if err := http.NewResponseController(c.ResponseWriter).Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	return nil
}

// SetWriteDeadline limits the time of the next writes of a stream.
func (c *notFoundWriter) SetWriteDeadline(t time.Time) error {
	if err := http.NewResponseController(c.ResponseWriter).SetWriteDeadline(t); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}

	return nil
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

// authenticate resolves the caller of r: the "Authorization: Bearer
// <token>" header, or, for a request with no Authorization header, the
// console session cookie. A request with both uses the header and ignores
// the cookie. The session is nil for a caller with a token.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, *sessionCaller, error) {
	header := apidesc.Header(r, "Authorization")
	if header == "" {
		return h.authenticateSession(r)
	}

	raw, ok := strings.CutPrefix(header, bearerScheme)
	if !ok || raw == "" {
		return identity.Principal{}, nil, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, nil, err
}

// errorResponse is the error body the installation API answers.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// maintenanceRetryAfter is the Retry-After wait of an
// installation API write that maintenance mode refused, and of an enter of
// maintenance mode that the writes in flight kept waiting.
const maintenanceRetryAfter = 30 * time.Second

// writeError writes the error answer of err with problem.Writer: the refusals
// of the credential first, then a denial with keepDenied, then problemFor.
// A path that has no code of the catalog keeps its own body, the same as before.
// Every error answer carries Cache-Control: no-store.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	if f, ok := uncodedFailure(err); ok {
		h.writeJSON(ctx, w, f.status, errorResponse{Code: f.status, Message: f.message})

		return
	}

	// problem.Writer logs a 500 with a cause from the closed list and the
	// request ID, and never the error text.
	problemWriter(h.logger).Write(ctx, w, err)
}

// problemWriter returns the writer of the error answers of the installation
// API.
func problemWriter(logger *slog.Logger) problem.Writer {
	return problem.Writer{Logger: logger, KeepDenied: keepDenied, Classify: problemFor}
}

// uncodedFailure returns the failure of an error that answers without a code
// of the catalog, and false for any other error. A denial, a refusal of the
// credential, an idempotency error, a coded error and a refusal that asks for
// a new sign-in all have a code and answer through problem.Writer.
func uncodedFailure(err error) (failure, bool) {
	if _, ok := problem.Auth(err); ok {
		return fail(0, ""), false
	}

	if _, ok := problem.Idempotency(err); ok {
		return fail(0, ""), false
	}

	if errors.As(err, new(*codedError)) {
		return fail(0, ""), false
	}

	if errors.As(err, new(reauthenticateError)) {
		return fail(0, ""), false
	}

	// A 403 gets the code of its cause, and the code of a cause that is not a
	// denial is empty: that answer has no code either.
	f := classify(err)
	if forbiddenCode(f, err) == "" {
		return f, true
	}

	return fail(0, ""), false
}

// problemFor is the classifier of problem.Writer: it answers the errors that
// have a code. It never sees an error that wraps identity.ErrDenied.
func problemFor(err error) (problem.Problem, bool) {
	if p, ok := problem.Idempotency(err); ok {
		return p, true
	}

	if coded, ok := errors.AsType[*codedError](err); ok {
		// The catalog checks the code and the arguments. When it refuses, the
		// bug is in the server and not in the request, so the writer answers
		// the internal error.
		return problem.New(coded.status, coded.code, coded.message, coded.args...), true
	}

	f := classify(err)
	p := problem.New(f.status, forbiddenCode(f, err), f.message, f.args...)

	if fresh, ok := errors.AsType[reauthenticateError](err); ok {
		p = problem.New(f.status, freshStepRequiredCode, f.message)
		p.Reauthenticate = fresh.url
	}

	p.RetryAfter = retryAfter(err)

	return p, true
}

// forbiddenCode returns the code of f. A 403 that its classify function gives
// no code gets the code of its cause, at least access.denied for a missing
// permission.
func forbiddenCode(f failure, err error) string {
	if f.code == "" && f.status == http.StatusForbidden {
		return errcode.Forbidden(err)
	}

	return f.code
}

// retryAfter returns the wait that the answer to err names in Retry-After: a
// refusal of maintenance mode names maintenanceRetryAfter, any other error 0.
func retryAfter(err error) time.Duration {
	if errors.Is(err, maintenance.ErrInMaintenance) || errors.Is(err, maintenance.ErrBusy) {
		return maintenanceRetryAfter
	}

	return 0
}

// deniedFailure returns the failure of an error that wraps identity.ErrDenied
// or that is a refusal of the credential of a request. It uses the order of
// problem.Writer: the refusals of the credential first, then keepDenied, then
// the standard 403 with fixed text. It holds no wrap text. The second result
// is false for any other error.
func deniedFailure(err error) (failure, bool) {
	if _, ok := problem.Auth(err); !ok {
		return fail(0, ""), false
	}

	p := problem.Writer{Logger: nil, KeepDenied: keepDenied, Classify: nil}.Problem(err)

	return failCoded(p.Status, p.Message, p.Code, p.Values...), true
}

// keepDenied maps the classes of a denial that keep their own text, as
// problem.Writer.KeepDenied does. The status is 403 only. The text is built
// from a sentinel or from a refusal of the domain, never from a wrapped
// cause.
func keepDenied(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, access.ErrEscalation):
		// checkMoveGrant in pkg/access wraps this refusal in identity.ErrDenied
		// when a grant of a move target gives more than the caller holds.
		return keptForbidden(err, access.ErrEscalation.Error()), true
	case errors.Is(err, accesstoken.ErrReachExceeded):
		// The token service wraps this refusal in identity.ErrDenied. Its text
		// names the principal and the actions of the principal that the caller
		// does not hold. The refusal never holds a cause.
		return keptForbidden(err, err.Error()), true
	case errors.Is(err, errSignInRefused):
		// The reason names no code, token or state: the sign-in handler adds it
		// on purpose, as a signInRefusalError.
		text := errSignInRefused.Error()
		if refusal, ok := errors.AsType[signInRefusalError](err); ok {
			text += ": " + refusal.reason
		}

		return keptForbidden(err, text), true
	default:
		return problem.Internal(), false
	}
}

// keptForbidden returns the 403 of a kept class with text.
func keptForbidden(err error, text string) problem.Problem {
	return problem.Built(http.StatusForbidden, text, errcode.Forbidden(err), errcode.Answer{}, nil)
}

// classifyIdentity maps the errors of the roles, the grants and the access
// tokens.
func classifyIdentity(err error) (failure, bool) {
	if f, ok := classifyAccess(err); ok {
		return f, true
	}

	return classifyToken(err)
}

// classify maps domain errors to their failure: an HTTP status, a message
// safe to show callers, and the code of the catalog with its arguments. Backup
// errors map in classifyBackup, namespace errors in classifyNamespace and
// maintenance errors in classifyMaintenance. The order of the functions is
// part of the contract: the first function that maps the error decides.
func classify(err error) failure {
	for _, try := range []func(error) (failure, bool){
		classifyStoreCheck, classifyMaintenance, classifyBodyTooLarge, classifyNamespace, classifyBackup, classifyPrincipal, classifyIdentity, classifyWorker,
		classifyIPAllowlist, classifyGateConfig, classifySessionPolicy, classifySignIn, classifyProviders,
		classifyGitHost, classifyInstance, classifyRequest,
	} {
		if f, ok := try(err); ok {
			return f
		}
	}

	return classifyRegistry(err)
}

// classifyRequest maps the caller errors to their status and message: a
// missing credential answers 401, a denied action 403, and a bad object
// reference or body 400.
func classifyRequest(err error) (failure, bool) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return failCoded(http.StatusUnauthorized, "unauthenticated", errcode.AuthUnauthenticated), true
	case errors.Is(err, identity.ErrDenied):
		return failCoded(http.StatusForbidden, "forbidden", errcode.Forbidden(err)), true
	case errors.Is(err, identity.ErrInvalidObject):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.RequestInvalidObject), true
	case errors.Is(err, errInvalidBody):
		return failCoded(http.StatusBadRequest, errInvalidBody.Error(), errcode.RequestBodyInvalid), true
	case errors.Is(err, errOverrideSwitchesBody):
		return failCoded(http.StatusBadRequest, errOverrideSwitchesBody.Error(), errcode.RequestBodyInvalid), true
	case errors.Is(err, errCheckpointsOff):
		return fail(http.StatusConflict, errCheckpointsOff.Error()), true
	default:
		return fail(0, ""), false
	}
}

// classifyRegistry maps the backup and restore registry errors to their
// status and message. Registry and target failures may hold driver text, so
// only the sentinel decides the status and the message. A missing backup
// answers 404 and a second start 409.
func classifyRegistry(err error) failure {
	switch {
	case errors.Is(err, backup.ErrAlreadyRunning):
		return fail(http.StatusConflict, "a backup is already running")
	case errors.Is(err, backup.ErrRestoreRunning):
		return fail(http.StatusConflict, "a restore is already running")
	case errors.Is(err, backup.ErrAlreadyExists):
		return fail(http.StatusConflict, "backup already exists")
	case errors.Is(err, backup.ErrRestoreExists):
		return fail(http.StatusConflict, "restore already exists")
	case errors.Is(err, backup.ErrNotFound):
		return fail(http.StatusNotFound, "backup not found")
	case errors.Is(err, backup.ErrRestoreNotFound):
		return fail(http.StatusNotFound, "restore not found")
	default:
		return failCoded(http.StatusInternalServerError, "internal error", errcode.ServerInternal)
	}
}

// classifyBackup maps the backup errors to their status and a fixed
// message: registry and target failures may hold driver text, so only the
// sentinel decides the status and the message. A missing backup answers 404,
// a second start 409, a backup that the bound on the unverified keys refused
// answers the coded 422, and a server with no backup target 503 that names the
// setting.
func classifyBackup(err error) (failure, bool) {
	if text, ok := backup.RefusalText(err); ok {
		// The refusal of a dev backup carries its own text, which names the
		// setting that allows it. It holds no driver detail.
		return fail(http.StatusConflict, text), true
	}

	switch {
	case errors.Is(err, backup.ErrTooManyUnverified):
		text, args := unverifiedRefusal(err)

		return failCoded(http.StatusUnprocessableEntity, text, errcode.BackupTooManyUnverified, args...), true
	case errors.Is(err, backup.ErrDevBackupRefused):
		return fail(http.StatusConflict, backup.ErrDevBackupRefused.Error()), true
	case errors.Is(err, backup.ErrManifestKindInvalid):
		return fail(http.StatusConflict, backup.ErrManifestKindInvalid.Error()), true
	case errors.Is(err, backup.ErrAlreadyRunning):
		return fail(http.StatusConflict, "a backup is already running"), true
	case errors.Is(err, backup.ErrAlreadyExists):
		return fail(http.StatusConflict, "backup already exists"), true
	case errors.Is(err, backup.ErrNotFound):
		return fail(http.StatusNotFound, "backup not found"), true
	case errors.Is(err, backup.ErrInvalidID):
		return fail(http.StatusBadRequest, "invalid backup ID: use an ID that opendustpan backup printed"), true
	case errors.Is(err, errNoBackupTarget):
		// No target is a setup problem of the server, not a bad request.
		return fail(http.StatusServiceUnavailable, errNoBackupTarget.Error()+": set --backup-target"), true
	default:
		return fail(0, ""), false
	}
}

// classifyNamespace maps the namespace errors to their status and message: a
// bad namespace name, body, page or cursor answers 400, a duplicate
// namespace or a fenced write 409, an unknown namespace 404, and a store without the paging
// port 501.
func classifyNamespace(err error) (failure, bool) {
	switch {
	case errors.Is(err, namespace.ErrInvalidName):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.NamespaceInvalidName), true
	case errors.Is(err, errInvalidNamespaceBody):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.RequestBodyInvalid), true
	case errors.Is(err, namespace.ErrInvalidPage):
		// The sentence of the sentinel only, as for a fenced write.
		return failCoded(http.StatusBadRequest, namespace.ErrInvalidPage.Error(), errcode.RequestPageSizeInvalid), true
	case errors.Is(err, errNamespaceCursor):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.RequestPageTokenInvalid), true
	case errors.Is(err, namespace.ErrAlreadyExists):
		return failCoded(http.StatusConflict, namespace.ErrAlreadyExists.Error(), errcode.NamespaceAlreadyExists), true
	case errors.Is(err, namespace.ErrFenced):
		// The sentence of the sentinel only: a wrap adds the name of the
		// operation, which the log holds.
		return failCoded(http.StatusConflict, namespace.ErrFenced.Error(), errcode.NamespaceFenced), true
	case errors.Is(err, namespace.ErrNotFound):
		// The fixed text of problem.Namespace: a wrap names the operation and
		// might name the object, and the log holds it.
		return failCoded(http.StatusNotFound, "namespace not found", errcode.NamespaceNotFound), true
	case errors.Is(err, namespace.ErrNoPaging):
		return fail(http.StatusNotImplemented, namespace.ErrNoPaging.Error()), true
	default:
		return fail(0, ""), false
	}
}

// classifyMaintenance maps the maintenance errors to their status and
// message: an invalid state value answers 400, active work answers 409
// naming each update and run, a leave while a change of the audit chain
// holds maintenance mode answers 409, a refused approval of an audit chain
// change answers 409 or, without a fresh MFA step or when a service
// principal approves turning the chain off, 403, an approval while the
// server runs no migration loop answers 503, an enter that the writes in
// flight kept waiting past the bound of the store answers 503, and an
// unreadable or unknown stored state answers 500 without driver text.
func classifyMaintenance(err error) (failure, bool) {
	switch {
	case errors.Is(err, maintenance.ErrInvalidState):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.MaintenanceInvalidState), true
	case errors.Is(err, maintenance.ErrActiveWork):
		return failCoded(http.StatusConflict, err.Error(), errcode.MaintenanceActiveWork, activeWorkArgs(err)...), true
	case errors.Is(err, maintenance.ErrHeld):
		return failCoded(http.StatusConflict, maintenance.ErrHeld.Error(), errcode.MaintenanceHeld), true
	case errors.Is(err, audit.ErrNoChainChange), errors.Is(err, audit.ErrChainMigrating),
		errors.Is(err, audit.ErrChainChangeWaits):
		return fail(http.StatusConflict, err.Error()), true
	case errors.Is(err, audit.ErrChainMigrationsStopped):
		return fail(http.StatusServiceUnavailable, audit.ErrChainMigrationsStopped.Error()), true
	case errors.Is(err, audit.ErrChainStepRequired):
		return fail(http.StatusForbidden, audit.ErrChainStepRequired.Error()), true
	case errors.Is(err, audit.ErrChainNeedsPerson):
		return fail(http.StatusForbidden, audit.ErrChainNeedsPerson.Error()), true
	case errors.Is(err, maintenance.ErrInMaintenance):
		return failCoded(http.StatusServiceUnavailable, maintenance.ErrInMaintenance.Error()+": try again later",
			errcode.MaintenanceInMaintenance), true
	case errors.Is(err, maintenance.ErrBusy):
		return failCoded(http.StatusServiceUnavailable, maintenance.ErrBusy.Error()+": try again later",
			errcode.MaintenanceBusy), true
	case errors.Is(err, maintenance.ErrUnavailable):
		return failCoded(http.StatusInternalServerError, maintenance.ErrUnavailable.Error(),
			errcode.MaintenanceUnavailable), true
	default:
		return fail(0, ""), false
	}
}

// belowMinimumText returns the text of the answer to a write that this binary
// is too old to make, and the versions as the arguments of the code. The text
// names both versions.
func belowMinimumText(err error) (string, []errcode.Value) {
	below, ok := errors.AsType[*schema.MinimumError](err)
	if !ok {
		return schema.ErrBinaryBelowMinimum.Error(), nil
	}

	return below.WriteText(), []errcode.Value{
		errcode.Identifier("binaryVersion", below.BinaryVersion),
		errcode.Identifier("minimumVersion", below.SchemaMinimum),
	}
}

// activeWorkArgs returns the arguments of maintenance.active-work: the number
// of units with active work and the number of units that a forced enter
// canceled. An error that does not carry the units gives zero for both.
func activeWorkArgs(err error) []errcode.Value {
	var work, canceled int

	if blocked, ok := errors.AsType[*maintenance.ActiveWorkError](err); ok {
		work, canceled = len(blocked.Work), len(blocked.Canceled)
	}

	return []errcode.Value{
		errcode.Quantity("workCount", int64(work)),
		errcode.Quantity("canceledCount", int64(canceled)),
	}
}

// unverifiedRefusal returns the text and the arguments of the answer to a
// backup that the bound on the unverified keys refused: the count of the keys
// and the bound. An error that carries no count gives zero for both.
func unverifiedRefusal(err error) (string, []errcode.Value) {
	refused, ok := errors.AsType[*backup.TooManyUnverifiedError](err)
	if !ok {
		return backup.ErrTooManyUnverified.Error(), []errcode.Value{
			errcode.Quantity("count", 0),
			errcode.Quantity("limit", 0),
		}
	}

	// Defense in depth: the refusal text holds a count and a bound and no
	// value of a request. Clean stays so that no control character reaches the
	// answer, also when a later change puts a name in the text.
	return denials.Clean(refused.Error(), maxCauseBytes), []errcode.Value{
		errcode.Quantity("count", int64(refused.Count)),
		errcode.Quantity("limit", int64(refused.Limit)),
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")

	if status >= http.StatusBadRequest {
		// An error answer may tell the state of an object, so no shared cache
		// keeps it.
		w.Header().Set("Cache-Control", "no-store")
	}

	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
