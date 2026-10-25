package triggers

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
)

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
		problem.Writer{Logger: logger, Classify: classify}.Write(ctx, w, err)

		return
	}

	h.writeError(ctx, w, err)
}
