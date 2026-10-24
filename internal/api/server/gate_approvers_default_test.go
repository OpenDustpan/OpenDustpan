package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/gate"
)

// TestApproverRefusalWithNoReasonFailsClosed proves that an approver error
// with no known reason answers 500 with server.internal and the text
// "internal error", never a 422 with a code of a reason, and never the text
// of the error. Only a bug makes such an error.
func TestApproverRefusalWithNoReasonFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "sentinel with no approver error", err: fmt.Errorf("put rule: %w", gate.ErrApprovers)},
		{name: "approver error with no reason", err: &gate.ApproversError{Reason: 0, Count: 0, People: 0}},
		{
			name: "approver error with an unknown reason",
			err: fmt.Errorf("put rule: %w",
				&gate.ApproversError{Reason: gate.ApproversReason(99), Count: 1, People: 1}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/server/x", http.NoBody)
			rec := httptest.NewRecorder()
			apiserver.WriteErrorForTest(rec, r, tt.err)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusInternalServerError, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.ServerInternal, Args: nil, RequestBody: nil})

			var body struct {
				Message string `json:"message"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %s: %v", rec.Body.String(), err)
			}

			if body.Message != "internal error" {
				t.Fatalf("message = %q, want %q", body.Message, "internal error")
			}
		})
	}
}
