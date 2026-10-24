package pulumi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// TestEnforceLeaseWithoutLeaseHandler checks that a lease row without a lease
// handler fails closed with 500 before it resolves the object, even when the
// route test that refuses such a row did not run.
func TestEnforceLeaseWithoutLeaseHandler(t *testing.T) {
	t.Parallel()

	var h Handler

	h.logger = slog.New(slog.DiscardHandler)

	resolved := false
	route := Route{
		Method: http.MethodPost, Path: "/x", Scope: ScopeUpdateLease,
		Actions: []identity.Action{identity.ActionUnitWrite},
		Resolve: func(http.ResponseWriter, *http.Request) (identity.Object, error) {
			resolved = true

			return identity.Object{}, nil
		},
		Serve: nil, Lease: nil,
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody)
	req.Header.Set("Authorization", updateTokenScheme+"x")

	rec := httptest.NewRecorder()
	h.enforceLease(route)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	if resolved {
		t.Fatal("the route resolved its object, want a refusal before Resolve")
	}
}
