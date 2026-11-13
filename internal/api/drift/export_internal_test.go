package drift

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/pkg/identity"
)

// CheckAddress runs the address check of h on r for p.
func CheckAddress(ctx context.Context, h *Handler, r *http.Request, p identity.Principal) error {
	return h.checkAddress(ctx, r, p)
}

// WriteError answers err as a handler does, with a logger that discards
// every line.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	WriteErrorTo(ctx, w, slog.New(slog.DiscardHandler), nil, err)
}

// WriteErrorTo answers err as a handler does, with logger. A non-nil classify
// stands in for the classifier of the handler, so a test can answer an error
// with a code that the catalog refuses.
func WriteErrorTo(
	ctx context.Context,
	w http.ResponseWriter,
	logger *slog.Logger,
	classify problem.Classifier,
	err error,
) {
	var h Handler

	h.logger = logger
	if classify != nil {
		problem.Writer{Logger: logger, KeepDenied: nil, Classify: classify}.Write(ctx, w, err)

		return
	}

	h.writeError(ctx, w, err)
}

// ErrNotFound is the not-found answer of the drift API, for a test of its
// precedence.
var ErrNotFound = errNotFound

// ErrBody is the error of a request body that is not valid, for a test of its
// precedence.
var ErrBody = errBody

// AuditReason returns the audit reason of err for a refused start, and false
// for an error that writes no entry.
func AuditReason(err error) (string, bool) {
	return refusalReason(err)
}
