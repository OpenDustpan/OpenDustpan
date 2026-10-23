package registry

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/idempotency"
)

// TestWriteErrorLostKey fails when a write that lost its idempotency key,
// also when code wraps the sentinel, does not answer 409 with the code
// request.idempotency-key-lost.
func TestWriteErrorLostKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"sentinel", idempotency.ErrLost},
		{"wrapped", fmt.Errorf("finish the write: %w", idempotency.ErrLost)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h Handler

			h.logger = slog.New(slog.DiscardHandler)

			rec := httptest.NewRecorder()
			h.writeError(t.Context(), rec, tc.err)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.RequestIdempotencyKeyLost, Args: nil, RequestBody: nil})
		})
	}
}
