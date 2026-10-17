package dustpan

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/maintenance"
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
// /api/server/maintenance, and the console files always pass. The maintenance
// routes pass on purpose: an operator must be able to clear stale active work
// on a single instance whose setting differs from the stored state. An
// instance whose setting differs from the stored state refuses every other
// request. While a change of the chain waits or migrates, every instance
// refuses every other request, including reads of the audit log. Without a
// chain it returns next.
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
// /api/server/maintenance, and the console files bypass the gate.
func chainGateShape(r *http.Request) (errorShape, bool) {
	path := r.URL.Path

	switch {
	case strings.HasPrefix(path, "/health/"),
		path == "/api/server/metrics" && r.Method == http.MethodGet,
		path == "/api/server/audit/chain" && (r.Method == http.MethodGet || r.Method == http.MethodPost),
		path == "/api/server/maintenance" || strings.HasPrefix(path, "/api/server/maintenance/"):
		return shapePulumi, false
	case strings.HasPrefix(path, "/api/inventory/"):
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
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// writeMaintenancePulumi answers 503 with Retry-After in the Pulumi shape.
func writeMaintenancePulumi(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenancePulumiError{
		Code:    http.StatusServiceUnavailable,
		Message: message,
	}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}

// maintenanceInventoryError is the error body the console expects, as in the
// inventory adapter. It matches the Pulumi shape today, and stays its own
// type: the console contract may change without the CLI one.
type maintenanceInventoryError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// writeMaintenanceInventory answers 503 with Retry-After in the inventory shape.
func writeMaintenanceInventory(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenanceInventoryError{
		Code:    http.StatusServiceUnavailable,
		Message: message,
	}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}

// maintenanceTFEError is one entry of a JSON:API error document, as in the
// TFE-compatible adapter.
type maintenanceTFEError struct {
	Status string `json:"status"`
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
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	http.Error(w, message, http.StatusServiceUnavailable)
}

// writeMaintenanceTFE answers 503 with Retry-After as a JSON:API error
// document. The title names maintenance for callers that show only it.
func writeMaintenanceTFE(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, message string) {
	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenanceTFEDocument{Errors: []maintenanceTFEError{{
		Status: "503",
		Title:  "maintenance",
		Detail: message,
	}}}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}
