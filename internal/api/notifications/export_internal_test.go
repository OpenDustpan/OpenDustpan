package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/notify"
)

// ClassifyText returns the text that the API answers for err, so the external
// tests reach the error mapping without a request.
func ClassifyText(err error) string { return classify(err).message }

// ClassifyCode returns the errorCode that the API answers for err.
func ClassifyCode(err error) string { return classify(err).code }

// DocumentJSON returns the JSON that the API answers for s, so the external
// tests reach the answer of a sink that the store would not read.
func DocumentJSON(s *notify.Sink) ([]byte, error) {
	b, err := json.Marshal(toDocument(s))
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
