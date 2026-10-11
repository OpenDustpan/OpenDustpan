package dustpan

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

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

		ctx := r.Context()

		switch shape {
		case shapePulumi:
			writeMaintenancePulumi(ctx, w, logger)
		case shapeInventory:
			writeMaintenanceInventory(ctx, w, logger)
		case shapeTFE:
			writeMaintenanceTFE(ctx, w, logger)
		default:
			// Unreachable: every call site passes a valid shape. Refuse
			// rather than serve, and log the programmer error.
			logger.ErrorContext(ctx, "unknown maintenance error shape", slog.Int("shape", int(shape)))
			writeMaintenancePulumi(ctx, w, logger)
		}
	})
}

// maintenancePulumiError is the error body the Pulumi CLI expects, as in the
// Pulumi adapter.
type maintenancePulumiError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// writeMaintenancePulumi answers 503 with Retry-After in the Pulumi shape.
func writeMaintenancePulumi(ctx context.Context, w http.ResponseWriter, logger *slog.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenancePulumiError{
		Code:    http.StatusServiceUnavailable,
		Message: maintenanceMessage,
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
func writeMaintenanceInventory(ctx context.Context, w http.ResponseWriter, logger *slog.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenanceInventoryError{
		Code:    http.StatusServiceUnavailable,
		Message: maintenanceMessage,
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

// writeMaintenanceTFE answers 503 with Retry-After as a JSON:API error
// document. The title names maintenance for callers that show only it.
func writeMaintenanceTFE(ctx context.Context, w http.ResponseWriter, logger *slog.Logger) {
	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.Header().Set("Retry-After", maintenanceRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)

	doc := maintenanceTFEDocument{Errors: []maintenanceTFEError{{
		Status: "503",
		Title:  "maintenance",
		Detail: maintenanceMessage,
	}}}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logger.WarnContext(ctx, "write maintenance refusal failed", slog.String("error", err.Error()))
	}
}
