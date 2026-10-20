package inventory_test

import (
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// testViewsConfig returns a saved view config whose store and principal
// directory answer nothing: the route table tests build a handler with it, so
// the table holds the saved view rows.
func testViewsConfig(t *testing.T) *apiinventory.ViewsConfig {
	t.Helper()

	service, err := savedview.NewService(savedview.Config{
		Store: struct{ savedview.Store }{nil}, Namespaces: struct{ savedview.Namespaces }{nil}, Now: nil,
	})
	if err != nil {
		t.Fatalf("saved view service: %v", err)
	}

	return &apiinventory.ViewsConfig{
		Service: service, Principals: struct{ apiinventory.ViewPrincipals }{nil}, Idempotency: nil,
	}
}
