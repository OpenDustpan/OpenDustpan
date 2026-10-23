package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errUnclassified is an error that no classify function maps.
var errUnclassified = errors.New("disk on fire")

// TestWriteErrorCodes proves that each error that the shared classify
// functions map answers its status, its code of the catalog and the
// arguments of the code, also when the error is wrapped.
func TestWriteErrorCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
		args   map[string]any
	}{
		{"denied", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied, nil},
		{"unclassified", errUnclassified, http.StatusInternalServerError, errcode.ServerInternal, nil},
	}

	for _, tc := range tests {
		for _, err := range []error{tc.err, fmt.Errorf("serve the route: %w", tc.err)} {
			t.Run(tc.name+"/"+err.Error(), func(t *testing.T) {
				t.Parallel()

				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/x", http.NoBody)
				rec := httptest.NewRecorder()
				apiserver.WriteErrorForTest(rec, r, err)

				if rec.Code != tc.status {
					t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
				}

				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
					errcodetest.Want{Code: tc.code, Args: tc.args, RequestBody: nil})
			})
		}
	}
}

// TestWriteErrorWithoutCode proves that an error that its classify function
// gives no code yet keeps its status and its message, and answers without
// errorCode.
func TestWriteErrorWithoutCode(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/x", http.NoBody)
	rec := httptest.NewRecorder()
	apiserver.WriteErrorForTest(rec, r, audit.ErrChainMigrationsStopped)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}

	if _, ok := body["errorCode"]; ok || rec.Code != http.StatusServiceUnavailable ||
		body["message"] != audit.ErrChainMigrationsStopped.Error() {
		t.Fatalf("answer = %d %s, want 503 with the message and no errorCode", rec.Code, rec.Body.String())
	}
}
