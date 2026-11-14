package dustpan

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// maintenanceRetryAfter answers how long a tool waits before it retries a
// call refused in maintenance mode, in seconds. The restore that entered
// maintenance mode still runs; the tool retries after it ends.
const maintenanceRetryAfter = "30"

// maintenanceMessage names maintenance in every refused tool call. Each
// adapter renders it in its own error shape, so every tool shows it.
const maintenanceMessage = "The installation is in maintenance mode. Retry later."

// errorShape is how one tool adapter renders the maintenance refusal.
type errorShape int

const (
	// shapePulumi renders {"code":503,"message":"..."} as the Pulumi CLI expects.
	shapePulumi errorShape = iota + 1
	// shapeInventory renders {"code":503,"message":"..."} as the console expects.
	shapeInventory
	// shapeTFE renders a JSON:API error document as OpenTofu expects.
	shapeTFE
	// shapeHTTPBackend renders plain text, as the OpenTofu http backend shows it.
	shapeHTTPBackend
	// shapeRunner renders {"code":"unavailable","message":"..."} as a runner
	// expects, and so retries after the Retry-After time.
	shapeRunner
)

// withMaintenance refuses tool calls with 503 while the installation is in
// maintenance mode (ADR 38 decision 4). Each refusal carries Retry-After and
// a message that names maintenance, in the error shape of its adapter. The
// installation API and the probes never pass through it: routes wires it
// around the tool adapters only.
//
// The gate runs before authentication on purpose: every tool call answers
// the same refusal with or without credentials, so CLIs show the
// maintenance message instead of an auth error. An unauthenticated caller
// can tell maintenance apart from normal operation; that signal carries no
// data, only the installation state.
func withMaintenance(
	next http.Handler, watcher *maintenance.Watcher, shape errorShape, logger *slog.Logger,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if watcher.State(r.Context()) != maintenance.StateMaintenance {
			next.ServeHTTP(w, r)

			return
		}

		writeRefusal(r.Context(), w, shape, logger, maintenanceMessage)
	})
}

// runnerAPIPrefix is the path prefix of the runner management API. The
// gate of that API lets the routes of the list under this prefix pass.
const runnerAPIPrefix = "/api/runners/"

// maintenanceAPIPath is the path of the maintenance routes of the
// installation API. They stay available while the audit chain gate refuses
// the other tool-neutral routes, so an operator can clear stale active work.
const maintenanceAPIPath = "/api/dustpan/v1/maintenance"

// newReducingRunnerRoutes returns the routes of the runner management API
// that pass the maintenance gate: the routes of maintenance.ReducingRoutes
// under the prefix of that API, which today are the retire of a runner and
// the revoke of a join token. They only reduce access, so incident response
// can cut a runner at any time. Every other route of the API stays refused.
func newReducingRunnerRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	for _, route := range maintenance.ReducingRoutes() {
		if strings.HasPrefix(route.Pattern, runnerAPIPrefix) {
			mux.Handle(route.Method+" "+route.Pattern, noop)
		}
	}

	return mux
}

// isReducingRunnerRoute reports whether r is one of routes. Defense in depth:
// the store journals the write and the handler checks the grants, so this
// gate does not rely on them. It refuses a path that is not in canonical
// form, so that a redirect or a rewrite cannot reach a route that the table
// does not name.
func isReducingRunnerRoute(routes *http.ServeMux, r *http.Request) bool {
	// A RawPath is set only when the request path has an escape that the
	// default encoding of Path would not write, such as %2F or %2e. A route
	// of the table has none, so such a request is refused here.
	if r.URL.RawPath != "" || !canonicalPath(r.URL.Path) {
		return false
	}

	_, pattern := routes.Handler(r)

	return pattern != ""
}

// canonicalPath reports whether p is an absolute path with no empty segment
// and no "." or ".." segment.
func canonicalPath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/")
	if !ok {
		return false
	}

	for segment := range strings.SplitSeq(rest, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}

	return true
}

// withRunnerMaintenance is withMaintenance for the runner management API: a
// request for a reducing route passes in maintenance mode, and every other
// request answers 503.
func withRunnerMaintenance(
	next http.Handler, watcher *maintenance.Watcher, shape errorShape, logger *slog.Logger,
) http.Handler {
	gated := withMaintenance(next, watcher, shape, logger)
	routes := newReducingRunnerRoutes()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isReducingRunnerRoute(routes, r) {
			next.ServeHTTP(w, r)

			return
		}

		gated.ServeHTTP(w, r)
	})
}

// writeRefusal answers 503 with Retry-After and message, in the error shape
// of one adapter.
func writeRefusal(ctx context.Context, w http.ResponseWriter, shape errorShape, logger *slog.Logger, message string) {
	switch shape {
	case shapePulumi:
		writeMaintenancePulumi(ctx, w, logger, message)
	case shapeInventory:
		writeMaintenanceInventory(ctx, w, logger, message)
	case shapeTFE:
		writeMaintenanceTFE(ctx, w, logger, message)
	case shapeHTTPBackend:
		writeMaintenanceHTTPBackend(w, message)
	case shapeRunner:
		writeMaintenanceRunner(ctx, w, logger, message)
	default:
		// Unreachable: every call site passes a valid shape. Refuse
		// rather than serve, and log the programmer error.
		logger.ErrorContext(ctx, "unknown maintenance error shape", slog.Int("shape", int(shape)))
		writeMaintenancePulumi(ctx, w, logger, message)
	}
}

