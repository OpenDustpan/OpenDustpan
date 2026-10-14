package tfe_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestUploadStateTooLargeNamesFlag proves a state upload over the limit
// answers 413 with a JSON:API body that names the flag.
func TestUploadStateTooLargeNamesFlag(t *testing.T) {
	t.Parallel()

	limitErr := fmt.Errorf(
		"%w: state is at least %d bytes, over the limit of %d bytes: set --max-state-bytes to raise the limit",
		workspace.ErrTooLarge,
		1025,
		1024,
	)

	setup := newUploadSetup(t, func(
		_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
	) (workspace.StateVersion, error) {
		return workspace.StateVersion{}, limitErr
	})

	rec := httptest.NewRecorder()
	setup.handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+setup.reveal, "state"),
	)

	checkJSONAPIError(t, rec, http.StatusRequestEntityTooLarge)

	if !strings.Contains(rec.Body.String(), "--max-state-bytes") {
		t.Fatalf("413 body lacks --max-state-bytes: %s", rec.Body.String())
	}
}
