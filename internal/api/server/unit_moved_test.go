package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/access"
)

// TestUnitMovedIsConflict checks that a grant or a revoke on a unit that
// moved to another collection during the request answers 409, with a
// message that tells the caller to send the request again.
func TestUnitMovedIsConflict(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("create grant: %w: unit %q changed collection during the request; send the request again",
		access.ErrUnitMoved, "pulumi:acme/web/prod")

	status, message := server.ClassifyForTest(err)
	if status != http.StatusConflict {
		t.Fatalf("status of ErrUnitMoved = %d, want %d", status, http.StatusConflict)
	}

	if !strings.Contains(message, "send the request again") {
		t.Fatalf("message of ErrUnitMoved = %q, want it to tell the caller to send the request again", message)
	}
}
