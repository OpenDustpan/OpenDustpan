package tofuhttp_test

import (
	"log/slog"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
)

// TestRoutesReturnsACopy proves Routes lists the four routes of the address
// and that a change to the result does not change the table of the handler.
func TestRoutesReturnsACopy(t *testing.T) {
	t.Parallel()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     newFakeUnits(true),
		Namespaces:     fakeNamespaces{},
		Audit:          newTestAudit(),
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	routes := h.Routes()
	if len(routes) != 4 {
		t.Fatalf("routes = %d, want 4", len(routes))
	}

	want := routes[0].Actions[0]
	routes[0].Actions[0] = "changed"
	routes[0].Path = "/changed"

	again := h.Routes()
	if again[0].Actions[0] != want || again[0].Path == "/changed" {
		t.Fatalf("route table changed through the result of Routes: %+v", again[0])
	}
}
