package dustpan

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestSDKAnswersCarryACode fails for an error answer of the SDK that has no
// code of the catalog, the wrong status, or an argument.
func TestSDKAnswersCarryACode(t *testing.T) {
	t.Parallel()

	console := t.TempDir()
	if err := os.WriteFile(filepath.Join(console, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	files, err := consoleHandler(console)
	if err != nil {
		t.Fatalf("console handler: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name   string
		method string
		serve  func(w http.ResponseWriter, r *http.Request)
		status int
		code   string
	}{
		{"router 404", http.MethodGet, v1NotFound, http.StatusNotFound, errcode.ServerRouteNotFound},
		{"component 404", http.MethodGet, componentNotFound, http.StatusNotFound, errcode.ServerComponentNotFound},
		{
			"readiness of a component that does not run", http.MethodGet,
			func(w http.ResponseWriter, r *http.Request) {
				r.SetPathValue("component", "runner")
				(&readiness{checks: nil, timeout: 0, failures: nil, logger: nil, parts: nil}).ServeOne(w, r)
			},
			http.StatusNotFound, errcode.ServerComponentNotFound,
		},
		{
			"console method",
			http.MethodPost,
			files.ServeHTTP,
			http.StatusMethodNotAllowed,
			errcode.ServerMethodNotAllowed,
		},
		{
			"maintenance, Pulumi shape", http.MethodGet,
			func(w http.ResponseWriter, r *http.Request) {
				writeMaintenancePulumi(r.Context(), w, logger, "in maintenance")
			},
			http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance,
		},
		{
			"maintenance, inventory shape", http.MethodGet,
			func(w http.ResponseWriter, r *http.Request) {
				writeMaintenanceInventory(r.Context(), w, logger, "in maintenance")
			},
			http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			tc.serve(rec, httptest.NewRequestWithContext(t.Context(), tc.method, "/x", http.NoBody))

			checkSDKAnswer(t, rec, tc.status, tc.code)
		})
	}
}

// TestMaintenanceTFEAnswerCarriesACode fails when the JSON:API refusal of
// maintenance mode has no code member.
func TestMaintenanceTFEAnswerCarriesACode(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeMaintenanceTFE(t.Context(), rec, slog.New(slog.DiscardHandler), "in maintenance")

	var doc tfeDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Errors) != 1 {
		t.Fatalf("body = %q, %v", rec.Body.String(), err)
	}

	if doc.Errors[0].Status != "503" || doc.Errors[0].Code != errcode.MaintenanceInMaintenance {
		t.Errorf("error = %+v", doc.Errors[0])
	}
}

// tfeDoc is the part of the JSON:API refusal that the test reads.
type tfeDoc struct {
	Errors []tfeEntry `json:"errors"`
}

// tfeEntry is one entry of tfeDoc.
type tfeEntry struct {
	Status string `json:"status"`
	Code   string `json:"code"`
}

// sdkBody is the error body that the SDK answers by itself.
type sdkBody struct {
	Code      int            `json:"code"`
	Message   string         `json:"message"`
	ErrorCode string         `json:"errorCode"`
	ErrorArgs map[string]any `json:"errorArgs"`
}

// checkSDKAnswer fails the test unless rec holds the answer of status with
// the catalog code, a message and no argument.
func checkSDKAnswer(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	var got sdkBody

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v: %q", err, rec.Body.String())
	}

	if rec.Code != status || got.Code != status || got.ErrorCode != code {
		t.Errorf("status = %d, code = %d, errorCode = %q; want %d, %d, %q",
			rec.Code, got.Code, got.ErrorCode, status, status, code)
	}

	if got.Message == "" || got.ErrorArgs != nil {
		t.Errorf("message = %q, errorArgs = %v", got.Message, got.ErrorArgs)
	}

	if _, ok := errcode.Lookup(got.ErrorCode); !ok {
		t.Errorf("errorCode %q is not in the catalog", got.ErrorCode)
	}
}
