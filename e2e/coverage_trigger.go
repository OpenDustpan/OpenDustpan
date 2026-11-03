//go:build e2e

package e2e

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	triggersapi "opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// coverageTriggerRoutes returns the trigger adapter's route table.
func coverageTriggerRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	manager, err := trigger.NewManager(trigger.ManagerConfig{
		Store: struct{ trigger.Store }{nil}, Dispatcher: struct{ trigger.Dispatcher }{nil},
		Authorizer: coverageAuthorizer{}, Savers: struct{ trigger.SaverCheck }{nil},
		Units: struct{ trigger.Units }{nil}, Fences: struct{ trigger.Fences }{nil}, Now: nil, NewID: nil,
	})
	if err != nil {
		tb.Fatalf("trigger manager: %v", err)
	}

	handler, err := triggersapi.NewHandler(triggersapi.Config{
		Namespaces:     namespaces,
		Workers:        nil,
		Units:          coverageReader{},
		Store:          struct{ trigger.Store }{nil},
		Manager:        manager,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("triggers handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageNoPools is the runner pool port of a handler that the coverage check
// builds only to list its routes.
type coverageNoPools struct{}

func (coverageNoPools) Pool(context.Context, string) (runner.Pool, error) {
	return runner.Pool{}, runner.ErrNotFound
}

func (coverageNoPools) Pools(context.Context, namespace.Name) ([]runner.Pool, error) { return nil, nil }

// triggerAllowEntries covers the routes of the run triggers. No CLI calls them:
// the console and the clients of the API do. The steps of TestRunTriggers call
// each of them with the personal token of a person, on a unit that the real
// OpenTofu CLI made.
func triggerAllowEntries() []AllowEntry {
	const (
		base = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers"
		one  = base + "/{trigger_id}"
	)

	return allowRoutes(
		"TestRunTriggers",
		"Console and client route: no CLI calls it. The steps of TestRunTriggers call it with the token of a person.",
		RouteRef{Method: http.MethodGet, Path: base},
		RouteRef{Method: http.MethodPost, Path: base},
		RouteRef{Method: http.MethodGet, Path: one},
		RouteRef{Method: http.MethodPut, Path: one},
		RouteRef{Method: http.MethodDelete, Path: one},
		RouteRef{Method: http.MethodPost, Path: one + "/fire"},
	)
}
