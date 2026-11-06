//go:build e2e

package e2e

import (
	"log/slog"
	"net/http"
	"testing"

	settingsapi "opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// coverageSettingsRoutes returns the unit settings adapter's route table.
func coverageSettingsRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	service, err := unitsettingssvc.New(unitsettingssvc.Config{
		Store: struct{ unitsettings.Store }{nil}, Fences: struct{ unitsettingssvc.Fences }{nil},
		Connections: struct{ unitsettingssvc.Connections }{nil}, Pools: coverageNoPools{},
		Now: nil,
	})
	if err != nil {
		tb.Fatalf("unit settings service: %v", err)
	}

	handler, err := settingsapi.NewHandler(settingsapi.Config{
		Namespaces:     namespaces,
		Units:          coverageReader{},
		Service:        service,
		Suggestions:    nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		Idempotency:    nil,
		Records:        nil,
	})
	if err != nil {
		tb.Fatalf("unit settings handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for i := range routes {
		route := &routes[i]
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// unitSettingsAllowEntries covers the routes of the settings of a unit. No CLI
// calls them: the console and the clients of the API do. The steps of
// TestUnitSettingsAPI call each of them with the token of a person, on a unit
// that the real OpenTofu CLI made.
func unitSettingsAllowEntries() []AllowEntry {
	const (
		unit = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings"
		base = "/api/dustpan/v1/namespaces/{namespace}"
	)

	return allowRoutes(
		"TestUnitSettingsAPI",
		"Console and client route: no CLI calls it. The steps of TestUnitSettingsAPI call it with the token of a person.",
		RouteRef{Method: http.MethodGet, Path: unit},
		RouteRef{Method: http.MethodPut, Path: unit},
		RouteRef{Method: http.MethodDelete, Path: unit},
		RouteRef{Method: http.MethodGet, Path: unit + "/suggestions"},
		RouteRef{Method: http.MethodGet, Path: base + "/settings/unit-defaults"},
		RouteRef{Method: http.MethodPut, Path: base + "/settings/unit-defaults"},
		RouteRef{Method: http.MethodPost, Path: base + "/unit-settings/bulk-put"},
	)
}
