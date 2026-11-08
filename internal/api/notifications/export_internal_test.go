package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/pkg/notify"
)

// ClassifyText returns the text that the API answers for err, so the external
// tests reach the error mapping without a request.
func ClassifyText(err error) string {
	return problem.Writer{Logger: nil, KeepDenied: nil, Classify: sinkErrors}.Problem(err).Message
}

// ClassifyCode returns the errorCode that the API answers for err.
func ClassifyCode(err error) string {
	return problem.Writer{Logger: nil, KeepDenied: nil, Classify: sinkErrors}.Problem(err).Code
}

// DocumentJSON returns the JSON that the API answers for s, so the external
// tests reach the answer of a sink that the store would not read. It writes
// the diagnostics of the conversion to no destination.
func DocumentJSON(s *notify.Sink) ([]byte, error) {
	return DocumentJSONTo(slog.New(slog.DiscardHandler), s)
}

// DocumentJSONTo returns the JSON that the API answers for s and writes the
// diagnostics of the conversion to logger.
func DocumentJSONTo(logger *slog.Logger, s *notify.Sink) ([]byte, error) {
	var h Handler

	h.logger = logger

	b, err := json.Marshal(h.toDocument(s))
	if err != nil {
		return nil, fmt.Errorf("marshal document: %w", err)
	}

	return b, nil
}

// WriteError writes the answer of err as a handler of the API does, so the
// external tests check the code and the arguments of an error that no request
// of the test reaches.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}

// WriteSubscriptionError is WriteError for the subscription routes.
func WriteSubscriptionError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeSubscriptionError(ctx, w, err)
}

// WriteDeliveryError is WriteError for the redelivery route.
func WriteDeliveryError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeDeliveryError(ctx, w, err)
}
