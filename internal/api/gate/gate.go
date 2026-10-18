// Package gate is the HTTP adapter of the policy-model spike: one route
// that reads a decision record. The server does not mount it. A caller
// passes the authorization check, so the route never answers without one.
package gate

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/gate"
)

// Reader reads decision records.
type Reader interface {
	Get(ctx context.Context, id string) (gate.Record, error)
}

// Authorize decides whether the request may read the record. It returns
// false for a refusal.
type Authorize func(r *http.Request, rec gate.Record) bool

// RoutePattern is the pattern of the route.
const RoutePattern = "GET /spike/v1/decisions/{id}"

// Handler returns the handler of RoutePattern.
func Handler(log *slog.Logger, records Reader, allow Authorize) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec, err := records.Get(r.Context(), r.PathValue("id"))

		switch {
		case errors.Is(err, gate.ErrNotFound):
			writeError(w, http.StatusNotFound, "Decision record not found.")

			return
		case err != nil:
			log.ErrorContext(r.Context(), "read decision record", slog.Any("error", err))
			writeError(w, http.StatusInternalServerError, "Decision record could not be read.")

			return
		case !allow(r, rec):
			// The same answer as a missing record, so an ID reveals nothing.
			writeError(w, http.StatusNotFound, "Decision record not found.")

			return
		}

		w.Header().Set("Content-Type", "application/json")

		err = json.NewEncoder(w).Encode(rec)
		if err != nil {
			log.ErrorContext(r.Context(), "write decision record", slog.Any("error", err))
		}
	})
}

// errorBody is the error shape of the API: {"code": <status>, "message": "..."}.
type errorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// The status line is sent: nothing else can report a failed write.
	_ = json.NewEncoder(w).Encode(errorBody{Code: status, Message: message})
}