// withAuditChainGate refuses requests while the audit chain of this
// instance does not agree with the stored state, in the error shape of the
// adapter of each path. The probes under /health/, GET /api/server/metrics,
// GET and POST /api/server/audit/chain, the paths under
// /api/dustpan/v1/maintenance, and the console files always pass. The
// maintenance routes pass on purpose: an operator must be able to clear stale
// active work on a single instance whose setting differs from the stored
// state. An instance whose setting differs from the stored state refuses
// every other request. While a change of the chain waits or migrates, every
// instance refuses every other request, including reads of the audit log.
// Without a chain it returns next.
func withAuditChainGate(next http.Handler, chain *auditChain, logger *slog.Logger) http.Handler {
	if chain == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shape, gated := chainGateShape(r)
		if !gated {
			next.ServeHTTP(w, r)

			return
		}

		rec := chain.current(r.Context())

		switch {
		case chain.setting.State() != rec.State:
			writeRefusal(r.Context(), w, shape, logger, chainMismatchMessage)
		case rec.Pending:
			message := chainPendingMessage
			running, err := chain.migrationRunning(r.Context())
			if err != nil {
				logger.WarnContext(r.Context(), "audit chain gate could not list migrations",
					slog.String("error", err.Error()))
			} else if running {
				message = chainMigratingMessage
			}

			writeRefusal(r.Context(), w, shape, logger, message)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// chainGateShape answers whether the audit chain gate covers the request,
// and the error shape of its adapter. The probes under /health/, GET
// /api/server/metrics, GET and POST /api/server/audit/chain, the paths under
// /api/dustpan/v1/maintenance, and the console files bypass the gate.
func chainGateShape(r *http.Request) (errorShape, bool) {
	path := r.URL.Path

	switch {
	case strings.HasPrefix(path, "/health/"),
		path == metricsPath && r.Method == http.MethodGet,
		path == "/api/server/audit/chain" && (r.Method == http.MethodGet || r.Method == http.MethodPost),
		path == maintenanceAPIPath || strings.HasPrefix(path, maintenanceAPIPath+"/"):
		return shapePulumi, false
	case strings.HasPrefix(path, "/runner/v1/"):
		return shapeRunner, true
	case strings.HasPrefix(path, "/api/inventory/"), strings.HasPrefix(path, v1Prefix),
		strings.HasPrefix(path, "/api/runners/"):
		// The backup and restore routes must stay in the bypass cases above
		// when they move under v1Prefix: an operator reads and restores a
		// backup while the gate refuses the tool-neutral routes.
		return shapeInventory, true
	case strings.HasPrefix(path, "/api/v2/"), path == "/.well-known/terraform.json":
		return shapeTFE, true
	case strings.HasPrefix(path, "/tofu/"):
		return shapeHTTPBackend, true
	case strings.HasPrefix(path, "/api/"):
		return shapePulumi, true
	default:
		// The console files: the console shows the approval.
		return shapePulumi, false
	}
}

// maintenancePulumiError is the error body the Pulumi CLI expects, as in the
// Pulumi adapter.
type maintenancePulumiError struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
}

// writeMaintenancePulumi answers 503 with Retry-After in the Pulumi shape.
func writeMaintenancePulumi(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenancePulumiError{
		Code:      http.StatusServiceUnavailable,
		Message:   message,
		ErrorCode: errcode.MaintenanceInMaintenance,
	}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}

// maintenanceInventoryError is the error body the console expects, as in the
// inventory adapter. It matches the Pulumi shape today, and stays its own
// type: the console contract may change without the CLI one.
type maintenanceInventoryError struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
}

// writeMaintenanceInventory answers 503 with Retry-After in the inventory shape.
func writeMaintenanceInventory(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenanceInventoryError{
		Code:      http.StatusServiceUnavailable,
		Message:   message,
		ErrorCode: errcode.MaintenanceInMaintenance,
	}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}

// maintenanceTFEError is one entry of a JSON:API error document, as in the
// TFE-compatible adapter.
type maintenanceTFEError struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// maintenanceTFEDocument is the error shape OpenTofu reads.
type maintenanceTFEDocument struct {
	Errors []maintenanceTFEError `json:"errors"`
}

// writeMaintenanceHTTPBackend answers 503 with Retry-After and the message as
// plain text, the shape that the OpenTofu http backend shows.
func writeMaintenanceHTTPBackend(w http.ResponseWriter, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	http.Error(w, message, http.StatusServiceUnavailable)
}

// writeMaintenanceTFE answers 503 with Retry-After as a JSON:API error
// document. The title names maintenance for callers that show only it.
func writeMaintenanceTFE(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, message string) {
	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenanceTFEDocument{Errors: []maintenanceTFEError{{
		Status: "503",
		Code:   errcode.MaintenanceInMaintenance,
		Title:  "maintenance",
		Detail: message,
	}}}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}

// maintenanceRunnerError is the error body of the runner protocol, as in the
// runner adapter.
type maintenanceRunnerError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeMaintenanceRunner answers 503 with Retry-After in the shape of the
// runner protocol.
func writeMaintenanceRunner(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenanceRunnerError{Code: protocol.ErrorUnavailable, Message: message}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}
