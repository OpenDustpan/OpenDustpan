package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetJSONShowsTheMessageOfTheServer checks that an error answer gives the
// message of the server and not only the status line.
func TestGetJSONShowsTheMessageOfTheServer(t *testing.T) {
	t.Parallel()

	const message = "no backup target is configured: set --backup-target"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)

		if _, err := w.Write([]byte(`{"code":503,"message":"` + message + `"}`)); err != nil {
			t.Errorf("write the test answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	var doc backupListDocument

	err := getJSON(t.Context(), srv.Client(), srv.URL, "test-token", &doc)
	if err == nil {
		t.Fatal("getJSON error = nil, want ErrBackupRead")
	}

	if !errors.Is(err, ErrBackupRead) {
		t.Fatalf("getJSON error = %v, want ErrBackupRead", err)
	}

	if !strings.Contains(err.Error(), message) {
		t.Fatalf("getJSON error = %q, want it to contain %q", err, message)
	}
}
