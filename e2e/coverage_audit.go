//go:build e2e

package e2e

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	auditapi "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// coverageAuditRoutes returns the audit adapter's route table.
func coverageAuditRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := auditapi.NewHandler(auditapi.Config{
		Namespaces:     namespaces,
		Entries:        coverageAuditStore{},
		EntryReader:    nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		OnAuditFailure: nil, DenialLimiter: nil,
	})
	if err != nil {
		tb.Fatalf("audit handler: %v", err)
	}

	return auditRefs(handler.Routes())
}

// coverageAuditStore answers no entry. It exists only to build a Handler
// for its route table; no request is ever served.
type coverageAuditStore struct{}

func (coverageAuditStore) Append(
	_ context.Context, _ namespace.Fence, _ audit.Entry,
) (audit.Entry, error) {
	var zero audit.Entry

	return zero, nil
}

func (coverageAuditStore) AppendServer(_ context.Context, _ audit.Entry) (audit.Entry, error) {
	var zero audit.Entry

	return zero, nil
}

func (coverageAuditStore) List(
	_ context.Context, _ namespace.Name, _ audit.Page,
) (audit.Listing, error) {
	var zero audit.Listing

	return zero, nil
}

func (coverageAuditStore) Check(_ context.Context, _ namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// auditRefs formats the audit table as method and path pairs.
func auditRefs(routes []auditapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// auditSinkCoveringTest names the test that proves the audit sink list
// instead of a CLI: no CLI reads the delivery state of the audit sinks.
const auditSinkCoveringTest = "TestAuditSinks"

// auditSinkAllowEntries returns the operator route of the audit sinks. The
// harness steps of auditSinkCoveringTest drive it through a server with a
// file sink on each store.
func auditSinkAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/server/audit/sinks",
			CoveringTest: auditSinkCoveringTest,
			Reason: "Operator route: no CLI lists the audit sinks. The harness " +
				"step starts a server with a file sink, writes one audited change " +
				"and reads the list.",
		},
	}
}

// auditReadAllowEntries returns the allowlist rows for the audit read
// routes, which only the console and audit sink receivers call.
func auditReadAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/audit/entries",
			CoveringTest: "TestCLIs (checkAuditLog)",
			Reason: "Console-only: no CLI reads the audit entries. The " +
				"harness step lists them after a CLI command that calls the " +
				"decrypt audit route and checks the chain.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/entries/{sequence}",
			CoveringTest: "TestCLIs (checkAuditLog), TestServerAuditScope",
			Reason: "Console-only and for audit sink receivers: no CLI reads " +
				"one audit entry. The harness steps read an entry of the " +
				"namespace and of @server by its sequence.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/check",
			CoveringTest: "TestCLIs (checkAuditLog)",
			Reason: "Console-only: no CLI checks the audit chain. The " +
				"harness step checks it after a CLI command that calls the " +
				"decrypt audit route.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/export",
			CoveringTest: "TestCLIs (checkAuditExport)",
			Reason: "Console-only and for auditors: no CLI exports the audit " +
				"log. The harness step exports the log after a CLI command " +
				"that calls the decrypt audit route and checks the hashes " +
				"of the lines with no help from the server.",
		},
	}
}

// auditChainCoveringTest names the test that proves the audit chain routes
// instead of a CLI: no tool CLI calls them.
const auditChainCoveringTest = "TestAuditChain"

// auditChainAllowEntries returns the operator audit chain routes that no
// tool CLI calls. The steps of auditChainCoveringTest restart the server
// binary with each setting, read the waiting change and approve it.
func auditChainAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/server/audit/chain",
			CoveringTest: auditChainCoveringTest,
			Reason: "Operator route: no tool CLI reads the audit chain. " +
				"The harness step reads the waiting change after each restart.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/audit/chain",
			CoveringTest: auditChainCoveringTest,
			Reason: "Operator route: no tool CLI approves an audit chain change. " +
				"The harness step approves the change to off, then to on.",
		},
	}
}
